//go:build unit

package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestConnectionDiagnosticsPostgresRoundTripFiltersAndRetention(t *testing.T) {
	dsn := os.Getenv("DIAGNOSTICS_TEST_DSN")
	if dsn == "" {
		t.Skip("requires lane-owned temporary PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	migration, err := os.ReadFile("../../migrations/909_request_connection_traces.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	// Minimal neighbouring schemas exercise the real query and joins without
	// importing production state or a whole application fixture.
	_, err = db.ExecContext(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT,username TEXT);
 CREATE TABLE api_keys(id BIGINT PRIMARY KEY,name TEXT);
 CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT);
 CREATE TABLE ops_error_logs(id BIGINT,created_at TIMESTAMPTZ,request_id TEXT,client_request_id TEXT,user_id BIGINT,api_key_id BIGINT,account_id BIGINT,group_id BIGINT,model TEXT,request_path TEXT,client_ip INET,user_agent TEXT,stream BOOLEAN,duration_ms BIGINT,status_code INT,error_type TEXT);`)
	require.NoError(t, err)
	repo := &usageLogRepository{db: db, sql: db}
	col := requesttiming.New(time.Now(), 0)
	var detail requesttiming.Snapshot
	col.WhenFinished(func(v requesttiming.Snapshot) { detail = v })
	requesttiming.Output(requesttiming.With(ctx, col), false, false, "failed")
	col.Finish(200, false)
	v := service.ConnectionTrace{TraceID: detail.TraceID, StartedAt: detail.StartedAt, RequestID: "zero-usage-failure", Path: "/v1/responses", Source: "application", Method: "POST", Detail: detail}
	require.NoError(t, repo.writeConnectionTrace(ctx, v))
	require.NoError(t, repo.writeConnectionTrace(ctx, v))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM request_connection_traces").Scan(&count))
	require.Equal(t, 1, count)
	f := service.ConnectionTraceFilter{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour), State: "failed", RequestID: v.RequestID, Page: 1, PageSize: 20}
	got, err := repo.ListConnectionTraces(ctx, f)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, "failed", got.Items[0].State)
	require.Equal(t, 200, got.Items[0].Detail.Status)
	f.State = "completed"
	got, err = repo.ListConnectionTraces(ctx, f)
	require.NoError(t, err)
	require.Empty(t, got.Items)
	old := v
	old.TraceID = uuid.NewString()
	old.RequestID = "expired"
	old.StartedAt = time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, repo.writeConnectionTrace(ctx, old))
	repo.cleanupConnectionTraces(ctx)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM request_connection_traces WHERE trace_id=$1", old.TraceID).Scan(&count))
	require.Zero(t, count)
	var raw string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT detail::text FROM request_connection_traces WHERE trace_id=$1", v.TraceID).Scan(&raw))
	require.False(t, strings.Contains(raw, "SECRET"))
}
