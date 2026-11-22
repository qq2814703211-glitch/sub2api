package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type legacyHistoryStub struct {
	bySource  map[string]map[string]map[string]float64
	providers map[string]string
}

// Synthetic unit fixture only. Real exports with an unclassified reason never use this stub.
func (h legacyHistoryStub) ListLatestPricingFields(_ context.Context, keys []string) ([]ModelPricingSyncHistory, error) {
	var rows []ModelPricingSyncHistory
	for _, k := range keys {
		for f, v := range h.bySource[ModelPricingSyncSourceCNYOfficial][k] {
			provider := h.providers[k]
			if provider == "" {
				provider = "vendor"
			}
			reason := cnyOfficialReason(provider, 0)
			if f == cnyOfficialFieldThreshold || f == cnyOfficialFieldInputMult || f == cnyOfficialFieldOutputMult {
				reason = "按官方人民币牌价折算长上下文阶梯（1 档，本版只表达第 1 档阈值 128000）"
			}
			rows = append(rows, ModelPricingSyncHistory{Model: k, Field: f, NewValue: v, Source: ModelPricingSyncSourceCNYOfficial, Reason: reason})
		}
	}
	return rows, nil
}

func (h legacyHistoryStub) InsertBatch(context.Context, []*ModelPricingSyncHistory) (int64, error) {
	return 0, nil
}
func (h legacyHistoryStub) List(context.Context, *ModelPricingSyncHistoryFilter) ([]*ModelPricingSyncHistory, int64, error) {
	return nil, 0, nil
}
func (h legacyHistoryStub) ListSyncedFields(_ context.Context, source string) (map[string]map[string]float64, error) {
	return h.bySource[source], nil
}

func legacyPtr(v float64) *float64 { return &v }

func legacyFixture(t *testing.T, history ModelPricingSyncHistoryRepository, settings string) (*ModelPricingSyncService, *BillingService, *publishTestStore) {
	t.Helper()
	c := &config.Config{}
	c.Pricing.DataDir = t.TempDir()
	c.Pricing.OverrideFile = filepath.Join(c.Pricing.DataDir, "projection.json")
	legacyPath := filepath.Join(c.Pricing.DataDir, "v1.json")
	require.NoError(t, os.WriteFile(filepath.Join(c.Pricing.DataDir, "model_pricing.json"), []byte(`{"sentinel":{"input_cost_per_token":0.003}}`), 0600))
	rows := `{
 "legacy-est":{"input_cost_per_token":1.5e-7,"output_cost_per_token":6e-7,"cache_read_input_token_cost":3e-8,"long_context_input_token_threshold":128000,"long_context_input_cost_multiplier":2,"long_context_output_cost_multiplier":1.5,"cny_official":true},
 "legacy-manual":{"input_cost_per_token":1.3e-5,"output_cost_per_token":6.8e-5,"cache_read_input_token_cost":1.3e-6,"cny_official":true},
 "legacy-edited":{"input_cost_per_token":2e-7,"output_cost_per_token":6e-7,"cny_official":true}
}`
	require.NoError(t, os.WriteFile(legacyPath, []byte(rows), 0600))
	p := NewPricingService(c, nil)
	store := &publishTestStore{}
	s := NewModelPricingSyncService(&ModelPricingSyncConfig{
		OverrideFile: c.Pricing.OverrideFile, LegacyOverrideFile: legacyPath,
		Store: store, SettingsReader: publishLegacyReader(settings),
	}, p, history, nil)
	return s, NewBillingService(c, p), store
}

