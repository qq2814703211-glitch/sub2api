package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRequestCatalogPinOpenAIStreamLedger(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	s := newOpenAIRecordUsageServiceForTest(logs, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	s.cfg.Default.RateMultiplier = 1
	ledger := &openAIRecordUsageBillingRepoStub{}
	s.usageBillingRepo = ledger
	catalog := NewPricingService(s.cfg, nil)
	s.billingService = NewBillingService(s.cfg, catalog)
	path := filepath.Join(t.TempDir(), "prices.json")
	load := func(price int) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`{"pin-sku":{"input_cost_per_token":%d,"output_cost_per_token":1}}`, price)), 0600))
		require.NoError(t, catalog.loadPricingData(path))
	}
	load(2)
	old, start := s.WithOpenAIRequestPricingContext(context.Background(), nil)
	load(7)
	fresh, next := s.WithOpenAIRequestPricingContext(context.Background(), nil)
	settle := func(ctx context.Context, in *OpenAIRecordUsageInput, want float64) {
		t.Helper()
		require.NoError(t, s.RecordUsage(ctx, in))
		require.NotNil(t, ledger.lastCmd)
		require.Equal(t, want, ledger.lastCmd.BalanceCost)
		require.Equal(t, want, logs.lastLog.ActualCost)
	}
	in := &OpenAIRecordUsageInput{APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3}, PricingAt: next,
		Result: &OpenAIForwardResult{RequestID: "fresh", Model: "pin-sku", Usage: OpenAIUsage{InputTokens: 3}}}
	settle(fresh, in, 21)
	in.PricingAt, in.Result.RequestID, in.Result.Stream = start, "old-stream", true
	settle(old, in, 6)
}

func TestRequestCatalogPinRestoreAndOverrides(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "override.json")
	cfg.Default.RateMultiplier = 1
	p := NewPricingService(cfg, nil)
	base := p.getPricingFilePath()
	require.NoError(t, os.WriteFile(base, []byte(`{"pin-sku":{"input_cost_per_token":2,"output_cost_per_token":1,"glory_price_source":{"provider":"vendor","currency":"CNY"}}}`), 0600))
	require.NoError(t, p.loadPricingData(base))
	bs := NewBillingService(cfg, p)
	cs := &ChannelService{}
	installChannel := func(v float64) {
		cs.cache.Store(populateChannelCache([]Channel{{ID: 9, Status: StatusActive, GroupIDs: []int64{2}, ModelPricing: []ChannelModelPricing{{ID: 99, Models: []string{"pin-sku"}, Platform: PlatformOpenAI, InputPrice: &v}}}}, map[int64]string{2: PlatformOpenAI}))
	}
	installChannel(11)
	s := &OpenAIGatewayService{billingService: bs, resolver: NewModelPricingResolver(cs, bs), channelService: cs, cfg: cfg}
	ctx := context.Background()
	old := s.WithRequestCatalog(ctx)
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(`{"pin-sku":{"input_cost_per_token":7,"output_cost_per_token":1,"glory_price_source":{"provider":"operator","currency":"SITE"}}}`), 0600))
	require.NoError(t, p.reloadCustomPricingLayers())
	installChannel(13)
	manual := s.WithRequestCatalog(ctx)
	require.NoError(t, os.Remove(cfg.Pricing.OverrideFile))
	require.NoError(t, p.reloadCustomPricingLayers())
	restored := s.WithRequestCatalog(ctx)
	operatorPrice := 17.0
	for _, tc := range []struct {
		name            string
		pin             context.Context
		global, channel float64
		origin          string
	}{{"auto", old, 7, 34, "auto"}, {"manual", manual, 22, 40, "manual"}, {"restored", restored, 7, 40, "auto"}} {
		t.Run(tc.name, func(t *testing.T) {
			for _, g := range []struct {
				id    int64
				want  float64
				scope string
			}{{1, tc.global, "catalog"}, {2, tc.channel, PricingSourceChannel}, {3, 52, PricingSourceGroup}} {
				group := &Group{ID: g.id, Platform: PlatformOpenAI, RateMultiplier: 1}
				if g.id == 3 {
					group.ModelPricing = []ChannelModelPricing{{Models: []string{"pin-sku"}, InputPrice: &operatorPrice}}
				}
				key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
				price, ok := s.EstimateInflightReservation(tc.pin, key, InflightEstimateRequest{Model: "pin-sku", BodyBytes: 12, MaxTokens: 1})
				require.True(t, ok)
				require.Equal(t, g.want, price)
				worker := CopyRequestPricing(tc.pin, ctx)
				cost, err := bs.CalculateCostUnified(CostInput{Ctx: worker, Model: "pin-sku", Tokens: UsageTokens{InputTokens: 3, OutputTokens: 1}, Group: group, GroupID: &group.ID, Resolver: s.resolver, RateMultiplier: 1})
				require.NoError(t, err)
				require.Equal(t, g.want, cost.ActualCost)
				trace := RequestCatalogFromContext(worker).trace(cost, "pin-sku", group)
				require.Equal(t, g.scope, trace.Basis.Scope)
				require.Equal(t, tc.origin, trace.Basis.Row.Origin)
				require.NotEmpty(t, trace.Basis.Row.Digest)
			}
		})
	}
}

