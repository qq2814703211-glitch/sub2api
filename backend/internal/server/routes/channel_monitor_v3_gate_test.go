package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// V3's routes must never be reachable through a V1/V2 gate. The admin registration
// function takes no SettingService at all, which is the structural guarantee that
// `channel_monitor_enabled` / `channel_monitor_mode` cannot block it (ADR 0008).
// This test asserts both the shape (signature) and the outcome (routes exist and
// answer from V3's own config, even with the global monitor switched off).

type channelMonitorV3RouteRepoStub struct {
	cfg *service.ChannelMonitorV3Config
}

func (r *channelMonitorV3RouteRepoStub) GetConfig(context.Context) (*service.ChannelMonitorV3Config, error) {
	return r.cfg, nil
}

func (r *channelMonitorV3RouteRepoStub) UpdateConfig(_ context.Context, cfg service.ChannelMonitorV3Config, _ int) (*service.ChannelMonitorV3Config, error) {
	r.cfg = &cfg
	return &cfg, nil
}

func (r *channelMonitorV3RouteRepoStub) InsertProbeSamples(context.Context, []service.ChannelMonitorV3ProbeSample) error {
	return nil
}

func (r *channelMonitorV3RouteRepoStub) ListProbeSamples(context.Context, time.Time, time.Time) ([]service.ChannelMonitorV3ProbeSample, error) {
	return nil, nil
}

func (r *channelMonitorV3RouteRepoStub) LoadMonitorableGroups(context.Context, []int64) (map[int64]service.ChannelMonitorV3GroupInfo, error) {
	return map[int64]service.ChannelMonitorV3GroupInfo{}, nil
}

func (r *channelMonitorV3RouteRepoStub) ListMonitorableGroups(context.Context) ([]service.ChannelMonitorV3MonitorableGroup, error) {
	return []service.ChannelMonitorV3MonitorableGroup{{ID: 7, Name: "claude-main", Platform: "anthropic", Status: "active"}}, nil
}

func (r *channelMonitorV3RouteRepoStub) GroupCacheRates(context.Context, []int64, int) (map[int64]float64, error) {
	return map[int64]float64{}, nil
}

func (r *channelMonitorV3RouteRepoStub) GroupHasOrganicTrafficSince(context.Context, int64, time.Time) (bool, error) {
	return false, nil
}

func newChannelMonitorV3AdminRouter(t *testing.T, enabled bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &service.ChannelMonitorV3Config{
		Version:                1,
		Enabled:                enabled,
		Groups:                 []service.ChannelMonitorV3Group{},
		CacheAverageWindowDays: 30,
	}
	svc := service.NewChannelMonitorV3Service(&channelMonitorV3RouteRepoStub{cfg: cfg}, nil)
	handlers := &handler.Handlers{
		Admin: &handler.AdminHandlers{ChannelMonitorV3: admin.NewChannelMonitorV3Handler(svc, nil)},
	}

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	registerChannelMonitorV3Routes(admin, handlers)
	return router
}

func TestChannelMonitorV3AdminRoutesIgnoreGlobalMonitorSwitches(t *testing.T) {
	// The global monitor is off and the mode is v1 — a V1 deployment. V3 must still
	// serve its own configuration surface, because operators need to be able to set
	// V3 up before turning anything on.
	router := newChannelMonitorV3AdminRouter(t, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/channel-monitor-v3/config", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"the V3 config endpoint must not be blocked by channel_monitor_enabled/mode")
	require.NotContains(t, rec.Body.String(), "CHANNEL_MONITOR_DISABLED")
	require.Contains(t, rec.Body.String(), "cache_average_window_days")
}

func TestChannelMonitorV3AdminRoutesListGroupsWithoutAllowlist(t *testing.T) {
	router := newChannelMonitorV3AdminRouter(t, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/channel-monitor-v3/groups", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "claude-main")
	// The candidate list carries no allow-list / pricing gate, so no such field leaks.
	require.NotContains(t, rec.Body.String(), "model_allowlist")
	require.NotContains(t, rec.Body.String(), "model_pricing")
}

func TestChannelMonitorV3ProbeEndpointHonoursItsOwnSwitch(t *testing.T) {
	// No runner wired: the endpoint must answer 503 rather than pretend it probed.
	router := newChannelMonitorV3AdminRouter(t, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/channel-monitor-v3/probe", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotContains(t, rec.Body.String(), "CHANNEL_MONITOR_DISABLED",
		"the probe endpoint must not be gated by the global monitor switch")
}
