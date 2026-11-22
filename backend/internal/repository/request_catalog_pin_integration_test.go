//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type pinFixtureRemote struct{ price int }

func (f *pinFixtureRemote) FetchPricingJSON(context.Context, string) ([]byte, error) {
	return []byte(fmt.Sprintf(`{"pin-sku":{"input_cost_per_token":%d,"output_cost_per_token":1}}`, f.price)), nil
}
func (*pinFixtureRemote) FetchHashText(context.Context, string) (string, error) {
	panic("unexpected network/hash fetch")
}

func TestRequestCatalogPinPostgresLedger(t *testing.T) {
	ctx := context.Background()
	c := testEntClient(t)
	id := uuid.NewString()
	u := mustCreateUser(t, c, &service.User{Email: id + "@example.invalid", PasswordHash: "test", Balance: 100})
	k := mustCreateApiKey(t, c, &service.APIKey{UserID: u.ID, Key: "pin-" + id, Name: "pin", Quota: 100})
	a := mustCreateAccount(t, c, &service.Account{Name: "pin-" + id, Type: service.AccountTypeAPIKey})
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.RemoteURL = "https://fixture.invalid/prices"
	remote := &pinFixtureRemote{price: 2}
	p := service.NewPricingService(cfg, remote)
	bs := service.NewBillingService(cfg, p)
	gw := service.NewOpenAIGatewayService(nil, NewUsageLogRepository(c, integrationDB), NewUsageBillingRepository(c, integrationDB), nil, nil, nil, nil, cfg, nil, nil, bs, nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	load := func(v int) context.Context {
		t.Helper()
		remote.price = v
		require.NoError(t, p.ForceUpdate())
		return gw.WithRequestCatalog(ctx)
	}
	old, manual, restored := load(2), load(7), load(3)
	settle := func(pin context.Context, name string, ws bool) {
		t.Helper()
		in := &service.OpenAIRecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: &service.APIKeyService{}, Result: &service.OpenAIForwardResult{RequestID: id + name, Model: "pin-sku", Stream: true, OpenAIWSMode: ws, Usage: service.OpenAIUsage{InputTokens: 3}}}
		require.NoError(t, gw.RecordUsage(service.CopyRequestPricing(pin, ctx), in))
	}
	settle(restored, "-new", false)
	settle(old, "-old", true)
	settle(manual, "-manual", false)
	settle(old, "-old", true)
	var balance, quota float64
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT u.balance,k.quota_used FROM users u JOIN api_keys k ON k.user_id=u.id WHERE k.id=$1`, k.ID).Scan(&balance, &quota))
	require.Equal(t, 64.0, balance)
	require.Equal(t, 36.0, quota)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1`, k.ID).Scan(&n))
	require.Equal(t, 3, n)
	require.Eventually(t, func() bool {
		return integrationDB.QueryRow(`SELECT count(*) FROM usage_logs WHERE api_key_id=$1`, k.ID).Scan(&n) == nil && n == 3
	}, 5*time.Second, 10*time.Millisecond)
	for suffix, want := range map[string]float64{"-new": 9, "-old": 6, "-manual": 21} {
		var cost float64
		require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, id+suffix, k.ID).Scan(&cost))
		require.Equal(t, want, cost)
	}
}
