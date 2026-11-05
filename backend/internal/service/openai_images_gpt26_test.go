//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPTImage26OfficialPricing(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		t.Run(model, func(t *testing.T) {
			// Deliberately stale catalog: 2.6 must not inherit legacy image prices.
			catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				"gpt-image-2": {InputCostPerToken: 2.5e-6, OutputCostPerImageToken: 15e-6},
			}}
			price := catalog.GetModelPricing(model)
			require.NotNil(t, price)
			require.Equal(t, 5e-6, price.InputCostPerToken)
			require.Equal(t, 1.25e-6, price.CacheReadInputTokenCost)
			require.Equal(t, 8e-6, price.InputCostPerImageToken)
			require.Equal(t, 2e-6, price.CacheReadInputImageTokenCost)
			require.Equal(t, 30e-6, price.OutputCostPerImageToken)
			require.Zero(t, price.OutputCostPerToken)

			for _, source := range []*PricingService{nil, catalog} {
				billing := NewBillingService(&config.Config{}, source)
				p, err := billing.GetModelPricing(model)
				require.NoError(t, err)
				require.Equal(t, 5e-6, p.InputPricePerToken)
				require.Equal(t, 8e-6, p.ImageInputPricePerToken)
				require.Equal(t, 30e-6, p.ImageOutputPricePerToken)
				cost := billing.computeTokenBreakdown(p, UsageTokens{
					InputTokens: 371, ImageInputTokens: 352, OutputTokens: 439, ImageOutputTokens: 439,
				}, 1, "", false)
				require.InDelta(t, 0.016081, cost.TotalCost, 1e-12)
				require.Greater(t, cost.ImageOutputCost, 0.0, "the first image must be billed")
			}

			custom := &LiteLLMModelPricing{InputCostPerToken: 7e-6, OutputCostPerImageToken: 35e-6}
			catalog.pricingData[model] = custom
			require.Same(t, custom, catalog.GetModelPricing(model), "operator pricing wins")
		})
	}
}

func TestGPTImage26BareAliasRequiresExplicitPricing(t *testing.T) {
	catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-image-2": {InputCostPerToken: 2.5e-6, OutputCostPerImageToken: 15e-6},
	}}
	require.Nil(t, catalog.GetModelPricing("gpt-image-2.6"), "a custom alias must not guess legacy model rates")
	custom := &LiteLLMModelPricing{InputCostPerToken: 7e-6, OutputCostPerImageToken: 35e-6}
	catalog.pricingData["gpt-image-2.6"] = custom
	require.Same(t, custom, catalog.GetModelPricing("gpt-image-2.6"))
}
