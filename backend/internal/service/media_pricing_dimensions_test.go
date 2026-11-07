//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mediaTestPtr[T any](v T) *T { return &v }
func mediaTestPricing(unit string, price float64, c MediaPricingConditions) *MediaPricing {
	return &MediaPricing{Rules: []MediaPricingRule{{Name: "fixture", Unit: unit, Price: &price, Conditions: c}}}
}

func TestMediaPricingPixelLayersAndPromptOnce(t *testing.T) {
	p := &MediaPricing{Rules: []MediaPricingRule{
		{Name: "small-flat", Unit: "image", Price: mediaTestPtr(0.1), PromptPrice: mediaTestPtr(0.03), Conditions: MediaPricingConditions{Layer: mediaTestPtr(false), MaxPixels: mediaTestPtr(int64(1048576))}},
		{Name: "large-layer", Unit: "image", Price: mediaTestPtr(0.4), PromptPrice: mediaTestPtr(0.03), Conditions: MediaPricingConditions{Layer: mediaTestPtr(true), MinPixels: mediaTestPtr(int64(1048577))}},
	}}
	u := &MediaUsage{Count: 2, Images: []MediaUsage{{Width: 1024, Height: 1024, Layer: mediaTestPtr(false)}, {Pixels: 1048577, Layer: mediaTestPtr(true)}}}
	got, err := CalculateMediaPricingCost(p, u, UsageTokens{}, 1.5)
	require.NoError(t, err)
	require.InDelta(t, 0.5, got.ImageOutputCost, 1e-12)
	require.InDelta(t, 0.03, got.InputCost, 1e-12)
	require.InDelta(t, 0.53, got.TotalCost, 1e-12)
	require.InDelta(t, 0.795, got.ActualCost, 1e-12)
	// A success list is authoritative even when an older producer omits Count.
	u.Count = 0
	again, err := CalculateMediaPricingCost(p, u, UsageTokens{}, 1.5)
	require.NoError(t, err)
	require.Equal(t, got, again)
}

func TestMediaPricingUnitsAndVideoDimensions(t *testing.T) {
	conditions := MediaPricingConditions{Resolution: "1080p", Mode: "pro", InputType: "image", Audio: mediaTestPtr(true), ReferenceVideo: mediaTestPtr(false), MinDuration: mediaTestPtr(2.0), MaxDuration: mediaTestPtr(5.0), FPS: mediaTestPtr(24.0), MinFPS: mediaTestPtr(20.0), MaxFPS: mediaTestPtr(30.0)}
	u := &MediaUsage{Count: 2, Resolution: "1080P", Mode: "pro", InputType: "image", Audio: mediaTestPtr(true), ReferenceVideo: mediaTestPtr(false), DurationSeconds: 2.5, FPS: 24}
	for _, tt := range []struct {
		unit string
		want float64
	}{{"image", 0.4}, {"second", 1}, {"frame", 24}, {"request", 0.2}} {
		t.Run(tt.unit, func(t *testing.T) {
			got, err := CalculateMediaPricingCost(mediaTestPricing(tt.unit, 0.2, conditions), u, UsageTokens{}, 2)
			require.NoError(t, err)
			require.InDelta(t, tt.want, got.TotalCost, 1e-12)
			require.InDelta(t, tt.want*2, got.ActualCost, 1e-12)
		})
	}
	u.Frames = 61
	got, err := CalculateMediaPricingCost(mediaTestPricing("frame", 0.2, conditions), u, UsageTokens{}, 1)
	require.NoError(t, err)
	require.InDelta(t, 24.4, got.TotalCost, 1e-12, "actual frame count overrides inferred duration × FPS")
}

func TestMediaPricingFramesUseReportedDecimalValues(t *testing.T) {
	got, err := CalculateMediaPricingCost(mediaTestPricing("frame", 0.01, MediaPricingConditions{}), &MediaUsage{Count: 1, DurationSeconds: 0.29, FPS: 100}, UsageTokens{}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.29, got.TotalCost, 1e-12)
}

