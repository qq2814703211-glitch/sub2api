//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/stretchr/testify/require"
)

// Native Gemini protocol fixtures, not real supplier responses.
func TestMediaRuntimeGeminiImageInputTokens(t *testing.T) {
	const body = `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"promptTokensDetails":[{"modality":"TEXT","tokenCount":40},{"modality":"IMAGE","tokenCount":60}],"cacheTokensDetails":[{"modality":"IMAGE","tokenCount":10},{"modality":"TEXT","tokenCount":10}],"candidatesTokenCount":5}}`
	for _, stream := range []bool{false, true} {
		c, _ := newGeminiNativeTestContext(t)
		contentType, responseBody := "application/json", body
		if stream {
			contentType = "text/event-stream"
			responseBody = "data: " + body + "\n\n"
		}
		svc := newGeminiSignalService(contentType, responseBody)
		result, err := svc.ForwardNative(context.Background(), c, geminiSignalTestAccount(), "gemini-3.7-flash", "generateContent", stream, geminiSignalTestRequest())
		require.NoError(t, err)
		require.Equal(t, 80, result.Usage.InputTokens)
		require.Equal(t, 50, result.Usage.ImageInputTokens)
		require.Equal(t, 20, result.Usage.CacheReadInputTokens)
		require.Equal(t, 5, result.Usage.OutputTokens)
		bs := NewBillingService(&config.Config{}, nil)
		gateway := &GatewayService{billingService: bs, resolver: NewModelPricingResolver(nil, bs)}
		group := &Group{ID: 10, ModelPricing: []ChannelModelPricing{{Models: []string{"gemini-3.7-flash"}, BillingMode: BillingModeToken, InputPrice: ptrFloat(0.01), ImageInputPrice: ptrFloat(0.03), OutputPrice: ptrFloat(0.02), CacheReadPrice: ptrFloat(0.005)}}}
		cost, err := gateway.calculateTokenCost(context.Background(), result, &APIKey{Group: group, GroupID: &group.ID}, "gemini-3.7-flash", 2, time.Time{})
		require.NoError(t, err)
		require.InDelta(t, 2.0, cost.TotalCost, 1e-12)
		require.InDelta(t, 4.0, cost.ActualCost, 1e-12)
	}
}
