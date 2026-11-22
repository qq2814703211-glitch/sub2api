package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrPriceSyncConflict = errors.New("价格修订已变更，请重新读取后保存")
var ErrPriceSyncInvalid = errors.New("无效价格配置")

// A nil returned payload means reconcile/read only. A non-nil payload appends
// an immutable head. Implementations must serialize callbacks across instances.
type PriceSyncPublishStore interface {
	WithPriceSyncHead(context.Context, func([]byte) (string, []byte, error)) error
}

type priceSyncModel struct {
	NativeCNY      bool            `json:"native_cny,omitempty"`
	Mode           string          `json:"mode"`
	Auto           *PriceSyncQuote `json:"auto,omitempty"`
	Effective      *PriceSyncQuote `json:"effective,omitempty"`
	Issue          string          `json:"issue,omitempty"`
	External       bool            `json:"external,omitempty"`
	ManualEvidence string          `json:"manual_baseline_evidence,omitempty"`
	// Re-verification of the same tariff must retain the projection that made
	// an equivalent restore safe, including the previous cache-write meters.
	PreserveRestoreProjection bool `json:"preserve_restore_projection,omitempty"`
	// LegacyAuto marks a V1 automatic CNY row whose current values still equal
	// the last cny-official sync write. LegacyQuarantine carries the proven
	// currency mismatch and the billing block status.
	LegacyAuto       bool                   `json:"legacy_auto,omitempty"`
	LegacyQuarantine *priceSyncLegacyStatus `json:"legacy_quarantine,omitempty"`
}

type priceSyncRevision struct {
	Revision     string                     `json:"revision"`
	Parent       string                     `json:"parent,omitempty"`
	Settings     CNYOfficialPricingSettings `json:"settings"`
	Models       map[string]*priceSyncModel `json:"models"`
	Entries      map[string]json.RawMessage `json:"entries"`
	SourceErrors map[string]string          `json:"source_errors,omitempty"`
}

func priceSyncStatus(h *priceSyncRevision) map[string]any {
	if h == nil {
		return map[string]any{"active_revision": "", "models": map[string]any{}}
	}
	return map[string]any{"active_revision": h.Revision, "models": h.Models, "source_errors": h.SourceErrors}
}

func (s *ModelPricingSyncService) bootstrapPriceSync(ctx context.Context) (*priceSyncRevision, error) {
	cfg, err := loadCNYOfficialPricingSettings(ctx, s.settings)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &CNYOfficialPricingSettings{}
	}
	cfg, err = normalizePriceSyncSettings(cfg)
	if err != nil {
		return nil, err
	}
	path := s.cfg.LegacyOverrideFile
	if path == "" {
		path = s.cfg.OverrideFile
	}
	legacy := ModelPricingSyncService{cfg: &ModelPricingSyncConfig{OverrideFile: path}}
	entries, err := legacy.loadOverrideEntries()
	if err != nil {
		return nil, err
	}
	h := &priceSyncRevision{Settings: *cfg, Entries: entries, Models: map[string]*priceSyncModel{}}

	for k, p := range cfg.ManualOverrides {
		q, err := priceSyncManualQuote(k, p, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		// V1 marked even foreign manual rows cny_official. This is a billing
		// compatibility flag, not evidence of a SKU's currency policy.
		h.Models[k] = &priceSyncModel{Mode: "manual", Effective: &q}
		// Existing stored rows can inherit rates from the V1 catalog. Preserve
		// the original projection until an explicit edit or a verified restore.
		if raw, ok := entries[k]; ok {
			// V1 treated zero as unspecified. Report the actual importer row,
			// not an inferred free cache write from its four scalar DTO fields.
			q.Rates = map[string]float64{}
			for f, v := range decodeModelPricingSyncEntry(raw) {
				if priceSyncManagedRate(f) {
					q.Rates[f] = v
				}
			}
		} else {
			entries[k], err = priceSyncRender(nil, &q, &p, isDeepSeekModel(k))
			if err != nil {
				return nil, err
			}
		}
	}
	if _, err := s.reconcileLegacyPrices(ctx, h, nil); err != nil {
		return nil, err
	}
	return h, nil
}

type priceSyncMutation func(*priceSyncRevision) (bool, error)