func TestMediaPricingSelectionAndUnknownMetadata(t *testing.T) {
	p := &MediaPricing{Rules: []MediaPricingRule{
		{Name: "default", Unit: "image", Price: mediaTestPtr(0.1)},
		{Name: "layered", Unit: "image", Price: mediaTestPtr(0.4), Conditions: MediaPricingConditions{Layer: mediaTestPtr(true)}},
		{Name: "later-tie", Unit: "image", Price: mediaTestPtr(0.9), Conditions: MediaPricingConditions{Layer: mediaTestPtr(true)}},
	}}
	got, err := CalculateMediaPricingCost(p, &MediaUsage{Count: 1, Layer: mediaTestPtr(true)}, UsageTokens{}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.4, got.TotalCost, 1e-12)
	_, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{}, 1)
	require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	got, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1, Layer: mediaTestPtr(false)}, UsageTokens{}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.1, got.TotalCost, 1e-12)
	for _, c := range []MediaPricingConditions{
		{Audio: mediaTestPtr(false)}, {ReferenceVideo: mediaTestPtr(false)}, {Layer: mediaTestPtr(false)},
		{MinPixels: mediaTestPtr(int64(0))}, {MinDuration: mediaTestPtr(0.0)}, {MinFPS: mediaTestPtr(0.0)},
		{Resolution: "1K"}, {Mode: "standard"}, {InputType: "text"},
	} {
		_, err := CalculateMediaPricingCost(mediaTestPricing("image", 1, c), &MediaUsage{Count: 1}, UsageTokens{}, 1)
		require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	}
	_, err = CalculateMediaPricingCost(mediaTestPricing("image", 1, MediaPricingConditions{MinPixels: mediaTestPtr(int64(1000))}), &MediaUsage{Count: 1, Resolution: "1K"}, UsageTokens{}, 1)
	require.ErrorIs(t, err, ErrMediaUsageIncomplete, "1K must not be interpreted as a pixel count")
	_, err = CalculateMediaPricingCost(mediaTestPricing("image", 1, MediaPricingConditions{Mode: "pro"}), &MediaUsage{Count: 1, Mode: "standard"}, UsageTokens{}, 1)
	require.ErrorIs(t, err, ErrMediaPricingUnavailable)
}

func TestMediaPricingFirstSuccessFailureZeroAndExplicitFree(t *testing.T) {
	p := mediaTestPricing("image", 0.2, MediaPricingConditions{})
	p.Rules[0].PromptPrice = mediaTestPtr(0.03)
	got, err := CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.23, got.TotalCost, 1e-12)
	got, err = CalculateMediaPricingCost(p, &MediaUsage{}, UsageTokens{InputTokens: 100}, 1)
	require.NoError(t, err)
	require.Zero(t, got.TotalCost)
	p.Rules[0].Price = mediaTestPtr(0.0)
	p.Rules[0].PromptPrice = mediaTestPtr(0.0)
	got, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{}, 2)
	require.NoError(t, err)
	require.Zero(t, got.ActualCost)
	p.Rules[0].Price = nil
	_, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{}, 1)
	require.ErrorContains(t, err, "price is required")
}

func TestMediaPricingMultimodalTokensExplicitZero(t *testing.T) {
	p := &MediaPricing{Rules: []MediaPricingRule{{Name: "tokens", Unit: "token", InputPrice: mediaTestPtr(0.01), OutputPrice: mediaTestPtr(0.02), ImageInputPrice: mediaTestPtr(0.0), ImageOutputPrice: mediaTestPtr(0.04), PromptPrice: mediaTestPtr(0.03)}}}
	got, err := CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{InputTokens: 100, ImageInputTokens: 40, OutputTokens: 20, ImageOutputTokens: 10, CacheReadTokens: 20, ImageCacheReadTokens: 5, CacheCreationTokens: 3}, 2)
	require.NoError(t, err)
	require.InDelta(t, 0.63, got.InputCost, 1e-12)
	require.Zero(t, got.ImageInputCost)
	require.InDelta(t, 0.2, got.OutputCost, 1e-12)
	require.InDelta(t, 0.4, got.ImageOutputCost, 1e-12)
	require.InDelta(t, 0.15, got.CacheReadCost, 1e-12)
	require.InDelta(t, 0.03, got.CacheCreationCost, 1e-12)
	require.InDelta(t, 1.41, got.TotalCost, 1e-12)
	require.InDelta(t, 2.82, got.ActualCost, 1e-12)
	p.Rules[0].InputPrice = nil
	_, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{InputTokens: 1}, 1)
	require.ErrorIs(t, err, ErrMediaPricingUnavailable)
	_, err = CalculateMediaPricingCost(p, &MediaUsage{Count: 1}, UsageTokens{InputTokens: 1, ImageInputTokens: 2}, 1)
	require.ErrorContains(t, err, "exceed")
}

func TestMediaPricingTokenUsageCannotSilentlyBillZero(t *testing.T) {
	pricing := &MediaPricing{Rules: []MediaPricingRule{{Name: "tokens", Unit: "token", InputPrice: mediaTestPtr(0.01), OutputPrice: mediaTestPtr(0.02)}}}
	got, err := CalculateMediaPricingCost(pricing, &MediaUsage{Count: 1}, UsageTokens{}, 1)
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrMediaUsageIncomplete)
}

