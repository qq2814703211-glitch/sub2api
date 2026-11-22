package keyusage

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDisabledRuntimeLeavesRequestAndSchemaUntouched(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := New(db)
	// No database expectation: an off module must not read settings, probe schema
	// or create a worker on any statistics request.
	stats, handled, err := r.TryRead(context.Background(), []int64{1}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.False(t, handled)
	require.Nil(t, stats)
	require.NoError(t, r.Step(context.Background()))
	status, err := r.Status(context.Background())
	require.NoError(t, err)
	require.False(t, status.Running)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConfigurationRejectsInvalidIntervalBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := New(db)
	require.Error(t, r.Configure(context.Background(), Config{Enabled: true, IntervalSeconds: -1}))
	require.NoError(t, mock.ExpectationsWereMet())
}