func TestRequestCatalogPinConcurrentPublication(t *testing.T) {
	p := NewPricingService(&config.Config{}, nil)
	dir := t.TempDir()
	path := func(n int) string { return filepath.Join(dir, fmt.Sprint(n)) }
	for _, n := range []int{2, 7} {
		require.NoError(t, os.WriteFile(path(n), []byte(fmt.Sprintf(`{"pin-sku":{"input_cost_per_token":%d}}`, n)), 0600))
	}
	require.NoError(t, p.loadPricingData(path(2)))
	bs := NewBillingService(&config.Config{}, p)
	gw := &GatewayService{billingService: bs}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 60; j++ {
				ctx := gw.WithRequestCatalog(context.Background())
				input := CostInput{Ctx: ctx, Model: "pin-sku", Tokens: UsageTokens{InputTokens: 3}, RateMultiplier: 1}
				x, e := bs.CalculateCostUnified(input)
				if e != nil || x == nil || (x.ActualCost != 6 && x.ActualCost != 21) {
					t.Errorf("invalid initial price: %v, %v", x, e)
					return
				}
				input.Ctx = CopyRequestPricing(ctx, context.Background())
				y, e := bs.CalculateCostUnified(input)
				if e != nil || y == nil || x.ActualCost != y.ActualCost {
					t.Errorf("worker repriced: %v -> %v (%v)", x, y, e)
					return
				}
			}
		}()
	}
	close(start)
	for i := 0; i < 15; i++ {
		require.NoError(t, p.loadPricingData(path([]int{7, 2}[i%2])))
	}
	wg.Wait()
}

func TestRequestCatalogPinGroupRefreshCannotRepriceOldKey(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	s := newOpenAIRecordUsageServiceForTest(logs, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	s.resolver = NewModelPricingResolver(nil, s.billingService)
	s.usageBillingRepo = &openAIRecordUsageBillingRepoStub{}
	ctx := context.Background()
	group := func(id int64, price float64) *Group {
		return &Group{ID: id, RateMultiplier: 1, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{{Models: []string{"group-pin"}, InputPrice: &price}}}
	}
	old, newGroup, other := group(1, 2), group(1, 7), group(2, 13)
	start := RequestCatalogFromContext(s.WithRequestCatalog(ctx)).WithPricingGroup(old).Context(ctx)
	key := &APIKey{ID: 2, GroupID: &newGroup.ID, Group: newGroup}
	retry := ContextWithAPIKeyRoute(start, key)
	later := RequestCatalogFromContext(s.WithRequestCatalog(ctx)).WithPricingGroup(newGroup).Context(ctx)
	for _, tc := range []struct {
		ctx  context.Context
		g    *Group
		want float64
	}{{retry, newGroup, 6}, {later, newGroup, 21}, {start, other, 39}} {
		k := *key
		k.Group = tc.g
		k.GroupID = &tc.g.ID
		in := &OpenAIRecordUsageInput{APIKey: &k, User: &User{ID: 1}, Account: &Account{ID: 3}, Result: &OpenAIForwardResult{Model: "group-pin", RequestID: "group-refreshed", Usage: OpenAIUsage{InputTokens: 3}}}
		require.NoError(t, s.RecordUsage(CopyRequestPricing(tc.ctx, ctx), in))
		require.Equal(t, tc.want, logs.lastLog.ActualCost)
		require.Equal(t, PricingSourceGroup, logs.lastLog.PriceTrace.Basis.Scope)
	}
}

func BenchmarkRequestCatalogPin(b *testing.B) {
	for _, n := range []int{1, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			p := NewPricingService(&config.Config{}, nil)
			for i := 0; i < n; i++ {
				p.pricingData[fmt.Sprint(i)] = &LiteLLMModelPricing{InputCostPerToken: 2}
			}
			gw := &GatewayService{billingService: NewBillingService(&config.Config{}, p)}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if gw.WithRequestCatalog(ctx) == nil {
					b.Fatal("missing context")
				}
			}
		})
	}
}
