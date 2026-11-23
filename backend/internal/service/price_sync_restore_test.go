package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPriceSyncRestoreEquivalentKimi(t *testing.T) {
	bodies := priceCoverageFetcher{officialKimiURL: officialFixture(t, "kimi.html")}
	quotes, problems, err := officialSources[0].load(context.Background(), bodies)
	require.NoError(t, err)
	require.Empty(t, problems)
	for _, tc := range []struct {
		model, manual string
		write, hour   float64
	}{
		{"kimi-k3.1-pro", `{"input":13,"output":68,"cache_read":1.3,"cache_write":13,"cache_write_1h":26}`, 13, 26},
		{"kimi-k3.1-pro-highspeed", `{"input":26,"output":136,"cache_read":1.3,"cache_write":26,"cache_write_1h":52}`, 26, 52},
	} {
		t.Run(tc.model, func(t *testing.T) {
			q, ok := quotes[tc.model]
			require.True(t, ok)
			q.VerifiedAt = time.Now().UTC().Add(-time.Minute)
			manual := publishManual(t, tc.manual)
			prior, err := priceSyncManualQuote(tc.model, manual, q.VerifiedAt)
			require.NoError(t, err)
			require.Equal(t, prior.Rates, q.Rates)
			for _, alreadyEffective := range []bool{false, true} {
				name := "manual-projection"
				if alreadyEffective {
					name = "auto-projection-with-stale-setting"
				}
				t.Run(name, func(t *testing.T) {
					record := priceSyncModel{Mode: "manual", NativeCNY: true, Auto: &q, Effective: &prior, ManualEvidence: q.Evidence}
					cfgForProjection := &manual
					if alreadyEffective {
						record.Mode, record.Effective, record.ManualEvidence = "auto", &q, ""
						cfgForProjection = nil
					}
					entry, err := priceSyncRender(nil, record.Effective, cfgForProjection, true)
					require.NoError(t, err)
					s, b := priceSyncRestoreWithEntry(t, manual, record, entry)
					usage := UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 50, CacheCreationTokens: 1000, CacheCreation1hTokens: 500}
					before, err := b.CalculateCost(tc.model, usage, 1)
					require.NoError(t, err)
					require.InDelta(t, (tc.write+tc.hour)*0.0005, before.CacheCreationCost, 1e-12)
					cfg := publishConfig(t, s)
					delete(cfg.ManualOverrides, tc.model)
					_, err = s.SavePublishedSettings(context.Background(), cfg)
					require.NoError(t, err)
					require.Equal(t, "auto", s.active.Models[tc.model].Mode)
					require.Equal(t, q.VerifiedAt, s.active.Models[tc.model].Effective.VerifiedAt)
					after, err := b.CalculateCost(tc.model, usage, 1)
					require.NoError(t, err)
					require.Equal(t, before.ActualCost, after.ActualCost)
				})
			}
		})
	}
}