func legacyAutomaticSupply() PriceSyncCollection {
	return PriceSyncCollection{
		Quotes:       map[string]PriceSyncQuote{},
		NativeModels: map[string]bool{"legacy-est": true, "legacy-manual": true, "legacy-edited": true},
		Problems: map[string]string{
			"legacy-est":    "reference only; missing model-level CNY tariff",
			"legacy-manual": "reference only; missing model-level CNY tariff",
			"legacy-edited": "reference only; missing model-level CNY tariff",
		},
		LegacyUSDEstimates: map[string]string{
			"legacy-est":    (legacyEstimate{SKU: "legacy-est", Provider: "vendor", Evidence: "fixture vendor/legacy-est: upstream USD estimate", Rates: map[string]float64{"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7, "cache_read_input_token_cost": 3e-8}}).encode(),
			"legacy-manual": "basellm sha256:fixture vendor/legacy-manual: upstream USD estimate",
			"legacy-edited": "basellm sha256:fixture vendor/legacy-edited: upstream USD estimate",
		},
	}
}

func TestPriceSyncLegacyUSDQuarantineProtectsManualAndGroup(t *testing.T) {
	ctx := context.Background()
	history := legacyHistoryStub{bySource: map[string]map[string]map[string]float64{
		ModelPricingSyncSourceCNYOfficial: {
			"legacy-est": {
				"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7, "cache_read_input_token_cost": 3e-8,
				"long_context_input_token_threshold": 128000, "long_context_input_cost_multiplier": 2, "long_context_output_cost_multiplier": 1.5,
			},
			"legacy-manual": {"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7},
			"legacy-edited": {"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7},
		},
	}}
	settings := `{"enabled":true,"manual_overrides":{"legacy-manual":{"input":13,"output":68,"cache_read":1.3}}}`
	s, b, _ := legacyFixture(t, history, settings)
	_, err := s.PublishedSettings(ctx)
	require.NoError(t, err)

	// Baseline: the unproven legacy row is billed at its stored numbers, which is
	// the behaviour the fix must withdraw only where the USD-estimate class is
	// proven.
	before, err := b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.6, before.ActualCost, 1e-12)
	manual, err := b.CalculateCost("legacy-manual", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 68, manual.ActualCost, 1e-12)

	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return legacyAutomaticSupply(), nil
	}
	_, err = s.Sync(ctx)
	require.NoError(t, err)

	row := s.active.Models["legacy-est"]
	require.NotNil(t, row)
	require.True(t, row.LegacyAuto)
	require.NotNil(t, row.LegacyQuarantine)
	require.Equal(t, priceSyncLegacyQuarantined, row.LegacyQuarantine.Status)
	require.Contains(t, row.Issue, "USD 估值")
	require.Contains(t, row.LegacyQuarantine.Evidence, "upstream USD estimate")

	// The original numbers stay in the file; only the CNY compatibility flag is
	// removed and V2 provenance is added.
	entry := map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(s.active.Entries["legacy-est"], &entry))
	_, hasFlag := entry["cny_official"]
	require.False(t, hasFlag)
	require.Contains(t, entry, priceSyncLegacyField)
	var input float64
	require.NoError(t, json.Unmarshal(entry["input_cost_per_token"], &input))
	require.InDelta(t, 1.5e-7, input, 1e-15)

	_, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.Error(t, err)
	require.True(t, isTariffPriceIssue(err))
	require.Contains(t, err.Error(), "USD 估值")

	// Operator manual price is untouched and still bills.
	manual, err = b.CalculateCost("legacy-manual", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 68, manual.ActualCost, 1e-12)
	require.Equal(t, "manual", s.active.Models["legacy-manual"].Mode)

	// A value that no longer matches the cny-official history is attributed to
	// another writer and must keep billing exactly as before.
	require.Nil(t, s.active.Models["legacy-edited"].LegacyQuarantine)
	require.True(t, s.active.Models["legacy-edited"].External)
	edited, err := b.CalculateCost("legacy-edited", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.6, edited.ActualCost, 1e-12)

	// An explicit group card is the operator's selling price and wins over the
	// quarantined catalog row.
	group := &Group{ID: 7, Name: "group", Platform: "openai", ModelPricing: []ChannelModelPricing{{
		Models: []string{"legacy-est"}, InputPrice: legacyPtr(1e-5), OutputPrice: legacyPtr(2e-5), CacheReadPrice: legacyPtr(1e-6),
	}}}
	resolver := NewModelPricingResolver(nil, b)
	resolved := resolver.Resolve(ctx, PricingInput{Model: "legacy-est", Group: group})
	require.Equal(t, PricingSourceGroup, resolved.Source)
	grouped, err := b.CalculateCostUnified(CostInput{Ctx: ctx, Model: "legacy-est", Group: group, Tokens: UsageTokens{OutputTokens: 1_000_000}, RequestCount: 1, RateMultiplier: 1, Resolver: resolver, Resolved: resolved})
	require.NoError(t, err)
	require.InDelta(t, 20, grouped.ActualCost, 1e-12)

	// A channel card uses the same explicit-operator precedence.
	channelCard := &ChannelModelPricing{Models: []string{"legacy-est"}, InputPrice: legacyPtr(3e-5), OutputPrice: legacyPtr(4e-5)}
	channelCost, err := b.calculateCostInternalWithPolicy("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1, "", channelCard, false)
	require.NoError(t, err)
	require.InDelta(t, 40, channelCost.ActualCost, 1e-12)

	// An operator card is a new selling price and must lift the quarantine.
	cfg := publishConfig(t, s)
	cfg.ManualOverrides["legacy-est"] = publishManual(t, `{"input":5,"output":50,"cache_read":2}`)
	publishSet(t, s, cfg)
	require.Nil(t, s.active.Models["legacy-est"].LegacyQuarantine)
	entry = map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(s.active.Entries["legacy-est"], &entry))
	require.NotContains(t, entry, priceSyncLegacyField)
	manualCost, err := b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 50, manualCost.ActualCost, 1e-12)
}

func TestPriceSyncLegacyQuarantineSurvivesOfflineAndRecoversOnVerifiedCNY(t *testing.T) {
	ctx := context.Background()
	history := legacyHistoryStub{bySource: map[string]map[string]map[string]float64{
		ModelPricingSyncSourceCNYOfficial: {
			"legacy-est": {
				"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7, "cache_read_input_token_cost": 3e-8,
				"long_context_input_token_threshold": 128000, "long_context_input_cost_multiplier": 2, "long_context_output_cost_multiplier": 1.5,
			},
		},
	}}
	s, b, _ := legacyFixture(t, history, `{"enabled":true,"manual_overrides":{}}`)
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return legacyAutomaticSupply(), nil
	}
	_, err := s.Sync(ctx)
	require.NoError(t, err)
	_, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.True(t, isTariffPriceIssue(err))
	quarantinedRevision := s.active.Revision

	// Source offline after a proven quarantine: the revision and the withheld
	// state must not be resurrected into a billable legacy price.
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{}, errors.New("vendor offline")
	}
	_, err = s.Sync(ctx)
	require.Error(t, err)
	require.Equal(t, quarantinedRevision, s.active.Revision)
	_, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.True(t, isTariffPriceIssue(err))

	// A verified CNY quote for the same exact SKU replaces the row and clears
	// the quarantine in the same revision.
	recovered := PriceSyncQuote{
		Model: "legacy-est", Provider: "vendor", Currency: "CNY", BillingMode: "token",
		SourceURL: "https://vendor.invalid/cn", Evidence: "fixture exact CNY quote", VerifiedAt: time.Now().UTC(),
		Rates: map[string]float64{"input_cost_per_token": 3e-6, "output_cost_per_token": 4e-5, "cache_read_input_token_cost": 1e-6},
	}
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{"legacy-est": recovered}, NativeModels: map[string]bool{"legacy-est": true}}, nil
	}
	_, err = s.Sync(ctx)
	require.NoError(t, err)
	row := s.active.Models["legacy-est"]
	require.False(t, row.LegacyAuto)
	require.Nil(t, row.LegacyQuarantine)
	require.Equal(t, "auto", row.Mode)
	entry := map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(s.active.Entries["legacy-est"], &entry))
	require.NotContains(t, entry, priceSyncLegacyField)
	require.Contains(t, entry, "glory_price_source")
	charged, err := b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 40, charged.ActualCost, 1e-12)

	// A later source failure must not roll back a correct published price.
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{}, errors.New("vendor timeout")
	}
	_, err = s.Sync(ctx)
	require.Error(t, err)
	charged, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 40, charged.ActualCost, 1e-12)
}

