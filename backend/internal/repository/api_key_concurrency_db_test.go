package repository

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAPIKeyConcurrencyDatabaseMigrationAndRepository(t *testing.T) {
	dsn := os.Getenv("KEY_QUEUE_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL endpoint not supplied")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.Eventually(t, func() bool { return db.PingContext(ctx) == nil }, 20*time.Second, 100*time.Millisecond)
	// Apply every baseline migration, then simulate data existing before 908.
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "908_api_key_concurrency_limit.sql"))
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	_, err = db.ExecContext(ctx, "ALTER TABLE api_keys DROP COLUMN concurrency_limit")
	require.NoError(t, err)
	var uid, id int64
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES ('key-queue@test.invalid','synthetic','user','active',100,0) RETURNING id").Scan(&uid))
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO api_keys(user_id,key,name,status) VALUES ($1,'sk-synthetic-existing','existing','active') RETURNING id", uid).Scan(&id))
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var limit int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT concurrency_limit FROM api_keys WHERE id=$1", id).Scan(&limit))
	require.Zero(t, limit)
	_, err = db.ExecContext(ctx, "UPDATE api_keys SET concurrency_limit=-1 WHERE id=$1", id)
	require.Error(t, err, "database constraint rejects negatives")
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAPIKeyRepositoryWithSQL(client, db)
	key := &service.APIKey{UserID: uid, Key: "sk-synthetic-new", Name: "new", Status: service.StatusActive, ConcurrencyLimit: 3}
	require.NoError(t, repo.Create(ctx, key))
	read, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, 3, read.ConcurrencyLimit)
	require.Zero(t, read.User.Concurrency)
	key.ConcurrencyLimit = 0
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{ConcurrencyLimit: true}))
	read, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Zero(t, read.ConcurrencyLimit)
	keys, _, err := repo.ListByUserID(ctx, uid, pagination.PaginationParams{Page: 1, PageSize: 20}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT concurrency_limit FROM api_keys WHERE id=$1", key.ID).Scan(&limit))
	require.Zero(t, limit)
}
