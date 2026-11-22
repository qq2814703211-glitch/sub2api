//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Load the publication-file shape, forward local provider HTTP responses, and
// check the single durable billing command, not just the resolver's estimate.
func TestPriceSyncTariffForwardAndDebitSecondBand(t *testing.T) {
	gpt26RestoreGinMode(t)
	const model = "gpt-6-luna"
	var rows map[string]map[string]any
	require.NoError(t, json.Unmarshal([]byte(tariffLadderFixture), &rows))
	row := rows["tariff-test"]
	row["glory_price_source"] = map[string]string{"currency": "USD", "provider": "openai", "evidence": "test-sentinel"}
	row["glory_billing_mode"] = "token"
	row["cache_creation_input_token_cost_30m"], row["cache_creation_input_token_cost_12h"] = 7., 11.
	for i, raw := range row["glory_price_tiers"].([]any) {
		rates := raw.(map[string]any)["rates"].(map[string]any)
		rates["cache_creation_input_token_cost_30m"] = []float64{9, 0}[i]
		rates["cache_creation_input_token_cost_12h"] = 17.
	}
	data, err := json.Marshal(map[string]any{model: row})
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "override.json")
	catalog := NewPricingService(cfg, nil)
	require.NoError(t, os.WriteFile(catalog.getPricingFilePath(), []byte(`{"gpt-6-luna":{"input_cost_per_token":99,"output_cost_per_token":88}}`), 0600))
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, data, 0600))
	require.NoError(t, catalog.reloadCustomPricingLayers())
	for _, tc := range []struct {
		n                                      int
		in, out, read, write, total, debit, up float64
	}{{20, 30, 28, 10, 35, 103, 51.5, 180.25}, {21, 48, 0, 26, 17, 91, 45.5, 159.25}} {
		t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
			ctx := context.Background()
			payload := fmt.Sprintf(`{"id":"resp_tariff_%d","object":"response","status":"completed","model":%q,"service_tier":"default","output":[],"usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":3,"cache_write_tokens_details":{"ttl_30m_tokens":2,"ttl_12h_tokens":1}},"output_tokens":4}}`, tc.n, model, tc.n)
			forward := newOpenAIImagesTestService(&httpUpstreamRecorder{resp: gpt26Response(payload, "application/json")})
			body := []byte(`{"model":"gpt-6-luna","input":"test","stream":false,"prompt_cache_options":{"ttl":"12h"}}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			c.Request.URL.Path = "/v1/responses"
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := &Account{ID: 35, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: tariffTestPtr(1.75), Credentials: map[string]any{"api_key": "test", "base_url": "https://api.openai.com/v1"}, Extra: map[string]any{"use_responses_api": true, "quota_limit": 1000.}}
			result, err := forward.Forward(ctx, c, account, body)
			require.NoError(t, err)
			usage := &openAIRecordUsageLogRepoStub{inserted: true}
			ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			gw := newOpenAIRecordUsageServiceWithBillingRepoForTest(usage, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			gw.billingService = NewBillingService(cfg, catalog)
			key := openAIRecordUsageAPIKeyWithGroup(gw, 1, true)
			key.GroupID = &key.Group.ID
			key.Group.Platform = PlatformOpenAI
			key.Group.RateMultiplier = .5
			key.Quota = 1000
			require.NoError(t, gw.RecordUsage(ctx, &OpenAIRecordUsageInput{APIKey: key, Account: account, User: &User{ID: 1}, Result: result, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}}))
			log := usage.lastLog
			require.NotNil(t, log)
			require.Equal(t, tc.n-5, log.InputTokens)
			require.Equal(t, 2, log.CacheCreation30mTokens)
			require.Equal(t, 1, log.CacheCreation12hTokens)
			require.Equal(t, tc.in, log.InputCost)
			require.Equal(t, tc.out, log.OutputCost)
			require.Equal(t, tc.read, log.CacheReadCost)
			require.Equal(t, tc.write, log.CacheCreationCost)
			require.Equal(t, tc.total, log.TotalCost)
			require.Equal(t, tc.debit, log.ActualCost)
			require.Equal(t, 1, ledger.calls)
			require.Equal(t, tc.debit, ledger.lastCmd.BalanceCost)
			require.Equal(t, tc.debit, ledger.lastCmd.APIKeyQuotaCost)
			require.Equal(t, tc.up, ledger.lastCmd.AccountQuotaCost)
		})
	}
}
