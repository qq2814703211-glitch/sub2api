//go:build unit

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type priceSyncV2TestFetcher struct {
	body       map[string][]byte
	supplement func(native bool) PriceSyncCollection
}

func (f priceSyncV2TestFetcher) FetchPricingJSON(_ context.Context, endpoint string) ([]byte, error) {
	if body, ok := f.body[endpoint]; ok {
		return body, nil
	}
	return nil, fmt.Errorf("no fixture for %s", endpoint)
}

func (f priceSyncV2TestFetcher) testOfficialPriceSupplements(native bool) PriceSyncCollection {
	if f.supplement != nil {
		return f.supplement(native)
	}
	return PriceSyncCollection{}
}

func priceSyncV2TestSnapshots(t *testing.T) map[string][]byte {
	t.Helper()
	fixtures := map[string][]byte{
		modelPricingSyncRegistryURL: []byte(readTestdata(t, "testdata/price_sync_v2_models-dev.json")),
		cnyOfficialDefaultSourceURL: []byte(readTestdata(t, "testdata/price_sync_v2_basellm.json")),
	}
	if dir := os.Getenv("PRICE_SYNC_V2_AUDIT_SNAPSHOTS"); dir != "" {
		for endpoint, name := range map[string]string{modelPricingSyncRegistryURL: "models-dev.json", cnyOfficialDefaultSourceURL: "basellm.json"} {
			body, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			fixtures[endpoint] = body
		}
	}
	return fixtures
}

func TestPriceSyncV2CollectSnapshot(t *testing.T) {
	got, err := collectPriceSyncV2(context.Background(), priceSyncV2TestFetcher{body: priceSyncV2TestSnapshots(t)}, "", &CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	for _, model := range []string{"qwen3-max", "kimi-k3.1-pro", "kimi-k3.1-pro-highspeed", "deepseek-flash", "doubao-seed-1-6-251015", "MiniMax-H3"} {
		key := normalizeModelPricingSyncModelName(model)
		require.True(t, got.NativeModels[key], model)
		require.NotContains(t, got.Quotes, key, model)
		require.NotEmpty(t, got.Problems[key], model)
	}
	require.NotContains(t, got.Quotes, "deepseek/deepseek-flash")
	require.False(t, got.NativeModels["deepseek/deepseek-flash"])
	for _, model := range []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-1017", "dealignai/deepseek-v4.1-flash-uncensored-fp8", "gemini-4-hydrogen-xhigh"} {
		require.NotContains(t, got.Quotes, model)
	}
	for _, model := range []string{"claude-opus-4-8", "gpt-5.6-sol", "gemini-2.5-pro", "mistral-large-latest", "gemini-4-argon", "gemini-4-carbon", "gemini-4-hydrogen", "gemini-4-neon"} {
		q, ok := got.Quotes[model]
		require.True(t, ok, "missing %s: %s", model, got.Problems[model])
		require.Equal(t, "USD", q.Currency)
		require.Contains(t, q.Evidence, "models.dev sha256:")
	}
	q := got.Quotes["claude-opus-4-8"]
	require.Equal(t, "anthropic", q.Provider, "Azure is a reseller of this exact ID")
	require.Empty(t, q.Tiers, "no Azure-specific context tier in Anthropic's tariff")
	// Anthropic publishes a 1-hour cache write at 2x the base input rate while
	// models.dev only carries the 5-minute component; the collector fills that
	// published multiple so 1h cache usage stays quotable.
	require.Equal(t, 2*q.Rates["input_cost_per_token"], q.Rates["cache_creation_input_token_cost_above_1hr"])
	require.NotContains(t, got.Quotes, "gpt-6.2-sol")
	require.Contains(t, got.Problems["gpt-6.2-sol"], "no complete OpenAI API Standard official quote")
	require.Contains(t, got.Problems["gpt-6.2-sol"], "no fixture for "+officialOpenAIURL)
	require.NotContains(t, got.Quotes, "glm-5.1", "a tier omitting a separately declared cache-write rate is incomplete")
	require.Contains(t, got.Problems["glm-5.1"], "missing component cache_creation_input_token_cost")
	q = got.Quotes["glm-5.3"]
	require.Equal(t, "CNY", q.Currency)
	require.Equal(t, 8e-6, q.Rates["input_cost_per_token"])
	require.Equal(t, 2e-6, q.Rates["cache_read_input_token_cost"])
	require.Contains(t, q.Rates, "cache_creation_input_token_cost")
	require.Zero(t, q.Rates["cache_creation_input_token_cost"])
	q = got.Quotes["image-01"]
	require.Equal(t, "per_image", q.BillingMode)
	require.Equal(t, 0.025, q.Rates["output_cost_per_image"])
	t.Logf("quotes=%d native=%d issues=%d source_errors=%d", len(got.Quotes), len(got.NativeModels), len(got.Problems), len(got.SourceErrors))
}

