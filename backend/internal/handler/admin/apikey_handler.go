package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminAPIKeyHandler handles admin API key management
type AdminAPIKeyHandler struct {
	adminService service.AdminService
}

// NewAdminAPIKeyHandler creates a new admin API key handler
func NewAdminAPIKeyHandler(adminService service.AdminService) *AdminAPIKeyHandler {
	return &AdminAPIKeyHandler{
		adminService: adminService,
	}
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key.
type AdminUpdateAPIKeyGroupRequest struct {
	GroupIDs            *[]int64 `json:"group_ids"`
	GroupID             *int64   `json:"group_id"`               // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // true=重置 5h/1d/7d 限速用量
	ConcurrencyLimit    *int     `json:"concurrency_limit" binding:"omitempty,gte=0,lte=2147483647"`
}

// UpdateGroup handles updating an API key's admin-managed fields.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var resetKey *service.APIKey
	if req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage {
		resetKey, err = h.adminService.AdminResetAPIKeyRateLimitUsage(c.Request.Context(), keyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	var result *service.AdminUpdateAPIKeyGroupIDResult
	if req.GroupIDs != nil {
		routes, ok := h.adminService.(interface {
			AdminUpdateAPIKeyGroupRoutes(context.Context, int64, *int64, []int64) (*service.AdminUpdateAPIKeyGroupIDResult, error)
		})
		if !ok {
			response.BadRequest(c, "Ordered API Key routes are unavailable")
			return
		}
		result, err = routes.AdminUpdateAPIKeyGroupRoutes(c.Request.Context(), keyID, req.GroupID, *req.GroupIDs)
	} else {
		result, err = h.adminService.AdminUpdateAPIKeyGroupID(c.Request.Context(), keyID, req.GroupID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if resetKey != nil && req.GroupID == nil && req.GroupIDs == nil {
		result.APIKey = resetKey
	}

	if req.ConcurrencyLimit != nil {
		manager, ok := h.adminService.(service.AdminAPIKeyConcurrencyManager)
		if !ok {
			response.Error(c, 503, "Key concurrency configuration unavailable")
			return
		}
		updated, updateErr := manager.AdminSetAPIKeyConcurrency(c.Request.Context(), keyID, *req.ConcurrencyLimit)
		if updateErr != nil {
			response.ErrorFrom(c, updateErr)
			return
		}
		result.APIKey = updated
	}
	resp := struct {
		APIKey                 *dto.APIKey `json:"api_key"`
		AutoGrantedGroupAccess bool        `json:"auto_granted_group_access"`
		GrantedGroupID         *int64      `json:"granted_group_id,omitempty"`
		GrantedGroupName       string      `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromService(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
	response.Success(c, resp)
}
