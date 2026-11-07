//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Protocol and repository fixtures: this exercises the actual handler,
// forwarding, snapshot reader, resolver and RecordUsage; no supplier/DB calls.
type mediaRuntimeLedger struct {
	service.UsageBillingRepository
	attempts int
	applied  map[string]*service.UsageBillingCommand
	fail     bool
	durable  service.UsageBillingRepository
}

func (l *mediaRuntimeLedger) Apply(ctx context.Context, c *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	l.attempts++
	if l.fail {
		return nil, errors.New("fixture durable write failed")
	}
	if l.durable != nil {
		result, err := l.durable.Apply(ctx, c)
		if err != nil || !result.Applied {
			return result, err
		}
	}
	if old := l.applied[c.RequestID]; old != nil {
		if old.RequestFingerprint != c.RequestFingerprint {
			return nil, service.ErrUsageBillingRequestConflict
		}
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	copied := *c
	l.applied[c.RequestID] = &copied
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type mediaRuntimeLogs struct {
	service.UsageLogRepository
	rows []*service.UsageLog
}

func (l *mediaRuntimeLogs) Create(_ context.Context, u *service.UsageLog) (bool, error) {
	for _, existing := range l.rows {
		if existing.RequestID == u.RequestID && existing.APIKeyID == u.APIKeyID {
			return false, nil
		}
	}
	copied := *u
	l.rows = append(l.rows, &copied)
	return true, nil
}

type mediaRuntimeBindings struct {
	*grokMediaSlotBindings
	releases      int
	storeFailures int
	stores        int
	storeErr      error
	bindErr       error
	beforeStore   func()
	leaseCache    service.GatewayCache
}

func (b *mediaRuntimeBindings) SetSessionAccountID(ctx context.Context, groupID int64, key string, owner int64, ttl time.Duration) error {
	if b.bindErr != nil {
		return b.bindErr
	}
	return b.grokMediaSlotBindings.SetSessionAccountID(ctx, groupID, key, owner, ttl)
}

func (b *mediaRuntimeBindings) SetGrokVideoPendingBilling(ctx context.Context, key string, body []byte, ttl time.Duration) error {
	b.stores++
	if b.beforeStore != nil {
		b.beforeStore()
	}
	if b.storeErr != nil || b.stores <= b.storeFailures {
		return errors.New("fixture snapshot storage unavailable")
	}
	return b.grokMediaSlotBindings.SetGrokVideoPendingBilling(ctx, key, body, ttl)
}

func (b *mediaRuntimeBindings) ClaimGrokVideoBilled(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	if b.leaseCache != nil {
		return b.leaseCache.ClaimGrokVideoBilled(context.Background(), key, owner, 2*time.Minute)
	}
	return b.grokMediaSlotBindings.ClaimGrokVideoBilled(context.Background(), key, owner, 2*time.Minute)
}

func (b *mediaRuntimeBindings) ReleaseGrokVideoBilled(_ context.Context, key, owner string) error {
	b.releases++
	if b.leaseCache != nil {
		return b.leaseCache.ReleaseGrokVideoBilled(context.Background(), key, owner)
	}
	if owner != "" {
		delete(b.billed, key)
	}
	return nil
}

func TestMediaRuntimeSeedanceCompletionSettlement(t *testing.T) {
	for _, compatible := range []bool{false, true} {
		t.Run(fmt.Sprint("compatible=", compatible), func(t *testing.T) { testMediaRuntimeSeedanceCompletionSettlement(t, compatible) })
	}
}

func testMediaRuntimeSeedanceCompletionSettlement(t *testing.T, compatible bool) {
	h, _, bindingBase, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	bindings := &mediaRuntimeBindings{grokMediaSlotBindings: bindingBase, leaseCache: repository.NewGatewayCache(rdb)}
	ledger := &mediaRuntimeLedger{applied: map[string]*service.UsageBillingCommand{}}
	logs := &mediaRuntimeLogs{}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	billing := service.NewBillingService(cfg, nil)
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "fixture-ark-key", "base_url": "https://ark.cn-beijing.volces.com/api/v3", "openai_capabilities": []string{"seedance"}}}
	if compatible {
		account.Credentials["base_url"] = "https://relay.example/v1"
		account.Credentials["openai_capabilities"] = []string{"video_generations"}
	}
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
	h.gatewayService = service.NewOpenAIGatewayService(repo, logs, ledger, nil, nil, nil, bindings, cfg, nil, nil, billing, nil, &service.BillingCacheService{}, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	tBool, fBool := true, false
	price := 0.0001
	fps := 24.0
	group := &service.Group{ID: 24, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 2, ModelPricing: []service.ChannelModelPricing{{Models: []string{"seedance-fixture"}, BillingMode: service.BillingModeToken, MediaPricing: &service.MediaPricing{Rules: []service.MediaPricingRule{{Name: "reference video actual resolution", Unit: "token", Conditions: service.MediaPricingConditions{Resolution: "1080p", ReferenceVideo: &tBool, Audio: &fBool, FPS: &fps}, OutputPrice: &price}}}}}}
	if compatible {
		group.ModelPricing[0].MediaPricing.Rules[0].Conditions.Audio = nil
		group.ModelPricing[0].MediaPricing.Rules[0].Conditions.FPS = nil
	}
	newContext := func(method, task string) (*gin.Context, *httptest.ResponseRecorder) {
		c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
		key, _ := middleware.GetAPIKeyFromContext(c)
		key.Group = group
		body := ""
		if method == http.MethodPost {
			body = `{"model":"seedance-fixture","resolution":"4k","duration":-1,"generate_audio":true,"content":[{"type":"video_url","video_url":{"url":"https://example.test/reference.mp4"},"role":"reference_video"}]}`
		}
		path := "/api/v3/contents/generations/tasks/" + task
		if compatible {
			path = "/v1/video/generations/" + task
			if method == http.MethodPost {
				body = `{"model":"seedance-fixture","resolution":"1080p","duration":-1,"generate_audio":false,"videos":[{"url":"https://example.test/reference.mp4","role":"reference_video"}]}`
			}
		}
		c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Request-ID", "poll-local-"+task)
		c.Params = gin.Params{{Key: "task_id", Value: task}}
		return c, w
	}
	status := "queued"
	task := "task-ark"
	missingMetadata := false
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		code := 200
		body := fmt.Sprintf(`{"id":%q}`, task)
		if req.Method == http.MethodGet {
			body = fmt.Sprintf(`{"id":%q,"model":"seedance-fixture","status":%q,"content":{"video_url":"https://example.test/result.mp4"},"resolution":"1080p","duration":11,"framespersecond":24,"generate_audio":false,"usage":{"completion_tokens":1000}}`, task, status)
			if missingMetadata {
				body = fmt.Sprintf(`{"id":%q,"status":"succeeded","content":{"video_url":"https://example.test/result.mp4"},"usage":{"completion_tokens":1000}}`, task)
			}
		}
		if compatible {
			require.True(t, strings.HasPrefix(req.URL.Path, "/v1/video/generations"), req.URL.String())
			if req.Method == http.MethodGet {
				compatStatus := status
				if status == "succeeded" {
					compatStatus = "completed"
				}
				body = fmt.Sprintf(`{"id":%q,"status":%q,"data":[{"url":"https://example.test/result.mp4"}],"usage":{"completion_tokens":1000}}`, task, compatStatus)
				if missingMetadata {
					body = fmt.Sprintf(`{"id":%q,"status":"completed","data":[{"url":"https://example.test/result.mp4"}],"usage":{"completion_tokens":0}}`, task)
				}
			}
		}
		if req.Method == http.MethodDelete {
			code = 204
			body = ""
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	call := func(method string) {
		c, w := newContext(method, task)
		if compatible {
			h.CompatibleVideoTasks(c)
		} else {
			h.SeedanceTasks(c)
		}
		if method == http.MethodDelete {
			require.Equal(t, 204, w.Code, w.Body.String())
		} else {
			require.Equal(t, 200, w.Code, w.Body.String())
		}
	}
	call(http.MethodPost)
	require.Empty(t, ledger.applied, "create must not charge")
	require.Len(t, bindings.pending, 1)
	for _, raw := range bindings.pending {
		var snapshot service.GrokVideoPendingBilling
		require.NoError(t, json.Unmarshal(raw, &snapshot))
		require.NotNil(t, snapshot.MediaUsage)
		if compatible {
			require.Equal(t, "1080p", snapshot.MediaUsage.Resolution)
			require.False(t, *snapshot.MediaUsage.Audio)
		} else {
			require.Equal(t, "4k", snapshot.MediaUsage.Resolution)
			require.True(t, *snapshot.MediaUsage.Audio)
		}
		require.True(t, *snapshot.MediaUsage.ReferenceVideo)
		require.Zero(t, snapshot.MediaUsage.Count)
	}
	for _, state := range []string{"queued", "running", "failed", "cancelled", "expired"} {
		status = state
		call(http.MethodGet)
		require.Empty(t, ledger.applied)
		require.Empty(t, server.Keys())
	}
	if !compatible {
		call(http.MethodDelete)
	}
	require.Empty(t, ledger.applied)
	status = "succeeded"
	missingMetadata = true
	call(http.MethodGet)
	require.Empty(t, ledger.applied, "missing FPS/audio must not fall back to scalar price")
	require.Empty(t, server.Keys(), "pricing error releases claim for a later correct status")
	missingMetadata = false
	ledger.fail = true
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Empty(t, server.Keys(), "failed durable write releases claim")
	require.Empty(t, logs.rows, "failed settlement must not occupy the successful usage-log key")
	ledger.fail = false
	taskKey := "seedance:task-ark"
	if compatible {
		taskKey = "compat-video:task-ark"
	}
	// The first poll claims, then its worker disappears before RecordUsage.
	owner, err := h.gatewayService.ClaimGrokVideoBilling(context.Background(), taskKey, 10, 20)
	require.NoError(t, err)
	require.NotEmpty(t, owner)
	attemptsBeforeRecovery := ledger.attempts
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Equal(t, attemptsBeforeRecovery, ledger.attempts)
	server.FastForward(2*time.Minute + time.Second)
	// Recovery requires another poll after TTL; expiry alone does not bill.
	call(http.MethodGet)
	require.Len(t, ledger.applied, 1)
	cmd := ledger.applied[service.StableGrokVideoBillingRequestID(taskKey)]
	require.NotNil(t, cmd)
	require.InDelta(t, 0.2, cmd.BalanceCost, 1e-12)
	require.Equal(t, 1000, cmd.OutputTokens)
	require.InDelta(t, 0.1, logs.rows[len(logs.rows)-1].TotalCost, 1e-12)
	require.InDelta(t, 0.2, logs.rows[len(logs.rows)-1].ActualCost, 1e-12)
	attempts := ledger.attempts
	for range 20 {
		call(http.MethodGet)
	}
	require.Equal(t, attempts, ledger.attempts, "repeated polls are stopped before durable billing")
	// Redis claim loss still sends exactly the same stable task ID/fingerprint to
	// the durable boundary, despite different HTTP poll requests.
	server.FastForward(2*time.Minute + time.Second)
	call(http.MethodGet)
	require.Len(t, ledger.applied, 1)
	require.Equal(t, attempts+1, ledger.attempts)
	require.Len(t, logs.rows, 1)
	if compatible {
		require.Equal(t, 1, bindings.releases)
	} else {
		require.Equal(t, 2, bindings.releases)
	}
}

func TestMediaRuntimeCreateResponseWaitsForRecoveryState(t *testing.T) {
	for _, endpoint := range []service.GrokMediaEndpoint{
		service.GrokMediaEndpointVideosGenerations, service.GrokMediaEndpointVideosEdits,
		service.GrokMediaEndpointVideosExtensions, service.SeedanceEndpointCreate, service.CompatibleVideoEndpointCreate,
	} {
		for _, scenario := range []string{"stored", "retry", "snapshot unavailable", "binding unavailable", "immediate artifact"} {
			if scenario == "immediate artifact" && endpoint.IsOpenAIAsyncVideo() {
				continue // these protocols require a task ID
			}
			t.Run(string(endpoint)+"/"+scenario, func(t *testing.T) {
				h, slots, base, upstream := newGrokMediaSlotHandler(t, false, false)
				bindings := &mediaRuntimeBindings{grokMediaSlotBindings: base}
				platform, capabilities := service.PlatformGrok, []string(nil)
				body := `{"model":"grok-imagine-video","prompt":"test","duration":6,"video":{"url":"https://example.test/input.mp4"}}`
				response := `{"request_id":"task","status":"pending"}`
				if endpoint.IsOpenAIAsyncVideo() {
					platform = service.PlatformOpenAI
					capabilities = []string{"seedance"}
					body = `{"model":"video-fixture","content":[{"type":"text","text":"test"}]}`
					response = `{"id":"task"}`
					if endpoint.IsCompatibleVideo() {
						capabilities = []string{"video_generations"}
						body = `{"model":"video-fixture","prompt":"test"}`
					}
				}
				account := service.Account{ID: 1, Platform: platform, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24},
					Credentials: map[string]any{"api_key": "fixture-key", "openai_capabilities": capabilities}}
				if platform == service.PlatformOpenAI {
					account.Credentials["base_url"] = "https://relay.example/v1"
				}
				repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				h.gatewayService = service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, bindings, cfg, nil,
					service.NewConcurrencyService(slots), nil, nil, nil, upstream, nil, nil, service.NewGrokTokenProvider(repo, nil), nil, nil, nil, nil, nil)
				c, w := grokMediaSlotContext(context.Background(), true)
				c.Request = httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				key, _ := middleware.GetAPIKeyFromContext(c)
				key.Group.Platform = platform
				bindings.beforeStore = func() {
					require.False(t, c.Writer.Written(), "task response must not precede its snapshot")
					require.Empty(t, w.Body.String())
				}
				switch scenario {
				case "retry":
					bindings.storeFailures = 1
				case "snapshot unavailable":
					bindings.storeErr = errors.New("snapshot unavailable")
				case "binding unavailable":
					bindings.bindErr = errors.New("binding unavailable")
				case "immediate artifact":
					response = `{"status":"done","video":{"url":"https://example.test/result.mp4","duration":6}}`
				}
				upstream.call = func(*http.Request, int64) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(response))}, nil
				}
				h.handleGrokMedia(c, endpoint, "")
				slots.assertReleased(t)
				require.Equal(t, 1, upstream.calls, "ambiguous creates must not be resubmitted")
				if scenario == "snapshot unavailable" || scenario == "binding unavailable" {
					require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
					require.Contains(t, w.Body.String(), "billing_state_unavailable")
					require.NotContains(t, w.Body.String(), `"request_id":"task"`)
					require.Empty(t, bindings.pending)
				} else {
					require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
					require.JSONEq(t, response, w.Body.String())
					if scenario != "immediate artifact" {
						require.Len(t, bindings.pending, 1)
					}
				}
				if scenario == "retry" || scenario == "snapshot unavailable" {
					require.Equal(t, 2, bindings.stores)
				}
			})
		}
	}
}
