//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Optional externally supplied, review-only CNY card fixture. These are test
// inputs, never defaults, and do not assert live provider protocol support.
func TestMediaPricingReviewedCNYCards(t *testing.T) {
	path := os.Getenv("MEDIA_PRICING_REVIEW_FIXTURE")
	if path == "" {
		t.Skip("review-only CNY fixture not supplied")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture struct {
		ReviewOnly  bool                  `json:"review_only"`
		NotLive     bool                  `json:"not_a_live_configuration"`
		Currency    string                `json:"source_currency"`
		FirstFree   bool                  `json:"first_output_free"`
		ExactModels int                   `json:"exact_models"`
		Rules       int                   `json:"rules"`
		Cards       []ChannelModelPricing `json:"cards"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.True(t, fixture.ReviewOnly)
	require.True(t, fixture.NotLive)
	require.False(t, fixture.FirstFree)
	require.Equal(t, "CNY", fixture.Currency)
	require.Len(t, fixture.Cards, fixture.ExactModels)
	count := 0
	billing := &BillingService{}
	resolver := NewModelPricingResolver(nil, billing)
	for _, card := range fixture.Cards {
		require.NoError(t, ValidateMediaPricing(card.MediaPricing))
		encoded, err := json.Marshal(card)
		require.NoError(t, err)
		var roundTrip ChannelModelPricing
		require.NoError(t, json.Unmarshal(encoded, &roundTrip))
		require.Equal(t, card.MediaPricing, roundTrip.MediaPricing)
		for _, rule := range card.MediaPricing.Rules {
			count++
			t.Run(card.Models[0]+"/"+rule.Name, func(t *testing.T) {
				c := rule.Conditions
				u := &MediaUsage{Count: 1, Resolution: c.Resolution, Mode: c.Mode, InputType: c.InputType, Audio: c.Audio, ReferenceVideo: c.ReferenceVideo, Layer: c.Layer, DurationSeconds: 1}
				// Synthetic text-only input, not an omitted/unknown measurement.
				u.InputImageCount = mediaTestPtr(0)
				u.InputDurationSeconds = mediaTestPtr(0.0)
				if c.MinPixels != nil {
					u.Pixels = *c.MinPixels
				} else if c.MaxPixels != nil {
					u.Pixels = *c.MaxPixels
				}
				if c.MinDuration != nil && *c.MinDuration > 0 {
					u.DurationSeconds = *c.MinDuration
				}
				if c.FPS != nil {
					u.FPS = *c.FPS
				} else if c.MinFPS != nil {
					u.FPS = *c.MinFPS
				} else if c.MaxFPS != nil {
					u.FPS = *c.MaxFPS
				}
				tokens := UsageTokens{}
				expected := rule.Price
				if rule.Unit == "token" {
					tokens.OutputTokens = 1
					expected = rule.OutputPrice
				}
				require.NotNil(t, expected)
				require.Nil(t, rule.PromptPrice, "reference-image prompt_rate belongs in extra_input_image_price")
				// A single output/second/token must use the exact rendered CNY price,
				// without a currency conversion or maximum-price approximation.
				require.Equal(t, 1.0, u.DurationSeconds)
				got, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: card.Models[0], Group: &Group{ModelPricing: []ChannelModelPricing{card}}, Resolver: resolver, MediaUsage: u, Tokens: tokens, RateMultiplier: 1})
				require.NoError(t, err)
				require.InDelta(t, *expected, got.TotalCost, 1e-12)
				require.Equal(t, got.TotalCost, got.ActualCost)
			})
		}
	}
	require.Equal(t, fixture.Rules, count)
}

func TestMediaReferenceReviewedV2Cards(t *testing.T) {
	path := os.Getenv("MEDIA_PRICING_REVIEW_V2_FIXTURE")
	if path == "" {
		t.Skip("review-only V2 CNY fixture not supplied")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture struct {
		Cards []ChannelModelPricing `json:"cards"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	cards := map[string]ChannelModelPricing{}
	for _, card := range fixture.Cards {
		cards[card.Models[0]] = card
	}
	// Literal acceptance amounts from the reviewed V2 fixture. These values
	// are test-only, not supplier defaults or a deployment configuration.
	for _, tc := range []struct {
		model           string
		free            int
		amounts, scaled [4]float64
	}{
		{"seedream-5-0-pro", 1, [4]float64{0.300844, 0.300844, 0.3209, 0.461292}, [4]float64{0.451266, 0.451266, 0.48135, 0.691938}},
		{"seedream-5-0-pro-NSFW-economy", 1, [4]float64{0.300844, 0.300844, 0.3209, 0.461292}, [4]float64{0.451266, 0.451266, 0.48135, 0.691938}},
		{"seedream-5.0-pro-NSFW", 1, [4]float64{0.94, 0.94, 0.98, 1.26}, [4]float64{1.41, 1.41, 1.47, 1.89}},
		{"MiniMax-H3", 5, [4]float64{0.92, 0.92, 1.19, 2}, [4]float64{1.38, 1.38, 1.785, 3}},
	} {
		card, ok := cards[tc.model]
		require.True(t, ok)
		p := card.MediaPricing
		require.NotNil(t, p)
		c := p.Rules[0].Conditions
		for i, n := range []int{0, tc.free, tc.free + 1, 9} {
			t.Run(fmt.Sprintf("%s/%d", tc.model, n), func(t *testing.T) {
				u := &MediaUsage{Count: 1, InputImageCount: &n, InputDurationSeconds: mediaTestPtr(0.0), DurationSeconds: 1, Resolution: c.Resolution, Layer: c.Layer}
				if c.MinPixels != nil {
					u.Pixels = *c.MinPixels
				}
				cost, err := CalculateMediaPricingCost(p, u, UsageTokens{}, 1.5)
				require.NoError(t, err)
				require.InDelta(t, tc.amounts[i], cost.TotalCost, 1e-12)
				require.InDelta(t, tc.scaled[i], cost.ActualCost, 1e-12)
				// Aliases with no layer_rate use their ordinary output rate even
				// when the provider reports a layer; do not synthesize Pro's half price.
				if tc.model == "seedream-5-0-pro-NSFW-economy" || tc.model == "seedream-5.0-pro-NSFW" {
					u.Layer = mediaTestPtr(true)
					layer, err := CalculateMediaPricingCost(p, u, UsageTokens{}, 1.5)
					require.NoError(t, err)
					require.Equal(t, cost, layer)
				}
			})
		}
	}
	p := cards["MiniMax-H3"].MediaPricing
	require.Equal(t, MediaBillingDurationTotal, p.BillingDuration)
	u := &MediaUsage{Count: 1, InputImageCount: mediaTestPtr(9), DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(3.0), TotalDurationSeconds: mediaTestPtr(7.0), Resolution: "1080p"}
	cost, err := CalculateMediaPricingCost(p, u, UsageTokens{}, 1.5)
	require.NoError(t, err)
	require.InDelta(t, 7.52, cost.TotalCost, 1e-12)
	require.InDelta(t, 11.28, cost.ActualCost, 1e-12)
}
