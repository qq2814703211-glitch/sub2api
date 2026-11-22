//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCatalogUnitsVideoHandlerReplay(t *testing.T) {
	for _, mode := range []string{"per_request", "per_second"} {
		t.Run(mode, func(t *testing.T) {
			h, _, base, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
			bindings := &mediaRuntimeBindings{grokMediaSlotBindings: base}
			ledger := &mediaRuntimeLedger{applied: map[string]*service.UsageBillingCommand{}}
			logs := &mediaRuntimeLogs{}
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.DataDir = t.TempDir()
			cfg.Pricing.UpdateIntervalHours = 24
			path := filepath.Join(cfg.Pricing.DataDir, "model_pricing.json")
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`{"remote-video":{"glory_billing_mode":%q,"cost_per_request":0.375,"cost_per_second":0.375}}`, mode)), 0600))
			catalog := service.NewPricingService(cfg, nil)
			require.NoError(t, catalog.Initialize())
			defer catalog.Stop()
			billing := service.NewBillingService(cfg, catalog)
			account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://relay.example/v1", "openai_capabilities": []string{"video_generations"}, "model_mapping": map[string]any{"public-video": "remote-video"}}}
			repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
			h.gatewayService = service.NewOpenAIGatewayService(repo, logs, ledger, nil, nil, nil, bindings, cfg, nil, nil, billing, nil, &service.BillingCacheService{}, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
			group := &service.Group{ID: 24, Platform: service.PlatformOpenAI, RateMultiplier: 2, AllowImageGeneration: true}
			status := "queued"
			duration := ""
			task := "task-frozen"
			upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
				body := `{"id":"task-frozen"}`
				if req.Method == http.MethodGet {
					body = fmt.Sprintf(`{"id":"task-frozen","status":%q,"data":[{"url":"a"},{"url":"b"},{"error":"failed"}],"usage":{%s}}`, status, duration)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			call := func(method string) {
				c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
				key, _ := middleware.GetAPIKeyFromContext(c)
				key.Group = group
				body := ""
				if method == http.MethodPost {
					body = `{"model":"public-video","duration":99,"prompt":"test"}`
				}
				c.Request = httptest.NewRequest(method, "/v1/video/generations/"+task, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Params = gin.Params{{Key: "task_id", Value: task}}
				h.CompatibleVideoTasks(c)
				require.Equal(t, 200, w.Code, w.Body.String())
			}
			call(http.MethodPost)
			call(http.MethodGet)
			status = "failed"
			call(http.MethodGet)
			require.Empty(t, ledger.applied)
			// A later price/multiplier change applies only to new creates.
			group.VideoRateIndependent = true
			group.VideoRateMultiplier = 9
			status = "completed"
			if mode == "per_second" {
				call(http.MethodGet)
				require.Empty(t, ledger.applied, "requested seconds must never settle")
				require.Equal(t, 1, bindings.releases)
			}
			duration = `"output_seconds":2.375`
			call(http.MethodGet)
			call(http.MethodGet)
			require.Len(t, ledger.applied, 1)
			require.Len(t, logs.rows, 1)
			want := 0.75
			if mode == "per_second" {
				want = 3.5625
			}
			require.Equal(t, want, logs.rows[0].ActualCost)
			require.Equal(t, mode, *logs.rows[0].BillingMode)
			for _, cmd := range ledger.applied {
				require.Equal(t, want, cmd.BalanceCost)
			}
		})
	}
}
