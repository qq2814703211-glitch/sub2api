package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDurableVideoAdminRoutesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Ops: adminhandler.NewOpsHandler(nil)}}
	auth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		status := http.StatusUnauthorized
		if c.GetHeader("Authorization") != "" {
			status = http.StatusForbidden
		}
		servermiddleware.AbortWithError(c, status, "AUTH_REQUIRED", "Admin access required")
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	step := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, auth, audit, step, nil, nil)
	for _, route := range []struct{ method, path string }{{"GET", "/jobs"}, {"POST", "/jobs/task/retry"}, {"GET", "/worker"}, {"PUT", "/worker"}} {
		for _, token := range []string{"", "Bearer user"} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(route.method, "/api/v1/admin/ops/video-billing"+route.path, nil)
			req.Header.Set("Authorization", token)
			router.ServeHTTP(w, req)
			expected := 401
			if token != "" {
				expected = 403
			}
			require.Equal(t, expected, w.Code, route.path)
		}
	}
}
