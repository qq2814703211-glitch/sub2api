//go:build cachebillingdb

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type cachedChargeQuotaUpdater struct{}

func (cachedChargeQuotaUpdater) UpdateQuotaUsed(context.Context, int64, float64) error { return nil }
func (cachedChargeQuotaUpdater) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

// Run only against the lane's disposable PostgreSQL via the recorded DB runner.
// No integration TestMain/testcontainers/production credentials are involved.
func cachedChargeDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CACHE_BILLING_TEST_DSN")
	require.NotEmpty(t, dsn, "dedicated database DSN is required; never silently skip")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, ApplyMigrations(context.Background(), db))
	return db
}

func cachedChargeFixture(t *testing.T, db *sql.DB) (*service.GatewayService, *service.GeminiCachedContentService, *geminiCachedContentRepository, *service.GeminiCachedContent, *service.RecordUsageInput) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var uid, kid, gid, aid int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'synthetic',100) RETURNING id`, "cache-"+suffix+"@example.invalid").Scan(&uid))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform,rate_multiplier) VALUES($1,'gemini',2) RETURNING id`, "cache-"+suffix).Scan(&gid))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota) VALUES($1,$2,'cache-test',$3,100) RETURNING id`, uid, "sk-cache-"+suffix, gid).Scan(&kid))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type) VALUES($1,'gemini','apikey') RETURNING id`, "cache-"+suffix).Scan(&aid))
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.UpdateIntervalHours = 100000
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), []byte(`{"gemini-2.5-pro":{"litellm_provider":"gemini","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.00001,"cache_storage_cost_per_token_per_hour":0.000002}}`), 0600))
	pricing := service.NewPricingService(cfg, nil)
	require.NoError(t, pricing.Initialize())
	billing := service.NewBillingService(cfg, pricing)
	ledger := NewUsageBillingRepository(nil, db)
	usage := NewUsageLogRepository(nil, db)
	gw := service.NewGatewayService(nil, nil, usage, ledger, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	repo := &geminiCachedContentRepository{sql: db}
	svc := service.NewGeminiCachedContentService(repo, nil, cfg)
	svc.SetBillingGateway(gw)
	record := &service.GeminiCachedContent{PublicID: "db" + suffix, UserID: uid, APIKeyID: kid, GroupID: gid, AccountID: aid, Model: "gemini-2.5-pro", UpstreamModel: "models/gemini-2.5-pro", UpstreamName: "cachedContents/synthetic", TotalTokenCount: 1000, ExpireTime: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)}
	group := &service.Group{ID: gid, Platform: service.PlatformGemini, RateMultiplier: 2}
	user := &service.User{ID: uid}
	key := &service.APIKey{ID: kid, UserID: uid, User: user, GroupID: &gid, Group: group, Quota: 100}
	input := &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: service.GeminiCachedContentCreateUsageRequestID(record.PublicID), Model: record.Model, Usage: service.ClaudeUsage{InputTokens: 1000}, CacheStorageTokenHours: 2000}, APIKey: key, User: user, Account: &service.Account{ID: aid, Type: service.AccountTypeAPIKey, Platform: service.PlatformGemini}, PricingAt: time.Now().UTC(), APIKeyService: cachedChargeQuotaUpdater{}}
	_, err := db.ExecContext(ctx, `SELECT 1`)
	require.NoError(t, err)
	return gw, svc, repo, record, input
}

