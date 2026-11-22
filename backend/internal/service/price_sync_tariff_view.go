package service

import (
	"maps"
	"slices"
)

// The published card already specifies exact whole-request per-component rates.
// Probing a wall clock would show the wrong base prices during a peak window;
// probing a missing component cannot distinguish unknown from an explicit zero.
func (t *priceSyncTariff) schedule(longCtx bool, group *Group) *ContextPricingSchedule {
	out := &ContextPricingSchedule{Basis: ContextPricingBasisWholeRequest}
	bands := t.tiers
	if !longCtx {
		bands = nil
	}
	var min int
	rates := t.base
	add := func(max *int) {
		rate := func(key string) *float64 {
			v, ok := rates[key]
			if !ok {
				return nil
			}
			return &v
		}
		out.Tiers = append(out.Tiers, ContextPricingTier{MinTokens: min, MaxTokens: max, Input: rate("input_cost_per_token"), Output: rate("output_cost_per_token"), CacheRead: rate("cache_read_input_token_cost"), CacheWrite: rate("cache_creation_input_token_cost"), CacheWrite1h: rate("cache_creation_input_token_cost_above_1hr"), CacheWrite30m: rate("cache_creation_input_token_cost_30m"), CacheWrite12h: rate("cache_creation_input_token_cost_12h")})
	}
	for _, band := range bands {
		max := tariffUpperBound(band)
		if max > min {
			add(&max)
		}
		min = max
		rates = band.Rates
	}
	add(nil)
	applyContextTierLabels(out.Tiers, contextBreakpointPlan{})
	if !groupOwnsTariffWindow(group) {
		for _, w := range t.windows {
			v := w.rule
			v.Rates = maps.Clone(v.Rates)
			v.Weekdays = slices.Clone(v.Weekdays)
			out.PriceWindows = append(out.PriceWindows, v)
		}
	}
	return out
}

func groupOwnsTariffWindow(g *Group) bool {
	return g != nil && g.IsSubscriptionType() && g.PeakRateEnabled
}
