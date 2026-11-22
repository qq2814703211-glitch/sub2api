package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGrokSubscriptionPostgresProxyEditBarrier(t *testing.T) {
	dsn := os.Getenv("GROK_SUBSCRIPTION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not set")
	}
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit_%v", commit), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			editor, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			defer func() {
				if err := editor.Close(); err != nil {
					t.Error(err)
				}
			}()
			editor.SetMaxOpenConns(1)
			writer, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			defer func() {
				if err := writer.Close(); err != nil {
					t.Error(err)
				}
			}()
			writer.SetMaxOpenConns(1)
			schema := fmt.Sprintf("grok_persistence_%d", time.Now().UnixNano())
			_, err = editor.ExecContext(ctx, `CREATE SCHEMA `+schema)
			require.NoError(t, err)
			defer func() {
				if _, err := editor.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
					t.Error(err)
				}
			}()
			for _, db := range []*sql.DB{editor, writer} {
				_, err = db.ExecContext(ctx, `SET search_path TO `+schema)
				require.NoError(t, err)
			}
			_, err = editor.ExecContext(ctx, `CREATE TABLE accounts(id bigint primary key,platform text,type text,credentials jsonb,extra jsonb,proxy_id bigint,deleted_at timestamptz,updated_at timestamptz);
 CREATE TABLE proxies(id bigint primary key,protocol text,host text,port integer,username text,password text,status text,deleted_at timestamptz);
 CREATE TABLE scheduler_outbox(event_type text,account_id bigint,group_id bigint,payload jsonb);
 INSERT INTO accounts VALUES(42,'grok','oauth','{"access_token":"at"}','{}',9,NULL,NOW());
 INSERT INTO proxies VALUES(9,'http','old.test',8080,'','','active',NULL)`)
			require.NoError(t, err)
			var writerPID int
			require.NoError(t, writer.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID))
			edit, err := editor.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() {
				if err := edit.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					t.Error(err)
				}
			}()
			_, err = edit.ExecContext(ctx, `UPDATE proxies SET host='new.test' WHERE id=9`)
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, writer)))
			repo := newAccountRepositoryWithSQL(client, writer, nil)
			type outcome struct {
				applied bool
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				id := int64(9)
				stamp := time.Now().UTC().Format(time.RFC3339Nano)
				ok, err := repo.UpdateGrokSubscriptionIfUnchanged(ctx, 42, map[string]any{"access_token": "at"}, &id, map[string]any{"tier": "free", "scope": "test", "requested_at": stamp}, &service.Proxy{ID: 9, Protocol: "http", Host: "old.test", Port: 8080, Status: "active"})
				done <- outcome{ok, err}
			}()
			// The editing connection proves the writer is blocked by its uncommitted edit.
			blocked := false
			for !blocked {
				select {
				case got := <-done:
					t.Fatalf("subscription completed before proxy edit resolved: applied=%v err=%v", got.applied, got.err)
				default:
				}
				require.NoError(t, edit.QueryRowContext(ctx, `SELECT pg_backend_pid() = ANY(pg_blocking_pids($1))`, writerPID).Scan(&blocked))
			}
			if commit {
				require.NoError(t, edit.Commit())
			} else {
				require.NoError(t, edit.Rollback())
			}
			select {
			case got := <-done:
				require.NoError(t, got.err)
				require.Equal(t, !commit, got.applied)
			case <-ctx.Done():
				t.Fatal("subscription did not finish after proxy edit resolved")
			}
			var observations, events int
			require.NoError(t, editor.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE extra ? 'grok_subscription_snapshot'`).Scan(&observations))
			require.NoError(t, editor.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox`).Scan(&events))
			if commit {
				require.Zero(t, observations)
				require.Zero(t, events)
			} else {
				require.Equal(t, 1, observations)
				require.Equal(t, 1, events)
			}
		})
	}
}
