package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func priceCoverageOfficialBodies(t *testing.T) map[string][]byte {
	t.Helper()
	bodies := map[string][]byte{modelPricingSyncRegistryURL: []byte(`{"openai":{"models":{"gpt-4.1":{"id":"gpt-4.1","cost":{"input":2,"output":8}}}}}`)}
	for url, file := range map[string]string{officialOpenAIURL: "openai-pricing.md", officialOpenAIModelURL("gpt-6.2-sol"): "sol.md", officialOpenAIModelURL("gpt-6.2-astra"): "astra.md"} {
		b, e := os.ReadFile(filepath.Join("testdata", "price_sync_official", file))
		require.NoError(t, e)
		bodies[url] = b
	}
	return bodies
}

type priceCoverageFetcher map[string][]byte

func (f priceCoverageFetcher) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	return f[url], nil
}

func TestPricePublishCoverageOfficialOpenAITTLReplacesLegacyDimensions(t *testing.T) {
	for _, tc := range []struct {
		model, manual string
		short, long   float64
	}{
		{"gpt-6.2-sol", `{"input":2,"output":10,"cache_read":0.1,"cache_write":2.5,"cache_write_1h":5,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5}`, 0.00125, 0.0025},
		{"gpt-6.2-astra", `{"input":8,"output":40,"cache_read":0.4,"cache_write":10,"cache_write_1h":20,"long_context_threshold":272000,"long_context_input_multiplier":2,"long_context_output_multiplier":1.5}`, 0.005, 0.01},
	} {
		t.Run(tc.model, func(t *testing.T) {
			ctx := context.Background()
			s, b := publishFixture(t, &publishTestStore{})
			s.settings = publishLegacyReader(`{"enabled":false,"manual_overrides":{"` + tc.model + `":` + tc.manual + `}}`)
			s.cfg.Collector = collectPriceSyncV2
			s.fetcher = priceCoverageFetcher(priceCoverageOfficialBodies(t))
			before := publishConfig(t, s)
			require.Contains(t, before.ManualOverrides, tc.model)
			_, err := s.Sync(ctx)
			require.NoError(t, err)
			row := s.active.Models[tc.model]
			require.Empty(t, row.Issue)
			require.NotNil(t, row.Auto)
			require.Equal(t, "manual", row.Mode)
			require.Contains(t, row.Effective.Rates, officialCache1h)
			require.NotContains(t, row.Auto.Rates, officialCache1h)
			require.NotContains(t, row.Auto.Rates, officialCacheWrite)
			after := publishConfig(t, s)
			delete(after.ManualOverrides, tc.model)
			publishSet(t, s, after)
			require.Equal(t, "auto", s.active.Models[tc.model].Mode)
			for _, band := range []struct {
				input int
				want  float64
			}{{271500, tc.short}, {271501, tc.long}} {
				cost, e := b.CalculateCost(tc.model, UsageTokens{InputTokens: band.input, CacheCreationTokens: 500, CacheCreation30mTokens: 500}, 1)
				require.NoError(t, e)
				require.InDelta(t, band.want, cost.CacheCreationCost, 1e-12)
				cost, e = b.CalculateCost(tc.model, UsageTokens{InputTokens: band.input, CacheCreationTokens: 500, CacheCreation12hTokens: 500}, 1)
				require.NoError(t, e)
				require.InDelta(t, 2*band.want, cost.CacheCreationCost, 1e-12)
			}
			_, err = b.CalculateCost(tc.model, UsageTokens{CacheCreationTokens: 500, CacheCreation1hTokens: 500}, 1)
			require.ErrorContains(t, err, officialCache1h)
		})
	}
}

