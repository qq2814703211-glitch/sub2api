//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCNYOfficial_LiveSourceWritesRMBValues 实拉数据源跑完整 Sync，然后检查
// 落盘文件里国模是人民币数字、非国模是美元数字。默认跳过。
//
//	CNY_OFFICIAL_LIVE=1 https_proxy=http://127.0.0.1:7897 go test -tags unit -run TestCNYOfficial_LiveSourceWritesRMBValues -v
func TestCNYOfficial_LiveSourceWritesRMBValues(t *testing.T) {
	if os.Getenv("CNY_OFFICIAL_LIVE") != "1" {
		t.Skip("set CNY_OFFICIAL_LIVE=1 to fetch the real cny-official source")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o644))
	encoded, err := json.Marshal(&CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)

	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{
			OverrideFile:   path,
			Interval:       1,
			RegistryURL:    "https://models.dev/api.json",
			SettingsReader: stubCNYSettings{values: map[string]string{SettingKeyPricingCNYOfficial: string(encoded)}},
		},
		nil,
		&stubModelPricingSyncHistory{},
		nil,
	)
	_, err = syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	// 条目里除数值外还有 cny_official 这类布尔标记，先解 any 再挑数值。
	var rawEntries map[string]map[string]any
	require.NoError(t, json.Unmarshal(body, &rawEntries))
	entries := make(map[string]map[string]float64, len(rawEntries))
	for model, fields := range rawEntries {
		numbers := make(map[string]float64, len(fields))
		for key, value := range fields {
			if number, ok := value.(float64); ok {
				numbers[key] = number
			}
		}
		entries[model] = numbers
	}

	// 国模：人民币数字。
	require.InDelta(t, 1e-6, entries["deepseek-flash"]["input_cost_per_token"], 1e-15)
	require.InDelta(t, 4e-6, entries["deepseek-flash"]["output_cost_per_token"], 1e-15)

	// 非国模：美元数字（claude-opus-5-5 输入 4 USD/1M）。
	if claude, ok := entries["claude-opus-5-5"]; ok {
		require.InDelta(t, 4e-6, claude["input_cost_per_token"], 1e-15)
	}

	// 统计：写入条目里属于人民币厂商的数量。
	dataset := buildCNYOfficialDataset(mustParseCNYRegistry(t, readTestdata(t, "testdata/llm-metadata-all.json")), &CNYOfficialPricingSettings{Enabled: true}, "snapshot")
	nationalWritten := 0
	for model := range entries {
		if dataset.isNationalModel(model) {
			nationalWritten++
		}
	}
	t.Logf("override entries=%d national=%d usd=%d", len(entries), nationalWritten, len(entries)-nationalWritten)
	require.Greater(t, nationalWritten, 50, "国模条目应大量写入")
	require.Greater(t, len(entries)-nationalWritten, 50, "非国模条目应保持美元口径")
}