func TestPriceSyncV2CollectExplicitDimensionsAndConflicts(t *testing.T) {
	fetcher := priceSyncV2TestFetcher{body: map[string][]byte{
		modelPricingSyncRegistryURL: []byte(`{
			"openai":{"id":"openai","models":{
				"ordinary":{"id":"ordinary","cost":{"input":2,"output":3}},
				"ambiguous":{"id":"ambiguous","cost":{"input":5,"output":8}},
				"hy4":{"id":"hy4","cost":{"input":3,"output":4}},
				"not-a-metered-offer":{"id":"not-a-metered-offer","cost":{"input":0,"output":0}},
				"qwen-never-seen-in-basellm":{"id":"qwen-never-seen-in-basellm","cost":{"input":1,"output":2}},
				"gpt-id-drift":{"id":"gpt-other","cost":{"input":1,"output":1}}
			}},
			"mistral":{"id":"mistral","models":{"ambiguous":{"id":"ambiguous","cost":{"input":6,"output":10}}}}
		}`),
		cnyOfficialDefaultSourceURL: []byte(`{
			"alibaba-cn":{"id":"alibaba-cn","currency":"CNY","models":{
				"qwen-explicit":{"id":"qwen-explicit","cost":{"currency":"CNY","unit":"per_million_tokens","input":1,"output":4,"cache_read":0,"cache_write_5m":2,"cache_write_1h":7,"cache_write_30m":3,"cache_write_12h":9,"tiers":[
					{"tier":{"type":"context","size":128000},"input":10,"output":20,"cache_read":3,"cache_write_5m":11,"cache_write_1h":13,"cache_write_30m":15,"cache_write_12h":17},
					{"tier":{"type":"context","size":32000},"input":2,"output":8,"cache_read":0,"cache_write_5m":4,"cache_write_1h":7,"cache_write_30m":6,"cache_write_12h":9}
				]}},
				"qwen-coarse":{"id":"qwen-coarse","cost":{"currency":"CNY","input":1,"output":4,"cache_read":2,"tiers":[{"tier":{"type":"context","size":32000},"input":4,"output":8}]}},
				"qwen-windows":{"id":"qwen-windows","cost":{"currency":"CNY","input":1,"output":4,"cache_read":0,"cache_write_5m":3,"schedule":{"timezone":"Asia/Shanghai","fallback":"off_peak","windows":[{"name":"peak","weekdays":[1,2,3,4,5],"hours":["09:00-12:00","14:00-18:00"],"exclude_cn_holidays":true,"input":2,"output":5,"cache_read":0,"cache_write_5m":6}]}}},
				"qwen-mixed-currency":{"id":"qwen-mixed-currency","cost":{"currency":"USD","input":1,"schedule":{"timezone":"UTC"},"currency_options":{"CNY":{"input":6,"output":7}}}},
				"qwen-cny-branch":{"id":"qwen-cny-branch","cost":{"currency_options":{"CNY":{"input":7,"output":9}},"input":1,"output":2,"cache_read":3}},
				"qwen-contradiction":{"id":"qwen-contradiction","cost":{"currency_options":{"CNY":{"currency":"USD","input":7,"output":9}}}},
				"qwen-duplicate-currency":{"id":"qwen-duplicate-currency","cost":{"currency":"USD","currency":"CNY","input":7,"output":9}},
				"qwen-unit":{"id":"qwen-unit","cost":{"currency":"CNY","unit":"per_thousand_tokens","input":1,"output":2}},
				"qwen-image":{"id":"qwen-image","cost":{"currency":"CNY","per_image":0}},
				"qwen-seconds":{"id":"qwen-seconds","cost":{"currency":"CNY","per_second":0.75}},
				"qwen-request":{"id":"qwen-request","cost":{"currency":"CNY","per_request":0.005}},
				"qwen-mix-units":{"id":"qwen-mix-units","cost":{"currency":"CNY","input":1,"output":2,"per_second":0.75}},
				"qwen-null":{"id":"qwen-null","cost":{"currency":"CNY","input":1,"output":null}},
				"qwen-unrepresentable":{"id":"qwen-unrepresentable","cost":{"currency":"CNY","input":1,"output":2,"input_audio":3}},
				"qwen-different-window-currency":{"id":"qwen-different-window-currency","cost":{"currency":"CNY","input":1,"output":2,"schedule":{"timezone":"UTC","fallback":"base","windows":[{"hours":["09:00-12:00"],"currency":"USD","input":4,"output":5}]}}},
				"siliconflow/deepseek-v3.2":{"id":"siliconflow/deepseek-v3.2","cost":{"currency":"CNY","input":2,"output":4}}
			}},
			"alibaba-token-plan-cn":{"id":"alibaba-token-plan-cn","models":{"qwen-plan":{"id":"qwen-plan","cost":{"currency":"CNY","input":0,"output":0}}}},
			"a-reseller":{"id":"a-reseller","models":{"qwen-resold":{"id":"qwen-resold","cost":{"currency":"CNY","input":1,"output":2}}}}
		}`),
	}}
	got, err := collectPriceSyncV2(context.Background(), fetcher, "", &CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	require.True(t, got.NativeModels["qwen-never-seen-in-basellm"])
	require.True(t, got.NativeModels["hy4"], "known native identity remains classified when an exact row is absent in basellm")
	require.NotContains(t, got.Quotes, "qwen-never-seen-in-basellm")
	require.Contains(t, got.Quotes, "ordinary")
	for _, model := range []string{"ambiguous", "gpt-id-drift", "hy4", "not-a-metered-offer", "qwen-coarse", "qwen-mixed-currency", "qwen-contradiction", "qwen-duplicate-currency", "qwen-unit", "qwen-mix-units", "qwen-null", "qwen-unrepresentable", "qwen-different-window-currency"} {
		require.NotContains(t, got.Quotes, model)
		require.NotEmpty(t, got.Problems[model], model)
	}
	for _, model := range []string{"qwen-plan", "qwen-resold", "siliconflow/deepseek-v3.2", "deepseek-v3.2"} {
		require.NotContains(t, got.Quotes, model)
	}
	q := got.Quotes["qwen-cny-branch"]
	require.Equal(t, 7e-6, q.Rates["input_cost_per_token"])
	require.NotContains(t, q.Rates, "cache_read_input_token_cost")
	q = got.Quotes["qwen-explicit"]
	require.Equal(t, map[string]float64{
		"input_cost_per_token": 1e-6, "output_cost_per_token": 4e-6, "cache_read_input_token_cost": 0,
		"cache_creation_input_token_cost": 2e-6, "cache_creation_input_token_cost_above_1hr": 7e-6,
		"cache_creation_input_token_cost_30m": 3e-6, "cache_creation_input_token_cost_12h": 9e-6,
	}, q.Rates)
	require.Equal(t, []PriceSyncTier{
		{Threshold: 32000, Rates: map[string]float64{
			"input_cost_per_token": 2e-6, "output_cost_per_token": 8e-6, "cache_read_input_token_cost": 0,
			"cache_creation_input_token_cost": 4e-6, "cache_creation_input_token_cost_above_1hr": 7e-6,
			"cache_creation_input_token_cost_30m": 6e-6, "cache_creation_input_token_cost_12h": 9e-6,
		}},
		{Threshold: 128000, Rates: map[string]float64{
			"input_cost_per_token": 10e-6, "output_cost_per_token": 20e-6, "cache_read_input_token_cost": 3e-6,
			"cache_creation_input_token_cost": 11e-6, "cache_creation_input_token_cost_above_1hr": 13e-6,
			"cache_creation_input_token_cost_30m": 15e-6, "cache_creation_input_token_cost_12h": 17e-6,
		}},
	}, q.Tiers)
	q = got.Quotes["qwen-windows"]
	require.Len(t, q.Windows, 2)
	require.True(t, q.Windows[0].ExcludeCNHolidays)
	require.Equal(t, "14:00", q.Windows[1].Start)
	require.Equal(t, 6e-6, q.Windows[1].Rates["cache_creation_input_token_cost"])
	for _, want := range []struct {
		model, mode, key string
		value            float64
	}{{"qwen-image", "per_image", "output_cost_per_image", 0}, {"qwen-seconds", "per_second", "cost_per_second", 0.75}, {"qwen-request", "per_request", "cost_per_request", 0.005}} {
		q := got.Quotes[want.model]
		require.Equal(t, want.mode, q.BillingMode)
		require.Equal(t, map[string]float64{want.key: want.value}, q.Rates)
	}
}

func TestPriceSyncV2CollectIndependentSources(t *testing.T) {
	for _, failed := range []string{cnyOfficialDefaultSourceURL, modelPricingSyncRegistryURL} {
		t.Run(failed, func(t *testing.T) {
			body := priceSyncV2TestSnapshots(t)
			delete(body, failed)
			got, err := collectPriceSyncV2(context.Background(), priceSyncV2TestFetcher{body: body}, "", &CNYOfficialPricingSettings{Enabled: true})
			require.NoError(t, err)
			require.NotEmpty(t, got.SourceErrors)
			if failed == cnyOfficialDefaultSourceURL {
				require.Contains(t, got.Quotes, "claude-opus-4-8")
				require.True(t, got.NativeModels["qwen3-max"])
				require.NotContains(t, got.Quotes, "kimi-k3.1-pro-highspeed")
			} else {
				require.Contains(t, got.Quotes, "glm-5.3")
			}
		})
	}
	got, err := collectPriceSyncV2(context.Background(), priceSyncV2TestFetcher{body: priceSyncV2TestSnapshots(t)}, "https://unapproved.example/api.json", nil)
	require.Error(t, err)
	require.Contains(t, got.SourceErrors["models.dev"], "not an allowed source")
}

func TestPriceSyncV2CollectOfficialSupplementOverlay(t *testing.T) {
	if os.Getenv("PRICE_SYNC_V2_VENDOR_OVERLAY") != "1" {
		t.Skip("requires isolated sources-lane vendor seam overlay")
	}
	now := time.Now().UTC().Add(-4 * time.Minute)
	quote := func(model string, in, out, w5, w1 float64) PriceSyncQuote {
		return PriceSyncQuote{Model: model, Provider: "moonshotai-cn", Currency: "CNY", BillingMode: "token", SourceURL: "https://platform.kimi.com/docs/pricing/chat", Evidence: "fixture-source-hash", VerifiedAt: now, Rates: map[string]float64{
			"input_cost_per_token": in, "output_cost_per_token": out, "cache_read_input_token_cost": 1.3e-6,
			"cache_creation_input_token_cost": w5, "cache_creation_input_token_cost_above_1hr": w1,
		}}
	}
	fetcher := priceSyncV2TestFetcher{body: priceSyncV2TestSnapshots(t), supplement: func(native bool) PriceSyncCollection {
		require.True(t, native)
		return PriceSyncCollection{
			Quotes: map[string]PriceSyncQuote{
				"kimi-k3.1-pro":           quote("kimi-k3.1-pro", 13e-6, 68e-6, 13e-6, 26e-6),
				"kimi-k3.1-pro-highspeed": quote("kimi-k3.1-pro-highspeed", 26e-6, 136e-6, 26e-6, 52e-6),
				"deepseek-v4.1-flash":     quote("deepseek-v4.1-flash", 1e-6, 4e-6, 2e-6, 4e-6),
			},
			SourceErrors: map[string]string{"other-vendor": "fixture unavailable"},
		}
	}}
	got, err := collectPriceSyncV2(context.Background(), fetcher, "", &CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	require.Equal(t, 136e-6, got.Quotes["kimi-k3.1-pro-highspeed"].Rates["output_cost_per_token"])
	require.Equal(t, 52e-6, got.Quotes["kimi-k3.1-pro-highspeed"].Rates["cache_creation_input_token_cost_above_1hr"])
	require.Equal(t, 68e-6, got.Quotes["kimi-k3.1-pro"].Rates["output_cost_per_token"])
	require.Equal(t, now, got.Quotes["kimi-k3.1-pro"].VerifiedAt, "independent source failure must not refresh evidence time")
	require.Empty(t, got.Problems["kimi-k3.1-pro"])
	require.NotContains(t, got.Quotes, "deepseek-v4.1-flash")
	require.Contains(t, got.Problems["deepseek-v4.1-flash"], "distinct")
	require.Equal(t, "fixture unavailable", got.SourceErrors["other-vendor"])
}
