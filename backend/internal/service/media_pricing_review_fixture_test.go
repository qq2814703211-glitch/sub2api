//go:build unit

package service

import (
	"context"
	"encoding/json"
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
				require.Nil(t, rule.PromptPrice, "upstream prompt_rate has no verified mapping in this fixture")
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