func TestPriceSyncRestoreEquivalentOfficialOpenAITTL(t *testing.T) {
	quotes, problems, err := loadOfficialOpenAI(context.Background(), priceCoverageFetcher(priceCoverageOfficialBodies(t)))
	require.NoError(t, err)
	require.Empty(t, problems)
	for _, tc := range []struct {
		model, manual string
		short, long   float64
	}{
		{"gpt-6.2-astra", `{"input":8,"output":40,"cache_read":0.4,"cache_write":10,"cache_write_1h":20,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5}`, 1e-5, 2e-5},
		{"gpt-6.2-sol", `{"input":2,"output":10,"cache_read":0.1,"cache_write":2.5,"cache_write_1h":5,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5}`, 2.5e-6, 5e-6},
	} {
		t.Run(tc.model, func(t *testing.T) {
			q, ok := quotes[tc.model]
			require.True(t, ok)
			q.VerifiedAt = time.Now().UTC().Add(-time.Minute)
			manual := publishManual(t, tc.manual)
			prior, err := priceSyncManualQuote(tc.model, manual, q.VerifiedAt)
			require.NoError(t, err)
			require.Equal(t, tc.short, q.Rates[officialCache30m])
			require.Equal(t, tc.long, q.Rates[officialCache12h])
			require.NotContains(t, q.Rates, officialCacheWrite)
			require.NotContains(t, q.Rates, officialCache1h)
			require.True(t, priceSyncOfficialTTLNotApplicable(&q, &prior, officialCacheWrite), "official adapter output + operator card must hit the existing exception")
			require.True(t, priceSyncOfficialTTLNotApplicable(&q, &prior, officialCache1h))
			s, b := priceSyncRestoreFixture(t, manual, &q, &prior, false)
			measure := func(exactTTL bool) []float64 {
				var costs []float64
				for _, input := range []int{271500, 271501} {
					for i, rate := range []float64{tc.short, tc.long} {
						usage := UsageTokens{InputTokens: input, OutputTokens: 10, CacheCreationTokens: 500}
						if exactTTL {
							if i == 0 {
								usage.CacheCreation30mTokens = 500
							} else {
								usage.CacheCreation12hTokens = 500
							}
						} else if i == 1 {
							usage.CacheCreation1hTokens = 500
						}
						cost, err := b.CalculateCost(tc.model, usage, 1)
						require.NoError(t, err)
						if input == 271501 {
							rate *= 2
						}
						require.InDelta(t, 500*rate, cost.CacheCreationCost, 1e-12)
						costs = append(costs, cost.ActualCost)
					}
				}
				return costs
			}
			before := measure(false)
			cfg := publishConfig(t, s)
			delete(cfg.ManualOverrides, tc.model)
			_, err = s.SavePublishedSettings(context.Background(), cfg)
			require.NoError(t, err)
			require.Equal(t, "auto", s.active.Models[tc.model].Mode)
			require.Equal(t, &q, s.active.Models[tc.model].Auto, "source quote must stay untouched")
			require.Equal(t, before, measure(false), "legacy meters retain the same prices")
			require.Equal(t, before, measure(true), "declared TTL meters bill the same amounts")
			s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
				return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{q.Model: q}}, nil
			}
			applied, err := s.Sync(context.Background())
			require.NoError(t, err)
			require.Zero(t, applied)
			require.Equal(t, before, measure(false), "next collection must not drop legacy meters")
		})
	}
}

func priceSyncRestoreFixture(t *testing.T, manual CNYOfficialManualPrice, auto, prior *PriceSyncQuote, native bool) (*ModelPricingSyncService, *BillingService) {
	t.Helper()
	entry, err := priceSyncRender(nil, prior, &manual, native)
	require.NoError(t, err)
	return priceSyncRestoreWithEntry(t, manual, priceSyncModel{Mode: "manual", NativeCNY: native, Auto: auto, Effective: prior, ManualEvidence: auto.Evidence}, entry)
}

func priceSyncRestoreWithEntry(t *testing.T, manual CNYOfficialManualPrice, record priceSyncModel, entry json.RawMessage) (*ModelPricingSyncService, *BillingService) {
	t.Helper()
	model := record.Effective.Model
	store := &publishTestStore{}
	var err error
	store.raw, err = json.Marshal(priceSyncRevision{
		Revision: "manual-head",
		Settings: CNYOfficialPricingSettings{SourceURL: cnyOfficialDefaultSourceURL, Enabled: record.NativeCNY, ManualOverrides: map[string]CNYOfficialManualPrice{model: manual}},
		Models:   map[string]*priceSyncModel{model: &record},
		Entries:  map[string]json.RawMessage{model: entry},
	})
	require.NoError(t, err)
	s, b := publishFixture(t, store)
	publishConfig(t, s)
	return s, b
}

