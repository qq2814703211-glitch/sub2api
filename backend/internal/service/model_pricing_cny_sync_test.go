//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本文件钉死「人民币官方价（cny-official）」这一档数据源与三档口径合并：
//
//	usd < cny-official < manual
//
// 数据全部取自 basellm/llm-metadata 实测结构：
//   - deepseek 用 cost.currency_options.CNY 独立分支（provider 无 currency 字段）；
//   - alibaba-cn / zhipuai 整厂商以 CNY 计价（cost.input 本身就是人民币）；
//   - qwen3-max 有两档长上下文阶梯（32000 / 128000）；
//   - deepseek-flash 有 UTC 分时谷峰（01:00-04:00、06:00-10:00）。

// cnyOfficialRegistrySnapshotJSON 是人民币数据源的实测精简快照。
// 价格单位：每百万 token。
const cnyOfficialRegistrySnapshotJSON = `{
  "deepseek": {
    "id": "deepseek",
    "name": "DeepSeek",
    "models": {
      "deepseek-flash": {
        "id": "deepseek-flash",
        "cost": {
          "input": 0.15, "output": 0.6, "cache_read": 0.003,
          "currency_options": {
            "CNY": {
              "input": 1, "output": 4, "cache_read": 0.02, "reasoning": 4,
              "schedule": {
                "timezone": "UTC", "fallback": "off_peak",
                "windows": [{
                  "name": "peak",
                  "weekdays": [1,2,3,4,5],
                  "hours": ["01:00-04:00","06:00-10:00"],
                  "input": 2, "output": 8, "cache_read": 0.04, "reasoning": 8
                }]
              }
            }
          },
          "schedule": {
            "timezone": "UTC", "fallback": "off_peak",
            "windows": [{
              "name": "peak", "weekdays": [1,2,3,4,5],
              "hours": ["01:00-04:00","06:00-10:00"],
              "input": 0.3, "output": 1.2, "cache_read": 0.006
            }]
          }
        }
      },
      "deepseek-v4-pro": {
        "id": "deepseek-v4-pro",
        "cost": {
          "input": 0.66, "output": 1.98,
          "currency_options": {"CNY": {"input": 4.5, "output": 13.5}}
        }
      }
    }
  },
  "alibaba-cn": {
    "id": "alibaba-cn",
    "name": "Alibaba (CN)",
    "currency": "CNY",
    "models": {
      "qwen3-max": {
        "id": "qwen3-max",
        "cost": {
          "input": 0.359, "output": 1.434, "reasoning": 1.434,
          "tiers": [
            {"input": 0.574, "output": 2.294, "reasoning": 2.294,
             "tier": {"size": 32000, "type": "context"}},
            {"input": 1.004, "output": 4.014, "reasoning": 4.014,
             "tier": {"size": 128000, "type": "context"}}
          ]
        }
      },
      "qwen3-coder-plus": {
        "id": "qwen3-coder-plus",
        "cost": {"input": 4, "output": 16, "cache_read": 0.4}
      }
    }
  },
  "zhipuai": {
    "id": "zhipuai",
    "name": "Zhipu AI",
    "currency": "CNY",
    "models": {
      "glm-5.3": {
        "id": "glm-5.3",
        "cost": {"input": 8, "output": 28}
      },
      "glm-4.5-flash": {
        "id": "glm-4.5-flash",
        "cost": {"input": 0, "output": 0}
      }
    }
  },
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "gpt-5.6-sol": {
        "id": "gpt-5.6-sol",
        "cost": {"input": 4, "output": 20, "cache_read": 0.4}
      }
    }
  }
}`

// cnyOfficialModelsDevSnapshotJSON 是对应的 models.dev 快照（USD / 1M tokens）。
// 特意让 deepseek-flash / qwen3-max 也出现在美元档里，用来验证「国模只认人民币、
// 不落美元」：这两个模型的美元价 0.15 / 1.2 绝不能出现在覆盖层里。
const cnyOfficialModelsDevSnapshotJSON = `{
  "deepseek": {
    "id": "deepseek",
    "name": "DeepSeek",
    "models": {
      "deepseek-flash": {
        "id": "deepseek-flash",
        "cost": {"input": 0.15, "output": 0.6, "cache_read": 0.003}
      }
    }
  },
  "alibaba": {
    "id": "alibaba",
    "name": "Alibaba",
    "models": {
      "qwen3-max": {
        "id": "qwen3-max",
        "cost": {"input": 1.2, "output": 6}
      }
    }
  },
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "gpt-5.6-sol": {
        "id": "gpt-5.6-sol",
        "cost": {"input": 4, "output": 20, "cache_read": 0.4}
      }
    }
  }
}`

// cnyOfficialErrUnreachable 模拟人民币数据源网络不可达。
var cnyOfficialErrUnreachable = errors.New("cny-official registry unreachable")

// cnyRoutingFetcher 按 URL 分发两份数据源，模拟真实的两条抓取链路。
type cnyRoutingFetcher struct {
	cnyBody []byte
	cnyErr  error
	usdBody []byte
	usdErr  error
	calls   []string
}

func (f *cnyRoutingFetcher) FetchPricingJSON(_ context.Context, endpoint string) ([]byte, error) {
	f.calls = append(f.calls, endpoint)
	if endpoint == cnyOfficialDefaultSourceURL || endpoint == "https://cny.test/all.json" {
		if f.cnyErr != nil {
			return nil, f.cnyErr
		}
		return f.cnyBody, nil
	}
	if f.usdErr != nil {
		return nil, f.usdErr
	}
	return f.usdBody, nil
}

// stubCNYSettings 是 settings 读取桩（只实现 cnyOfficialSettingsReader）。
type stubCNYSettings struct {
	values map[string]string
	err    error
}

func (s stubCNYSettings) GetRawSettings(_ context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// newCNYSyncTestService 构造一个同时挂着两份数据源的同步服务。
func newCNYSyncTestService(t *testing.T, overrideJSON string, settings *CNYOfficialPricingSettings, fetcher *cnyRoutingFetcher) (*ModelPricingSyncService, string, *stubModelPricingSyncHistory) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	if overrideJSON != "" {
		require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0o644))
	}
	history := &stubModelPricingSyncHistory{}
	values := map[string]string{}
	if settings != nil {
		encoded, err := json.Marshal(settings)
		require.NoError(t, err)
		values[SettingKeyPricingCNYOfficial] = string(encoded)
	}
	if fetcher == nil {
		fetcher = &cnyRoutingFetcher{
			cnyBody: []byte(cnyOfficialRegistrySnapshotJSON),
			usdBody: []byte(cnyOfficialModelsDevSnapshotJSON),
		}
	}
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   path,
			Interval:       time.Minute,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: values},
		},
		nil,
		history,
		fetcher,
	)
	return svc, path, history
}

