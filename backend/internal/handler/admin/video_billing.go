package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpsHandler) SetVideoBillingService(s *service.OpenAIGatewayService) { h.videoBilling = s }
func (h *OpsHandler) requireVideoBilling(c *gin.Context) bool {
	if h.videoBilling == nil || !h.videoBilling.HasDurableVideoBilling() {
		response.Error(c, http.StatusServiceUnavailable, "Video billing unavailable")
		return false
	}
	return true
}

// Admin DTO deliberately excludes command internals and the create snapshot.
func (h *OpsHandler) ListVideoBillingJobs(c *gin.Context) {
	if !h.requireVideoBilling(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	jobs, err := h.videoBilling.ListVideoBillingJobs(c.Request.Context(), c.Query("state"), c.Query("before"), limit)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Unable to load video tasks")
		return
	}
	items := make([]gin.H, 0, len(jobs))
	for _, j := range jobs {
		var actual any
		if j.Intent != nil {
			actual = j.Intent.Usage.ActualCost
		}
		items = append(items, gin.H{"id": j.ID, "user_id": j.UserID, "api_key_id": j.APIKeyID, "account_id": j.AccountID,
			"group_id": j.GroupID, "provider": j.Provider, "task_id": j.TaskID, "state": j.State, "model": j.Snapshot.Pending.Model,
			"provider_status": j.ProviderStatus, "last_error": j.LastError, "attempts": j.Attempts, "actual_cost": actual,
			"created_at": j.CreatedAt, "updated_at": j.UpdatedAt, "available_at": j.AvailableAt})
	}
	response.Success(c, gin.H{"items": items})
}
func (h *OpsHandler) RetryVideoBillingJob(c *gin.Context) {
	if !h.requireVideoBilling(c) {
		return
	}
	if err := h.videoBilling.RetryVideoBillingJob(c.Request.Context(), c.Param("id")); err != nil {
		response.Error(c, http.StatusConflict, "Task is terminal, leased, or has no known provider task ID")
		return
	}
	response.Success(c, gin.H{"queued": true})
}
func (h *OpsHandler) GetVideoBillingWorker(c *gin.Context) {
	if !h.requireVideoBilling(c) {
		return
	}
	enabled, err := h.videoBilling.VideoBillingWorkerEnabled(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Unable to read video worker setting")
		return
	}
	response.Success(c, gin.H{"enabled": enabled})
}
func (h *OpsHandler) SetVideoBillingWorker(c *gin.Context) {
	if !h.requireVideoBilling(c) {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Enabled == nil {
		response.BadRequest(c, "enabled is required")
		return
	}
	if err := h.videoBilling.SetVideoBillingWorkerEnabled(c.Request.Context(), *req.Enabled); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Unable to save video worker setting")
		return
	}
	response.Success(c, gin.H{"enabled": *req.Enabled})
}
