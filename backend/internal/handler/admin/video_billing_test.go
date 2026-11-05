//go:build unit

package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type videoAdminRepo struct {
	service.VideoBillingJobRepository
	retryID  string
	retryErr error
}

func (r *videoAdminRepo) List(context.Context, string, string, int) ([]*service.VideoBillingJob, error) {
	return []*service.VideoBillingJob{{ID: "job", TaskID: "task", State: "reconciliation", LastError: "completion_usage_missing",
		Snapshot: service.VideoBillingSnapshot{Pending: service.GrokVideoPendingBilling{Model: "video"}, Unit: service.VideoBillingIntent{Command: service.UsageBillingCommand{RequestPayloadHash: "private-hash"}}}}}, nil
}
func (r *videoAdminRepo) Retry(_ context.Context, id string) error { r.retryID = id; return r.retryErr }
func TestDurableVideoAdminVisibleStatusAndRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &videoAdminRepo{}
	svc := &service.OpenAIGatewayService{}
	svc.SetVideoBillingJobs(repo)
	h := NewOpsHandler(nil)
	h.SetVideoBillingService(svc)
	router := gin.New()
	router.GET("/jobs", h.ListVideoBillingJobs)
	router.POST("/jobs/:id/retry", h.RetryVideoBillingJob)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs?state=reconciliation", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "completion_usage_missing")
	require.Contains(t, w.Body.String(), "reconciliation")
	require.NotContains(t, w.Body.String(), "private-hash")
	require.NotContains(t, w.Body.String(), "snapshot")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/jobs/job/retry", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "job", repo.retryID)
	repo.retryErr = errors.New("leased")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/jobs/job/retry", nil))
	require.Equal(t, 409, w.Code)
}
func TestDurableVideoAdminUnavailableAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOpsHandler(nil)
	router := gin.New()
	router.GET("/jobs", h.ListVideoBillingJobs)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	require.Equal(t, 503, w.Code)
	svc := &service.OpenAIGatewayService{}
	svc.SetVideoBillingJobs(&videoAdminRepo{})
	h.SetVideoBillingService(svc)
	router.PUT("/worker", h.SetVideoBillingWorker)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/worker", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
}