func readOverrideEntries(t *testing.T, path string) map[string]map[string]float64 {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	// 覆盖层条目除数值价外还会带非数值标记（如 cny_official: true），
	// 所以先解成 any 再只挑数值字段，避免整体反序列化失败。
	var raw map[string]map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	entries := make(map[string]map[string]float64, len(raw))
	for model, fields := range raw {
		numbers := make(map[string]float64, len(fields))
		for key, value := range fields {
			if number, ok := value.(float64); ok {
				numbers[key] = number
			}
		}
		entries[model] = numbers
	}
	return entries
}

// TestCNYOfficial_DeepSeekFlashParsesOfficialRMB 是 brief 的第一条硬要求：
// deepseek-flash 的人民币官方价是 input 1 / output 4 / cache_read 0.02（每百万），
// 覆盖层按每 token 存 → 除以 1e6。
func TestCNYOfficial_DeepSeekFlashParsesOfficialRMB(t *testing.T) {
	dataset := buildCNYOfficialDataset(mustParseCNYRegistry(t, cnyOfficialRegistrySnapshotJSON), &CNYOfficialPricingSettings{Enabled: true}, "test")

	priced, ok := dataset.pricing("deepseek-flash")
	require.True(t, ok, "deepseek-flash 必须解析出人民币价")
	require.Equal(t, "deepseek", priced.Provider)

	require.InDelta(t, 1e-6, priced.PerToken["input_cost_per_token"], 1e-15, "1 元 / 1M token")
	require.InDelta(t, 4e-6, priced.PerToken["output_cost_per_token"], 1e-15, "4 元 / 1M token")
	require.InDelta(t, 0.02e-6, priced.PerToken["cache_read_input_token_cost"], 1e-15, "0.02 元 / 1M token")

	// 单位换算的独立校验：每百万 token 的价 × 1e6 应还原成官方牌价。
	require.InDelta(t, 1.0, priced.PerToken["input_cost_per_token"]*1e6, 1e-12)
	require.InDelta(t, 4.0, priced.PerToken["output_cost_per_token"]*1e6, 1e-12)
	require.InDelta(t, 0.02, priced.PerToken["cache_read_input_token_cost"]*1e6, 1e-12)

	// 另一个 currency_options.CNY 模型：4.5 / 13.5 元（不是 USD 的 0.66 / 1.98）。
	pro, ok := dataset.pricing("deepseek-v4-pro")
	require.True(t, ok)
	require.InDelta(t, 4.5e-6, pro.PerToken["input_cost_per_token"], 1e-15)
	require.InDelta(t, 13.5e-6, pro.PerToken["output_cost_per_token"], 1e-15)
}

// mustParseCNYRegistry 把快照解成数据源结构。
func mustParseCNYRegistry(t *testing.T, body string) map[string]cnyOfficialRegistryProvider {
	t.Helper()
	var registry map[string]cnyOfficialRegistryProvider
	require.NoError(t, json.Unmarshal([]byte(body), &registry))
	for vendorID, provider := range registry {
		if provider.ID == "" {
			provider.ID = vendorID
			registry[vendorID] = provider
		}
	}
	return registry
}

// readTestdata 读取 testdata 下的数据源快照，用于离线跑真实数据。
func readTestdata(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

// TestCNYOfficial_NationalModelUsesCNYNotUSD 钉死「国模只认人民币，绝不落美元」。
// 两个数据源都报 deepseek-flash 与 qwen3-max，覆盖层里必须只有人民币数字。
func TestCNYOfficial_NationalModelUsesCNYNotUSD(t *testing.T) {
	svc, path, history := newCNYSyncTestService(t, "", &CNYOfficialPricingSettings{Enabled: true}, nil)

	applied, err := svc.Sync(context.Background())
	require.NoError(t, err)
	require.Greater(t, applied, 0)
	entries := readOverrideEntries(t, path)

	// deepseek-flash：人民币 1e-6，绝不是 USD 的 0.15e-6。
	deepseek := entries["deepseek-flash"]
	require.InDelta(t, 1e-6, deepseek["input_cost_per_token"], 1e-15)
	require.InDelta(t, 4e-6, deepseek["output_cost_per_token"], 1e-15)
	require.InDelta(t, 0.02e-6, deepseek["cache_read_input_token_cost"], 1e-15)

	// qwen3-max：人民币 0.359e-6，绝不是 USD 的 1.2e-6。
	qwen := entries["qwen3-max"]
	require.InDelta(t, 0.359e-6, qwen["input_cost_per_token"], 1e-15)
	require.InDelta(t, 1.434e-6, qwen["output_cost_per_token"], 1e-15)

	// 非国模照旧走美元档。
	openai := entries["gpt-5.6-sol"]
	require.InDelta(t, 4e-6, openai["input_cost_per_token"], 1e-15)

	// 变更史按口径分列：国模记 cny-official，非国模记 models.dev。
	sources := map[string]map[string]struct{}{}
	for _, row := range history.rows {
		if sources[row.Model] == nil {
			sources[row.Model] = map[string]struct{}{}
		}
		sources[row.Model][row.Source] = struct{}{}
	}
	require.Contains(t, sources["deepseek-flash"], ModelPricingSyncSourceCNYOfficial)
	require.NotContains(t, sources["deepseek-flash"], ModelPricingSyncSourceModelsDev,
		"国模不得产生 models.dev 来源的变更")
	require.Contains(t, sources["gpt-5.6-sol"], ModelPricingSyncSourceModelsDev)
}

// TestCNYOfficial_ManualPricingNeverOverwritten 是优先级最高的那条：
// manual > cny-official。覆盖层里手配过的国模，人民币档一个字段都不许动。
func TestCNYOfficial_ManualPricingNeverOverwritten(t *testing.T) {
	const manualInput = 9e-6
	const manualOutput = 9e-5
	overrideJSON := `{
	  "deepseek-flash": {
	    "input_cost_per_token": 9e-06,
	    "output_cost_per_token": 9e-05
	  }
	}`
	svc, path, history := newCNYSyncTestService(t, overrideJSON, &CNYOfficialPricingSettings{Enabled: true}, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.InDelta(t, manualInput, entries["deepseek-flash"]["input_cost_per_token"], 1e-15,
		"手配价必须压过人民币官方价 1e-6")
	require.InDelta(t, manualOutput, entries["deepseek-flash"]["output_cost_per_token"], 1e-15)
	// 手配模型整体跳过：原本没有的 cache_read 也不补。
	require.NotContains(t, entries["deepseek-flash"], "cache_read_input_token_cost")

	for _, row := range history.rows {
		require.NotEqual(t, "deepseek-flash", row.Model, "手配模型不应产生任何同步变更记录")
	}
}

// TestCNYOfficial_OverridesPriorUSDValue 上一轮美元档写过的字段，本轮被人民币
// 官方价覆盖（cny-official > usd）。这是三档里唯一会「主动改数字」的路径。
func TestCNYOfficial_OverridesPriorUSDValue(t *testing.T) {
	// 覆盖层里是上一轮 models.dev 写入的美元数字 0.15e-6，且变更史记着这个值
	// （说明是同步写的、不是运营手配）。
	const priorUSDInput = 0.15e-6
	overrideJSON := `{"deepseek-flash": {"input_cost_per_token": 1.5e-07}}`
	history := &stubModelPricingSyncHistory{syncedSets: map[string]map[string]float64{
		"deepseek-flash": {"input_cost_per_token": priorUSDInput},
	}}
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0o644))

	settings := &CNYOfficialPricingSettings{Enabled: true}
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   path,
			Interval:       time.Minute,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: map[string]string{SettingKeyPricingCNYOfficial: string(encoded)}},
		},
		nil,
		history,
		&cnyRoutingFetcher{
			cnyBody: []byte(cnyOfficialRegistrySnapshotJSON),
			usdBody: []byte(cnyOfficialModelsDevSnapshotJSON),
		},
	)

	_, err = svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.InDelta(t, 1e-6, entries["deepseek-flash"]["input_cost_per_token"], 1e-15,
		"上一轮美元档写过的字段应被人民币官方价覆盖")
	require.Greater(t, absFloat(entries["deepseek-flash"]["input_cost_per_token"]-priorUSDInput), 1e-12,
		"覆盖层里不能再是上一轮的美元数字")

	var drifted *ModelPricingSyncHistory
	for _, row := range history.rows {
		if row.Model == "deepseek-flash" && row.Field == "input_cost_per_token" {
			drifted = row
		}
	}
	require.NotNil(t, drifted)
	require.Equal(t, ModelPricingSyncSourceCNYOfficial, drifted.Source)
	require.InDelta(t, priorUSDInput, drifted.OldValue, 1e-15)
	require.InDelta(t, 1e-6, drifted.NewValue, 1e-15)
}

