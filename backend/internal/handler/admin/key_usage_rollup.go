package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/keyusage"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SetKeyUsageRuntime(runtime *keyusage.Runtime) { h.keyUsage = runtime }
func (h *SettingHandler) GetKeyUsageRollup(c *gin.Context) {
	if h.keyUsage == nil {
		response.Success(c, keyusage.Status{})
		return
	}
	status, err := h.keyUsage.Status(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
func (h *SettingHandler) UpdateKeyUsageRollup(c *gin.Context) {
	var req struct {
		Enabled         *bool `json:"enabled" binding:"required"`
		IntervalSeconds int   `json:"interval_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid key usage configuration: "+err.Error())
		return
	}
	cfg := keyusage.Config{Enabled: *req.Enabled, IntervalSeconds: req.IntervalSeconds}
	if cfg.IntervalSeconds < 0 || cfg.IntervalSeconds > 3600 {
		response.BadRequest(c, "interval_seconds must be between 1 and 3600")
		return
	}
	if h.keyUsage == nil {
		response.Error(c, 503, "Key usage runtime unavailable")
		return
	}
	if err := h.keyUsage.Configure(c.Request.Context(), cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	status, err := h.keyUsage.Status(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
func (h *SettingHandler) PurgeKeyUsageRollup(c *gin.Context) {
	if h.keyUsage == nil {
		response.Error(c, 503, "Key usage runtime unavailable")
		return
	}
	if err := h.keyUsage.Purge(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"purged": true})
}
