//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

// TestChannelModelPricingBeatsSyncedOverride 钉死 brief §5.3 的优先级：
// 分组价卡存在时，models.dev 同步来的覆盖层不得参与结算。
func TestChannelModelPricingBeatsSyncedOverride(t *testing.T) {
	// 覆盖层（同步产物）：input 4e-6、output 2e-5。
	const overrideJSON = `{
	  "gpt-5.6-sol": {
	    "input_cost_per_token": 4e-06,
	    "output_cost_per_token": 2e-05,
	    "cache_read_input_token_cost": 4e-07
	  }
	}`
	overridePath := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(overrideJSON), 0o644))

	pricingService := &PricingService{cfg: &config.Config{}}
	pricingService.cfg.Pricing.OverrideFile = overridePath
	pricingService.pricingData = map[string]*LiteLLMModelPricing{
		"gpt-5.6-sol": {
			InputCostPerToken:       4e-6,
			OutputCostPerToken:      2e-5,
			CacheReadInputTokenCost: 4e-7,
		},
	}
	billing := NewBillingService(&config.Config{}, pricingService)

	// 无分组价卡时按覆盖层计费：1000 × 4e-6 = 4e-3。
	noChannel, err := billing.CalculateCost("gpt-5.6-sol", UsageTokens{InputTokens: 1000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 4e-3, noChannel.InputCost, 1e-12)

	// 分组价卡存在：1.5e-6，必须完全压过覆盖层。
	channelService := newTestChannelServiceWithCache(t, &channelCache{
		pricingByGroupModel: map[channelModelKey]*ChannelModelPricing{
			{groupID: 7, model: "gpt-5.6-sol"}: {
				BillingMode: BillingModeToken,
				InputPrice:  testPtrFloat64(1.5e-6),
				OutputPrice: testPtrFloat64(9e-6),
			},
		},
		channelByGroupID:        map[int64]*Channel{7: {ID: 7, Status: StatusActive}},
		groupPlatform:           map[int64]string{7: ""},
		wildcardByGroupPlatform: map[channelGroupPlatformKey][]*wildcardPricingEntry{},
		mappingByGroupModel:     map[channelModelKey]string{},
		wildcardMappingByGP:     map[channelGroupPlatformKey][]*wildcardMappingEntry{},
		byID:                    map[int64]*Channel{},
	})
	resolver := NewModelPricingResolver(channelService, billing)
	groupID := int64(7)
	withChannel, err := billing.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "gpt-5.6-sol",
		GroupID:        &groupID,
		Tokens:         UsageTokens{InputTokens: 1000},
		RateMultiplier: 1,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.InDelta(t, 1.5e-3, withChannel.InputCost, 1e-12,
		"分组价卡必须压过 models.dev 同步来的覆盖层")
}
