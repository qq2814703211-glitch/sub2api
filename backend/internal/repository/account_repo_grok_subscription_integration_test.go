package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGrokSubscriptionPostgresAtomicBoundaries(t *testing.T) {
	dsn := os.Getenv("GROK_SUBSCRIPTION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id bigint primary key,platform text,type text,credentials jsonb,extra jsonb,proxy_id bigint,deleted_at timestamptz,updated_at timestamptz);
 CREATE TEMP TABLE proxies(id bigint,protocol text,host text,port integer,username text,password text,deleted_at timestamptz,status text);
 INSERT INTO proxies VALUES(9,'http','proxy.test',8080,'','',NULL,'');
 CREATE TEMP TABLE scheduler_outbox(event_type text,account_id bigint,group_id bigint,payload jsonb)`)
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	initial := map[string]any{"access_token": "at", "refresh_token": "rt", "subscription_tier": "supergrok_heavy", "model_mapping": map[string]any{"alias": "model"}, "_token_version": 7}
	raw, err := json.Marshal(initial)
	require.NoError(t, err)
	reset := func() {
		_, err := db.ExecContext(ctx, `TRUNCATE accounts,scheduler_outbox`)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO accounts VALUES(42,'grok','oauth',$1,'{"unrelated":true}',9,NULL,NOW())`, string(raw))
		require.NoError(t, err)
	}
	stamp := time.Now().UTC()
	snapshot := func(tier string, at time.Time) map[string]any {
		return map[string]any{"tier": tier, "display_name": tier, "source": "settings", "requested_at": at.Format(time.RFC3339Nano), "observed_at": at.Add(time.Second).Format(time.RFC3339Nano), "status_code": 200, "scope": "test-scope"}
	}
	proxy := int64(9)
	proxyConfig := &service.Proxy{ID: 9, Protocol: "http", Host: "proxy.test", Port: 8080}
	apply := func(expected map[string]any, next map[string]any) bool {
		ok, err := repo.UpdateGrokSubscriptionIfUnchanged(ctx, 42, expected, &proxy, next, proxyConfig)
		require.NoError(t, err)
		return ok
	}
	t.Run("preserves unrelated state and projects tier", func(t *testing.T) {
		reset()
		require.True(t, apply(initial, snapshot("free", stamp)))
		var creds, extra []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=42`).Scan(&creds, &extra))
		var c, e map[string]any
		require.NoError(t, json.Unmarshal(creds, &c))
		require.NoError(t, json.Unmarshal(extra, &e))
		require.Equal(t, "at", c["access_token"])
		require.Equal(t, "rt", c["refresh_token"])
		require.Equal(t, float64(7), c["_token_version"])
		require.Equal(t, map[string]any{"alias": "model"}, c["model_mapping"])
		require.Equal(t, "free", c["subscription_tier"])
		require.Equal(t, "free", e["subscription_tier"])
		require.Equal(t, true, e["unrelated"])
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox`).Scan(&n))
		require.Equal(t, 1, n)
	})
	t.Run("same identity latest request wins even after projected tier changes", func(t *testing.T) {
		reset()
		require.True(t, apply(initial, snapshot("free", stamp)))
		require.False(t, apply(initial, snapshot("supergrok_heavy", stamp.Add(-time.Second))))
		require.False(t, apply(initial, snapshot("supergrok_heavy", stamp)))
		require.True(t, apply(initial, snapshot("ultra", stamp.Add(time.Second))))
		var tier string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->>'subscription_tier' FROM accounts`).Scan(&tier))
		require.Equal(t, "ultra", tier)
	})
	for _, mutation := range []string{`credentials=jsonb_set(credentials,'{access_token}','"reauthorized"')`, `credentials=jsonb_set(credentials,'{refresh_token}','"rotated"')`, `credentials=jsonb_set(credentials,'{model_mapping}','{}')`, `proxy_id=10`, `platform='openai'`, `type='apikey'`, `deleted_at=NOW()`} {
		t.Run(mutation, func(t *testing.T) {
			reset()
			_, err := db.ExecContext(ctx, `UPDATE accounts SET `+mutation)
			require.NoError(t, err)
			require.False(t, apply(initial, snapshot("free", stamp)))
			var n int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox`).Scan(&n))
			require.Zero(t, n)
		})
	}
	t.Run("outbox failure rolls back patch", func(t *testing.T) {
		reset()
		_, err := db.ExecContext(ctx, `ALTER TABLE scheduler_outbox ADD CONSTRAINT reject_event CHECK (account_id<>42)`)
		require.NoError(t, err)
		ok, err := repo.UpdateGrokSubscriptionIfUnchanged(ctx, 42, initial, &proxy, snapshot("free", stamp), proxyConfig)
		require.Error(t, err)
		require.False(t, ok)
		var tier string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->>'subscription_tier' FROM accounts`).Scan(&tier))
		require.Equal(t, "supergrok_heavy", tier)
		_, err = db.ExecContext(ctx, `ALTER TABLE scheduler_outbox DROP CONSTRAINT reject_event`)
		require.NoError(t, err)
	})
	t.Run("admin stale save preserves locked live subscription", func(t *testing.T) {
		reset()
		require.True(t, apply(initial, snapshot("free", stamp)))
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		a := &service.Account{ID: 42, Platform: "grok", Type: "oauth", Credentials: initial, ProxyID: &proxy, Proxy: proxyConfig, Extra: map[string]any{"unrelated": true}}
		merged, err := lockAndMergeAccountProbeExtra(dbent.NewTxContext(ctx, tx), tx.Client(), a)
		require.NoError(t, err)
		require.NotNil(t, merged["grok_subscription_snapshot"], "locked observation must survive a stale whole-extra save")
	})
	t.Run("transaction scope rolls back without escaping", func(t *testing.T) {
		reset()
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		ok, err := repo.UpdateGrokSubscriptionIfUnchanged(dbent.NewTxContext(ctx, tx), 42, initial, &proxy, snapshot("free", stamp), proxyConfig)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, tx.Rollback())
		var tier string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->>'subscription_tier' FROM accounts`).Scan(&tier))
		require.Equal(t, "supergrok_heavy", tier)
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox`).Scan(&n))
		require.Zero(t, n)
	})
	t.Run("same proxy id edited during request is rejected", func(t *testing.T) {
		reset()
		_, err := db.ExecContext(ctx, `UPDATE proxies SET host='changed.test' WHERE id=9`)
		require.NoError(t, err)
		require.False(t, apply(initial, snapshot("free", stamp)))
		_, err = db.ExecContext(ctx, `UPDATE proxies SET host='proxy.test' WHERE id=9`)
		require.NoError(t, err)
	})
	t.Run("corrupt previous timestamp is repairable", func(t *testing.T) {
		reset()
		_, err := db.ExecContext(ctx, `UPDATE accounts SET extra='{"grok_subscription_snapshot":{"requested_at":"broken"}}'`)
		require.NoError(t, err)
		require.True(t, apply(initial, snapshot("free", stamp)))
	})
	t.Run("late rotated tokens persist without undoing settings", func(t *testing.T) {
		reset()
		require.True(t, apply(initial, snapshot("free", stamp)))
		ok, err := repo.UpdateGrokOAuthCredentialsIfUnchanged(ctx, 42, initial, &proxy, map[string]any{"access_token": "new", "refresh_token": "new-rt", "subscription_tier": "supergrok_heavy"})
		require.NoError(t, err)
		require.True(t, ok, "settings projection must not discard provider-rotated refresh token")
		var at, rt, tier string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->>'access_token',credentials->>'refresh_token',credentials->>'subscription_tier' FROM accounts`).Scan(&at, &rt, &tier))
		require.Equal(t, "new", at)
		require.Equal(t, "new-rt", rt)
		require.Equal(t, "free", tier)
	})
}
