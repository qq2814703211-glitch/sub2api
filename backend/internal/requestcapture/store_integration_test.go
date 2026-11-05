//go:build integration

package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

import (
	"database/sql"
	_ "github.com/lib/pq"
	"os"
	"path/filepath"
)

func captureDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("REQUEST_CAPTURE_TEST_DSN")
	if dsn == "" {
		t.Skip("REQUEST_CAPTURE_TEST_DSN required")
	}
	db, e := sql.Open("postgres", dsn)
	require.NoError(t, e)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// Dedicated test instance only. No shared database or production schema.
	migration, e := os.ReadFile(filepath.Join("..", "..", "migrations", "910_request_captures.sql"))
	require.NoError(t, e)
	_, e = db.Exec(string(migration))
	require.NoError(t, e)
	return db
}

func TestRequestCaptureSQLLifecycleFailureRecoveryAndCleanup(t *testing.T) {
	db := captureDB(t)
	store := &SQLStore{DB: db}
	dir := t.TempDir()
	cfg := Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1}
	m, e := New(store, dir, cfg)
	require.NoError(t, e)
	task, e := m.Create(t.Context(), CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	instance := m.InstanceID()
	session := m.Begin(Meta{UserID: 27, RequestID: "fixture-db-failure"})
	require.NotNil(t, session)
	session.ClientRequest([]byte(`{"input":"database-lifecycle-fixture","access_token":"fixture-redacted"}`), "application/json", nil)
	session.Finish(503)
	drain(t, m)
	success := m.Begin(Meta{UserID: 27, RequestID: "fixture-db-success"})
	require.NotNil(t, success)
	success.ClientRequest([]byte(`{"input":"success-must-disappear"}`), "application/json", nil)
	success.Finish(200)
	drain(t, m)
	rows, e := m.Records(t.Context(), task.ID, "", true, 20, 0)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	recordID := rows[0].ID
	require.NoError(t, m.Stop(t.Context(), task.ID))
	m.Close()
	m, e = New(store, dir, cfg)
	require.NoError(t, e)
	defer m.Close()
	require.Equal(t, instance, m.InstanceID())
	record, e := m.Record(t.Context(), task.ID, recordID)
	require.NoError(t, e)
	require.NotEmpty(t, record.Parts)
	text, _, _, e := m.Preview(t.Context(), task.ID, record.ID, record.Parts[0].Name, 0)
	require.NoError(t, e)
	require.Contains(t, text, "database-lifecycle-fixture")
	require.NotContains(t, text, "fixture-redacted")
	var archive bytes.Buffer
	require.NoError(t, m.Export(t.Context(), &archive, task.ID, ""))
	require.Positive(t, archive.Len())
	rows, e = store.Records(t.Context(), task.ID, "fixture-db-success", false, 20, 0)
	require.NoError(t, e)
	require.Empty(t, rows)
	stored, e := store.Task(t.Context(), instance, task.ID)
	require.NoError(t, e)
	old := time.Now().Add(-48 * time.Hour)
	stored.EndedAt = &old
	require.NoError(t, store.SaveTask(t.Context(), stored))
	m.cleanExpired()
	_, e = store.Task(t.Context(), instance, task.ID)
	require.ErrorIs(t, e, sql.ErrNoRows)
	rows, e = store.Records(t.Context(), task.ID, "", false, 20, 0)
	require.NoError(t, e)
	require.Empty(t, rows)
	entries, e := os.ReadDir(dir)
	require.NoError(t, e)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), task.ID))
	}
}

func TestRequestCaptureMigrationReplayPreservesExistingMetadata(t *testing.T) {
	db := captureDB(t)
	store := &SQLStore{DB: db}
	ctx := t.Context()
	task := &Task{ID: uuid.NewString(), InstanceID: uuid.NewString(), CreatedAt: time.Now(), Status: "stopped"}
	require.NoError(t, store.SaveTask(ctx, task))
	t.Cleanup(func() { _ = store.DeleteTask(ctx, task.InstanceID, task.ID) })
	migration, e := os.ReadFile(filepath.Join("..", "..", "migrations", "910_request_captures.sql"))
	require.NoError(t, e)
	_, e = db.Exec(string(migration))
	require.NoError(t, e)
	actual, e := store.Task(ctx, task.InstanceID, task.ID)
	require.NoError(t, e)
	require.Equal(t, task.ID, actual.ID)
	_, e = store.Task(ctx, "foreign-instance", task.ID)
	require.ErrorIs(t, e, sql.ErrNoRows)
}
func requestCaptureSQLStoreFixture(t *testing.T) (*SQLStore, *Task) {
	t.Helper()
	store := &SQLStore{DB: captureDB(t)}
	now := time.Now().UTC()
	task := &Task{
		ID: uuid.NewString(), InstanceID: uuid.NewString(), CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), Status: "active", TargetType: "user", TargetID: 1,
	}
	require.NoError(t, store.SaveTask(context.Background(), task))
	t.Cleanup(func() {
		require.NoError(t, store.DeleteTask(context.Background(), task.InstanceID, task.ID))
	})
	return store, task
}

