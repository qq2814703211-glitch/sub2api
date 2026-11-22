package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deliberately unrelated component prices (site units/token). The second band
// has a free output and a cheaper 1h write; neither is an input multiplier.
const tariffLadderFixture = `{"tariff-test": {
 "input_cost_per_token":1,"output_cost_per_token":2,"cache_read_input_token_cost":3,
 "cache_creation_input_token_cost":4,"cache_creation_input_token_cost_above_1hr":9,
 "glory_price_tiers":[
 {"threshold":10,"inclusive":true,"rates":{"input_cost_per_token":2,"output_cost_per_token":7,"cache_read_input_token_cost":5,"cache_creation_input_token_cost":3,"cache_creation_input_token_cost_above_1hr":11}},
 {"threshold":20,"inclusive":false,"rates":{"input_cost_per_token":3,"output_cost_per_token":0,"cache_read_input_token_cost":13,"cache_creation_input_token_cost":4,"cache_creation_input_token_cost_above_1hr":1}}]}}`

func TestPriceSyncTariffUnifiedSecondStrictBoundary(t *testing.T) {
	billing := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, tariffLadderFixture))
	resolver := NewModelPricingResolver(nil, billing)
	for _, tc := range []struct {
		name                         string
		input                        int
		in, out, read, write, actual float64
	}{
		{"below-first", 5, 5, 8, 6, 13, 48},
		{"first-inclusive", 6, 12, 28, 10, 14, 96},
		{"below-second", 15, 30, 28, 10, 14, 123},
		{"at-second-stays-first", 16, 32, 28, 10, 14, 126},
		{"above-second", 17, 51, 0, 26, 5, 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "tariff-test", Resolver: resolver, RateMultiplier: 1.5,
				Tokens: UsageTokens{InputTokens: tc.input, OutputTokens: 4, CacheReadTokens: 2, CacheCreationTokens: 2, CacheCreation5mTokens: 1, CacheCreation1hTokens: 1}})
			require.NoError(t, err)
			assert.Equal(t, tc.in, cost.InputCost)
			assert.Equal(t, tc.out, cost.OutputCost)
			assert.Equal(t, tc.read, cost.CacheReadCost)
			assert.Equal(t, tc.write, cost.CacheCreationCost)
			assert.Equal(t, tc.actual, cost.ActualCost)
		})
	}
}
