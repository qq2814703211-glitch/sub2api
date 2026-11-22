//go:build unit

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const plazaTariffCatalog = `{
  "fixture-window": {
    "input_cost_per_token": 0.000002, "output_cost_per_token": 0.000003,
    "cache_read_input_token_cost": 0.0000005,
    "cache_creation_input_token_cost": 0.000004,
    "cache_creation_input_token_cost_30m": 0.000005,
    "cache_creation_input_token_cost_12h": 0.000007,
    "glory_price_windows": [{"timezone":"Asia/Shanghai","weekdays":[1,3,5],"start":"09:00","end":"13:00","exclude_cn_holidays":true,
      "rates":{"input_cost_per_token":0.000009,"output_cost_per_token":0.000004,"cache_read_input_token_cost":0.0000015,"cache_creation_input_token_cost":0.000006,"cache_creation_input_token_cost_30m":0,"cache_creation_input_token_cost_12h":0.000013}}]
  },
  "fixture-tier": {
    "input_cost_per_token":0.000001,"output_cost_per_token":0.000003,"cache_creation_input_token_cost_30m":0.000005,"cache_creation_input_token_cost_12h":0.000006,
    "glory_price_tiers":[{"threshold":32000,"rates":{"input_cost_per_token":0.000002,"output_cost_per_token":0.000004,"cache_creation_input_token_cost_30m":0,"cache_creation_input_token_cost_12h":0.000011}}]
  },
  "fixture-request":{"glory_billing_mode":"per_request","cost_per_request":0},
  "fixture-second":{"glory_billing_mode":"per_second","cost_per_second":0.375},
  "fixture-image":{"glory_billing_mode":"per_image","output_cost_per_image":2.125}
}`

type plazaWindowGroups struct {
	service.GroupRepository
	extraModels []string
}

func (g plazaWindowGroups) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{
		{ID: 51, Name: "Fixture standard", Platform: service.PlatformOpenAI, SubscriptionType: "standard", RateMultiplier: 2,
			LongContextPricingEnabled: true, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: append([]string{"fixture-window", "fixture-tier", "fixture-request", "fixture-second", "fixture-image"}, g.extraModels...)}},
		{ID: 52, Name: "Fixture group peak", Platform: service.PlatformOpenAI, SubscriptionType: "subscription", RateMultiplier: 2,
			PeakRateEnabled: true, PeakStart: "09:00", PeakEnd: "13:00", PeakRateMultiplier: 3,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"fixture-window"}}},
		{ID: 53, Name: "Fixture explicit sale", Platform: service.PlatformOpenAI, SubscriptionType: "standard", RateMultiplier: 2,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"fixture-window"}},
			ModelPricing:   []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"fixture-window"}, BillingMode: service.BillingModeToken, InputPrice: testPtr(0), OutputPrice: testPtr(0.000019)}}},
		// 显式缓存写价覆盖会把 30m/12h 清成缺项（沿用普通写价的旧契约）：
		// 广场售价必须缺项，不能拿目录参考价的 30m/12h 顶上。
		{ID: 54, Name: "Fixture cache write override", Platform: service.PlatformOpenAI, SubscriptionType: "standard", RateMultiplier: 2,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"fixture-window"}},
			ModelPricing:   []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"fixture-window"}, BillingMode: service.BillingModeToken, InputPrice: testPtr(0), OutputPrice: testPtr(0.000019), CacheWritePrice: testPtr(0.000008)}}},
	}, nil
}

type plazaWindowUser struct{ service.UserRepository }

func (plazaWindowUser) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{}, nil
}

type plazaWindowSubscriptions struct {
	service.UserSubscriptionRepository
}

func (plazaWindowSubscriptions) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type plazaWindowUserRates struct {
	service.UserGroupRateRepository
}

func (plazaWindowUserRates) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return map[int64]float64{51: 0.5}, nil
}

