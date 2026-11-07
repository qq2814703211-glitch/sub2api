package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// usageInflightTracker 是一次请求在登记表里的本地句柄。
//
// 它记录登记时用到的 requestID / groupID，使 defer 注销不需要重新推导，
// 并用原子标志保证注销只发生一次、模型补写只排一次。
type usageInflightTracker struct {
	store    service.UsageInflightStore
	snapshot service.UsageInflightSnapshot
	notified atomic.Bool
	released atomic.Bool
}

const (
	// ContextKeyUsageInflight 指向当前请求的 usageInflightTracker（*usageInflightTracker）。
	ContextKeyUsageInflight ContextKey = "usage_inflight_tracker"
	// usageInflightStoreTimeout 限制单次登记/注销/补写的 Redis 等待上限。
	// 登记失败必须只记日志，不允许拖慢业务请求。
	usageInflightStoreTimeout = 2 * time.Second
)

// usageInflightSkipPathPrefixes 里是没有「请求正在处理」语义的端点：
// 健康检查、静态资源、模型/用量自省。这些既不进登记表，也不参与分组实时人数。
var usageInflightSkipPathPrefixes = []string{
	"/health",
	"/healthz",
	"/readyz",
	"/metrics",
	"/static/",
	"/assets/",
	"/favicon",
	"/v1/sub2api/billing",
	"/v1/usage",
	"/v1/models",
	"/v1beta/models",
}

// UsageInflight 是在途请求登记中间件。
//
// 挂载位置：每条网关链的鉴权、分组解析、模型白名单之后，handler 之前。
// 位置选在最后是刻意的：多路由 Key 的最终分组由 PrepareAPIKeyRoutes 改写，
// 只有在那之后登记，group_id 才与用户真正使用的分组一致。
//
// 语义：
//   - OPTIONS 与健康检查/静态资源/自省端点跳过，不污染「实时使用中」人数。
//   - 登记成功后在 defer 注销；panic 展开时同样注销（defer 会在 unwind 中执行）。
//   - 登记、补写、注销失败只记一行日志：Redis 不可用时网关照常转发。
func UsageInflight(store service.UsageInflightStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil || c.Request == nil {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.UserID <= 0 {
			c.Next()
			return
		}
		if shouldSkipUsageInflight(c) {
			c.Next()
			return
		}

		tracker := &usageInflightTracker{
			store: store,
			snapshot: service.UsageInflightSnapshot{
				RequestID:  usageInflightRequestID(c),
				UserID:     apiKey.UserID,
				APIKeyID:   apiKey.ID,
				APIKeyName: apiKey.Name,
				GroupID:    usageInflightGroupID(apiKey),
				StartedAt:  time.Now().UTC(),
			},
		}
		if apiKey.User != nil {
			tracker.snapshot.Email = apiKey.User.Email
			tracker.snapshot.Username = apiKey.User.Username
		}
		// handler 解析出模型后通过 NoteUsageInflightModel 补写；若上下文里已经有
		// 模型（重试、内层复用），直接带上，省掉一次补写。
		if model, ok := c.Request.Context().Value(ctxkey.Model).(string); ok {
			tracker.snapshot.Model = strings.TrimSpace(model)
		}
		c.Set(string(ContextKeyUsageInflight), tracker)

		// 登记与注销都用带超时的独立上下文：客户端断开后 request ctx 会被取消，
		// 若沿用它会漏注销，登记表只能等 TTL 自然过期。
		registerCtx, cancelRegister := context.WithTimeout(context.WithoutCancel(c.Request.Context()), usageInflightStoreTimeout)
		if err := store.Register(registerCtx, tracker.snapshot); err != nil {
			logger.FromContext(c.Request.Context()).Warn("usage inflight register failed", zap.Int64("user_id", tracker.snapshot.UserID), zap.Error(err))
		}
		cancelRegister()

		defer func() {
			if !tracker.released.CompareAndSwap(false, true) {
				return
			}
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), usageInflightStoreTimeout)
			defer cancel()
			if err := store.Release(releaseCtx, tracker.snapshot.UserID, tracker.snapshot.RequestID, tracker.snapshot.GroupID); err != nil {
				logger.FromContext(c.Request.Context()).Warn("usage inflight release failed", zap.Int64("user_id", tracker.snapshot.UserID), zap.Error(err))
			}
		}()

		c.Next()
	}
}

// NoteUsageInflightModel 由 handler 在解析出模型后调用，把模型/流式标记补写进在途快照。
//
// 补写走独立 goroutine 与独立超时：模型解析在请求热路径上，不能让一次 Redis
// 往返拖慢首字节；每个请求最多排一次补写。快照已注销时存储层直接 no-op，
// 不会给结束的请求留下幽灵条目。
func NoteUsageInflightModel(c *gin.Context, model string, stream bool) {
	if c == nil {
		return
	}
	value, exists := c.Get(string(ContextKeyUsageInflight))
	if !exists {
		return
	}
	tracker, ok := value.(*usageInflightTracker)
	if !ok || tracker == nil || tracker.store == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if !tracker.notified.CompareAndSwap(false, true) {
		return
	}
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = context.WithoutCancel(c.Request.Context())
	}
	go func() {
		// 用 WithoutCancel 派生，避免请求结束后写操作被立刻取消。
		ctx, cancel := context.WithTimeout(requestCtx, usageInflightStoreTimeout)
		defer cancel()
		if err := tracker.store.UpdateModel(ctx, tracker.snapshot.UserID, tracker.snapshot.RequestID, model, stream); err != nil {
			logger.FromContext(requestCtx).Warn("usage inflight model update failed", zap.Int64("user_id", tracker.snapshot.UserID), zap.Error(err))
		}
	}()
}

func shouldSkipUsageInflight(c *gin.Context) bool {
	if c.Request.Method == http.MethodOptions {
		return true
	}
	if c.Request.URL == nil {
		return true
	}
	path := c.Request.URL.Path
	for _, prefix := range usageInflightSkipPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// usageInflightRequestID 复用 client_request_id 作为登记表的 field，便于与
// Ops 日志、审计记录按同一 ID 关联；缺失时兜底生成。
func usageInflightRequestID(c *gin.Context) string {
	if c.Request != nil {
		if id, ok := c.Request.Context().Value(ctxkey.ClientRequestID).(string); ok {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				return trimmed
			}
		}
		if id, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				return trimmed
			}
		}
	}
	return uuid.New().String()
}

// usageInflightGroupID 返回登记用的分组 ID；未绑定分组时为 0（前端显示「未分组」）。
func usageInflightGroupID(apiKey *service.APIKey) int64 {
	if apiKey == nil {
		return 0
	}
	if apiKey.Group != nil && apiKey.Group.ID > 0 {
		return apiKey.Group.ID
	}
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 {
		return *apiKey.GroupID
	}
	return 0
}
