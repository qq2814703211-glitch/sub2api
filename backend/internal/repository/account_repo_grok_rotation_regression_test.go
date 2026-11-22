package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGrokSubscriptionPostgresNoSubjectRotation(t *testing.T) {
	dsn := os.Getenv("GROK_SUBSCRIPTION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id bigint primary key, platform text, type text, credentials jsonb, extra jsonb, proxy_id bigint, deleted_at timestamptz, updated_at timestamptz, parent_account_id bigint);
 CREATE TEMP TABLE proxies(id bigint,protocol text,host text,port integer,username text,password text,deleted_at timestamptz);
 CREATE TEMP TABLE scheduler_outbox(event_type text, account_id bigint, group_id bigint, payload jsonb)`)
	require.NoError(t, err)
	token := func(n string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":5,"nonce":"`+n+`"}`)) + ".sig"
	}
	oldToken, newToken := token("old"), token("new")
	initial := map[string]any{"access_token": oldToken, "refresh_token": "old-rt", "subscription_tier": "supergrok_heavy"}
	a := &service.Account{ID: 42, Platform: "grok", Type: "oauth", Credentials: initial}
	// Mirror the public scope contract to seed a real settings observation without new APIs.
	scopeData, err := json.Marshal([]any{a.ID, a.Platform, a.Type, oldToken, "", a.GetGrokBaseURL(), nil, a.GetHeaderOverrides()})
	require.NoError(t, err)
	digest := sha256.Sum256(scopeData)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	snapshot := map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "requested_at": stamp, "observed_at": stamp, "status_code": 200, "scope": hex.EncodeToString(digest[:])}
	initialJSON, err := json.Marshal(initial)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO accounts VALUES(42,'grok','oauth',$1,'{}',NULL,NULL,NOW())`, string(initialJSON))
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	ok, err := repo.UpdateGrokSubscriptionIfUnchanged(ctx, 42, initial, nil, snapshot, nil)
	require.NoError(t, err)
	require.True(t, ok)
	next := map[string]any{"access_token": newToken, "refresh_token": "new-rt", "subscription_tier": "supergrok_heavy"}
	ok, err = repo.UpdateGrokOAuthCredentialsIfUnchanged(ctx, 42, initial, nil, next)
	require.NoError(t, err)
	require.True(t, ok)
	var creds, extra []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=42`).Scan(&creds, &extra))
	require.NoError(t, json.Unmarshal(creds, &a.Credentials))
	require.NoError(t, json.Unmarshal(extra, &a.Extra))
	require.Equal(t, newToken, a.GetGrokAccessToken())
	require.Equal(t, "new-rt", a.GetGrokRefreshToken())
	require.Equal(t, "free", (&service.GrokQuotaFetcher{}).BuildUsageInfo(a).SubscriptionTier, "durable rotated credentials must retain confirmed Free")
	require.True(t, service.GrokSubscriptionSnapshotValid(a))
	public := dto.AccountFromServiceShallow(a)
	require.Equal(t, "free", public.Credentials["subscription_tier"])
	require.Contains(t, public.Extra, "grok_subscription_snapshot")
	t.Run("caller transaction rollback keeps credentials and snapshot", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer func() {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Error(err)
			}
		}()
		ok, err := repo.UpdateGrokOAuthCredentialsIfUnchanged(dbent.NewTxContext(ctx, tx), 42, a.Credentials, nil, map[string]any{"access_token": token("rolled-back"), "refresh_token": "rolled-back-rt"})
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, tx.Rollback())
		var gotCreds, gotExtra []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=42`).Scan(&gotCreds, &gotExtra))
		require.JSONEq(t, string(creds), string(gotCreds))
		require.JSONEq(t, string(extra), string(gotExtra))
	})
	t.Run("outbox failure rolls back scope migration too", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `ALTER TABLE scheduler_outbox RENAME TO unavailable_outbox`)
		require.NoError(t, err)
		defer func() {
			if _, err := db.ExecContext(ctx, `ALTER TABLE unavailable_outbox RENAME TO scheduler_outbox`); err != nil {
				t.Error(err)
			}
		}()
		ok, err := repo.UpdateGrokOAuthCredentialsIfUnchanged(ctx, 42, a.Credentials, nil, map[string]any{"access_token": token("failed"), "refresh_token": "failed-rt"})
		require.Error(t, err)
		require.False(t, ok)
		var gotCreds, gotExtra []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=42`).Scan(&gotCreds, &gotExtra))
		require.JSONEq(t, string(creds), string(gotCreds))
		require.JSONEq(t, string(extra), string(gotExtra))
	})
	// A separately authorized token is not a provider-issued rotation.
	_, err = db.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{access_token}',to_jsonb($1::text)) WHERE id=42`, token("other-authorization"))
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=42`).Scan(&creds, &extra))
	a.Credentials = nil
	a.Extra = nil
	require.NoError(t, json.Unmarshal(creds, &a.Credentials))
	require.NoError(t, json.Unmarshal(extra, &a.Extra))
	require.False(t, service.GrokSubscriptionSnapshotValid(a))
	require.NotContains(t, dto.AccountFromServiceShallow(a).Extra, "grok_subscription_snapshot")
	require.Equal(t, "supergrok_heavy", (&service.GrokQuotaFetcher{}).BuildUsageInfo(a).SubscriptionTier)
}
