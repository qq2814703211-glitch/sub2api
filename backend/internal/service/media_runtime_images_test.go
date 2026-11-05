//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// These are protocol fixtures, not captured supplier requests.
func TestMediaRuntimeSeedreamStreamingCountsOnlySuccessfulOutputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `data: {"type":"image_generation.partial_succeeded","image_index":0,"url":"https://example.test/0.png","size":"2048x1024"}

data: {"type":"image_generation.partial_failed","image_index":1,"error":{"code":"generation_failed"}}

data: {"type":"image_generation.partial_succeeded","image_index":2,"url":"https://example.test/2.png","size":"4096x2048"}

data: {"type":"image_generation.partial_succeeded","image_index":0,"url":"https://example.test/0.png","size":"2048x1024"}

data: {"type":"image_generation.completed","usage":{"generated_images":2}}

`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(context.Background())
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	svc := &OpenAIGatewayService{}
	_, count, sizes, _, err := svc.handleOpenAIImagesStreamingResponse(resp, c, time.Now(), nil)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, []string{"2048x1024", "4096x2048"}, sizes)
}

func TestMediaRuntimeAPIKeyImagesToActualCost(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		multipart, stream, failed bool
	}{
		{name: "json mixed outputs"}, {name: "multipart mixed outputs", multipart: true},
		{name: "stream mixed outputs", stream: true}, {name: "all failed", failed: true}, {name: "stream all failed", stream: true, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := "seedream-5-0-pro"
			requestBody := []byte(`{"model":"seedream-5-0-pro","prompt":"layers","image":"https://example.test/input.png","size":"2K","layer_decomposition":true,"n":1}`)
			if tc.stream {
				model = "seedream-4.0"
				requestBody = []byte(`{"model":"seedream-4.0","prompt":"images","size":"2K","n":3,"stream":true}`)
			}
			contentType := "application/json"
			if tc.multipart {
				model = "gpt-image-2.6"
				var b bytes.Buffer
				w := multipart.NewWriter(&b)
				for k, v := range map[string]string{"model": "gpt-image-2.6", "prompt": "images", "size": "2048x1024", "n": "3"} {
					require.NoError(t, w.WriteField(k, v))
				}
				part, err := w.CreateFormFile("image", "source.png")
				require.NoError(t, err)
				_, err = part.Write([]byte("fixture input"))
				require.NoError(t, err)
				require.NoError(t, w.Close())
				requestBody = b.Bytes()
				contentType = w.FormDataContentType()
			}
			responseBody := `{"usage":{"input_images":3},"data":[{"url":"https://example.test/a","size":"1024x1024","z_index":0},{"error":{"code":"Failed"}},{"url":"https://example.test/b","size":"2048x1024","z_index":1}]}`
			responseType := "application/json"
			if tc.stream {
				responseType = "text/event-stream"
				responseBody = "data: {\"type\":\"image_generation.partial_succeeded\",\"image_index\":0,\"url\":\"https://example.test/a\",\"size\":\"1024x1024\"}\n\ndata: {\"type\":\"image_generation.partial_failed\",\"image_index\":1,\"error\":{\"code\":\"Failed\"}}\n\ndata: {\"type\":\"image_generation.partial_succeeded\",\"image_index\":2,\"url\":\"https://example.test/b\",\"size\":\"4096x2048\"}\n\ndata: {\"type\":\"image_generation.completed\",\"usage\":{\"generated_images\":2}}\n\n"
			}
			if tc.failed {
				responseBody = `{"data":[{"error":{"code":"Failed"}}],"usage":{"output_tokens":100}}`
				if tc.stream {
					responseBody = "data: {\"type\":\"image_generation.partial_failed\",\"image_index\":0,\"error\":{\"code\":\"Failed\"}}\n\ndata: {\"type\":\"image_generation.completed\",\"usage\":{\"generated_images\":0}}\n\n"
				}
			}
			if tc.stream && !tc.failed {
				responseBody = strings.Replace(responseBody, `"generated_images":2`, `"generated_images":2,"input_images":3`, 1)
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{responseType}}, Body: io.NopCloser(strings.NewReader(responseBody))}}
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, nil, nil)
			svc.httpUpstream = upstream
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			svc.cfg = &config.Config{}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(requestBody))
			c.Request.Header.Set("Content-Type", contentType)
			c.Set("api_key", &APIKey{ID: 2, Group: &Group{ImageCountBilling: true}})
			parsed, err := svc.ParseOpenAIImagesRequest(c, requestBody)
			require.NoError(t, err)
			account := &Account{ID: 6, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://ark.example/api/v3"}}
			result, err := svc.ForwardImages(context.Background(), c, account, requestBody, parsed, "")
			require.NoError(t, err)
			require.NotNil(t, result.MediaUsage)
			wantCount := 2
			if tc.failed {
				wantCount = 0
			}
			require.Equal(t, wantCount, result.ImageCount)
			require.Equal(t, wantCount, result.MediaUsage.Count)
			group := &Group{ID: 3, RateMultiplier: 2, ImageRateMultiplier: 1.5, ImageRateIndependent: true, ImageCountBilling: true, ImagePrice1K: ptrFloat(0.001),
				ModelPricing: []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeImage, MediaPricing: &MediaPricing{ExtraInputImagePrice: ptrFloat(0.02), FreeInputImages: mediaReferenceCount(1), Rules: []MediaPricingRule{
					{Name: "small layer", Unit: "image", Conditions: MediaPricingConditions{MaxPixels: mediaRuntimeInt(2000000)}, Price: ptrFloat(0.2)},
					{Name: "large layer", Unit: "image", Conditions: MediaPricingConditions{MinPixels: mediaRuntimeInt(2000001)}, Price: ptrFloat(0.4)},
				}}}}}
			statsPricing := ChannelModelPricing{Models: []string{model}, BillingMode: BillingModeImage,
				MediaPricing: &MediaPricing{ExtraInputImagePrice: ptrFloat(0.01), FreeInputImages: mediaReferenceCount(1), Rules: []MediaPricingRule{{Name: "upstream count", Unit: "image", Price: ptrFloat(0.1)}}}}
			statsRule := AccountStatsPricingRule{GroupIDs: []int64{3}, Pricing: []ChannelModelPricing{statsPricing}}
			svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 99, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{statsRule}}, 3, PlatformOpenAI)
			user := &User{ID: 1}
			key := &APIKey{ID: 2, UserID: 1, User: user, Group: group, GroupID: &group.ID}
			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: user, Account: account})
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			if tc.failed {
				require.Zero(t, usageRepo.lastLog.ActualCost)
			} else {
				require.Equal(t, mediaReferenceCount(3), result.MediaUsage.InputImageCount)
				require.InDelta(t, 0.04, usageRepo.lastLog.ImageInputCost, 1e-12, "input surcharge once, not per output")
				require.InDelta(t, 0.64, usageRepo.lastLog.TotalCost, 1e-12, "first image is charged")
				require.InDelta(t, 0.96, usageRepo.lastLog.ActualCost, 1e-12, "separate image multiplier exactly once")
				require.InDelta(t, 0.96, billingRepo.lastCmd.BalanceCost, 1e-12)
				require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
				require.InDelta(t, 0.22, *usageRepo.lastLog.AccountStatsCost, 1e-12)
			}
		})
	}
}
func mediaReferenceCount(v int) *int { return &v }
func mediaRuntimeBool(v bool) *bool  { return &v }
func mediaRuntimeInt(v int64) *int64 { return &v }