// TestCNYOfficial_NationalModelWithoutCNYPriceIsSkipped 是辉的硬要求：
// 国模厂商的模型若没有人民币价（这里 glm-4.5-flash 的 cost 全 0），跳过不写，
// 覆盖层不新增条目，绝不回退美元价。
func TestCNYOfficial_NationalModelWithoutCNYPriceIsSkipped(t *testing.T) {
	svc, path, _ := newCNYSyncTestService(t, "{}", &CNYOfficialPricingSettings{Enabled: true}, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.NotContains(t, entries, "glm-4.5-flash",
		"零价国模必须整条跳过，不得写入覆盖层")
	// 同厂商有价的其他模型正常写入，证明跳过是逐模型的。
	require.Contains(t, entries, "glm-5.3")
	require.InDelta(t, 8e-6, entries["glm-5.3"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 28e-6, entries["glm-5.3"]["output_cost_per_token"], 1e-15)

	// 解析报告里能查到被跳过的模型（Status 可观测性）。
	status := svc.Status()
	cnyStatus, ok := status["cny_official"].(map[string]any)
	require.True(t, ok)
	report, ok := cnyStatus["report"].(*CNYOfficialSyncReport)
	require.True(t, ok, "Status 必须带解析报告")
	require.Contains(t, report.SkippedNoCNY, "glm-4.5-flash")
}

// TestCNYOfficial_ManualOverridesWriteConfiguredPrice settings 里 manual_overrides
// 命中的模型必须按配置的人民币价写入覆盖层（manual 是最高优先级一档）。
//
// 历史缺陷：这一档最初只用来「跳过」，价格值被丢弃，导致页面上配置的手工价
// 永远不生效，模型继续按上一版的美元价计费。
func TestCNYOfficial_ManualOverridesWriteConfiguredPrice(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			// 全名命中。
			"deepseek/deepseek-flash": {Input: 1, Output: 4, CacheRead: 0.02},
			// 裸模型名命中。
			"qwen3-max": {Input: 0.5, Output: 2},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	// 命中的模型按手工价写入，且带人民币标记打通计费。
	require.Contains(t, entries, "deepseek-flash", "manual 命中的模型必须写入")
	require.InDelta(t, 1e-6, entries["deepseek-flash"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 4e-6, entries["deepseek-flash"]["output_cost_per_token"], 1e-15)
	require.InDelta(t, 0.02e-6, entries["deepseek-flash"]["cache_read_input_token_cost"], 1e-15)

	require.Contains(t, entries, "qwen3-max", "裸模型名命中同样写入")
	require.InDelta(t, 0.5e-6, entries["qwen3-max"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 2e-6, entries["qwen3-max"]["output_cost_per_token"], 1e-15)

	// 未命中的国模照常写入官方价。
	require.Contains(t, entries, "deepseek-v4-pro")
	require.InDelta(t, 4.5e-6, entries["deepseek-v4-pro"]["input_cost_per_token"], 1e-15)
}

// TestCNYOfficial_ManualOverrideWithoutOfficialPrice 钉死一个真实的生产问题：
// 某个模型在官方人民币源里没有牌价（只有境外厂商的美元价），运营在页面上手工
// 给它配了人民币价。此时必须按手工价写入，不能因为「官方表里没有」就跳过。
func TestCNYOfficial_ManualOverrideWithoutOfficialPrice(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			// 该模型不在人民币数据源里（模拟只有 mistral 美元价的场景）。
			"deepseek-v4.1-flash-1017": {Input: 1, Output: 4, CacheRead: 0.02},
		},
	}
	// 覆盖层里先放一个美元价，模拟线上现状。
	existing := `{"deepseek-v4-1-flash-1017":{"input_cost_per_token":3e-7,"output_cost_per_token":1.2e-6}}`
	svc, path, _ := newCNYSyncTestService(t, existing, settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.Contains(t, entries, "deepseek-v4.1-flash-1017",
		"官方无人民币价但手工配了价的模型必须写入")
	require.InDelta(t, 1e-6, entries["deepseek-v4.1-flash-1017"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 4e-6, entries["deepseek-v4.1-flash-1017"]["output_cost_per_token"], 1e-15)
}

// TestCNYOfficial_TimezoneShiftUTCToShanghai brief 第 5 条：
// 数据源 01:00-04:00 UTC 必须变成站内 09:00-12:00 Asia/Shanghai。
func TestCNYOfficial_TimezoneShiftUTCToShanghai(t *testing.T) {
	dataset := buildCNYOfficialDataset(mustParseCNYRegistry(t, cnyOfficialRegistrySnapshotJSON), &CNYOfficialPricingSettings{Enabled: true}, "test")
	priced, ok := dataset.pricing("deepseek-flash")
	require.True(t, ok)
	require.NotNil(t, priced.TimePricing, "deepseek-flash 有分时谷峰，必须解析出来")

	timePricing := priced.TimePricing
	require.Equal(t, "Asia/Shanghai", timePricing.Timezone)

	// 两个 UTC 时段 → 北京时间 09:00-12:00 与 14:00-18:00。
	require.Len(t, timePricing.Periods, 2)
	require.Equal(t, "09:00", timePricing.Periods[0].StartTime)
	require.Equal(t, "12:00", timePricing.Periods[0].EndTime)
	require.Equal(t, "14:00", timePricing.Periods[1].StartTime)
	require.Equal(t, "18:00", timePricing.Periods[1].EndTime)

	// 倍率：高峰 2 元 / 平峰 1 元 = 2.0（output 8/4 与 cache 0.04/0.02 同为 2.0）。
	for _, period := range timePricing.Periods {
		require.InDelta(t, 2.0, period.Multiplier, 1e-9)
	}

	// 站内可落库形态：工作日开关 + Asia/Shanghai + 平移后的时段。
	require.NotNil(t, timePricing.ChannelTimePricing)
	require.Equal(t, "Asia/Shanghai", timePricing.ChannelTimePricing.Timezone)
	require.True(t, timePricing.ChannelTimePricing.WeekdaysOnly)
	require.NoError(t, validateChannelTimePricing(timePricing.ChannelTimePricing))

	// 时段平移的正确性用站内计费器反向验证：北京时间周一 10:00（= UTC 02:00）为高峰。
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	mondayPeak := time.Date(2026, 11, 9, 10, 0, 0, 0, location)
	require.InDelta(t, 2.0, timePricing.ChannelTimePricing.MultiplierAt(mondayPeak), 1e-9,
		"北京时间周一 10:00 属于高峰段 09:00-12:00")
	mondayOffPeak := time.Date(2026, 11, 9, 13, 0, 0, 0, location)
	require.InDelta(t, 1.0, timePricing.ChannelTimePricing.MultiplierAt(mondayOffPeak), 1e-9,
		"13:00 不在任何高峰段内")

	// 原始 UTC 时段不得出现在落库形态里（错 8 小时的经典形态）。
	for _, period := range timePricing.Periods {
		require.NotEqual(t, "01:00", period.StartTime, "时段必须完成 UTC→Asia/Shanghai 平移")
	}
}

// TestCNYOfficial_CrossDayWindowShiftsWeekday 跨日窗口的星期要跟着平移。
// 22:00-23:00 UTC 周五 → 06:00-07:00 周六（北京时间），星期顺延。
func TestCNYOfficial_CrossDayWindowShiftsWeekday(t *testing.T) {
	registry := mustParseCNYRegistry(t, `{
	  "deepseek": {
	    "id": "deepseek",
	    "models": {
	      "deepseek-cross": {
	        "id": "deepseek-cross",
	        "cost": {
	          "input": 0.1,
	          "currency_options": {"CNY": {
	            "input": 1, "output": 2,
	            "schedule": {"timezone": "UTC", "fallback": "off_peak",
	              "windows": [{"name": "peak", "weekdays": [5],
	                "hours": ["22:00-23:00"], "input": 3, "output": 6}]}
	          }}
	        }
	      }
	    }
	  }
	}`)
	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, "test")
	priced, ok := dataset.pricing("deepseek-cross")
	require.True(t, ok)
	require.NotNil(t, priced.TimePricing)
	require.Len(t, priced.TimePricing.Periods, 1)
	require.Equal(t, "06:00", priced.TimePricing.Periods[0].StartTime)
	require.Equal(t, "07:00", priced.TimePricing.Periods[0].EndTime)

	// 星期五 +1 天 = 星期六，不再是工作日集合 → 不给落库形态，但记冲突。
	require.False(t, priced.TimePricing.WeekdaysOnly)
	require.Nil(t, priced.TimePricing.ChannelTimePricing)
	require.NotEmpty(t, priced.TimePricing.Conflicts)
}

// TestCNYOfficial_MultiTierLadderOnlyExpressesFirstTier brief 第 2 条：
// qwen3-max 有两档（32000 / 128000），覆盖层只表达第 1 档阈值 32000 + 倍率，
// 更高档写不进去但必须在报告里列出。
func TestCNYOfficial_MultiTierLadderOnlyExpressesFirstTier(t *testing.T) {
	dataset := buildCNYOfficialDataset(mustParseCNYRegistry(t, cnyOfficialRegistrySnapshotJSON), &CNYOfficialPricingSettings{Enabled: true}, "test")
	priced, ok := dataset.pricing("qwen3-max")
	require.True(t, ok)
	require.NotNil(t, priced.LongContext, "qwen3-max 有两档阶梯，必须解析")

	ladder := priced.LongContext
	require.Equal(t, 32000, ladder.Threshold, "只表达第一档（最小 size）")
	require.Equal(t, 2, ladder.TierCount, "总档数要记下来")

	// 倍率 = 第一档价 / 基础价，input 与 output 各自算。
	require.InDelta(t, 0.574/0.359, ladder.InputMultiplier, 1e-12)
	require.InDelta(t, 2.294/1.434, ladder.OutputMultiplier, 1e-12)
	// 两档倍率几乎一致（1.5990 vs 1.5997），差异小于容差，不取平均。
	require.Less(t, absFloat(ladder.InputMultiplier-ladder.OutputMultiplier), cnyOfficialMultiplierTolerance)

	// 报告里明确「2 档阶梯，本版只表达第 1 档」。
	require.NotEmpty(t, dataset.Report.Ladders)
	require.Contains(t, dataset.Report.Ladders[0], "qwen3-max")
	require.Contains(t, dataset.Report.Ladders[0], "2 档阶梯")
	require.Contains(t, dataset.Report.Ladders[0], "32000")

	// 落库校验：三个字段成组写入，128000 档不得出现在覆盖层里。
	svc, path, _ := newCNYSyncTestService(t, "{}", &CNYOfficialPricingSettings{Enabled: true}, nil)
	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)
	require.Equal(t, 32000.0, entries["qwen3-max"]["long_context_input_token_threshold"])
	require.InDelta(t, 0.574/0.359, entries["qwen3-max"]["long_context_input_cost_multiplier"], 1e-12)
	require.InDelta(t, 2.294/1.434, entries["qwen3-max"]["long_context_output_cost_multiplier"], 1e-12)
}

// TestCNYOfficial_LadderMultiplierConflictIsAveraged 两侧倍率差异超过容差时取平均，
// 并把冲突写进日志/报告（不阻断同步）。
func TestCNYOfficial_LadderMultiplierConflictIsAveraged(t *testing.T) {
	registry := mustParseCNYRegistry(t, `{
	  "alibaba-cn": {
	    "id": "alibaba-cn",
	    "currency": "CNY",
	    "models": {
	      "qwen-lopsided": {
	        "id": "qwen-lopsided",
	        "cost": {
	          "input": 1, "output": 1,
	          "tiers": [{"input": 3, "output": 1.5, "tier": {"size": 32000, "type": "context"}}]
	        }
	      }
	    }
	  }
	}`)
	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, "test")
	priced, ok := dataset.pricing("qwen-lopsided")
	require.True(t, ok)
	require.NotNil(t, priced.LongContext)

	// input x3、output x1.5，差 1.5 > 0.001 → 取平均 2.25。
	require.InDelta(t, 2.25, priced.LongContext.InputMultiplier, 1e-12)
	require.InDelta(t, 2.25, priced.LongContext.OutputMultiplier, 1e-12)
	require.NotEmpty(t, dataset.Report.Conflicts)
	require.Contains(t, dataset.Report.Conflicts[0], "倍率两侧不一致")
}

// TestCNYOfficial_DisabledByDefault 没有 pricing.cny_official 时行为与引入三档前
// 完全一致：只跑美元档，国模也按 models.dev 的美元价写（此时没有人民币口径可依）。
func TestCNYOfficial_DisabledByDefault(t *testing.T) {
	svc, path, _ := newCNYSyncTestService(t, "{}", nil, nil)

	applied, err := svc.Sync(context.Background())
	require.NoError(t, err)
	require.Greater(t, applied, 0)
	entries := readOverrideEntries(t, path)

	// 关闭时 deepseek-flash 走美元档 0.15e-6。
	require.InDelta(t, 0.15e-6, entries["deepseek-flash"]["input_cost_per_token"], 1e-15)
	require.Contains(t, entries, "gpt-5.6-sol")
}

// TestCNYOfficial_EnabledButExplicitlyFalse settings 里 enabled=false 时同样关闭。
func TestCNYOfficial_EnabledButExplicitlyFalse(t *testing.T) {
	svc, path, _ := newCNYSyncTestService(t, "{}", &CNYOfficialPricingSettings{Enabled: false}, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)
	require.InDelta(t, 0.15e-6, entries["deepseek-flash"]["input_cost_per_token"], 1e-15)
}

// TestCNYOfficial_FetchFailureAbortsWholeSync 人民币档开启但抓取失败时整轮放弃：
// 只跑美元档会把已按人民币配好的国模改回美元数字，那是静默的 7 倍降价。
func TestCNYOfficial_FetchFailureAbortsWholeSync(t *testing.T) {
	const original = `{"deepseek-flash": {"input_cost_per_token": 1e-06}}`
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	encoded, err := json.Marshal(&CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   path,
			Interval:       time.Minute,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: map[string]string{SettingKeyPricingCNYOfficial: string(encoded)}},
		},
		nil,
		&stubModelPricingSyncHistory{},
		&cnyRoutingFetcher{cnyErr: cnyOfficialErrUnreachable, usdBody: []byte(cnyOfficialModelsDevSnapshotJSON)},
	)

	_, err = svc.Sync(context.Background())
	require.Error(t, err)

	body, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.JSONEq(t, original, string(body), "人民币档失败不得改动覆盖层")

	// 状态里能看到人民币档的错误。
	status := svc.Status()
	cnyStatus, ok := status["cny_official"].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, cnyStatus["last_error"])
}

