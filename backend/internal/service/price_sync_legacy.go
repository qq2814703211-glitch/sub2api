package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A legacy V1 row can carry cny_official even when the selected upstream row is
// not a CNY tariff. The marker below is V2-owned state: it records why such a
// price is withheld from settlement while keeping every original numeric field.
// A later verified quote or an operator card re-renders the row and removes it.
const priceSyncLegacyField = "glory_price_legacy"

type priceSyncLegacyStatus struct {
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	Evidence    string    `json:"evidence,omitempty"`
	RatesDigest string    `json:"rates_sha256,omitempty"`
	Protected   bool      `json:"protected,omitempty"`
	DetectedAt  time.Time `json:"detected_at,omitempty"`
}

const (
	priceSyncLegacyQuarantined = "quarantined"
	priceSyncLegacyPending     = "pending_review"
)

const priceSyncLegacyReason = "历史自动价缺少同 SKU 原币凭证：来源把该行标为 USD 估值，不能继续按人民币计费；待同地区同 SKU 原币报价或人工核验"

// The collector's evidence slot is a string. Carry a structured witness, not a
// vendor or rate inferred by searching its human-readable description.
type legacyEstimate struct {
	Provider string             `json:"provider"`
	SKU      string             `json:"sku"`
	Evidence string             `json:"evidence"`
	Rates    map[string]float64 `json:"rates"`
}

func (e legacyEstimate) encode() string { b, _ := json.Marshal(e); return string(b) }

// legacyCNYUpstreamEstimates mirrors the legacy CNY selection order (non-plan
// vendors first, then vendor id) and reports exact SKUs whose winning row sits
// on a non-USD billing endpoint without a model-level currency declaration.
//
// This is the source's documented currency contract, not a vendor-name or
// amount heuristic: an endpoint that bills in CNY but whose model cost omits
// `currency` carries an upstream USD estimate. A declared CNY cost or a
// currency_options.CNY branch is a native quote and is never reported here.
func legacyCNYUpstreamEstimates(registry map[string]cnyOfficialRegistryProvider, evidence string) map[string]string {
	out := map[string]string{}
	if len(registry) == 0 {
		return out
	}
	vendors := make([]string, 0, len(registry))
	for vendorID := range registry {
		vendors = append(vendors, vendorID)
	}
	sort.Slice(vendors, func(i, j int) bool {
		planI, planJ := isCNYOfficialPlanVendor(vendors[i]), isCNYOfficialPlanVendor(vendors[j])
		if planI != planJ {
			return !planI
		}
		return vendors[i] < vendors[j]
	})
	seen := map[string]bool{}
	for _, vendorID := range vendors {
		provider := registry[vendorID]
		if !isCNYOfficialProvider(vendorID, provider) {
			continue
		}
		for _, sourceID := range sortedStrings(provider.Models) {
			model := normalizeModelPricingSyncModelName(sourceID)
			if model == "" || seen[model] {
				continue
			}
			cost := provider.Models[sourceID].Cost
			if cost == nil || (cnyOfficialDeref(cost.Input) <= 0 && cnyOfficialDeref(cost.Output) <= 0) {
				continue
			}
			// The first usable row in legacy order is the one a V1 sync could
			// have written; later rows are not evidence about that value.
			seen[model] = true
			declared := strings.TrimSpace(cost.Currency)
			if _, cny := cnyOfficialCurrencyOptionForCNY(cost.CurrencyOptions); cny || strings.EqualFold(declared, "CNY") {
				continue
			}
			// A USD endpoint's numbers are a USD tariff, not an estimate. Only a
			// non-USD endpoint without a model currency is the documented
			// upstream USD-estimate case.
			if declared != "" || provider.Currency == "" || strings.EqualFold(provider.Currency, "USD") {
				continue
			}
			if provider.ID != vendorID || provider.Models[sourceID].ID != sourceID || strings.TrimSpace(sourceID) != sourceID {
				continue
			}
			rates := map[string]float64{}
			for field, value := range map[string]*float64{"input_cost_per_token": cost.Input, "output_cost_per_token": cost.Output, "cache_read_input_token_cost": cost.CacheRead, "cache_creation_input_token_cost": cost.CacheWrite} {
				if value != nil && *value > 0 {
					rates[field] = *value / 1e6
				}
			}
			out[model] = (legacyEstimate{Provider: vendorID, SKU: sourceID, Rates: rates, Evidence: fmt.Sprintf("%s %s/%s: upstream USD estimate (endpoint %s; no model currency)", evidence, vendorID, sourceID, provider.Currency)}).encode()
		}
	}
	return out
}

