package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGrokSubscriptionPostgresManagedExtraWrites(t *testing.T) {
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
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts(id bigint primary key, platform text, type text, credentials jsonb, extra jsonb, proxy_id bigint, deleted_at timestamptz, updated_at timestamptz);
 CREATE TEMP TABLE scheduler_outbox(event_type text,account_id bigint,group_id bigint,payload jsonb,dedup_key text UNIQUE);
 INSERT INTO accounts VALUES(42,'grok','oauth','{}','{"grok_subscription_snapshot":{"tier":"free","requested_at":"new"}}',NULL,NULL,NOW())`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	for _, value := range []any{nil, map[string]any{"tier": "supergrok_heavy", "requested_at": "old"}} {
		for _, path := range []string{"extra", "bulk"} {
			t.Run(path, func(t *testing.T) {
				_, err := db.ExecContext(ctx, `UPDATE accounts SET extra='{"grok_subscription_snapshot":{"tier":"free","requested_at":"new"}}'`)
				require.NoError(t, err)
				updates := map[string]any{"grok_subscription_snapshot": value, "custom": "keep"}
				if path == "extra" {
					require.NoError(t, repo.UpdateExtra(ctx, 42, updates))
				} else {
					n, err := repo.BulkUpdate(ctx, []int64{42}, service.AccountBulkUpdate{Extra: updates})
					require.NoError(t, err)
					require.EqualValues(t, 1, n)
				}
				var tier, custom string
				require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(extra->'grok_subscription_snapshot'->>'tier',''),extra->>'custom' FROM accounts`).Scan(&tier, &custom))
				require.Equal(t, "free", tier)
				require.Equal(t, "keep", custom)
			})
		}
	}
}