func TestMediaPricingInvalidAndInsufficientUsage(t *testing.T) {
	for _, u := range []*MediaUsage{nil, {Count: 1, Width: math.MaxInt64, Height: 2}, {Count: 1, Width: 2, Height: 2, Pixels: 5}, {Count: -1}, {Count: 1, DurationSeconds: math.NaN()}, {Count: 3, Images: []MediaUsage{{Pixels: 2}}}} {
		_, err := CalculateMediaPricingCost(mediaTestPricing("image", 1, MediaPricingConditions{}), u, UsageTokens{}, 1)
		require.Error(t, err)
	}
	for _, unit := range []string{"second", "frame"} {
		_, err := CalculateMediaPricingCost(mediaTestPricing(unit, 1, MediaPricingConditions{}), &MediaUsage{Count: 1}, UsageTokens{}, 1)
		require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	}
	_, err := CalculateMediaPricingCost(mediaTestPricing("frame", 1, MediaPricingConditions{}), &MediaUsage{Count: 1, DurationSeconds: 1.1, FPS: 24}, UsageTokens{}, 1)
	require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	for _, p := range []*MediaPricing{
		{}, mediaTestPricing("pixel", 1, MediaPricingConditions{}), mediaTestPricing("image", -1, MediaPricingConditions{}), mediaTestPricing("image", math.Inf(1), MediaPricingConditions{}),
		mediaTestPricing("image", 1, MediaPricingConditions{MinPixels: mediaTestPtr(int64(2)), MaxPixels: mediaTestPtr(int64(1))}),
		mediaTestPricing("image", 1, MediaPricingConditions{MinDuration: mediaTestPtr(2.0), MaxDuration: mediaTestPtr(1.0)}),
		mediaTestPricing("image", 1, MediaPricingConditions{MinFPS: mediaTestPtr(30.0), MaxFPS: mediaTestPtr(24.0)}),
		mediaTestPricing("image", 1, MediaPricingConditions{FPS: mediaTestPtr(24.0), MinFPS: mediaTestPtr(30.0)}),
	} {
		require.Error(t, ValidateMediaPricing(p))
	}
	require.NoError(t, ValidateMediaPricing(nil))
}

func TestMediaPricingUnifiedGroupPrecedenceTimeRateAndNoFallback(t *testing.T) {
	billing := &BillingService{}
	groupID := int64(12)
	channel := newChannelServiceWithPricings(groupID, []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"fixture-media"}, BillingMode: BillingModeImage, MediaPricing: mediaTestPricing("image", 4, MediaPricingConditions{})}})
	resolver := NewModelPricingResolver(channel, billing)
	group := &Group{ID: groupID, ModelPricing: []ChannelModelPricing{{Models: []string{"fixture-media"}, BillingMode: BillingModeImage, MediaPricing: mediaTestPricing("image", 0.2, MediaPricingConditions{Layer: mediaTestPtr(true)}), TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}}}}
	input := CostInput{Ctx: context.Background(), GroupID: &groupID, Group: group, Model: "fixture-media", Resolver: resolver, RateMultiplier: 3, MediaUsage: &MediaUsage{Count: 1, Layer: mediaTestPtr(true)}, PricingAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	got, err := billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 0.4, got.TotalCost, 1e-12)
	require.InDelta(t, 1.2, got.ActualCost, 1e-12)
	require.Equal(t, "image", got.BillingMode)
	input.MediaUsage.Layer = nil
	_, err = billing.CalculateCostUnified(input)
	require.ErrorIs(t, err, ErrMediaUsageIncomplete)
	input.Group = nil
	got, err = billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 4, got.TotalCost, 1e-12)
	require.InDelta(t, 12, got.ActualCost, 1e-12)
	input.RateMultiplier = 0
	got, err = billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Zero(t, got.ActualCost)
}

func TestMediaPricingCloneJSONAndAuthCacheIsolation(t *testing.T) {
	original := ChannelModelPricing{Models: []string{"fixture"}, MediaPricing: mediaTestPricing("image", 0, MediaPricingConditions{Audio: mediaTestPtr(false), MinPixels: mediaTestPtr(int64(0))})}
	clone := original.Clone()
	*clone.MediaPricing.Rules[0].Price = 7
	*clone.MediaPricing.Rules[0].Conditions.Audio = true
	*clone.MediaPricing.Rules[0].Conditions.MinPixels = 8
	require.Zero(t, *original.MediaPricing.Rules[0].Price)
	require.False(t, *original.MediaPricing.Rules[0].Conditions.Audio)
	require.Zero(t, *original.MediaPricing.Rules[0].Conditions.MinPixels)
	payload, err := json.Marshal(original)
	require.NoError(t, err)
	var decoded ChannelModelPricing
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, original.MediaPricing, decoded.MediaPricing)
	groupID := int64(2)
	key := &APIKey{ID: 1, UserID: 3, GroupID: &groupID, Key: "fixture", Status: StatusActive, User: &User{ID: 3, Status: StatusActive}, Group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, ModelPricing: []ChannelModelPricing{original}}}
	svc := &APIKeyService{}
	snap := svc.snapshotFromAPIKey(context.Background(), key)
	*key.Group.ModelPricing[0].MediaPricing.Rules[0].Price = 9
	require.Zero(t, *snap.Group.ModelPricing[0].MediaPricing.Rules[0].Price)
	restored, used, err := svc.applyAuthCacheEntry(key.Key, &APIKeyAuthCacheEntry{Snapshot: snap})
	require.NoError(t, err)
	require.True(t, used)
	*restored.Group.ModelPricing[0].MediaPricing.Rules[0].Price = 5
	require.Zero(t, *snap.Group.ModelPricing[0].MediaPricing.Rules[0].Price)
}