// decodeCNYRegistry re-reads the raw provider map with the CNY registry shape.
// It is used only for the non-USD endpoint currency contract; malformed rows
// are skipped, exactly like the V1 dataset builder.
func decodeCNYRegistry(providers map[string]json.RawMessage) map[string]cnyOfficialRegistryProvider {
	out := make(map[string]cnyOfficialRegistryProvider, len(providers))
	for vendorID, raw := range providers {
		var provider cnyOfficialRegistryProvider
		if json.Unmarshal(raw, &provider) != nil || provider.Models == nil {
			continue
		}
		if strings.TrimSpace(provider.ID) == "" {
			provider.ID = vendorID
		}
		out[vendorID] = provider
	}
	return out
}

// priceSyncLegacyRates extracts the managed numeric components of an override
// row. Metadata and the cny_official flag are deliberately excluded: they are
// not price values and must never be treated as evidence by themselves.
func priceSyncLegacyRates(fields map[string]json.RawMessage) map[string]float64 {
	rates := map[string]float64{}
	for field, raw := range fields {
		billable := priceSyncManagedField(field) || tariffTokenField(field) ||
			field == "cache_storage_cost_per_token_per_hour" || field == "cost_per_request" || field == "cost_per_second"
		if !billable || field == "cny_official" || field == priceSyncLegacyField ||
			field == "glory_billing_mode" || field == "glory_price_tiers" || field == "glory_price_windows" || field == "glory_price_source" {
			continue
		}
		var value float64
		if json.Unmarshal(raw, &value) == nil {
			rates[field] = value
		}
	}
	return rates
}

