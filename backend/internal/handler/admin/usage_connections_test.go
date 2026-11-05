//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type diagnosticsAdminRepo struct {
	service.UsageLogRepository
	enabled bool
	filter  service.ConnectionTraceFilter
}

func (r *diagnosticsAdminRepo) SetConnectionDiagnosticsEnabled(v bool) { r.enabled = v }
func (r *diagnosticsAdminRepo) ConnectionDiagnosticsStats() service.ConnectionDiagnosticsStats {
	return service.ConnectionDiagnosticsStats{Dropped: 7}
}
func (r *diagnosticsAdminRepo) ListConnectionTraces(_ context.Context, f service.ConnectionTraceFilter) (*service.ConnectionTraceList, error) {
	r.filter = f
	return &service.ConnectionTraceList{Items: []service.ConnectionTrace{}, Page: f.Page, PageSize: f.PageSize, RetentionDays: 30}, nil
}
func TestConnectionDiagnosticsAdminSettingsAndQueryValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer middleware.SetConnectionDiagnosticsEnabled(false)
	repo := &diagnosticsAdminRepo{}
	h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil)
	r := gin.New()
	r.GET("/settings", h.ConnectionDiagnosticsSettings)
	r.PUT("/settings", h.ConnectionDiagnosticsSettings)
	r.GET("/connections", h.Connections)
	for _, v := range []bool{true, false} {
		body := `{"enabled":false}`
		if v {
			body = `{"enabled":true}`
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rr, req)
		require.Equal(t, 200, rr.Code)
		require.Equal(t, v, repo.enabled)
		require.Equal(t, v, middleware.ConnectionDiagnosticsEnabled())
		require.Contains(t, rr.Body.String(), `"body_capture":false`)
		require.Contains(t, rr.Body.String(), `"dropped":7`)
	}
	for _, query := range []string{"state=not-a-state", "page_size=500", "user_id=-1", "client_ip=invalid", "source=invalid"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/connections?"+query, nil))
		require.Equal(t, 400, rr.Code)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest("GET", "/connections?state=failed&request_id=test&account_id=42", nil))
	require.Equal(t, 200, rr.Code)
	require.Equal(t, int64(42), repo.filter.AccountID)
	require.Equal(t, "failed", repo.filter.State)
}
