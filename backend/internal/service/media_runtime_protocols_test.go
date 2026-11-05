//go:build unit

package service

import (
	"context"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMediaRuntimeCompatibleVideoSelectorsAndBilling(t *testing.T) {
	for _, tc := range []struct {
		name, body, usage string
		conditions        MediaPricingConditions
		wantDuration      float64
	}{
		{"vidu reference 4k", `{"model":"viduq4-preview","images":[{"url":"https://example.test/ref.png","role":"reference_image"}],"resolution":"4K","duration":6}`, `{"duration":4.5}`, MediaPricingConditions{InputType: "reference", Resolution: "4k"}, 4.5},
		{"vidu image 2k", `{"model":"viduq4-preview","images":["https://example.test/frame.png"],"resolution":"2K","duration":6}`, `{"duration":4.5}`, MediaPricingConditions{InputType: "image", Resolution: "2k"}, 4.5},
		{"kling pro audio reference", `{"model":"kling-v3-omni","settings":{"resolution":"1080p","duration":6,"audio":"native"},"contents":[{"type":"base_video","url":"https://example.test/ref.mp4"}]}`, `{"duration":4.5}`, MediaPricingConditions{Mode: "pro", Audio: mediaRuntimeBool(true), ReferenceVideo: mediaRuntimeBool(true)}, 4.5},
		{"enhance 8k high fps", `{"model":"volc_video_enhance_professional","video_url":"https://example.test/input.mp4","resolution":"8k","fps":60}`, `{"duration":4.5,"fps":60}`, MediaPricingConditions{Resolution: "8k", MinFPS: mediaRuntimeFloat(math.Nextafter(30, math.Inf(1)))}, 4.5},
		{"minimax output seconds", `{"model":"MiniMax-H3","resolution":"2K","duration":6,"images":["https://example.test/a.png"]}`, `{"total_seconds":7.5,"input_seconds":3,"output_seconds":4.5}`, MediaPricingConditions{Resolution: "2k", InputType: "image"}, 4.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := seedanceTestAccount()
			account.Credentials["base_url"] = "https://relay.example/v1"
			account.Credentials["openai_capabilities"] = []string{"video_generations"}
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task-1","status":"pending"}`)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/video/generations", nil)
			created, err := svc.ForwardCompatibleVideo(context.Background(), c, account, CompatibleVideoEndpointCreate, "", []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, "https://relay.example/v1/video/generations", upstream.request.URL.String())
			forwarded, err := io.ReadAll(upstream.request.Body)
			require.NoError(t, err)
			require.JSONEq(t, tc.body, string(forwarded))
			require.Zero(t, created.MediaUsage.Count)
			upstream.response = grokMediaContentStatusResponse(`{"id":"task-1","status":"completed","data":[{"url":"https://example.test/result.mp4"}],"usage":` + tc.usage + `}`)
			c, _ = grokMediaContentTestContext(http.MethodGet, "/v1/video/generations/task-1", nil)
			done, err := svc.ForwardCompatibleVideo(context.Background(), c, account, CompatibleVideoEndpointStatus, created.ResponseID, nil)
			require.NoError(t, err)
			done.Model = created.Model
			done.MediaUsage = MergeMediaUsage(created.MediaUsage, done.MediaUsage)
			ApplyMediaUsageModelDimensions(done.MediaUsage, done.Model)
			require.Equal(t, tc.wantDuration, done.MediaUsage.DurationSeconds)
			billSvc, key := newVideoBillingTestService()
			key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{done.Model}, BillingMode: BillingModeVideo, MediaPricing: &MediaPricing{Rules: []MediaPricingRule{{Name: "fixture", Unit: "second", Conditions: tc.conditions, Price: ptrFloat(0.2)}}}}}
			cost, err := billSvc.calculateOpenAIRecordUsageCost(context.Background(), done, key, []string{done.Model}, 9, 8, 2, 1, UsageTokens{}, "", nil, false, time.Time{})
			require.NoError(t, err)
			require.InDelta(t, 0.9, cost.TotalCost, 1e-12)
			require.InDelta(t, 1.8, cost.ActualCost, 1e-12)
		})
	}
}
func mediaRuntimeFloat(v float64) *float64 { return &v }

func TestMediaRuntimeCompatibleScalarDoesNotInventDuration(t *testing.T) {
	svc, key := newVideoBillingTestService()
	key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"video-fixture"}, BillingMode: BillingModeVideo, PerRequestPrice: ptrFloat(0.2)}}
	result := &OpenAIForwardResult{Model: "video-fixture", ResponseID: "compat-video:task", VideoCount: 1, MediaUsage: &MediaUsage{Count: 1, Resolution: "4k", DurationSeconds: 4.5}}
	cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{result.Model}, 9, 8, 2, 1, UsageTokens{}, "", nil, false, time.Time{})
	require.NoError(t, err)
	require.InDelta(t, 1.8, cost.ActualCost, 1e-12)
	result.MediaUsage.DurationSeconds = 0
	_, err = svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{result.Model}, 9, 8, 2, 1, UsageTokens{}, "", nil, false, time.Time{})
	require.ErrorContains(t, err, "missing duration")
}

func TestMediaRuntimeGrokActualDurationPreservesUnnormalizedRequest(t *testing.T) {
	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-video","resolution":"4k","duration":12,"generate_audio":false,"mode":"pro","fps":60,"image":{"url":"https://example.test/input.png"}}`))
	pending := &GrokVideoPendingBilling{Model: info.Model, VideoResolution: info.Resolution, VideoDurationSeconds: info.DurationSeconds, MediaUsage: info.MediaUsage}
	result := ExtractGrokVideoBillingFromStatusBody([]byte(`{"status":"done","video":{"url":"https://example.test/out.mp4","duration":4.25}}`), pending, "task")
	require.NotNil(t, result)
	require.Equal(t, "4k", result.MediaUsage.Resolution, "do not collapse metadata to the legacy scalar tier")
	require.Equal(t, 4.25, result.MediaUsage.DurationSeconds)
	require.Equal(t, 60.0, result.MediaUsage.FPS)
	require.Equal(t, "image", result.MediaUsage.InputType)
	require.Equal(t, "pro", result.MediaUsage.Mode)
	require.False(t, *result.MediaUsage.Audio)
	require.False(t, *result.MediaUsage.ReferenceVideo)
	for _, status := range []string{"pending", "failed", "expired"} {
		require.Nil(t, ExtractGrokVideoBillingFromStatusBody([]byte(`{"status":"`+status+`","video":{"url":"https://example.test/out.mp4"}}`), pending, "task"))
	}
}

func TestMediaRuntimeCompatibleVideoFailureNeverCounts(t *testing.T) {
	for _, body := range []string{`{"status":"failed","data":[{"url":"not-a-success"}],"usage":{"completion_tokens":1000}}`, `{"status":"completed","data":[]}`, `{"status":"completed","error":{"code":"failure"},"data":[{"url":"not-a-success"}]}`} {
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		a := seedanceTestAccount()
		a.Credentials["openai_capabilities"] = []string{"video_generations"}
		c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/video/generations/task", nil)
		result, err := svc.ForwardCompatibleVideo(context.Background(), c, a, CompatibleVideoEndpointStatus, "compat-video:task", nil)
		require.NoError(t, err)
		require.Zero(t, result.MediaUsage.Count)
		require.Zero(t, result.Usage.OutputTokens)
	}
}