func TestGeminiCachedContentDB_MigrationPreservesExistingRows(t *testing.T) {
	db := cachedChargeDB(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	schema := fmt.Sprintf("cache_migration_%d", time.Now().UnixNano())
	_, err = tx.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	_, err = tx.Exec(`SET LOCAL search_path TO ` + schema)
	require.NoError(t, err)
	for _, name := range []string{"243_gemini_cached_contents.sql", "244_gemini_cached_contents_channel_usage.sql", "245_gemini_cached_contents_mutation_lease.sql"} {
		payload, e := dbmigrations.FS.ReadFile(name)
		require.NoError(t, e)
		_, e = tx.Exec(string(payload))
		require.NoError(t, e)
	}
	baseline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	_, err = tx.Exec(`INSERT INTO gemini_cached_contents(public_id,user_id,api_key_id,group_id,account_id,upstream_name,model,upstream_model,expire_time) VALUES('existing',1,2,3,4,'cachedContents/old','gemini-2.5-pro','models/gemini-2.5-pro',$1)`, baseline)
	require.NoError(t, err)
	payload, err := dbmigrations.FS.ReadFile("913_gemini_cached_content_charge_intent.sql")
	require.NoError(t, err)
	_, err = tx.Exec(string(payload))
	require.NoError(t, err)
	_, err = tx.Exec(string(payload))
	require.NoError(t, err)
	repo := &geminiCachedContentRepository{sql: tx}
	existing, err := repo.GetForOwner(ctx, 2, "existing")
	require.NoError(t, err)
	require.False(t, existing.BillingPending)
	require.Nil(t, existing.ChargeIntent)
	require.True(t, existing.ExpireTime.Equal(baseline))
	list, err := repo.ListForOwner(ctx, 2, 3, time.Now(), 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestGeminiCachedContentDB_RecoveryAfterLedgerBeforeUsage(t *testing.T) {
	db := cachedChargeDB(t)
	ctx := context.Background()
	gw, svc, repo, record, input := cachedChargeFixture(t, db)
	intent, err := gw.PrepareCachedContentCharge(ctx, input)
	require.NoError(t, err)
	require.InDelta(t, 0.01, intent.Command.BalanceCost, 1e-8)
	require.Equal(t, 2000.0, intent.StorageTokenHours)
	require.NoError(t, svc.SaveCharged(ctx, record, intent))
	var stored []byte
	var ready bool
	require.NoError(t, db.QueryRow(`SELECT charge_intent,billing_ready FROM gemini_cached_contents WHERE id=$1`, record.ID).Scan(&stored, &ready))
	require.False(t, ready)
	var frozen service.GeminiCachedContentChargeIntent
	require.NoError(t, json.Unmarshal(stored, &frozen))
	require.Equal(t, intent.Command, frozen.Command)
	_, err = svc.Resolve(ctx, record.APIKeyID, &record.GroupID, record.PublicName())
	require.ErrorIs(t, err, service.ErrGeminiCachedContentNotFound)
	_, err = repo.GetForOwner(ctx, record.APIKeyID+1, record.PublicID)
	require.ErrorIs(t, err, service.ErrGeminiCachedContentNotFound)
	list, err := svc.List(ctx, record.APIKeyID, record.GroupID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = db.Exec(`CREATE OR REPLACE FUNCTION cache_test_fail_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure after ledger commit'; END $$; CREATE TRIGGER cache_test_fail_usage BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION cache_test_fail_usage()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS cache_test_fail_usage ON usage_logs; DROP FUNCTION IF EXISTS cache_test_fail_usage()`)
	})
	require.ErrorContains(t, svc.SettleCharge(ctx, record), "injected failure")
	var balance, quota float64
	var dedup, logs int
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, record.UserID).Scan(&balance))
	require.InDelta(t, 99.99, balance, 1e-8)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, record.APIKeyID).Scan(&quota))
	require.InDelta(t, 0.01, quota, 1e-8)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1`, intent.Command.RequestID).Scan(&dedup))
	require.Equal(t, 1, dedup)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, intent.Command.RequestID).Scan(&logs))
	require.Zero(t, logs)
	loaded, err := repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.BillingPending)
	require.NotEmpty(t, loaded.ChargeLastError)
	require.Equal(t, 1, loaded.ChargeAttempts)
	_, err = db.Exec(`DROP TRIGGER cache_test_fail_usage ON usage_logs`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE gemini_cached_contents SET charge_next_attempt_at=NOW() WHERE id=$1`, record.ID)
	require.NoError(t, err)
	// Price/rate changes after persistence cannot alter recovery.
	input.APIKey.Group.RateMultiplier = 999
	input.Result.CacheStorageTokenHours = 999999
	_, err = db.Exec(`UPDATE groups SET rate_multiplier=999 WHERE id=$1`, record.GroupID)
	require.NoError(t, err)
	restarted := service.NewGeminiCachedContentService(repo, nil, nil)
	// Restart with no pricing service at all: settlement only needs frozen data.
	recoveryGW := service.NewGatewayService(nil, nil, NewUsageLogRepository(nil, db), NewUsageBillingRepository(nil, db), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	restarted.SetBillingGateway(recoveryGW)
	count, err := restarted.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = restarted.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, record.UserID).Scan(&balance))
	require.InDelta(t, 99.99, balance, 1e-8)
	var actual float64
	require.NoError(t, db.QueryRow(`SELECT actual_cost FROM usage_logs WHERE request_id=$1`, intent.Command.RequestID).Scan(&actual))
	require.InDelta(t, 0.01, actual, 1e-8)
	_, err = restarted.Resolve(ctx, record.APIKeyID, &record.GroupID, record.PublicName())
	require.NoError(t, err)
}

