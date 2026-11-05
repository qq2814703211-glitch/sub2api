//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// These tests exercise the real RecordUsage -> PostgreSQL transaction boundary.
// Supplier completion is a fixture; recovery is triggered by a later poll, not
// by an automatic reconciler. Both Redis backends use the production cache code.
type mediaRecoveryFixture struct {
	svc     *service.OpenAIGatewayService
	user    *service.User
	key     *service.APIKey
	account *service.Account
	task    string
}

func newMediaRecoveryFixture(t *testing.T, rdb *redis.Client, ledger service.UsageBillingRepository) *mediaRecoveryFixture {
	t.Helper()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{
		Email: "media-lease-" + uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100,
	})
	key := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID, Key: "sk-media-lease-" + uuid.NewString(), Name: "media-lease", Quota: 100, RateLimit5h: 100,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "media-lease-account-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
	})
	price := 0.001
	// An in-memory pricing group avoids coupling these tests to catalog workers;
	// the balance, key quotas, usage log and dedup rows are all real PostgreSQL.
	key.Group = &service.Group{RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{
		Models: []string{"media-recovery-fixture"}, BillingMode: service.BillingModeToken,
		MediaPricing: &service.MediaPricing{Rules: []service.MediaPricingRule{{Name: "output", Unit: "token", OutputPrice: &price}}},
	}}}
	if ledger == nil {
		ledger = NewUsageBillingRepository(client, integrationDB)
	}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	billing := service.NewBillingService(cfg, nil)
	cache := NewGatewayCache(rdb)
	svc := service.NewOpenAIGatewayService(nil, NewUsageLogRepository(client, integrationDB), ledger,
		nil, nil, nil, cache, cfg, nil, nil, billing, nil, &service.BillingCacheService{}, nil,
		&service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	return &mediaRecoveryFixture{svc: svc, user: user, key: key, account: account, task: "seedance:" + uuid.NewString()}
}

func (f *mediaRecoveryFixture) store(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.svc.BindGrokMediaVideoRequestAccount(ctx, nil, f.task, f.user.ID, f.key.ID, f.account.ID))
	require.NoError(t, f.svc.StoreGrokVideoPendingBilling(ctx, f.task, f.user.ID, f.key.ID, service.GrokVideoPendingBilling{
		Model: "media-recovery-fixture", BillingModel: "media-recovery-fixture",
		MediaUsage: &service.MediaUsage{Resolution: "1080p", DurationSeconds: 11},
	}))
}

func (f *mediaRecoveryFixture) claim(t *testing.T) string {
	t.Helper()
	owner, err := f.svc.ClaimGrokVideoBilling(context.Background(), f.task, f.user.ID, f.key.ID)
	require.NoError(t, err)
	require.NotEmpty(t, owner)
	return owner
}

func (f *mediaRecoveryFixture) record(owner string) error {
	ctx := context.Background()
	pending, err := f.svc.LoadGrokVideoPendingBilling(ctx, f.task, f.user.ID, f.key.ID)
	if err != nil {
		return err
	}
	if pending == nil {
		return fmt.Errorf("missing create-time billing snapshot")
	}
	// Same completed-task result a status poll prepares. A new result object per
	// call also models two processes without sharing mutable forwarding state.
	return f.svc.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result: &service.OpenAIForwardResult{
			RequestID: service.StableGrokVideoBillingRequestID(f.task), ResponseID: f.task, BillingClaimOwner: owner,
			Model: pending.Model, BillingModel: pending.BillingModel,
			MediaUsage: service.MergeMediaUsage(pending.MediaUsage, &service.MediaUsage{Count: 1}),
			Usage:      service.OpenAIUsage{OutputTokens: 1250},
		},
		APIKey: f.key, User: f.user, Account: f.account,
		APIKeyService:      &service.APIKeyService{},
		RequestPayloadHash: service.HashUsageRequestPayload([]byte(f.task)),
	})
}