func requestCaptureSQLStoreRecord(task *Task, id string) *Record {
	return &Record{
		ID: id, TaskID: task.ID, InstanceID: task.InstanceID, CreatedAt: time.Now().UTC(),
		Meta: Meta{
			RequestID: "request-" + id, ClientRequestID: "client-" + id,
			SessionID: "session-" + id, ThreadID: "thread-" + id, UserID: 1,
			Method: "POST", Path: "/v1/messages", Protocol: "anthropic",
		},
		Parts: []Part{{Name: "synthetic-metadata", Stage: "client-request", Bytes: 1}},
		Usage: map[string]int64{"input_tokens": 1},
	}
}

func requireCaptureSQLStoreIDs(t *testing.T, store *SQLStore, task, filter string, errorsOnly bool, ids ...string) {
	t.Helper()
	rows, err := store.Records(context.Background(), task, filter, errorsOnly, 100, 0)
	require.NoError(t, err, "filter %q must not fail because another row has non-array attempts", filter)
	actual := make([]string, 0, len(rows))
	for _, row := range rows {
		actual = append(actual, row.ID)
		require.Equal(t, task, row.TaskID, "filtered metadata must remain task-scoped")
		require.Nil(t, row.Parts, "record lists must not return captured part details")
		require.Nil(t, row.Attempts, "record lists must not return upstream attempt details")
		require.Nil(t, row.Usage, "record lists must retain their metadata-only projection")
	}
	require.ElementsMatch(t, ids, actual)
}

func TestRequestCaptureSQLStoreRecordsFiltersHandleAttemptShapes(t *testing.T) {
	store, task := requestCaptureSQLStoreFixture(t)
	_, otherTask := requestCaptureSQLStoreFixture(t)
	ctx := context.Background()
	type attemptShape struct {
		name     string
		missing  bool
		attempts any
		upstream bool
	}
	shapes := []attemptShape{
		{name: "missing", missing: true},
		{name: "null", attempts: nil},
		{name: "object", attempts: map[string]any{"upstream_request_id": "not-an-array"}},
		{name: "string", attempts: "not-an-array"},
		{name: "number", attempts: 42},
		{name: "boolean", attempts: true},
		{name: "empty-array", attempts: []any{}},
		{name: "array", upstream: true},
		{name: "mixed-array", upstream: true},
	}
	records := make([]*Record, 0, len(shapes))
	for _, shape := range shapes {
		r := requestCaptureSQLStoreRecord(task, task.ID+"-"+shape.name)
		require.NoError(t, store.SaveRecord(ctx, r))
		if shape.missing {
			_, err := store.DB.ExecContext(ctx, `UPDATE request_capture_records SET data=data-'attempts' WHERE id=$1`, r.ID)
			require.NoError(t, err)
		} else {
			value := shape.attempts
			if shape.upstream {
				value = []any{map[string]any{"upstream_request_id": "upstream-" + r.ID}}
				if shape.name == "mixed-array" {
					value = []any{nil, 42, "ignored", map[string]any{}, map[string]any{"upstream_request_id": "upstream-" + r.ID}}
				}
			}
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			_, err = store.DB.ExecContext(ctx, `UPDATE request_capture_records SET data=jsonb_set(data,'{attempts}',$2::jsonb) WHERE id=$1`, r.ID, string(encoded))
			require.NoError(t, err)
		}
		records = append(records, r)
	}

	// A different task deliberately shares every searchable identity, including
	// an upstream ID. No OR branch may escape the outer task_id boundary.
	foreign := requestCaptureSQLStoreRecord(otherTask, otherTask.ID+"-shared-identities")
	foreign.Meta = records[0].Meta
	foreign.Attempts = []Attempt{{UpstreamRequestID: "upstream-" + records[7].ID}}
	require.NoError(t, store.SaveRecord(ctx, foreign))
	foreignOnly := requestCaptureSQLStoreRecord(otherTask, otherTask.ID+"-foreign-only")
	foreignOnly.Attempts = []Attempt{{UpstreamRequestID: "upstream-" + foreignOnly.ID}}
	require.NoError(t, store.SaveRecord(ctx, foreignOnly))

	for i, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			r := records[i]
			for _, filter := range []string{r.RequestID, r.ClientRequestID, r.SessionID, r.ThreadID} {
				requireCaptureSQLStoreIDs(t, store, task.ID, filter, false, r.ID)
			}
			if shape.upstream {
				requireCaptureSQLStoreIDs(t, store, task.ID, "upstream-"+r.ID, false, r.ID)
			}
		})
	}
	for _, filter := range []string{records[0].RequestID, records[0].ClientRequestID, records[0].SessionID, records[0].ThreadID, "upstream-" + records[7].ID} {
		requireCaptureSQLStoreIDs(t, store, otherTask.ID, filter, false, foreign.ID)
	}
	for _, filter := range []string{foreignOnly.RequestID, foreignOnly.ClientRequestID, foreignOnly.SessionID, foreignOnly.ThreadID, "upstream-" + foreignOnly.ID, "unknown-request-id"} {
		requireCaptureSQLStoreIDs(t, store, task.ID, filter, false)
	}
	ids := make([]string, 0, len(records))
	for _, row := range records {
		ids = append(ids, row.ID)
	}
	requireCaptureSQLStoreIDs(t, store, task.ID, "", false, ids...)
}

