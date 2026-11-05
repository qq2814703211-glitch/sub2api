package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture/capturetest"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type captureUserRepo struct{ service.UserRepository }

func (captureUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (captureUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 27, Role: service.RoleAdmin, TotpEnabled: true}, nil
}

type captureTotpCache struct {
	service.TotpCache
	granted bool
}

func (c captureTotpCache) HasStepUpGrant(_ context.Context, id int64, session string) (bool, error) {
	return c.granted && id == 27 && session == "fixture-session", nil
}
func TestRequestCaptureVerifiedAdminCanExportAndReadRedactedBody(t *testing.T) {
	m, e := requestcapture.New(capturetest.NewStore(), t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	require.NoError(t, e)
	defer m.Close()
	task, e := m.Create(t.Context(), requestcapture.CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	session := m.Begin(requestcapture.Meta{UserID: 27})
	session.ClientRequest([]byte(`{"input":"synthetic-private-body","api_key":"fixture-credential-must-redact"}`), "application/json", nil)
	session.Finish(503)
	require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 3*time.Second, 10*time.Millisecond)
	records, e := m.Records(t.Context(), task.ID, "", true, 20, 0)
	require.NoError(t, e)
	require.Len(t, records, 1)
	record, e := m.Record(t.Context(), task.ID, records[0].ID)
	require.NoError(t, e)
	require.NotEmpty(t, record.Parts)
	for _, grant := range []bool{false, true} {
		h := &RequestCaptureHandler{Manager: m}
		h.SetStepUpDependencies(service.NewTotpService(nil, nil, captureTotpCache{granted: grant}, nil, nil, nil), service.NewUserService(captureUserRepo{}, nil, nil, nil))
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 27})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			c.Set(middleware.ContextKeySessionID, "fixture-session")
			c.Set("auth_method", "jwt")
		}, h.Gate)
		r.GET("/:task/requests/:record/export", h.Export)
		r.GET("/:task/requests/:record/content/:part", h.Content)
		for _, path := range []string{"/" + task.ID + "/requests/" + record.ID + "/export", "/" + task.ID + "/requests/" + record.ID + "/content/" + record.Parts[0].Name} {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("X-Capture-Instance", m.InstanceID())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			if !grant {
				require.Equal(t, 403, w.Code)
				require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")
				continue
			}
			require.Equal(t, 200, w.Code)
			if strings.HasSuffix(path, "/export") {
				gz, e := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
				require.NoError(t, e)
				tarReader := tar.NewReader(gz)
				var content strings.Builder
				for {
					_, e := tarReader.Next()
					if e == io.EOF {
						break
					}
					require.NoError(t, e)
					b, e := io.ReadAll(tarReader)
					require.NoError(t, e)
					content.Write(b)
				}
				require.Contains(t, content.String(), "synthetic-private-body")
				require.NotContains(t, content.String(), "fixture-credential-must-redact")
			} else {
				require.Contains(t, w.Body.String(), "synthetic-private-body")
				require.NotContains(t, w.Body.String(), "fixture-credential-must-redact")
			}
		}
	}
}

func TestRequestCaptureBodyAndExportRequireFreshHumanStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &RequestCaptureHandler{}
	for name, handler := range map[string]gin.HandlerFunc{"body": h.Content, "export": h.Export, "preflight": h.AuthorizeSensitiveRead} {
		for _, tc := range []struct {
			name, role, auth string
			want             int
		}{{"user", "user", "jwt", 403}, {"machine", "admin", service.AuditAuthMethodAdminAPIKey, 403}, {"any-admin-needs-stepup", "admin", "jwt", 503}} {
			t.Run(name+tc.name, func(t *testing.T) {
				r := gin.New()
				r.GET("/sensitive", func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 27})
					c.Set(string(middleware.ContextKeyUserRole), tc.role)
					c.Set("auth_method", tc.auth)
					handler(c)
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/sensitive", nil))
				require.Equal(t, tc.want, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.NotContains(t, w.Body.String(), "authorized")
			})
		}
	}
}
func TestRequestCaptureOrdinaryAdminAndScopedKeyPermissions(t *testing.T) {
	h := &RequestCaptureHandler{}
	for _, scoped := range []bool{false, true} {
		r := gin.New()
		r.GET("/config", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 27})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			if scoped {
				c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{KeyID: "fixture"}))
			}
			h.GetConfig(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/config", nil))
		if scoped {
			require.Equal(t, 403, w.Code)
		} else {
			require.Equal(t, 200, w.Code)
			require.Contains(t, w.Body.String(), `"enabled":false`)
		}
	}
}
func TestRequestCaptureInstanceMismatchBlocksReadsAndMutations(t *testing.T) {
	m, e := requestcapture.New(capturetest.NewStore(), t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	require.NoError(t, e)
	defer m.Close()
	_, e = m.Create(context.Background(), requestcapture.CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	h := &RequestCaptureHandler{Manager: m}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), "admin") }, h.Gate)
		r.Handle(method, "/:task/requests", func(c *gin.Context) { t.Fatal("instance mismatch reached handler") })
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/fixture/requests", nil)
		req.Header.Set("X-Capture-Instance", "another-instance")
		r.ServeHTTP(w, req)
		require.Equal(t, 409, w.Code)
		require.Equal(t, m.InstanceID(), w.Header().Get("X-Capture-Instance"))
	}
	time.Sleep(time.Millisecond)
}
