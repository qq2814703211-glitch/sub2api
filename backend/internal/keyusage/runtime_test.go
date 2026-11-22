package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*Runtime, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("KEY_USAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("KEY_USAGE_TEST_DSN required for isolated PostgreSQL integration")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("keys_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	if strings.HasPrefix(dsn, "postgres") {
		u, e := url.Parse(dsn)
		require.NoError(t, e)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE settings(id bigserial PRIMARY KEY,key text UNIQUE,value text NOT NULL,updated_at timestamptz DEFAULT now());
 CREATE TABLE usage_logs(id bigserial PRIMARY KEY,api_key_id bigint NOT NULL,request_id text UNIQUE,created_at timestamptz NOT NULL,actual_cost numeric(20,10) NOT NULL);
 CREATE INDEX usage_keys_time ON usage_logs(api_key_id,created_at); CREATE INDEX usage_time ON usage_logs(created_at);`)
	require.NoError(t, err)
	r := New(db)
	r.clock = func() time.Time { return time.Date(2026, 11, 3, 4, 35, 0, 0, time.UTC) }
	t.Cleanup(func() {
		r.Stop()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, db
}
func enablePaused(t *testing.T, r *Runtime) {
	t.Helper()
	require.NoError(t, r.Configure(context.Background(), Config{Enabled: true, IntervalSeconds: 3600}))
	r.mu.Lock()
	r.stopLocked()
	r.mu.Unlock()
}
func cover(t *testing.T, r *Runtime, start, end time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	for b := start.UTC().Truncate(time.Hour); b.Before(end.UTC().Truncate(time.Hour)); b = b.Add(time.Hour) {
		require.NoError(t, backfillHour(ctx, tx, b))
	}
	require.NoError(t, tx.Commit())
}
func sameAsRaw(t *testing.T, r *Runtime, ids []int64, start, end time.Time) {
	t.Helper()
	now := r.clock()
	if start.IsZero() {
		start = now.AddDate(0, 0, -30)
	}
	if end.IsZero() {
		end = now
	}
	got, ok, err := r.TryRead(context.Background(), ids, start, end)
	require.NoError(t, err)
	require.True(t, ok)
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		var total, today float64
		require.NoError(t, r.db.QueryRow(`SELECT COALESCE(SUM(actual_cost) FILTER(WHERE created_at >= $2 AND created_at < $3),0),COALESCE(SUM(actual_cost) FILTER(WHERE created_at >= $4),0) FROM usage_logs WHERE api_key_id=$1 AND created_at>=LEAST($2::timestamptz,$4::timestamptz)`, id, start, end, r.dayStart(now)).Scan(&total, &today))
		require.Equal(t, total, got[id].TotalActualCost)
		require.Equal(t, today, got[id].TodayActualCost)
	}
}

func TestRollupColdReadCoveredPendingAndZero(t *testing.T) {
	r, db := fixture(t)
	ctx := context.Background()
	now := r.clock()
	start := now.AddDate(0, 0, -30)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES
 (1,'a',$1,1.25),(1,'b',$2,2.75),(2,'c',$3,4.125),(1,'future',$4,9.5)`, start.Add(time.Minute), now.Add(-2*time.Hour), now.Add(-time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, ok, err := r.TryRead(ctx, []int64{1}, start, now)
	require.NoError(t, err)
	require.False(t, ok)
	enablePaused(t, r)
	sameAsRaw(t, r, []int64{1, 2, 3, 1, -1}, start, now)
	cover(t, r, start.Add(time.Hour), now)
	sameAsRaw(t, r, []int64{1, 2, 3}, start, now)
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'late',$1,0.75)`, now.Add(-2*time.Hour))
	require.NoError(t, err)
	sameAsRaw(t, r, []int64{1, 2, 3}, start, now)
	require.NoError(t, r.Step(ctx))
	sameAsRaw(t, r, []int64{1, 2, 3}, start, now)
	got, _, err := r.TryRead(ctx, []int64{1, 3}, start, now)
	require.NoError(t, err)
	require.Equal(t, 4.75, got[1].TotalActualCost)
	require.Equal(t, 0.0, got[3].TotalActualCost)
}

func TestRollupBackfillCommitDisorderAndDuplicateRollback(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx := context.Background()
	b := r.clock().Add(-3 * time.Hour).UTC().Truncate(time.Hour)
	early, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if err := early.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, err = early.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'early_uncommitted',$1,2.5)`, b.Add(time.Minute))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'later_committed',$1,3.5)`, b.Add(2*time.Minute))
	require.NoError(t, err)
	cover(t, r, b, b.Add(time.Hour))
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, early.Commit())
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, r.Step(ctx))
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'later_committed',$1,3.5) ON CONFLICT DO NOTHING`, b)
	require.NoError(t, err)
	rolled, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = rolled.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'rolled',$1,100)`, b)
	require.NoError(t, err)
	require.NoError(t, rolled.Rollback())
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	got, _, err := r.TryRead(ctx, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 6.0, got[1].TotalActualCost)
}

func TestRollupCorrectionsDeletesTruncateAndReenable(t *testing.T) {
	r, db := fixture(t)
	ctx := context.Background()
	b := r.clock().Add(-2 * time.Hour).UTC().Truncate(time.Hour)
	enablePaused(t, r)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'change',$1,2),(1,'remove',$1,3)`, b)
	require.NoError(t, err)
	cover(t, r, b, b.Add(time.Hour))
	_, err = db.Exec(`UPDATE usage_logs SET api_key_id=2,created_at=created_at+interval '1 hour',actual_cost=7 WHERE request_id='change'; DELETE FROM usage_logs WHERE request_id='remove'`)
	require.NoError(t, err)
	sameAsRaw(t, r, []int64{1, 2}, time.Time{}, time.Time{})
	require.NoError(t, r.Step(ctx))
	sameAsRaw(t, r, []int64{1, 2}, time.Time{}, time.Time{})
	require.NoError(t, r.Configure(ctx, Config{Enabled: false}))
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgrelid='usage_logs'::regclass AND tgname LIKE 'key_usage_%'`).Scan(&n))
	require.Zero(t, n)
	_, err = db.Exec(`UPDATE usage_logs SET actual_cost=11`)
	require.NoError(t, err)
	enablePaused(t, r)
	sameAsRaw(t, r, []int64{1, 2}, time.Time{}, time.Time{})
	_, err = db.Exec(`TRUNCATE usage_logs`)
	require.NoError(t, err)
	sameAsRaw(t, r, []int64{1, 2}, time.Time{}, time.Time{})
	require.NoError(t, r.Purge(ctx))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname LIKE 'key_usage_%'`).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM settings WHERE key=$1`, SettingKey).Scan(&n))
	require.Zero(t, n)
}

func TestRollupTimezoneFractionalHourBoundary(t *testing.T) {
	r, db := fixture(t)
	loc, err := time.LoadLocation("Asia/Kathmandu")
	require.NoError(t, err)
	r.dayStart = func(now time.Time) time.Time {
		now = now.In(loc)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	}
	r.clock = func() time.Time { return time.Date(2026, 11, 3, 1, 15, 0, 0, loc) }
	now := r.clock()
	day := r.dayStart(now)
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'before',$1,2.1),(1,'after',$2,3.2)`, day.Add(-time.Second), day.Add(time.Second))
	require.NoError(t, err)
	enablePaused(t, r)
	cover(t, r, day.Add(-time.Hour), now)
	sameAsRaw(t, r, []int64{1}, now.AddDate(0, 0, -30), now)
	got, _, err := r.TryRead(context.Background(), []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 3.2, got[1].TodayActualCost)
}