func (f *mediaRecoveryFixture) assertCharges(t *testing.T, charged bool) {
	t.Helper()
	ctx := context.Background()
	wantCost, wantCount := 0.0, 0
	if charged {
		wantCost, wantCount = 1.25, 1
	}
	var balance, quota, window float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", f.user.ID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used, usage_5h FROM api_keys WHERE id = $1", f.key.ID).Scan(&quota, &window))
	require.InDelta(t, 100-wantCost, balance, 1e-8)
	require.InDelta(t, wantCost, quota, 1e-8)
	require.InDelta(t, wantCost, window, 1e-8)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", service.StableGrokVideoBillingRequestID(f.task), f.key.ID).Scan(&count))
	require.Equal(t, wantCount, count)
	if charged {
		// Usage logging is batched best-effort; wait for the actual persisted row.
		require.Eventually(t, func() bool {
			var n int
			err := integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs WHERE request_id = $1 AND api_key_id = $2 AND actual_cost = 1.25", service.StableGrokVideoBillingRequestID(f.task), f.key.ID).Scan(&n)
			return err == nil && n == 1
		}, 5*time.Second, 10*time.Millisecond)
	}
}

func mediaLeaseKey(f *mediaRecoveryFixture) string {
	return fmt.Sprintf("%s%d:%d:%s", grokVideoBilledPrefix, f.user.ID, f.key.ID, f.task)
}

func TestUsageBillingMediaRecovery_ClaimCrashBeforeRecordUsage(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f := newMediaRecoveryFixture(t, rdb, nil)
	f.store(t)
	oldOwner := f.claim(t)
	f.assertCharges(t, false) // process dies after claim, before RecordUsage
	server.FastForward(2*time.Minute + time.Second)
	newOwner := f.claim(t)
	require.NotEqual(t, oldOwner, newOwner)
	require.NoError(t, f.svc.ReleaseGrokVideoBilling(context.Background(), f.task, f.user.ID, f.key.ID, oldOwner))
	storedOwner, err := server.Get(mediaLeaseKey(f))
	require.NoError(t, err)
	require.Equal(t, newOwner, storedOwner)
	require.NoError(t, f.record(newOwner))
	f.assertCharges(t, true)
}

func TestUsageBillingMediaRecovery_PendingAndOwnerSurviveFourteenDays(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f := newMediaRecoveryFixture(t, rdb, nil)
	f.store(t)
	for _, elapsed := range []time.Duration{25 * time.Hour, 12*24*time.Hour + 22*time.Hour, 2 * time.Hour} {
		server.FastForward(elapsed)
		id, err := f.svc.ResolveGrokMediaVideoRequestAccount(context.Background(), nil, f.task, f.user.ID, f.key.ID)
		require.NoError(t, err)
		require.Equal(t, f.account.ID, id)
		pending, err := f.svc.LoadGrokVideoPendingBilling(context.Background(), f.task, f.user.ID, f.key.ID)
		require.NoError(t, err)
		require.NotNil(t, pending)
		require.Equal(t, "1080p", pending.MediaUsage.Resolution)
	}
	// A later poll within the 15-day retention period can still settle once.
	require.NoError(t, f.record(f.claim(t)))
	f.assertCharges(t, true)
}

func expireMediaLease(t *testing.T, f *mediaRecoveryFixture, rdb *redis.Client) {
	t.Helper()
	// Accelerate real Redis expiry without waiting two production minutes.
	ok, err := rdb.PExpire(context.Background(), mediaLeaseKey(f), time.Millisecond).Result()
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		n, err := rdb.Exists(context.Background(), mediaLeaseKey(f)).Result()
		return err == nil && n == 0
	}, 3*time.Second, 5*time.Millisecond)
}

// Hold both RecordUsage calls at the durable boundary so the first worker can
// outlive its lease and overlap a second worker that holds the replacement.
type mediaRecoveryBarrier struct {
	service.UsageBillingRepository
	arrived chan struct{}
	release chan struct{}
}

func (b *mediaRecoveryBarrier) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	b.arrived <- struct{}{}
	<-b.release
	return b.UsageBillingRepository.Apply(ctx, cmd)
}

