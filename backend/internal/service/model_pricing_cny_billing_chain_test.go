package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 这一组测试钉死 0.1.3 里最容易被静默破坏的一环：
//
// 人民币官方价（CNY 口径）写进覆盖层之后，必须在**计费**时真正生效，
// 不能被 applyModelSpecificPricingPolicyEx 里「DeepSeek 一律强制回官方
// 低谷美元价」的兜底改写掉。那条兜底是为了修正远端仓库的旧美元价，
// 但它对人民币口径的条目是破坏性的：¥1/百万 → 1e-6 每 token 会被改回
// 1.5e-7（$0.15/百万），人民币价等于白写。

// cnyPricingEntry 模拟同步写进覆盖层的 DeepSeek 人民币口径条目。
// ¥1 / ¥4 / ¥0.02 每百万 token → 1e-6 / 4e-6 / 2e-8 每 token。
func cnyPricingEntry() *LiteLLMModelPricing {
	return &LiteLLMModelPricing{
		InputCostPerToken:       1e-6,
		OutputCostPerToken:      4e-6,
		CacheReadInputTokenCost: 2e-8,
		LiteLLMProvider:         "deepseek",
		CNYOfficial:             true,
	}
}

// TestCNYOfficial_DeepSeekRateSurvivesBillingPolicy 是核心回归：
// 带 cny_official 标记时，策略函数必须原样返回人民币价。
func TestCNYOfficial_DeepSeekRateSurvivesBillingPolicy(t *testing.T) {
	bs := &BillingService{}
	in := &ModelPricing{
		InputPricePerToken:     1e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 2e-8,
		CNYOfficial:            true,
	}
	out := bs.applyModelSpecificPricingPolicyEx("deepseek-flash", in, true, time.Now())
	require.InDelta(t, 1e-6, out.InputPricePerToken, 1e-12, "人民币输入价被覆盖，应保持 ¥1/M")
	require.InDelta(t, 4e-6, out.OutputPricePerToken, 1e-12, "人民币输出价被覆盖，应保持 ¥4/M")
	require.InDelta(t, 2e-8, out.CacheReadPricePerToken, 1e-13, "人民币缓存价被覆盖，应保持 ¥0.02/M")
}

// TestCNYOfficial_HardcodedFallbackStillWorks 保证兜底没被拆掉：
// 没有 cny_official 标记的 DeepSeek 条目（美元目录价 / 远端旧价）
// 仍然被改写成官方低谷美元价。
func TestCNYOfficial_HardcodedFallbackStillWorks(t *testing.T) {
	bs := &BillingService{}

	flash := bs.applyModelSpecificPricingPolicyEx("deepseek-flash", &ModelPricing{
		InputPricePerToken:     9e-7, // 故意给个错价，模拟远端旧值
		OutputPricePerToken:    9e-6,
		CacheReadPricePerToken: 9e-9,
	}, true, time.Now())
	require.InDelta(t, deepseekFlashOffPeakInputPrice, flash.InputPricePerToken, 1e-15, "无标记时必须回落到官方美元谷价")
	require.InDelta(t, deepseekFlashOffPeakOutputPrice, flash.OutputPricePerToken, 1e-15)
	require.InDelta(t, deepseekFlashOffPeakCacheRead, flash.CacheReadPricePerToken, 1e-15)

	pro := bs.applyModelSpecificPricingPolicyEx("deepseek-v4-pro", &ModelPricing{
		InputPricePerToken:  9e-7,
		OutputPricePerToken: 9e-6,
	}, true, time.Now())
	require.InDelta(t, deepseekProOffPeakInputPrice, pro.InputPricePerToken, 1e-15, "pro 档兜底必须保留")
	require.InDelta(t, deepseekProOffPeakOutputPrice, pro.OutputPricePerToken, 1e-15)
}

// TestCNYOfficial_GroupPricingUnaffected 确认分组自定义定价语义没变：
// 非 LiteLLM 来源传 forceDeepSeekRates=false，任何条目都不动。
func TestCNYOfficial_GroupPricingUnaffected(t *testing.T) {
	bs := &BillingService{}
	custom := &ModelPricing{InputPricePerToken: 3e-6, OutputPricePerToken: 5e-6}
	out := bs.applyModelSpecificPricingPolicyEx("deepseek-flash", custom, false, time.Now())
	require.InDelta(t, 3e-6, out.InputPricePerToken, 1e-12, "分组自定义价不得被官方价改写")
	require.InDelta(t, 5e-6, out.OutputPricePerToken, 1e-12)
}

// TestCNYOfficial_PeakMultiplierDoublesRMB 验证谷峰复用：
// 覆盖层存谷价，高峰时段 ×2 得到峰价，正好等于官方 CNY 峰价 2/8/0.04。
func TestCNYOfficial_PeakMultiplierDoublesRMB(t *testing.T) {
	// 2026-11-09 是周一；02:00 UTC 落在高峰窗口 01:00-04:00。
	peak := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	require.Equal(t, 2.0, deepseekPeakMultiplierAt(peak), "周一 02:00 UTC 应为高峰")

	// 同一时刻套用人民币谷价 ×2
	off := cnyPricingEntry()
	got := &ModelPricing{
		InputPricePerToken:     off.InputCostPerToken * deepseekPeakMultiplierAt(peak),
		OutputPricePerToken:    off.OutputCostPerToken * deepseekPeakMultiplierAt(peak),
		CacheReadPricePerToken: off.CacheReadInputTokenCost * deepseekPeakMultiplierAt(peak),
	}
	require.InDelta(t, 2e-6, got.InputPricePerToken, 1e-12, "峰价应为 ¥2/M")
	require.InDelta(t, 8e-6, got.OutputPricePerToken, 1e-12, "峰价应为 ¥8/M")
	require.InDelta(t, 4e-8, got.CacheReadPricePerToken, 1e-13, "峰价应为 ¥0.04/M")

	// 周末全天谷价：2026-11-14 是周六，同小时不算高峰。
	weekend := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	require.Equal(t, 1.0, deepseekPeakMultiplierAt(weekend), "周末应全天谷价")

	// 谷时（周一 05:00 UTC 不在任何窗口）
	trough := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	require.Equal(t, 1.0, deepseekPeakMultiplierAt(trough), "05:00 UTC 不在高峰窗口")
}

