//go:build videodb

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type videoDBSettings struct {
	service.SettingRepository
	db *sql.DB
}

func (s *videoDBSettings) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		var value string
		err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}
func (s *videoDBSettings) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, value)
	return err
}

func TestVideoDBSubscriptionAndAccountQuota(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/video","duration":10}}`)
	var subscriptionID int64
	require.NoError(t, f.db.QueryRow(`INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at) VALUES($1,$2,NOW(),NOW()+INTERVAL '1 day') RETURNING id`, f.user, f.group).Scan(&subscriptionID))
	in.APIKey.Group.SubscriptionType = service.SubscriptionTypeSubscription
	in.Subscription = &service.UserSubscription{ID: subscriptionID, UserID: f.user, GroupID: f.group}
	in.APIKey.Group.RateMultiplier = 2
	in.Account.Extra = map[string]any{"quota_limit": 100.0, "quota_daily_limit": 100.0, "quota_weekly_limit": 100.0}
	_, err := f.db.Exec(`UPDATE accounts SET extra=$1::jsonb WHERE id=$2`, `{"quota_limit":100,"quota_daily_limit":100,"quota_weekly_limit":100}`, f.account)
	require.NoError(t, err)
	f.begin(t, svc, in, "subscription")
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 3))
	var balance, used, quota, accountUsed float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
	require.NoError(t, f.db.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=$1`, subscriptionID).Scan(&used))
	require.InDelta(t, 0.5, used, 1e-8)
	require.NoError(t, f.db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, f.key).Scan(&quota))
	require.InDelta(t, 0.5, quota, 1e-8)
	require.NoError(t, f.db.QueryRow(`SELECT COALESCE((extra->>'quota_used')::numeric,0) FROM accounts WHERE id=$1`, f.account).Scan(&accountUsed))
	require.InDelta(t, 0.25, accountUsed, 1e-8)
}

func TestVideoDBWorkerHotDisableAndTerminalStates(t *testing.T) {
	for _, status := range []string{"failed", "expired"} {
		t.Run(status, func(t *testing.T) {
			f := newVideoDBFixture(t, "grok")
			svc, in, u := f.service(t, `{"status":"`+status+`"}`)
			j := f.begin(t, svc, in, "terminal-"+status)
			require.NoError(t, svc.SetVideoBillingWorkerEnabled(context.Background(), false))
			require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 2))
			require.Zero(t, u.calls)
			require.NoError(t, svc.SetVideoBillingWorkerEnabled(context.Background(), true))
			require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 2))
			require.Equal(t, 1, u.calls)
			row, err := NewVideoBillingJobRepository(f.db).GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			require.Equal(t, status, row.State)
			var balance float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
			require.Equal(t, 100.0, balance)
		})
	}
}

// Build the intent through production observation and stop at a real SQL failure,
// rather than installing a synthetic SimpleMode intent that cannot debit money.
func videoDBRetryFixture(t *testing.T, stage string) (*videoDBFixture, *service.OpenAIGatewayService, *service.OpenAIRecordUsageInput, *service.VideoBillingJob) {
	t.Helper()
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/video","duration":10}}`)
	j := f.begin(t, svc, in, "admin-retry-"+stage)
	ctx := context.Background()
	repo := NewVideoBillingJobRepository(f.db)
	if stage != "without_intent" {
		target := "UPDATE OF quota_used ON api_keys"
		if stage == "billed" {
			target = "INSERT ON usage_logs"
		}
		_, err := f.db.Exec(`CREATE FUNCTION video_retry_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic retry boundary failure'; END $$;
 CREATE TRIGGER video_retry_fail BEFORE ` + target + ` FOR EACH ROW EXECUTE FUNCTION video_retry_fail()`)
		require.NoError(t, err)
		require.NoError(t, svc.RunVideoBillingRecovery(ctx, 1))
		table := "api_keys"
		if stage == "billed" {
			table = "usage_logs"
		}
		_, err = f.db.Exec(`DROP TRIGGER video_retry_fail ON ` + table + `;DROP FUNCTION video_retry_fail()`)
		require.NoError(t, err)
		row, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
		require.NoError(t, err)
		require.NotNil(t, row.Intent)
		require.False(t, row.Intent.SimpleMode)
		require.InDelta(t, 0.25, row.Intent.Command.BalanceCost, 1e-8)
		expectedState := "ready"
		expectedBalance := 100.0
		if stage == "billed" {
			expectedState = "billed"
			expectedBalance = 99.75
		}
		require.Equal(t, expectedState, row.State)
		var balance float64
		require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
		require.Equal(t, expectedBalance, balance)
		var logs int
		require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE api_key_id=$1`, f.key).Scan(&logs))
		require.Zero(t, logs)
	}
	if stage != "billed" {
		claim, err := repo.Claim(ctx, j.ID, time.Minute)
		require.NoError(t, err)
		require.NoError(t, repo.Finish(ctx, claim, "reconciliation", "done", "manual_review_required", 0))
	}
	return f, svc, in, j
}

func TestVideoDBAdminRetryRecoversRealSettlement(t *testing.T) {
	for _, stage := range []string{"without_intent", "with_intent", "billed"} {
		t.Run(stage, func(t *testing.T) {
			f, svc, in, j := videoDBRetryFixture(t, stage)
			ctx := context.Background()
			repo := NewVideoBillingJobRepository(f.db)
			before, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			// Active leases remain fenced, including their exact token and expiry.
			claim, err := repo.Claim(ctx, j.ID, time.Minute)
			require.NoError(t, err)
			var leaseUntil time.Time
			require.NoError(t, f.db.QueryRow(`SELECT lease_until FROM video_billing_jobs WHERE id=$1`, j.ID).Scan(&leaseUntil))
			require.ErrorIs(t, svc.RetryVideoBillingJob(ctx, j.ID), service.ErrVideoJobLeaseLost)
			var token string
			var afterLease time.Time
			require.NoError(t, f.db.QueryRow(`SELECT lease_token,lease_until FROM video_billing_jobs WHERE id=$1`, j.ID).Scan(&token, &afterLease))
			require.Equal(t, claim.LeaseToken, token)
			require.Equal(t, leaseUntil, afterLease)
			require.NoError(t, repo.Finish(ctx, claim, before.State, before.ProviderStatus, before.LastError, 0))

			// Neither a new service's prices nor a changed live card may replace a
			// frozen completion intent (or the original no-intent snapshot).
			in.APIKey.Group.VideoModelPrices[f.model]["720p"] = 99
			fresh, current, upstream := f.service(t, `{"status":"done","video":{"url":"https://example.test/video","duration":10}}`)
			current.APIKey.Group.VideoModelPrices[f.model]["720p"] = 77
			require.NoError(t, fresh.RetryVideoBillingJob(ctx, j.ID))
			requeued, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			require.Equal(t, before.Intent, requeued.Intent)
			require.NoError(t, fresh.RunVideoBillingRecovery(ctx, 3))
			row, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			require.Equal(t, "done", row.State, "last_error=%s", row.LastError)
			f.assertBill(t, 0.25)
			expectedState := "ready"
			expectedMethods := []string(nil)
			if stage == "without_intent" {
				expectedState = "submitted"
				expectedMethods = []string{"GET"}
			} else if stage == "billed" {
				expectedState = "billed"
			}
			require.Equal(t, expectedState, requeued.State)
			require.Equal(t, expectedMethods, upstream.methods)
			if before.Intent != nil {
				require.Equal(t, before.Intent, row.Intent)
			}
			// Repeated operational passes must not create another debit or usage.
			for n := 0; n < 3; n++ {
				require.NoError(t, fresh.RunVideoBillingRecovery(ctx, 3))
				f.assertBill(t, 0.25)
			}
			require.Equal(t, expectedMethods, upstream.methods)
		})
	}
}

func TestVideoDBReconciliationRequiresManualRetry(t *testing.T) {
	for _, stage := range []string{"without_intent", "with_intent"} {
		t.Run(stage, func(t *testing.T) {
			f, _, _, j := videoDBRetryFixture(t, stage)
			ctx := context.Background()
			repo := NewVideoBillingJobRepository(f.db)
			before, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			fresh, _, upstream := f.service(t, `{"status":"done","video":{"url":"https://example.test/video","duration":10}}`)
			for n := 0; n < 3; n++ {
				require.NoError(t, fresh.RunVideoBillingRecovery(ctx, 3))
			}
			after, err := repo.GetForOwner(ctx, j.TaskID, f.user, f.key, f.group)
			require.NoError(t, err)
			require.Equal(t, before, after, "unretried reconciliation must not be claimed, polled or settled")
			require.Zero(t, upstream.calls)
			var balance float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
			require.Equal(t, 100.0, balance)
			for _, table := range []string{"usage_billing_dedup", "usage_logs"} {
				var count int
				require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE api_key_id=$1`, f.key).Scan(&count))
				require.Zero(t, count)
			}
		})
	}
}

