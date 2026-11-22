package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const partialCardModel = "legacy-partial-card"

const partialCardCatalog = `{
  "legacy-partial-card": {
    "input_cost_per_token": 2, "output_cost_per_token": 3,
    "cache_read_input_token_cost": 5, "cache_creation_input_token_cost": 7,
    "cache_creation_input_token_cost_above_1hr": 11,
    "cache_creation_input_token_cost_30m": 13, "cache_creation_input_token_cost_12h": 17,
    "input_cost_per_image_token": 19, "output_cost_per_image_token": 23,
    "cache_read_input_image_token_cost": 29,
    "input_cost_per_token_priority": 6,
    "long_context_input_token_threshold": 2,
    "long_context_input_cost_multiplier": 3, "long_context_output_cost_multiplier": 4,
    "glory_price_legacy": {"status":"quarantined", "reason":"fixture: legacy USD estimate"}
  }
}`

func partialCardPtr(v float64) *float64 { return &v }

func partialCardCost(b *BillingService, scope string, card ChannelModelPricing, tokens UsageTokens, serviceTier string) (*CostBreakdown, error) {
	ctx := context.Background()
	group := &Group{ID: 7, Platform: PlatformOpenAI, LongContextPricingEnabled: true}
	card.Models = []string{partialCardModel}
	card.Platform = PlatformOpenAI
	ch := &ChannelService{}
	if scope == "group" {
		group.ModelPricing = []ChannelModelPricing{card}
	}
	ch.cache.Store(populateChannelCache([]Channel{{ID: 8, Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{7: PlatformOpenAI}))
	resolver := NewModelPricingResolver(ch, b)
	if card.MediaPricing != nil {
		return b.CalculateCostUnified(CostInput{Ctx: ctx, Model: partialCardModel, Group: group, GroupID: &group.ID, Resolver: resolver, Tokens: tokens, MediaUsage: &MediaUsage{Count: 2}, RateMultiplier: 1})
	}
	return b.CalculateTokenCostForRequest(TokenCostRequest{Ctx: ctx, Model: partialCardModel, Group: group, Resolver: resolver, Tokens: tokens, ServiceTier: serviceTier, RateMultiplier: 1})
}

func TestLegacyQuarantinePartialCommercialCard(t *testing.T) {
	b := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, partialCardCatalog))
	_, err := b.CalculateCost(partialCardModel, UsageTokens{OutputTokens: 1}, 1)
	require.ErrorAs(t, err, new(*TariffPriceIssue), "fixture must actually be quarantined")
	card := ChannelModelPricing{InputPrice: partialCardPtr(31)}
	for _, scope := range []string{"group", "channel"} {
		t.Run(scope+"/input_only", func(t *testing.T) {
			cost, err := partialCardCost(b, scope, card, UsageTokens{InputTokens: 2}, "")
			require.NoError(t, err)
			require.Equal(t, 62., cost.ActualCost)
		})
		for _, tc := range []struct {
			name string
			u    UsageTokens
		}{
			{"output", UsageTokens{OutputTokens: 1}},
			{"cache_write", UsageTokens{CacheCreationTokens: 1}},
			{"cache_read", UsageTokens{CacheReadTokens: 1}},
			{"5m", UsageTokens{CacheCreationTokens: 1, CacheCreation5mTokens: 1}},
			{"1h", UsageTokens{CacheCreationTokens: 1, CacheCreation1hTokens: 1}},
			{"30m", UsageTokens{CacheCreationTokens: 1, CacheCreation30mTokens: 1}},
			{"12h", UsageTokens{CacheCreationTokens: 1, CacheCreation12hTokens: 1}},
		} {
			t.Run(scope+"/missing_"+tc.name, func(t *testing.T) {
				u := tc.u
				u.InputTokens = 2
				cost, err := partialCardCost(b, scope, card, u, "")
				var issue *TariffPriceIssue
				require.ErrorAs(t, err, &issue, "invalid settlement: %+v", cost)
				require.Equal(t, partialCardModel, issue.Model)
				require.Nil(t, cost)
			})
		}
	}
}

