package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const tariffWindowFixture = `{"deepseek-flash":{"glory_price_source":{"currency":"CNY","provider":"deepseek"}, "glory_billing_mode":"token", "cny_official":true,
 "input_cost_per_token":2,"output_cost_per_token":3,"cache_read_input_token_cost":4,
 "glory_price_windows":[{"timezone":"Asia/Shanghai","weekdays":[1,2,3,4,5],"start":"09:00","end":"12:00","exclude_cn_holidays":true,
 "rates":{"input_cost_per_token":7,"output_cost_per_token":0,"cache_read_input_token_cost":9}}]}}`

func TestPriceSyncTariffWindowSettlement(t *testing.T) {
	billing := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, tariffWindowFixture))
	resolver := NewModelPricingResolver(nil, billing)
	tokens := UsageTokens{InputTokens: 2, OutputTokens: 3, CacheReadTokens: 1}
	for _, tc := range []struct {
		name, at       string
		in, out, cache float64
		unknown        bool
	}{
		{"before", "2026-11-09T00:59:59Z", 4, 9, 4, false},
		{"start", "2026-11-09T01:00:00Z", 14, 0, 9, false},
		{"last-second", "2026-11-09T03:59:59Z", 14, 0, 9, false},
		{"end", "2026-11-09T04:00:00Z", 4, 9, 4, false},
		{"weekend", "2026-11-14T02:00:00Z", 4, 9, 4, false},
		{"statutory-holiday", "2026-10-02T02:00:00Z", 4, 9, 4, false},
		{"holiday-next-year", "2027-02-08T02:00:00Z", 4, 9, 4, false},
		{"calendar-needed", "2028-03-06T02:00:00Z", 0, 0, 0, true},
		{"calendar-not-needed", "2028-03-06T14:00:00Z", 4, 9, 4, false},
	} {
		for _, r := range []*ModelPricingResolver{resolver, nil} {
			path := "with-resolver"
			if r == nil {
				path = "without-resolver"
			}
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				at, err := time.Parse(time.RFC3339, tc.at)
				require.NoError(t, err)
				got, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "deepseek-flash", PricingAt: at, Tokens: tokens, RateMultiplier: 2, Resolver: r})
				if tc.unknown {
					require.ErrorContains(t, err, "calendar")
					require.ErrorContains(t, err, "2028")
					require.Nil(t, got)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.in, got.InputCost)
				require.Equal(t, tc.out, got.OutputCost)
				require.Equal(t, tc.cache, got.CacheReadCost)
				require.Equal(t, (tc.in+tc.out+tc.cache)*2, got.ActualCost)
			})
		}
	}
}

func TestPriceSyncTariffCompatibilityAndToggle(t *testing.T) {
	billing := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, tariffLadderFixture))
	tokens := UsageTokens{InputTokens: 21, OutputTokens: 2}
	resolver := NewModelPricingResolver(nil, billing)
	for _, e := range []bool{true, false} {
		want := float64(25)
		if e {
			want = 63
		}
		c, err := billing.calculateCostInternalWithPolicy("tariff-test", tokens, 1, "", nil, e)
		require.NoError(t, err)
		require.Equal(t, want, c.TotalCost)
		for _, r := range []*ModelPricingResolver{nil, resolver} {
			c, err = billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "tariff-test", Tokens: tokens, RateMultiplier: 1, Resolver: r, Group: &Group{LongContextPricingEnabled: e}, LongContextBillingEnabled: &e})
			require.NoError(t, err)
			require.Equal(t, want, c.TotalCost)
			require.Equal(t, e, c.LongContextBillingApplied)
		}
	}
}
