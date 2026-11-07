//go:build unit

package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 本文件钉死 ADR 0011 的三条口径（OpenAI 缓存写入按 TTL 分档计费）：
//
//   - 30 分钟写入 = 输入价 × 1.25；
//   - 12 小时写入 = 输入价 × 2.5；
//   - 写入价是总价，30 分钟升 12 小时重新计一次 12 小时写入、不抵扣；
//   - 上游只回 cached_tokens（无写入分档）时不产生任何写入计费；
//   - Anthropic 的 5m / 1h 路径行为不变。
//
// 断言用具体金额，不用「大于零」这类松断言，否则把 2.5 改成 1.25 也能绿。

// openAICacheWriteTTLTestPricing 选一组便于手算的价格：输入 $3/MTok、输出 $15/MTok。
// 缓存读按官方 0.1× 输入价填 $0.3/MTok，用于验证「只回 cached_tokens 时按读计」。
func openAICacheWriteTTLTestPricing() *ModelPricing {
	return &ModelPricing{
		InputPricePerToken:     3e-6,
		OutputPricePerToken:    15e-6,
		CacheReadPricePerToken: 3e-7,
	}
}

// TestComputeTokenBreakdown_OpenAICacheWrite30m_BilledAt1_25x 钉死 30 分钟档单价。
// 1000 个 30m 写入 token × 3e-6 × 1.25 = 0.00375。
func TestComputeTokenBreakdown_OpenAICacheWrite30m_BilledAt1_25x(t *testing.T) {
	svc := newTestBillingService()
	bd := svc.computeTokenBreakdown(openAICacheWriteTTLTestPricing(), UsageTokens{
		InputTokens:        0,
		OutputTokens:       0,
		CacheCreationTokens: 1000,
		CacheWrite30mTokens: 1000,
	}, 1.0, "", false)

	require.InDelta(t, 0.00375, bd.CacheWriteTTLCost, 1e-12)
	// 分档 token 已从 CacheCreationTokens 扣除，不应再按 CacheCreationPricePerToken 计一次。
	require.InDelta(t, 0.0, bd.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.00375, bd.TotalCost, 1e-12)
}

// TestComputeTokenBreakdown_OpenAICacheWrite12h_BilledAt2_5x 钉死 12 小时档单价。
// 1000 个 12h 写入 token × 3e-6 × 2.5 = 0.0075。若把 2.5 改成 1.25 这条必红。
func TestComputeTokenBreakdown_OpenAICacheWrite12h_BilledAt2_5x(t *testing.T) {
	svc := newTestBillingService()
	bd := svc.computeTokenBreakdown(openAICacheWriteTTLTestPricing(), UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite12hTokens: 1000,
	}, 1.0, "", false)

	require.InDelta(t, 0.0075, bd.CacheWriteTTLCost, 1e-12)
	require.InDelta(t, 0.0075, bd.TotalCost, 1e-12)
}

// TestComputeTokenBreakdown_OpenAICacheWrite_BothTiersSummedSeparately
// 两档同时上报时各自按自己的倍率计，不混用。
// 400 × 3e-6 × 1.25 = 0.0015；600 × 3e-6 × 2.5 = 0.0045；合计 0.006。
func TestComputeTokenBreakdown_OpenAICacheWrite_BothTiersSummedSeparately(t *testing.T) {
	svc := newTestBillingService()
	bd := svc.computeTokenBreakdown(openAICacheWriteTTLTestPricing(), UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite30mTokens: 400,
		CacheWrite12hTokens: 600,
	}, 1.0, "", false)

	require.InDelta(t, 0.006, bd.CacheWriteTTLCost, 1e-12)
}

