package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRollupReaderSeesBaseAndQueueAtomically(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx := context.Background()
	b := r.clock().Add(-3 * time.Hour).UTC().Truncate(time.Hour)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'base',$1,1.25)`, b)
	require.NoError(t, err)
	cover(t, r, b, b.Add(time.Hour))
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'pending',$1,2.75)`, b)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	require.NoError(t, applyChanges(ctx, tx))
	// Uncommitted queue deletion and base update must not become two separately
	// visible facts. A second reader still observes the old base plus the queue.
	got, handled, err := r.TryRead(ctx, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 4.0, got[1].TotalActualCost)
	require.NoError(t, tx.Commit())
	got, handled, err = r.TryRead(ctx, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 4.0, got[1].TotalActualCost)
}

func TestRollupLateCommitAfterBackfillSnapshotRemainsInQueue(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx := context.Background()
	b := r.clock().Add(-3 * time.Hour).UTC().Truncate(time.Hour)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	// Pin the backfill snapshot before a writer starts.
	var n int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&n))
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'after_snapshot',$1,5.125)`, b)
	require.NoError(t, err)
	require.NoError(t, backfillHour(ctx, tx, b))
	require.NoError(t, tx.Commit())
	got, handled, err := r.TryRead(ctx, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 5.125, got[1].TotalActualCost)
	require.NoError(t, r.Step(ctx))
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
}