// Two pricing-only rows from the saved V2 head. In particular the effective
// quotes have V1 projections and the tier output differs by one float step.
func TestPriceSyncRestoreSavedOpenAIRows(t *testing.T) {
	for _, tc := range []struct{ model, row string }{
		{"gpt-6.2-astra", `{"model":{"auto":{"model":"gpt-6.2-astra","rates":{"input_cost_per_token":8e-06,"output_cost_per_token":4e-05,"cache_read_input_token_cost":4.0000000000000003e-07,"cache_creation_input_token_cost_12h":2e-05,"cache_creation_input_token_cost_30m":1e-05},"tiers":[{"rates":{"input_cost_per_token":1.6e-05,"output_cost_per_token":6e-05,"cache_read_input_token_cost":8.000000000000001e-07,"cache_creation_input_token_cost_12h":4e-05,"cache_creation_input_token_cost_30m":2e-05},"threshold":272000}],"currency":"USD","evidence":"region=OpenAI API Standard; sha256:88f7825ffddb006fefea4d3838831bd3deaed6f72df6914ad79aefa021d51355; model_rule=https://developers.openai.com/api/docs/models/gpt-6.2-astra.md; sha256:b12e61471fed60c843626fdee3dea09c86babae84ea779a5f5eb9193cb62720f","provider":"openai","source_url":"https://developers.openai.com/api/docs/pricing.md#standard-pricing-data","verified_at":"2026-11-22T11:59:42.443755045Z","billing_mode":"token"},"mode":"manual","effective":{"model":"gpt-6.2-astra","rates":{"input_cost_per_token":8e-06,"output_cost_per_token":4e-05,"cache_read_input_token_cost":4.0000000000000003e-07,"cache_creation_input_token_cost":1e-05,"cache_creation_input_token_cost_above_1hr":2e-05},"tiers":[{"rates":{"input_cost_per_token":1.6e-05,"output_cost_per_token":6.000000000000001e-05,"cache_read_input_token_cost":8.000000000000001e-07,"cache_creation_input_token_cost":2e-05,"cache_creation_input_token_cost_above_1hr":4e-05},"threshold":272000}],"currency":"SITE","evidence":"sha256:0660875901781d87cbf692eac712458c2728693ae06cdfbff4df7598b2e0d3ab","provider":"operator","source_url":"settings:pricing.cny_official","verified_at":"2026-11-22T11:59:41.835471469Z","billing_mode":"token"}},"settings":{"input":8,"output":40,"cache_read":0.4,"cache_write":10,"cache_write_1h":20,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5},"entry":{"cny_official":true,"input_cost_per_token":8e-06,"output_cost_per_token":4e-05,"cache_read_input_token_cost":4.0000000000000003e-07,"cache_creation_input_token_cost":1e-05,"long_context_input_cost_multiplier":2,"long_context_input_token_threshold":272000,"long_context_output_cost_multiplier":1.5,"cache_creation_input_token_cost_above_1hr":2e-05}}`},
		{"gpt-6.2-sol", `{"model":{"auto":{"model":"gpt-6.2-sol","rates":{"input_cost_per_token":2e-06,"output_cost_per_token":1e-05,"cache_read_input_token_cost":1.0000000000000001e-07,"cache_creation_input_token_cost_12h":5e-06,"cache_creation_input_token_cost_30m":2.5e-06},"tiers":[{"rates":{"input_cost_per_token":4e-06,"output_cost_per_token":1.5e-05,"cache_read_input_token_cost":2.0000000000000002e-07,"cache_creation_input_token_cost_12h":1e-05,"cache_creation_input_token_cost_30m":5e-06},"threshold":272000}],"currency":"USD","evidence":"region=OpenAI API Standard; sha256:88f7825ffddb006fefea4d3838831bd3deaed6f72df6914ad79aefa021d51355; model_rule=https://developers.openai.com/api/docs/models/gpt-6.2-sol.md; sha256:551d09dbb7e6f64a1af314446b8d5a72ab9bf66daa4407060007d11ce54acf43","provider":"openai","source_url":"https://developers.openai.com/api/docs/pricing.md#standard-pricing-data","verified_at":"2026-11-22T11:59:42.443755045Z","billing_mode":"token"},"mode":"manual","effective":{"model":"gpt-6.2-sol","rates":{"input_cost_per_token":2e-06,"output_cost_per_token":1e-05,"cache_read_input_token_cost":1.0000000000000001e-07,"cache_creation_input_token_cost":2.5e-06,"cache_creation_input_token_cost_above_1hr":5e-06},"tiers":[{"rates":{"input_cost_per_token":4e-06,"output_cost_per_token":1.5000000000000002e-05,"cache_read_input_token_cost":2.0000000000000002e-07,"cache_creation_input_token_cost":5e-06,"cache_creation_input_token_cost_above_1hr":1e-05},"threshold":272000}],"currency":"SITE","evidence":"sha256:23ada79def722b732d4f7bd55a33889eb40499a150c70806f8fa1a62517a8d9e","provider":"operator","source_url":"settings:pricing.cny_official","verified_at":"2026-11-22T11:59:41.835375746Z","billing_mode":"token"}},"settings":{"input":2,"output":10,"cache_read":0.1,"cache_write":2.5,"cache_write_1h":5,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5},"entry":{"cny_official":true,"input_cost_per_token":2e-06,"output_cost_per_token":1e-05,"cache_read_input_token_cost":1.0000000000000001e-07,"cache_creation_input_token_cost":2.5e-06,"long_context_input_cost_multiplier":2,"long_context_input_token_threshold":272000,"long_context_output_cost_multiplier":1.5,"cache_creation_input_token_cost_above_1hr":5e-06}}`},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var fixture struct {
				Model    priceSyncModel
				Settings CNYOfficialManualPrice
				Entry    json.RawMessage
			}
			require.NoError(t, json.Unmarshal([]byte(tc.row), &fixture))
			a, e := fixture.Model.Auto, fixture.Model.Effective
			require.True(t, a.VerifiedAt.After(e.VerifiedAt))
			require.True(t, priceSyncOfficialTTLNotApplicable(a, e, officialCacheWrite))
			require.True(t, priceSyncOfficialTTLNotApplicable(a, e, officialCache1h))
			require.NoError(t, priceSyncValid(tc.model, a, e, a.VerifiedAt))
			// Move the clock, not the interval between the two recorded timestamps.
			delta := a.VerifiedAt.Sub(e.VerifiedAt)
			a.VerifiedAt = time.Now().UTC().Add(-time.Minute)
			e.VerifiedAt = a.VerifiedAt.Add(-delta)
			s, b := priceSyncRestoreWithEntry(t, fixture.Settings, fixture.Model, fixture.Entry)
			meters := []UsageTokens{
				{CacheCreationTokens: 500}, {CacheCreationTokens: 500, CacheCreation1hTokens: 500},
				{CacheCreationTokens: 500, CacheCreation30mTokens: 500}, {CacheCreationTokens: 500, CacheCreation12hTokens: 500},
				{InputTokens: 271500, OutputTokens: 1000, CacheCreationTokens: 500, CacheCreation12hTokens: 500},
				{InputTokens: 271501, OutputTokens: 1000, CacheCreationTokens: 500, CacheCreation12hTokens: 500},
			}
			measure := func(b *BillingService) []float64 {
				var result []float64
				for _, usage := range meters {
					cost, err := b.CalculateCost(tc.model, usage, 1)
					require.NoError(t, err)
					result = append(result, cost.ActualCost)
				}
				return result
			}
			before := measure(b)
			cfg := publishConfig(t, s)
			delete(cfg.ManualOverrides, tc.model)
			cfg, err := s.SavePublishedSettings(context.Background(), cfg)
			require.NoError(t, err)
			require.NotContains(t, cfg.ManualOverrides, tc.model)
			require.Equal(t, "auto", s.active.Models[tc.model].Mode)
			require.Equal(t, before, measure(b))

			restarted, restartedBilling := publishFixture(t, s.cfg.Store)
			publishConfig(t, restarted)
			restarted.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
				return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{tc.model: *a}}, nil
			}
			applied, err := restarted.Sync(context.Background())
			require.NoError(t, err)
			require.Zero(t, applied)
			require.Empty(t, restarted.active.Models[tc.model].Issue)
			require.Equal(t, before, measure(restartedBilling))
		})
	}
}

