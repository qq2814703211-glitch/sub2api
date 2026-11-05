package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type availableGroupsUserRepoStub struct{ service.UserRepository }

func (availableGroupsUserRepoStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id}, nil
}

type availableGroupsSubRepoStub struct {
	service.UserSubscriptionRepository
}

func (availableGroupsSubRepoStub) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type availableGroupsRateRepoStub struct {
	service.UserGroupRateRepository
	rates map[int64]float64
}

func (r availableGroupsRateRepoStub) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return r.rates, nil
}

type availableGroupsRepoStub struct {
	service.GroupRepository
	groups   []service.Group
	history  []service.GroupRateChange
	histErr  error
	gotSince time.Time
}

func (r *availableGroupsRepoStub) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

func (r *availableGroupsRepoStub) ListGroupRateChangesSince(_ context.Context, _ []int64, since time.Time) ([]service.GroupRateChange, error) {
	r.gotSince = since
	return r.history, r.histErr
}

// availableGroupsRepoWithoutHistory 只实现基础 GroupRepository，没有历史能力。
type availableGroupsRepoWithoutHistory struct{ service.GroupRepository }

func (r *availableGroupsRepoWithoutHistory) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{
		{ID: 2, Name: "fresh", Platform: service.PlatformAnthropic, Status: service.StatusActive,
			RateMultiplier: 0.05, CreatedAt: time.Now().Add(-2 * time.Hour)},
	}, nil
}

// availableGroupsFixture: 分组 1 是旧分组且 1 小时前调过价，分组 2 是两小时前
// 上线且未调价，分组 3 是未变动的旧分组。
func availableGroupsFixture(now time.Time) (*availableGroupsRepoStub, time.Time) {
	newCreatedAt := now.Add(-2 * time.Hour)
	return &availableGroupsRepoStub{
		groups: []service.Group{
			{ID: 1, Name: "repriced", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 0.08, CreatedAt: now.Add(-30 * 24 * time.Hour)},
			{ID: 2, Name: "fresh", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 0.05, CreatedAt: newCreatedAt},
			{ID: 3, Name: "quiet", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1, CreatedAt: now.Add(-30 * 24 * time.Hour)},
		},
		history: []service.GroupRateChange{
			{ID: 1, GroupID: 1, OldRate: 0.06, NewRate: 0.08, ChangedAt: now.Add(-time.Hour)},
		},
	}, newCreatedAt
}

func getAvailableGroupsPayload(t *testing.T, repo service.GroupRepository, rates map[int64]float64, want int) []map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var rateRepo service.UserGroupRateRepository
	if rates != nil {
		rateRepo = availableGroupsRateRepoStub{rates: rates}
	}
	svc := service.NewAPIKeyService(nil, availableGroupsUserRepoStub{}, repo, availableGroupsSubRepoStub{}, rateRepo, nil, nil)
	h := NewAPIKeyHandler(svc)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9})
		c.Next()
	})
	router.GET("/groups/available", h.GetAvailableGroups)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups/available", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code int              `json:"code"`
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Zero(t, body.Code)
	require.Len(t, body.Data, want)
	return body.Data
}

func TestGetAvailableGroupsAddsRateChangeAndNewHints(t *testing.T) {
	repo, _ := availableGroupsFixture(time.Now())
	data := getAvailableGroupsPayload(t, repo, nil, 3)

	require.WithinDuration(t, time.Now().Add(-service.GroupRateChangeWindow), repo.gotSince, 5*time.Second,
		"按 48h 窗口读取历史")

	repriced := data[0]
	change, ok := repriced["recent_rate_change"].(map[string]any)
	require.True(t, ok, "调过价的分组携带 recent_rate_change")
	require.Equal(t, "up", change["direction"])
	require.InDelta(t, 0.02, change["delta"], 1e-9)
	require.InDelta(t, 0.06, change["previous_rate"], 1e-9)
	require.Contains(t, change, "changed_at")
	require.NotContains(t, repriced, "is_new")

	fresh := data[1]
	require.Equal(t, true, fresh["is_new"])
	require.Contains(t, fresh, "launched_at")
	require.InDelta(t, 0.05, fresh["launch_rate"], 1e-9, "未调价的新分组以当前倍率作为初始倍率")
	require.NotContains(t, fresh, "recent_rate_change")

	quiet := data[2]
	for _, key := range []string{"recent_rate_change", "is_new", "launched_at", "launch_rate"} {
		require.NotContains(t, quiet, key, "无信号的旧分组不携带 %s", key)
	}
}

func TestGetAvailableGroupsPersonalRateHidesBaseRateHint(t *testing.T) {
	repo, _ := availableGroupsFixture(time.Now())
	data := getAvailableGroupsPayload(t, repo, map[int64]float64{1: 0.05, 2: 0.05}, 3)

	require.NotContains(t, data[0], "recent_rate_change", "专属倍率不跟随基础倍率，不能声称用户涨价")
	fresh := data[1]
	require.Equal(t, true, fresh["is_new"])
	require.NotContains(t, fresh, "launch_rate", "专属倍率用户不展示基础初始倍率")
}

func TestGetAvailableGroupsKeepsNewWhenHistoryUnavailable(t *testing.T) {
	data := getAvailableGroupsPayload(t, &availableGroupsRepoWithoutHistory{}, nil, 1)

	require.Len(t, data, 1)
	require.Equal(t, true, data[0]["is_new"])
	require.NotContains(t, data[0], "recent_rate_change")
	require.NotContains(t, data[0], "launch_rate")
}