// TestCNYOfficial_SettingsParseErrorAbortsSync 配置写坏时整轮放弃，
// 不猜默认值（既不能当关闭，也不能静默按美元跑）。
func TestCNYOfficial_SettingsParseErrorAbortsSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"deepseek-flash": {"input_cost_per_token": 1e-06}}`), 0o644))

	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   path,
			Interval:       time.Minute,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: map[string]string{SettingKeyPricingCNYOfficial: `{"enabled": tru`}},
		},
		nil,
		&stubModelPricingSyncHistory{},
		&cnyRoutingFetcher{cnyBody: []byte(cnyOfficialRegistrySnapshotJSON), usdBody: []byte(cnyOfficialModelsDevSnapshotJSON)},
	)

	_, err := svc.Sync(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), SettingKeyPricingCNYOfficial)
}

// TestCNYOfficial_StatusExposesPriorityAndReport 三档优先级与解析结果必须能通过
// Status() 查到（brief 要求分时解析结果可观测）。
func TestCNYOfficial_StatusExposesPriorityAndReport(t *testing.T) {
	svc, _, _ := newCNYSyncTestService(t, "", &CNYOfficialPricingSettings{Enabled: true}, nil)
	_, err := svc.Sync(context.Background())
	require.NoError(t, err)

	status := svc.Status()
	require.Equal(t, []string{ModelPricingTierUSD, ModelPricingTierCNYOfficial, ModelPricingTierManual},
		status["tier_priority"], "口径优先级必须是 usd < cny-official < manual")

	cnyStatus, ok := status["cny_official"].(map[string]any)
	require.True(t, ok)
	require.True(t, cnyStatus["enabled"].(bool))
	require.Equal(t, "Asia/Shanghai", cnyStatus["site_timezone"])
	require.NotEmpty(t, cnyStatus["national_vendor"])

	report, ok := cnyStatus["report"].(*CNYOfficialSyncReport)
	require.True(t, ok)
	// 分时解析结果可通过 Status 查到（本版不落 channel_model_pricing）。
	require.NotEmpty(t, report.TimePricing)
	found := false
	for _, item := range report.TimePricing {
		if item.Model == "deepseek-flash" {
			found = true
			require.Equal(t, "09:00", item.Periods[0].StartTime, "Status 里的时段已平移")
		}
	}
	require.True(t, found)
	require.Greater(t, report.Applied, 0)
}

// TestCNYOfficial_PlanVendorDoesNotShadowMeteredPrice 套餐条目（cost 全 0）不得
// 遮蔽按量厂商的真实牌价：qwen3-coder-plus 同时出现在 alibaba-cn（4 元）与
// alibaba-coding-plan-cn（0 元），必须取 4 元。
func TestCNYOfficial_PlanVendorDoesNotShadowMeteredPrice(t *testing.T) {
	registry := mustParseCNYRegistry(t, `{
	  "alibaba-cn": {
	    "id": "alibaba-cn", "currency": "CNY",
	    "models": {"qwen3-coder-plus": {"id": "qwen3-coder-plus",
	      "cost": {"input": 4, "output": 16}}}
	  },
	  "alibaba-coding-plan-cn": {
	    "id": "alibaba-coding-plan-cn", "currency": "CNY",
	    "models": {"qwen3-coder-plus": {"id": "qwen3-coder-plus",
	      "cost": {"input": 0, "output": 0, "cache_read": 0}}}
	  }
	}`)
	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, "test")
	priced, ok := dataset.pricing("qwen3-coder-plus")
	require.True(t, ok)
	require.Equal(t, "alibaba-cn", priced.Provider, "套餐厂商排序在后，不遮蔽按量价")
	require.InDelta(t, 4e-6, priced.PerToken["input_cost_per_token"], 1e-15)
}

// TestCNYOfficial_DeveloperWhitelistMatchesSpec 白名单必须是 brief 里实测的 18 个，
// 少一个就会让该厂商的模型悄悄回退到美元价。
func TestCNYOfficial_DeveloperWhitelistMatchesSpec(t *testing.T) {
	want := []string{
		"alibaba-cn", "alibaba-coding-plan-cn", "alibaba-token-plan-cn", "bailing",
		"minimax-cn", "minimax-cn-coding-plan", "moonshotai-cn", "sensenova", "stepfun",
		"stepfun-step-plan", "tencent-coding-plan", "tencent-token-plan", "tencent-tokenhub",
		"volcengine", "volcengine-coding-plan", "xiaomi-token-plan-cn", "zhipuai",
		"zhipuai-coding-plan",
	}
	require.Len(t, cnyOfficialProviderWhitelist, len(want))
	for _, vendor := range want {
		require.Contains(t, cnyOfficialProviderWhitelist, vendor)
	}
}

// TestCNYOfficial_ParsesRealSnapshotShape 用真实数据源的完整条目校验解析健壮性：
// provider 无 currency 字段但模型带 currency_options.CNY 时必须识别为人民币厂商。
func TestCNYOfficial_ParsesRealSnapshotShape(t *testing.T) {
	registry := mustParseCNYRegistry(t, cnyOfficialRegistrySnapshotJSON)
	provider := registry["deepseek"]
	require.Empty(t, provider.Currency, "真实 deepseek 条目没有 currency 字段")
	require.True(t, isCNYOfficialProvider("deepseek", provider),
		"currency_options.CNY 分支必须让厂商被识别为人民币口径")

	// 非人民币厂商（只有美元价）不得被误判。
	require.False(t, isCNYOfficialProvider("openai", registry["openai"]))
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// TestCNYOfficial_ManualLongContextWritesLadder 钉死 manual 里的长上下文阶梯：
// 阈值 + 输入倍率 + 输出倍率三字段成组写入覆盖层，供计费侧读取。
func TestCNYOfficial_ManualLongContextWritesLadder(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek-v4.1-flash-1017": {
				Input: 1, Output: 4, CacheRead: 0.02,
				LongContextThreshold:        32000,
				LongContextInputMultiplier:  1.5,
				LongContextOutputMultiplier: 2,
			},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	entry := entries["deepseek-v4.1-flash-1017"]
	require.NotNil(t, entry)
	require.InDelta(t, 1e-6, entry["input_cost_per_token"], 1e-15)
	require.InDelta(t, 32000, entry["long_context_input_token_threshold"], 1e-9)
	require.InDelta(t, 1.5, entry["long_context_input_cost_multiplier"], 1e-9)
	require.InDelta(t, 2, entry["long_context_output_cost_multiplier"], 1e-9)
}

// TestCNYOfficial_ManualLongContextOmittedWhenEmpty 确认留空不写阶梯字段：
// 阈值留 0 时三个字段都不应出现，否则计费侧会误判为「已配置阶梯」。
func TestCNYOfficial_ManualLongContextOmittedWhenEmpty(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek-v4.1-flash-1017": {Input: 1, Output: 4},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	entry := entries["deepseek-v4.1-flash-1017"]
	require.NotNil(t, entry)
	require.NotContains(t, entry, "long_context_input_token_threshold")
	require.NotContains(t, entry, "long_context_input_cost_multiplier")
	require.NotContains(t, entry, "long_context_output_cost_multiplier")
}

// TestCNYOfficial_ManualLongContextOnlyOutputSide 只配一侧倍率时另一侧保持 1，
// 与官方数据的写法一致（倍率 <= 1 在计费侧判定为该侧不涨价）。
func TestCNYOfficial_ManualLongContextOnlyOutputSide(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"deepseek-v4.1-flash-1017": {
				Input: 1, Output: 4,
				LongContextThreshold:        32000,
				LongContextOutputMultiplier: 2,
			},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entry := readOverrideEntries(t, path)["deepseek-v4.1-flash-1017"]
	require.InDelta(t, 1, entry["long_context_input_cost_multiplier"], 1e-9)
	require.InDelta(t, 2, entry["long_context_output_cost_multiplier"], 1e-9)
}

// TestCNYOfficial_SlashInModelNameIsNotAPrefix 钉死一个会静默改错价的生产问题。
//
// 真实模型名本身可能带斜杠（生产覆盖层里就有 `siliconflow/deepseek-v3.2`，
// 单价 0.27）。早先版本把斜杠前的部分当厂商前缀剥掉，导致：
//   1. 给 `siliconflow/deepseek-v3.2` 配的价被写到裸名 `deepseek-v3.2` 上；
//   2. 如果同时存在同名裸模型，它的价被覆盖（实测 0.27 → 55）。
//
// 键必须原样当作模型名。两种键共存的场合各自独立。
func TestCNYOfficial_SlashInModelNameIsNotAPrefix(t *testing.T) {
	// 覆盖层里两条都真实存在，价格不同。
	existing := `{
		"siliconflow/deepseek-v3.2": {"input_cost_per_token": 2.7e-7, "output_cost_per_token": 1.08e-6},
		"deepseek-v3.2":             {"input_cost_per_token": 1e-6,  "output_cost_per_token": 4e-6}
	}`
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			// 只给带斜杠这一条配手工价。
			"siliconflow/deepseek-v3.2": {Input: 55, Output: 55},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, existing, settings, nil)
	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.InDelta(t, 55e-6, entries["siliconflow/deepseek-v3.2"]["input_cost_per_token"], 1e-15,
		"带斜杠的真实模型名必须写到自己名下")
	require.InDelta(t, 55e-6, entries["siliconflow/deepseek-v3.2"]["output_cost_per_token"], 1e-15)

	// 裸名那条不能被动到 —— 它是另一个模型。
	require.InDelta(t, 1e-6, entries["deepseek-v3.2"]["input_cost_per_token"], 1e-15,
		"给带斜杠模型配价不能污染同名裸模型")
	require.InDelta(t, 4e-6, entries["deepseek-v3.2"]["output_cost_per_token"], 1e-15)
}

// TestCNYOfficial_ManualKeyIsExactModelName manual 的键就是完整模型名，
// 不做任何厂商区分、不剥前缀、不做 lastSegment 猜测。
//
// 只按一字不差的模型名命中：配 `deepseek-v3.2` 只影响 `deepseek-v3.2`，
// 配 `siliconflow/deepseek-v3.2` 只影响它自己。两者是不同的模型。
func TestCNYOfficial_ManualKeyIsExactModelName(t *testing.T) {
	existing := `{
		"deepseek-v3.2":             {"input_cost_per_token": 5.8e-7},
		"siliconflow/deepseek-v3.2": {"input_cost_per_token": 2.7e-7}
	}`
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			// 只配裸名这一条。
			"deepseek-v3.2": {Input: 1, Output: 4},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, existing, settings, nil)
	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entries := readOverrideEntries(t, path)

	require.InDelta(t, 1e-6, entries["deepseek-v3.2"]["input_cost_per_token"], 1e-15,
		"裸名配置应写到裸名条目")
	require.InDelta(t, 2.7e-7, entries["siliconflow/deepseek-v3.2"]["input_cost_per_token"], 1e-15,
		"带斜杠的同名模型是完全不同的模型，不能被裸名配置波及")
}

// TestCNYOfficial_ManualExtraFieldsWriteToOverride 确认「可选计费项」真的落到
// 覆盖层的对应字段上，且单位折算正确：
//
//   - token 类按「元 / 百万」÷ 1e6（与默认四项同口径）
//   - 图片按张价（元 / 张）不除 1e6
//
// 未填写的项必须不出现在覆盖层里 —— 写 0 会让计费侧把它当成「显式配置了 0 价」。
func TestCNYOfficial_ManualExtraFieldsWriteToOverride(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"glm-4.6": {
				Input: 0.6, Output: 2.2, CacheRead: 0.11, CacheWrite: 0.11,
				CacheWrite1h:        0.22,
				InputPriority:       1.2,
				OutputPriority:      4.4,
				CacheReadPriority:   0.22,
				CacheWritePriority:  0.22,
				ImageInput:          1.8,
				ImageCacheRead:      0.2,
				ImageOutputToken:    8.8,
				ImageOutputPerImage: 0.5,
				CacheStoragePerHour: 4.5,
			},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entry := readOverrideEntries(t, path)["glm-4.6"]
	require.NotNil(t, entry)

	// token 类：÷ 1e6
	require.InDelta(t, 0.22e-6, entry["cache_creation_input_token_cost_above_1hr"], 1e-15)
	require.InDelta(t, 1.2e-6, entry["input_cost_per_token_priority"], 1e-15)
	require.InDelta(t, 4.4e-6, entry["output_cost_per_token_priority"], 1e-15)
	require.InDelta(t, 0.22e-6, entry["cache_read_input_token_cost_priority"], 1e-15)
	require.InDelta(t, 0.22e-6, entry["cache_creation_input_token_cost_priority"], 1e-15)
	require.InDelta(t, 1.8e-6, entry["input_cost_per_image_token"], 1e-15)
	require.InDelta(t, 0.2e-6, entry["cache_read_input_image_token_cost"], 1e-15)
	require.InDelta(t, 8.8e-6, entry["output_cost_per_image_token"], 1e-15)
	require.InDelta(t, 4.5e-6, entry["cache_storage_cost_per_token_per_hour"], 1e-15)

	// 按张价：不除 1e6
	require.InDelta(t, 0.5, entry["output_cost_per_image"], 1e-12)

	// 默认四项仍在
	require.InDelta(t, 0.6e-6, entry["input_cost_per_token"], 1e-15)
	require.InDelta(t, 2.2e-6, entry["output_cost_per_token"], 1e-15)
}

// TestCNYOfficial_ManualExtraFieldsOmittedWhenZero 确认没填的可选计费项不写入。
// 写 0 与不写语义不同：0 会被计费侧当成「显式配置了免费」。
func TestCNYOfficial_ManualExtraFieldsOmittedWhenZero(t *testing.T) {
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"glm-4.6": {Input: 0.6, Output: 2.2, CacheRead: 0.11, CacheWrite: 0.11},
		},
	}
	svc, path, _ := newCNYSyncTestService(t, "{}", settings, nil)

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entry := readOverrideEntries(t, path)["glm-4.6"]
	require.NotNil(t, entry)

	for _, extra := range cnyOfficialManualExtraFields {
		_, exists := entry[extra.OverrideField]
		require.False(t, exists, "未填写的计费项 %s 不应出现在覆盖层", extra.OverrideField)
	}
}

// TestCNYOfficial_ManualRemovedExtraFieldIsPruned 确认把已添加的计费项从条目上
// 删掉后，覆盖层里的旧值也被删掉。
//
// 这是本功能最容易漏的一条：页面上看不见了，计费却还按旧值收，运营无从察觉。
// 判据用同步史（history）确认该值确实是自己写的，不碰运营在文件里手配的值。
func TestCNYOfficial_ManualRemovedExtraFieldIsPruned(t *testing.T) {
	// 覆盖层里已有上次写入的 image_output_token 与 1h 缓存价。
	const seeded = `{
		"glm-4.6": {
			"input_cost_per_token": 6e-7,
			"output_cost_per_token": 2.2e-6,
			"output_cost_per_image_token": 8.8e-6,
			"cache_creation_input_token_cost_above_1hr": 2.2e-7
		}
	}`
	// 这次配置里这两个项都被删掉了（不再出现）。
	settings := &CNYOfficialPricingSettings{
		Enabled: true,
		ManualOverrides: map[string]CNYOfficialManualPrice{
			"glm-4.6": {Input: 0.6, Output: 2.2},
		},
	}
	svc, path, history := newCNYSyncTestService(t, seeded, settings, nil)
	// 同步史声明这两个字段是 cny-official 写的、值就是覆盖层里的值。
	history.syncedSets = map[string]map[string]float64{
		"glm-4.6": {
			"output_cost_per_image_token":               8.8e-6,
			"cache_creation_input_token_cost_above_1hr": 2.2e-7,
		},
	}

	_, err := svc.Sync(context.Background())
	require.NoError(t, err)
	entry := readOverrideEntries(t, path)["glm-4.6"]
	require.NotNil(t, entry)

	_, hasImage := entry["output_cost_per_image_token"]
	require.False(t, hasImage, "从条目上删掉的按张图片价应同时从覆盖层删除")
	_, has1h := entry["cache_creation_input_token_cost_above_1hr"]
	require.False(t, has1h, "从条目上删掉的 1h 缓存写价应同时从覆盖层删除")

	// 默认四项不受影响。
	require.InDelta(t, 0.6e-6, entry["input_cost_per_token"], 1e-15)
}

// TestCNYOfficial_ManualPruneRespectsOperatorHandEdits 是上一条的阴性对照：
// 覆盖层里的值若与同步史不符（运营直接在文件里改过），或有值但从未被同步写过，
// 都不能删 —— 那不是本功能的产物。
//
// 没有这条，「删除」会变成把别人手配的价一起清掉。
func TestCNYOfficial_ManualPruneRespectsOperatorHandEdits(t *testing.T) {
	current := map[string]float64{
		// 同步史说写过 8.8e-6，实际是 9.9e-6 → 运营改过，不算自己的，不删。
		"output_cost_per_image_token": 9.9e-6,
		// 从未被同步写过 → 来自目录或文件手配，不删。
		"input_cost_per_image_token": 1.8e-6,
	}
	history := map[string]float64{
		"output_cost_per_image_token": 8.8e-6,
	}

	pruned := cnyOfficialManualPruneFields(nil, nil, current, history)
	require.Empty(t, pruned, "运营改过的值与从未同步过的值都不能被删除")

	// 正控：值确实由同步写入且没人改过时，才删。
	current["input_cost_per_image_token"] = 1.8e-6
	history["input_cost_per_image_token"] = 1.8e-6
	current["output_cost_per_image_token"] = 8.8e-6
	pruned = cnyOfficialManualPruneFields(nil, nil, current, history)
	require.ElementsMatch(t, []string{"input_cost_per_image_token", "output_cost_per_image_token"}, pruned)
}

// TestCNYOfficial_ManualExtraFieldsMatchOverrideTags 是「可选计费项登记表」与
// 覆盖层结构体的一致性守卫。
//
// 登记表里的 OverrideField 拼错一个字母，值照样写进覆盖层，但计费侧永远读不到
// 那个字段 —— 页面上配了、钱不算，而且没有任何报错。这条测试用内省把名字钉死。
func TestCNYOfficial_ManualExtraFieldsMatchOverrideTags(t *testing.T) {
	tags := map[string]bool{}
	typ := reflect.TypeOf(LiteLLMModelPricing{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		tags[strings.Split(tag, ",")[0]] = true
	}

	for _, extra := range cnyOfficialManualExtraFields {
		require.True(t, tags[extra.OverrideField],
			"计费项 %q 的覆盖层字段 %q 在 LiteLLMModelPricing 里不存在", extra.Key, extra.OverrideField)
	}
	// 默认四项与阶梯三字段也一起核，防止同步字段表被改坏。
	for _, field := range modelPricingSyncFields {
		require.True(t, tags[field], "默认字段 %q 在 LiteLLMModelPricing 里不存在", field)
	}
	for _, field := range cnyOfficialLadderOverrideFields() {
		require.True(t, tags[field], "阶梯字段 %q 在 LiteLLMModelPricing 里不存在", field)
	}

	// 负控：证明内省真有检测力，不是恒真。
	require.False(t, tags["cache_creation_input_token_cost_above_2hr"],
		"不存在的字段却被查到，说明本测试失去了检测力")
}

// TestCNYOfficial_ManualExtraValueCoversAllKeys 确保每个登记项都能被 extraValue
// 取到值。漏一个就是「页面上能填、后端永远读到 0」。
func TestCNYOfficial_ManualExtraValueCoversAllKeys(t *testing.T) {
	probe := CNYOfficialManualPrice{
		CacheWrite1h: 1, InputPriority: 2, OutputPriority: 3, CacheReadPriority: 4,
		CacheWritePriority: 5, ImageInput: 6, ImageCacheRead: 7, ImageOutputToken: 8,
		ImageOutputPerImage: 9, CacheStoragePerHour: 10,
	}
	want := map[string]float64{
		"cache_write_1h": 1, "input_priority": 2, "output_priority": 3,
		"cache_read_priority": 4, "cache_write_priority": 5, "image_input": 6,
		"image_cache_read": 7, "image_output_token": 8, "image_output_per_image": 9,
		"cache_storage_per_hour": 10,
	}
	for _, extra := range cnyOfficialManualExtraFields {
		expected, ok := want[extra.Key]
		require.True(t, ok, "登记表里的 key %q 没有对应的取值用例", extra.Key)
		require.Equal(t, expected, probe.extraValue(extra.Key), "extraValue(%q) 取值不对", extra.Key)
	}
}
