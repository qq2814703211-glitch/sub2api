//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type runtimeQATariffRemote struct{ body string }

func (r *runtimeQATariffRemote) FetchPricingJSON(context.Context, string) ([]byte, error) {
	return []byte(r.body), nil
}
func (*runtimeQATariffRemote) FetchHashText(context.Context, string) (string, error) {
	panic("unexpected remote fetch")
}

// An unresolved event has durable usage evidence but owns neither monetary
// dedup nor a settled usage row. Replay accounting only, from the saved meters.
func TestRuntimeQATariffPostgresRecovery(t *testing.T) {
	const sku = "qatariff"
	const broken = `{"qatariff":{"glory_billing_mode":"token","glory_price_source":{"provider":"vendor","currency":"USD"},"input_cost_per_token":2,"output_cost_per_token":3},"sibling":{"input_cost_per_token":13,"output_cost_per_token":17}}`
	for _, protocol := range []string{"gateway", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			id := uuid.NewString()
			u := mustCreateUser(t, client, &service.User{Email: id + "@example.invalid", PasswordHash: "fixture", Balance: 100})
			k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: "fixture-" + id, Name: "runtimeqa", Quota: 100})
			a := mustCreateAccount(t, client, &service.Account{Name: "fixture-" + id, Type: service.AccountTypeAPIKey})
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.DataDir = t.TempDir()
			cfg.Pricing.RemoteURL = "https://fixture.invalid/prices"
			remote := &runtimeQATariffRemote{body: broken}
			ps := service.NewPricingService(cfg, remote)
			require.NoError(t, ps.ForceUpdate())
			bill := service.NewBillingService(cfg, ps)
			logs := NewUsageLogRepository(client, integrationDB)
			ledger := NewUsageBillingRepository(client, integrationDB)
			gw := service.NewGatewayService(nil, nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			oa := service.NewOpenAIGatewayService(nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			at := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
			keyUpdater := &service.APIKeyService{}
			do := func(ctx context.Context, reqID string, clock time.Time, m service.UsageTokens) error {
				if protocol == "gateway" {
					return gw.RecordUsage(ctx, &service.RecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: keyUpdater, PricingAt: clock, Result: &service.ForwardResult{RequestID: reqID, Model: sku, Usage: service.ClaudeUsage{InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CacheCreationInputTokens: m.CacheCreationTokens, CacheCreation5mTokens: m.CacheCreation5mTokens}}})
				}
				return oa.RecordUsage(ctx, &service.OpenAIRecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: keyUpdater, PricingAt: clock, Result: &service.OpenAIForwardResult{RequestID: reqID, Model: "sibling", BillingModel: sku, Usage: service.OpenAIUsage{InputTokens: m.InputTokens + m.CacheCreationTokens, OutputTokens: m.OutputTokens, CacheCreationInputTokens: m.CacheCreationTokens, CacheCreation12hInputTokens: m.CacheCreation12hTokens}}})
			}
			tokens := service.UsageTokens{InputTokens: 2, OutputTokens: 1, CacheCreationTokens: 3}
			if protocol == "gateway" {
				tokens.CacheCreation5mTokens = 3
			} else {
				tokens.CacheCreation12hTokens = 3
			}
			var issue *service.TariffPriceIssue
			require.ErrorAs(t, do(ctx, id, at, tokens), &issue)
			require.Equal(t, sku, issue.Model)
			check := func(bal, quota float64, n int) {
				t.Helper()
				var balance, used float64
				var dedup, usage int
				require.NoError(t, integrationDB.QueryRow(`SELECT u.balance,k.quota_used,(SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=k.id),(SELECT count(*) FROM usage_logs WHERE api_key_id=k.id) FROM users u JOIN api_keys k ON u.id=k.user_id WHERE k.id=$1`, k.ID).Scan(&balance, &used, &dedup, &usage))
				require.Equal(t, bal, balance)
				require.Equal(t, quota, used)
				require.Equal(t, n, dedup)
				require.Equal(t, n, usage)
			}
			check(100, 0, 0)
			var raw, reason string
			var owner, account int64
			var resolved bool
			require.NoError(t, integrationDB.QueryRow(`SELECT user_id, account_id, resolved, error_message, error_body FROM ops_error_logs WHERE api_key_id=$1 AND request_id=$2 AND error_type='tariff_unsettled'`, k.ID, id).Scan(&owner, &account, &resolved, &reason, &raw))
			require.False(t, resolved)
			require.Equal(t, u.ID, owner)
			require.Equal(t, a.ID, account)
			require.Contains(t, reason, "unquoted component")
			require.NotContains(t, raw, k.Key)
			var saved struct {
				RequestID    string              `json:"request_id"`
				BillingModel string              `json:"billing_model"`
				PricingAt    time.Time           `json:"pricing_at"`
				Tokens       service.UsageTokens `json:"tokens"`
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &saved))
			require.Equal(t, id, saved.RequestID)
			require.Equal(t, sku, saved.BillingModel)
			require.Equal(t, tokens, saved.Tokens)
			require.True(t, at.Equal(saved.PricingAt))
			remote.body = strings.Replace(broken, `"output_cost_per_token":3}`, `"output_cost_per_token":3,"cache_creation_input_token_cost":5,"cache_creation_input_token_cost_12h":11}`, 1)
			require.NoError(t, ps.ForceUpdate())
			want := 22.
			if protocol == "openai" {
				want = 40
			}
			for i := 0; i < 2; i++ {
				require.NoError(t, do(ctx, saved.RequestID, saved.PricingAt, saved.Tokens))
				check(100-want, want, 1)
			}
			var actual float64
			var written int
			require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost,cache_creation_tokens FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, id, k.ID).Scan(&actual, &written))
			require.Equal(t, want, actual)
			require.Equal(t, 3, written)
			t.Logf("held: balance=100 quota=0 dedup=0 usage=0; accounting replay twice: balance=%g quota=%g dedup=1 usage=1 stored_cost=%g", 100-want, want, actual)
		})
	}
}
