//go:build unit

package admin

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type keyQueueAdminStatsStub struct {
	service.AdminService
	userID int64
}

func (s *keyQueueAdminStatsStub) GetUserAPIKeys(_ context.Context, userID int64, _ int, _ int, _ string, _ string) ([]service.APIKey, int64, error) {
	s.userID = userID
	return []service.APIKey{{ID: 7, UserID: userID, ConcurrencyLimit: 1}}, 1, nil
}

func TestAPIKeyQueueAdminStatsCountsAndUnavailable(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer rdb.Close()
	cache := repository.NewConcurrencyCache(rdb, 1, 30)
	svc := service.NewConcurrencyService(cache)
	defer svc.StopAPIKeyQueue()
	require.NoError(t, rdb.ZAdd(context.Background(), "concurrency:api_key:7", redis.Z{Score: float64(time.Now().Unix()), Member: "holder"}).Err())
	stub := &keyQueueAdminStatsStub{}
	h := &UserHandler{adminService: stub, concurrencyService: svc}
	router := gin.New()
	router.GET("/users/:id/api-keys/concurrency", h.GetUserAPIKeyQueueStats)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/users/9/api-keys/concurrency", nil))
	require.Equal(t, 200, rec.Code)
	require.EqualValues(t, 9, stub.userID)
	require.Contains(t, rec.Body.String(), `"current_concurrency":1`)
	require.Contains(t, rec.Body.String(), `"current_waiting":0`)
	require.NoError(t, rdb.Close())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/users/9/api-keys/concurrency", nil))
	require.Equal(t, 503, rec.Code)
	require.NotContains(t, rec.Body.String(), `"current_waiting":0`)
}