func TestVideoDBLongPendingRetainedAndRecoverable(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, u := f.service(t, `{"status":"pending"}`)
	j := f.begin(t, svc, in, "late-completion")
	_, err := f.db.Exec(`UPDATE video_billing_jobs SET created_at=NOW()-INTERVAL '3 days' WHERE id=$1`, j.ID)
	require.NoError(t, err)
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 3))
	row, err := NewVideoBillingJobRepository(f.db).GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, "reconciliation", row.State)
	require.Equal(t, "provider_pending_over_24h", row.LastError)
	require.NoError(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	u.body = `{"status":"done","video":{"url":"https://example.test/late","duration":10}}`
	fresh, _, _ := f.service(t, u.body)
	require.NoError(t, fresh.RunVideoBillingRecovery(context.Background(), 3))
	f.assertBill(t, 0.25)
}

func TestVideoDBCrashAfterUsageBeforeAcknowledgement(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/v","duration":8}}`)
	j := f.begin(t, svc, in, "after-usage")
	_, err := f.db.Exec(`CREATE FUNCTION video_ack_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='done' THEN RAISE EXCEPTION 'synthetic ack failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER video_ack_fail BEFORE UPDATE ON video_billing_jobs FOR EACH ROW EXECUTE FUNCTION video_ack_fail()`)
	require.NoError(t, err)
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 1))
	f.assertBill(t, 0.2)
	_, err = f.db.Exec(`DROP TRIGGER video_ack_fail ON video_billing_jobs;DROP FUNCTION video_ack_fail();UPDATE video_billing_jobs SET lease_until=NOW()-INTERVAL '1 second'`)
	require.NoError(t, err)
	fresh, _, u := f.service(t, "")
	require.NoError(t, fresh.RunVideoBillingRecovery(context.Background(), 2))
	f.assertBill(t, 0.2)
	require.Zero(t, u.calls, "persisted intent must settle without another provider request")
	row, err := NewVideoBillingJobRepository(f.db).GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, "done", row.State)
}