func TestPriceSyncRestoreEquivalenceGuards(t *testing.T) {
	const sku = "restore-guard"
	const evidenceError = "价格修订已变更，请重新读取后保存: restore-guard 尚无纠错后的新核验证据"
	for _, tc := range []struct {
		name string
		edit func(*PriceSyncQuote, *PriceSyncQuote)
		want string
	}{
		{"different-rate", func(a, _ *PriceSyncQuote) { a.Rates[officialOutput] *= 2 }, evidenceError},
		{"newer-different-rate", func(a, e *PriceSyncQuote) {
			a.Rates[officialOutput] *= 2
			a.VerifiedAt = a.VerifiedAt.Add(time.Second)
			e.Evidence = "previous-operator-card"
		}, ""},
		{"missing-explicit-zero-cache-write", func(a, _ *PriceSyncQuote) { delete(a.Rates, officialCacheWrite) }, "价格修订已变更，请重新读取后保存: restore-guard 缺少 cache_creation_input_token_cost"},
		{"explicit-zero-cache-write", func(_, _ *PriceSyncQuote) {}, ""},
		{"currency-policy", func(a, _ *PriceSyncQuote) { a.Currency = "CNY" }, evidenceError},
		{"billing-mode", func(a, _ *PriceSyncQuote) {
			a.BillingMode = "per_request"
			a.Rates = map[string]float64{"cost_per_request": 2}
		}, evidenceError},
		{"older", func(a, _ *PriceSyncQuote) { a.VerifiedAt = a.VerifiedAt.Add(-time.Second) }, evidenceError},
		{"extra-zero-rate", func(a, _ *PriceSyncQuote) { a.Rates[officialCacheRead] = 0 }, evidenceError},
		{"tier-boundary", func(a, e *PriceSyncQuote) {
			a.Tiers = []PriceSyncTier{{Threshold: 100, Rates: a.Rates}}
			e.Tiers = []PriceSyncTier{{Threshold: 100, Inclusive: true, Rates: e.Rates}}
		}, evidenceError},
		{"tier-rate", func(a, e *PriceSyncQuote) {
			a.Tiers = []PriceSyncTier{{Threshold: 100, Rates: priceCoverageClone(a.Rates)}}
			e.Tiers = []PriceSyncTier{{Threshold: 100, Rates: e.Rates}}
			a.Tiers[0].Rates[officialOutput] *= 2
		}, evidenceError},
		{"window-boundary", func(a, e *PriceSyncQuote) {
			a.Windows = []PriceSyncWindow{{Timezone: "UTC", Start: "01:00", End: "02:00", Rates: a.Rates}}
			e.Windows = []PriceSyncWindow{{Timezone: "UTC", Start: "01:00", End: "03:00", Rates: e.Rates}}
		}, evidenceError},
		{"window-rate", func(a, e *PriceSyncQuote) {
			a.Windows = []PriceSyncWindow{{Timezone: "UTC", Start: "01:00", End: "02:00", Rates: priceCoverageClone(a.Rates)}}
			e.Windows = []PriceSyncWindow{{Timezone: "UTC", Start: "01:00", End: "02:00", Rates: e.Rates}}
			a.Windows[0].Rates[officialOutput] *= 2
		}, evidenceError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manual := publishManual(t, `{"input":1,"output":2,"cache_write":0}`)
			prior, err := priceSyncManualQuote(sku, manual, time.Now().UTC().Add(-time.Minute))
			require.NoError(t, err)
			auto := prior
			auto.Currency, auto.Provider, auto.SourceURL, auto.Evidence = "USD", "vendor", "https://vendor.invalid/price", "new-source"
			auto.Rates = priceCoverageClone(prior.Rates)
			tc.edit(&auto, &prior)
			entry, err := priceSyncRender(nil, &prior, &manual, false)
			require.NoError(t, err)
			s, b := priceSyncRestoreWithEntry(t, manual, priceSyncModel{Mode: "manual", Auto: &auto, Effective: &prior, ManualEvidence: "old-source"}, entry)
			cfg := publishConfig(t, s)
			original := cfg.Revision
			delete(cfg.ManualOverrides, sku)
			cfg, err = s.SavePublishedSettings(context.Background(), cfg)
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
				require.Equal(t, original, publishConfig(t, s).Revision)
				require.Equal(t, "manual", s.active.Models[sku].Mode)
				return
			}
			require.NoError(t, err)
			require.NotContains(t, cfg.ManualOverrides, sku)
			require.Equal(t, "auto", s.active.Models[sku].Mode)
			cost, err := b.CalculateCost(sku, UsageTokens{OutputTokens: 1_000_000, CacheCreationTokens: 10}, 1)
			require.NoError(t, err)
			require.Zero(t, cost.CacheCreationCost)
			require.Equal(t, auto.Rates[officialOutput]*1_000_000, cost.OutputCost)
		})
	}
}
