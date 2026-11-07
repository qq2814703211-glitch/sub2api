//go:build unit

package service

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestProductionOpenAIPricingOverrideShortLongAndFast(t *testing.T) {
	const catalogPath = "../../../model-pricing-current.json"
	const overridePath = "../../../production-model-pricing-overrides.json"
	catalog, err := os.ReadFile(catalogPath)
	require.NoError(t, err)
	override, err := os.ReadFile(overridePath)
	require.NoError(t, err)

	var source map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(catalog, &source))
	var patches map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(override, &patches))
	require.Equal(t, 2, len(patches))
	for model, patch := range patches {
		base, ok := source[model]
		require.True(t, ok, "catalog entry missing for %s", model)
		merged, ok := mergePricingOverrideEntry(base, patch)
		require.True(t, ok)
		source[model] = merged
	}
	mergedCatalog, err := json.Marshal(source)
	require.NoError(t, err)
	pricing := newStubPricingServiceFromJSON(t, string(mergedCatalog))
	billing := NewBillingService(&config.Config{}, pricing)

	tests := []struct {
		model                                      string
		input, output, write, read                 float64
		longInput, longOutput, longWrite, longRead float64
	}{
		{"gpt-5.6-sol", 4e-6, 20e-6, 5e-6, .4e-6, 8e-6, 30e-6, 10e-6, .8e-6},
		{"gpt-6.1-terra", 1e-6, 5e-6, 1.25e-6, .05e-6, 2e-6, 7.5e-6, 2.5e-6, .1e-6},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := pricing.GetModelPricing(tt.model)
			require.NotNil(t, p)
			require.Equal(t, 272000, p.LongContextInputTokenThreshold)
			require.InDelta(t, 2, p.LongContextInputCostMultiplier, 1e-12)
			require.InDelta(t, 1.5, p.LongContextOutputCostMultiplier, 1e-12)

			shortWriteTier, err := billing.CalculateCost(tt.model, UsageTokens{InputTokens: 100, OutputTokens: 100, CacheCreationTokens: 100, CacheCreation5mTokens: 100}, 1)
			require.NoError(t, err)
			require.InDelta(t, 100*tt.write, shortWriteTier.CacheCreationCost, 1e-12)
			longWriteTier, err := billing.CalculateCost(tt.model, UsageTokens{InputTokens: 100, OutputTokens: 100, CacheCreationTokens: 100, CacheCreation1hTokens: 100}, 1)
			require.NoError(t, err)
			require.InDelta(t, 100*tt.write, longWriteTier.CacheCreationCost, 1e-12)
			boundary, err := billing.CalculateCost(tt.model, UsageTokens{InputTokens: 271800, OutputTokens: 100, CacheCreationTokens: 100, CacheReadTokens: 100}, 1)
			require.NoError(t, err)
			require.False(t, boundary.LongContextBillingApplied)
			require.InDelta(t, 271800*tt.input, boundary.InputCost, 1e-12)
			require.InDelta(t, 100*tt.output, boundary.OutputCost, 1e-12)
			require.InDelta(t, 100*tt.write, boundary.CacheCreationCost, 1e-12)
			require.InDelta(t, 100*tt.read, boundary.CacheReadCost, 1e-12)

			long, err := billing.CalculateCost(tt.model, UsageTokens{InputTokens: 271801, OutputTokens: 100, CacheCreationTokens: 100, CacheReadTokens: 100}, 1)
			require.NoError(t, err)
			require.True(t, long.LongContextBillingApplied)
			require.InDelta(t, 271801*tt.longInput, long.InputCost, 1e-12)
			require.InDelta(t, 100*tt.longOutput, long.OutputCost, 1e-12)
			require.InDelta(t, 100*tt.longWrite, long.CacheCreationCost, 1e-12)
			require.InDelta(t, 100*tt.longRead, long.CacheReadCost, 1e-12)

			fast, err := billing.CalculateCostWithServiceTier(tt.model, UsageTokens{InputTokens: 271801, OutputTokens: 100, CacheCreationTokens: 100, CacheReadTokens: 100}, 1, "fast")
			require.NoError(t, err)
			require.True(t, fast.LongContextBillingApplied)
			require.InDelta(t, 271801*tt.longInput*2, fast.InputCost, 1e-12)
			require.InDelta(t, 100*tt.longOutput*2, fast.OutputCost, 1e-12)
			require.InDelta(t, 100*tt.longWrite*2, fast.CacheCreationCost, 1e-12)
			require.InDelta(t, 100*tt.longRead*2, fast.CacheReadCost, 1e-12)
			require.False(t, math.IsNaN(fast.TotalCost))
		})
	}
}
