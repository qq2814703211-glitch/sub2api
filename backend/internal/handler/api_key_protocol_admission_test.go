//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func protocolAdmissionCache(t *testing.T) (*service.ConcurrencyService, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := service.NewConcurrencyService(repository.NewConcurrencyCache(rdb, 1, 30))
	svc.SetAPIKeyQueuePolicy(service.APIKeyQueuePolicy{MaxWaiting: 2, Timeout: 3 * time.Second})
	t.Cleanup(svc.StopAPIKeyQueue)
	return svc, rdb
}

func TestAPIKeyProtocolHTTPQueueBeforeAccountAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "handoff", true: "revocation"}[revoke], func(t *testing.T) {
			svc, rdb := protocolAdmissionCache(t)
			cache := repository.NewConcurrencyCache(rdb, 1, 30).(service.APIKeySlotQueueCache)
			held, _, err := cache.AcquireAPIKeySlotWithTime(context.Background(), 7, 1, "holder")
			require.NoError(t, err)
			require.True(t, held)
			var revoked atomic.Bool
			var accountEntered atomic.Bool
			router := gin.New()
			router.Use(func(c *gin.Context) {
				ctx, cancel := service.WithAPIKeyAdmissionOwner(c.Request.Context())
				defer cancel()
				ctx = service.WithAPIKeyQueueAuthRevalidator(ctx, func(context.Context) (int, error) {
					if revoked.Load() {
						return 0, service.NewAPIKeyQueueAuthRejected(infraerrors.Unauthorized("API_KEY_DISABLED", "API key is disabled"))
					}
					return 1, nil
				})
				c.Request = c.Request.WithContext(ctx)
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, ConcurrencyLimit: 1})
				c.Next()
			})
			helper := NewConcurrencyHelper(svc, SSEPingFormatNone, time.Second)
			router.POST("/v1/responses", func(c *gin.Context) {
				release, err := helper.AcquireUserSlotWithWait(c, 9, 0, false, new(bool))
				if err != nil {
					status, _, _, message := concurrencyErrorResponse(err, "API key")
					c.String(status, message)
					return
				}
				defer release()
				accountEntered.Store(true)
				result, err := svc.AcquireAccountSlot(c.Request.Context(), 11, 1)
				require.NoError(t, err)
				require.True(t, result.Acquired)
				defer result.ReleaseFunc()
				c.Status(204)
			})
			server := httptest.NewServer(router)
			defer server.Close()
			done := make(chan int, 1)
			go func() {
				res, err := http.Post(server.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"test"}`))
				if err != nil {
					done <- 0
					return
				}
				_ = res.Body.Close()
				done <- res.StatusCode
			}()
			require.Eventually(t, func() bool { _, waiting, _ := cache.GetAPIKeyQueueStats(context.Background(), 7); return waiting == 1 }, time.Second, 10*time.Millisecond)
			require.False(t, accountEntered.Load())
			require.Zero(t, rdb.ZCard(context.Background(), "concurrency:account:11").Val())
			if revoke {
				revoked.Store(true)
			}
			require.NoError(t, cache.ReleaseAPIKeySlot(context.Background(), 7, "holder"))
			select {
			case status := <-done:
				if revoke {
					require.Equal(t, 401, status)
					require.False(t, accountEntered.Load())
				} else {
					require.Equal(t, 204, status)
					require.True(t, accountEntered.Load())
				}
			case <-time.After(4 * time.Second):
				t.Fatal("HTTP admission stuck")
			}
			active, waiting, err := cache.GetAPIKeyQueueStats(context.Background(), 7)
			require.NoError(t, err)
			require.Zero(t, active)
			require.Zero(t, waiting)
		})
	}
}

func TestAPIKeyProtocolRealResponsesHandlerWaitsBeforeSelection(t *testing.T) {
	svc, rdb := protocolAdmissionCache(t)
	cache := repository.NewConcurrencyCache(rdb, 1, 30).(service.APIKeySlotQueueCache)
	_, _, err := cache.AcquireAPIKeySlotWithTime(context.Background(), 902, 1, "holder")
	require.NoError(t, err)
	router, upstream, _, _, accounts := newNormalForwardHandlerFixtureWithAdmission(t, false, svc)
	server := httptest.NewServer(router)
	defer server.Close()
	done := make(chan int, 1)
	go func() {
		res, err := http.Post(server.URL+"/openai/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5.5","input":"queue-test","stream":true}`))
		if err != nil {
			done <- 0
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		done <- res.StatusCode
	}()
	require.Eventually(t, func() bool { _, w, _ := cache.GetAPIKeyQueueStats(context.Background(), 902); return w == 1 }, time.Second, 10*time.Millisecond)
	accounts.mu.Lock()
	selections := accounts.selectionCalls
	accounts.mu.Unlock()
	require.Zero(t, selections)
	require.Empty(t, upstream.snapshot())
	require.NoError(t, cache.ReleaseAPIKeySlot(context.Background(), 902, "holder"))
	select {
	case status := <-done:
		require.Equal(t, 200, status)
	case <-time.After(5 * time.Second):
		t.Fatal("Responses forwarding remained blocked")
	}
	require.NotEmpty(t, upstream.snapshot())
	require.Eventually(t, func() bool { a, w, _ := cache.GetAPIKeyQueueStats(context.Background(), 902); return a == 0 && w == 0 }, time.Second, 10*time.Millisecond)
}

