//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Synthetic fixtures, no supplier or production data.
func TestUIReviewMediaScheduleResponse(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeToken, BillingModeImage, BillingModeVideo} {
		t.Run(string(mode), func(t *testing.T) {
			card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"review-media"}, BillingMode: mode,
				MediaPricing: mediaTestPricing("image", 2, MediaPricingConditions{}),
				TimePricing:  &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00:00", EndTime: "23:59:59", Multiplier: 3}}},
			}
			group := Group{ID: 10, Platform: PlatformOpenAI, RateMultiplier: 0.5, ModelPlazaEnabled: true, ModelPricing: []ChannelModelPricing{card}}
			svc := newPlazaService(nil, []Group{group}, nil)
			svc.billingService = &BillingService{}
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groups, err := svc.ListGroups(context.Background())
			require.NoError(t, err)
			require.Len(t, groups, 1)
			model := groups[0].Models[0]
			require.NotNil(t, model.Pricing.TimePricing)
			cost, err := svc.billingService.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "review-media", Group: &group, Resolver: svc.resolver, MediaUsage: &MediaUsage{Count: 1}, RateMultiplier: 0.5, PricingAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)})
			require.NoError(t, err)
			require.InDelta(t, 3.0, cost.ActualCost, 1e-12)
			snapshot, err := json.Marshal(map[string]any{"name": model.Name, "platform": model.Platform, "pricing": model.Pricing, "time_pricing": model.TimePricing, "actual_cost": cost.ActualCost})
			require.NoError(t, err)
			t.Log(string(snapshot))
			if dir := os.Getenv("UI_REVIEW_OUTPUT"); dir != "" {
				require.NoError(t, os.WriteFile(dir+"/plaza-"+string(mode)+".json", snapshot, 0600))
			}
			require.NotNil(t, model.TimePricing, "plaza response must expose the schedule that actual billing applies")
		})
	}
}

func TestUIReviewRequestCardMediaMultiplier(t *testing.T) {
	for _, video := range []bool{false, true} {
		name := "image"
		if video {
			name = "video"
		}
		t.Run(name, func(t *testing.T) {
			card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"review-media"}, BillingMode: BillingModePerRequest, MediaPricing: mediaTestPricing("request", 2, MediaPricingConditions{})}
			group := Group{ID: 10, Platform: PlatformOpenAI, RateMultiplier: 0.1, ImageRateIndependent: true, ImageRateMultiplier: 0.5, VideoRateIndependent: true, VideoRateMultiplier: 0.8, ModelPricing: []ChannelModelPricing{card}}
			billing := &BillingService{}
			svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
			key := &APIKey{Group: &group, GroupID: &group.ID}
			result := &OpenAIForwardResult{Model: "review-media", MediaUsage: &MediaUsage{Count: 1}, ImageEndpoint: !video, ImageCount: 1}
			expected := 1.0
			if video {
				result.ImageEndpoint = false
				result.ImageCount = 0
				result.VideoCount = 1
				expected = 1.6
			}
			cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{"review-media"}, 0.1, 0.5, 0.8, 0.1, UsageTokens{}, "", nil, false, time.Time{})
			require.NoError(t, err)
			require.InDelta(t, expected, cost.ActualCost, 1e-12)
			t.Logf("billing_mode=per_request endpoint=%s actual_cost=%g", name, cost.ActualCost)
		})
	}
}

// Endpoint rates, time, prompt and reference-input fees each apply once.
func TestPlazaEndpointRateCosts(t *testing.T) {
	cases := []struct {
		name     string
		mode     BillingMode
		expected float64
	}{
		{"image", BillingModePerRequest, 4.35}, {"video", BillingModePerRequest, 6.96}, {"general", BillingModePerRequest, 1.74}, {"token", BillingModeToken, 1.74},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var media MediaPricing
			require.NoError(t, json.Unmarshal([]byte(`{"free_input_images":1,"extra_input_image_price":0.25,"duration_source":"output","rules":[{"unit":"request","price":2,"prompt_price":0.4}]}`), &media))
			card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"opaque-name"}, BillingMode: tc.mode, MediaPricing: &media, TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00:00", EndTime: "23:59:59", Multiplier: 3}}}}
			group := Group{ID: 10, Platform: PlatformOpenAI, RateMultiplier: 0.1, ImageRateIndependent: true, ImageRateMultiplier: 0.5, VideoRateIndependent: true, VideoRateMultiplier: 0.8, ModelPricing: []ChannelModelPricing{card}}
			billing := &BillingService{}
			svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
			count := 3
			result := &OpenAIForwardResult{Model: "opaque-name", MediaUsage: &MediaUsage{Count: 1, InputImageCount: &count}}
			if tc.name == "image" || tc.name == "token" {
				result.ImageEndpoint = true
				result.ImageCount = 1
			}
			if tc.name == "video" {
				result.VideoCount = 1
			}
			cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, &APIKey{Group: &group, GroupID: &group.ID}, []string{"opaque-name"}, 0.2, 0.5, 0.8, 0.2, UsageTokens{}, "", nil, false, time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
			require.NoError(t, err)
			require.InDelta(t, tc.expected, cost.ActualCost, 1e-12)
			if dir := os.Getenv("UI_REVIEW_OUTPUT"); dir != "" {
				snapshot, err := json.Marshal(map[string]any{"pricing": card, "time_pricing": resolvedTimePricingSchedule(&ResolvedPricing{Source: PricingSourceGroup, channelPricing: &card}), "actual_cost": cost.ActualCost})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(dir+"/endpoint-"+tc.name+".json", snapshot, 0600))
			}
		})
	}
}

func TestPlazaMediaChannelAndGroupSchedule(t *testing.T) {
	for _, source := range []string{"channel", "group"} {
		for _, mode := range []BillingMode{BillingModeToken, BillingModeImage, BillingModeVideo, BillingModePerRequest} {
			t.Run(source+"/"+string(mode), func(t *testing.T) {
				card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"opaque-name"}, BillingMode: mode, MediaPricing: mediaTestPricing("image", 2, MediaPricingConditions{}), TimePricing: &ChannelTimePricing{Timezone: "UTC", WeekdaysOnly: true, Periods: []ChannelTimePricingPeriod{{StartTime: "00:00:00", EndTime: "23:59:59", Multiplier: 3}}}}
				ch := Channel{ID: 1, Name: "fixture", Status: StatusActive, GroupIDs: []int64{10}, ModelPricing: []ChannelModelPricing{card}}
				g := Group{ID: 10, Platform: PlatformOpenAI, RateMultiplier: 0.5}
				if source == "group" {
					g.ModelPricing = []ChannelModelPricing{card}
					ch.ModelPricing[0].TimePricing = nil
				}
				svc := newPlazaService([]Channel{ch}, []Group{g}, nil)
				groups, err := svc.ListGroups(context.Background())
				require.NoError(t, err)
				model := groups[0].Models[0]
				require.NotNil(t, model.TimePricing)
				require.True(t, model.TimePricing.WeekdaysOnly)
				require.Equal(t, 3.0, model.TimePricing.Periods[0].Multiplier)
				require.Equal(t, 2.0, *model.Pricing.MediaPricing.Rules[0].Price)
			})
		}
	}
}