func TestRequestCaptureSQLStoreRecordsErrorsOnlyWithNilAttempts(t *testing.T) {
	store, task := requestCaptureSQLStoreFixture(t)
	ctx := context.Background()
	finished := time.Now().UTC()
	completedError := requestCaptureSQLStoreRecord(task, task.ID+"-completed-error")
	completedError.CreatedAt = finished.Add(-3 * time.Second)
	completedError.IsError, completedError.FinishedAt = true, &finished
	// SaveRecord itself must reproduce the production shape: an early/attempt-
	// less request's nil slice is stored as JSON null, not a missing SQL value.
	require.NoError(t, store.SaveRecord(ctx, completedError))
	var attemptsType string
	require.NoError(t, store.DB.QueryRowContext(ctx, `SELECT jsonb_typeof(data->'attempts') FROM request_capture_records WHERE id=$1`, completedError.ID).Scan(&attemptsType))
	require.Equal(t, "null", attemptsType)

	activeError := requestCaptureSQLStoreRecord(task, task.ID+"-active-error")
	activeError.CreatedAt = finished.Add(-2 * time.Second)
	activeError.IsError = true
	require.NoError(t, store.SaveRecord(ctx, activeError))
	success := requestCaptureSQLStoreRecord(task, task.ID+"-success")
	success.CreatedAt = finished.Add(-time.Second)
	success.FinishedAt = &finished
	require.NoError(t, store.SaveRecord(ctx, success))
	upstreamError := requestCaptureSQLStoreRecord(task, task.ID+"-upstream-error")
	upstreamError.CreatedAt = finished
	upstreamError.IsError, upstreamError.FinishedAt = true, &finished
	upstreamError.Attempts = []Attempt{{UpstreamRequestID: "upstream-" + upstreamError.ID}}
	require.NoError(t, store.SaveRecord(ctx, upstreamError))

	requireCaptureSQLStoreIDs(t, store, task.ID, "", true, completedError.ID, upstreamError.ID)
	for _, filter := range []string{completedError.RequestID, completedError.ClientRequestID, completedError.SessionID, completedError.ThreadID} {
		requireCaptureSQLStoreIDs(t, store, task.ID, filter, true, completedError.ID)
	}
	requireCaptureSQLStoreIDs(t, store, task.ID, "upstream-"+upstreamError.ID, true, upstreamError.ID)
	for _, r := range []*Record{activeError, success} {
		for _, filter := range []string{r.RequestID, r.ClientRequestID, r.SessionID, r.ThreadID} {
			requireCaptureSQLStoreIDs(t, store, task.ID, filter, true)
			requireCaptureSQLStoreIDs(t, store, task.ID, filter, false, r.ID)
		}
	}
	requireCaptureSQLStoreIDs(t, store, task.ID, "unknown-request-id", true)

	rows, err := store.Records(ctx, task.ID, "", true, 1, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, upstreamError.ID, rows[0].ID)
	rows, err = store.Records(ctx, task.ID, "", true, 1, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, completedError.ID, rows[0].ID)
}
