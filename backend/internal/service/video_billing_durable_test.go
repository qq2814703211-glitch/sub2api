//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type videoJobCaptureRepo struct {
	VideoBillingJobRepository
	job       *VideoBillingJob
	createErr error
}

func (r *videoJobCaptureRepo) Create(_ context.Context, j *VideoBillingJob) error {
	r.job = j
	return r.createErr
}

func TestDurableVideoFrozenPricingUnits(t *testing.T) {
	for _, mode := range []string{"request", "second", "free"} {
		t.Run(mode, func(t *testing.T) {
			svc, key := newVideoBillingTestService()
			key.ID = 2
			key.UserID = 1
			key.User = &User{ID: 1}
			key.Group.RateMultiplier = 1
			key.Group.VideoModelPrices = map[string]map[string]float64{"grok-imagine-video-1.5": {"720p": 0.025}}
			if mode != "second" {
				price := 0.2
				if mode == "free" {
					price = 0
				}
				key.Group.ModelPricing = []ChannelModelPricing{{Platform: PlatformComposite, Models: []string{"grok-imagine-video-1.5"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}
			}
			store := &videoJobCaptureRepo{}
			svc.SetVideoBillingJobs(store)
			in := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{Model: "grok-imagine-video-1.5"}, APIKey: key, User: key.User, Account: &Account{ID: 3, Type: AccountTypeAPIKey}}
			job, err := svc.BeginVideoBillingJob(context.Background(), in, GrokVideoPendingBilling{Model: in.Result.Model, VideoResolution: "720p"}, "grok")
			require.NoError(t, err)
			require.Equal(t, mode == "second", job.Snapshot.PerSecond)
			job.TaskID = "task-frozen"
			job.CreatedAt = time.Now()
			key.Group.ModelPricing = nil
			key.Group.VideoModelPrices["grok-imagine-video-1.5"]["720p"] = 99
			intent, err := videoIntentForCompletion(job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 10, VideoStatus: "done"})
			require.NoError(t, err)
			expected := 0.2
			if mode == "second" {
				expected = 0.25
			}
			if mode == "free" {
				expected = 0
			}
			require.InDelta(t, expected, intent.Command.BalanceCost, 1e-10)
			require.InDelta(t, expected, intent.Usage.ActualCost, 1e-10)
			require.Equal(t, "grok-video:task-frozen", intent.Command.RequestID)
		})
	}
}

func TestDurableVideoNoGuessedUsage(t *testing.T) {
	job := &VideoBillingJob{Provider: "seedance", Snapshot: VideoBillingSnapshot{TokenBilled: true}}
	_, err := videoIntentForCompletion(job, &OpenAIForwardResult{VideoStatus: "succeeded"})
	require.ErrorContains(t, err, "usage_missing")
	job.Provider = "grok"
	job.Snapshot.PerSecond = true
	_, err = videoIntentForCompletion(job, &OpenAIForwardResult{VideoCount: 1, VideoStatus: "done"})
	require.ErrorContains(t, err, "duration_missing")
	job.Snapshot.PerSecond = false
	_, err = videoIntentForCompletion(job, &OpenAIForwardResult{VideoCount: 1, VideoStatus: "done"})
	require.NoError(t, err, "request-priced completion does not require duration")
}

func TestDurableVideoRawTokenPriceAndZeroUsage(t *testing.T) {
	job := &VideoBillingJob{Provider: "seedance", TaskID: "seedance:one", CreatedAt: time.Now(), Snapshot: VideoBillingSnapshot{TokenBilled: true,
		Unit: VideoBillingIntent{RawAmounts: [5]float64{0.00000012345}, Usage: UsageLog{TotalCost: 0.00000012345, ActualCost: 0.00000012345}}}}
	result := &OpenAIForwardResult{VideoStatus: "succeeded", VideoUsageKnown: true, Usage: OpenAIUsage{OutputTokens: 1000000}}
	intent, err := videoIntentForCompletion(job, result)
	require.NoError(t, err)
	require.InDelta(t, 0.12345, intent.Command.BalanceCost, 1e-10)
	require.Equal(t, 1000000, intent.Command.OutputTokens)
	result.Usage.OutputTokens = 0
	intent, err = videoIntentForCompletion(job, result)
	require.NoError(t, err)
	require.Zero(t, intent.Command.BalanceCost)
}

func TestDurableVideoSeedanceMissingVersusZero(t *testing.T) {
	for _, tc := range []struct {
		body   string
		known  bool
		tokens int
	}{
		{`{"status":"succeeded"}`, false, 0}, {`{"status":"succeeded","usage":{"completion_tokens":0}}`, true, 0},
		{`{"status":"succeeded","usage":{"completion_tokens":12345}}`, true, 12345},
		{`{"status":"succeeded","usage":{"completion_tokens":-1}}`, false, 0},
		{`{"status":"succeeded","usage":{"completion_tokens":1.5}}`, false, 0},
		{`{"status":"running","usage":{"completion_tokens":12345}}`, false, 0},
	} {
		r := &OpenAIForwardResult{}
		applySeedanceVideoStatus(r, []byte(tc.body))
		require.Equal(t, tc.known, r.VideoUsageKnown, tc.body)
		require.Equal(t, tc.tokens, r.Usage.OutputTokens)
	}
	require.Equal(t, 60, actualGrokVideoSeconds([]byte(`{"video":{"duration":60}}`)))
	require.Equal(t, -1, actualGrokVideoSeconds([]byte(`{"video":{"duration":1.5}}`)))
	require.Zero(t, actualGrokVideoSeconds([]byte(`{}`)))
}

func TestDurableVideoCreateSnapshotMatchesRelayAdapter(t *testing.T) {
	account := grokMediaContentTestAccount()
	body := []byte(`{"model":"grok-imagine-video","prompt":"x"}`)
	official, err := VideoBillingCreateInfo(GrokMediaEndpointVideosGenerations, body, "application/json", account)
	require.NoError(t, err)
	require.False(t, official.DurationSpecified)
	account.Credentials["video_relay_compat"] = "true"
	relay, err := VideoBillingCreateInfo(GrokMediaEndpointVideosGenerations, body, "application/json", account)
	require.NoError(t, err)
	require.True(t, relay.DurationSpecified)
	require.Equal(t, 8, relay.DurationSeconds)
}

func TestDurableVideoPublishAfterPersistence(t *testing.T) {
	for _, provider := range []string{"seedance", "grok"} {
		t.Run(provider, func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, w := grokMediaContentTestContext(http.MethodPost, "/video", nil)
			ctx := ContextWithVideoBeforePublish(context.Background(), func(_ context.Context, result *OpenAIForwardResult) error {
				require.NotEmpty(t, result.ResponseID)
				require.Empty(t, w.Body.String())
				return errors.New("save failed")
			})
			if provider == "seedance" {
				upstream.response = grokMediaContentStatusResponse(`{"id":"task-1"}`)
				_, err := svc.ForwardSeedance(ctx, c, seedanceTestAccount(), SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"x"}]}`))
				require.ErrorContains(t, err, "save failed")
			} else {
				upstream.response = grokMediaContentStatusResponse(`{"request_id":"task-1"}`)
				_, err := svc.ForwardGrokMedia(ctx, c, grokMediaContentTestAccount(), GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"grok-imagine-video"}`), "application/json")
				require.ErrorContains(t, err, "save failed")
			}
			require.Empty(t, w.Body.String())
			require.Len(t, upstream.requests, 1)
		})
	}
}
