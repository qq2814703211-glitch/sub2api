package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPriceSyncTariffViewShowsEveryBreakAndExplicitZero(t *testing.T) {
	b := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, tariffLadderFixture))
	r := NewModelPricingResolver(nil, b)
	sched, err := b.ResolveContextPricingSchedule(context.Background(), r, ContextPricingScheduleInput{Model: "tariff-test"})
	require.NoError(t, err)
	require.Len(t, sched.Tiers, 3)
	got := plazaPricingFromSchedule(nil, sched)
	require.Len(t, got.Intervals, 3)
	for i, want := range []struct {
		min                int
		max                *int
		in, out, cr, cw, h float64
	}{{0, tariffTestPtr(9), 1, 2, 3, 4, 9}, {9, tariffTestPtr(20), 2, 7, 5, 3, 11}, {20, nil, 3, 0, 13, 4, 1}} {
		iv := got.Intervals[i]
		require.Equal(t, want.min, iv.MinTokens)
		require.Equal(t, want.max, iv.MaxTokens)
		require.Equal(t, &want.in, iv.InputPrice)
		require.Equal(t, &want.out, iv.OutputPrice)
		require.Equal(t, &want.cr, iv.CacheReadPrice)
		require.Equal(t, &want.cw, iv.CacheWritePrice)
		require.Equal(t, &want.h, iv.CacheWrite1hPrice)
	}
}
func tariffTestPtr[T any](v T) *T { return &v }

func TestPriceSyncTariffTTLAndServiceRate(t *testing.T) {
	const row = `{"gpt-6.2-astra":{"glory_billing_mode":"token","glory_price_source":{"provider":"openai"},"input_cost_per_token":2,"output_cost_per_token":3,"cache_read_input_token_cost":4,
 "cache_creation_input_token_cost":5,"cache_creation_input_token_cost_above_1hr":6,"cache_creation_input_token_cost_30m":7,"cache_creation_input_token_cost_12h":11,
 "glory_price_tiers":[{"threshold":10,"rates":{"input_cost_per_token":3,"output_cost_per_token":13,"cache_read_input_token_cost":11,"cache_creation_input_token_cost":8,"cache_creation_input_token_cost_above_1hr":5,"cache_creation_input_token_cost_30m":0,"cache_creation_input_token_cost_12h":17}}]}}`
	b := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, row))
	r := NewModelPricingResolver(nil, b)
	for _, tc := range []struct {
		name, st             string
		input                int
		write, in, out, read float64
	}{{"exact", "", 6, 25, 12, 6, 4}, {"above", "", 7, 17, 21, 26, 11}, {"above-fast", "priority", 7, 34, 42, 52, 22}} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := b.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gpt-6.2-astra", Resolver: r, ServiceTier: tc.st, RateMultiplier: 1.5,
				Tokens: UsageTokens{InputTokens: tc.input, OutputTokens: 2, CacheReadTokens: 1, CacheCreationTokens: 3, CacheCreation30mTokens: 2, CacheCreation12hTokens: 1}})
			require.NoError(t, err)
			require.Equal(t, tc.write, cost.CacheCreationCost)
			require.Equal(t, tc.in, cost.InputCost)
			require.Equal(t, tc.out, cost.OutputCost)
			require.Equal(t, tc.read, cost.CacheReadCost)
			require.Equal(t, 1.5*(tc.in+tc.out+tc.read+tc.write), cost.ActualCost)
		})
	}
}

func TestPriceSyncTariffPartialOperatorPatchOutranksBand(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.OverrideFile = filepath.Join(t.TempDir(), "override.json")
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(`{"tariff-test":{"input_cost_per_token":0,"cache_creation_input_token_cost_above_1hr":77}}`), 0600))
	p := NewPricingService(cfg, nil)
	var err error
	p.pricingData, err = p.parsePricingData([]byte(tariffLadderFixture))
	require.NoError(t, err)
	b := NewBillingService(cfg, p)
	cost, err := b.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "tariff-test", Resolver: NewModelPricingResolver(nil, b), RateMultiplier: 1, Tokens: UsageTokens{InputTokens: 21, OutputTokens: 1, CacheReadTokens: 1, CacheCreationTokens: 2, CacheCreation5mTokens: 1, CacheCreation1hTokens: 1}})
	require.NoError(t, err)
	require.Equal(t, 0., cost.InputCost)
	require.Equal(t, 0., cost.OutputCost)
	require.Equal(t, 13., cost.CacheReadCost)
	require.Equal(t, 81., cost.CacheCreationCost)
}

func TestPriceSyncTariffInvalidReloadKeepsQuote(t *testing.T) {
	const model = "tariff-test"
	for _, tc := range []struct{ name, from, to, why string }{
		{"duplicate", `"threshold":20`, `"threshold":10`, "threshold"},
		{"null", `"output_cost_per_token":0`, `"output_cost_per_token":null`, "output_cost_per_token"},
		{"missing", `"output_cost_per_token":0,`, "", "output_cost_per_token"},
		{"negative", `"cache_read_input_token_cost":13`, `"cache_read_input_token_cost":-1`, "cache_read_input_token_cost"},
		{"unknown", `"cache_read_input_token_cost":13`, `"unpriced_dimension":1,"cache_read_input_token_cost":13`, "unpriced_dimension"},
		{"cross-product", `"glory_price_tiers"`, `"glory_price_windows":[{"timezone":"UTC","start":"01:00","end":"02:00","rates":{"input_cost_per_token":3}}],"glory_price_tiers"`, "joint quote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Pricing.DataDir = t.TempDir()
			p := NewPricingService(cfg, nil)
			path := p.getPricingFilePath()
			require.NoError(t, os.WriteFile(path, []byte(tariffLadderFixture), 0600))
			require.NoError(t, p.loadPricingData(path))
			bad := strings.Replace(tariffLadderFixture, tc.from, tc.to, 1)
			require.NotEqual(t, tariffLadderFixture, bad)
			require.True(t, json.Valid([]byte(bad)))
			require.NoError(t, os.WriteFile(path, []byte(bad), 0600))
			require.ErrorContains(t, p.loadPricingData(path), tc.why)
			b := NewBillingService(cfg, p)
			cost, err := b.CalculateCost(model, UsageTokens{InputTokens: 21, OutputTokens: 1}, 1)
			require.NoError(t, err)
			require.Equal(t, 63., cost.TotalCost)
		})
	}
}

func TestPriceSyncTariffOvernightStartWeekdayAndOverlap(t *testing.T) {
	const row = `{"tariff-night":{"input_cost_per_token":2,"output_cost_per_token":1,"glory_price_windows":[{"timezone":"America/New_York","weekdays":[5],"start":"22:00","end":"02:00","rates":{"input_cost_per_token":7,"output_cost_per_token":1}}]}}`
	for _, tc := range []struct {
		at   string
		want float64
	}{{"2026-11-07T02:59:00Z", 2}, {"2026-11-07T03:00:00Z", 7}, {"2026-11-07T06:59:00Z", 7}, {"2026-11-07T07:00:00Z", 2}, {"2026-11-08T06:00:00Z", 2}} {
		at, err := time.Parse(time.RFC3339, tc.at)
		require.NoError(t, err)
		b := NewBillingService(nil, newStubPricingServiceFromJSON(t, row))
		c, err := b.CalculateCostUnified(CostInput{Model: "tariff-night", PricingAt: at, RateMultiplier: 1, Tokens: UsageTokens{InputTokens: 1}})
		require.NoError(t, err)
		require.Equal(t, tc.want, c.TotalCost)
	}
}
