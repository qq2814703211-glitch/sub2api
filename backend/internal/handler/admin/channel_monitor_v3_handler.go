package admin

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ChannelMonitorV3Handler 是 /admin/channel-monitor-v3 的管理端 handler。
//
// 该页是 V3 唯一的配置面：分组勾选、探针身份、缓存均值窗口。保存直接写
// channel_monitor_v3_config，不进 V1/V2 的设置页（ADR 0008）。
type ChannelMonitorV3Handler struct {
	service *service.ChannelMonitorV3Service
	runner  *service.ChannelMonitorV3ProbeRunner
}

// NewChannelMonitorV3Handler 创建 handler。runner 可为 nil（只有配置面，无「立即探测」）。
func NewChannelMonitorV3Handler(svc *service.ChannelMonitorV3Service, runner *service.ChannelMonitorV3ProbeRunner) *ChannelMonitorV3Handler {
	return &ChannelMonitorV3Handler{service: svc, runner: runner}
}

// GetConfig GET /admin/channel-monitor-v3/config
func (h *ChannelMonitorV3Handler) GetConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

type channelMonitorV3UpdateRequest struct {
	Version                int                           `json:"version"`
	Enabled                bool                          `json:"enabled"`
	Groups                 []service.ChannelMonitorV3Group `json:"groups"`
	Probe                  service.ChannelMonitorV3Probe   `json:"probe"`
	APIKey                 string                        `json:"api_key"`
	CacheAverageWindowDays int                           `json:"cache_average_window_days"`
}

// UpdateConfig PUT /admin/channel-monitor-v3/config
//
// api_key 为空表示保留已存的探针 key；非空则加密覆盖。响应里永远不回传密文。
func (h *ChannelMonitorV3Handler) UpdateConfig(c *gin.Context) {
	var input channelMonitorV3UpdateRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid channel monitor v3 config")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	updated, err := h.service.UpdateConfig(c.Request.Context(), service.ChannelMonitorV3Config{
		Version:                input.Version,
		Enabled:                input.Enabled,
		Groups:                 input.Groups,
		Probe:                  input.Probe,
		CacheAverageWindowDays: input.CacheAverageWindowDays,
	}, input.APIKey, input.Version, subject.UserID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrChannelMonitorV3ConfigConflict):
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrChannelMonitorV3InvalidConfig):
			response.BadRequest(c, err.Error())
		default:
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, updated)
}

// ListGroups GET /admin/channel-monitor-v3/groups
//
// 候选分组列表。刻意不带任何白名单 / 价卡信息：V3 的可监控前置只有「被勾选」。
func (h *ChannelMonitorV3Handler) ListGroups(c *gin.Context) {
	groups, err := h.service.ListMonitorableGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, groups)
}

// ProbeNow POST /admin/channel-monitor-v3/probe
//
// 立即跑一轮 V3 探测。同样只受 V3 自己的开关控制。
func (h *ChannelMonitorV3Handler) ProbeNow(c *gin.Context) {
	if h.runner == nil {
		response.Error(c, http.StatusServiceUnavailable, "channel monitor v3 runner unavailable")
		return
	}
	if err := h.runner.ProbeOnce(c.Request.Context()); err != nil {
		if errors.Is(err, service.ErrChannelMonitorV3ProbeDisabled) {
			response.Error(c, http.StatusForbidden, err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}
