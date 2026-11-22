package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFailedPurgePreservesEnabledRuntime(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	ctx := context.Background()
	b := r.clock().Add(-2 * time.Hour)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'retained',$1,3)`, b)
	require.NoError(t, err)
	locked, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() {
		if err := locked.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, err = locked.Exec(`SELECT pg_advisory_xact_lock($1)`, lockID)
	require.NoError(t, err)
	timeout, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.Error(t, r.Purge(timeout))
	require.NoError(t, locked.Rollback())
	require.True(t, r.enabled.Load())
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, r.Purge(ctx))
	require.False(t, r.enabled.Load())
}
