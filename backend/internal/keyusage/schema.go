package keyusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"github.com/lib/pq"
)

func schemaName(ctx context.Context, tx *sql.Tx) (string, error) {
	var s string
	err := tx.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&s)
	return s, err
}
func qualified(s, n string) string { return pq.QuoteIdentifier(s) + "." + pq.QuoteIdentifier(n) }
func dropCaptureSQL(s string) string {
	logs := qualified(s, "usage_logs")
	return `DROP TRIGGER IF EXISTS key_usage_insert ON ` + logs + `; DROP TRIGGER IF EXISTS key_usage_update ON ` + logs + `; DROP TRIGGER IF EXISTS key_usage_delete ON ` + logs + `; DROP TRIGGER IF EXISTS key_usage_truncate ON ` + logs + `; DROP FUNCTION IF EXISTS ` + qualified(s, "key_usage_capture") + `();`
}
func dropTablesSQL(s string) string {
	return `DROP TABLE IF EXISTS ` + qualified(s, "key_usage_changes") + `,` + qualified(s, "key_usage_hourly") + `,` + qualified(s, "key_usage_covered") + `;`
}

func (r *Runtime) install(ctx context.Context, c Config, persist bool) (installed Config, retErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer cleanup.Rollback(&retErr, tx)
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='15s'`); err != nil {
		return c, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		return c, err
	}
	if !persist {
		var raw string
		err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, SettingKey).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			c = Config{IntervalSeconds: 10}
		} else if err != nil {
			return c, err
		} else {
			if err = json.Unmarshal([]byte(raw), &c); err != nil {
				return c, err
			}
			c, err = c.normalized()
			if err != nil {
				return c, err
			}
		}
	}
	// Acquire the source before auxiliary relations, matching TRUNCATE order.
	if _, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS EXCLUSIVE MODE`); err != nil {
		return c, err
	}
	s, err := schemaName(ctx, tx)
	if err != nil {
		return c, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='usage_logs'::regclass AND tgname='key_usage_insert' AND NOT tgisinternal)`).Scan(&exists)
	if err != nil {
		return c, err
	}
	if c.Enabled {
		if _, err = tx.ExecContext(ctx, schemaSQL(s)); err != nil {
			return c, fmt.Errorf("create key usage schema: %w", err)
		}
		if !exists {
			// The source lock is held only for lifecycle DDL, never backfill.
			if _, err = tx.ExecContext(ctx, `TRUNCATE `+qualified(s, "key_usage_hourly")+`,`+qualified(s, "key_usage_covered")+`,`+qualified(s, "key_usage_changes")+`;`+captureSQL(s)); err != nil {
				return c, err
			}
		}
	} else if exists {
		if _, err = tx.ExecContext(ctx, dropCaptureSQL(s)); err != nil {
			return c, err
		}
	}
	if persist {
		raw, _ := json.Marshal(c)
		_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,now()) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, SettingKey, string(raw))
		if err != nil {
			return c, err
		}
	}
	return c, tx.Commit()
}

func schemaSQL(s string) string {
	return `CREATE TABLE IF NOT EXISTS ` + qualified(s, "key_usage_hourly") + `(
 api_key_id bigint NOT NULL,bucket_start timestamptz NOT NULL,actual_cost numeric NOT NULL,
 PRIMARY KEY(api_key_id,bucket_start));
 CREATE TABLE IF NOT EXISTS ` + qualified(s, "key_usage_covered") + `(bucket_start timestamptz PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS ` + qualified(s, "key_usage_changes") + `(
 id bigserial PRIMARY KEY,api_key_id bigint NOT NULL,bucket_start timestamptz NOT NULL,actual_cost numeric NOT NULL);
 CREATE INDEX IF NOT EXISTS key_usage_changes_bucket_key ON ` + qualified(s, "key_usage_changes") + `(bucket_start,api_key_id);
 CREATE INDEX IF NOT EXISTS key_usage_changes_key_bucket ON ` + qualified(s, "key_usage_changes") + `(api_key_id,bucket_start);
 CREATE INDEX IF NOT EXISTS key_usage_changes_bucket_id ON ` + qualified(s, "key_usage_changes") + `(bucket_start,id);`
}
func captureSQL(s string) string {
	f := qualified(s, "key_usage_capture")
	q := qualified(s, "key_usage_changes")
	logs := qualified(s, "usage_logs")
	// Transition relations aggregate each statement without a hot shared row.
	// Hard deletes and corrections use signed deltas in the same writer transaction.
	return `CREATE FUNCTION ` + f + `() RETURNS trigger LANGUAGE plpgsql AS $capture$
 BEGIN
 IF TG_OP='TRUNCATE' THEN
  TRUNCATE ` + qualified(s, "key_usage_hourly") + `,` + qualified(s, "key_usage_covered") + `,` + q + `;
 ELSE
  IF TG_OP IN ('DELETE','UPDATE') THEN
   INSERT INTO ` + q + `(api_key_id,bucket_start,actual_cost)
   SELECT api_key_id,date_bin('1 hour',created_at,'2000-01-01 00:00:00+00'::timestamptz),-SUM(actual_cost)
   FROM old_rows GROUP BY 1,2;
  END IF;
  IF TG_OP IN ('INSERT','UPDATE') THEN
   INSERT INTO ` + q + `(api_key_id,bucket_start,actual_cost)
   SELECT api_key_id,date_bin('1 hour',created_at,'2000-01-01 00:00:00+00'::timestamptz),SUM(actual_cost)
   FROM new_rows GROUP BY 1,2;
  END IF;
 END IF;
 RETURN NULL;
 END;$capture$;
 CREATE TRIGGER key_usage_insert AFTER INSERT ON ` + logs + ` REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION ` + f + `();
 CREATE TRIGGER key_usage_update AFTER UPDATE ON ` + logs + ` REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION ` + f + `();
 CREATE TRIGGER key_usage_delete AFTER DELETE ON ` + logs + ` REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION ` + f + `();
 CREATE TRIGGER key_usage_truncate AFTER TRUNCATE ON ` + logs + ` FOR EACH STATEMENT EXECUTE FUNCTION ` + f + `();`
}
