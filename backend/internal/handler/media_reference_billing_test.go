//go:build unit

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Protocol/cache/ledger fixtures exercise real handlers and settlement. No paid
// supplier calls are made; durable Apply idempotency is represented by the seam.
func TestMediaReferenceMiniMaxHandlerPendingToLedger(t *testing.T) {
	for _, tc := range []struct {
		name, request, usage string
		want                 float64
		native               bool
	}{
		{"authoritative total", `{"images":["a","b","c","d","e","f","g","h","i"],"videos":["unknown-duration"]}`, `{"total_seconds":7,"input_seconds":3,"output_seconds":4,"input_image_count":6}`, 7.4, false},
		{"native task usage", `{"content":[{"type":"image_url","image_url":{"url":"a"},"role":"first_frame"},{"type":"video_url","video_url":{"url":"v"},"role":"reference_video"}]}`, `{"total_seconds":7,"input_seconds":3,"output_seconds":4,"input_image_count":6}`, 7.4, true},
		{"exact sum", `{"images":["a","b","c","d","e","f"],"videos":["unknown-duration"]}`, `{"input_seconds":3,"output_seconds":4,"input_image_count":6}`, 7.4, false},
		{"request reference seconds fallback", `{"images":["a","b","c","d","e","f"],"videos":[{"url":"v","duration":3}]}`, `{"output_seconds":4}`, 7.4, false},
		{"total only", `{"images":["a","b","c","d","e","f"],"videos":["unknown-duration"]}`, `{"total_seconds":7}`, 7.4, false},
		{"explicit zero overrides", `{"images":["a","b","c","d","e","f"],"videos":["unknown-duration"]}`, `{"total_seconds":4,"input_seconds":0,"output_seconds":4,"input_image_count":0}`, 4, false},
		{"no reference video", `{"images":["a"]}`, `{"output_seconds":4}`, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) { testReferenceVideoHandler(t, tc.request, tc.usage, tc.want, tc.native, nil, 10) })
	}
}