// Prepare a private projection under the same mutex as ordinary catalog rebuilds.
// Publish the in-memory map only AFTER commit; a failed write/reload/commit
// leaves the previous live map untouched. A restart regenerates from PG.
func (s *ModelPricingSyncService) publish(ctx context.Context, mutate priceSyncMutation) (*priceSyncRevision, error) {
	if !s.SyncEnabled() || s.cfg.Store == nil || s.pricing == nil {
		return nil, errors.New("未配置持久发布仓储、结算目录或覆盖文件")
	}
	p := s.pricing
	p.buildMu.Lock()
	defer p.buildMu.Unlock()
	var head *priceSyncRevision
	var ready map[string]*LiteLLMModelPricing
	var fp, baseHash string
	var previous []byte
	var existed, written bool
	err := s.cfg.Store.WithPriceSyncHead(ctx, func(raw []byte) (string, []byte, error) {
		var err error
		if len(raw) == 0 {
			head, err = s.bootstrapPriceSync(ctx)
		} else {
			err = json.Unmarshal(raw, &head)
		}
		if err != nil {
			return "", nil, err
		}
		if head == nil || head.Entries == nil || head.Models == nil {
			return "", nil, errors.New("invalid price revision")
		}
		// The committed head is now known. It is recorded even when this
		// process already installed it, so `converged` keeps a fresh
		// verification time instead of an unbounded claim from startup.
		s.follow.recordHead(head.Revision, time.Now().UTC())
		changed := false
		if mutate != nil {
			changed, err = mutate(head)
			if err != nil {
				return "", nil, err
			}
		}
		var payload []byte
		if changed || len(raw) == 0 {
			head.Parent = head.Revision
			head.Revision = uuid.NewString()
			payload, err = json.Marshal(head)
			if err != nil {
				return "", nil, err
			}
		}
		s.mu.Lock()
		installed := s.active != nil && s.active.Revision == head.Revision
		s.mu.Unlock()
		if installed && !p.projectionFault {
			s.follow.recordProjectionOK()
			return head.Revision, payload, nil
		}
		previous, err = os.ReadFile(s.cfg.OverrideFile)
		existed = err == nil
		if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		}
		err = s.writeOverrideFile(head.Entries)
		if err != nil {
			return "", nil, err
		}
		written = true
		if err = p.validateCustomPricingFiles(); err != nil {
			return "", nil, err
		}
		body, err := os.ReadFile(p.getPricingFilePath())
		if err != nil {
			return "", nil, err
		}
		baseHash = fmt.Sprintf("%x", sha256.Sum256(body))
		ready, fp, err = p.buildPricingData(body)
		if err != nil {
			return "", nil, err
		}
		if fp != p.customPricingFilesFingerprint() {
			return "", nil, errors.New("pricing layers changed during publish")
		}
		for model, row := range head.Models {
			if row.Effective != nil && ready[model] == nil {
				return "", nil, fmt.Errorf("价卡未被结算目录加载: %s", model)
			}
		}
		return head.Revision, payload, nil
	})
	if err != nil {
		if written {
			var restore error
			if existed {
				restore = writePriceProjection(s.cfg.OverrideFile, previous)
			} else {
				restore = os.Remove(s.cfg.OverrideFile)
			}
			if restore != nil {
				p.projectionFault = true
				s.follow.recordProjectionFault()
				return nil, errors.Join(err, fmt.Errorf("restore previous projection: %w", restore))
			}
		}
		return nil, err
	}
	if ready != nil {
		p.projectionFault = false
		p.mu.Lock()
		p.pricingData = ready
		p.catalogRevision = head.Revision
		p.catalogBaseHash = baseHash
		p.customFilesHash = fp
		p.mu.Unlock()
		// Same critical path as the map swap: before this line the process is
		// still billing the previous revision, so it must not be reported as
		// applied anywhere else either.
		s.follow.recordApplied(head.Revision, time.Now().UTC())
	}
	s.mu.Lock()
	s.active = head
	s.mu.Unlock()
	return head, nil
}

