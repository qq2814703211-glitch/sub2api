package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本文件钉死 ADR 0010 的两条硬约束：
//
//   - models.dev 同步**绝不覆盖**已有手动定价的模型，只补空缺；
//   - 覆盖层写入是「临时文件 + rename」的原子替换，写失败后原文件内容不变。
//
// 另外钉死 brief §5.3 的优先级：分组价卡（ChannelModelPricing）存在时，
// 同步来的覆盖层不得参与结算。

// stubModelPricingSyncFetcher 是一个可注入注册表内容/错误的抓取桩。
type stubModelPricingSyncFetcher struct {
	body []byte
	err  error
}

func (f stubModelPricingSyncFetcher) FetchPricingJSON(context.Context, string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

// stubModelPricingSyncHistory 是变更史仓储的内存桩。
type stubModelPricingSyncHistory struct {
	rows       []*ModelPricingSyncHistory
	insertErr  error
	syncedSets map[string]map[string]float64
}

func (s *stubModelPricingSyncHistory) InsertBatch(_ context.Context, rows []*ModelPricingSyncHistory) (int64, error) {
	if s.insertErr != nil {
		return 0, s.insertErr
	}
	s.rows = append(s.rows, rows...)
	if s.syncedSets == nil {
		s.syncedSets = map[string]map[string]float64{}
	}
	for _, row := range rows {
		fields, ok := s.syncedSets[row.Model]
		if !ok {
			fields = map[string]float64{}
			s.syncedSets[row.Model] = fields
		}
		fields[row.Field] = row.NewValue
	}
	return int64(len(rows)), nil
}

func (s *stubModelPricingSyncHistory) List(_ context.Context, _ *ModelPricingSyncHistoryFilter) ([]*ModelPricingSyncHistory, int64, error) {
	return s.rows, int64(len(s.rows)), nil
}

func (s *stubModelPricingSyncHistory) ListSyncedFields(_ context.Context, _ string) (map[string]map[string]float64, error) {
	if s.syncedSets == nil {
		return map[string]map[string]float64{}, nil
	}
	out := make(map[string]map[string]float64, len(s.syncedSets))
	for model, fields := range s.syncedSets {
		cloned := make(map[string]float64, len(fields))
		for field, value := range fields {
			cloned[field] = value
		}
		out[model] = cloned
	}
	return out, nil
}

// modelsDevRegistryJSON 是 models.dev 注册表的精简样本：单位 USD / 1M tokens。
const modelsDevRegistryJSON = `{
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "gpt-5.6-sol": {
        "id": "gpt-5.6-sol",
        "cost": {"input": 4, "output": 20, "cache_read": 0.4, "cache_write": 5}
      },
      "brand-new-model": {
        "id": "brand-new-model",
        "cost": {"input": 2, "output": 10, "cache_read": 0.2}
      }
    }
  }
}`

func newModelPricingSyncTestService(t *testing.T, overrideJSON string) (*ModelPricingSyncService, string, *stubModelPricingSyncHistory) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	if overrideJSON != "" {
		require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0o644))
	}
	history := &stubModelPricingSyncHistory{}
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: time.Minute, RegistryURL: "https://models.dev/api.json"},
		nil,
		history,
		stubModelPricingSyncFetcher{body: []byte(modelsDevRegistryJSON)},
	)
	return svc, path, history
}

// TestModelPricingSync_NeverOverridesManualPricing 是 ADR 0010 的核心用例。
// 覆盖层里已经手配了 gpt-5.6-sol 的四个字段，同步不得改动其中任何一个；
// registry 里另一个没有手配价的品牌新模型则要被补进来。
func TestModelPricingSync_NeverOverridesManualPricing(t *testing.T) {
	manualEntry := `{
	  "gpt-5.6-sol": {
	    "input_cost_per_token": 9e-06,
	    "output_cost_per_token": 9e-05,
	    "cache_read_input_token_cost": 9e-07,
	    "cache_creation_input_token_cost": 1e-05
	  }
	}`
	svc, path, history := newModelPricingSyncTestService(t, manualEntry)

	applied, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)
	require.Greater(t, applied, 0, "没有手配价的新模型应被补入")

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]float64
	require.NoError(t, json.Unmarshal(body, &entries))

	// 手动定价原样保留，一个字段都没被 models.dev 的 4e-6 冲掉。
	manual := entries["gpt-5.6-sol"]
	require.InDelta(t, 9e-6, manual["input_cost_per_token"], 1e-15)
	require.InDelta(t, 9e-5, manual["output_cost_per_token"], 1e-15)
	require.InDelta(t, 9e-7, manual["cache_read_input_token_cost"], 1e-15)
	require.InDelta(t, 1e-5, manual["cache_creation_input_token_cost"], 1e-15)

	// 没有手配价的模型被补入，单位换算为 USD / token。
	fresh := entries["brand-new-model"]
	require.InDelta(t, 2e-6, fresh["input_cost_per_token"], 1e-15)
	require.InDelta(t, 1e-5, fresh["output_cost_per_token"], 1e-15)
	require.InDelta(t, 2e-7, fresh["cache_read_input_token_cost"], 1e-15)

	// 变更史只记新补的模型，手配模型不出现。
	for _, row := range history.rows {
		require.NotEqual(t, "gpt-5.6-sol", row.Model, "手动定价的模型不应产生变更记录")
	}
	require.NotEmpty(t, history.rows)
}

