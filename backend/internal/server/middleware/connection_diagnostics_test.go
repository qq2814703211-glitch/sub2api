package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/poison"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type traceRecorder struct{ records []service.ConnectionTrace }

func (r *traceRecorder) RecordConnectionTrace(v service.ConnectionTrace) {
	r.records = append(r.records, v)
}

func TestConnectionDiagnosticsHTTPProtocolAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body, terminal string
		cancel, panic        bool
		status               int
	}{
		{name: "zero usage SSE failure", body: "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"SECRET_PROMPT\"}}}\n\n", terminal: "failed", status: 200},
		{name: "completed", body: "data: {\"type\":\"response.completed\"}\n\n", terminal: "completed", status: 200},
		{name: "incomplete", body: "data: {\"type\":\"response.created\"}\n\n", terminal: "incomplete", status: 200},
		{name: "client cancellation", cancel: true, status: 200},
		{name: "recovered panic", panic: true, status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetConnectionDiagnosticsEnabled(true)
			t.Cleanup(func() { SetConnectionDiagnosticsEnabled(false) })
			sink := &traceRecorder{}
			engine := gin.New()
			engine.Use(Recovery(), poison.Middleware(), RequestTiming(sink))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			engine.POST("/v1/responses", func(c *gin.Context) {
				if tc.panic {
					panic("synthetic")
				}
				c.Header("Content-Type", "text/event-stream")
				if tc.cancel {
					cancel()
				}
				_, _ = c.Writer.WriteString(tc.body)
				c.Writer.Flush()
			})
			rr := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader("SECRET_REQUEST")).WithContext(ctx)
			engine.ServeHTTP(rr, req)
			require.Equal(t, tc.status, rr.Code)
			require.Len(t, sink.records, 1)
			v := sink.records[0]
			require.Equal(t, tc.status, v.Detail.Status)
			if tc.panic {
				require.Equal(t, "failed", v.Detail.Outcome)
			} else {
				require.Equal(t, tc.terminal, v.Detail.Terminal)
				require.Equal(t, tc.body, rr.Body.String())
			}
			require.Equal(t, tc.cancel, v.Detail.Canceled)
			raw, err := json.Marshal(v)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "SECRET_")
		})
	}
}

func TestConnectionDiagnosticsPoisonTailFinalizedBeforeSnapshot(t *testing.T) {
	SetConnectionDiagnosticsEnabled(true)
	defer SetConnectionDiagnosticsEnabled(false)
	st := poison.NewStore(func() (map[string]string, error) { return map[string]string{"poison_guard_enabled": "true"}, nil })
	require.NoError(t, st.Reload())
	sink := &traceRecorder{}
	engine := gin.New()
	engine.Use(Recovery(), poison.Middleware(), RequestTiming(sink))
	body := `{"ok":"a safe tail"}`
	engine.POST("/v1/responses", func(c *gin.Context) {
		require.True(t, poison.Attach(c, st.Current(), poison.AccountInfo{Name: "防投毒test", BaseURL: "https://poison.example", HasProxy: true, AccountID: 1}))
		c.Data(200, "application/json", []byte(body))
	})
	rr := httptest.NewRecorder()
	engine.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/responses", nil))
	require.Equal(t, body, rr.Body.String())
	require.Len(t, sink.records, 1)
	require.Equal(t, int64(len(body)), sink.records[0].Detail.DownstreamBytes)
	require.Equal(t, "completed", sink.records[0].Detail.Terminal)
}

func TestConnectionDiagnosticsDisabledLeavesIOUntouched(t *testing.T) {
	SetConnectionDiagnosticsEnabled(false)
	sink := &traceRecorder{}
	engine := gin.New()
	engine.Use(RequestTiming(sink))
	engine.POST("/v1/responses", func(c *gin.Context) {
		require.Nil(t, requesttiming.From(c.Request.Context()))
		c.String(200, "unchanged")
	})
	rr := httptest.NewRecorder()
	engine.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/responses", nil))
	require.Equal(t, "unchanged", rr.Body.String())
	require.Empty(t, sink.records)
}

type failedTraceWriter struct{ gin.ResponseWriter }

func (w failedTraceWriter) Write([]byte) (int, error) { return 0, errors.New("SECRET_WRITE_ERROR") }
func TestConnectionDiagnosticsWriterPreservesFailureAndUnwrap(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	col := requesttiming.New(time.Now(), 0)
	inner := failedTraceWriter{c.Writer}
	writer := &timingWriter{ResponseWriter: inner, collector: col}
	n, err := writer.Write([]byte("SECRET_BODY"))
	require.Zero(t, n)
	require.Error(t, err)
	require.Equal(t, http.ResponseWriter(inner), writer.Unwrap())
	var snap requesttiming.Snapshot
	col.WhenFinished(func(v requesttiming.Snapshot) { snap = v })
	col.Finish(200, false)
	require.True(t, snap.DownstreamError)
	require.Zero(t, snap.DownstreamBytes)
}

func TestConnectionDiagnosticsWebSocketTurnIsIndependentFromHandshakeStatus(t *testing.T) {
	SetConnectionDiagnosticsEnabled(true)
	defer SetConnectionDiagnosticsEnabled(false)
	sink := &traceRecorder{}
	engine := gin.New()
	engine.Use(RequestTiming(sink))
	engine.GET("/v1/responses", func(c *gin.Context) {
		finish := requesttiming.StartWSAttempt(c.Request.Context(), 42, transportdiag.Route{
			Downstream: "websocket", Upstream: "websocket", Mode: "ingress", Endpoint: "responses",
		}, "websocket_turn")
		finish(http.StatusBadGateway, "failed", errors.New("upstream turn failed"))
		c.Status(http.StatusSwitchingProtocols)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rr := httptest.NewRecorder()
	engine.ServeHTTP(rr, req)
	require.Equal(t, http.StatusSwitchingProtocols, rr.Code)
	require.Len(t, sink.records, 1)
	require.Len(t, sink.records[0].Detail.Attempts, 1)
	require.Equal(t, "failed", sink.records[0].Detail.Attempts[0].Terminal)
	require.Equal(t, http.StatusBadGateway, sink.records[0].Detail.Attempts[0].Status)
}
