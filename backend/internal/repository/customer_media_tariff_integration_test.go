//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The ledger commits on its own connections, so a rollback around the test
// cannot isolate its groups, outbox events and charge records from other suites.
func newCustomerMediaTariffDB(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	name := "media_tariff_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pq.QuoteIdentifier(name)
	dsn := integrationDSN
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		var err error
		dsn, err = pq.ParseURL(dsn)
		require.NoError(t, err)
	}
	_, err := integrationDB.Exec("CREATE DATABASE " + quoted + " TEMPLATE template0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DROP DATABASE " + quoted)
		require.NoError(t, err)
	})
	db, err := sql.Open("postgres", dsn+" dbname="+name)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var actual string
	require.NoError(t, db.QueryRow("SELECT current_database()").Scan(&actual))
	require.Equal(t, name, actual)
	require.NoError(t, ApplyMigrations(context.Background(), db))
	return db, dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
}

// Use separate selling and reporting cards on the same successful outputs. A
// missing selling dimension holds the event; a missing reporting dimension does not.
func TestCustomerMediaTariffPostgres(t *testing.T) {
	db, client := newCustomerMediaTariffDB(t)
	no := false
	audio := service.MediaPricingConditions{Audio: &no}
	layer := service.MediaPricingConditions{Layer: &no}
	for _, tc := range []struct {
		name                              string
		sale, stats                       service.MediaPricingConditions
		reportedAudio                     *bool
		price, debit                      float64
		hold, statsIssue, failed, generic bool
	}{
		{name: "missing-audio-and-accounting-replay", sale: audio, stats: layer, price: .29, debit: .87, hold: true},
		{name: "explicit-false", sale: audio, stats: layer, reportedAudio: &no, price: .29, debit: .87},
		{name: "explicit-free", sale: audio, stats: layer, reportedAudio: &no, price: 0, debit: 0},
		{name: "stats-only-missing-audio", sale: layer, stats: audio, price: .29, debit: .87, statsIssue: true},
		{name: "generic-stats-only-missing-media", stats: audio, debit: .86, statsIssue: true, generic: true},
		{name: "failed-attempt-not-unsettled-usage", sale: audio, stats: layer, price: .29, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			id := uuid.NewString()
			const sku = "qa-media-card"
			platform := service.PlatformOpenAI
			if tc.generic {
				platform = service.PlatformAnthropic
			}
			card := func(name string, p float64, dims service.MediaPricingConditions) service.ChannelModelPricing {
				return service.ChannelModelPricing{Platform: platform, Models: []string{sku}, BillingMode: service.BillingModeImage,
					MediaPricing: &service.MediaPricing{Rules: []service.MediaPricingRule{{Name: name, Unit: "image", Price: &p, Conditions: dims}}}}
			}
			u := mustCreateUser(t, client, &service.User{Email: id + "@example.invalid", PasswordHash: "disabled", Balance: 100})
			groups := NewGroupRepository(client, db)
			sale := card("sale", tc.price, tc.sale)
			if tc.generic {
				inPrice, outPrice := .02, .03
				sale = service.ChannelModelPricing{Platform: platform, Models: []string{sku}, BillingMode: service.BillingModeToken, InputPrice: &inPrice, OutputPrice: &outPrice}
			}
			g := &service.Group{Name: id, Platform: platform, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard,
				RateMultiplier: 2, ImageRateIndependent: true, ImageRateMultiplier: 1.5, ModelPricing: []service.ChannelModelPricing{sale}}
			require.NoError(t, groups.Create(ctx, g))
			k := mustCreateApiKey(t, client, &service.APIKey{Name: "test", Key: "fixture-" + id, UserID: u.ID, GroupID: &g.ID, Quota: 100})
			var err error
			k.Group, err = groups.GetByIDLite(ctx, g.ID)
			require.NoError(t, err)
			a := mustCreateAccount(t, client, &service.Account{Name: id, Type: service.AccountTypeAPIKey, Platform: g.Platform})
			channels := NewChannelRepository(db)
			ch := &service.Channel{Name: id, Status: service.StatusActive, BillingModelSource: service.BillingModelSourceRequested, GroupIDs: []int64{g.ID},
				AccountStatsPricingRules: []service.AccountStatsPricingRule{{Name: "report", AccountIDs: []int64{a.ID}, GroupIDs: []int64{}, Pricing: []service.ChannelModelPricing{card("report", .11, tc.stats)}}}}
			require.NoError(t, channels.Create(ctx, ch))
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.DataDir = t.TempDir()
			cfg.Pricing.RemoteURL = "https://fixture.invalid/prices"
			ps := service.NewPricingService(cfg, &runtimeQATariffRemote{body: `{"qa-media-card":{"glory_billing_mode":"per_image","output_cost_per_image":0.97}}`})
			require.NoError(t, ps.ForceUpdate())
			bill := service.NewBillingService(cfg, ps)
			cs := service.NewChannelService(channels, groups, nil, ps, nil)
			logs := NewUsageLogRepository(client, db)
			ledger := NewUsageBillingRepository(client, db)
			resolver := service.NewModelPricingResolver(cs, bill)
			oa := service.NewOpenAIGatewayService(nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, &service.DeferredService{}, nil, nil, resolver, cs, nil, nil, nil)
			zero := 0
			media := service.MediaUsage{Count: 2, Audio: tc.reportedAudio, Layer: &no, InputImageCount: &zero, Images: []service.MediaUsage{{Count: 1, Audio: &no, Width: 1024, Height: 1024}, {Count: 1, Width: 1536, Height: 1024}}}
			meter := service.OpenAIUsage{InputTokens: 11, OutputTokens: 7}
			at := time.Date(2026, 1, 8, 3, 0, 0, 0, time.UTC)
			do := func(c context.Context, request string, clock time.Time, m service.MediaUsage, tokens service.OpenAIUsage) error {
				if tc.generic {
					gw := service.NewGatewayService(nil, nil, logs, ledger, nil, nil, nil, nil, cfg, nil, nil, bill, nil, nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, cs, resolver, nil, nil, nil)
					return gw.RecordUsage(c, &service.RecordUsageInput{APIKey: k, User: u, Account: a, APIKeyService: &service.APIKeyService{}, PricingAt: clock,
						Result: &service.ForwardResult{RequestID: request, Model: sku, Usage: service.ClaudeUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens}}})
				}
				return oa.RecordUsage(c, &service.OpenAIRecordUsageInput{APIKey: k, User: u, Account: a, APIKeyService: &service.APIKeyService{}, PricingAt: clock,
					RequestPayloadHash: "fixture-fingerprint", ChannelUsageFields: service.ChannelUsageFields{ChannelID: ch.ID, OriginalModel: sku},
					Result: &service.OpenAIForwardResult{RequestID: request, Model: sku, UpstreamModel: sku, ImageEndpoint: true, ImageCount: m.Count,
						MediaUsage: &m, Usage: tokens, UnitRequestFailed: tc.failed, DeferredHTTPBody: []byte("private-response-marker")}})
			}
			check := func(phase string, debit float64, rows, held, stats int) {
				t.Helper()
				var bal, used float64
				var n, d, h, s int
				err := db.QueryRow(`SELECT u.balance,k.quota_used,
 (SELECT count(*) FROM usage_logs WHERE api_key_id=k.id AND request_id=$2),
 (SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=k.id AND request_id=$2),
 (SELECT count(*) FROM ops_error_logs WHERE api_key_id=k.id AND request_id=$2 AND error_type='tariff_unsettled' AND NOT resolved),
 (SELECT count(*) FROM ops_error_logs WHERE api_key_id=k.id AND request_id=$2 AND error_type='account_stats_unsettled' AND NOT resolved)
 FROM users u JOIN api_keys k ON k.user_id=u.id WHERE k.id=$1`, k.ID, id).Scan(&bal, &used, &n, &d, &h, &s)
				require.NoError(t, err)
				t.Logf("%s request=%s wallet=%.8f key=%.8f usage=%d dedup=%d tariff=%d stats=%d", phase, id, bal, used, n, d, h, s)
				require.InDelta(t, 100-debit, bal, 1e-9)
				require.InDelta(t, debit, used, 1e-9)
				require.Equal(t, []int{rows, rows, held, stats}, []int{n, d, h, s})
			}
			if tc.failed {
				require.ErrorIs(t, do(ctx, id, at, media, meter), service.ErrMediaUsageIncomplete)
				check("failed", 0, 0, 0, 0)
				return
			}
			h, s := 0, 0
			if tc.hold {
				start := time.Now()
				failure := do(oa.WithRequestCatalog(ctx), id, at, media, meter)
				end := time.Now()
				require.ErrorIs(t, failure, service.ErrMediaUsageIncomplete)
				check("held", 0, 0, 1, 0)
				h = 1
				var raw, reason string
				var owners [4]int64
				require.NoError(t, db.QueryRow(`SELECT error_body,error_message,user_id,api_key_id,account_id,group_id FROM ops_error_logs WHERE request_id=$1 AND api_key_id=$2 AND error_type='tariff_unsettled'`, id, k.ID).Scan(&raw, &reason, &owners[0], &owners[1], &owners[2], &owners[3]))
				require.Equal(t, [4]int64{u.ID, k.ID, a.ID, g.ID}, owners)
				require.Contains(t, reason, "audio")
				require.Contains(t, reason, "sale")
				require.NotContains(t, raw, k.Key)
				require.NotContains(t, raw, "private-response-marker")
				var saved struct {
					Request string              `json:"request_id"`
					Model   string              `json:"billing_model"`
					At      time.Time           `json:"pricing_at"`
					Rate    float64             `json:"rate_multiplier"`
					Tokens  service.UsageTokens `json:"tokens"`
					Media   *service.MediaUsage `json:"media"`
					Images  int                 `json:"image_count"`
					Channel int64               `json:"channel_id"`
					Trace   struct {
						Basis struct {
							Scope string `json:"scope"`
						} `json:"basis"`
					} `json:"price_trace"`
				}
				require.NoError(t, json.Unmarshal([]byte(raw), &saved))
				require.Equal(t, id, saved.Request)
				require.Equal(t, sku, saved.Model)
				require.Equal(t, ch.ID, saved.Channel)
				require.True(t, !saved.At.Before(start) && !saved.At.After(end))
				require.Equal(t, "group", saved.Trace.Basis.Scope)
				require.Equal(t, 1.5, saved.Rate)
				require.Equal(t, service.UsageTokens{InputTokens: 11, OutputTokens: 7}, saved.Tokens)
				require.Equal(t, 2, saved.Images)
				require.Equal(t, &media, saved.Media)
				// Accounting only: supply the missing authoritative dimension on the
				// saved event; do not re-run a supplier or change the selling card.
				media = *saved.Media
				media.Audio = &no
				at = saved.At
				id = saved.Request
				meter = service.OpenAIUsage{InputTokens: saved.Tokens.InputTokens, OutputTokens: saved.Tokens.OutputTokens}
			}
			if tc.statsIssue {
				s = 1
			}
			for _, phase := range []string{"settled", "same-key-replay"} {
				require.NoError(t, do(ctx, id, at, media, meter))
				check(phase, tc.debit, 1, h, s)
			}
			var actual, total float64
			var report sql.NullFloat64
			var images, in, out int
			require.NoError(t, db.QueryRow(`SELECT actual_cost,total_cost,account_stats_cost,image_count,input_tokens,output_tokens FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, id, k.ID).Scan(&actual, &total, &report, &images, &in, &out))
			wantImages, wantTotal := 2, tc.price*2
			if tc.generic {
				wantImages, wantTotal = 0, .43
			}
			require.Equal(t, []int{wantImages, 11, 7}, []int{images, in, out})
			require.InDelta(t, tc.debit, actual, 1e-9)
			require.InDelta(t, wantTotal, total, 1e-9)
			require.Equal(t, !tc.statsIssue, report.Valid)
			if report.Valid {
				require.InDelta(t, .22, report.Float64, 1e-9)
			}
		})
	}
}