func TestPriceSyncLegacyMarkerSurvivesReload(t *testing.T) {
	s, _, _ := legacyFixture(t, nil, `{"enabled":true,"manual_overrides":{}}`)
	rates := map[string]json.RawMessage{
		"input_cost_per_token":  json.RawMessage("1.5e-7"),
		"output_cost_per_token": json.RawMessage("6e-7"),
	}
	numeric := map[string]float64{"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7}
	status, err := json.Marshal(priceSyncLegacyStatus{
		Status: priceSyncLegacyQuarantined, Reason: priceSyncLegacyReason,
		Evidence: "basellm sha256:fixture", RatesDigest: priceSyncLegacyRatesDigest(numeric),
	})
	require.NoError(t, err)
	rates[priceSyncLegacyField] = status
	row, err := json.Marshal(rates)
	require.NoError(t, err)
	h := &priceSyncRevision{
		Settings: CNYOfficialPricingSettings{Enabled: true},
		Entries:  map[string]json.RawMessage{"legacy-est": row},
		Models:   map[string]*priceSyncModel{},
	}
	_, err = s.reconcileLegacyPrices(context.Background(), h, nil)
	require.NoError(t, err)
	require.True(t, h.Models["legacy-est"].LegacyAuto)
	require.NotNil(t, h.Models["legacy-est"].LegacyQuarantine)
	require.Equal(t, priceSyncLegacyQuarantined, h.Models["legacy-est"].LegacyQuarantine.Status)
}

func TestPriceSyncLegacyQuarantineBootstrapsFromFile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "projection.json")
	legacyPath := filepath.Join(cfg.Pricing.DataDir, "v1.json")
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), []byte(`{"sentinel":{"input_cost_per_token":0.003}}`), 0600))
	rates := map[string]json.RawMessage{
		"input_cost_per_token":  json.RawMessage("1.5e-7"),
		"output_cost_per_token": json.RawMessage("6e-7"),
	}
	status, err := json.Marshal(priceSyncLegacyStatus{
		Status: priceSyncLegacyQuarantined, Reason: priceSyncLegacyReason,
		Evidence: "basellm sha256:fixture", RatesDigest: priceSyncLegacyRatesDigest(map[string]float64{"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7}),
	})
	require.NoError(t, err)
	rates[priceSyncLegacyField] = status
	row, err := json.Marshal(rates)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{"legacy-est":`+string(row)+`}`), 0600))

	history := legacyHistoryStub{bySource: map[string]map[string]map[string]float64{
		ModelPricingSyncSourceCNYOfficial: {
			"legacy-est": {"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7},
		},
	}}
	pricing := NewPricingService(cfg, nil)
	s := NewModelPricingSyncService(&ModelPricingSyncConfig{
		OverrideFile: cfg.Pricing.OverrideFile, LegacyOverrideFile: legacyPath,
		Store: &publishTestStore{}, SettingsReader: publishLegacyReader(`{"enabled":true,"manual_overrides":{}}`),
	}, pricing, history, nil)
	_, err = s.PublishedSettings(context.Background())
	require.NoError(t, err)
	model := s.active.Models["legacy-est"]
	require.NotNil(t, model)
	require.False(t, model.External, "quarantine provenance must not turn the row into external/manual")
	require.True(t, model.LegacyAuto)
	require.NotNil(t, model.LegacyQuarantine)
	require.Equal(t, priceSyncLegacyQuarantined, model.LegacyQuarantine.Status)
	_, err = NewBillingService(cfg, pricing).CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.True(t, isTariffPriceIssue(err))
}

func TestPriceSyncLegacyWithoutHistoryStaysDeclaredAndBillable(t *testing.T) {
	s, b, _ := legacyFixture(t, nil, `{"enabled":true,"manual_overrides":{}}`)
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		supply := legacyAutomaticSupply()
		delete(supply.Quotes, "legacy-est")
		return supply, nil
	}
	_, err := s.Sync(context.Background())
	require.NoError(t, err)
	row := s.active.Models["legacy-est"]
	require.True(t, row.External)
	require.Contains(t, row.Issue, "缺少同步变更史")
	require.Nil(t, row.LegacyQuarantine)
	entry := map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(s.active.Entries["legacy-est"], &entry))
	require.Contains(t, entry, "cny_official")
	require.NotContains(t, entry, priceSyncLegacyField)
	charged, err := b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.6, charged.ActualCost, 1e-12)
}
