//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceSyncV2CollectRealVendorWiring(t *testing.T) {
	dir := os.Getenv("PRICE_SYNC_V2_VENDOR_FIXTURES")
	if dir == "" {
		t.Skip("requires real vendors implementation and fixture directory")
	}
	bodies := priceSyncV2TestSnapshots(t)
	for endpoint, name := range map[string]string{
		"https://platform.kimi.com/docs/pricing/chat":                        "kimi.html",
		"https://api-docs.deepseek.com/zh-cn/quick_start/pricing.md":         "deepseek.md",
		"https://help.aliyun.com/zh/model-studio/model-pricing.md?mode=pure": "aliyun.mdx",
		"https://developers.openai.com/api/docs/pricing.md":                  "openai-pricing.md",
		"https://developers.openai.com/api/docs/models/gpt-6.2-sol.md":       "sol.md",
		"https://developers.openai.com/api/docs/models/gpt-6.2-astra.md":     "astra.md",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		bodies[endpoint] = body
	}
	got, err := collectPriceSyncV2(context.Background(), priceSyncV2TestFetcher{body: bodies}, "", &CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	for _, k := range []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-1017", "gemini-4-hydrogen-xhigh"} {
		require.NotContains(t, got.Quotes, k)
	}
	q := got.Quotes["kimi-k3.1-pro-highspeed"]
	require.Equal(t, 136e-6, q.Rates["output_cost_per_token"], got.Problems[q.Model])
	require.Equal(t, 52e-6, q.Rates["cache_creation_input_token_cost_above_1hr"])
	q = got.Quotes["deepseek-flash"]
	require.Equal(t, 4e-6, q.Rates["output_cost_per_token"], got.Problems[q.Model])
	require.Len(t, q.Windows, 2)
	require.True(t, q.Windows[0].ExcludeCNHolidays)
	require.Equal(t, 8e-6, q.Windows[0].Rates["output_cost_per_token"])
	q = got.Quotes["gpt-6.2-sol"]
	require.Equal(t, 2.5e-6, q.Rates["cache_creation_input_token_cost_30m"], got.Problems[q.Model])
	require.Equal(t, 5e-6, q.Rates["cache_creation_input_token_cost_12h"])
	require.NotContains(t, q.Rates, "cache_creation_input_token_cost_above_1hr")
	require.Len(t, q.Tiers, 1)
	require.Equal(t, 272000, q.Tiers[0].Threshold)
	require.Equal(t, 10e-6, q.Tiers[0].Rates["cache_creation_input_token_cost_12h"])
	for k, mode := range map[string]string{"qwen3-asr-flash": "per_second", "wan2.6-t2i": "per_image", "facechain-finetune": "per_request"} {
		require.Equal(t, mode, got.Quotes[k].BillingMode, got.Problems[k])
	}
	t.Logf("combined quotes=%d native=%d issues=%d source_errors=%d", len(got.Quotes), len(got.NativeModels), len(got.Problems), len(got.SourceErrors))
}
