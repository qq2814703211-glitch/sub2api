//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 真实 models.dev 注册表的离线快照：只保留与我们字段相关的部分，
// 用来验证「真实数据结构 → 抓取解析 → 差异计算 → 原子写入」整条链路，
// 同时不引入网络依赖。
//
// 结构与 https://models.dev/api.json 一致：顶层 providerID → {models: {modelID: {cost}}},
// cost 单位 USD / 1M tokens。
const modelsDevRegistrySnapshotJSON = `{
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "gpt-6.1-sol": {
        "id": "gpt-6.1-sol",
        "cost": {"input": 2, "output": 10, "cache_read": 0.1, "cache_write": 2.5}
      },
      "gpt-6.1-terra": {
        "id": "gpt-6.1-terra",
        "cost": {"input": 1, "output": 5, "cache_read": 0.05, "cache_write": 1.25}
      },
      "gpt-5.4": {
        "id": "gpt-5.4",
        "cost": {"input": 2.5, "output": 15, "cache_read": 0.25,
                 "tiers": [{"input": 5, "output": 22.5, "cache_read": 0.5}]}
      },
      "gpt-image-1.5": {
        "id": "gpt-image-1.5",
        "type": "image"
      }
    }
  },
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "models": {
      "claude-opus-5-5": {
        "id": "claude-opus-5-5",
        "cost": {"input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5}
      }
    }
  }
}`

// TestModelPricingSync_RealRegistrySnapshotEndToEnd 用真实注册表形态跑一遍完整链路：
// 抓取 → 解析 → 差异 → 原子写 → 变更史，并从覆盖层读回结果核对单位换算。
func TestModelPricingSync_RealRegistrySnapshotEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.json")
	// 覆盖层里已经手配了 gpt-5.4 的输入价 —— 同步不得改它。
	require.NoError(t, os.WriteFile(path, []byte(`{"gpt-5.4": {"input_cost_per_token": 9.9e-06}}`), 0o644))

	history := &stubModelPricingSyncHistory{}
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: time.Minute, RegistryURL: "https://models.dev/api.json"},
		nil, history,
		stubModelPricingSyncFetcher{body: []byte(modelsDevRegistrySnapshotJSON)},
	)

	applied, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)
	require.Greater(t, applied, 0)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &entries))

	// 手配模型一字未改。
	var gpt54Input float64
	require.NoError(t, json.Unmarshal(entries["gpt-5.4"]["input_cost_per_token"], &gpt54Input))
	require.InDelta(t, 9.9e-6, gpt54Input, 1e-15)

	// 未手配的模型被补入，单位从 USD / 1M tokens 换算到 USD / token。
	var solInput, solCacheWrite, terraInput float64
	require.NoError(t, json.Unmarshal(entries["gpt-6.1-sol"]["input_cost_per_token"], &solInput))
	require.NoError(t, json.Unmarshal(entries["gpt-6.1-sol"]["cache_creation_input_token_cost"], &solCacheWrite))
	require.NoError(t, json.Unmarshal(entries["gpt-6.1-terra"]["input_cost_per_token"], &terraInput))
	require.InDelta(t, 2e-6, solInput, 1e-15)
	require.InDelta(t, 2.5e-6, solCacheWrite, 1e-15)
	require.InDelta(t, 1e-6, terraInput, 1e-15)

	// 没带 cost 的模型（图片模型）不产生任何条目。
	require.NotContains(t, entries, "gpt-image-1.5")

	// 第二条：内容与当前一致时是幂等的，不再产生变更、不再写文件。
	before, err := os.Stat(path)
	require.NoError(t, err)
	appliedAgain, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)
	require.Zero(t, appliedAgain, "内容未变时应无变更")
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(), "无变更时不应重写文件")
}

// TestModelPricingSync_RealtimeFetch 在设置了 MODELSDEV_LIVE=1 时用真实网络验证
// 抓取路径。默认跳过，避免单测依赖外网。
func TestModelPricingSync_RealtimeFetch(t *testing.T) {
	if os.Getenv("MODELSDEV_LIVE") != "1" {
		t.Skip("set MODELSDEV_LIVE=1 (with https_proxy) to exercise the real models.dev fetch")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.json")
	history := &stubModelPricingSyncHistory{}
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: time.Minute},
		nil, history, nil, // nil fetcher → 真实 HTTP 客户端
	)
	applied, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err, "真实抓取失败说明是网络问题（检查 https_proxy），不是代码问题")
	require.Greater(t, applied, 0, "真实注册表应产生大量新条目")

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &entries))
	require.NotEmpty(t, entries)
	t.Logf("live sync applied %d field changes across %d models", applied, len(entries))
}
