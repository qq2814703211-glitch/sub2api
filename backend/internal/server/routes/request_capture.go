package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerRequestCaptureRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h.Admin.RequestCapture == nil {
		return
	}
	capture := admin.Group("/request-captures")
	c := h.Admin.RequestCapture
	capture.GET("/config", c.GetConfig)
	capture.PUT("/config", c.UpdateConfig)
	capture.Use(c.Gate)
	capture.GET("/sensitive-access", c.AuthorizeSensitiveRead)
	capture.GET("", c.List)
	capture.POST("", c.Create)
	capture.POST("/:task/stop", c.Stop)
	capture.DELETE("/:task", c.Delete)
	capture.GET("/:task/requests", c.Records)
	capture.GET("/:task/requests/:record", c.Detail)
	capture.GET("/:task/requests/:record/content/:part", c.Content)
	capture.GET("/:task/export", c.Export)
	capture.GET("/:task/requests/:record/export", c.Export)
}