func TestModelPlazaHTTPAbsoluteWindows(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.UpdateIntervalHours = 24
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), []byte(plazaTariffCatalog), 0600))
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	pricing := service.NewPricingService(cfg, nil)
	require.NoError(t, pricing.Initialize())
	t.Cleanup(pricing.Stop)
	groups := plazaWindowGroups{}
	billing := service.NewBillingService(cfg, pricing)
	resolver := service.NewModelPricingResolver(nil, billing)
	plaza := service.NewModelPlazaService(plazaVaryChannelRepo{}, groups, pricing, billing, resolver)
	keys := service.NewAPIKeyService(nil, plazaWindowUser{}, groups, plazaWindowSubscriptions{}, plazaWindowUserRates{}, nil, cfg)
	h := NewModelPlazaHandler(plaza, keys, service.NewSettingService(plazaVarySettingRepo{}, cfg))
	r := gin.New()
	r.GET("/api/v1/model-plaza", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		h.Get(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Data struct {
			Groups []struct {
				ID       int              `json:"id"`
				UserRate *float64         `json:"user_rate_multiplier"`
				Models   []map[string]any `json:"models"`
			} `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data.Groups, 4)
	find := func(groupID int, name string) map[string]any {
		t.Helper()
		for _, group := range out.Data.Groups {
			if group.ID == groupID {
				for _, model := range group.Models {
					if model["name"] == name {
						return model
					}
				}
			}
		}
		t.Fatalf("missing group %d model %s", groupID, name)
		return nil
	}
	m := find(51, "fixture-window")
	reference := m["official_pricing"].(map[string]any)
	want := []any{map[string]any{
		"timezone": "Asia/Shanghai", "weekdays": []any{1.0, 3.0, 5.0}, "start": "09:00", "end": "13:00", "exclude_cn_holidays": true,
		"rates": map[string]any{"input_cost_per_token": 9e-6, "output_cost_per_token": 4e-6, "cache_read_input_token_cost": 1.5e-6, "cache_creation_input_token_cost": 6e-6, "cache_creation_input_token_cost_30m": 0.0, "cache_creation_input_token_cost_12h": 13e-6},
	}}
	require.Equal(t, want, m["price_windows"])
	require.Equal(t, want, reference["price_windows"])
	require.Equal(t, 5e-6, reference["cache_write_30m_price"])
	require.Equal(t, 7e-6, reference["cache_write_12h_price"])
	require.NotContains(t, m, "time_pricing")
	for _, groupID := range []int{52, 53, 54} {
		other := find(groupID, "fixture-window")
		require.NotContains(t, other, "price_windows")
		require.Equal(t, want, other["official_pricing"].(map[string]any)["price_windows"])
	}
	sale := find(53, "fixture-window")["pricing"].(map[string]any)
	require.Equal(t, 0.0, sale["input_price"])
	require.Equal(t, 19e-6, sale["output_price"])
	// 运营卡没动缓存写价：售价沿用目录的 30m=5e-6、12h=7e-6。
	require.Equal(t, 5e-6, sale["cache_write_30m_price"])
	require.Equal(t, 7e-6, sale["cache_write_12h_price"])
	// 运营卡显式给了普通缓存写价：30m/12h 在实收口径下是缺项，
	// 目录参考价的 30m 不得出现在售价里。
	overridden := find(54, "fixture-window")
	overriddenSale := overridden["pricing"].(map[string]any)
	require.Equal(t, 8e-6, overriddenSale["cache_write_price"])
	require.NotContains(t, overriddenSale, "cache_write_30m_price", "reference 30m must not masquerade as a sale price")
	require.Nil(t, overriddenSale["cache_write_12h_price"], "12h 维持既有 null 缺项契约")
	require.Equal(t, 5e-6, overridden["official_pricing"].(map[string]any)["cache_write_30m_price"])
	for _, group := range out.Data.Groups {
		if group.ID == 51 {
			require.Equal(t, testPtr(0.5), group.UserRate)
		}
	}
	for model, expectation := range map[string]struct {
		mode  string
		price float64
	}{
		"fixture-request": {"per_request", 0}, "fixture-second": {"per_second", 0.375}, "fixture-image": {"per_image", 2.125},
	} {
		p := find(51, model)["pricing"].(map[string]any)
		require.Equal(t, expectation.mode, p["billing_mode"])
		require.Equal(t, expectation.price, p["unit_price"])
	}
	tier := find(51, "fixture-tier")["official_pricing"].(map[string]any)["intervals"].([]any)[1].(map[string]any)
	require.Equal(t, 0.0, tier["cache_write_30m_price"])
	require.Equal(t, 11e-6, tier["cache_write_12h_price"])
	// 分组售价同样的两档：30m 显式 0 与 12h 独立价逐档给出，缺项不补。
	paidTier := find(51, "fixture-tier")["pricing"].(map[string]any)
	require.Equal(t, 5e-6, paidTier["cache_write_30m_price"])
	paidTiers := paidTier["intervals"].([]any)
	require.Len(t, paidTiers, 2)
	require.Equal(t, 5e-6, paidTiers[0].(map[string]any)["cache_write_30m_price"])
	require.Equal(t, 0.0, paidTiers[1].(map[string]any)["cache_write_30m_price"], "explicit zero 30m sale price must survive")
	require.Equal(t, 6e-6, paidTiers[0].(map[string]any)["cache_write_12h_price"])
	require.Equal(t, 11e-6, paidTiers[1].(map[string]any)["cache_write_12h_price"])
	// 平价模型同样带售价 30m/12h（非阶梯）。
	paidFlat := find(51, "fixture-window")["pricing"].(map[string]any)
	require.Equal(t, 5e-6, paidFlat["cache_write_30m_price"])
	require.Equal(t, 7e-6, paidFlat["cache_write_12h_price"])
	if path := os.Getenv("PLAZA_HTTP_FIXTURE_OUT"); path != "" {
		require.NoError(t, os.WriteFile(path, w.Body.Bytes(), 0600))
	}
}

// Replay the real Vue form payload and return the actual admin GET and plaza
// GET bodies to Vue. Only the fixture revision is replaced with the live one.
func TestModelPlazaAdminPublicationHTTP(t *testing.T) {
	socket, payloadPath := os.Getenv("PRICE_PUBLISH_TEST_SOCKET"), os.Getenv("PLAZA_ADMIN_PAYLOAD_IN")
	if socket == "" || payloadPath == "" {
		t.Skip("requires a disposable PostgreSQL Unix socket and exported Vue form payload")
	}
	require.True(t, filepath.IsAbs(socket))
	dsn := "host=" + socket + " user=publish_test dbname=price_publish_test sslmode=disable"
	root, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer root.Close()
	schema := pq.QuoteIdentifier("plaza_" + uuid.NewString())
	_, err = root.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	defer root.Exec("DROP SCHEMA " + schema + " CASCADE")
	db, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	defer db.Close()
	for _, name := range []string{"916_model_pricing_sync_history.sql", "921_price_sync_revisions.sql"} {
		ddl, err := os.ReadFile(filepath.Join("../../migrations", name))
		require.NoError(t, err)
		_, err = db.Exec(string(ddl))
		require.NoError(t, err)
	}

	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "override.json")
	cfg.Pricing.UpdateIntervalHours = 24
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), []byte(plazaTariffCatalog), 0600))
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	pricing, err := service.ProvidePricingService(cfg, nil)
	require.NoError(t, err)
	t.Cleanup(pricing.Stop)
	history := repository.NewModelPricingSyncHistoryRepository(db)
	syncService, err := service.ProvideModelPricingSyncService(cfg, pricing, history, nil, nil, repository.NewPriceSyncPublishStore(db))
	require.NoError(t, err)
	billing := service.NewBillingService(cfg, pricing)
	groups := plazaWindowGroups{extraModels: []string{"fixture-manual"}}
	plazaService := service.NewModelPlazaService(plazaVaryChannelRepo{}, groups, pricing, billing, service.NewModelPricingResolver(nil, billing))
	plaza := NewModelPlazaHandler(plazaService, nil, service.NewSettingService(plazaVarySettingRepo{}, cfg))
	editor := admin.NewModelPricingSyncHandler(syncService, history)
	const adminURL = "/api/v1/admin/model-pricing-sync/cny-official"
	const plazaURL = "/api/v1/model-plaza"
	r := gin.New()
	r.GET(adminURL, editor.GetCNYOfficialConfig)
	r.PUT(adminURL, editor.UpdateCNYOfficialConfig)
	r.GET(plazaURL, plaza.Get)
	call := func(method, path string, body []byte) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		q := httptest.NewRequest(method, path, bytes.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, q)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w.Body.Bytes()
	}
	var head struct {
		Data struct {
			Revision string `json:"revision"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(call(http.MethodGet, adminURL, nil), &head))
	require.NotEmpty(t, head.Data.Revision)
	raw, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "r1", payload["revision"])
	payload["revision"] = head.Data.Revision
	raw, err = json.Marshal(payload)
	require.NoError(t, err)
	call(http.MethodPut, adminURL, raw)
	readback := call(http.MethodGet, adminURL, nil)
	var saved struct {
		Data struct {
			Revision string                    `json:"revision"`
			Active   string                    `json:"active_revision"`
			Manual   map[string]map[string]any `json:"manual_overrides"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(readback, &saved))
	require.NotEqual(t, head.Data.Revision, saved.Data.Revision)
	require.Equal(t, saved.Data.Revision, saved.Data.Active)
	require.Equal(t, 0.0, saved.Data.Manual["fixture-manual"]["cache_write_30m"])
	require.NotContains(t, saved.Data.Manual["fixture-manual"], "cache_write_1h")
	var entry []byte
	require.NoError(t, db.QueryRow(`SELECT snapshot->'entries'->'fixture-manual' FROM price_sync_revisions WHERE revision=$1`, saved.Data.Revision).Scan(&entry))
	var stored map[string]any
	require.NoError(t, json.Unmarshal(entry, &stored))
	require.Equal(t, 11e-6, stored["output_cost_per_token"])
	require.Equal(t, 0.0, stored["cache_creation_input_token_cost_30m"])
	require.Equal(t, 13e-6, stored["cache_creation_input_token_cost_12h"])
	require.NotContains(t, stored, "cache_creation_input_token_cost_above_1hr")
	for model, check := range map[string]struct {
		field string
		price float64
	}{
		"fixture-request": {"cost_per_request", 0},
		"fixture-second":  {"cost_per_second", 0.375},
		"fixture-image":   {"output_cost_per_image", 2.125},
	} {
		var rate float64
		require.NoError(t, db.QueryRow(`SELECT (snapshot->'entries'->$1->>$2)::float8 FROM price_sync_revisions WHERE revision=$3`, model, check.field, saved.Data.Revision).Scan(&rate))
		require.Equal(t, check.price, rate)
	}
	shown := call(http.MethodGet, plazaURL, nil)
	for key, body := range map[string][]byte{"PLAZA_ADMIN_READBACK_OUT": readback, "PLAZA_ADMIN_PLAZA_OUT": shown} {
		if path := os.Getenv(key); path != "" {
			require.NoError(t, os.WriteFile(path, body, 0600))
		}
	}
}
