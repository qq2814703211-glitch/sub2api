//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// handler 层要证明两件事：
//   - group_api_keys 的字符串键被解析成 group_id（JSON 对象键只能是字符串）；
//   - 每组独立探针的字段原样进服务层，密文永远不出现在响应里。

type v3HandlerRepoStub struct {
	cfg     *service.ChannelMonitorV3Config
	plain   string
	group   map[int64]string
	updates int
}

func (r *v3HandlerRepoStub) GetConfig(context.Context) (*service.ChannelMonitorV3Config, error) {
	if r.cfg == nil {
		return &service.ChannelMonitorV3Config{
			Version: 1,
			Groups:  []service.ChannelMonitorV3Group{},
		}, nil
	}
	return r.cfg, nil
}

func (r *v3HandlerRepoStub) UpdateConfig(_ context.Context, cfg service.ChannelMonitorV3Config, _ int) (*service.ChannelMonitorV3Config, error) {
	r.updates++
	// 存一份深拷贝：服务层返回前会 redact（清空密文），共用底层数组会把断言
	// 要看的密文一起擦掉。
	stored := cfg
	stored.Groups = make([]service.ChannelMonitorV3Group, len(cfg.Groups))
	copy(stored.Groups, cfg.Groups)
	r.cfg = &stored
	returned := cfg
	returned.Groups = make([]service.ChannelMonitorV3Group, len(cfg.Groups))
	copy(returned.Groups, cfg.Groups)
	return &returned, nil
}

func (r *v3HandlerRepoStub) InsertProbeSamples(context.Context, []service.ChannelMonitorV3ProbeSample) error {
	return nil
}

func (r *v3HandlerRepoStub) ListProbeSamples(context.Context, time.Time, time.Time) ([]service.ChannelMonitorV3ProbeSample, error) {
	return nil, nil
}

func (r *v3HandlerRepoStub) LoadMonitorableGroups(context.Context, []int64) (map[int64]service.ChannelMonitorV3GroupInfo, error) {
	return map[int64]service.ChannelMonitorV3GroupInfo{}, nil
}

func (r *v3HandlerRepoStub) ListMonitorableGroups(context.Context) ([]service.ChannelMonitorV3MonitorableGroup, error) {
	return []service.ChannelMonitorV3MonitorableGroup{}, nil
}

func (r *v3HandlerRepoStub) GroupCacheRates(context.Context, []int64, int) (map[int64]float64, error) {
	return map[int64]float64{}, nil
}

func (r *v3HandlerRepoStub) GroupHasOrganicTrafficSince(context.Context, int64, time.Time) (bool, error) {
	return false, nil
}

// v3EncryptorStub 让断言能看清「哪条明文被加密成了什么」。
type v3EncryptorStub struct{}

func (v3EncryptorStub) Encrypt(plain string) (string, error)  { return "enc:" + plain, nil }
func (v3EncryptorStub) Decrypt(cipher string) (string, error) { return cipher, nil }

// newV3UpdateRouter 挂一个只带 V3 UpdateConfig 的最小路由。
//
// handler 从 gin.Context 里取 subject；这里直接注入一份，避免为了测一个 DTO
// 去装配整套鉴权中间件。
func newV3UpdateRouter(repo *v3HandlerRepoStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewChannelMonitorV3Service(repo, v3EncryptorStub{})
	h := NewChannelMonitorV3Handler(svc, nil)

	router := gin.New()
	router.PUT("/api/v1/admin/channel-monitor-v3/config", func(c *gin.Context) {
		// 与真实 admin 鉴权中间件写的是同一个键，取值路径保持一致。
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		h.UpdateConfig(c)
	})
	return router
}

func TestChannelMonitorV3UpdateEndpointParsesPerGroupKeys(t *testing.T) {
	repo := &v3HandlerRepoStub{}
	router := newV3UpdateRouter(repo)

	body := map[string]any{
		"version": 1,
		"enabled": true,
		"groups": []map[string]any{
			{
				"group_id":      42,
				"enabled":       true,
				"models":        []string{"gpt-5"},
				"probe_enabled": true,
				"probe": map[string]any{
					"endpoint":         "https://api.anthropic.com",
					"provider":         "openai",
					"primary_model":    "gpt-5",
					"extra_models":     []string{},
					"interval_seconds": 120,
				},
			},
		},
		// JSON 对象的键只能是字符串：handler 必须把它解析成 group_id。
		"group_api_keys":             map[string]string{"42": "group-key"},
		"cache_average_window_value": 30,
		"cache_average_window_unit":  "minute",
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/channel-monitor-v3/config", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, repo.updates)

	saved := repo.cfg
	require.Len(t, saved.Groups, 1)
	require.Equal(t, int64(42), saved.Groups[0].GroupID)
	// 分组级探针原样落库，并各自加密。
	require.Equal(t, "https://api.anthropic.com", saved.Groups[0].Probe.Endpoint)
	require.Equal(t, "enc:group-key", saved.Groups[0].Probe.APIKeyEncrypted)
	// 分钟窗口穿过 DTO 被保存。
	require.Equal(t, 30, saved.CacheAverageWindowValue)
	require.Equal(t, service.ChannelMonitorV3WindowUnitMinute, saved.CacheAverageWindowUnit)

	// 响应里没有明文 key，只有 api_key_set 标记。
	require.NotContains(t, rec.Body.String(), "group-key")
	require.NotContains(t, rec.Body.String(), "global-key")
	require.Contains(t, rec.Body.String(), `"api_key_set":true`)
}

func TestChannelMonitorV3UpdateEndpointRejectsSharedProbe(t *testing.T) {
	repo := &v3HandlerRepoStub{}
	router := newV3UpdateRouter(repo)
	body := []byte(`{"version":1,"groups":[{"group_id":42,"enabled":true,"probe_enabled":true}],"probe":{"endpoint":"https://example.com","primary_model":"shared"}}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/channel-monitor-v3/config", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "configure the probe and key for each group")
	require.Zero(t, repo.updates)
}

func TestChannelMonitorV3UpdateEndpointRejectsOverlongWindow(t *testing.T) {
	repo := &v3HandlerRepoStub{}
	router := newV3UpdateRouter(repo)

	body := map[string]any{
		"version":                    1,
		"enabled":                    true,
		"groups":                     []map[string]any{{"group_id": 42, "enabled": true, "models": []string{}, "probe_enabled": true}},
		"cache_average_window_value": 129601,
		"cache_average_window_unit":  "minute",
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/channel-monitor-v3/config", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, repo.updates)
}