func testReferenceVideoHandler(t *testing.T, request, successUsage string, want float64, native bool, durable service.UsageBillingRepository, userID int64) {
	h, _, baseBindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
	bindings := &mediaRuntimeBindings{grokMediaSlotBindings: baseBindings}
	ledger := &mediaRuntimeLedger{applied: map[string]*service.UsageBillingCommand{}, durable: durable}
	logs := &mediaRuntimeLogs{}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	billing := service.NewBillingService(cfg, nil)
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://relay.example/v1", "openai_capabilities": []string{"video_generations"}}}
	repo := grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
	h.gatewayService = service.NewOpenAIGatewayService(repo, logs, ledger, nil, nil, nil, bindings, cfg, nil, nil, billing, nil, &service.BillingCacheService{}, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
	rate, extra, free := 0.5, 0.2, 5
	group := &service.Group{ID: 24, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 2, ModelPricing: []service.ChannelModelPricing{{Models: []string{"MiniMax-H3"}, BillingMode: service.BillingModeVideo, MediaPricing: &service.MediaPricing{ExtraInputImagePrice: &extra, FreeInputImages: &free, BillingDuration: service.MediaBillingDurationTotal, Rules: []service.MediaPricingRule{{Unit: "second", Conditions: service.MediaPricingConditions{Resolution: "2k"}, Price: &rate}}}}}}
	var submitted map[string]any
	require.NoError(t, json.Unmarshal([]byte(request), &submitted))
	submitted["model"] = "MiniMax-H3"
	submitted["resolution"] = "2K"
	submitted["duration"] = 15
	submitted["usage"] = map[string]any{"input_images": 0, "input_seconds": 0, "total_seconds": 0}
	payload, err := json.Marshal(submitted)
	require.NoError(t, err)
	state, usage := "pending", "{}"
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		body := `{"id":"input-fee-task","status":"pending"}`
		if req.Method == http.MethodGet {
			body = fmt.Sprintf(`{"id":"input-fee-task","status":%q,"data":[{"url":"https://example.test/out.mp4"}],"usage":%s}`, state, usage)
			if native {
				ns := state
				if ns == "completed" {
					ns = "succeeded"
				}
				body = fmt.Sprintf(`{"task":{"status":%q,"content":{"url":"https://example.test/out.mp4"},"usage":%s}}`, ns, usage)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	call := func(method string) {
		c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
		key, _ := middleware.GetAPIKeyFromContext(c)
		key.Group = group
		key.UserID = userID
		key.User.ID = userID
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 5})
		body := ""
		if method == http.MethodPost {
			body = string(payload)
		}
		c.Request = httptest.NewRequest(method, "/v1/video/generations/input-fee-task", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "task_id", Value: "input-fee-task"}}
		h.CompatibleVideoTasks(c)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	call(http.MethodPost)
	require.Empty(t, ledger.applied)
	require.Len(t, bindings.pending, 1)
	var original []byte
	var pendingKey string
	for key, raw := range bindings.pending {
		original = append([]byte(nil), raw...)
		pendingKey = key
		var snapshot service.GrokVideoPendingBilling
		require.NoError(t, json.Unmarshal(raw, &snapshot))
		require.Equal(t, service.ExtractMediaRequestUsage(payload).InputImageCount, snapshot.MediaUsage.InputImageCount)
		require.Nil(t, snapshot.MediaUsage.TotalDurationSeconds, "client usage cannot enter pending")
	}
	for _, s := range []string{"pending", "failed", "cancelled"} {
		state = s
		call(http.MethodGet)
		require.Empty(t, ledger.applied)
		require.Empty(t, bindings.billed)
	}
	state = "completed"
	// Unknown output must not fall back to the requested 15 seconds.
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Empty(t, bindings.billed)
	require.Len(t, bindings.pending, 1)
	// Simulate an older pending snapshot with no input counters. Exact duration
	// alone must not silently turn unknown image input into zero-priced input.
	var old service.GrokVideoPendingBilling
	require.NoError(t, json.Unmarshal(original, &old))
	old.MediaUsage.InputImageCount = nil
	old.MediaUsage.InputDurationSeconds = nil
	raw, err := json.Marshal(old)
	require.NoError(t, err)
	bindings.pending[pendingKey] = raw
	usage = `{"total_seconds":7,"output_seconds":4,"input_seconds":3}`
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Empty(t, bindings.billed)
	require.Len(t, bindings.pending, 1)
	// A later authoritative response can settle even this old snapshot.
	bindings.pending[pendingKey] = original
	usage = successUsage
	ledger.fail = true
	call(http.MethodGet)
	require.Empty(t, ledger.applied)
	require.Empty(t, bindings.billed)
	ledger.fail = false
	call(http.MethodGet)
	require.Len(t, ledger.applied, 1)
	command := ledger.applied[service.StableGrokVideoBillingRequestID("compat-video:input-fee-task")]
	require.NotNil(t, command)
	require.InDelta(t, want, command.BalanceCost, 1e-12)
	require.InDelta(t, want, logs.rows[len(logs.rows)-1].ActualCost, 1e-12)
	require.InDelta(t, want/2, logs.rows[len(logs.rows)-1].TotalCost, 1e-12)
	attempts := ledger.attempts
	for range 5 {
		call(http.MethodGet)
	}
	require.Equal(t, attempts, ledger.attempts)
	clear(bindings.billed)
	call(http.MethodGet)
	require.Equal(t, attempts+1, ledger.attempts)
	require.Len(t, ledger.applied, 1)
}

func TestMediaReferenceHandlerPostgresLedgerIdempotency(t *testing.T) {
	dsn := os.Getenv("MEDIA_PRICING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated media pricing PostgreSQL DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))
	var userID int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES ('input-fees-ledger@example.test','fixture',100) RETURNING id`).Scan(&userID))
	requestID := service.StableGrokVideoBillingRequestID("compat-video:input-fee-task")
	t.Cleanup(func() {
		_, err := db.Exec(`DELETE FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=20`, requestID)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM users WHERE id=$1`, userID)
		require.NoError(t, err)
	})
	testReferenceVideoHandler(t, `{"images":["a","b","c","d","e","f"],"videos":["unknown-duration"]}`, `{"total_seconds":7,"input_seconds":3,"output_seconds":4,"input_image_count":6}`, 7.4, false, repository.NewUsageBillingRepository(nil, db), userID)
	var balance float64
	var count int
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance))
	require.InDelta(t, 92.6, balance, 1e-8)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=20`, requestID).Scan(&count))
	require.Equal(t, 1, count)
}
