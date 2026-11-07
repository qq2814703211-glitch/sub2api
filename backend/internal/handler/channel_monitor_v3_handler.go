package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ChannelMonitorV3Handler 是 /channel-monitor-v3 的用户只读 handler。
//
// 与 V2 handler 的分工：V2 的读取挂在 channel_monitor_mode 守卫上；V3 的读取只
// 看 channel_monitor_v3_config.enabled（ADR 0008），因此用户侧 V3 视图在 mode=v1/v2
// 的部署上也能按 V3 自己的开关工作。
type ChannelMonitorV3Handler struct {
	service       *service.ChannelMonitorV3Service
	apiKeyService channelMonitorV2GroupAuthorizer
}

// NewChannelMonitorV3Handler 创建用户端 V3 handler。
func NewChannelMonitorV3Handler(svc *service.ChannelMonitorV3Service, apiKeyService *service.APIKeyService) *ChannelMonitorV3Handler {
	return &ChannelMonitorV3Handler{service: svc, apiKeyService: apiKeyService}
}

// Status GET /channel-monitor-v3/status —— 前端据此决定是否渲染 V3 看板。
func (h *ChannelMonitorV3Handler) Status(c *gin.Context) {
	status, err := h.service.PublicStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// Snapshot GET /channel-monitor-v3/snapshot
func (h *ChannelMonitorV3Handler) Snapshot(c *gin.Context) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	if !h.scopeFilter(c, &filter) {
		return
	}
	result, err := h.service.Snapshot(c.Request.Context(), filter, channelMonitorV2IsAdmin(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// Matrix GET /channel-monitor-v3/matrix
func (h *ChannelMonitorV3Handler) Matrix(c *gin.Context) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	groupBy, err := service.ParseChannelMonitorV2GroupBy(c.Query("group_by"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !h.scopeFilter(c, &filter) {
		return
	}
	result, err := h.service.Matrix(c.Request.Context(), filter, groupBy, channelMonitorV2IsAdmin(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// parseFilter 解析 range / platform / model / group_id 查询参数。
func (h *ChannelMonitorV3Handler) parseFilter(c *gin.Context) (service.ChannelMonitorV2Filter, bool) {
	groups, err := parseChannelMonitorV2GroupIDs(queryList(c, "group_id"))
	if err != nil {
		response.BadRequest(c, "invalid group_id")
		return service.ChannelMonitorV2Filter{}, false
	}
	filter, err := h.service.ParseFilter(c.Query("range"), queryList(c, "platform"), queryList(c, "model"), groups)
	if err != nil {
		response.BadRequest(c, err.Error())
		return service.ChannelMonitorV2Filter{}, false
	}
	return filter, true
}

// scopeFilter 把普通用户限制在自己可用的分组内（与 V2 用户视图一致的服务端作用域）。
func (h *ChannelMonitorV3Handler) scopeFilter(c *gin.Context, filter *service.ChannelMonitorV2Filter) bool {
	if channelMonitorV2IsAdmin(c) {
		return true
	}
	if h.apiKeyService == nil {
		response.Error(c, http.StatusInternalServerError, "channel monitor group authorization unavailable")
		return false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return false
	}
	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	filter.RestrictGroups = true
	filter.AllowedGroupIDs = make([]int64, 0, len(groups))
	for i := range groups {
		filter.AllowedGroupIDs = append(filter.AllowedGroupIDs, groups[i].ID)
	}
	return true
}

