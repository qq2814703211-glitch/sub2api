//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT26PerImagePriceHierarchy(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, size := range []struct {
			name  string
			scale float64
		}{{"1K", 1}, {"2K", 1.5}, {"4K", 2}} {
			for _, mode := range []string{"legacy", "exact", "empty_catalog", "channel", "group_over_channel", "group_card_over_channel", "group_free"} {
				for _, independent := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/independent=%t", model, size.name, mode, independent), func(t *testing.T) {
						catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-image-2": {OutputCostPerImage: 0.42, InputCostPerToken: 9, OutputCostPerToken: 9}}}
						require.Nil(t, catalog.GetModelPricing(model), "legacy per-image pricing must never restore token fallback")
						wantUnit := 0.42 * size.scale
						if mode == "exact" {
							catalog.pricingData[model] = &LiteLLMModelPricing{OutputCostPerImage: 0.65}
							wantUnit = 0.65 * size.scale
						}
						if mode == "empty_catalog" {
							catalog.pricingData = nil
							wantUnit = 0.134 * size.scale
						}
						usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
						userRepo := &openAIRecordUsageUserRepoStub{}
						svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
						svc.billingService = NewBillingService(&config.Config{}, catalog)
						svc.resolver = NewModelPricingResolver(nil, svc.billingService)
						group := &Group{ID: 123, RateMultiplier: 0.5, ImageRateIndependent: independent, ImageRateMultiplier: 0.25}
						if mode == "channel" || mode == "group_over_channel" || mode == "group_card_over_channel" {
							svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, 123, model, 0.25)
							svc.resolver.billingService = svc.billingService
							wantUnit = 0.25
						}
						if mode == "group_over_channel" || mode == "group_free" {
							p := 0.18
							if mode == "group_free" {
								p = 0
							}
							group.ImagePrice1K = &p
							group.ImagePrice2K = &p
							group.ImagePrice4K = &p
							wantUnit = p
						}
						if mode == "group_card_over_channel" {
							group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeImage, PerRequestPrice: f64p(0.12)}}
							wantUnit = 0.12
						}
						err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: &OpenAIForwardResult{RequestID: "price-matrix", Model: model, UpstreamModel: model, ImageEndpoint: true, ImageCount: 3, ImageSize: size.name},
							APIKey: &APIKey{ID: 10123, GroupID: i64p(123), Group: group}, User: &User{ID: 20123}, Account: &Account{ID: 30123},
						})
						require.NoError(t, err)
						require.NotNil(t, usageRepo.lastLog)
						multiplier := 0.5
						if independent {
							multiplier = 0.25
						}
						require.InDelta(t, 3*wantUnit, usageRepo.lastLog.TotalCost, 1e-12)
						require.InDelta(t, 3*wantUnit*multiplier, usageRepo.lastLog.ActualCost, 1e-12)
						require.InDelta(t, 3*wantUnit*multiplier, userRepo.lastAmount, 1e-12)
						require.Equal(t, 3, usageRepo.lastLog.ImageCount)
						if wantUnit > 0 {
							require.Equal(t, 1, userRepo.deductCalls)
						}
					})
				}
			}
		}
	}
}
