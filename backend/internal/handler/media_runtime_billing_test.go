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

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
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
	copied := *u
	l.rows = append(l.rows, &copied)
	return true, nil
}

type mediaRuntimeBindings struct {
	*grokMediaSlotBindings
	releases int
}

func (b *mediaRuntimeBindings) ReleaseGrokVideoBilled(_ context.Context, key string) error {
	delete(b.billed, key)
	b.releases++
	return nil
}

func TestMediaRuntimeSeedanceCompletionSettlement(t *testing.T) {
	for _, compatible := range []bool{false, true} {
		t.Run(fmt.Sprint("compatible=", compatible), func(t *testing.T) { testMediaRuntimeSeedanceCompletionSettlement(t, compatible) })
	}
}

func testMediaRuntimeSeedanceCompletionSettlement(t *testing.T, compatible bool) {
	h, _, bindingBase, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	bindings := &mediaRuntimeBindings{grokMediaSlotBindings: bindingBase}
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
		require.Empty(t, bindings.billed)
	}
	if !compatible {
		call(http.MethodDelete)
	}
	require.Empty(t, ledger.applied)
	status = "succeeded"
	missingMetadata = true
	call(http.MethodGet)
	require.Empty(t, ledger.applied, "missing FPS/audio must not fall back to scalar price")
	require.Empty(t, bindings.billed, "pricing error releases claim for a later correct status")
	missingMetadata = false
	ledger.fail = true
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Empty(t, bindings.billed, "failed durable write releases claim")
	ledger.fail = false
	call(http.MethodGet)
	require.Len(t, ledger.applied, 1)
	taskKey := "seedance:task-ark"
	if compatible {
		taskKey = "compat-video:task-ark"
	}
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
	clear(bindings.billed)
	call(http.MethodGet)
	require.Len(t, ledger.applied, 1)
	require.Equal(t, attempts+1, ledger.attempts)
	if compatible {
		require.Equal(t, 1, bindings.releases)
	} else {
		require.Equal(t, 2, bindings.releases)
	}
}
