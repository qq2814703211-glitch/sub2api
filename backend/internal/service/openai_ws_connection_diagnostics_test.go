package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type diagnosticWSRepo struct {
	UsageLogRepository
	enabled atomic.Bool
	records chan ConnectionTrace
}

func (r *diagnosticWSRepo) RecordConnectionTrace(v ConnectionTrace) {
	if r.enabled.Load() {
		r.records <- v
	}
}
func (r *diagnosticWSRepo) ConnectionDiagnosticsEnabled() bool { return r.enabled.Load() }

func TestConnectionDiagnosticsRealWSPassthroughPersistsEachTurnBeforeClose(t *testing.T) {
	ctx, cancel := context.WithCancel(requesttiming.With(context.Background(), requesttiming.New(time.Now(), 0)))
	defer cancel()
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	repo := &diagnosticWSRepo{records: make(chan ConnectionTrace, 4)}
	repo.enabled.Store(true)
	svc.usageLogRepo = repo
	server, done := startPassthroughHookRecordingServer(t, ctx, svc, passthroughLifecycleAccount(), &OpenAIWSIngressHooks{InitialRequestModel: "gpt-5.1"})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	first := `{"type":"response.completed","response":{"id":"resp_diag_1","model":"gpt-5.1","output":[{"content":"SECRET_BODY"}]}}`
	upstream.Send(first)
	event, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.JSONEq(t, first, string(event))
	take := func() ConnectionTrace {
		select {
		case v := <-repo.records:
			return v
		case <-time.After(2 * time.Second):
			t.Fatal("no independent turn trace while WS remains open")
			return ConnectionTrace{}
		}
	}
	one := take()
	require.Equal(t, "completed", one.Detail.Terminal)
	require.Equal(t, "completed", one.Detail.TerminalFlushed)
	require.Equal(t, int64(len(first)), one.Detail.DownstreamBytes)
	require.Positive(t, one.Detail.Attempts[0].RequestBytes)
	require.Equal(t, int64(len(first)), one.Detail.Attempts[0].ResponseBytes)
	require.NotEmpty(t, one.Detail.Attempts)
	require.Equal(t, "passthrough", one.Detail.Attempts[0].Route.Mode)
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":"SECRET_PROMPT"}`)))
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.incomplete","response":{"id":"resp_diag_2","status":"incomplete"}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	two := take()
	require.Equal(t, "incomplete", two.Detail.Terminal)
	require.Equal(t, 2, two.Detail.Turn)
	require.Equal(t, "websocket", two.Detail.Protocol)
	require.NotEqual(t, one.TraceID, two.TraceID)
	raw, err := json.Marshal([]ConnectionTrace{one, two})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET_")
	client.CloseNow()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WS forwarding did not stop")
	}
}

type diagnosticJSONWSConn struct{ *stagedPassthroughConn }

func (c *diagnosticJSONWSConn) WriteJSON(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, raw)
}

func TestConnectionDiagnosticsNativeAndHTTPBridgeTurns(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeHTTPBridge} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(requesttiming.With(context.Background(), requesttiming.New(time.Now(), 0)))
			defer cancel()
			cfg := passthroughLifecycleConfig()
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(cfg, upstream)
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			account.Extra["responses_websockets_v2_enabled"] = true
			repo := &diagnosticWSRepo{records: make(chan ConnectionTrace, 4)}
			repo.enabled.Store(true)
			svc.usageLogRepo = repo
			if mode == OpenAIWSIngressModeCtxPool {
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &diagnosticJSONWSConn{upstream}})
				svc.openaiWSPool = pool
				defer svc.CloseOpenAIWSPool()
				upstream.Send(`{"type":"response.completed","response":{"id":"native","status":"completed","model":"gpt-5.1"}}`)
			} else {
				svc.httpUpstream = &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"bridge\",\"status\":\"completed\",\"model\":\"gpt-5.1\"}}\n\n"))}}
			}
			server, done := startPassthroughHookRecordingServer(t, ctx, svc, account, nil)
			defer server.Close()
			client := dialPassthroughLifecycleClient(t, server)
			defer client.CloseNow()
			_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			select {
			case v := <-repo.records:
				require.Equal(t, "completed", v.Detail.Terminal)
				require.Equal(t, mode, v.Detail.Attempts[0].Route.Mode)
				require.Positive(t, v.Detail.DownstreamBytes)
			case <-time.After(3 * time.Second):
				t.Fatal("missing independent turn trace")
			}
			client.CloseNow()
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("forwarding did not close")
			}
		})
	}
}

func TestConnectionDiagnosticsWSHotDisableSuppressesFurtherTurns(t *testing.T) {
	ctx, cancel := context.WithCancel(requesttiming.With(context.Background(), requesttiming.New(time.Now(), 0)))
	defer cancel()
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	repo := &diagnosticWSRepo{records: make(chan ConnectionTrace, 4)}
	repo.enabled.Store(true)
	svc.usageLogRepo = repo
	server, done := startPassthroughHookRecordingServer(t, ctx, svc, passthroughLifecycleAccount(), nil)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	repo.enabled.Store(false)
	upstream.Send(`{"type":"response.completed","response":{"id":"disabled","status":"completed"}}`)
	_, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`)))
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"disabled-2","status":"completed"}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	client.CloseNow()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("forwarding did not stop")
	}
	require.Empty(t, repo.records)
}

func TestConnectionDiagnosticsWSCancelRecordsActiveTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(requesttiming.With(context.Background(), requesttiming.New(time.Now(), 0)))
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	repo := &diagnosticWSRepo{records: make(chan ConnectionTrace, 4)}
	repo.enabled.Store(true)
	svc.usageLogRepo = repo
	server, done := startPassthroughHookRecordingServer(t, ctx, svc, passthroughLifecycleAccount(), nil)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	client.CloseNow()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled WS did not stop")
	}
	select {
	case v := <-repo.records:
		require.NotEqual(t, "completed", v.Detail.Terminal)
	case <-time.After(time.Second):
		t.Fatal("missing canceled turn trace")
	}
}
