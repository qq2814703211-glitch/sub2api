//go:build videodb

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The videodb launcher requires a dedicated, disposable database. Exercise the
// actual recovery worker, durable intent/dedup transaction and usage log writer.
func TestCatalogUnitsVideoDB(t *testing.T) {
	for _, tc := range []struct {
		mode           string
		quantity, cost float64
	}{{"per_request", 1, 0.75}, {"per_second", 2.375, 1.78125}} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			f := newVideoDBFixture(t, "grok")
			_, in, upstream := f.service(t, `{"status":"done","video":{"url":"https://example.test/v","duration":2.375}}`)
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.UpdateIntervalHours = 24
			cfg.Pricing.DataDir = t.TempDir()
			path := filepath.Join(cfg.Pricing.DataDir, "model_pricing.json")
			catalog := func(rate float64) *service.OpenAIGatewayService {
				require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`{%q:{"glory_billing_mode":%q,"cost_per_request":%g,"cost_per_second":%g}}`, f.model, tc.mode, rate, rate)), 0600))
				ps := service.NewPricingService(cfg, nil)
				require.NoError(t, ps.Initialize())
				t.Cleanup(ps.Stop)
				bill := service.NewBillingService(cfg, ps)
				s := service.NewOpenAIGatewayService(&videoDBAccountRepo{account: in.Account}, NewUsageLogRepository(nil, f.db), NewUsageBillingRepository(nil, f.db), nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, upstream, nil, nil, nil, service.NewModelPricingResolver(nil, bill), nil, nil, service.NewSettingService(&videoDBSettings{db: f.db}, cfg), nil)
				s.SetVideoBillingJobs(NewVideoBillingJobRepository(f.db))
				return s
			}
			in.APIKey.Group.VideoModelPrices = nil
			in.APIKey.Group.RateMultiplier = 2
			svc := catalog(0.375)
			j := f.begin(t, svc, in, "global-"+tc.mode)
			in.APIKey.Group.RateMultiplier = 11
			restarted := catalog(37)
			require.NoError(t, restarted.RunVideoBillingRecovery(ctx, 4))
			require.NoError(t, svc.ObserveVideoBilling(ctx, j, &service.OpenAIForwardResult{VideoCount: 1}))
			f.assertBill(t, tc.cost)
			var units sql.NullFloat64
			var mode string
			var rowID int64
			require.NoError(t, f.db.QueryRow(`SELECT id,(to_jsonb(l)->>'billing_units')::numeric, billing_mode FROM usage_logs l WHERE api_key_id=$1`, f.key).Scan(&rowID, &units, &mode))
			row, err := NewUsageLogRepository(nil, f.db).GetByID(ctx, rowID)
			require.NoError(t, err)
			require.Equal(t, tc.quantity, *row.BillingUnits)
			require.Equal(t, tc.mode, mode)
			require.Equal(t, sql.NullFloat64{Float64: tc.quantity, Valid: true}, units)
			t.Logf("%s units=%g balance=%g quota=%g ledger=%g", mode, units.Float64, 100-tc.cost, tc.cost, tc.cost)
		})
	}
}
