//go:build unit

package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestConnectionDiagnosticsQueueDropVisibleWithoutBlocking(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &usageLogRepository{db: db, connectionQueue: make(chan service.ConnectionTrace, 1), connectionCancel: func() {}}
	repo.connectionEnabled.Store(true)
	repo.RecordConnectionTrace(service.ConnectionTrace{})
	repo.RecordConnectionTrace(service.ConnectionTrace{})
	require.Equal(t, uint64(1), repo.ConnectionDiagnosticsStats().Dropped)
	repo.connectionMu.Lock()
	repo.RecordConnectionTrace(service.ConnectionTrace{})
	repo.connectionMu.Unlock()
	require.Equal(t, uint64(2), repo.ConnectionDiagnosticsStats().Dropped)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectionDiagnosticsWorkerStopsAndRestartsWithoutStaleQueue(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &usageLogRepository{db: db}
	repo.SetConnectionDiagnosticsEnabled(true)
	mock.ExpectExec("DELETE FROM request_connection_traces").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO request_connection_traces").WillReturnResult(sqlmock.NewResult(1, 1))
	record := service.ConnectionTrace{TraceID: uuid.NewString(), StartedAt: time.Now(), Detail: service.ConnectionTrace{}.Detail}
	repo.RecordConnectionTrace(record)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mock.ExpectationsWereMet() == nil && repo.ConnectionDiagnosticsStats().QueueDepth == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, mock.ExpectationsWereMet())
	repo.SetConnectionDiagnosticsEnabled(false)
	require.False(t, repo.ConnectionDiagnosticsStats().WorkerRunning)
	require.Zero(t, repo.ConnectionDiagnosticsStats().QueueDepth)

	mock.ExpectExec("DELETE FROM request_connection_traces").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO request_connection_traces").WillReturnResult(sqlmock.NewResult(1, 1))
	repo.SetConnectionDiagnosticsEnabled(true)
	record.TraceID = uuid.NewString()
	repo.RecordConnectionTrace(record)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mock.ExpectationsWereMet() == nil && repo.ConnectionDiagnosticsStats().QueueDepth == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, mock.ExpectationsWereMet())
	repo.SetConnectionDiagnosticsEnabled(false)
	require.False(t, repo.ConnectionDiagnosticsStats().WorkerRunning)
}
