package service

import (
	"context"
	"fmt"
	"strings"
)

// resolveMediaAccountStatsCost reports whether an explicit media card owns the
// statistics calculation. Once owned, errors must propagate; falling back to a
// scalar/default price would silently misstate the upstream cost.
func resolveMediaAccountStatsCost(ctx context.Context, cs *ChannelService, accountID, groupID int64, model string, usage *MediaUsage, tokens UsageTokens, reasoningEffort string) (*float64, bool, error) {
	if cs == nil || model == "" {
		return nil, false, nil
	}
	channel, err := cs.GetChannelForGroup(ctx, groupID)
	if err != nil {
		return nil, false, nil
	}
	if channel == nil {
		return nil, false, nil
	}
	platform := cs.GetGroupPlatform(ctx, groupID)
	for _, rule := range channel.AccountStatsPricingRules {
		if !matchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := findPricingForModel(rule.Pricing, platform, strings.ToLower(model))
		if pricing == nil {
			continue
		}
		// Preserve first-matching-rule precedence, including ordinary scalar cards.
		if pricing.MediaPricing == nil {
			return nil, false, nil
		}
		bd, err := CalculateMediaPricingCost(pricing.MediaPricing, usage, tokens, 1)
		if err != nil {
			return nil, true, fmt.Errorf("account statistics media pricing: %w", err)
		}
		applyCostBreakdownMultiplier(bd, reasoningEffortBillingMultiplier(reasoningEffort, pricing.ReasoningEffortMultipliers))
		if !mediaFinite(bd.TotalCost) {
			return nil, true, fmt.Errorf("account statistics media cost overflow")
		}
		return &bd.TotalCost, true, nil
	}
	return nil, false, nil
}
