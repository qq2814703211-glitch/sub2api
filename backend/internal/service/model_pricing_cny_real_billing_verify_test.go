package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/stretchr/testify/require"
)

// 用验收栈真实生成的覆盖层文件，验证计费链路拿到的是人民币价而不是美元兜底。
func cnyAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestCNYOfficial_RealOverrideBillsRMB 是端到端回归：用验收栈真实同步出来的
// 覆盖层文件，验证默认价卡路径下计费取到的是**人民币价**，而不是 DeepSeek
// 美元谷价兜底；同时钉死峰谷倍增在这条路径上依然生效（谷 ¥1 → 峰 ¥2）。
//
// 缺文件时跳过（该文件由同步任务产出，不随仓库提交）。
func TestCNYOfficial_RealOverrideBillsRMB(t *testing.T) {
	body, err := os.ReadFile("/Users/dev/Global-session/xiao-013/acc-ov3.json")
	if err != nil {
		t.Skip("验收栈覆盖层文件不存在，跳过")
	}
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))

	cfg := &config.Config{}
	svc := NewPricingService(cfg, nil)
	parsed, err := svc.parsePricingData(body)
	require.NoError(t, err)
	svc.pricingData = parsed

	bs := NewBillingService(cfg, svc)

	// 谷时：周一 05:00 UTC 不在高峰窗口
	trough := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	for _, model := range []string{"deepseek-flash", "glm-4.6", "kimi-k3"} {
		p, err := bs.getModelPricingAt(model, trough)
		require.NoError(t, err, model)
		t.Logf("%-16s 计费取到: in=¥%.4f/M out=¥%.4f/M cache=¥%.4f/M",
			model, p.InputPricePerToken*1e6, p.OutputPricePerToken*1e6, p.CacheReadPricePerToken*1e6)
		if cnyAbs(p.InputPricePerToken-deepseekFlashOffPeakInputPrice) < 1e-12 && model == "deepseek-flash" {
			t.Fatalf("%s 用了美元谷价兜底 %.10g", model, p.InputPricePerToken)
		}
	}

	// deepseek-flash 必须正好是 ¥1/4/0.02
	p, err := bs.getModelPricingAt("deepseek-flash", trough)
	require.NoError(t, err)
	require.InDelta(t, 1e-6, p.InputPricePerToken, 1e-12, "¥1/M")
	require.InDelta(t, 4e-6, p.OutputPricePerToken, 1e-12, "¥4/M")
	require.InDelta(t, 2e-8, p.CacheReadPricePerToken, 1e-13, "¥0.02/M")

	// 峰时：走真实计费入口 calculateTokenCost，峰谷倍增在这里叠加。
	peak := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	troughTime := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	tokens := UsageTokens{InputTokens: 1_000_000, OutputTokens: 0}

	resolved := NewModelPricingResolver(nil, bs).Resolve(context.Background(), PricingInput{Model: "deepseek-flash"})
	require.NotNil(t, resolved)

	troughCost, err := bs.calculateTokenCost(resolved, CostInput{
		Model: "deepseek-flash", Tokens: tokens, RateMultiplier: 1, PricingAt: troughTime,
	})
	require.NoError(t, err)
	peakCost, err := bs.calculateTokenCost(resolved, CostInput{
		Model: "deepseek-flash", Tokens: tokens, RateMultiplier: 1, PricingAt: peak,
	})
	require.NoError(t, err)
	t.Logf("谷时 1M 输入成本=%.10f  峰时 1M 输入成本=%.10f", troughCost.TotalCost, peakCost.TotalCost)
	// 站内 TotalCost 以「美元」为单位承载，人民币口径下 1M 输入 = 1.0 = ¥1。
	require.InDelta(t, 1.0, troughCost.TotalCost, 1e-9, "谷时 1M 输入应为 ¥1")
	require.InDelta(t, 2.0, peakCost.TotalCost, 1e-9, "峰时 1M 输入应为 ¥2（谷价 x2）")
}
