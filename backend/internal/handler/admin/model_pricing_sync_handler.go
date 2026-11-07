package admin

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPricingSyncHandler 管理 models.dev 官方参考价同步的变更史。
// 同步本身由后台任务按 pricing.hash_check_interval_minutes 自动跑；
// 这里只提供「看改了什么」和「现在什么状态」两个只读接口。
type ModelPricingSyncHandler struct {
	syncService *service.ModelPricingSyncService
	historyRepo service.ModelPricingSyncHistoryRepository
}

// NewModelPricingSyncHandler 创建处理器。
func NewModelPricingSyncHandler(syncService *service.ModelPricingSyncService, historyRepo service.ModelPricingSyncHistoryRepository) *ModelPricingSyncHandler {
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