func TestUsageBillingRepositoryApply_ConcurrentRetryAcrossExpiredMediaLeaseDeduplicates(t *testing.T) {
	rdb := integrationRedis // dedicated DB; UUID-scoped keys avoid cross-test collisions
	barrier := &mediaRecoveryBarrier{UsageBillingRepository: NewUsageBillingRepository(testEntClient(t), integrationDB), arrived: make(chan struct{}, 2), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	t.Cleanup(release)
	f := newMediaRecoveryFixture(t, rdb, barrier)
	f.store(t)
	owner1 := f.claim(t)
	ttl, err := rdb.TTL(context.Background(), mediaLeaseKey(f)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Minute)
	require.LessOrEqual(t, ttl, 2*time.Minute)
	errs := make(chan error, 2)
	go func() { errs <- f.record(owner1) }()
	select {
	case <-barrier.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not reach durable billing")
	}
	expireMediaLease(t, f, rdb)
	owner2 := f.claim(t)
	require.NotEqual(t, owner1, owner2)
	require.NoError(t, f.svc.ReleaseGrokVideoBilling(context.Background(), f.task, f.user.ID, f.key.ID, owner1))
	storedOwner, err := rdb.Get(context.Background(), mediaLeaseKey(f)).Result()
	require.NoError(t, err)
	require.Equal(t, owner2, storedOwner)
	go func() { errs <- f.record(owner2) }()
	select {
	case <-barrier.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement worker did not reach durable billing")
	}
	release()
	for range 2 {
		select {
		case err := <-errs:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent billing did not finish")
		}
	}
	f.assertCharges(t, true)
}

type mediaRecoveryCommitBeforeAck struct {
	service.UsageBillingRepository
	once sync.Once
}

func (l *mediaRecoveryCommitBeforeAck) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	result, err := l.UsageBillingRepository.Apply(ctx, cmd)
	if err == nil && result.Applied {
		// Model process death after PostgreSQL commits, before RecordUsage receives
		// the result or writes its usage log. No fabricated transaction outcomes.
		l.once.Do(func() { panic("fixture process died before commit acknowledgement") })
	}
	return result, err
}

func TestUsageBillingMediaRecovery_CommitBeforeAckRetriesWithoutCharge(t *testing.T) {
	ledger := &mediaRecoveryCommitBeforeAck{UsageBillingRepository: NewUsageBillingRepository(testEntClient(t), integrationDB)}
	f := newMediaRecoveryFixture(t, integrationRedis, ledger)
	f.store(t)
	owner := f.claim(t)
	require.PanicsWithValue(t, "fixture process died before commit acknowledgement", func() { _ = f.record(owner) })
	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT balance FROM users WHERE id = $1", f.user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 1e-8)
	// DB committed but the process lost its acknowledgement. A later poll gets a
	// fresh lease and calls RecordUsage again with the same durable task key.
	expireMediaLease(t, f, integrationRedis)
	require.NoError(t, f.record(f.claim(t)))
	f.assertCharges(t, true)
}

type mediaRecoveryRollbackOnce struct {
	service.UsageBillingRepository
	failed bool
}

func (l *mediaRecoveryRollbackOnce) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	if !l.failed {
		l.failed = true
		invalid := *cmd
		// Claim the dedup key in a real transaction, then fail a later effect so
		// PostgreSQL must roll the claim back together with the attempted debit.
		invalid.UserID = -1
		return l.UsageBillingRepository.Apply(ctx, &invalid)
	}
	return l.UsageBillingRepository.Apply(ctx, cmd)
}

func TestUsageBillingMediaRecovery_RolledBackChargeCanRetryAndLog(t *testing.T) {
	ledger := &mediaRecoveryRollbackOnce{UsageBillingRepository: NewUsageBillingRepository(testEntClient(t), integrationDB)}
	f := newMediaRecoveryFixture(t, integrationRedis, ledger)
	f.store(t)
	owner := f.claim(t)
	require.ErrorIs(t, f.record(owner), service.ErrUserNotFound)
	f.assertCharges(t, false)
	var logs int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM usage_logs WHERE request_id = $1 AND api_key_id = $2", service.StableGrokVideoBillingRequestID(f.task), f.key.ID).Scan(&logs))
	require.Zero(t, logs, "failed settlement cannot occupy the eventual successful log's unique key")
	// Same release the handler performs when RecordUsage fails.
	require.NoError(t, f.svc.ReleaseGrokVideoBilling(context.Background(), f.task, f.user.ID, f.key.ID, owner))
	require.NoError(t, f.record(f.claim(t)))
	f.assertCharges(t, true)
}
