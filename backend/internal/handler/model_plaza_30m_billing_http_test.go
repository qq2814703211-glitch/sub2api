//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestModelPlazaHTTP30mBillingMatchesPage 把广场 HTTP 响应里分组售价的 30m/12h
// 直接喂给真实计费入口，对同一 SKU 逐项核对：
//   - 两档上下文（≤32K 与 >32K）× 非 1 倍率（用户专属 0.5）；
//   - 30m 显式 0 与 12h 独立价；
//   - 运营卡给了普通缓存写价后 30m 缺项：页面不得展示，计费按普通写价。
//
// 期望值全部由 HTTP 字段推导（单价 × token × 生效倍率），不硬编码金额。
func TestModelPlazaHTTP30mBillingMatchesPage(t *testing.T) {
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
				ID           int64            `json:"id"`
				Rate         float64          `json:"rate_multiplier"`
				UserRate     *float64         `json:"user_rate_multiplier"`
				Models       []map[string]any `json:"models"`
			} `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	active, err := groups.ListActive(context.Background())
	require.NoError(t, err)
	groupByID := make(map[int64]service.Group, len(active))
	for _, g := range active {
		groupByID[g.ID] = g
	}

	type pageModel struct {
		rate      float64
		model     map[string]any
		intervals []any
	}
	find := func(groupID int64, name string) pageModel {
		t.Helper()
		for _, group := range out.Data.Groups {
			if group.ID != groupID {
				continue
			}
			rate := group.Rate
			if group.UserRate != nil {
				rate = *group.UserRate
			}
			for _, model := range group.Models {
				if model["name"] != name {
					continue
				}
				pm := pageModel{rate: rate, model: model}
				if p, ok := model["pricing"].(map[string]any); ok {
					if iv, ok := p["intervals"].([]any); ok {
						pm.intervals = iv
					}
				}
				return pm
			}
		}
		t.Fatalf("missing group %d model %s", groupID, name)
		return pageModel{}
	}
	num := func(m map[string]any, key string) (float64, bool) {
		v, ok := m[key]
		if !ok || v == nil {
			return 0, false
		}
		f, ok := v.(float64)
		return f, ok
	}

	// 计费入口：与 HTTP 广场相同的分组、解析器与用户倍率。
	bill := func(groupID int64, model string, tokens service.UsageTokens, at time.Time) *service.CostBreakdown {
		t.Helper()
		g := groupByID[groupID]
		bd, err := billing.CalculateCostUnified(service.CostInput{
			Ctx:            context.Background(),
			Model:          model,
			GroupID:        &g.ID,
			Group:          &g,
			Tokens:         tokens,
			RateMultiplier: find(groupID, model).rate,
			PricingAt:      at,
			Resolver:       resolver,
		})
		require.NoError(t, err)
		require.NotNil(t, bd)
		return bd
	}
	// at 固定在 2026-01-05 00:00 UTC = 周一 08:00 Asia/Shanghai，落在
	// fixture-window 的 09:00–13:00 窗口之外，避免分时窗口影响断言。
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

	t.Run("tiered 30m and 12h across two context tiers at a 0.5 user rate", func(t *testing.T) {
		pm := find(51, "fixture-tier")
		require.Equal(t, 0.5, pm.rate)
		require.Len(t, pm.intervals, 2)
		tiers := []struct {
			context int
			iv      map[string]any
		}{
			{context: 1000, iv: pm.intervals[0].(map[string]any)},
			{context: 40000, iv: pm.intervals[1].(map[string]any)},
		}
		for i, tc := range tiers {
			inputPrice, ok := num(tc.iv, "input_price")
			require.True(t, ok, "tier %d input price", i)
			w30, has30 := num(tc.iv, "cache_write_30m_price")
			require.True(t, has30, "tier %d must expose its own 30m sale price", i)
			w12, has12 := num(tc.iv, "cache_write_12h_price")
			require.True(t, has12, "tier %d must expose its own 12h sale price", i)
			require.NotEqual(t, w30, w12, "tier %d: 30m and 12h must be independent", i)

			// 30m 桶：真实计费里 CacheCreation30mTokens 与 12h 桶互斥。
			bd := bill(51, "fixture-tier", service.UsageTokens{
				InputTokens: tc.context, CacheCreationTokens: 1000, CacheCreation30mTokens: 1000,
			}, at)
			wantCache := float64(1000) * w30 * pm.rate
			require.InDelta(t, wantCache, bd.CacheCreationCost*pm.rate, 1e-12,
				"tier %d: billed 30m cost must equal the displayed 30m price", i)
			require.InDelta(t, (float64(tc.context)*inputPrice+float64(1000)*w30)*pm.rate, bd.ActualCost, 1e-12)

			// 12h 桶：同一档、同一倍率，价格必须独立。
			bd = bill(51, "fixture-tier", service.UsageTokens{
				InputTokens: tc.context, CacheCreationTokens: 1000, CacheCreation12hTokens: 1000,
			}, at)
			require.InDelta(t, float64(1000)*w12*pm.rate, bd.CacheCreationCost*pm.rate, 1e-12,
				"tier %d: billed 12h cost must equal the displayed 12h price", i)
			require.InDelta(t, (float64(tc.context)*inputPrice+float64(1000)*w12)*pm.rate, bd.ActualCost, 1e-12)
		}
		// 第二档的 30m 是显式 0：页面显示 0 且真实计费为 0，不是缺项、也不是参考价。
		require.Equal(t, 0.0, mustNum(t, pm.intervals[1].(map[string]any), "cache_write_30m_price"))
		bdZero := bill(51, "fixture-tier", service.UsageTokens{
			InputTokens: 40000, CacheCreationTokens: 1000, CacheCreation30mTokens: 1000,
		}, at)
		require.Equal(t, 0.0, bdZero.CacheCreationCost, "billed 30m write must be free where the page says 0")
	})

	t.Run("operator cache-write override leaves 30m missing and bills the ordinary write", func(t *testing.T) {
		pm := find(54, "fixture-window")
		require.Equal(t, 2.0, pm.rate)
		pricingMap := pm.model["pricing"].(map[string]any)
		require.NotContains(t, pricingMap, "cache_write_30m_price", "missing sale 30m must stay missing")
		require.Nil(t, pricingMap["cache_write_12h_price"])
		ordinary, ok := num(pricingMap, "cache_write_price")
		require.True(t, ok)

		bd := bill(54, "fixture-window", service.UsageTokens{
			InputTokens: 1000, CacheCreationTokens: 1000, CacheCreation30mTokens: 1000,
		}, at)
		// 页面没有 30m 价：计费按运营卡普通写价，不得偷偷用目录参考价 5e-6。
		require.InDelta(t, float64(1000)*ordinary, bd.CacheCreationCost, 1e-12)
		require.NotEqual(t, 5e-6, ordinary)
	})
}

func mustNum(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key]
	require.Truef(t, ok, "missing %s", key)
	require.NotNil(t, v, "nil %s", key)
	f, ok := v.(float64)
	require.Truef(t, ok, "%s is not a number", key)
	return f
}
