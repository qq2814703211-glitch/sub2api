package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type priceSyncManualJSON CNYOfficialManualPrice

func (p *CNYOfficialManualPrice) UnmarshalJSON(b []byte) error {
	var v priceSyncManualJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("manual price must be an object")
	}
	*p = CNYOfficialManualPrice(v)
	p.present = make(map[string]bool, len(fields))
	for k, raw := range fields {
		if !bytes.Equal(raw, []byte("null")) {
			p.present[k] = true
		}
	}
	return nil
}

func (p CNYOfficialManualPrice) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(priceSyncManualJSON(p))
	if err != nil || p.present == nil {
		return b, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"input", "output", "cache_read", "cache_write"} {
		if !p.present[key] && string(fields[key]) == "0" {
			delete(fields, key)
		}
	}
	for _, e := range cnyOfficialManualExtraFields {
		if p.present[e.Key] && fields[e.Key] == nil {
			fields[e.Key] = json.RawMessage("0")
		}
	}
	return json.Marshal(fields)
}

var priceSyncUnitField = map[string]string{"per_request": "cost_per_request", "per_second": "cost_per_second", "per_image": "output_cost_per_image"}

func priceSyncManualQuote(model string, p CNYOfficialManualPrice, now time.Time) (PriceSyncQuote, error) {
	mode := p.BillingMode
	if mode == "" {
		mode = "token"
	}
	q := PriceSyncQuote{Model: model, Provider: "operator", Currency: "SITE", BillingMode: mode, Rates: map[string]float64{}, SourceURL: "settings:pricing.cny_official", VerifiedAt: now}
	if mode != "token" && priceSyncUnitField[mode] == "" {
		return q, fmt.Errorf("%s: unsupported billing_mode %q", model, mode)
	}
	for _, f := range []struct {
		k, field string
		v        float64
	}{{"input", "input_cost_per_token", p.Input}, {"output", "output_cost_per_token", p.Output}, {"cache_read", "cache_read_input_token_cost", p.CacheRead}, {"cache_write", "cache_creation_input_token_cost", p.CacheWrite}} {
		if f.v != 0 || (p.present[f.k] && mode == "token") {
			q.Rates[f.field] = f.v / 1e6
		}
	}
	for _, e := range cnyOfficialManualExtraFields {
		v := p.extraValue(e.Key)
		if v != 0 || p.present[e.Key] {
			q.Rates[e.OverrideField] = v / e.extraDivisor()
		}
	}
	for _, f := range []struct {
		k   string
		v   *float64
		div float64
	}{{"cache_creation_input_token_cost_30m", p.CacheWrite30m, 1e6}, {"cache_creation_input_token_cost_12h", p.CacheWrite12h, 1e6}, {"cost_per_request", p.PerRequestPrice, 1}, {"cost_per_second", p.PerSecondPrice, 1}} {
		if f.v != nil {
			q.Rates[f.k] = *f.v / f.div
		}
	}
	for k, v := range q.Rates {
		if !priceSyncFinite(v) {
			return q, fmt.Errorf("%s: invalid %s", model, k)
		}
		if mode != "token" && k != priceSyncUnitField[mode] || mode == "token" && (k == "cost_per_request" || k == "cost_per_second") {
			return q, fmt.Errorf("%s: %s incompatible with %s", model, k, mode)
		}
	}
	if mode == "token" {
		for _, k := range []string{"input_cost_per_token", "output_cost_per_token"} {
			if _, ok := q.Rates[k]; !ok {
				return q, fmt.Errorf("%s: token price requires %s (explicit zero allowed)", model, k)
			}
		}
	} else if _, ok := q.Rates[priceSyncUnitField[mode]]; !ok {
		return q, fmt.Errorf("%s: missing %s", model, priceSyncUnitField[mode])
	}
	if l := cnyOfficialManualLongContext(p); l != nil {
		if mode != "token" {
			return q, fmt.Errorf("%s: long context requires token mode", model)
		}
		rates := map[string]float64{}
		for k, v := range q.Rates {
			m := l.InputMultiplier
			if strings.HasPrefix(k, "output_") {
				m = l.OutputMultiplier
			}
			if k != "output_cost_per_image" {
				rates[k] = v * m
			}
		}
		q.Tiers = []PriceSyncTier{{Threshold: l.Threshold, Rates: rates}}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return q, err
	}
	q.Evidence = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	return q, nil
}

func priceSyncFinite(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func priceSyncManagedRate(k string) bool {
	for _, v := range modelPricingSyncFields {
		if v == k {
			return true
		}
	}
	for _, v := range cnyOfficialManualExtraFields {
		if v.OverrideField == k {
			return true
		}
	}
	switch k {
	case "cache_creation_input_token_cost_30m", "cache_creation_input_token_cost_12h", "cost_per_request", "cost_per_second":
		return true
	}
	return false
}

func priceSyncManagedField(k string) bool {
	return priceSyncManagedRate(k) || aboveTierPricePattern.MatchString(k) || cacheTierPricePattern.MatchString(k) || k == cnyOfficialFieldThreshold || k == cnyOfficialFieldInputMult || k == cnyOfficialFieldOutputMult || k == "cny_official" || k == priceSyncLegacyField || k == "glory_billing_mode" || k == "glory_price_tiers" || k == "glory_price_windows" || k == "glory_price_source"
}

// A managed row replaces all tariff fields, including fields inherited from the
// independently refreshed base catalog. Metadata outside that set still merges.
func priceSyncRender(raw json.RawMessage, q *PriceSyncQuote, manual *CNYOfficialManualPrice, native bool) (json.RawMessage, error) {
	var fields = map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("%s: invalid override row", q.Model)
		}
	}
	for k := range fields {
		if priceSyncManagedField(k) {
			delete(fields, k)
		}
	}
	put := func(k string, v any) error {
		b, err := json.Marshal(v)
		if err == nil {
			fields[k] = b
		}
		return err
	}
	for k, v := range q.Rates {
		if err := put(k, v); err != nil {
			return nil, err
		}
	}
	_ = put("glory_billing_mode", q.BillingMode)
	_ = put("glory_price_source", map[string]any{"currency": q.Currency, "provider": q.Provider, "url": q.SourceURL, "evidence": q.Evidence})
	if q.BillingMode == "token" && (q.Currency == "CNY" || q.Currency == "SITE" && native) {
		_ = put("cny_official", true)
	}
	if len(q.Tiers) > 0 {
		if err := put("glory_price_tiers", q.Tiers); err != nil {
			return nil, err
		}
	}
	if len(q.Windows) > 0 {
		if err := put("glory_price_windows", q.Windows); err != nil {
			return nil, err
		}
	}
	if manual != nil {
		if l := cnyOfficialManualLongContext(*manual); l != nil {
			_ = put(cnyOfficialFieldThreshold, l.Threshold)
			_ = put(cnyOfficialFieldInputMult, l.InputMultiplier)
			_ = put(cnyOfficialFieldOutputMult, l.OutputMultiplier)
		}
	}
	return json.Marshal(fields)
}
