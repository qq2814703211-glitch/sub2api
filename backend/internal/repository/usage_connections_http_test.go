//go:build unit

package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/stretchr/testify/require"
)

func TestConnectionDiagnosticsRealHTTPProducerAttemptAndTLS(t *testing.T) {
	body := "data: {\"type\":\"response.failed\"}\n\n"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	col := requesttiming.New(time.Now(), 0)
	ctx := requesttiming.With(context.Background(), col)
	req, err := http.NewRequestWithContext(ctx, "POST", upstream.URL+"/v1/responses?token=SECRET", nil)
	require.NoError(t, err)
	req, _ = requesttiming.StartAttempt(req, 42, 0)
	resp, err := doUpstreamRequest(upstream.Client(), req)
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(got))
	require.NoError(t, resp.Body.Close())
	var snap requesttiming.Snapshot
	col.WhenFinished(func(v requesttiming.Snapshot) { snap = v })
	col.Finish(200, false)
	require.Len(t, snap.Attempts, 2)
	a := snap.Attempts[1]
	require.Equal(t, "http", a.Kind)
	require.Equal(t, int64(42), a.AccountID)
	require.Equal(t, 200, a.Status)
	require.True(t, a.BodyEOF)
	require.Equal(t, int64(len(body)), a.ResponseBytes)
	require.Contains(t, a.Events, "tcp_start")
	require.Contains(t, a.Events, "tls_start")
	require.Contains(t, a.Events, "first_byte")
}
