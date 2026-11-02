package keyusage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRollupRestartPreservesCaptureAndPendingChanges(t *testing.T) {
	r, db := fixture(t)
	ctx := context.Background()
	b := r.clock().Add(-2 * time.Hour).UTC().Truncate(time.Hour)
	enablePaused(t, r)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'first',$1,1.1)`, b)
	require.NoError(t, err)
	cover(t, r, b, b.Add(time.Hour))
	r.Stop()
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'while_stopped',$1,2.2)`, b)
	require.NoError(t, err)
	require.NoError(t, r.Load(ctx))
	r.mu.Lock()
	r.stopLocked()
	r.mu.Unlock()
	sameAsRaw(t, r, []int64{1}, time.Time{}, time.Time{})
	var hours int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM key_usage_covered WHERE bucket_start=$1`, b).Scan(&hours))
	require.Equal(t, 1, hours)
}

func TestRollupFutureAndHistoricalWindowsKeepLegacyBounds(t *testing.T) {
	r, db := fixture(t)
	now := r.clock()
	b := now.Add(-48 * time.Hour).UTC().Truncate(time.Hour)
	_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost) VALUES(1,'old',$1,1.25),(1,'today',$2,2.5),(1,'future',$3,4.75)`, b, now.Add(-time.Minute), now.Add(2*time.Hour))
	require.NoError(t, err)
	enablePaused(t, r)
	cover(t, r, b, b.Add(time.Hour))
	cover(t, r, now.Add(-time.Hour), now)
	// Today's value intentionally remains open-ended even for a historical total.
	sameAsRaw(t, r, []int64{1}, b.Add(-time.Hour), b.Add(time.Hour))
	sameAsRaw(t, r, []int64{1}, now.Add(time.Hour), now.Add(3*time.Hour))
	sameAsRaw(t, r, []int64{1}, now, now.Add(-time.Hour))
}

func TestRollupExternallyDisabledRuntimeFallsBack(t *testing.T) {
	r, db := fixture(t)
	enablePaused(t, r)
	_, err := db.Exec(`UPDATE settings SET value='{"enabled":false,"interval_seconds":10}' WHERE key=$1`, SettingKey)
	require.NoError(t, err)
	got, handled, err := r.TryRead(context.Background(), []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.False(t, handled)
	require.Nil(t, got)
}
