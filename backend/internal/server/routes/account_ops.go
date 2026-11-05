package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerAccountOpsRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	guard := func(c *gin.Context) {
		role, ok := middleware.GetUserRoleFromContext(c)
		if !ok || role != "admin" || adminaccess.FromContext(c.Request.Context()) != nil {
			response.Forbidden(c, "Administrator access required")
			c.Abort()
			return
		}
		c.Next()
	}
	if v := h.Admin.AccountOps; v != nil {
		r := admin.Group("/account-ops")
		r.Use(guard)
		r.GET("/config", v.GetConfig)
		r.PUT("/config", v.RequireStepUp, v.SaveConfig)
		r.PUT("/notification-settings", v.SaveNotificationSettings)
		r.GET("/alerts", v.List)
		r.GET("/balance-accounts", v.BalanceAccounts)
		r.GET("/threshold-accounts", v.ThresholdAccounts)
		r.GET("/groups", v.Groups)
		r.PUT("/groups", v.SaveGroups)
		r.PUT("/webhooks/:id", v.RequireStepUp, v.SaveWebhook)
		r.DELETE("/webhooks/:id", v.RequireStepUp, v.DeleteWebhook)
		r.POST("/webhooks/:id/test", v.RequireStepUp, v.TestWebhook)
		r.PUT("/rules/batch", v.SaveRulesBatch)
		r.PUT("/rules/:id", v.SaveRule)
		r.DELETE("/rules/:id", v.DeleteRule)
	}
	if v := h.Admin.UpstreamMonitor; v != nil {
		r := admin.Group("/upstream-monitor")
		r.Use(guard)
		r.GET("/overview", v.Overview)
		r.GET("/accounts", v.Accounts)
		r.GET("/accounts/:id/history", v.AccountHistory)
		r.GET("/rate-changes", v.RateChanges)
		r.GET("/trend", v.Trend)
		r.GET("/alerts", v.Alerts)
		r.POST("/alerts/:id/ack", v.AckAlert)
		r.GET("/alert-rules", v.ListAlertRules)
		r.PUT("/alert-rules", v.SaveAlertRule)
		r.DELETE("/alert-rules/:id", v.DeleteAlertRule)
		r.GET("/export", v.Export)
		r.POST("/sample", v.Sample)
	}
}
