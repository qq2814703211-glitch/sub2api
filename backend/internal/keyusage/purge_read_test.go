package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReaderFallsBackAcrossConcurrentPurgeWithoutLockInversion(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	purge, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if err := purge.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, err = purge.Exec(`LOCK TABLE usage_logs IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	var schema string
	require.NoError(t, purge.QueryRow(`SELECT current_schema()`).Scan(&schema))
	type result struct {
		handled bool
		err     error
	}
	completed := make(chan result, 1)
	go func() {
		_, handled, e := r.TryRead(ctx, []int64{1}, time.Time{}, time.Time{})
		completed <- result{handled, e}
	}()
	var pid int
	waitForCondition(t, func() bool {
		_ = db.QueryRow(`SELECT pid FROM pg_locks WHERE relation='usage_logs'::regclass AND NOT granted AND mode='AccessShareLock' LIMIT 1`).Scan(&pid)
		return pid != 0
	})
	var auxiliaryLocks int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND relation IN('key_usage_hourly'::regclass,'key_usage_changes'::regclass,'key_usage_covered'::regclass)`, pid).Scan(&auxiliaryLocks))
	_, err = purge.Exec(dropCaptureSQL(schema) + dropTablesSQL(schema))
	require.NoError(t, err)
	_, err = purge.Exec(`DELETE FROM settings WHERE key=$1`, SettingKey)
	require.NoError(t, err)
	require.NoError(t, purge.Commit())
	outcome := <-completed
	require.NoError(t, outcome.err)
	require.False(t, outcome.handled)
	require.Zero(t, auxiliaryLocks, "reader must not hold auxiliary locks while waiting for source")
}