func TestVideoDBCreateAndSubmitTransactionFailure(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, "")
	_, err := f.db.Exec(`CREATE FUNCTION video_create_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic create failure'; END $$;
 CREATE TRIGGER video_create_fail BEFORE INSERT ON video_billing_jobs FOR EACH ROW EXECUTE FUNCTION video_create_fail()`)
	require.NoError(t, err)
	_, err = svc.BeginVideoBillingJob(context.Background(), in, service.GrokVideoPendingBilling{Model: f.model}, "grok")
	require.Error(t, err)
	_, err = f.db.Exec(`DROP TRIGGER video_create_fail ON video_billing_jobs;DROP FUNCTION video_create_fail()`)
	require.NoError(t, err)
	j, err := svc.BeginVideoBillingJob(context.Background(), in, service.GrokVideoPendingBilling{Model: f.model}, "grok")
	require.NoError(t, err)
	_, err = f.db.Exec(`CREATE FUNCTION video_submit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic submit failure'; END $$;
 CREATE TRIGGER video_submit_fail BEFORE UPDATE ON video_billing_jobs FOR EACH ROW EXECUTE FUNCTION video_submit_fail()`)
	require.NoError(t, err)
	require.Error(t, svc.SubmitVideoBillingJob(context.Background(), j, "not-public"))
	_, err = f.db.Exec(`DROP TRIGGER video_submit_fail ON video_billing_jobs;DROP FUNCTION video_submit_fail()`)
	require.NoError(t, err)
	jobs, err := repoListVideoTest(f.db)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Empty(t, jobs[0].TaskID)
	require.Equal(t, "creating", jobs[0].State)
	data, err := json.Marshal(jobs[0].Snapshot)
	require.NoError(t, err)
	for _, secret := range []string{"synthetic", "provider.example", "Bearer"} {
		require.NotContains(t, string(data), secret)
	}
	require.Greater(t, jobs[0].AvailableAt.Sub(time.Now()), time.Minute)
}
