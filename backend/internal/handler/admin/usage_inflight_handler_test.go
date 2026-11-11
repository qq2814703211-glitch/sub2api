//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// adminInflightStoreStub 是 UsageInflightStore 的内存实现，只覆盖两个新接口用到的读路径。
type adminInflightStoreStub struct {
	snapshots []service.UsageInflightSnapshot
	err       error
}

func (s *adminInflightStoreStub) Register(context.Context, service.UsageInflightSnapshot) error {
	return s.err
}

func (s *adminInflightStoreStub) UpdateModel(context.Context, int64, string, string, bool) error {
	return s.err
}

func (s *adminInflightStoreStub) Release(context.Context, int64, string, int64) error {
	return s.err
}

func (s *adminInflightStoreStub) ListByUser(_ context.Context, userID int64) ([]service.UsageInflightSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]service.UsageInflightSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		if snapshot.UserID == userID {
			out = append(out, snapshot)
		}
	}
	return out, nil
}

func (s *adminInflightStoreStub) CountByUsers(_ context.Context, userIDs []int64) (map[int64]int, error) {
	if s.err != nil {
		return nil, s.err
	}
	counts := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		for _, snapshot := range s.snapshots {
			if snapshot.UserID == userID {
				counts[userID]++
			}
		}
	}
	return counts, nil
}

func (s *adminInflightStoreStub) ListByGroup(_ context.Context, groupID int64) ([]service.UsageInflightSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]service.UsageInflightSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		if snapshot.GroupID == groupID {
			out = append(out, snapshot)
		}
	}
	return out, nil
}

func (s *adminInflightStoreStub) CountActiveUsersByGroups(_ context.Context, groupIDs []int64) (map[int64]int, error) {
	if s.err != nil {
		return nil, s.err
	}
	counts := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		seen := make(map[int64]struct{})
		for _, snapshot := range s.snapshots {
			if snapshot.GroupID == groupID {
				seen[snapshot.UserID] = struct{}{}
			}
		}
		counts[groupID] = len(seen)
	}
	return counts, nil
}

func newInflightAdminRouter(t *testing.T, store *adminInflightStoreStub) (*gin.Engine, *UserHandler, *GroupHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc := service.NewUsageInflightService(store, nil)
	userHandler := &UserHandler{}
	userHandler.SetUsageInflightService(svc)
	groupHandler := &GroupHandler{}
	groupHandler.SetUsageInflightService(svc)

	r := gin.New()
	r.GET("/users/:id/inflight", userHandler.GetUserInflight)
	r.GET("/groups/:id/active-users", groupHandler.GetGroupActiveUsers)
	return r, userHandler, groupHandler
}

func decodeInflightBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

// GET /admin/users/:id/inflight 返回按分组的在途明细，且只包含该用户的请求。
func TestGetUserInflightReturnsPerGroupBreakdown(t *testing.T) {
	store := &adminInflightStoreStub{snapshots: []service.UsageInflightSnapshot{
		{RequestID: "a", UserID: 7, GroupID: 3, Model: "claude-sonnet-4-5", StartedAt: time.Now().UTC()},
		{RequestID: "b", UserID: 7, GroupID: 3, Model: "claude-opus-4-1", StartedAt: time.Now().UTC()},
		{RequestID: "c", UserID: 99, GroupID: 3, Model: "claude-sonnet-4-5", StartedAt: time.Now().UTC()},
	}}
	router, _, _ := newInflightAdminRouter(t, store)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/7/inflight", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	payload := decodeInflightBody(t, rec.Body.Bytes())
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, "response must wrap the payload in data: %s", rec.Body.String())
	require.EqualValues(t, 7, data["user_id"])
	require.EqualValues(t, 2, data["total_inflight"], "only this user's requests count")
	groups, ok := data["groups"].([]any)
	require.True(t, ok)
	require.Len(t, groups, 1)
	first := groups[0].(map[string]any)
	require.EqualValues(t, 3, first["group_id"])
	require.EqualValues(t, 2, first["inflight"])
}

// 非法用户 ID 返回 400，不落到服务层。
func TestGetUserInflightRejectsInvalidID(t *testing.T) {
	store := &adminInflightStoreStub{}
	router, _, _ := newInflightAdminRouter(t, store)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/0/inflight", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// GET /admin/groups/:id/active-users 返回去重后的用户名单与在途数。
func TestGetGroupActiveUsersReturnsDedupedList(t *testing.T) {
	// 快照必须带新鲜的 StartedAt：没有时间戳的条目按陈旧处理会被读侧过滤掉。
	now := time.Now().UTC()
	store := &adminInflightStoreStub{snapshots: []service.UsageInflightSnapshot{
		{RequestID: "a", UserID: 1, GroupID: 5, Username: "alice", Email: "alice@example.com", Model: "claude-sonnet-4-5", StartedAt: now},
		{RequestID: "b", UserID: 1, GroupID: 5, Username: "alice", Email: "alice@example.com", Model: "claude-opus-4-1", StartedAt: now},
		{RequestID: "c", UserID: 2, GroupID: 5, Username: "bob", Email: "bob@example.com", Model: "gpt-5", StartedAt: now},
		{RequestID: "d", UserID: 3, GroupID: 6, Username: "carol", Email: "carol@example.com", Model: "gpt-5", StartedAt: now},
	}}
	router, _, _ := newInflightAdminRouter(t, store)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups/5/active-users", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	payload := decodeInflightBody(t, rec.Body.Bytes())
	rows, ok := payload["data"].([]any)
	require.True(t, ok, "response must wrap the list in data: %s", rec.Body.String())
	require.Len(t, rows, 2, "two distinct users in group 5")

	first := rows[0].(map[string]any)
	require.EqualValues(t, 1, first["user_id"])
	require.EqualValues(t, 2, first["inflight_count"])
	require.Equal(t, "alice", first["username"])
}

// 在途登记表读失败时返回 500，不返回半截数据。
func TestGetGroupActiveUsersSurfacesStoreError(t *testing.T) {
	store := &adminInflightStoreStub{err: context.DeadlineExceeded}
	router, _, _ := newInflightAdminRouter(t, store)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups/5/active-users", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// 服务未注入时（老部署/精简装配）返回空列表而不是 panic。
func TestInflightHandlersTolerateMissingService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userHandler := &UserHandler{}
	groupHandler := &GroupHandler{}
	r := gin.New()
	r.GET("/users/:id/inflight", userHandler.GetUserInflight)
	r.GET("/groups/:id/active-users", groupHandler.GetGroupActiveUsers)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/7/inflight", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups/5/active-users", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