func TestGeminiCachedContentDB_StartupWorker(t *testing.T) {
	db := cachedChargeDB(t)
	ctx := context.Background()
	gw, svc, repo, record, input := cachedChargeFixture(t, db)
	intent, err := gw.PrepareCachedContentCharge(ctx, input)
	require.NoError(t, err)
	require.NoError(t, svc.SaveCharged(ctx, record, intent))
	// Exercise the production provider/wire entry: no HTTP request drives recovery.
	worker := service.ProvideGeminiCachedContentService(repo, nil, nil, gw)
	defer worker.Stop()
	require.Eventually(t, func() bool {
		got, e := repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
		return e == nil && !got.BillingPending
	}, 5*time.Second, 20*time.Millisecond)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, record.UserID).Scan(&balance))
	require.InDelta(t, 99.99, balance, 1e-8)
}

func TestGeminiCachedContentDB_UsageBeforeVisibilityCrash(t *testing.T) {
	db := cachedChargeDB(t)
	ctx := context.Background()
	gw, svc, repo, record, input := cachedChargeFixture(t, db)
	intent, err := gw.PrepareCachedContentCharge(ctx, input)
	require.NoError(t, err)
	bad := *record
	bad.GroupID++
	require.ErrorContains(t, svc.SaveCharged(ctx, &bad, intent), "owner conflict")
	require.NoError(t, svc.SaveCharged(ctx, record, intent))
	_, err = db.Exec(`CREATE OR REPLACE FUNCTION cache_test_fail_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.charge_intent IS NOT NULL AND NEW.charge_intent IS NULL THEN RAISE EXCEPTION 'injected visibility commit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER cache_test_fail_complete BEFORE UPDATE ON gemini_cached_contents FOR EACH ROW EXECUTE FUNCTION cache_test_fail_complete()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS cache_test_fail_complete ON gemini_cached_contents; DROP FUNCTION IF EXISTS cache_test_fail_complete()`)
	})
	require.ErrorContains(t, svc.SettleCharge(ctx, record), "injected visibility")
	var logs int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, intent.Command.RequestID).Scan(&logs))
	require.Equal(t, 1, logs)
	loaded, err := repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.BillingPending)
	_, err = svc.Resolve(ctx, record.APIKeyID, &record.GroupID, record.PublicName())
	require.ErrorIs(t, err, service.ErrGeminiCachedContentNotFound)
	_, err = db.Exec(`DROP TRIGGER cache_test_fail_complete ON gemini_cached_contents`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE gemini_cached_contents SET charge_next_attempt_at=NOW() WHERE id=$1`, record.ID)
	require.NoError(t, err)
	count, err := svc.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, intent.Command.RequestID).Scan(&logs))
	require.Equal(t, 1, logs)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, record.UserID).Scan(&balance))
	require.InDelta(t, 99.99, balance, 1e-8)
	_, err = svc.Resolve(ctx, record.APIKeyID, &record.GroupID, record.PublicName())
	require.NoError(t, err)
	// Stopping recovery preserves a second pending bill and prevents business work.
	secondGW, _, _, second, secondInput := cachedChargeFixture(t, db)
	secondIntent, err := secondGW.PrepareCachedContentCharge(ctx, secondInput)
	require.NoError(t, err)
	require.NoError(t, svc.SaveCharged(ctx, second, secondIntent))
	svc.Stop()
	count, err = svc.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	loaded, err = repo.GetForOwner(ctx, second.APIKeyID, second.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.BillingPending)
	restart := service.NewGeminiCachedContentService(repo, nil, nil)
	restart.SetBillingGateway(secondGW)
	count, err = restart.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestGeminiCachedContentDB_SaveCrashLeaseDeletionAndMutation(t *testing.T) {
	db := cachedChargeDB(t)
	ctx := context.Background()
	gw, svc, repo, record, input := cachedChargeFixture(t, db)
	intent, err := gw.PrepareCachedContentCharge(ctx, input)
	require.NoError(t, err)
	require.NoError(t, svc.SaveCharged(ctx, record, intent))
	// Competing instances: exactly one lease can own this durable intent.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	winner := ""
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprint(i)
			claimed, e := repo.ClaimCharge(ctx, record.ID, intent.Command.RequestID, token, time.Minute)
			mu.Lock()
			defer mu.Unlock()
			require.NoError(t, e)
			if claimed != nil {
				wins++
				winner = token
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 1, wins)
	count, err := svc.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = db.Exec(`UPDATE gemini_cached_contents SET charge_lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, record.ID)
	require.NoError(t, err)
	claimed, err := repo.ClaimCharge(ctx, record.ID, intent.Command.RequestID, "restart", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.ErrorIs(t, repo.CompleteCharge(ctx, record.ID, intent.Command.RequestID, winner), service.ErrGeminiCachedContentMutationConflict)
	require.NoError(t, repo.FailCharge(ctx, record.ID, "restart", "simulated crash"))
	_, err = db.Exec(`UPDATE gemini_cached_contents SET charge_next_attempt_at=NOW() WHERE id=$1`, record.ID)
	require.NoError(t, err)
	count, err = svc.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	loaded, err := repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	baseline := loaded.ExpireTime
	target := baseline.Add(time.Hour)
	acquired, err := repo.BeginMutation(ctx, record.ID, baseline, "renew", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = repo.BeginMutation(ctx, record.ID, baseline, "competitor", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)
	patchIntent := *intent
	patchIntent.Command.RequestID = service.GeminiCachedContentPatchUsageRequestID(record.PublicID)
	patchIntent.Usage.RequestID = patchIntent.Command.RequestID
	require.ErrorIs(t, repo.CommitChargedMutation(ctx, record.ID, "competitor", target, &patchIntent), service.ErrGeminiCachedContentMutationConflict)
	require.NoError(t, repo.ReleaseMutation(ctx, record.ID, "renew"))
	require.ErrorIs(t, repo.CommitChargedMutation(ctx, record.ID, "renew", target, &patchIntent), service.ErrGeminiCachedContentMutationConflict)
	acquired, err = repo.BeginMutation(ctx, record.ID, baseline, "renew2", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, repo.CommitChargedMutation(ctx, record.ID, "renew2", target, &patchIntent))
	loaded, err = repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.ExpireTime.Equal(baseline))
	require.True(t, loaded.ChargeTargetExpire.Equal(target))
	require.ErrorIs(t, repo.CompleteCharge(ctx, record.ID, intent.Command.RequestID, winner), service.ErrGeminiCachedContentMutationConflict)
	// Deletion cancels visibility, never the already accepted charge.
	require.NoError(t, repo.SoftDelete(ctx, record.ID))
	count, err = svc.RecoverPendingCharges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	_, err = repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.ErrorIs(t, err, service.ErrGeminiCachedContentNotFound)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, record.UserID).Scan(&balance))
	require.InDelta(t, 99.98, balance, 1e-8)
	var remaining int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM gemini_cached_contents WHERE id=$1 AND charge_intent IS NOT NULL`, record.ID).Scan(&remaining))
	require.Zero(t, remaining)
}
