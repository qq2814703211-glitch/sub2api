package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPricingSyncHandler 管理价格同步的变更史与三档口径配置。
// 同步本身由后台任务按 pricing.hash_check_interval_minutes 自动跑；
// 这里提供「看改了什么」「现在什么状态」以及人民币口径配置的读写。
type ModelPricingSyncHandler struct {
	syncService *service.ModelPricingSyncService
	historyRepo service.ModelPricingSyncHistoryRepository
}

// NewModelPricingSyncHandler 创建处理器。
func NewModelPricingSyncHandler(
	syncService *service.ModelPricingSyncService,
	historyRepo service.ModelPricingSyncHistoryRepository,
) *ModelPricingSyncHandler {
	return &ModelPricingSyncHandler{
		syncService: syncService,
		historyRepo: historyRepo,
	}
}

// ListHistory 分页查询变更史。
// GET /api/v1/admin/model-pricing-sync/history
func (h *ModelPricingSyncHandler) ListHistory(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 200 {
		pageSize = 200
	}
	filter := &service.ModelPricingSyncHistoryFilter{
		Model:    strings.TrimSpace(c.Query("model")),
		Field:    strings.TrimSpace(c.Query("field")),
		Source:   strings.TrimSpace(c.Query("source")),
		Page:     page,
		PageSize: pageSize,
	}
	records, total, err := h.historyRepo.List(c.Request.Context(), filter)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to list model pricing sync history")
		return
	}
	response.Paginated(c, records, total, page, pageSize)
}

// Status 返回同步任务状态与配置摘要。
// GET /api/v1/admin/model-pricing-sync/status
func (h *ModelPricingSyncHandler) Status(c *gin.Context) {
	status := h.syncService.Status()
	if status == nil {
		status = map[string]any{"enabled": false}
	}
	response.Success(c, status)
}

// GetCNYOfficialConfig 返回人民币官方价（cny-official）口径的配置。
// GET /api/v1/admin/model-pricing-sync/cny-official
//
// 不走 PUT /admin/settings：那里按 UpdateSettingsRequest 的字段白名单逐项写库，
// 未列出的 JSON 键会被 encoding/json 静默丢弃，提交后返回 200 却什么都不写。
func (h *ModelPricingSyncHandler) GetCNYOfficialConfig(c *gin.Context) {
	if h.syncService == nil {
		response.Error(c, http.StatusServiceUnavailable, "settings service unavailable")
		return
	}
	settings, err := h.syncService.PublishedSettings(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, settings)
}

// UpdateCNYOfficialConfig 写入人民币官方价口径的配置。
// PUT /api/v1/admin/model-pricing-sync/cny-official
func (h *ModelPricingSyncHandler) UpdateCNYOfficialConfig(c *gin.Context) {
	if h.syncService == nil {
		response.Error(c, http.StatusServiceUnavailable, "settings service unavailable")
		return
	}
	var payload service.CNYOfficialPricingSettings
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid payload: "+err.Error())
		return
	}
	saved, err := h.syncService.SavePublishedSettings(c.Request.Context(), &payload)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, service.ErrPriceSyncConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, service.ErrPriceSyncInvalid) {
			status = http.StatusBadRequest
		}
		response.Error(c, status, err.Error())
		return
	}
	response.Success(c, saved)
}
