package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture/capturetest"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestRequestCaptureWebSocketPoolLeaseProducesUpstreamLegs(t *testing.T) {
	m, e := requestcapture.New(capturetest.NewStore(), t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	require.NoError(t, e)
	defer m.Close()
	task, e := m.Create(t.Context(), requestcapture.CreateTask{TargetType: "account", TargetID: 901, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	capture := m.Begin(requestcapture.Meta{UserID: 27, Protocol: "websocket"})
	capture.ClientFrame([]byte(`{"type":"response.create","model":"fixture","input":"raw-client"}`))
	ctx := requestcapture.WithSession(t.Context(), capture)
	failure := []byte(`{"type":"response.failed","response":{"error":{"code":"synthetic_failure"},"access_token":"fixture-redacted"}}`)
	upstream := &openAIWSCaptureConn{events: [][]byte{failure}}
	lease := &openAIWSConnLease{conn: newOpenAIWSConn("capture-lease", 901, upstream, nil), accountID: 901}
	request := map[string]any{"type": "response.create", "model": "fixture", "input": "rewritten-upstream"}
	require.NoError(t, lease.WriteJSONWithContextTimeout(ctx, request, time.Second))
	body, e := lease.ReadMessageWithContextTimeout(ctx, time.Second)
	require.NoError(t, e)
	require.Equal(t, failure, body)
	capture.Frame("client_response", 0, body)
	require.Eventually(t, func() bool { rows, _ := m.Records(t.Context(), task.ID, "", true, 20, 0); return len(rows) == 1 }, 3*time.Second, 10*time.Millisecond)
	capture.Finish(101)
	rows, e := m.Records(t.Context(), task.ID, "", true, 20, 0)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	record, e := m.Record(t.Context(), task.ID, rows[0].ID)
	require.NoError(t, e)
	require.Len(t, record.Attempts, 1)
	require.EqualValues(t, 901, record.Attempts[0].AccountID)
	legs := map[string]string{}
	for _, p := range record.Parts {
		text, _, _, e := m.Preview(t.Context(), task.ID, record.ID, p.Name, 0)
		require.NoError(t, e)
		require.NotContains(t, text, "fixture-redacted")
		legs[p.Stage] += text
	}
	require.Contains(t, legs["client_request"], "raw-client")
	require.Contains(t, legs["upstream_request"], "rewritten-upstream")
	require.Contains(t, legs["upstream_response"], "synthetic_failure")
	require.Contains(t, legs["client_response"], "synthetic_failure")
}

func TestRequestCaptureWebSocketRealPassthroughProducer(t *testing.T) {
	m, e := requestcapture.New(capturetest.NewStore(), t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	require.NoError(t, e)
	defer m.Close()
	task, e := m.Create(t.Context(), requestcapture.CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "synthetic")
	require.NoError(t, e)
	capture := m.Begin(requestcapture.Meta{UserID: 27, RequestID: "synthetic-ws", Protocol: "websocket"})
	control, cancel := context.WithCancelCause(requestcapture.WithSession(context.Background(), capture))
	defer cancel(context.Canceled)
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	server, serverErr := startPassthroughLifecycleServer(t, control, svc, passthroughLifecycleAccount())
	defer server.Close()
	conn := dialPassthroughLifecycleClient(t, server)
	defer conn.CloseNow()
	request := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Contains(t, string(request), "response.create")
	failure := `{"type":"response.failed","response":{"id":"fixture-ws","error":{"code":"synthetic_failure","message":"fixture"},"access_token":"never-store"}}`
	upstream.Send(failure)
	payload, err := readPassthroughLifecycleFrame(t, conn, time.Second)
	require.NoError(t, err)
	require.Equal(t, failure, string(payload))
	require.Eventually(t, func() bool { rows, _ := m.Records(t.Context(), task.ID, "", true, 20, 0); return len(rows) == 1 }, 3*time.Second, 10*time.Millisecond)
	_ = conn.Close(coderws.StatusNormalClosure, "fixture done")
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not exit")
	}
	capture.Finish(101)
	rows, e := m.Records(t.Context(), task.ID, "", true, 20, 0)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	record, e := m.Record(t.Context(), task.ID, rows[0].ID)
	require.NoError(t, e)
	require.GreaterOrEqual(t, len(record.Attempts), 2)
	require.Equal(t, "websocket_handshake", record.Attempts[0].Phase)
	require.EqualValues(t, 901, record.Attempts[0].AccountID)
	stages := map[string]string{}
	for _, p := range record.Parts {
		text, _, _, e := m.Preview(t.Context(), task.ID, record.ID, p.Name, 0)
		require.NoError(t, e)
		require.NotContains(t, text, "never-store")
		stages[p.Stage] += text
	}
	for _, stage := range []string{"client_request", "upstream_request", "upstream_response", "client_response"} {
		require.NotEmpty(t, strings.TrimSpace(stages[stage]), stage)
	}
}
