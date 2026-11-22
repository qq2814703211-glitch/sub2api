//go:build unit

package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Persist the create snapshot and reload it without consulting the catalog.
func TestCatalogUnitsVideoFrozenAtCreate(t *testing.T) {
	for _, tc := range []struct {
		mode, rate string
		want       float64
	}{{"per_request", "cost_per_request", 0.75}, {"per_second", "cost_per_second", 1.78125}} {
		t.Run(tc.mode, func(t *testing.T) {
			svc, in, _, _ := unitCatalogGateway(t, `"example-call":{"glory_billing_mode":"`+tc.mode+`","`+tc.rate+`":0.375}`)
			svc.SetVideoBillingJobs(&videoJobCaptureRepo{})
			job, err := svc.BeginVideoBillingJob(context.Background(), in, GrokVideoPendingBilling{Model: "example-call", VideoDurationSeconds: 15}, "grok")
			require.NoError(t, err)
			job.TaskID = "fixture-video"
			job.CreatedAt = time.Now()
			blob, err := json.Marshal(job)
			require.NoError(t, err)
			var frozen VideoBillingJob
			require.NoError(t, json.Unmarshal(blob, &frozen))
			result := ExtractGrokVideoBillingFromStatusBody([]byte(`{"status":"done","video":{"url":"https://example.test/v","duration":2.375}}`), nil, job.TaskID)
			require.NotNil(t, result)
			bill, err := videoIntentForCompletion(&frozen, result)
			require.NoError(t, err)
			require.Equal(t, tc.want, bill.Command.BalanceCost)
			require.Equal(t, tc.want, bill.Command.APIKeyQuotaCost)
			require.Equal(t, tc.mode, *bill.Usage.BillingMode)
			repeat, err := videoIntentForCompletion(&frozen, result)
			require.NoError(t, err)
			require.Equal(t, bill.Command.RequestID, repeat.Command.RequestID)
			require.Equal(t, bill.Command.BalanceCost, repeat.Command.BalanceCost)
			missing := ExtractGrokVideoBillingFromStatusBody([]byte(`{"status":"done","video":{"url":"https://example.test/v"}}`), &frozen.Snapshot.Pending, job.TaskID)
			_, err = videoIntentForCompletion(&frozen, missing)
			if tc.mode == "per_second" {
				require.ErrorContains(t, err, "provider billable seconds")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCatalogUnitsBatchUsesPerItemOrActualArtifacts(t *testing.T) {
	for _, tc := range []struct {
		mode, field string
		cost        float64
	}{{"per_request", "cost_per_request", 0.1875}, {"per_image", "output_cost_per_image", 0.375}} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			gw, _, logs, _ := unitCatalogGateway(t, `"gemini-2.5-flash-image":{"glory_billing_mode":"`+tc.mode+`","`+tc.field+`":0.375}`)
			pub, repo, _, _, _ := newTestBatchImagePublicService(true)
			pub.Pricing = &BatchImageModelPricingResolver{Resolver: gw.resolver}
			receipt, err := pub.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
			require.NoError(t, err)
			job := repo.jobs[receipt.ID]
			job.Status = BatchImageJobStatusSettling
			job.SuccessCount = 1
			job.FailCount = 1
			repo.items[job.BatchID] = []CreateBatchImageItemParams{{JobID: job.BatchID, CustomID: "one", Status: BatchImageParsedStatusSucceeded, ImageCount: 2}, {JobID: job.BatchID, CustomID: "two", Status: BatchImageParsedStatusFailed}}
			svc := &BatchImageSettlementService{Repo: repo, Pricing: &fakeBatchImagePricingResolver{unitPrice: 99}, BillingRepo: pub.BillingRepo, UsageLogRepo: logs}
			bill, err := svc.Settle(ctx, job.BatchID)
			require.NoError(t, err)
			require.Equal(t, tc.cost, bill.ActualCost)
			require.Equal(t, 2, logs.lastLog.ImageCount)
			require.Equal(t, tc.mode, *logs.lastLog.BillingMode)
			_, err = svc.Settle(ctx, job.BatchID)
			require.NoError(t, err)
			require.Len(t, pub.BillingRepo.(*fakeBatchImageBillingRepo).captures, 1)
		})
	}
}

func TestCatalogUnitsChatWithoutReportedTokens(t *testing.T) {
	svc, in, _, ledger := unitCatalogGateway(t, `"grok-4.5":{"glory_billing_mode":"per_request","cost_per_request":0.375}`)
	account := rawChatCompletionsTestAccount()
	account.Extra = map[string]any{"openai_responses_supported": false}
	svc.cfg = rawChatCompletionsTestConfig()
	svc.httpUpstream = unitUpstream(`{"id":"resp","object":"chat.completion","model":"grok-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	body := `{"model":"grok-4.5","messages":[{"role":"user","content":"hello"}]}`
	c := unitForwardContext(body, "/v1/chat/completions")
	c.Set("api_key", in.APIKey)
	var err error
	in.Result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), "", "")
	require.NoError(t, err)
	in.Account = account
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.Equal(t, 0.75, ledger.lastCmd.BalanceCost)
}

func TestCatalogUnitsVideoPerArtifactDurations(t *testing.T) {
	svc, in, logs, ledger := unitCatalogGateway(t, `"video-test":{"glory_billing_mode":"per_second","cost_per_second":0.25}`)
	account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "fixture", "openai_capabilities": []string{"video_generations"}}}
	cases := []struct {
		name, body string
		cost       float64
		missing    bool
	}{
		{"different lengths", `{"data":[{"url":"a","duration":1.25},{"url":"b","duration":2.375},{"error":"failed","duration":99}]}`, 1.8125, false},
		{"incomplete set", `{"data":[{"url":"a","duration":1.25},{"url":"b"}]}`, 0, true},
		{"provider total", `{"data":[{"url":"a"},{"url":"b"}],"usage":{"total_seconds":4.75}}`, 2.375, false},
		{"reported frames", `{"data":[{"url":"a"}],"usage":{"frames":57,"fps":24}}`, 1.1875, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"status":"completed",` + tc.body[1:]
			svc.httpUpstream = unitUpstream(body)
			c := unitForwardContext("", "/v1/video/generations/t")
			res, err := svc.ForwardCompatibleVideo(context.Background(), c, account, CompatibleVideoEndpointStatus, "compat-video:t", nil)
			require.NoError(t, err)
			in.Result = res
			res.Model = "video-test"
			res.MediaUsage.DurationSeconds = 98 // pending request dimensions must not become measured usage
			calls := ledger.calls
			err = svc.RecordUsage(context.Background(), in)
			if tc.missing {
				require.ErrorContains(t, err, "provider billable seconds")
				require.Equal(t, calls, ledger.calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.cost, logs.lastLog.ActualCost)
		})
	}
}
