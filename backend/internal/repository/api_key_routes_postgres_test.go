package repository

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"testing/fstest"
	"time"
)

// Opt-in isolated database, never an application's configured database.
func TestAPIKeyRoutesPostgres(t *testing.T) {
	dsn := os.Getenv("KEY_ROUTES_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KEY_ROUTES_TEST_POSTGRES_DSN not configured")
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	require.NoError(t, db.PingContext(ctx))
	// Start from the actual baseline migrations, then add 907 with existing rows.
	baseline := fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() == "907_api_key_group_routes.sql" {
			continue
		}
		b, err := migrations.FS.ReadFile(e.Name())
		require.NoError(t, err)
		baseline[e.Name()] = &fstest.MapFile{Data: b}
	}
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	users := NewUserRepository(client, db)
	repo := newAPIKeyRepositoryWithSQL(client, db)
	user := &service.User{Email: "route-pg@test.local", PasswordHash: "synthetic", Status: service.StatusActive, Balance: 100}
	require.NoError(t, users.Create(ctx, user))
	ids := make([]int64, 30)
	for i := range ids {
		g, err := client.Group.Create().SetName(fmt.Sprintf("route-%d", i)).SetPlatform(service.PlatformOpenAI).SetRateMultiplier(2.5).Save(ctx)
		require.NoError(t, err)
		ids[i] = g.ID
	}
	// Legacy Create has no route-table dependency before the additive migration.
	legacy := &service.APIKey{UserID: user.ID, Name: "legacy", Key: "synthetic-legacy-route-key", Status: service.StatusActive, GroupID: &ids[0]}
	require.NoError(t, repo.Create(ctx, legacy))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	got, err := repo.GetByID(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{ids[0]}, got.CandidateGroupIDs())
	key := &service.APIKey{UserID: user.ID, Name: "many", Key: "synthetic-many-route-key", Status: service.StatusActive, GroupID: &ids[0], RouteGroupIDs: ids, Quota: 1, RateLimit5h: 1}
	require.NoError(t, repo.Create(ctx, key))
	got, err = repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, ids, got.CandidateGroupIDs())
	keyService := service.NewAPIKeyService(repo, users, NewGroupRepository(client, db), nil, nil, nil, &config.Config{})
	createdByService, err := keyService.Create(ctx, user.ID, service.CreateAPIKeyRequest{Name: "via-service", GroupIDs: ids, ConcurrencyLimit: 3})
	require.NoError(t, err)
	require.Equal(t, ids, createdByService.CandidateGroupIDs())
	require.Equal(t, 3, createdByService.ConcurrencyLimit)
	reversed := append([]int64(nil), ids...)
	for l, r := 0, len(reversed)-1; l < r; l, r = l+1, r-1 {
		reversed[l], reversed[r] = reversed[r], reversed[l]
	}
	_, err = keyService.Update(ctx, createdByService.ID, user.ID, service.UpdateAPIKeyRequest{GroupIDs: &reversed})
	require.NoError(t, err)
	fromAuth, err := keyService.GetByKey(ctx, createdByService.Key)
	require.NoError(t, err)
	require.Equal(t, reversed, fromAuth.CandidateGroupIDs())
	require.Equal(t, 3, fromAuth.ConcurrencyLimit)
	require.EqualValues(t, ids[29], *fromAuth.GroupID)
	// Real FK error rolls metadata and DELETE/INSERT route replacement back.
	got.Name = "rollback"
	got.GroupID = &ids[29]
	got.RouteGroupIDs = []int64{ids[29], 9999999}
	require.Error(t, repo.Update(ctx, got, service.APIKeyUpdateFields{Name: true, GroupID: true, RouteGroupIDs: true}))
	got, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "many", got.Name)
	require.Equal(t, ids, got.CandidateGroupIDs())
	require.EqualValues(t, ids[0], *got.GroupID)
	failedCreate := &service.APIKey{UserID: user.ID, Name: "failed-create", Key: "synthetic-failed-create", Status: service.StatusActive, GroupID: &ids[0], RouteGroupIDs: []int64{ids[0], 9999999}}
	require.Error(t, repo.Create(ctx, failedCreate))
	exists, err := repo.ExistsByKey(ctx, failedCreate.Key)
	require.NoError(t, err)
	require.False(t, exists)
	// Real group membership produces cache invalidation targets for fallback #30.
	targets, err := repo.ListKeysByGroupID(ctx, ids[29])
	require.NoError(t, err)
	require.Contains(t, targets, key.Key)
	// Actual non-primary group price -> real idempotent billing repository -> DB.
	account, err := client.Account.Create().SetName("route-billing").SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeAPIKey).SetStatus(service.StatusActive).SetCredentials(map[string]any{"api_key": "synthetic"}).Save(ctx)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	usageRepo := NewUsageLogRepository(client, db)
	billingRepo := NewUsageBillingRepository(client, db)
	gateway := service.NewOpenAIGatewayService(nil, usageRepo, billingRepo, users, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	gid := ids[29]
	got.GroupID = &gid
	got.Group = &service.Group{ID: gid, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 2.5}
	got.User = user
	input := &service.OpenAIRecordUsageInput{APIKeyService: service.NewAPIKeyService(repo, users, nil, nil, nil, nil, cfg), APIKey: got, User: user, Account: &service.Account{ID: account.ID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}, Result: &service.OpenAIForwardResult{RequestID: "route-pg-idempotent", Model: "gpt-5.1", Usage: service.OpenAIUsage{InputTokens: 1000, OutputTokens: 200}, Duration: time.Second}}
	require.NoError(t, gateway.RecordUsage(service.ContextWithAPIKeyRoute(ctx, got), input))
	require.NoError(t, gateway.RecordUsage(service.ContextWithAPIKeyRoute(ctx, got), input))
	var groupID int64
	var rate, cost, balance, quota, window float64
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT group_id,rate_multiplier,actual_cost FROM usage_logs WHERE api_key_id=$1 AND request_id=$2", key.ID, "route-pg-idempotent").Scan(&groupID, &rate, &cost))
	require.Equal(t, gid, groupID)
	require.Equal(t, 2.5, rate)
	require.Greater(t, cost, 0.0)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.InDelta(t, 100-cost, balance, 1e-8)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT quota_used,usage_5h FROM api_keys WHERE id=$1", key.ID).Scan(&quota, &window))
	require.InDelta(t, cost, quota, 1e-8)
	require.InDelta(t, cost, window, 1e-8)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1 AND request_id=$2", key.ID, "route-pg-idempotent").Scan(&count))
	require.Equal(t, 1, count)
	// Position is positive, unique and unconstrained above 10.
	_, err = db.ExecContext(ctx, "UPDATE api_key_group_routes SET position=0 WHERE api_key_id=$1", key.ID)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
}
