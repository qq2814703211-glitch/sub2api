//go:build unit

package handler

import (
	"context"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type keyQueueOwnershipRepo struct{ service.APIKeyRepository }

func (keyQueueOwnershipRepo) VerifyOwnership(_ context.Context, userID int64, ids []int64) ([]int64, error) {
	if userID == 9 && len(ids) == 1 && ids[0] == 7 {
		return ids, nil
	}
	return nil, nil
}
func TestAPIKeyQueueStatsOwnershipAndFailure(t *testing.T) {
	svc, _ := protocolAdmissionCache(t)
	keys := service.NewAPIKeyService(keyQueueOwnershipRepo{}, nil, nil, nil, nil, nil, nil)
	keys.SetConcurrencyService(svc)
	handler := NewAPIKeyHandler(keys)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	router.GET("/keys/concurrency", handler.GetConcurrencyQueue)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/keys/concurrency?ids=7", nil))
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "current_waiting")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/keys/concurrency?ids=8", nil))
	require.Equal(t, 404, rec.Code)
	keys.SetConcurrencyService(nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/keys/concurrency?ids=7", nil))
	require.Equal(t, 503, rec.Code)
}
