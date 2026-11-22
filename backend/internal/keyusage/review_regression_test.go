package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitForCondition(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("concurrent operation did not reach expected wait state")
}

func TestStartupUsesConfigurationInsideLifecycleLock(t *testing.T) {
	r, db := fixture(t)
	ctx := context.Background()
	enablePaused(t, r)
	r.Stop()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, err = tx.Exec(`SELECT pg_advisory_xact_lock($1)`, lockID)
	require.NoError(t, err)
	other := New(db)
	defer other.Stop()
	completed := make(chan error, 1)
	go func() { completed <- other.Load(ctx) }()
	waitForCondition(t, func() bool {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&n)
		return n > 0
	})
	var schema string
	require.NoError(t, tx.QueryRow(`SELECT current_schema()`).Scan(&schema))
	_, err = tx.Exec(dropCaptureSQL(schema))
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE settings SET value='{"enabled":false,"interval_seconds":10}' WHERE key=$1`, SettingKey)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NoError(t, <-completed)
	var triggers int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgrelid='usage_logs'::regclass AND tgname LIKE 'key_usage_%'`).Scan(&triggers))
	require.Zero(t, triggers, "a stale startup must not reinstall capture after disable")
	require.False(t, other.enabled.Load())
}

func TestBackfillLocksSourceBeforeSummary(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, err = blocker.Exec(`LOCK TABLE key_usage_hourly IN SHARE MODE`)
	require.NoError(t, err)
	worker, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() {
		if err := worker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	var pid int
	require.NoError(t, worker.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid))
	completed := make(chan error, 1)
	go func() { completed <- backfillHour(ctx, worker, r.clock().Add(-2*time.Hour)) }()
	waitForCondition(t, func() bool {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM pg_locks WHERE pid=$1 AND relation='key_usage_hourly'::regclass AND NOT granted`, pid).Scan(&n)
		return n > 0
	})
	var sourceLocked bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='usage_logs'::regclass AND granted AND mode='AccessShareLock')`, pid).Scan(&sourceLocked))
	// Release blockers before asserting, including the pre-fix failing path.
	require.NoError(t, blocker.Rollback())
	require.NoError(t, <-completed)
	require.NoError(t, worker.Commit())
	require.True(t, sourceLocked, fmt.Sprintf("worker %d must acquire source before auxiliary write locks to coexist with TRUNCATE", pid))
}
