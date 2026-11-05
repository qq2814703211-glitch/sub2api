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
