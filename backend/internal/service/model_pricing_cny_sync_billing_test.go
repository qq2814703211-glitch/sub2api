//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TestCNYOfficial_LadderTakesEffectInBilling 是「写好」到「真按阶梯收费」的闭环：
// 同步写入的长上下文三字段必须被 PricingService 解析出来，并在计费时真正生效。
//
// 只断言 JSON 里有三个字段是不够的 —— 字段名写错、倍率写成 0、阈值语义反了，
// 都能让「文件看起来对」但计费照基础价收（历史上真实的漏收形态）。
func TestCNYOfficial_LadderTakesEffectInBilling(t *testing.T) {
	// 目录里 qwen3-max 的基础价，模拟人民币口径（每 token）。
	const catalog = `{
	  "qwen3-max": {
	    "litellm_provider": "alibaba",
	    "mode": "chat",
	    "input_cost_per_token": 3.59e-07,
	    "output_cost_per_token": 1.434e-06
	  }
	}`
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	require.NoError(t, os.WriteFile(catalogPath, []byte(catalog), 0o644))

	overridePath := filepath.Join(t.TempDir(), "overrides.json")
	encoded, err := json.Marshal(&CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)

	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   overridePath,
			Interval:       time.Minute,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: map[string]string{SettingKeyPricingCNYOfficial: string(encoded)}},
		},
		nil,
		&stubModelPricingSyncHistory{},
		&cnyRoutingFetcher{
			cnyBody: []byte(cnyOfficialRegistrySnapshotJSON),
			usdBody: []byte(cnyOfficialRegistrySnapshotJSON),
		},
	)
	_, err = syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	pricingService := &PricingService{cfg: &config.Config{}}
	pricingService.cfg.Pricing.OverrideFile = overridePath
	data, err := pricingService.parsePricingData([]byte(catalog))
	require.NoError(t, err)

	patched := data["qwen3-max"]
	require.NotNil(t, patched)
	require.Equal(t, 32000, patched.LongContextInputTokenThreshold,
		"同步写入的阈值必须被 PricingService 解析出来")
	require.InDelta(t, 0.574/0.359, patched.LongContextInputCostMultiplier, 1e-9)
	require.InDelta(t, 2.294/1.434, patched.LongContextOutputCostMultiplier, 1e-9)

	// 计费闭环：32k 以下按基础价，超过 32k 按倍率整次会话加价。
	pricingService.pricingData = data
	billing := NewBillingService(&config.Config{}, pricingService)

	belowThreshold, err := billing.CalculateCost("qwen3-max",
		UsageTokens{InputTokens: 32000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.False(t, belowThreshold.LongContextBillingApplied, "刚好 32000 不触发（严格大于）")
	require.InDelta(t, 32000*3.59e-7, belowThreshold.InputCost, 1e-12)

	aboveThreshold, err := billing.CalculateCost("qwen3-max",
		UsageTokens{InputTokens: 33000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.True(t, aboveThreshold.LongContextBillingApplied, "超过 32000 必须触发阶梯")
	require.InDelta(t, 33000*3.59e-7*(0.574/0.359), aboveThreshold.InputCost, 1e-12)
	require.InDelta(t, 1000*1.434e-6*(2.294/1.434), aboveThreshold.OutputCost, 1e-12)
}
