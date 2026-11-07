package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserInflightResponse 是 GET /admin/users/:id/inflight 的响应体。
//
// TotalInflight 是该用户此刻全部在途请求数（未去重按请求计），Groups 是按分组聚合的明细。
// 未绑定分组的请求归入 GroupID=0 一行，GroupName 为空表示「未分组」。
type UserInflightResponse struct {
	UserID        int64                              `json:"user_id"`
	TotalInflight int                                `json:"total_inflight"`
	Groups        []service.UserInflightGroupSummary `json:"groups"`
}

// GetUserInflight handles the per-group inflight breakdown for one user.
// GET /api/v1/admin/users/:id/inflight
//
// 只读在途登记表，不碰并发槽位：current_concurrency 仍由 /admin/users 提供，
// 两者数据源不同（槽位计数 vs 请求级快照），本接口不替换也不复用前者。
func (h *UserHandler) GetUserInflight(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	out := UserInflightResponse{UserID: userID, Groups: []service.UserInflightGroupSummary{}}
	if h.usageInflightService != nil {
		groups, total, inflightErr := h.usageInflightService.SummarizeUserInflight(c.Request.Context(), userID)
		if inflightErr != nil {
			response.Error(c, 500, "Failed to load inflight requests")
			return
		}
		out.TotalInflight = total
		if groups != nil {
			out.Groups = groups
		}
	}
	response.Success(c, out)
}

// GetGroupActiveUsers handles listing users that currently have inflight requests in a group.
// GET /api/v1/admin/groups/:id/active-users
//
// 口径：此刻有请求正在处理的用户（在途登记表按 group_id 聚合后 COUNT(DISTINCT user_id)）。
// 请求一结束就不算，与会话数（sessions_used）完全无关。
func (h *GroupHandler) GetGroupActiveUsers(c *gin.Context) {
	if h.rejectUnsupportedSimpleModeOperation(c, "advanced") {
		return
	}
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	users := []service.GroupActiveUser{}
	if h.usageInflightService != nil {
		loaded, inflightErr := h.usageInflightService.ListGroupActiveUsers(c.Request.Context(), groupID)
		if inflightErr != nil {
			response.Error(c, 500, "Failed to load active users")
			return
		}
		if loaded != nil {
			users = loaded
		}
	}
	response.Success(c, users)
}
