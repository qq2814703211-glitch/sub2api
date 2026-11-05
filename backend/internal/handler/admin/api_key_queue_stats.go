package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"strconv"
)

// GetUserAPIKeyQueueStats refreshes statistics for the currently visible user key page.
func (h *UserHandler) GetUserAPIKeyQueueStats(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	keys, _, err := h.adminService.GetUserAPIKeys(c.Request.Context(), userID, 1, 100, "created_at", "desc")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.concurrencyService == nil {
		response.Error(c, 503, "Queue statistics unavailable")
		return
	}
	h.concurrencyService.FillAPIKeyQueueStats(c.Request.Context(), keys)
	type item struct {
		ID                 int64 `json:"id"`
		CurrentConcurrency int   `json:"current_concurrency"`
		CurrentWaiting     *int  `json:"current_waiting"`
	}
	out := make([]item, 0, len(keys))
	for _, key := range keys {
		if !key.QueueStatsAvailable {
			response.Error(c, 503, "Queue statistics unavailable")
			return
		}
		out = append(out, item{key.ID, key.CurrentConcurrency, key.WaitingCount})
	}
	response.Success(c, map[string]any{"items": out})
}
