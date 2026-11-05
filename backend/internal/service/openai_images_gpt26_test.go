//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPTImage26UsesConfiguredSettlementPrices(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		t.Run(model, func(t *testing.T) {
			catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				"gpt-image-2": {InputCostPerToken: 2.5e-6, OutputCostPerImageToken: 15e-6},
			}}
			require.Nil(t, catalog.GetModelPricing(model), "new CNY-billed models must not inherit USD or legacy fallback rates")
			billing := NewBillingService(&config.Config{}, nil)
			require.Nil(t, billing.getFallbackPricing(model))

			// Synthetic operator-configured settlement units; not an official price.
			custom := &LiteLLMModelPricing{
				InputCostPerToken: 19e-6, InputCostPerImageToken: 23e-6,
				OutputCostPerImageToken: 41e-6,
			}
			catalog.pricingData[model] = custom
			require.Same(t, custom, catalog.GetModelPricing(model))
			billing = NewBillingService(&config.Config{}, catalog)
			p, err := billing.GetModelPricing(model)
			require.NoError(t, err)
			require.Equal(t, 19e-6, p.InputPricePerToken)
			require.Equal(t, 23e-6, p.ImageInputPricePerToken)
			require.Equal(t, 41e-6, p.ImageOutputPricePerToken)
			cost := billing.computeTokenBreakdown(p, UsageTokens{
				InputTokens: 10, ImageInputTokens: 4, OutputTokens: 5, ImageOutputTokens: 5,
			}, 1, "", false)
			require.InDelta(t, 6*19e-6+4*23e-6+5*41e-6, cost.TotalCost, 1e-12)
			require.Greater(t, cost.ImageOutputCost, 0.0, "the first image is billed normally")
		})
	}
}
