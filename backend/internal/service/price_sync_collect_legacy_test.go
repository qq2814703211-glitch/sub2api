//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceSyncLegacyCollectorDoesNotPromoteEndpointCurrency(t *testing.T) {
	registry := mustParseCNYRegistry(t, `{
		"moonshotai-cn":{"currency":"CNY","models":{
			"kimi-k3.1-pro-highspeed":{"id":"kimi-k3.1-pro-highspeed","cost":{"input":3.8874,"output":20.3342}},
			"kimi-k3":{"id":"kimi-k3","cost":{"currency":"CNY","input":20,"output":100}}
		}}
	}`)
	dataset := buildCNYOfficialDataset(registry, &CNYOfficialPricingSettings{Enabled: true}, cnyOfficialDefaultSourceURL)
	require.True(t, dataset.isNationalModel("kimi-k3.1-pro-highspeed"))
	require.NotContains(t, dataset.Pricing, "kimi-k3.1-pro-highspeed", "provider currency cannot prove model tariff currency")
	require.Contains(t, dataset.Pricing, "kimi-k3")
}
