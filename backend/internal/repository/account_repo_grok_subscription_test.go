package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountRepositoryGrokSubscriptionAtomicGuard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	var updater service.GrokSubscriptionRepository = repo
	mock.ExpectQuery("SELECT COALESCE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow([]byte("null")))
	mock.ExpectExec("(?s)WITH updated AS.*a.credentials - 'subscription_tier'.*a.proxy_id IS NOT DISTINCT FROM.*grok_subscription_snapshot.*SELECT 1 FROM proxies.*INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(0, 1))
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	applied, err := updater.UpdateGrokSubscriptionIfUnchanged(context.Background(), 42, map[string]any{"access_token": "attempt"}, nil, map[string]any{"tier": "free", "requested_at": stamp, "scope": "scope"}, nil)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}
