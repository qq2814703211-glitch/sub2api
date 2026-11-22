package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPriceSyncTariffSellingPolicyPrecedence(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	cfg := &config.Config{}
	b := NewBillingService(cfg, newStubPricingServiceFromJSON(t, tariffWindowFixture))
	c := ChannelModelPricing{Models: []string{"deepseek-flash"}, Platform: PlatformOpenAI, InputPrice: tariffTestPtr(20.), OutputPrice: tariffTestPtr(0.), CacheReadPrice: tariffTestPtr(10.), TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "01:00", EndTime: "04:00", Multiplier: 3}}}}
	ch := &ChannelService{}
	ch.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{c}}}, map[int64]string{7: PlatformOpenAI}))
	r := NewModelPricingResolver(ch, b)
	g := &Group{ID: 7, Platform: PlatformOpenAI, LongContextPricingEnabled: true}
	tokens := UsageTokens{InputTokens: 2, OutputTokens: 1, CacheReadTokens: 1}
	calc := func(resolver *ModelPricingResolver, group *Group, pre *ResolvedPricing, rate float64) (float64, string) {
		input := CostInput{Ctx: ctx, Model: "deepseek-flash", Group: group, Tokens: tokens, Resolver: resolver, Resolved: pre, RateMultiplier: rate, PricingAt: at}
		if group != nil {
			input.GroupID = &group.ID
		}
		cost, err := b.CalculateCostUnified(input)
		require.NoError(t, err)
		return cost.ActualCost, cost.BillingMode
	}
	// Channel (2*20 + 10) * own time policy 3 * ordinary group multiplier 2. Never the vendor window.
	pre := r.Resolve(ctx, PricingInput{Group: g, GroupID: &g.ID, Model: "deepseek-flash"})
	require.Equal(t, PricingSourceChannel, pre.Source)
	got, _ := calc(r, g, pre, 2)
	require.Equal(t, 300., got)
	got, _ = calc(nil, g, pre, 2)
	require.Equal(t, 300., got, "already resolved channel card must not be replaced by catalog even with a nil resolver")
	g.ModelPricing = []ChannelModelPricing{{Models: c.Models, InputPrice: tariffTestPtr(10.), OutputPrice: tariffTestPtr(1.), CacheReadPrice: tariffTestPtr(5.)}}
	for _, rr := range []*ModelPricingResolver{r, nil} {
		got, _ = calc(rr, g, nil, 2)
		require.Equal(t, 52., got, "flat group > channel > vendor window")
	}
	// No card: caller has already folded the subscription's own peak factor into rate.
	g = &Group{ID: 8, SubscriptionType: SubscriptionTypeSubscription, PeakRateEnabled: true, PeakStart: "00:00", PeakEnd: "23:59", PeakRateMultiplier: 3}
	multi, _ := computePeakAwareMultipliers(&APIKey{Group: g}, 2, at)
	require.Equal(t, 6., multi)
	got, _ = calc(r, g, nil, multi)
	require.Equal(t, 66., got)
}

func TestPriceSyncTariffExactSKUAndMissingUsage(t *testing.T) {
	row := strings.ReplaceAll(tariffLadderFixture, "tariff-test", "Tariff-SKU")
	p := newStubPricingServiceFromJSON(t, row)
	b := NewBillingService(&config.Config{}, p)
	for _, model := range []string{"Tariff-SKU", "Tariff-SKU-20261109", "tariff-sku"} {
		c, err := b.CalculateCostUnified(CostInput{Ctx: context.Background(), Resolver: NewModelPricingResolver(nil, b), Model: model, Tokens: UsageTokens{InputTokens: 21}, RateMultiplier: 1})
		if model == "Tariff-SKU" {
			require.NoError(t, err)
			require.Equal(t, 63., c.TotalCost)
		} else {
			require.Error(t, err, model)
			require.Nil(t, c)
		}
	}
	row = `{"tariff-sku":{"glory_price_source":{"currency":"USD"},"input_cost_per_token":2,"output_cost_per_token":3,"cache_creation_input_token_cost_30m":10,"cache_creation_input_token_cost_12h":20}}`
	b = NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, row))
	for _, u := range []UsageTokens{{CacheCreationTokens: 1}, {CacheReadTokens: 1}, {CacheCreationTokens: 3, CacheCreation30mTokens: 1, CacheCreation12hTokens: 1}, {CacheCreationTokens: 1, CacheCreation1hTokens: 1}} {
		c, err := b.CalculateCost("tariff-sku", u, 1)
		var issue *TariffPriceIssue
		require.ErrorAs(t, err, &issue)
		require.Equal(t, "tariff-sku", issue.Model)
		require.Nil(t, c)
	}
}

func TestPriceSyncTariffWindowViewAndAmbiguity(t *testing.T) {
	catalog := newStubPricingServiceFromJSON(t, tariffWindowFixture)
	b := NewBillingService(&config.Config{}, catalog)
	r := NewModelPricingResolver(nil, b)
	p := NewModelPlazaService(nil, nil, catalog, b, r)
	m := PlazaModel{Name: "deepseek-flash", Platform: PlatformOpenAI}
	p.fillDisplayPricing(context.Background(), &m, &Group{ID: 9})
	official := p.lookupOfficialPricing(context.Background(), m.Name, map[string]*PlazaOfficialPricing{})
	require.NotNil(t, official)
	require.Len(t, m.PriceWindows, 1)
	require.Equal(t, m.PriceWindows, official.PriceWindows)
	require.Equal(t, 2., *official.InputPrice)
	require.Equal(t, 0., official.PriceWindows[0].Rates["output_cost_per_token"])
	require.True(t, m.PriceWindows[0].ExcludeCNHolidays)
	require.Equal(t, "Asia/Shanghai", m.PriceWindows[0].Timezone)
	require.Nil(t, m.TimePricing)
	// The view is not allowed to mutate a catalog snapshot serving concurrent bills.
	m.PriceWindows[0].Rates["input_cost_per_token"] = 999
	m.PriceWindows[0].Weekdays[0] = 0
	at := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	c, err := b.CalculateCostUnified(CostInput{Model: m.Name, PricingAt: at, Tokens: UsageTokens{InputTokens: 1}, RateMultiplier: 1})
	require.NoError(t, err)
	require.Equal(t, 7., c.TotalCost)
	g := &Group{ID: 10, ModelPricing: []ChannelModelPricing{{Models: []string{m.Name}, InputPrice: tariffTestPtr(8.)}}}
	m = PlazaModel{Name: m.Name, Platform: m.Platform}
	p.fillDisplayPricing(context.Background(), &m, g)
	require.Empty(t, m.PriceWindows)
	// Overlapping quotes must never be chosen by their wire order.
	const row = `{"x":{"input_cost_per_token":1,"output_cost_per_token":1,"glory_price_windows":[{"timezone":"UTC","start":"00:00","end":"24:00","rates":{"input_cost_per_token":7,"output_cost_per_token":1}},{"timezone":"UTC","start":"01:00","end":"04:00","rates":{"input_cost_per_token":9,"output_cost_per_token":1}}]}}`
	b = NewBillingService(nil, newStubPricingServiceFromJSON(t, row))
	c, err = b.CalculateCostUnified(CostInput{Model: "x", PricingAt: at, Tokens: UsageTokens{InputTokens: 1}, RateMultiplier: 1})
	var issue *TariffPriceIssue
	require.ErrorAs(t, err, &issue)
	require.Contains(t, issue.Reason, "overlap")
	require.Nil(t, c)
}