// TestComputeTokenBreakdown_OpenAICacheWrite12hUpgrade_BillsFullSecondWrite
// 30 分钟升 12 小时：本次请求上报的是升级后的 12h 写入量，按 2.5× 全额计一次，
// 不抵扣之前已付的 30m 写入。场景：上一轮已按 30m 付 1000 × 3e-6 × 1.25 = 0.00375，
// 本轮升级上报 1000 个 12h 写入，须再付 1000 × 3e-6 × 2.5 = 0.0075。
func TestComputeTokenBreakdown_OpenAICacheWrite12hUpgrade_BillsFullSecondWrite(t *testing.T) {
	svc := newTestBillingService()
	pricing := openAICacheWriteTTLTestPricing()

	firstRound := svc.computeTokenBreakdown(pricing, UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite30mTokens: 1000,
	}, 1.0, "", false)
	require.InDelta(t, 0.00375, firstRound.CacheWriteTTLCost, 1e-12)

	upgradeRound := svc.computeTokenBreakdown(pricing, UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite12hTokens: 1000,
	}, 1.0, "", false)
	// 没有跨请求状态、没有抵扣：本轮就是完整的 2.5× 写入价。
	require.InDelta(t, 0.0075, upgradeRound.CacheWriteTTLCost, 1e-12)
	require.InDelta(t, 0.00375+0.0075, firstRound.CacheWriteTTLCost+upgradeRound.CacheWriteTTLCost, 1e-12)
}

// TestComputeTokenBreakdown_OpenAICacheWrite_BelowInputPriceWouldFail
// 反证：如果写入档被错算成缓存读价（0.1×），1000 token 只会是 0.0003。
// 这条断言存在，是为了让「把 1.25 误写成 0.1」这类改动也暴露出来。
func TestComputeTokenBreakdown_OpenAICacheWrite_NotBilledAsCacheRead(t *testing.T) {
	svc := newTestBillingService()
	bd := svc.computeTokenBreakdown(openAICacheWriteTTLTestPricing(), UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite30mTokens: 1000,
	}, 1.0, "", false)

	require.Greater(t, math.Abs(bd.CacheWriteTTLCost-0.0003), 1e-9)
	require.InDelta(t, 0.00375, bd.CacheWriteTTLCost, 1e-12)
}

// TestComputeTokenBreakdown_OnlyCachedTokens_NoCacheWriteCharge
// 降级口径：上游只回 cached_tokens（缓存读）、不返回写入分档时，绝不臆造写入费。
func TestComputeTokenBreakdown_OnlyCachedTokens_NoCacheWriteCharge(t *testing.T) {
	svc := newTestBillingService()
	bd := svc.computeTokenBreakdown(openAICacheWriteTTLTestPricing(), UsageTokens{
		InputTokens:     500,
		OutputTokens:    100,
		CacheReadTokens: 2000,
	}, 1.0, "", false)

	require.Equal(t, 0.0, bd.CacheWriteTTLCost)
	require.Equal(t, 0.0, bd.CacheCreationCost)
	// 缓存读仍按既有口径计：2000 × 3e-6 × 0.1 = 0.0006。
	require.InDelta(t, 0.0006, bd.CacheReadCost, 1e-12)
}

// TestOpenAICacheWriteTTLTokensFromUsage_AbsentTiersStayZero 上游不带分档字段时解析为 0。
func TestOpenAICacheWriteTTLTokensFromUsage_AbsentTiersStayZero(t *testing.T) {
	usage := gjson.Parse(`{"input_tokens":100,"input_tokens_details":{"cached_tokens":80}}`)
	cacheWrite30m, cacheWrite12h := openAICacheWriteTTLTokensFromUsage(usage)
	require.Zero(t, cacheWrite30m)
	require.Zero(t, cacheWrite12h)
}

// TestOpenAICacheWriteTTLTokensFromUsage_ParsesBothShapes 顶层与 input_tokens_details 下都能读。
func TestOpenAICacheWriteTTLTokensFromUsage_ParsesBothShapes(t *testing.T) {
	nested := gjson.Parse(`{"input_tokens_details":{"cache_write_30m_tokens":120,"cache_write_12h_tokens":80}}`)
	cacheWrite30m, cacheWrite12h := openAICacheWriteTTLTokensFromUsage(nested)
	require.Equal(t, 120, cacheWrite30m)
	require.Equal(t, 80, cacheWrite12h)

	flat := gjson.Parse(`{"cache_write_30m_tokens":7,"cache_write_12h_tokens":9}`)
	cacheWrite30m, cacheWrite12h = openAICacheWriteTTLTokensFromUsage(flat)
	require.Equal(t, 7, cacheWrite30m)
	require.Equal(t, 9, cacheWrite12h)
}