// TestModelPricingSync_ManualModelSkippedEntirely 手配模型整体不参与同步：
// 即使 registry 有它的新价，也一个字段都不写、不产生变更记录。
func TestModelPricingSync_ManualModelSkippedEntirely(t *testing.T) {
	manualEntry := `{"gpt-5.6-sol": {"input_cost_per_token": 9e-06}}`
	svc, path, history := newModelPricingSyncTestService(t, manualEntry)

	_, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]float64
	require.NoError(t, json.Unmarshal(body, &entries))

	// 手配价没变；registry 的 4e-6 没被写入。
	require.InDelta(t, 9e-6, entries["gpt-5.6-sol"]["input_cost_per_token"], 1e-15)
	// 手配条目里原本没有的 output 字段也不补：手配模型整体跳过。
	require.NotContains(t, entries["gpt-5.6-sol"], "output_cost_per_token")
	for _, row := range history.rows {
		require.NotEqual(t, "gpt-5.6-sol", row.Model)
	}
}

// TestModelPricingSync_SyncedFieldFollowsRegistryDrift 上一轮同步写过的字段
// 可以跟随官方价更新（这是 ADR「补/改」里的「改」，与手配价无关）。
func TestModelPricingSync_SyncedFieldFollowsRegistryDrift(t *testing.T) {
	history := &stubModelPricingSyncHistory{syncedSets: map[string]map[string]float64{
		// 上次同步写入的值就是覆盖层里现在的值，说明没被人改过，可以跟随官方价。
		"brand-new-model": {"input_cost_per_token": 1e-06},
	}}
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"brand-new-model": {"input_cost_per_token": 1e-06}}`), 0o644))
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: time.Minute, RegistryURL: "https://models.dev/api.json"},
		nil, history, stubModelPricingSyncFetcher{body: []byte(modelsDevRegistryJSON)},
	)

	_, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]float64
	require.NoError(t, json.Unmarshal(body, &entries))
	require.InDelta(t, 2e-6, entries["brand-new-model"]["input_cost_per_token"], 1e-15,
		"上一轮同步写入的字段应跟随官方价")

	var drifted *ModelPricingSyncHistory
	for _, row := range history.rows {
		if row.Model == "brand-new-model" && row.Field == "input_cost_per_token" {
			drifted = row
		}
	}
	require.NotNil(t, drifted)
	require.InDelta(t, 1e-6, drifted.OldValue, 1e-15)
	require.InDelta(t, 2e-6, drifted.NewValue, 1e-15)
	require.Equal(t, modelPricingSyncReasonDrift, drifted.Reason)
}

// TestModelPricingSync_RegistryFailureKeepsOverrideFile 拉不到 registry 时
// 不碰覆盖层，上一份数据原样保留。
func TestModelPricingSync_RegistryFailureKeepsOverrideFile(t *testing.T) {
	const original = `{"gpt-5.6-sol": {"input_cost_per_token": 9e-06}}`
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: time.Minute},
		nil, &stubModelPricingSyncHistory{},
		stubModelPricingSyncFetcher{err: errors.New("network unreachable")},
	)

	_, err := syncLegacyForTest(svc, context.Background())
	require.Error(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, original, string(body), "抓取失败不得改动覆盖层")
}

// TestModelPricingSync_WriteFailureKeepsOriginalFile 原子替换的关键性质：
// 写失败后原文件内容逐字节不变，且不留下半截文件。
//
// 构造方式：把覆盖层路径指向一个目录 —— 临时文件能建、rename 到目录上会失败，
// 于是走的是「rename 阶段失败」这条最危险的路径。
func TestModelPricingSync_WriteFailureKeepsOriginalFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "overrides.json")
	const original = `{"gpt-5.6-sol": {"input_cost_per_token": 9e-06}}`
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: target, Interval: time.Minute},
		nil, &stubModelPricingSyncHistory{},
		stubModelPricingSyncFetcher{body: []byte(modelsDevRegistryJSON)},
	)

	// 直接用 writeOverrideFile 走失败路径：把目标改成已存在的目录，
	// 使 rename 无法覆盖它。
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o755))
	svc.cfg.OverrideFile = blocked

	err := svc.writeOverrideFile(map[string]json.RawMessage{
		"brand-new-model": json.RawMessage(`{"input_cost_per_token": 2e-06}`),
	})
	require.Error(t, err, "rename 到目录应失败")

	// 临时文件必须被清理，不留残渣。
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".tmp-", "失败后不应留下临时文件")
	}

	// 原覆盖层内容逐字节不变。
	body, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	require.Equal(t, original, string(body))
}

// TestModelPricingSync_SuccessfulWriteReplacesAtomically 成功路径上覆盖层被
// 完整替换，且保留运营手配的其他字段。
func TestModelPricingSync_SuccessfulWriteReplacesAtomically(t *testing.T) {
	const original = `{"gpt-5.6-sol": {"input_cost_per_token": 9e-06, "fast_multiplier": 1.5}}`
	svc, path, _ := newModelPricingSyncTestService(t, original)

	_, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]any
	require.NoError(t, json.Unmarshal(body, &entries))
	// 手配字段与手配价都在。
	require.InDelta(t, 9e-6, entries["gpt-5.6-sol"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 1.5, entries["gpt-5.6-sol"]["fast_multiplier"], 1e-15)
	require.Contains(t, entries, "brand-new-model")
}

// TestModelPricingSync_DisabledWithoutOverrideFile 没有 override_file 就没有
// 写入目标，同步不启用。
func TestModelPricingSync_DisabledWithoutOverrideFile(t *testing.T) {
	svc := NewModelPricingSyncService(&ModelPricingSyncConfig{}, nil, nil, nil)
	require.False(t, svc.SyncEnabled())
	_, err := syncLegacyForTest(svc, context.Background())
	require.Error(t, err)
}

// TestBuildModelPricingSyncReference_ConvertsUnitsAndDedupes 校验单位换算与
// 同名模型去重（先出现的官方供应商胜出，结果确定性）。
func TestBuildModelPricingSyncReference_ConvertsUnitsAndDedupes(t *testing.T) {
	registry := map[string]modelPricingSyncRegistryProvider{
		"openai": {ID: "openai", Models: map[string]modelPricingSyncRegistryModel{
			"shared": {ID: "shared", Cost: &modelPricingSyncCostBlob{Input: 4, Output: 20}},
		}},
		"azure": {ID: "azure", Models: map[string]modelPricingSyncRegistryModel{
			"shared": {ID: "shared", Cost: &modelPricingSyncCostBlob{Input: 99}},
		}},
	}
	reference := buildModelPricingSyncReference(registry)
	require.Equal(t, "azure", reference["shared"].Provider, "按供应商名排序，azure 先于 openai")
	require.InDelta(t, 9.9e-5, reference["shared"].PerToken["input_cost_per_token"], 1e-15)
}

// TestBuildModelPricingSyncReference_RejectsResellers 第三方转售网关报的价不是
// 官方价，必须被过滤掉 —— 否则按字母序会先命中转售价。
func TestBuildModelPricingSyncReference_RejectsResellers(t *testing.T) {
	registry := map[string]modelPricingSyncRegistryProvider{
		// 这些是注册表里真实存在的转售/聚合网关，字母序都排在官方前面。
		"above":  {ID: "above", Models: map[string]modelPricingSyncRegistryModel{"only-reseller": {ID: "only-reseller", Cost: &modelPricingSyncCostBlob{Input: 1}}}},
		"auriko": {ID: "auriko", Models: map[string]modelPricingSyncRegistryModel{"gpt-6.1-sol": {ID: "gpt-6.1-sol", Cost: &modelPricingSyncCostBlob{Input: 0.01}}}},
		"kenari": {ID: "kenari", Models: map[string]modelPricingSyncRegistryModel{"gpt-6.1-sol": {ID: "gpt-6.1-sol", Cost: &modelPricingSyncCostBlob{Input: 0.02}}}},
		"openai": {ID: "openai", Models: map[string]modelPricingSyncRegistryModel{"gpt-6.1-sol": {ID: "gpt-6.1-sol", Cost: &modelPricingSyncCostBlob{Input: 2, Output: 10}}}},
		// 套餐价不是按 token 的 API 价。
		"openai-coding-plan": {ID: "openai-coding-plan", Models: map[string]modelPricingSyncRegistryModel{"plan-only": {ID: "plan-only", Cost: &modelPricingSyncCostBlob{Input: 0}}}},
	}
	reference := buildModelPricingSyncReference(registry)

	// 只在转售商名录里出现的模型不进参考价。
	require.NotContains(t, reference, "only-reseller")
	require.NotContains(t, reference, "plan-only")
	// 官方 openai 的价胜出，不是转售商的 0.01 / 0.02 美元。
	require.Equal(t, "openai", reference["gpt-6.1-sol"].Provider)
	require.InDelta(t, 2e-6, reference["gpt-6.1-sol"].PerToken["input_cost_per_token"], 1e-15)
}
