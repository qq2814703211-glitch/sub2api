//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

// Synthetic fixtures and explicit operator prices, not upstream price claims.
// Hosted usage is aggregated once; the two output items do not multiply it.
func TestIndependentGPT26ResponsesSettlement(t *testing.T) {
	gpt26RestoreGinMode(t)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		for _, stream := range []bool{false, true} {
			for _, cached := range []bool{false, true} {
				for _, passthrough := range []bool{false, true} {
					if passthrough && accountType != AccountTypeAPIKey {
						continue
					}
					t.Run(fmt.Sprintf("%s/stream=%t/cache=%t/passthrough=%t", accountType, stream, cached, passthrough), func(t *testing.T) {
						const model = "gpt-image-2.6-flare"
						body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-luna","input":"draw","stream":%t,"tools":[{"type":"image_generation","model":"gpt-image-2.6-flare","complexity":"high"}]}`, stream))
						payload := gpt26ResponsesPayload(model)
						if cached {
							var err error
							payload, err = sjson.Set(payload, "usage.input_tokens_details.cached_tokens", 2)
							require.NoError(t, err)
							payload, err = sjson.Set(payload, "tool_usage.image_gen.cached_tokens", 12)
							require.NoError(t, err)
							payload, err = sjson.Set(payload, "tool_usage.image_gen.cached_tokens_details.cached_image_tokens", 10)
							require.NoError(t, err)
							payload, err = sjson.Set(payload, "tool_usage.image_gen.cached_tokens_details.cached_text_tokens", 2)
							require.NoError(t, err)
						}
						ct := "application/json"
						if accountType != AccountTypeAPIKey || stream {
							// Repeated progressive snapshots plus final driver-only usage exercise
							// component retention through both streaming and SSE-to-JSON forwarding.
							progress := "data: " + `{"type":"response.in_progress","response":` + payload + "}\n\n"
							if cached {
								var err error
								payload, err = sjson.Delete(payload, "tool_usage")
								require.NoError(t, err)
							}
							payload = progress + progress + "data: " + `{"type":"response.completed","response":` + payload + "}\n\n"
							ct = "text/event-stream"
						}
						upstream := &httpUpstreamRecorder{resp: gpt26Response(payload, ct)}
						svc := newOpenAIImagesTestService(upstream)
						c, _ := newOpenAIImagesTestContext(t, body)
						c.Request.URL.Path = "/v1/responses"
						SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
						account := directImagesTestAccount()
						account.Type = accountType
						account.Credentials["api_key"] = "sk-test"
						account.Credentials["base_url"] = "https://api.openai.com/v1"
						account.Extra = map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}
						result, err := svc.Forward(context.Background(), c, account, body)
						require.NoError(t, err)
						usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
						userRepo := &openAIRecordUsageUserRepoStub{}
						billingSvc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
						// Exact operator catalog entry supplies the image cache rate; no USD fallback.
						catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: {InputCostPerToken: 0.01, OutputCostPerToken: 0.02, InputCostPerImageToken: 0.03, OutputCostPerImageToken: 0.04, CacheReadInputTokenCost: 0.005, CacheReadInputImageTokenCost: 0.006}}}
						billingSvc.billingService = NewBillingService(&config.Config{}, catalog)
						billingSvc.resolver = NewModelPricingResolver(nil, billingSvc.billingService)
						group := &Group{ID: 123, RateMultiplier: 1, ImageCountBilling: true, ModelPricing: []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken, InputPrice: f64p(0.01), OutputPrice: f64p(0.02), ImageInputPrice: f64p(0.03), ImageOutputPrice: f64p(0.04), CacheReadPrice: f64p(0.005)}}}
						err = billingSvc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 10123, GroupID: i64p(123), Group: group}, User: &User{ID: 20123}, Account: account})
						require.NoError(t, err)
						require.NotNil(t, usageRepo.lastLog)
						textInput, imageInput, cacheCost := 24, 352, 0.0
						if cached {
							textInput = 20
							imageInput = 342
							cacheCost = 4*0.005 + 10*0.006
						}
						t.Logf("parsed=%+v text_input=%g image_input=%g text_output=%g image_output=%g cache=%g total=%g deducted=%g", result.Usage, usageRepo.lastLog.InputCost, usageRepo.lastLog.ImageInputCost, usageRepo.lastLog.OutputCost, usageRepo.lastLog.ImageOutputCost, usageRepo.lastLog.CacheReadCost, usageRepo.lastLog.TotalCost, userRepo.lastAmount)
						require.InDelta(t, float64(textInput)*0.01, usageRepo.lastLog.InputCost, 1e-10)
						require.InDelta(t, float64(imageInput)*0.03, usageRepo.lastLog.ImageInputCost, 1e-10)
						require.InDelta(t, 9*0.02, usageRepo.lastLog.OutputCost, 1e-10)
						require.InDelta(t, 439*0.04, usageRepo.lastLog.ImageOutputCost, 1e-10)
						require.InDelta(t, cacheCost, usageRepo.lastLog.CacheReadCost, 1e-10)
						require.InDelta(t, float64(textInput)*0.01+float64(imageInput)*0.03+9*0.02+439*0.04+cacheCost, userRepo.lastAmount, 1e-10)
						require.Equal(t, 1, userRepo.deductCalls)
						require.Equal(t, 376, result.Usage.InputTokens)
						require.Equal(t, 448, result.Usage.OutputTokens)
						require.Equal(t, 2, result.ImageCount)
						require.False(t, result.ImageEndpoint)
					})
				}
			}
		}
	}
}

func TestIndependentGPT26LegacyPerImageDefault(t *testing.T) {
	catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-image-2": {OutputCostPerImage: 0.42}}}
	billing := NewBillingService(&config.Config{}, catalog)
	cost := billing.CalculateImageCost("gpt-image-2.6-flare", "1K", 1, nil, 1)
	t.Logf("configured existing image fallback=0.42 charged=%g", cost.TotalCost)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	svc.billingService = billing
	svc.resolver = NewModelPricingResolver(nil, billing)
	group := &Group{ID: 123, RateMultiplier: 1}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "review-price", Model: "gpt-image-2.6-flare", UpstreamModel: "gpt-image-2.6-flare", ImageEndpoint: true, ImageCount: 1, ImageSize: "1K"}, APIKey: &APIKey{ID: 10123, GroupID: i64p(123), Group: group}, User: &User{ID: 20123}, Account: &Account{ID: 30123}})
	require.NoError(t, err)
	t.Logf("usage total=%g actual=%g deducted=%g calls=%d", usageRepo.lastLog.TotalCost, usageRepo.lastLog.ActualCost, userRepo.lastAmount, userRepo.deductCalls)
	require.InDelta(t, 0.42, userRepo.lastAmount, 1e-12)
}
