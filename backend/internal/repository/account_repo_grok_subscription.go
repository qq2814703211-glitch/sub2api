package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Only the guarded settings writer and known token rotation may write this key.
func stripGrokSubscriptionExtra(extra map[string]any) map[string]any {
	if _, ok := extra["grok_subscription_snapshot"]; !ok {
		return extra
	}
	copy := copyJSONMap(extra)
	delete(copy, "grok_subscription_snapshot")
	return copy
}

// Patch only subscription keys and publish the durable invalidation atomically.
// Ignore the materialized tier when comparing credentials; requested_at decides
// ordering between concurrent observations of the same identity. Read/validate
// timestamps in Go and CAS the raw JSON: malformed historical data is repairable
// and never reaches a PostgreSQL timestamp cast.
func (r *accountRepository) UpdateGrokSubscriptionIfUnchanged(ctx context.Context, id int64, expected map[string]any, proxyID *int64, snapshot map[string]any, proxy *service.Proxy) (updated bool, retErr error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	// Serialize proxy identity before taking any account row lock, matching proxy edits.
	if proxyID != nil && dbent.TxFromContext(ctx) == nil {
		client := r.client
		if client == nil {
			if db, ok := r.sql.(*sql.DB); ok {
				client = dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			}
		}
		if client == nil {
			return false, errors.New("proxy subscription transaction is not configured")
		}
		tx, err := client.Tx(ctx)
		if err != nil {
			return false, err
		}
		defer cleanup.Rollback(&retErr, tx)
		applied, err := r.UpdateGrokSubscriptionIfUnchanged(dbent.NewTxContext(ctx, tx), id, expected, proxyID, snapshot, proxy)
		if err != nil || !applied {
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
		return true, nil
	}
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.UpdateGrokSubscriptionIfUnchanged(ctx, id, expected, proxyID, snapshot, proxy)
	}
	if proxyID != nil {
		if proxy == nil || proxy.ID != *proxyID {
			return false, nil
		}
		matches, err := lockAndMatchProbeProxyIdentity(ctx, r.client, &service.Account{ProxyID: proxyID, Proxy: proxy})
		if err != nil || !matches {
			return false, err
		}
	}
	tier, _ := snapshot["tier"].(string)
	requested, _ := snapshot["requested_at"].(string)
	started, err := time.Parse(time.RFC3339Nano, requested)
	if err != nil || tier == "" {
		return false, errors.New("invalid Grok subscription snapshot")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	var proxyJSON any
	if proxyID != nil {
		if proxy == nil || proxy.ID != *proxyID {
			return false, nil
		}
		data, err := json.Marshal(map[string]any{"protocol": proxy.Protocol, "host": proxy.Host, "port": proxy.Port, "username": proxy.Username, "password": proxy.Password})
		if err != nil {
			return false, err
		}
		proxyJSON = string(data)
	}
	for attempt := 0; attempt < 3; attempt++ {
		rows, err := r.sql.QueryContext(ctx, `SELECT COALESCE(extra->'grok_subscription_snapshot','null'::jsonb) FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id)
		if err != nil {
			return false, err
		}
		if !rows.Next() {
			err = errors.Join(rows.Err(), rows.Close())
			return false, err
		}
		var previous []byte
		err = errors.Join(rows.Scan(&previous), rows.Close())
		if err != nil {
			return false, err
		}
		var old struct {
			RequestedAt string `json:"requested_at"`
			Scope       string `json:"scope"`
		}
		if json.Unmarshal(previous, &old) == nil && old.Scope == snapshot["scope"] {
			if stamp, err := time.Parse(time.RFC3339Nano, old.RequestedAt); err == nil && !started.After(stamp) {
				return false, nil
			}
		}
		result, err := r.sql.ExecContext(ctx, `
 WITH updated AS (
  UPDATE accounts AS a
  SET credentials = COALESCE(a.credentials, '{}'::jsonb) || jsonb_build_object('subscription_tier', $1::text),
      extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object('subscription_tier', $1::text, 'grok_subscription_snapshot', $2::jsonb),
      updated_at = NOW()
  WHERE a.id = $3 AND a.deleted_at IS NULL
    AND a.platform = $4 AND a.type = $5
    AND a.credentials - 'subscription_tier' = $6::jsonb - 'subscription_tier'
    AND a.proxy_id IS NOT DISTINCT FROM $7
    AND COALESCE(a.extra->'grok_subscription_snapshot','null'::jsonb) = $8::jsonb
    AND (a.proxy_id IS NULL OR EXISTS (
      SELECT 1 FROM proxies p WHERE p.id=a.proxy_id AND p.deleted_at IS NULL
      AND jsonb_build_object('protocol',p.protocol,'host',p.host,'port',p.port,'username',COALESCE(p.username,''),'password',COALESCE(p.password,'')) = $10::jsonb))
  RETURNING a.id
 )
 INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
 SELECT $9, updated.id, NULL, NULL FROM updated
 `, tier, string(snapshotJSON), id, service.PlatformGrok, service.AccountTypeOAuth, string(expectedJSON), proxyID, string(previous), service.SchedulerOutboxEventAccountChanged, proxyJSON)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n > 0 {
			if dbent.TxFromContext(ctx) == nil {
				r.syncSchedulerAccountSnapshotDetached(ctx, id)
			}
			return true, nil
		}
	}
	return false, nil
}