func priceSyncLegacyRatesDigest(rates map[string]float64) string {
	fields := make([]string, 0, len(rates))
	for field := range rates {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	h := sha256.New()
	for _, field := range fields {
		fmt.Fprintf(h, "%s=%x\n", field, rates[field])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func decodePriceSyncLegacyMarker(raw json.RawMessage) *priceSyncLegacyStatus {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var status priceSyncLegacyStatus
	if json.Unmarshal(raw, &status) != nil || status.Status == "" {
		return &priceSyncLegacyStatus{Status: priceSyncLegacyPending, Reason: "历史价格隔离标记无法解析，保留待核"}
	}
	return &status
}

func ensurePriceSyncModel(h *priceSyncRevision, key string) *priceSyncModel {
	m := h.Models[key]
	if m == nil {
		m = &priceSyncModel{Mode: "auto"}
		h.Models[key] = m
	}
	return m
}

// Only the cross-source reader can establish ownership. A source-filtered history
// is never a fallback, even for an equal amount.
type latestPricingFields interface {
	ListLatestPricingFields(context.Context, []string) ([]ModelPricingSyncHistory, error)
}

type legacyDecision struct {
	issue, usd string
	manual     bool
}

func legacyFieldDecision(key string, rates map[string]float64, rows map[string]ModelPricingSyncHistory, estimate legacyEstimate, native bool, quote PriceSyncQuote) legacyDecision {
	d := legacyDecision{}
	var unknown, verified []string
	batches := map[time.Time]bool{}
	for _, f := range sortedStrings(rates) {
		r, ok := rows[f]
		if !ok || r.Model != key || r.Field != f || !modelPricingValueEqual(r.NewValue, rates[f]) {
			unknown = append(unknown, f+": 缺少同步变更史或金额已变")
			continue
		}
		if r.Reason == "按运营手工配置的人民币价写入（manual 优先级最高）" || r.Reason == "运营已从 manual 条目移除该计费项，按配置删除覆盖层旧值" {
			d.manual = true
			unknown = append(unknown, f+": 最后写入为人工")
			continue
		}
		if r.Source == ModelPricingSyncSourceModelsDev && (r.Reason == modelPricingSyncReasonMissing || r.Reason == modelPricingSyncReasonDrift) {
			if native {
				verified = append(verified, f+": models.dev USD")
			}
			continue
		}
		if r.Source == ModelPricingSyncSourceCNYOfficial {
			if f == cnyOfficialFieldThreshold || f == cnyOfficialFieldInputMult || f == cnyOfficialFieldOutputMult {
				continue
			}
			vendor := estimate.Provider
			if vendor == "" && quote.Model == key && quote.Currency == "CNY" {
				vendor = quote.Provider
			}
			if vendor != "" && (r.Reason == cnyOfficialReason(vendor, 0) || r.Reason == cnyOfficialReason(vendor, 1)) {
				if estimate.Provider == "" {
					batches[r.AppliedAt] = true
					continue
				}
				if v, ok := estimate.Rates[f]; ok && normalizeModelPricingSyncModelName(estimate.SKU) == key && modelPricingValueEqual(v, rates[f]) {
					verified = append(verified, f+": "+estimate.Evidence)
					batches[r.AppliedAt] = true
					continue
				}
			}
		}
		unknown = append(unknown, f+": "+r.Source+" 最后写入原因、厂商或原币未核实")
	}
	for _, f := range []string{cnyOfficialFieldThreshold, cnyOfficialFieldInputMult, cnyOfficialFieldOutputMult} {
		if _, ok := rates[f]; !ok {
			continue
		}
		r := rows[f]
		if r.Source != ModelPricingSyncSourceCNYOfficial {
			continue
		}
		if !batches[r.AppliedAt] || !strings.HasPrefix(r.Reason, "按官方人民币牌价折算长上下文阶梯（") {
			unknown = append(unknown, f+": 阶梯写入归属未核实")
		}
	}
	if len(unknown) > 0 {
		d.issue = strings.Join(unknown, "；") + "；保留原值"
	}
	d.usd = strings.Join(verified, "；")
	return d
}

func (s *ModelPricingSyncService) reconcileLegacyPrices(ctx context.Context, h *priceSyncRevision, c *PriceSyncCollection) (bool, error) {
	candidates := map[string]map[string]json.RawMessage{}
	for k, raw := range h.Entries {
		if _, manual := h.Settings.ManualOverrides[k]; manual {
			continue
		}
		var f map[string]json.RawMessage
		if json.Unmarshal(raw, &f) != nil || f == nil {
			return false, fmt.Errorf("invalid legacy row %q", k)
		}
		if _, managed := f["glory_price_source"]; managed {
			continue
		}
		if len(priceSyncLegacyRates(f)) > 0 {
			candidates[k] = f
		}
	}
	indexed := map[string]map[string]ModelPricingSyncHistory{}
	if reader, ok := s.history.(latestPricingFields); ok && len(candidates) > 0 {
		rows, err := reader.ListLatestPricingFields(ctx, sortedStrings(candidates))
		if err != nil {
			return false, err
		}
		for _, r := range rows {
			if indexed[r.Model] == nil {
				indexed[r.Model] = map[string]ModelPricingSyncHistory{}
			}
			indexed[r.Model][r.Field] = r
		}
	}
	changed := false
	for _, k := range sortedStrings(candidates) {
		f := candidates[k]
		m := ensurePriceSyncModel(h, k)
		before := *m
		rates := priceSyncLegacyRates(f)
		marker := decodePriceSyncLegacyMarker(f[priceSyncLegacyField])
		var e legacyEstimate
		var q PriceSyncQuote
		native := false
		if c != nil {
			_ = json.Unmarshal([]byte(c.LegacyUSDEstimates[k]), &e)
			q = c.Quotes[k]
			native = c.NativeModels[k] || e.Provider != ""
		}
		// A confirmed models.dev last write is already a USD witness before
		// the first fetch. Use the existing site policy for unqualified SKUs.
		// Leave namespaced routes and unproven last writes to normal reconciliation.
		reserved := h.Settings.Enabled && !strings.Contains(k, "/") && priceSyncV2ReservedNativeOwner(k) != ""
		d := legacyFieldDecision(k, rates, indexed[k], e, native || reserved, q)
		if marker != nil && marker.Status == priceSyncLegacyQuarantined && marker.RatesDigest == priceSyncLegacyRatesDigest(rates) && c == nil {
			m.LegacyQuarantine = marker
			m.LegacyAuto = !marker.Protected
			m.External = marker.Protected
			m.NativeCNY = true
			m.Issue = marker.Reason
			if !m.External {
				m.Mode = "auto"
			}
			changed = changed || !priceSyncEqual(before, m)
			continue
		}
		m.External = d.issue != ""
		m.LegacyAuto = !m.External
		m.Mode = "auto"
		if d.manual {
			m.Mode = "manual"
		}
		if native || d.usd != "" {
			m.NativeCNY = true
		}
		if d.usd != "" {
			reason := priceSyncLegacyReason
			if d.issue != "" {
				reason += "；" + d.issue
			}
			written, err := s.quarantineLegacyPriceSync(k, m, h, d.usd, reason, time.Now().UTC())
			if err != nil {
				return false, err
			}
			changed = changed || written
			m.Issue = reason
		} else if marker != nil {
			m.LegacyQuarantine = marker
			m.NativeCNY = true
			m.Issue = marker.Reason
			if d.issue != "" {
				m.Issue += "；" + d.issue
			}
		} else if d.issue != "" {
			m.Issue = d.issue
		}
		changed = changed || !priceSyncEqual(before, m)
	}
	return changed, nil
}

// quarantineLegacyPriceSync withholds a proven legacy USD-estimate row from CNY
// settlement without deleting any original price field. Only the billing
// compatibility flag is removed and V2 provenance is added.
func (s *ModelPricingSyncService) quarantineLegacyPriceSync(key string, m *priceSyncModel, h *priceSyncRevision, evidence, reason string, now time.Time) (bool, error) {
	if m == nil || evidence == "" {
		return false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(h.Entries[key], &fields); err != nil || fields == nil {
		return false, fmt.Errorf("legacy quarantine %s: invalid override row", key)
	}
	if _, managed := fields["glory_price_source"]; managed {
		return false, nil
	}
	rates := priceSyncLegacyRates(fields)
	if len(rates) == 0 {
		return false, nil
	}
	digest := priceSyncLegacyRatesDigest(rates)
	if m.LegacyQuarantine != nil && m.LegacyQuarantine.Status == priceSyncLegacyQuarantined &&
		m.LegacyQuarantine.RatesDigest == digest && m.LegacyQuarantine.Evidence == evidence && m.LegacyQuarantine.Reason == reason && m.LegacyQuarantine.Protected == m.External {
		return false, nil
	}
	delete(fields, "cny_official")
	status, err := json.Marshal(priceSyncLegacyStatus{
		Status: priceSyncLegacyQuarantined, Reason: reason, Protected: m.External,
		Evidence: evidence, RatesDigest: digest, DetectedAt: now.UTC(),
	})
	if err != nil {
		return false, err
	}
	fields[priceSyncLegacyField] = status
	row, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	h.Entries[key] = row
	m.LegacyQuarantine = &priceSyncLegacyStatus{
		Status: priceSyncLegacyQuarantined, Reason: reason, Protected: m.External,
		Evidence: evidence, RatesDigest: digest, DetectedAt: now.UTC(),
	}
	m.Issue = reason
	return true, nil
}
