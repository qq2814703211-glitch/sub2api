//go:build unit

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRequestCatalogPinTransientChannelReadDoesNotEraseCommercialPrice(t *testing.T) {
	ctx := context.Background()
	const model = "pin-channel-unit"
	var groupID int64 = 99
	cfg := &config.Config{}
	p := NewPricingService(cfg, nil)
	file := filepath.Join(t.TempDir(), "base.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"pin-channel-unit":{"input_cost_per_token":2,"output_cost_per_token":1}}`), 0600))
	require.NoError(t, p.loadPricingData(file))
	b := NewBillingService(cfg, p)
	price := 7.0
	var unavailable, removed bool
	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) {
			if unavailable {
				return nil, errors.New("fixture: temporary channel lookup error")
			}
			if removed {
				return nil, nil
			}
			amount := price
			return []Channel{{ID: 3, GroupIDs: []int64{groupID}, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Models: []string{model}, Platform: PlatformOpenAI, InputPrice: &amount}}}}, nil
		},
		getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
			return map[int64]string{groupID: PlatformOpenAI}, nil
		},
	}
	c := NewChannelService(repo, nil, nil, p, nil)
	r := NewModelPricingResolver(c, b)
	gw := &GatewayService{billingService: b, channelService: c}
	bill := func(request context.Context, want float64) {
		t.Helper()
		cost, err := b.CalculateCostUnified(CostInput{Ctx: request, Model: model, GroupID: &groupID, Tokens: UsageTokens{InputTokens: 3}, RateMultiplier: 1, Resolver: r})
		require.NoError(t, err)
		require.Equal(t, want, cost.ActualCost)
	}
	bill(gw.WithRequestCatalog(ctx), 21)
	unavailable = true
	c.InvalidateCache()
	inFlight := gw.WithRequestCatalog(ctx)
	bill(inFlight, 21)
	// Only billing borrows the last confirmed snapshot; routing sees the live result.
	route, err := c.GetChannelForGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, route)
	unavailable, price = false, 11
	c.InvalidateCache()
	bill(gw.WithRequestCatalog(ctx), 33)
	bill(inFlight, 21)
	removed = true
	c.InvalidateCache()
	bill(gw.WithRequestCatalog(ctx), 6)
	unavailable = true
	c.InvalidateCache()
	bill(gw.WithRequestCatalog(ctx), 6)
}
