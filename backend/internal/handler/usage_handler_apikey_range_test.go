package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// keyUsageRepoStub answers the two aggregate calls the panel makes and records
// the windows it was asked for, so the test can tell "today" apart from "range"
// without depending on wall-clock time.
type keyUsageRepoStub struct {
	service.UsageLogRepository
	trend []usagestats.TrendDataPoint

	// summaryByWindow is keyed by the window length in whole days.
	summaryByWindow map[int]*usagestats.UsageStats

	summaryWindows []time.Duration
	trendCalled    bool
}

func (s *keyUsageRepoStub) GetUsageTrendWithFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
) ([]usagestats.TrendDataPoint, error) {
	s.trendCalled = true
	return s.trend, nil
}

func (s *keyUsageRepoStub) GetAPIKeyStatsAggregated(
	ctx context.Context,
	apiKeyID int64,
	startTime, endTime time.Time,
) (*usagestats.UsageStats, error) {
	days := int(endTime.Sub(startTime).Hours() / 24)
	s.summaryWindows = append(s.summaryWindows, endTime.Sub(startTime))
	if stats, ok := s.summaryByWindow[days]; ok {
		clone := *stats
		return &clone, nil
	}
	return &usagestats.UsageStats{}, nil
}

func newKeyUsageTestRouter(usageRepo *keyUsageRepoStub, apiKeyRepo *dailyUsageAPIKeyRepoStub, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(usageRepo, nil, nil, nil)
	apiKeySvc := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, nil)
	handler := NewUsageHandler(usageSvc, apiKeySvc, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		c.Next()
	})
	router.GET("/user/api-keys/:id/usage", handler.GetMyAPIKeyUsage)
	return router
}

type keyUsageHandlerResponse struct {
	Code int `json:"code"`
	Data struct {
		APIKeyID  int64                              `json:"api_key_id"`
		Days      int                                `json:"days"`
		StartDate string                             `json:"start_date"`
		EndDate   string                             `json:"end_date"`
		Today     usagestats.APIKeyUsageSummary      `json:"today"`
		Range     usagestats.APIKeyUsageSummary      `json:"range"`
		Items     []usagestats.APIKeyDailyUsagePoint `json:"items"`
	} `json:"data"`
}

func ownedKey() *dailyUsageAPIKeyRepoStub {
	return &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
		},
	}
}

func TestGetMyAPIKeyUsageRejectsCrossUserAccess(t *testing.T) {
	usageRepo := &keyUsageRepoStub{}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 99, Status: service.StatusAPIKeyActive},
		},
	}
	router := newKeyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage?days=30", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, usageRepo.summaryWindows, "no aggregate may run before ownership is proven")
	require.False(t, usageRepo.trendCalled)
}

func TestGetMyAPIKeyUsageRejectsInvalidDays(t *testing.T) {
	for _, path := range []string{
		"/user/api-keys/7/usage?days=0",
		"/user/api-keys/7/usage?days=91",
		"/user/api-keys/7/usage?days=abc",
	} {
		t.Run(path, func(t *testing.T) {
			usageRepo := &keyUsageRepoStub{}
			router := newKeyUsageTestRouter(usageRepo, ownedKey(), 42)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Empty(t, usageRepo.summaryWindows)
		})
	}
}

func TestGetMyAPIKeyUsageSeparatesTodayFromRange(t *testing.T) {
	usageRepo := &keyUsageRepoStub{
		summaryByWindow: map[int]*usagestats.UsageStats{
			1: {
				TotalRequests:            9,
				TotalInputTokens:         90,
				TotalOutputTokens:        45,
				TotalCacheReadTokens:     12,
				TotalCacheCreationTokens: 3,
				TotalTokens:              150,
				TotalCost:                1.5,
				TotalActualCost:          1.2,
			},
			30: {
				TotalRequests:            100,
				TotalInputTokens:         1000,
				TotalOutputTokens:        500,
				TotalCacheReadTokens:     100,
				TotalCacheCreationTokens: 20,
				TotalTokens:              1620,
				TotalCost:                15,
				TotalActualCost:          12,
			},
		},
		trend: []usagestats.TrendDataPoint{
			{Date: "2026-11-07", Requests: 9, InputTokens: 90, OutputTokens: 45,
				CacheCreationTokens: 3, CacheReadTokens: 12, TotalTokens: 150,
				Cost: 1.5, ActualCost: 1.2},
		},
	}
	router := newKeyUsageTestRouter(usageRepo, ownedKey(), 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage?days=30", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Two aggregates: the 30-day range and the single-day "today" window.
	require.Len(t, usageRepo.summaryWindows, 2)
	require.ElementsMatch(t,
		[]time.Duration{30 * 24 * time.Hour, 24 * time.Hour},
		usageRepo.summaryWindows,
		"the panel must issue one bounded range aggregate and one today aggregate")

	var got keyUsageHandlerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	require.Equal(t, int64(7), got.Data.APIKeyID)
	require.Equal(t, 30, got.Data.Days)

	require.Equal(t, int64(100), got.Data.Range.Requests)
	require.Equal(t, int64(1620), got.Data.Range.TotalTokens)
	require.InDelta(t, 12.0, got.Data.Range.ActualCost, 1e-9)

	require.Equal(t, int64(9), got.Data.Today.Requests)
	require.Equal(t, int64(150), got.Data.Today.TotalTokens)
	require.InDelta(t, 1.2, got.Data.Today.ActualCost, 1e-9)
	require.NotEmpty(t, got.Data.Today.Date, "today carries its own date so the UI can label it")

	require.Len(t, got.Data.Items, 1)
	require.Equal(t, "2026-11-07", got.Data.Items[0].Date)
}

func TestGetMyAPIKeyUsageDefaultsToThirtyDays(t *testing.T) {
	usageRepo := &keyUsageRepoStub{summaryByWindow: map[int]*usagestats.UsageStats{}}
	router := newKeyUsageTestRouter(usageRepo, ownedKey(), 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got keyUsageHandlerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 30, got.Data.Days)
	require.Contains(t, usageRepo.summaryWindows, 30*24*time.Hour)
}

func TestGetMyAPIKeyUsageUnknownKeyIsNotFound(t *testing.T) {
	usageRepo := &keyUsageRepoStub{}
	router := newKeyUsageTestRouter(usageRepo, ownedKey(), 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/999/usage", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Empty(t, usageRepo.summaryWindows)
}
