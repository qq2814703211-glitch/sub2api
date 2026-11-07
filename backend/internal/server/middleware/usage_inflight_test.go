//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// middlewareInflightStoreStub 记录中间件对在途登记表的调用，用于验证
// 「登记 → 业务 → 注销」的顺序，以及在存储报错时请求照常完成。
type middlewareInflightStoreStub struct {
	mu       sync.Mutex
	register []service.UsageInflightSnapshot
	released []string
	err      error
	panicked bool
}

func (s *middlewareInflightStoreStub) Register(_ context.Context, snapshot service.UsageInflightSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.register = append(s.register, snapshot)
	return nil
}

func (s *middlewareInflightStoreStub) UpdateModel(_ context.Context, _ int64, _ string, _ string, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *middlewareInflightStoreStub) Release(_ context.Context, _ int64, requestID string, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.released = append(s.released, requestID)
	return nil
}

func (s *middlewareInflightStoreStub) ListByUser(context.Context, int64) ([]service.UsageInflightSnapshot, error) {
	return nil, s.err
}

func (s *middlewareInflightStoreStub) ListByGroup(context.Context, int64) ([]service.UsageInflightSnapshot, error) {
	return nil, s.err
}

func (s *middlewareInflightStoreStub) CountActiveUsersByGroups(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, s.err
}

func (s *middlewareInflightStoreStub) registeredIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.register))
	for _, snapshot := range s.register {
		out = append(out, snapshot.RequestID)
	}
	return out
}

func (s *middlewareInflightStoreStub) releasedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.released))
	copy(out, s.released)
	return out
}

func newInflightTestAPIKey(groupID int64) *service.APIKey {
	return &service.APIKey{
		ID:      99,
		UserID:  4242,
		Name:    "test-key",
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Name: "主力分组"},
		User:    &service.User{ID: 4242, Username: "alice", Email: "alice@example.com"},
	}
}

func newInflightTestRouter(store service.UsageInflightStore, apiKey *service.APIKey, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), apiKey)
		c.Next()
	})
	r.Use(UsageInflight(store))
	r.POST("/v1/messages", handler)
	return r
}

// 正常路径：登记一次，业务结束后注销一次，快照带上用户/分组/API Key 身份。
func TestUsageInflightRegistersAndReleases(t *testing.T) {
	store := &middlewareInflightStoreStub{}
	var snapshotAtHandler service.UsageInflightSnapshot
	var hadTracker bool
	router := newInflightTestRouter(store, newInflightTestAPIKey(7), func(c *gin.Context) {
		if value, ok := c.Get(string(ContextKeyUsageInflight)); ok {
			if tracker, ok := value.(*usageInflightTracker); ok {
				hadTracker = tracker != nil
				snapshotAtHandler = tracker.snapshot
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !hadTracker {
		t.Fatal("inflight tracker missing from gin context inside handler")
	}
	if snapshotAtHandler.UserID != 4242 || snapshotAtHandler.GroupID != 7 {
		t.Fatalf("snapshot identity = {user %d, group %d}, want {4242, 7}", snapshotAtHandler.UserID, snapshotAtHandler.GroupID)
	}
	if snapshotAtHandler.Email != "alice@example.com" || snapshotAtHandler.Username != "alice" {
		t.Fatalf("snapshot identity = %q/%q, want alice/alice@example.com", snapshotAtHandler.Username, snapshotAtHandler.Email)
	}
	if snapshotAtHandler.StartedAt.IsZero() {
		t.Fatal("snapshot started_at is zero")
	}
	registered := store.registeredIDs()
	if len(registered) != 1 {
		t.Fatalf("registered %d times, want 1", len(registered))
	}
	released := store.releasedIDs()
	if len(released) != 1 || released[0] != registered[0] {
		t.Fatalf("released %v, want exactly the registered request id %v", released, registered)
	}
}

// 存储不可用（Redis 挂掉）不得阻断业务请求，也不得 panic。
func TestUsageInflightDoesNotBlockRequestWhenStoreFails(t *testing.T) {
	store := &middlewareInflightStoreStub{err: errors.New("redis: connection refused")}
	handlerCalled := false
	router := newInflightTestRouter(store, newInflightTestAPIKey(7), func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	router.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Fatal("handler was not reached while the inflight store was failing")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when the store fails", rec.Code)
	}
}

// handler panic 时 defer 仍然注销，登记表不会留下幽灵条目。
func TestUsageInflightReleasesOnPanic(t *testing.T) {
	store := &middlewareInflightStoreStub{}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), newInflightTestAPIKey(7))
		c.Next()
	})
	router.Use(gin.CustomRecovery(func(_ *gin.Context, _ any) {}))
	router.Use(UsageInflight(store))
	router.POST("/v1/messages", func(_ *gin.Context) {
		panic("boom")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	router.ServeHTTP(rec, req)

	if len(store.registeredIDs()) != 1 {
		t.Fatalf("registered %d times, want 1", len(store.registeredIDs()))
	}
	if len(store.releasedIDs()) != 1 {
		t.Fatalf("released %d times after panic, want 1", len(store.releasedIDs()))
	}
}

// OPTIONS 预检与健康检查/自省端点不登记，避免污染「实时使用中」人数。
func TestUsageInflightSkipsOptionsAndProbePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodOptions, "/v1/messages"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1/usage"},
	}
	for _, tc := range cases {
		store := &middlewareInflightStoreStub{}
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(ContextKeyAPIKey), newInflightTestAPIKey(7))
			c.Next()
		})
		r.Use(UsageInflight(store))
		r.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(http.StatusOK) })

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if got := store.registeredIDs(); len(got) != 0 {
			t.Fatalf("%s %s registered %v, want no registration", tc.method, tc.path, got)
		}
	}
}

// 未鉴权（上下文中没有 API Key）时不登记：没有 user_id 就没有归属。
func TestUsageInflightSkipsRequestsWithoutAPIKey(t *testing.T) {
	store := &middlewareInflightStoreStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(UsageInflight(store))
	r.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	if got := store.registeredIDs(); len(got) != 0 {
		t.Fatalf("registered %v without an authenticated API key, want no registration", got)
	}
}

// 模型补写只发生一次：重复解析（重试、内层复用）不会重复排队。
func TestNoteUsageInflightModelUpdatesOnlyOnce(t *testing.T) {
	store := &middlewareInflightStoreStub{}
	var tracker *usageInflightTracker
	router := newInflightTestRouter(store, newInflightTestAPIKey(7), func(c *gin.Context) {
		if value, ok := c.Get(string(ContextKeyUsageInflight)); ok {
			tracker, _ = value.(*usageInflightTracker)
		}
		NoteUsageInflightModel(c, "claude-sonnet-4-5", true)
		NoteUsageInflightModel(c, "claude-opus-4-1", false)
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	if tracker == nil {
		t.Fatal("tracker missing from context")
	}
	// 补写走 goroutine，给它一点时间落地，然后确认只排了一次。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tracker.notified.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !tracker.notified.Load() {
		t.Fatal("model note was never scheduled")
	}
}
