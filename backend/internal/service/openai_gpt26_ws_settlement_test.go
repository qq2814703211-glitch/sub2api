//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGPT26WSPassthroughRecordUsage(t *testing.T) {
	gpt26RestoreGinMode(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	billing := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	billing.resolver = NewModelPricingResolver(nil, billing.billingService)
	group := &Group{ID: 123, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.1"}, BillingMode: BillingModeToken, InputPrice: f64p(0.01), OutputPrice: f64p(0.02), ImageInputPrice: f64p(0.03), ImageOutputPrice: f64p(0.04), CacheReadPrice: f64p(0.005)}}}
	resultCh := make(chan *OpenAIForwardResult, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
			if err == nil && result != nil {
				resultCh <- result
			}
		}}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	const tool = `"tool_usage":{"image_gen":{"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439},"cached_tokens":12,"cached_tokens_details":{"cached_image_tokens":10,"cached_text_tokens":2}}}`
	progress := `{"type":"response.in_progress","response":{"id":"r_image","usage":{"input_tokens":5,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}},` + tool + `}}`
	terminal := `{"type":"response.completed","response":{"id":"r_image","model":"gpt-5.1","usage":{"input_tokens":5,"output_tokens":9,"input_tokens_details":{"cached_tokens":2}}}}`
	for _, event := range []string{progress, progress, terminal, terminal} {
		upstream.Send(event)
		_, err := readPassthroughLifecycleFrame(t, client, time.Second)
		require.NoError(t, err)
	}
	var result *OpenAIForwardResult
	select {
	case result = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("missing settled websocket turn")
	}
	require.Equal(t, 376, result.Usage.InputTokens)
	require.Equal(t, 448, result.Usage.OutputTokens)
	require.Equal(t, 352, result.Usage.ImageInputTokens)
	require.Equal(t, 10, result.Usage.ImageCacheReadTokens)
	require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 10123, GroupID: i64p(123), Group: group}, User: &User{ID: 20123}, Account: account}))
	require.InDelta(t, 20*0.01, usageRepo.lastLog.InputCost, 1e-10)
	require.InDelta(t, 342*0.03, usageRepo.lastLog.ImageInputCost, 1e-10)
	require.InDelta(t, 9*0.02, usageRepo.lastLog.OutputCost, 1e-10)
	require.InDelta(t, 439*0.04, usageRepo.lastLog.ImageOutputCost, 1e-10)
	require.InDelta(t, 14*0.005, usageRepo.lastLog.CacheReadCost, 1e-10)
	require.InDelta(t, 20*0.01+342*0.03+9*0.02+439*0.04+14*0.005, userRepo.lastAmount, 1e-10)
	require.Equal(t, 1, userRepo.deductCalls)
	_ = client.CloseNow()
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket handler did not finish")
	}
	select {
	case extra := <-resultCh:
		t.Fatalf("duplicate settled turn: %+v", extra)
	default:
	}
}
