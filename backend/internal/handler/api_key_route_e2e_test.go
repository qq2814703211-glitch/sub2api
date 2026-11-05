//go:build unit

package handler

import (
	"bytes"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type routeE2EGroups struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (r *routeE2EGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return r.groups[id], nil
}

type routeE2EAccounts struct{ normalForwardHandlerAccounts }

func (r *routeE2EAccounts) ListSchedulableByGroupID(_ context.Context, id int64) ([]service.Account, error) {
	var out []service.Account
	for _, a := range r.accounts {
		for _, gid := range a.GroupIDs {
			if gid == id {
				out = append(out, a)
			}
		}
	}
	return out, nil
}
func (r *routeE2EAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]service.Account, error) {
	a, _ := r.ListSchedulableByGroupID(ctx, id)
	out := a[:0]
	for _, v := range a {
		if v.Platform == p {
			out = append(out, v)
		}
	}
	return out, nil
}

type routeE2EUpstream struct {
	service.HTTPUpstream
	calls        []int64
	ctxGroups    []int64
	models       []string
	failPrimary  bool
	writeVisible bool
	rec          *httptest.ResponseRecorder
	writer       gin.ResponseWriter
}

func (u *routeE2EUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.calls = append(u.calls, id)
	u.models = append(u.models, string(body))
	status := 200
	payload := `{"id":"resp-route","object":"response","status":"completed","model":"gpt-5.1","output":[],"usage":{"input_tokens":1000,"output_tokens":200}}`
	if u.failPrimary && id == 801 {
		status = 503
		payload = `{"error":{"type":"server_error","message":"capacity unavailable"}}`
	}
	if u.writeVisible && id == 801 {
		u.writer.Write([]byte("visible"))
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
}
func TestAPIKeyRoutesE2EResponsesFallbackBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, postOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_output", true: "after_output"}[postOutput], func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			cfg.Gateway.MaxAccountSwitches = 3
			cfg.Gateway.Scheduling.DbFallbackEnabled = true
			groups := &routeE2EGroups{groups: map[int64]*service.Group{1: {ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 9}, 2: {ID: 2, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 2.5}}}
			repo := &routeE2EAccounts{}
			for i := 0; i < 2; i++ {
				repo.accounts = append(repo.accounts, service.Account{ID: int64(801 + i), GroupIDs: []int64{int64(i + 1)}, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://upstream.example"}})
			}
			ledger := &normalHandlerBillingSink{balance: 100, seen: map[string]string{}}
			logs := &normalHandlerUsageSink{}
			billing := service.NewBillingCacheService(nil, ledger, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			upstream := &routeE2EUpstream{failPrimary: true, writeVisible: postOutput}
			// Snapshot with database fallback preserves real scheduling; no selector is mocked.
			snapshot := service.NewSchedulerSnapshotService(nil, nil, repo, groups, cfg)
			gateway := service.NewOpenAIGatewayService(repo, logs, ledger, ledger, nil, nil, nil, cfg, snapshot, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&normalForwardHandlerSlots{}), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			id := int64(1)
			key := &service.APIKey{ID: 902, UserID: 903, GroupID: &id, RouteGroupIDs: []int64{1, 2}, Group: groups.groups[1], User: &service.User{ID: 903, Status: service.StatusActive, Balance: 100}}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				upstream.writer = c.Writer
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 903, Concurrency: 1})
				c.Next()
			})
			r.POST("/v1/responses", h.Responses)
			request := httptest.NewRequest("POST", "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.1","input":"hello","stream":false}`))
			request.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			upstream.rec = rec
			r.ServeHTTP(rec, request)
			require.NotEmpty(t, upstream.calls, rec.Body.String())
			if postOutput {
				require.NotContains(t, upstream.calls, int64(802))
				return
			}
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Contains(t, upstream.calls, int64(802))
			rows := logs.snapshot()
			require.Len(t, rows, 1)
			require.EqualValues(t, 2, *rows[0].GroupID)
			require.Equal(t, 2.5, rows[0].RateMultiplier)
			require.Greater(t, rows[0].ActualCost, 0.0)
			require.Equal(t, 1, ledger.debits)
			require.InDelta(t, rows[0].ActualCost, ledger.commands[0].BalanceCost, 1e-12)
		})
	}
}
