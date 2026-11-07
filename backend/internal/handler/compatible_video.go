package handler

import (
	"context"
	"mime"
	"net/http"
	"strings"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) CompatibleVideoTasks(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || (key.Group.Platform != service.PlatformOpenAI && key.Group.Platform != service.PlatformComposite) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Compatible video requires an OpenAI or composite group")
		return
	}
	endpoint := service.CompatibleVideoEndpointCreate
	taskID := ""
	if c.Request.Method == http.MethodPost {
		if typ, _, err := mime.ParseMediaType(c.GetHeader("Content-Type")); err != nil || typ != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "Compatible video requires application/json")
			return
		}
	} else {
		endpoint = service.CompatibleVideoEndpointStatus
		taskID = service.CompatibleVideoTaskKey(c.Param("task_id"))
	}
	setActualUpstreamEndpoint(c, EndpointCompatibleVideoTasks)
	h.handleGrokMedia(c, endpoint, taskID)
}

func prepareCompatibleVideoCompletionBilling(ctx context.Context, h *OpenAIGatewayHandler, key *service.APIKey, subject middleware.AuthSubject, taskID string, result *service.OpenAIForwardResult) *service.OpenAIForwardResult {
	if h == nil || h.gatewayService == nil || key == nil || result == nil || result.UpstreamTerminalEvent != "completed" || result.MediaUsage == nil || result.MediaUsage.Count <= 0 {
		return nil
	}
	pending, err := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil || pending == nil {
		return nil
	}
	merged := *result
	merged.MediaUsage = service.MergeMediaUsage(pending.MediaUsage, result.MediaUsage)
	merged.Model = pending.Model
	merged.BillingModel = firstNonEmptyString(pending.BillingModel, pending.Model)
	merged.UpstreamModel = firstNonEmptyString(pending.UpstreamModel, result.UpstreamModel)
	if strings.EqualFold(merged.UpstreamModel, "MiniMax-H3") || strings.EqualFold(merged.Model, "MiniMax-H3") {
		// Requested duration is not a measured output duration. MiniMax total
		// billing must wait for authoritative total or output+input measurements.
		merged.MediaUsage.DurationSeconds = result.MediaUsage.DurationSeconds
	}
	service.ApplyMediaUsageModelDimensions(merged.MediaUsage, merged.UpstreamModel)
	// Seedance's existing no-rule path is token settlement. Other compatible
	// video keeps scalar-video legacy behavior; an explicit media card supersedes it.
	if strings.Contains(strings.ToLower(merged.UpstreamModel), "seedance") || strings.Contains(strings.ToLower(merged.Model), "seedance") {
		if merged.Usage.OutputTokens <= 0 {
			return nil
		}
		merged.VideoCount = 0
	}
	owner, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil || owner == "" {
		return nil
	}
	merged.RequestID = service.StableGrokVideoBillingRequestID(taskID)
	merged.ResponseID = taskID
	merged.BillingClaimOwner = owner
	merged.Duration = service.GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	merged.VideoResolution = merged.MediaUsage.Resolution
	merged.VideoDurationSeconds = int(merged.MediaUsage.DurationSeconds)
	return &merged
}