func TestPricePublishCoverageKeepRequiredTTL(t *testing.T) {
	ctx := context.Background()
	got, e := collectPriceSyncV2(ctx, priceCoverageFetcher(priceCoverageOfficialBodies(t)), "", nil)
	require.NoError(t, e)
	for _, name := range []string{"wrong-source", "missing-12h", "missing-long-30m", "wrong-long-boundary", "wrong-identity", "missing-cache-read", "kimi-needs-1h"} {
		t.Run(name, func(t *testing.T) {
			model := "gpt-6.2-sol"
			q := got.Quotes[model]
			q.Rates = priceCoverageClone(q.Rates)
			q.Tiers = append([]PriceSyncTier(nil), q.Tiers...)
			q.Tiers[0].Rates = priceCoverageClone(q.Tiers[0].Rates)
			switch name {
			case "wrong-source":
				q.SourceURL = modelPricingSyncRegistryURL
			case "missing-12h":
				delete(q.Rates, officialCache12h)
			case "missing-long-30m":
				delete(q.Tiers[0].Rates, officialCache30m)
			case "wrong-long-boundary":
				q.Tiers[0].Threshold++
			case "wrong-identity":
				q.Model = "gpt-other"
			case "missing-cache-read":
				delete(q.Rates, officialCacheRead)
			case "kimi-needs-1h":
				model = publishModel
				q = publishQuote(model, 136, 52, "kimi-sentinel")
				delete(q.Rates, officialCache1h)
			}
			s, b := publishFixture(t, &publishTestStore{})
			v := publishConfig(t, s)
			v.Enabled = true
			v.ManualOverrides[model] = publishManual(t, `{"input":13,"output":68,"cache_write_1h":26}`)
			publishSet(t, s, v)
			s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
				return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{model: q}}, nil
			}
			_, e := s.Sync(ctx)
			require.NoError(t, e)
			require.NotEmpty(t, s.active.Models[model].Issue)
			require.Nil(t, s.active.Models[model].Auto)
			v = publishConfig(t, s)
			rev := v.Revision
			delete(v.ManualOverrides, model)
			_, e = s.SavePublishedSettings(ctx, v)
			require.ErrorIs(t, e, ErrPriceSyncConflict)
			require.Equal(t, rev, publishConfig(t, s).Revision)
			publishMeasure(t, b, model, 68, 26)
		})
	}
}

func TestPricePublishCoverageRejectsFixedUnitSchedulesPerSKU(t *testing.T) {
	for _, tt := range []string{"window", "tier"} {
		t.Run(tt, func(t *testing.T) {
			ctx := context.Background()
			s, bill := publishFixture(t, &publishTestStore{})
			const video, text = "coverage-video", "coverage-text"
			quote := func(model, mode string, rates map[string]float64, evidence string) PriceSyncQuote {
				return PriceSyncQuote{Model: model, Provider: "fixture", Currency: "USD", SourceURL: modelPricingSyncRegistryURL, Evidence: evidence, VerifiedAt: time.Now().UTC(), BillingMode: mode, Rates: rates}
			}
			first := quote(video, "per_second", map[string]float64{"cost_per_second": 0.40}, "original")
			batch := map[string]PriceSyncQuote{video: first}
			s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
				return PriceSyncCollection{Quotes: batch}, nil
			}
			applied, err := s.Sync(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, applied)
			costVideo := func() float64 {
				cost, e := bill.CalculateCostUnified(CostInput{Ctx: ctx, Model: video, UsageUnits: 2.5, RequestCount: 1, RateMultiplier: 1})
				require.NoError(t, e)
				return cost.ActualCost
			}
			require.InDelta(t, 1, costVideo(), 1e-12)
			bad := quote(video, "per_second", map[string]float64{"cost_per_second": 0.80}, "invalid-schedule")
			if tt == "window" {
				bad.Windows = []PriceSyncWindow{{Timezone: "UTC", Start: "00:00", End: "12:00", Rates: map[string]float64{"cost_per_second": 0.95}}}
			} else {
				bad.Tiers = []PriceSyncTier{{Threshold: 100, Rates: map[string]float64{"cost_per_second": 0.95}}}
			}
			batch = map[string]PriceSyncQuote{video: bad, text: quote(text, "token", map[string]float64{officialInput: 1e-6, officialOutput: 9e-6}, "independent-valid")}
			applied, err = s.Sync(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, applied)
			row := s.active.Models[video]
			require.Contains(t, row.Issue, "token")
			require.Equal(t, first.Evidence, row.Effective.Evidence)
			require.Equal(t, first.VerifiedAt, row.Auto.VerifiedAt)
			require.InDelta(t, 1, costVideo(), 1e-12)
			cost, err := bill.CalculateCost(text, UsageTokens{OutputTokens: 1000}, 1)
			require.NoError(t, err)
			require.InDelta(t, 0.009, cost.ActualCost, 1e-12)
		})
	}
}

func priceCoverageClone(src map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