// TestCNYOfficial_OverrideFileCarriesMarker 验证同步写入侧：
// 人民币变更落进覆盖层后，条目上必须带 cny_official: true。
func TestCNYOfficial_OverrideFileCarriesMarker(t *testing.T) {
	changes := []modelPricingSyncChange{
		{Model: "deepseek-flash", Field: "input_cost_per_token", NewValue: 1e-6, Source: ModelPricingSyncSourceCNYOfficial},
		{Model: "deepseek-flash", Field: "output_cost_per_token", NewValue: 4e-6, Source: ModelPricingSyncSourceCNYOfficial},
		// 美元档模型不得带标记
		{Model: "gpt-6-sol", Field: "input_cost_per_token", NewValue: 2e-6, Source: ModelPricingSyncSourceModelsDev},
	}
	out := applyModelPricingSyncChanges(
		map[string]json.RawMessage{},
		changes,
		map[string]struct{}{"deepseek-flash": {}},
	)

	var cnyEntry map[string]any
	require.NoError(t, json.Unmarshal(out["deepseek-flash"], &cnyEntry))
	require.Equal(t, true, cnyEntry["cny_official"], "人民币条目必须带 cny_official 标记")
	require.InDelta(t, 1e-6, cnyEntry["input_cost_per_token"], 1e-12)

	var usdEntry map[string]any
	require.NoError(t, json.Unmarshal(out["gpt-6-sol"], &usdEntry))
	_, has := usdEntry["cny_official"]
	require.False(t, has, "美元档条目不应带 cny_official 标记")
}

// TestCNYOfficial_MarkerRoundTripsThroughParser 验证覆盖层解析端：
// 文件里的 cny_official 会被读进 LiteLLMModelPricing，从而进入计费路径。
func TestCNYOfficial_MarkerRoundTripsThroughParser(t *testing.T) {
	var entry LiteLLMRawEntry
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_cost_per_token": 1e-06,
		"output_cost_per_token": 4e-06,
		"cache_read_input_token_cost": 2e-08,
		"cny_official": true,
		"litellm_provider": "deepseek"
	}`), &entry))
	require.NotNil(t, entry.CNYOfficial)
	require.True(t, *entry.CNYOfficial)

	// 缺省（老文件）应为 nil，解析后保持 false，不影响美元档兜底。
	var legacy LiteLLMRawEntry
	require.NoError(t, json.Unmarshal([]byte(`{"input_cost_per_token": 1.5e-07}`), &legacy))
	require.Nil(t, legacy.CNYOfficial)
}

// TestCNYOfficial_MarkerAppliedEvenWhenPriceUnchanged 钉死一个曾经的真实缺陷：
// 标记最初按「本轮产生了变更」置位，导致已经同步过、价格未变因而没有 change 的
// 模型永久缺标记（实测生产上 glm-4.6/glm-4.5/glm-4.5-air 就是这个症状）。
// 标记必须按「数据源里属于人民币口径的模型」置位，与是否有变更无关。
func TestCNYOfficial_MarkerAppliedEvenWhenPriceUnchanged(t *testing.T) {
	// 覆盖层里 glm-4.6 已经是人民币价（0.6/2.2），本轮不会产生任何 change。
	existing := map[string]json.RawMessage{
		"glm-4.6": json.RawMessage(`{"input_cost_per_token":6e-07,"output_cost_per_token":2.2e-06}`),
	}
	out := applyModelPricingSyncChanges(existing, nil, map[string]struct{}{"glm-4.6": {}})

	var entry map[string]any
	require.NoError(t, json.Unmarshal(out["glm-4.6"], &entry))
	require.Equal(t, true, entry["cny_official"],
		"价格未变、无 change 的模型也必须打上 cny_official，否则人民币价会被美元兜底吞掉")
	// 原值不得被改动
	require.InDelta(t, 6e-07, entry["input_cost_per_token"], 1e-15)
	require.InDelta(t, 2.2e-06, entry["output_cost_per_token"], 1e-15)
}

// TestCNYOfficial_ModelSetComesFromDataset 确认集合来源是数据源而不是变更列表。
func TestCNYOfficial_ModelSetComesFromDataset(t *testing.T) {
	require.Nil(t, cnyOfficialModelSet(nil), "空 dataset 应返回 nil")

	ds := &cnyOfficialDataset{Pricing: map[string]*cnyOfficialPricing{
		"deepseek-flash": {},
		"glm-4.6":        {},
	}}
	set := cnyOfficialModelSet(ds)
	require.Len(t, set, 2)
	_, ok := set["deepseek-flash"]
	require.True(t, ok)
	_, ok = set["glm-4.6"]
	require.True(t, ok)
}
