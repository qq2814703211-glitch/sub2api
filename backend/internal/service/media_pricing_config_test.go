//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMediaPricingConfigurationValidationAndPlaza(t *testing.T) {
	pricing := ChannelModelPricing{Models: []string{"fixture"}, Platform: PlatformOpenAI, BillingMode: BillingModeImage, MediaPricing: mediaTestPricing("image", 0.2, MediaPricingConditions{Layer: mediaTestPtr(false)})}
	require.NoError(t, validatePricingEntries([]ChannelModelPricing{pricing}))
	normalized, err := normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{pricing})
	require.NoError(t, err)
	require.Equal(t, pricing.MediaPricing, normalized[0].MediaPricing)
	require.False(t, pricingNeedsFallback(&pricing))
	require.NoError(t, validateAccountStatsPricingRules([]AccountStatsPricingRule{{Pricing: []ChannelModelPricing{pricing}}}))
	price := 10.0
	group := &Group{ModelPricing: []ChannelModelPricing{pricing}, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
	plaza := &ModelPlazaService{billingService: &BillingService{}, resolver: NewModelPricingResolver(nil, &BillingService{})}
	model := &PlazaModel{Name: "fixture", Platform: PlatformOpenAI}
	plaza.fillDisplayPricing(context.Background(), model, group)
	require.Equal(t, pricing.MediaPricing, model.Pricing.MediaPricing)
	require.Empty(t, model.Pricing.Intervals, "scalar tier synthesis cannot overwrite a media card")
	require.Nil(t, model.Pricing.PerRequestPrice)
	*model.Pricing.MediaPricing.Rules[0].Price = 3
	require.Equal(t, 0.2, *group.ModelPricing[0].MediaPricing.Rules[0].Price)
	pricing.MediaPricing.Rules[0].Price = nil
	require.Error(t, validatePricingEntries([]ChannelModelPricing{pricing}))
	_, err = normalizeGroupModelPricing(PlatformOpenAI, []ChannelModelPricing{pricing})
	require.Error(t, err)
	require.Error(t, validateAccountStatsPricingRules([]AccountStatsPricingRule{{Pricing: []ChannelModelPricing{pricing}}}))
}

func TestMediaPricingAccountStatsExplicitZeroAndError(t *testing.T) {
	groupID := int64(10)
	stats := mediaTestPricing("image", 0.0, MediaPricingConditions{Layer: mediaTestPtr(false)})
	channel := Channel{ID: 1, Name: "fixture", Status: StatusActive, GroupIDs: []int64{groupID}, AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{20}, Pricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"fixture-image"}, BillingMode: BillingModeImage, MediaPricing: stats}}}}}
	cs := &ChannelService{}
	cs.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{groupID: PlatformOpenAI}))
	log := &UsageLog{ImageCount: 1}
	err := applyAccountStatsCost(context.Background(), log, cs, &BillingService{}, 20, groupID, "fixture-image", "fixture-image", UsageTokens{}, 3, time.Time{}, false, &MediaUsage{Count: 1, Layer: mediaTestPtr(false)})
	require.NoError(t, err)
	require.NotNil(t, log.AccountStatsCost)
	require.Zero(t, *log.AccountStatsCost)
	log.AccountStatsCost = nil
	err = applyAccountStatsCost(context.Background(), log, cs, &BillingService{}, 20, groupID, "fixture-image", "fixture-image", UsageTokens{}, 3, time.Time{}, false, &MediaUsage{Count: 1})
	require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	require.Nil(t, log.AccountStatsCost)
}

func TestMediaPricingGroupDuplicatePreservesIndependentRules(t *testing.T) {
	source := &Group{Name: "media", ModelPricing: []ChannelModelPricing{{Models: []string{"fixture"}, MediaPricing: mediaTestPricing("image", 0.2, MediaPricingConditions{Layer: mediaTestPtr(false)})}}}
	copied := cloneGroupForDuplicate(source, "fixture-operation")
	require.Equal(t, source.ModelPricing, copied.ModelPricing)
	*copied.ModelPricing[0].MediaPricing.Rules[0].Price = 5
	require.Equal(t, 0.2, *source.ModelPricing[0].MediaPricing.Rules[0].Price)
}

func TestMediaPricingUnifiedPreResolvedDoesNotNeedResolver(t *testing.T) {
	billing := &BillingService{}
	card := ChannelModelPricing{Models: []string{"fixture"}, BillingMode: BillingModeImage, MediaPricing: mediaTestPricing("image", 0.2, MediaPricingConditions{})}
	resolved := NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "fixture", Group: &Group{ModelPricing: []ChannelModelPricing{card}}})
	got, err := billing.CalculateCostUnified(CostInput{Model: "fixture", Resolved: resolved, MediaUsage: &MediaUsage{Count: 1}, RateMultiplier: 2})
	require.NoError(t, err)
	require.InDelta(t, 0.4, got.ActualCost, 1e-12)
}

func TestMediaPricingNilPreservesLegacyCost(t *testing.T) {
	billing := &BillingService{}
	group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"fixture-image"}, BillingMode: BillingModeImage, PerRequestPrice: mediaTestPtr(0.2)}}}
	input := CostInput{Ctx: context.Background(), Model: "fixture-image", Group: group, Resolver: NewModelPricingResolver(nil, billing), RequestCount: 3, RateMultiplier: 2}
	old, err := billing.CalculateCostUnified(input)
	require.NoError(t, err)
	input.MediaUsage = &MediaUsage{Count: 0, Layer: mediaTestPtr(true), Pixels: 4096}
	got, err := billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Equal(t, old, got)
	require.InDelta(t, 1.2, got.ActualCost, 1e-12)
}
