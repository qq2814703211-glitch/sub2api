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

// TestCNYOfficial_RealSourceSnapshotParses 是对真实数据源的离线回归：快照由
// scripts 侧用 curl 从 basellm/llm-metadata 拉下后落在 testdata 里，测试本身不联网。
//
// 快照缺失时 Skip：CI 上不依赖这份大文件，本地核对牌价时再补。
func TestCNYOfficial_RealSourceSnapshotParses(t *testing.T) {
	path := filepath.Join("testdata", "llm-metadata-all.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("real cny-official snapshot not present")
	}
	registry := mustParseCNYRegistry(t, readTestdata(t, path))

	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, "snapshot")

	// 国模归属和可用原生人民币价分别统计。
	require.Greater(t, len(registry), 40)
	require.Greater(t, dataset.Report.ProvidersCNY, 10, "人民币厂商应多于 10 个")
	require.Greater(t, dataset.Report.UsablePrices, 0)
	require.Greater(t, dataset.Report.NationalModels, dataset.Report.UsablePrices)

	// deepseek-flash 官方人民币牌价 1 / 4 / 0.02。
	deepseek, ok := dataset.pricing("deepseek-flash")
	require.True(t, ok)
	require.InDelta(t, 1e-6, deepseek.PerToken["input_cost_per_token"], 1e-15)
	require.InDelta(t, 4e-6, deepseek.PerToken["output_cost_per_token"], 1e-15)

	// 地区 CNY 与数字量级都不能证明模型原价。
	require.True(t, dataset.isNationalModel("qwen3-max"))
	require.NotContains(t, dataset.Pricing, "qwen3-max")
	require.NotContains(t, dataset.Pricing, "kimi-k3.1-pro-highspeed")

	t.Logf("real snapshot: providers=%d cny_providers=%d entries=%d usable=%d national=%d ladders=%d conflicts=%d skipped_no_cny=%d time_pricing=%d",
		dataset.Report.Providers, dataset.Report.ProvidersCNY, dataset.Report.ModelEntries,
		dataset.Report.UsablePrices, dataset.Report.NationalModels, len(dataset.Report.Ladders),
		len(dataset.Report.Conflicts), len(dataset.Report.SkippedNoCNY), len(dataset.Report.TimePricing))
}

// TestCNYOfficial_LiveSourceFetch 实拉数据源验证抓取链路。默认跳过，
// 只在本机显式开启时跑：
//
//	CNY_OFFICIAL_LIVE=1 https_proxy=http://127.0.0.1:7897 go test -tags unit -run TestCNYOfficial_LiveSourceFetch
func TestCNYOfficial_LiveSourceFetch(t *testing.T) {
	if os.Getenv("CNY_OFFICIAL_LIVE") != "1" {
		t.Skip("set CNY_OFFICIAL_LIVE=1 to fetch the real cny-official source")
	}
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o644))
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
		&stubModelPricingSyncHistory{},
		nil,
	)
	applied, err := syncLegacyForTest(svc, context.Background())
	if err != nil {
		t.Fatalf("live sync failed: %v", err)
	}
	require.Greater(t, applied, 0)
	t.Logf("live sync applied %d changes", applied)
}