func TestAPIKeyProtocolLiveHandlerWaitsBeforeCreation(t *testing.T) {
	svc, rdb := protocolAdmissionCache(t)
	cache := repository.NewConcurrencyCache(rdb, 1, 30).(service.APIKeySlotQueueCache)
	_, _, err := cache.AcquireAPIKeySlotWithTime(context.Background(), 7, 1, "holder")
	require.NoError(t, err)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	gateway := service.NewOpenAIGatewayService(&normalForwardHandlerAccounts{}, nil, nil, nil, nil, nil, nil, cfg, nil, svc, service.NewBillingService(cfg, nil), nil, billing, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, svc, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, cancel := service.WithAPIKeyAdmissionOwner(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, ConcurrencyLimit: 1, User: &service.User{ID: 9, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, AllowLive: true}})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9, Concurrency: 0})
		c.Next()
	})
	router.POST("/v1/live", h.Live)
	server := httptest.NewServer(router)
	defer server.Close()
	done := make(chan int, 1)
	go func() {
		res, err := http.Post(server.URL+"/v1/live", "application/json", strings.NewReader(`{"sdp":"v=0","session":{"model":"gpt-live"}}`))
		if err != nil {
			done <- 0
			return
		}
		_ = res.Body.Close()
		done <- res.StatusCode
	}()
	require.Eventually(t, func() bool { _, w, _ := cache.GetAPIKeyQueueStats(context.Background(), 7); return w == 1 }, time.Second, 10*time.Millisecond)
	require.NoError(t, cache.ReleaseAPIKeySlot(context.Background(), 7, "holder"))
	select {
	case status := <-done:
		require.Equal(t, 503, status, "unconfigured Live store/attestation remains unavailable after admission")
	case <-time.After(4 * time.Second):
		t.Fatal("Live create did not leave queue")
	}
	a, w, err := cache.GetAPIKeyQueueStats(context.Background(), 7)
	require.NoError(t, err)
	require.Zero(t, a)
	require.Zero(t, w)
}

func TestAPIKeyProtocolResponsesWebSocketQueueDisconnect(t *testing.T) {
	svc, rdb := protocolAdmissionCache(t)
	cache := repository.NewConcurrencyCache(rdb, 1, 30).(service.APIKeySlotQueueCache)
	_, _, err := cache.AcquireAPIKeySlotWithTime(context.Background(), 7, 1, "holder")
	require.NoError(t, err)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	gateway := service.NewOpenAIGatewayService(&normalForwardHandlerAccounts{}, nil, nil, nil, nil, nil, nil, cfg, nil, svc, service.NewBillingService(cfg, nil), nil, billing, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, svc, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, cancel := service.WithAPIKeyAdmissionOwner(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, ConcurrencyLimit: 1, User: &service.User{ID: 9, Status: service.StatusActive}})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9, Concurrency: 0})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", nil)
	require.NoError(t, err)
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`)))
	require.Eventually(t, func() bool { _, waiting, _ := cache.GetAPIKeyQueueStats(ctx, 7); return waiting == 1 }, time.Second, 10*time.Millisecond)
	_ = client.CloseNow()
	require.Eventually(t, func() bool { _, waiting, _ := cache.GetAPIKeyQueueStats(ctx, 7); return waiting == 0 }, time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, rdb.ZCard(ctx, "concurrency:api_key:7").Val(), "only original holder survives disconnect")
}