func TestLegacyQuarantineCommercialCardControls(t *testing.T) {
	b := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, partialCardCatalog))
	flat := ChannelModelPricing{InputPrice: partialCardPtr(31), OutputPrice: partialCardPtr(37), CacheReadPrice: partialCardPtr(41), CacheWritePrice: partialCardPtr(43)}
	full := flat
	full.CacheWrite1hPrice, full.CacheWrite12hPrice = partialCardPtr(47), partialCardPtr(53)
	full.ImageInputPrice, full.ImageOutputPrice = partialCardPtr(59), partialCardPtr(61)
	mixed := UsageTokens{InputTokens: 3, ImageInputTokens: 2, OutputTokens: 3, ImageOutputTokens: 2, CacheReadTokens: 1, CacheCreationTokens: 3, CacheCreation5mTokens: 1, CacheCreation1hTokens: 2}
	for _, scope := range []string{"group", "channel"} {
		t.Run(scope, func(t *testing.T) {
			price := func(card ChannelModelPricing, usage UsageTokens, tier string, want float64) *CostBreakdown {
				t.Helper()
				cost, err := partialCardCost(b, scope, card, usage, tier)
				require.NoError(t, err)
				require.Equal(t, want, cost.ActualCost)
				return cost
			}
			issue := func(card ChannelModelPricing, usage UsageTokens, reason string) {
				t.Helper()
				cost, err := partialCardCost(b, scope, card, usage, "")
				require.ErrorAs(t, err, new(*TariffPriceIssue))
				require.ErrorContains(t, err, reason)
				require.Nil(t, cost)
			}
			cost := price(full, mixed, "", 486)
			require.Equal(t, 137., cost.CacheCreationCost)
			require.Equal(t, 118., cost.ImageInputCost)
			require.Equal(t, 122., cost.ImageOutputCost)
			price(full, UsageTokens{CacheCreationTokens: 3, CacheCreation30mTokens: 1, CacheCreation12hTokens: 2}, "", 149)
			price(flat, UsageTokens{CacheCreationTokens: 3, CacheCreation5mTokens: 1, CacheCreation1hTokens: 2}, "", 129)
			price(flat, UsageTokens{CacheCreationTokens: 3, CacheCreation30mTokens: 1, CacheCreation12hTokens: 2}, "", 129)
			zero := partialCardPtr(0)
			price(ChannelModelPricing{InputPrice: flat.InputPrice, OutputPrice: zero, ImageInputPrice: zero, ImageOutputPrice: zero, CacheReadPrice: zero, CacheWritePrice: zero}, mixed, "", 31)
			price(ChannelModelPricing{CacheWrite1hPrice: zero}, UsageTokens{CacheCreationTokens: 2, CacheCreation1hTokens: 2}, "", 0)
			price(ChannelModelPricing{CacheWrite12hPrice: zero}, UsageTokens{CacheCreationTokens: 2, CacheCreation12hTokens: 2}, "", 0)
			issue(ChannelModelPricing{CacheWrite12hPrice: zero}, UsageTokens{CacheCreationTokens: 2, CacheCreation12hTokens: 1, CacheCreation30mTokens: 1}, "_30m")
			issue(ChannelModelPricing{CacheWrite1hPrice: zero}, UsageTokens{CacheCreationTokens: 2, CacheCreation1hTokens: 1}, "cache_creation_input_token_cost")
			issue(flat, UsageTokens{InputTokens: 1, ImageInputTokens: 1}, "input_cost_per_image_token")
			issue(flat, UsageTokens{OutputTokens: 1, ImageOutputTokens: 1}, "output_cost_per_image_token")
			issue(flat, UsageTokens{CacheReadTokens: 1, ImageCacheReadTokens: 1}, "cache_read_input_image_token_cost")
			issue(ChannelModelPricing{FastMultiplier: partialCardPtr(4), Intervals: []PricingInterval{{MinTokens: 1}}}, UsageTokens{InputTokens: 2}, "legacy USD estimate")
			price(ChannelModelPricing{InputPrice: flat.InputPrice}, UsageTokens{InputTokens: 2}, "fast", 124)
			price(ChannelModelPricing{InputPrice: flat.InputPrice, FastMultiplier: partialCardPtr(4)}, UsageTokens{InputTokens: 2}, "fast", 248)
			price(ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: partialCardPtr(67)}, mixed, "", 67)
			price(ChannelModelPricing{MediaPricing: &MediaPricing{Rules: []MediaPricingRule{{Unit: "request", Price: partialCardPtr(71)}}}}, mixed, "", 71)
		})
	}

	plain := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, strings.ReplaceAll(partialCardCatalog, `"status":"quarantined"`, `"status":"external"`)))
	cost, err := partialCardCost(plain, "channel", ChannelModelPricing{InputPrice: flat.InputPrice}, UsageTokens{InputTokens: 2, OutputTokens: 1, CacheReadTokens: 1, CacheCreationTokens: 1, CacheCreation12hTokens: 1}, "")
	require.NoError(t, err)
	require.Equal(t, 264., cost.ActualCost)
	cost, err = plain.CalculateCost(partialCardModel, UsageTokens{InputTokens: 2, OutputTokens: 1}, 1)
	require.NoError(t, err)
	require.Equal(t, 7., cost.ActualCost)

	sparse := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{"legacy-partial-card":{"output_cost_per_image":0.01,"glory_price_legacy":{"status":"quarantined","reason":"fixture: legacy USD estimate"}}}`))
	cost, err = partialCardCost(sparse, "group", ChannelModelPricing{InputPrice: flat.InputPrice}, UsageTokens{InputTokens: 2, OutputTokens: 1}, "")
	require.ErrorAs(t, err, new(*TariffPriceIssue))
	require.Nil(t, cost)
	cost, err = partialCardCost(sparse, "channel", flat, UsageTokens{InputTokens: 2, ImageInputTokens: 1, OutputTokens: 1, ImageOutputTokens: 1, CacheReadTokens: 1, ImageCacheReadTokens: 1}, "")
	require.NoError(t, err)
	require.Equal(t, 140., cost.ActualCost)
}

func TestLegacyQuarantineCommercialCardContext(t *testing.T) {
	b := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, partialCardCatalog))
	card := ChannelModelPricing{InputPrice: partialCardPtr(31), Intervals: []PricingInterval{{MinTokens: 2, OutputPrice: partialCardPtr(37), InputMultiplier: partialCardPtr(2), CacheReadMultiplier: partialCardPtr(1)}}}
	for _, tc := range []struct {
		name, scope string
		usage       UsageTokens
		want        float64
		missing     string
	}{
		{"channel_low_input", "channel", UsageTokens{InputTokens: 2}, 62, ""},
		{"channel_low_output", "channel", UsageTokens{InputTokens: 2, OutputTokens: 1}, 0, "output_cost_per_token"},
		{"channel_high", "channel", UsageTokens{InputTokens: 3, OutputTokens: 1}, 223, ""},
		{"multiplier_cannot_certify_catalog", "channel", UsageTokens{InputTokens: 2, OutputTokens: 1, CacheReadTokens: 1}, 0, "cache_read_input_token_cost"},
		{"group_ignores_token_intervals", "group", UsageTokens{InputTokens: 3, OutputTokens: 1}, 0, "output_cost_per_token"},
		{"group_retains_flat_input", "group", UsageTokens{InputTokens: 3}, 93, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := partialCardCost(b, tc.scope, card, tc.usage, "")
			if tc.missing != "" {
				require.ErrorAs(t, err, new(*TariffPriceIssue))
				require.ErrorContains(t, err, tc.missing)
				require.Nil(t, cost)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, cost.ActualCost)
		})
	}
}