func publishedSettings(h *priceSyncRevision) (*CNYOfficialPricingSettings, error) {
	b, e := json.Marshal(h.Settings)
	if e != nil {
		return nil, e
	}
	var v CNYOfficialPricingSettings
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	v.Revision = h.Revision
	v.ActiveRevision = h.Revision
	return &v, nil
}
func (s *ModelPricingSyncService) PublishedSettings(ctx context.Context) (*CNYOfficialPricingSettings, error) {
	if !s.SyncEnabled() {
		v, err := loadCNYOfficialPricingSettings(ctx, s.settings)
		if v == nil && err == nil {
			v = &CNYOfficialPricingSettings{SourceURL: cnyOfficialDefaultSourceURL}
		}
		return v, err
	}
	h, err := s.publish(ctx, nil)
	if err != nil {
		return nil, err
	}
	return publishedSettings(h)
}

func priceSyncEqual(a, b any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}

func (s *ModelPricingSyncService) SavePublishedSettings(ctx context.Context, in *CNYOfficialPricingSettings) (*CNYOfficialPricingSettings, error) {
	cfg, err := normalizePriceSyncSettings(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPriceSyncInvalid, err)
	}
	h, err := s.publish(ctx, func(h *priceSyncRevision) (bool, error) {
		// Omitted manual_overrides is a settings-only request. An explicit {}
		// is the full replacement which can request restoration of every card.
		if in.ManualOverrides == nil {
			cfg.ManualOverrides = h.Settings.ManualOverrides
		}
		if priceSyncEqual(h.Settings, *cfg) {
			return false, nil
		} // idempotent replay, including old clients
		if in.Revision == "" || in.Revision != h.Revision {
			return false, ErrPriceSyncConflict
		}
		now := time.Now().UTC()
		if cfg.SourceURL != h.Settings.SourceURL || cfg.Enabled != h.Settings.Enabled {
			for _, m := range h.Models {
				if m.Auto != nil && m.Auto.Currency == "CNY" {
					m.Issue = "采集设置已变更，需重新核验"
				}
			}
		}
		for k, manual := range h.Settings.ManualOverrides {
			if _, keep := cfg.ManualOverrides[k]; !keep {
				if err := restorePriceSync(k, h.Models[k], now, h.Entries[k], manual); err != nil {
					return false, fmt.Errorf("%w: %v", ErrPriceSyncConflict, err)
				}
				if m := h.Models[k]; !m.preserveLegacyRestoreEntry(h.Entries[k]) {
					h.Entries[k], err = priceSyncRender(h.Entries[k], m.Effective, nil, m.NativeCNY)
					if err != nil {
						return false, err
					}
				}
			}
		}
		for k, p := range cfg.ManualOverrides {
			if prev, ok := h.Settings.ManualOverrides[k]; ok && priceSyncEqual(p, prev) {
				continue
			}
			q, e := priceSyncManualQuote(k, p, now)
			if e != nil {
				return false, fmt.Errorf("%w: %v", ErrPriceSyncInvalid, e)
			}
			m := h.Models[k]
			if m == nil {
				m = &priceSyncModel{}
				h.Models[k] = m
			}
			if m.External {
				return false, fmt.Errorf("%w: %s 已由外部文件手配", ErrPriceSyncConflict, k)
			}
			m.Mode = "manual"
			m.PreserveRestoreProjection = false
			m.Effective = &q
			m.ManualEvidence = ""
			if m.Auto != nil {
				m.ManualEvidence = m.Auto.Evidence
			}
			h.Entries[k], e = priceSyncRender(h.Entries[k], &q, &p, m.NativeCNY || isDeepSeekModel(k))
			if e != nil {
				return false, e
			}
			// An operator card is an independent selling price; it supersedes
			// any legacy catalog quarantine for this SKU.
			m.LegacyAuto = false
			m.LegacyQuarantine = nil
		}
		h.Settings = *cfg
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return publishedSettings(h)
}

const priceSyncMaxAge = 24 * time.Hour

func priceSyncValid(model string, q *PriceSyncQuote, prior *PriceSyncQuote, now time.Time) error {
	if q == nil {
		return fmt.Errorf("%s 缺少可信自动报价", model)
	}
	if q.Model != model || strings.TrimSpace(q.Provider) == "" || strings.TrimSpace(q.SourceURL) == "" || strings.TrimSpace(q.Evidence) == "" || (q.Currency != "CNY" && q.Currency != "USD") {
		return fmt.Errorf("%s 模型、币种或来源证据不足", model)
	}
	if q.VerifiedAt.IsZero() || q.VerifiedAt.After(now) || now.Sub(q.VerifiedAt) > priceSyncMaxAge {
		return fmt.Errorf("%s 自动报价已过期或核验时间不合法", model)
	}
	required := []string{priceSyncUnitField[q.BillingMode]}
	if q.BillingMode == "token" {
		required = []string{"input_cost_per_token", "output_cost_per_token"}
	} else if required[0] == "" {
		return fmt.Errorf("%s: unknown billing mode", model)
	} else if len(q.Tiers) != 0 || len(q.Windows) != 0 {
		return fmt.Errorf("%s: quote tiers and windows require token billing (got %s)", model, q.BillingMode)
	}
	if prior != nil {
		if q.BillingMode != prior.BillingMode {
			return fmt.Errorf("%s 计费单位不符", model)
		}
		for _, k := range sortedStrings(prior.Rates) {
			if !priceSyncOfficialTTLNotApplicable(q, prior, k) {
				required = append(required, k)
			}
		}
		if len(q.Tiers) < len(prior.Tiers) || len(q.Windows) < len(prior.Windows) {
			return fmt.Errorf("%s 缺少阶梯或时窗", model)
		}
	}
	for _, k := range required {
		if _, ok := q.Rates[k]; !ok {
			return fmt.Errorf("%s 缺少 %s", model, k)
		}
	}
	check := func(r map[string]float64) error {
		if len(r) == 0 {
			return fmt.Errorf("%s: empty rates", model)
		}
		for k, v := range r {
			if !priceSyncManagedRate(k) || !priceSyncFinite(v) {
				return fmt.Errorf("%s: invalid %s", model, k)
			}
			if q.BillingMode != "token" && k != required[0] || q.BillingMode == "token" && (k == "cost_per_request" || k == "cost_per_second") {
				return fmt.Errorf("%s: incompatible %s", model, k)
			}
		}
		return nil
	}
	if err := check(q.Rates); err != nil {
		return err
	}
	last := 0
	for _, t := range q.Tiers {
		if t.Threshold <= last {
			return fmt.Errorf("%s: invalid tier threshold", model)
		}
		last = t.Threshold
		if err := check(t.Rates); err != nil {
			return err
		}
		for k := range q.Rates {
			if _, ok := t.Rates[k]; !ok {
				return fmt.Errorf("%s: tier %d missing %s", model, t.Threshold, k)
			}
		}
	}
	for _, w := range q.Windows {
		if _, err := time.LoadLocation(w.Timezone); err != nil || w.Timezone == "" {
			return fmt.Errorf("%s: invalid timezone", model)
		}
		if _, err := time.Parse("15:04", w.Start); err != nil {
			return err
		}
		if _, err := time.Parse("15:04", w.End); err != nil && w.End != "24:00" {
			return err
		}
		if err := check(w.Rates); err != nil {
			return err
		}
	}
	return nil
}

func restorePriceSync(k string, m *priceSyncModel, now time.Time, entry json.RawMessage, manual CNYOfficialManualPrice) error {
	if m == nil || m.External {
		return fmt.Errorf("%s 没有可恢复的自动报价", k)
	}
	if m.Issue != "" {
		return fmt.Errorf("%s: %s", k, m.Issue)
	}
	// Dimensions of a temporary manual unit are not requirements of a
	// different, independently verified automatic unit for the same exact SKU.
	required := m.Effective
	if required != nil && m.Auto != nil && required.BillingMode != m.Auto.BillingMode {
		required = nil
	}
	if err := priceSyncValid(k, m.Auto, required, now); err != nil {
		return err
	}
	prior := m.Effective
	if prior == nil {
		return fmt.Errorf("%s 尚无纠错后的新核验证据", k)
	}
	freshCorrection := m.Auto.VerifiedAt.After(prior.VerifiedAt) && m.Auto.Evidence != m.ManualEvidence
	var equivalent *PriceSyncQuote
	var preserve bool
	if m.Auto.VerifiedAt.Equal(prior.VerifiedAt) || (freshCorrection && !declaresTokenTariff(entry)) {
		equivalent, preserve = equivalentPriceSyncRestore(m, entry, manual)
	}
	if !freshCorrection && equivalent == nil {
		return fmt.Errorf("%s 尚无纠错后的新核验证据", k)
	}
	m.Mode = "auto"
	m.Effective = m.Auto
	if equivalent != nil {
		m.Effective = equivalent
	}
	m.PreserveRestoreProjection = preserve
	m.ManualEvidence = ""
	return nil
}
