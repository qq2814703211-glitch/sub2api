//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func gpt26RestoreGinMode(t *testing.T) {
	t.Helper()
	mode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(mode) })
}

func gpt26ImagesBody(t *testing.T, model, complexity string, stream, multipartBody, edits bool) ([]byte, string) {
	t.Helper()
	fields := map[string]any{
		"model": model, "prompt": "draw a red cup", "complexity": complexity,
		"quality": "medium", "size": "1024x1024", "output_format": "png", "n": 2, "stream": stream,
	}
	if !multipartBody {
		if edits {
			fields["images"] = []map[string]string{{"image_url": "data:image/png;base64,AA=="}}
			fields["mask"] = map[string]string{"image_url": "data:image/png;base64,AQ=="}
		}
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		return body, "application/json"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		require.NoError(t, w.WriteField(name, fmt.Sprint(value)))
	}
	if edits {
		for _, name := range []string{"image", "mask"} {
			part, err := w.CreateFormFile(name, name+".png")
			require.NoError(t, err)
			_, err = part.Write([]byte("image-bytes"))
			require.NoError(t, err)
		}
	}
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

func gpt26ResponsesPayload(model string) string {
	return fmt.Sprintf(`{"id":"resp_gpt26","model":"gpt-5.6-luna","created_at":1710000000,"tools":[{"type":"image_generation","model":%q,"size":"1024x1024","output_format":"png","quality":"medium"}],"output":[{"id":"ig_first","type":"image_generation_call","result":"AA=="},{"id":"ig_second","type":"image_generation_call","result":"AQ=="}],"usage":{"input_tokens":5,"output_tokens":9},"tool_usage":{"image_gen":{"input_tokens":371,"input_tokens_details":{"text_tokens":19,"image_tokens":352},"output_tokens":439,"output_tokens_details":{"image_tokens":439},"images":2}}}`, model)
}

func gpt26Response(payload, contentType string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}
}

