//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPartialCardQuarantinePostgres(t *testing.T) {
	const sku = "qa-partial-card"
	const rows = `{"qa-partial-card":{"input_cost_per_token":2,"output_cost_per_token":3,"glory_price_legacy":{"status":"quarantined","reason":"fixture: legacy USD estimate"}}}`
	for _, protocol := range []string{"gateway", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			id := uuid.NewString()
			u := mustCreateUser(t, client, &service.User{Email: id + "@example.invalid", PasswordHash: "fixture", Balance: 100})
			g := mustCreateGroup(t, client, &service.Group{Name: id, Platform: service.PlatformOpenAI, RateMultiplier: 1})
			inputPrice := 7.
			g.ModelPricing = []service.ChannelModelPricing{{Models: []string{sku}, Platform: g.Platform, InputPrice: &inputPrice}}
			k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, GroupID: &g.ID, Key: "fixture-" + id, Name: "partial-card", Quota: 100})
			k.Group = g
			a := mustCreateAccount(t, client, &service.Account{Name: "fixture-" + id, Type: service.AccountTypeAPIKey, Platform: g.Platform})
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.DataDir = t.TempDir()
			cfg.Pricing.RemoteURL = "https://fixture.invalid/prices"
			ps := service.NewPricingService(cfg, &runtimeQATariffRemote{body: rows})
			require.NoError(t, ps.ForceUpdate())
			bill := service.NewBillingService(cfg, ps)
			resolver := service.NewModelPricingResolver(nil, bill)
			logs := NewUsageLogRepository(client, integrationDB)
			ledger := NewUsageBillingRepository(client, integrationDB)
			gw := service.NewGatewayService(nil, nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, resolver, nil, nil, nil)
			oa := service.NewOpenAIGatewayService(nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, &service.DeferredService{}, nil, nil, resolver, nil, nil, nil, nil)
			keyUpdater := &service.APIKeyService{}
			do := func(reqID string, output, searches int) error {
				if protocol == "gateway" {
					return gw.RecordUsage(gw.WithRequestCatalog(ctx), &service.RecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: keyUpdater, Result: &service.ForwardResult{RequestID: reqID, Model: sku, SearchCount: searches, Usage: service.ClaudeUsage{InputTokens: 2, OutputTokens: output}}})
				}
				return oa.RecordUsage(oa.WithRequestCatalog(ctx), &service.OpenAIRecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: keyUpdater, Result: &service.OpenAIForwardResult{RequestID: reqID, Model: sku, SearchCount: searches, Usage: service.OpenAIUsage{InputTokens: 2, OutputTokens: output}}})
			}
			check := func(wantBalance, wantQuota float64, wantRows int) {
				t.Helper()
				var balance, quota float64
				var billed, usage int
				require.NoError(t, integrationDB.QueryRow(`SELECT u.balance,k.quota_used,(SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=k.id),(SELECT count(*) FROM usage_logs WHERE api_key_id=k.id) FROM users u JOIN api_keys k ON u.id=k.user_id WHERE k.id=$1`, k.ID).Scan(&balance, &quota, &billed, &usage))
				require.Equal(t, wantBalance, balance)
				require.Equal(t, wantQuota, quota)
				require.Equal(t, wantRows, billed)
				require.Equal(t, wantRows, usage)
			}
			var issue *service.TariffPriceIssue
			blockedID := id + "-blocked"
			require.ErrorAs(t, do(blockedID, 1, 1), &issue)
			require.Equal(t, sku, issue.Model)
			check(100, 0, 0)
			var reason, raw string
			var resolved bool
			require.NoError(t, integrationDB.QueryRow(`SELECT error_message,error_body,resolved FROM ops_error_logs WHERE api_key_id=$1 AND request_id=$2 AND error_type='tariff_unsettled'`, k.ID, blockedID).Scan(&reason, &raw, &resolved))
			require.False(t, resolved)
			require.Contains(t, reason, "output_cost_per_token")
			var evidence struct {
				Tokens      service.UsageTokens `json:"tokens"`
				SearchCount int                 `json:"search_count"`
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &evidence))
			require.Equal(t, service.UsageTokens{InputTokens: 2, OutputTokens: 1}, evidence.Tokens)
			require.Equal(t, 1, evidence.SearchCount)
			positiveID := id + "-paid"
			for i := 0; i < 2; i++ {
				require.NoError(t, do(positiveID, 0, 0))
				check(86, 14, 1)
			}
			var cost float64
			require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE api_key_id=$1 AND request_id=$2`, k.ID, positiveID).Scan(&cost))
			require.Equal(t, 14., cost)
			t.Log("blocked: balance=100 quota=0 dedup=0 usage=0; paid twice: balance=86 quota=14 dedup=1 usage=1 cost=14")
		})
	}
}
