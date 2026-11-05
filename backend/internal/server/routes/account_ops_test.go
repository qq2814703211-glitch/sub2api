//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type opsRouteSettings struct{ service.SettingRepository }

func (opsRouteSettings) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func TestAccountOpsLocalAdminRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, role string
		scoped     bool
		status     int
	}{{"non-primary-full-admin", "admin", false, 200}, {"normal-user", "user", false, 403}, {"scoped-admin-key", "admin", true, 403}, {"missing-role", "", false, 403}} {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUserRole), tc.role)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				if tc.scoped {
					c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{KeyID: "synthetic"}))
				}
				c.Next()
			})
			ops := service.NewAccountOpsService(opsRouteSettings{}, nil, nil)
			h := &handler.Handlers{Admin: &handler.AdminHandlers{AccountOps: admin.NewAccountOpsHandler(ops, nil), UpstreamMonitor: admin.NewUpstreamMonitorHandler(nil)}}
			registerAccountOpsRoutes(engine.Group("/admin"), h)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/account-ops/config", nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == 200 {
				require.Contains(t, w.Body.String(), `"enabled":false`)
				require.NotContains(t, w.Body.String(), "url_cipher")
			} else {
				w = httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/upstream-monitor/sample", nil))
				require.Equal(t, 403, w.Code)
			}
		})
	}
}
