package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPriceSyncTariffBadPublishedOverrideCannotDropActiveSKU(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"malformed-base", `"input_cost_per_token":1,`, `"input_cost_per_token":"oops",`},
		{"missing-base", `"input_cost_per_token":1,"output_cost_per_token":2,`, ""},
		{"unknown-bound", `"inclusive":true`, `"inclusive":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Pricing.DataDir = t.TempDir()
			cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "override.json")
			p := NewPricingService(cfg, nil)
			require.NoError(t, os.WriteFile(p.getPricingFilePath(), []byte(`{"unrelated":{"input_cost_per_token":1}}`), 0600))
			require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(tariffLadderFixture), 0600))
			require.NoError(t, p.reloadCustomPricingLayers())
			b := NewBillingService(cfg, p)
			verify := func() {
				c, err := b.CalculateCost("tariff-test", UsageTokens{InputTokens: 21}, 1)
				require.NoError(t, err)
				require.Equal(t, 63., c.TotalCost)
			}
			verify()
			bad := strings.Replace(tariffLadderFixture, tc.from, tc.to, 1)
			require.NotEqual(t, tariffLadderFixture, bad)
			require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(bad), 0600))
			var issue *TariffPriceIssue
			require.ErrorAs(t, p.reloadCustomPricingLayers(), &issue)
			require.Equal(t, "tariff-test", issue.Model)
			verify()
		})
	}
}

func TestPriceSyncTariffNullWindowControlsCannotInventAPeak(t *testing.T) {
	for _, tc := range []struct{ from, to string }{{`"exclude_cn_holidays":true`, `"exclude_cn_holidays":null`}, {`"weekdays":[1,2,3,4,5]`, `"weekdays":null`}, {`"weekdays":[1,2,3,4,5]`, `"weekdays":[]`}} {
		row := strings.Replace(tariffWindowFixture, tc.from, tc.to, 1)
		require.NotEqual(t, tariffWindowFixture, row)
		_, err := (&PricingService{}).parsePricingData([]byte(row))
		var issue *TariffPriceIssue
		require.ErrorAs(t, err, &issue)
	}
}

func TestPriceSyncTariffMalformedFallbackKeepsActiveRule(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.FallbackFile = filepath.Join(cfg.Pricing.DataDir, "fallback.json")
	p := NewPricingService(cfg, nil)
	require.NoError(t, os.WriteFile(p.getPricingFilePath(), []byte(`{"other":{"input_cost_per_token":3}}`), 0600))
	require.NoError(t, os.WriteFile(cfg.Pricing.FallbackFile, []byte(tariffLadderFixture), 0600))
	require.NoError(t, p.reloadCustomPricingLayers())
	bad := strings.Replace(tariffLadderFixture, `"threshold":20`, `"threshold":-1`, 1)
	require.NoError(t, os.WriteFile(cfg.Pricing.FallbackFile, []byte(bad), 0600))
	var issue *TariffPriceIssue
	require.ErrorAs(t, p.reloadCustomPricingLayers(), &issue)
	b := NewBillingService(cfg, p)
	c, err := b.CalculateCost("tariff-test", UsageTokens{InputTokens: 21}, 1)
	require.NoError(t, err)
	require.Equal(t, 63., c.TotalCost)
}
