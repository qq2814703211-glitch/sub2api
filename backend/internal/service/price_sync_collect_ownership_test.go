package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceCollectCoverageReportsLegacyExcludedSourceRows(t *testing.T) {
	bodies := priceCoverageOfficialBodies(t)
	bodies[cnyOfficialDefaultSourceURL] = []byte(`{
 "tencent-token-plan":{"models":{"glm-5-3":{"id":"glm-5-3","cost":{"input":3,"output":8}}}},
 "alibaba-cn":{"models":{"siliconflow/deepseek-v3.2":{"id":"siliconflow/deepseek-v3.2","cost":{"input":2,"output":8}},"qwen-test":{"id":"qwen-test","cost":{"currency":"CNY","input":2,"output":6}}}},
 "a-reseller":{"models":{"gpt-4.1":{"id":"gpt-4.1","cost":{"input":1,"output":1}}}}
 }`)
	got, err := collectPriceSyncV2(context.Background(), priceCoverageFetcher(bodies), "", &CNYOfficialPricingSettings{Enabled: true})
	require.NoError(t, err)
	for _, model := range []string{"glm-5-3", "siliconflow/deepseek-v3.2"} {
		require.NotContains(t, got.Quotes, model)
		require.Contains(t, got.Problems[model], "legacy CNY source excluded")
	}
	require.Empty(t, got.Problems["qwen-test"])
	require.Empty(t, got.Problems["gpt-4.1"])
	require.Equal(t, "USD", got.Quotes["gpt-4.1"].Currency)
}