func TestGPTImage26ImagesProtocolMatrix(t *testing.T) {
	gpt26RestoreGinMode(t)
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			for _, stream := range []bool{false, true} {
				for _, multipartBody := range []bool{false, true} {
					for _, edits := range []bool{false, true} {
						for _, complexity := range []string{"low", "medium", "high"} {
							name := fmt.Sprintf("%s/%s/stream=%t/multipart=%t/edits=%t/%s", model, accountType, stream, multipartBody, edits, complexity)
							t.Run(name, func(t *testing.T) {
								// Bare family spelling is a configured alias, not an assumed public SKU.
								upstreamModel := model
								if model == "gpt-image-2.6" {
									upstreamModel = "gpt-image-2.6-flare"
								}
								body, contentType := gpt26ImagesBody(t, model, complexity, stream, multipartBody, edits)
								c, rec := newOpenAIImagesTestContext(t, body)
								c.Request.Header.Set("Content-Type", contentType)
								if edits {
									c.Request.URL.Path = openAIImagesEditsEndpoint
								}
								account := directImagesTestAccount()
								account.Type = accountType
								account.Credentials["model_mapping"] = map[string]any{model: upstreamModel}
								if accountType == AccountTypeAPIKey {
									account.Credentials["api_key"] = "sk-test"
									account.Credentials["base_url"] = "https://api.openai.com/v1"
								}
								payload := fmt.Sprintf(`{"model":%q,"data":[{"b64_json":"AA==","output_format":"png"},{"b64_json":"AQ==","output_format":"png"}],"usage":{"input_tokens":371,"input_tokens_details":{"text_tokens":19,"image_tokens":352},"output_tokens":439,"output_tokens_details":{"image_tokens":439}}}`, upstreamModel)
								responseType := "application/json"
								if stream {
									payload = "data: " + `{"type":"image_generation.completed","b64_json":"AA==","output_format":"png"}` + "\n\ndata: " + `{"type":"image_generation.completed","b64_json":"AQ==","output_format":"png","usage":{"input_tokens":371,"input_tokens_details":{"text_tokens":19,"image_tokens":352},"output_tokens":439,"output_tokens_details":{"image_tokens":439}}}` + "\n\n"
									responseType = "text/event-stream"
								}
								upstream := &httpUpstreamRecorder{resp: gpt26Response(payload, responseType)}
								svc := newOpenAIImagesTestService(upstream)
								parsed, err := svc.ParseOpenAIImagesRequest(c, body)
								require.NoError(t, err)
								require.Equal(t, complexity, parsed.Complexity)
								require.Equal(t, OpenAIImagesCapabilityNative, parsed.RequiredCapability)
								result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
								require.NoError(t, err)
								require.Equal(t, model, result.Model)
								require.Equal(t, upstreamModel, result.UpstreamModel)
								require.Equal(t, 2, result.ImageCount)
								require.Equal(t, 371, result.Usage.InputTokens)
								require.Equal(t, 352, result.Usage.ImageInputTokens)
								require.Equal(t, 439, result.Usage.ImageOutputTokens)
								require.Equal(t, stream, result.Stream)
								require.Equal(t, http.StatusOK, rec.Code)
								if !stream {
									require.Len(t, gjson.GetBytes(rec.Body.Bytes(), "data").Array(), 2)
								} else {
									require.Contains(t, rec.Body.String(), ".completed")
								}
								if accountType != AccountTypeAPIKey {
									wantPath := "/backend-api/codex/images/generations"
									if edits {
										wantPath = "/backend-api/codex/images/edits"
									}
									require.Equal(t, wantPath, upstream.lastReq.URL.Path)
									require.Equal(t, upstreamModel, gjson.GetBytes(upstream.lastBody, "model").String())
									require.Equal(t, complexity, gjson.GetBytes(upstream.lastBody, "complexity").String())
									require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "quality").String())
									require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning").Exists())
									require.Equal(t, int64(2), gjson.GetBytes(upstream.lastBody, "n").Int())
									if edits {
										require.Len(t, gjson.GetBytes(upstream.lastBody, "images").Array(), 1)
										require.True(t, gjson.GetBytes(upstream.lastBody, "mask.image_url").Exists())
									}
								} else {
									require.Equal(t, c.Request.URL.Path, upstream.lastReq.URL.Path)
									if multipartBody {
										out := &OpenAIImagesRequest{}
										require.NoError(t, parseOpenAIImagesMultipartRequest(upstream.lastBody, upstream.lastReq.Header.Get("Content-Type"), out))
										require.Equal(t, upstreamModel, out.Model)
										require.Equal(t, complexity, out.Complexity)
										if edits {
											require.Len(t, out.Uploads, 1)
											require.NotNil(t, out.MaskUpload)
										}
									} else {
										require.Equal(t, upstreamModel, gjson.GetBytes(upstream.lastBody, "model").String())
										require.Equal(t, complexity, gjson.GetBytes(upstream.lastBody, "complexity").String())
									}
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestGPTImage26ModelIdentityAndAliases(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		t.Run(model, func(t *testing.T) {
			require.True(t, IsGPTImageGenerationModel(model))
			require.True(t, IsImageGenerationIntent("/v1/responses", model, nil))
			require.True(t, IsExplicitImageGenerationIntent("/v1/responses", model, nil))
			require.Equal(t, model, normalizeCodexModel(model), "image model must not become a text model")
			require.NoError(t, validateOpenAIImagesModel(model))
			for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
				account := &Account{Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{}}
				require.True(t, account.IsModelSupported(model))
				account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6": model, "draw-*": model}
				require.True(t, account.IsModelSupported("gpt-image-2.6"))
				require.True(t, account.IsModelSupported("draw-cup"))
				require.Equal(t, model, account.GetMappedModel("gpt-image-2.6"))
				require.Equal(t, model, account.GetMappedModel("draw-cup"))
				require.False(t, account.IsModelSupported("gpt-image-1"), "explicit allowlists remain restricted")
			}
		})
	}
}

func TestGPTImage26ChannelAndAccountMapping(t *testing.T) {
	gpt26RestoreGinMode(t)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-1","prompt":"draw","complexity":"high"}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			account := directImagesTestAccount()
			account.Type = accountType
			account.Credentials["api_key"] = "sk-test"
			account.Credentials["base_url"] = "https://api.openai.com/v1"
			account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6": "gpt-image-2.6-sunburst"}
			response := openAIImagesJSONResponse()
			upstream := &httpUpstreamRecorder{resp: response}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "gpt-image-2.6")
			require.NoError(t, err)
			require.Equal(t, "gpt-image-2.6", result.Model)
			require.Equal(t, "gpt-image-2.6-sunburst", result.UpstreamModel)
		})
	}
}

func TestGPTImage26RecordUsageBillsFirstImage(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, mode := range []string{"token", "image", "default"} {
			t.Run(model+"/"+mode, func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
				svc.resolver = NewModelPricingResolver(nil, svc.billingService)
				group := &Group{ID: 123, RateMultiplier: 1}
				want := 0.025
				if mode == "token" {
					// Explicit settlement-unit prices; no official USD fallback.
					group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken,
						InputPrice: f64p(0), ImageInputPrice: f64p(0), ImageOutputPrice: f64p(want / 439)}}
				}
				if mode == "default" {
					// Existing default per-image billing stays unchanged.
					want = 0.134
				}
				if mode == "image" {
					// Explicit operator per-image price, not a claimed official price.
					svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, 123, model, 0.25)
					want = 0.25
				}
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "resp_first_" + model + mode, Model: model, UpstreamModel: model,
						ImageCount: 1, ImageSize: "1K", ImageEndpoint: true, Duration: time.Second,
						Usage: OpenAIUsage{InputTokens: 371, ImageInputTokens: 352, OutputTokens: 439, ImageOutputTokens: 439}},
					APIKey: &APIKey{ID: 10123, GroupID: i64p(123), Group: group},
					User:   &User{ID: 20123}, Account: &Account{ID: 30123},
				})
				require.NoError(t, err)
				require.NotNil(t, usageRepo.lastLog)
				require.Equal(t, 1, usageRepo.lastLog.ImageCount)
				require.InDelta(t, want, usageRepo.lastLog.TotalCost, 1e-12)
				require.Equal(t, 1, userRepo.deductCalls)
				require.InDelta(t, want, userRepo.lastAmount, 1e-12)
			})
		}
	}
}
