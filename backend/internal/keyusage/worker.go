package keyusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"time"
)

// Step backfills one bounded hour and applies a bounded committed queue batch.
// Transaction advisory locking serializes workers without holding a session
// connection between ticks. No sequence watermark assumes commit order.
func (r *Runtime) Step(ctx context.Context) (retErr error) {
	if r == nil || !r.enabled.Load() {
		return nil
	}
	r.gate.RLock()
	defer r.gate.RUnlock()
	if !r.enabled.Load() {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer cleanup.Rollback(&retErr, tx)
	if _, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS SHARE MODE`); err != nil {
		return err
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lockID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT (value::jsonb->>'enabled')::boolean FROM settings WHERE key=$1),false)`, SettingKey).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	now := r.clock()
	start := now.AddDate(0, 0, -30).UTC().Truncate(time.Hour)
	end := now.UTC().Truncate(time.Hour)
	var bucket time.Time
	err = tx.QueryRowContext(ctx, `SELECT candidate FROM generate_series($1::timestamptz,$2::timestamptz-interval '1 hour',interval '1 hour') candidate
 WHERE NOT EXISTS(SELECT 1 FROM key_usage_covered WHERE bucket_start=candidate) ORDER BY candidate DESC LIMIT 1`, start, end).Scan(&bucket)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = backfillHour(ctx, tx, bucket); err != nil {
			return err
		}
	}
	if err = applyChanges(ctx, tx); err != nil {
		return err
	}
	// Bound retained optional state independently of the application's log policy.
	// Old raw data remains the authority whenever its coverage is removed.
	cutoff := start.Add(-time.Hour)
	if _, err = tx.ExecContext(ctx, `DELETE FROM key_usage_changes WHERE bucket_start<$1`, cutoff); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM key_usage_hourly WHERE bucket_start<$1`, cutoff); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM key_usage_covered WHERE bucket_start<$1`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillHour(ctx context.Context, tx *sql.Tx, bucket time.Time) error {
	if _, err := tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS SHARE MODE`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM key_usage_hourly WHERE bucket_start=$1`, bucket); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO key_usage_hourly(api_key_id,bucket_start,actual_cost)
 SELECT api_key_id,$1,SUM(actual_cost) FROM usage_logs WHERE created_at >= $1 AND created_at < $1::timestamptz+interval '1 hour' GROUP BY api_key_id`, bucket); err != nil {
		return fmt.Errorf("backfill key usage hour: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM key_usage_changes WHERE bucket_start=$1`, bucket); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO key_usage_covered(bucket_start) VALUES($1) ON CONFLICT DO NOTHING`, bucket)
	return err
}

func applyChanges(ctx context.Context, tx *sql.Tx) error {
	// DELETE RETURNING selects exactly visible committed rows. The rollup and
	// removal are atomic, so readers see either base+queue or updated base.
	_, err := tx.ExecContext(ctx, `WITH selected AS MATERIALIZED (
 SELECT q.id FROM key_usage_covered c CROSS JOIN LATERAL(
 SELECT id FROM key_usage_changes WHERE bucket_start=c.bucket_start ORDER BY id LIMIT 10000
 ) q LIMIT 10000
 ), removed AS (
 DELETE FROM key_usage_changes WHERE id IN(SELECT id FROM selected) RETURNING api_key_id,bucket_start,actual_cost
 ) INSERT INTO key_usage_hourly(api_key_id,bucket_start,actual_cost)
 SELECT api_key_id,bucket_start,SUM(actual_cost) FROM removed GROUP BY api_key_id,bucket_start
 ON CONFLICT(api_key_id,bucket_start) DO UPDATE SET actual_cost=key_usage_hourly.actual_cost+excluded.actual_cost`)
	return err
}