// TestOpenAICacheWriteTTLFromPromptCacheOptions 请求侧 TTL 解析只用于日志口径。
func TestOpenAICacheWriteTTLFromPromptCacheOptions(t *testing.T) {
	require.Equal(t, OpenAICacheWriteTTL12h, openAICacheWriteTTLFromPromptCacheOptions([]byte(`{"prompt_cache_options":{"ttl":"12h"}}`)))
	require.Equal(t, OpenAICacheWriteTTL30m, openAICacheWriteTTLFromPromptCacheOptions([]byte(`{"prompt_cache_options":{"ttl":"30m"}}`)))
	require.Equal(t, OpenAICacheWriteTTL30m, openAICacheWriteTTLFromPromptCacheOptions([]byte(`{}`)))
	require.Equal(t, OpenAICacheWriteTTL30m, openAICacheWriteTTLFromPromptCacheOptions(nil))
}

// TestSplitOpenAICacheWriteTTLTokens_ClampsToTotal 分档之和超过写入总量时按 30m 优先裁剪，
// 结算不高于上游上报的写入总量。
func TestSplitOpenAICacheWriteTTLTokens_ClampsToTotal(t *testing.T) {
	remaining, cacheWrite30m, cacheWrite12h := splitOpenAICacheWriteTTLTokens(UsageTokens{
		CacheCreationTokens: 100,
		CacheWrite30mTokens: 80,
		CacheWrite12hTokens: 90,
	})
	require.Equal(t, 80, cacheWrite30m)
	require.Equal(t, 20, cacheWrite12h)
	require.Zero(t, remaining.CacheCreationTokens)
}

// TestComputeTokenBreakdown_AnthropicTierRegression 回归：Anthropic 5m / 1h 分档口径不受
// 新增的 OpenAI 字段影响。1000 × 3.75e-6 + 1000 × 6e-6 = 0.00975。
func TestComputeTokenBreakdown_AnthropicTierRegression(t *testing.T) {
	svc := newTestBillingService()
	pricing := &ModelPricing{
		InputPricePerToken:      3e-6,
		OutputPricePerToken:     15e-6,
		SupportsCacheBreakdown:  true,
		CacheCreation5mPrice:    3.75e-6,
		CacheCreation1hPrice:    6e-6,
		CacheCreationPricePerToken: 3.75e-6,
	}
	bd := svc.computeTokenBreakdown(pricing, UsageTokens{
		CacheCreationTokens:     2000,
		CacheCreation5mTokens:   1000,
		CacheCreation1hTokens:   1000,
	}, 1.0, "", false)

	require.InDelta(t, 0.00975, bd.CacheCreationCost, 1e-12)
	// Anthropic 路径不产生 OpenAI 分档费。
	require.Equal(t, 0.0, bd.CacheWriteTTLCost)
}

// TestComputeTokenBreakdown_ExplicitTTLPricesOverrideMultiplier 显式价卡优先于官方倍率。
func TestComputeTokenBreakdown_ExplicitTTLPricesOverrideMultiplier(t *testing.T) {
	svc := newTestBillingService()
	pricing := &ModelPricing{
		InputPricePerToken:  3e-6,
		OutputPricePerToken: 15e-6,
		CacheWrite30mPrice:  1e-6,
		CacheWrite12hPrice:  2e-6,
	}
	bd := svc.computeTokenBreakdown(pricing, UsageTokens{
		CacheCreationTokens: 1000,
		CacheWrite30mTokens: 500,
		CacheWrite12hTokens: 500,
	}, 1.0, "", false)

	require.InDelta(t, 500*1e-6+500*2e-6, bd.CacheWriteTTLCost, 1e-12)
}
