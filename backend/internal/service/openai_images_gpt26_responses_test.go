//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPTImage26ResponsesProtocolMatrix(t *testing.T) {
	gpt26RestoreGinMode(t)
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			for _, stream := range []bool{false, true} {
				for _, entry := range []string{"image_only", "text_tool", "apikey_passthrough"} {
					if entry == "apikey_passthrough" && accountType != AccountTypeAPIKey {
						continue
					}
					name := fmt.Sprintf("%s/%s/stream=%t/%s", model, accountType, stream, entry)
					t.Run(name, func(t *testing.T) {
						account := directImagesTestAccount()
						account.Type = accountType
						account.Credentials["api_key"] = "sk-test"
						account.Credentials["base_url"] = "https://api.openai.com/v1"
						account.Extra = map[string]any{"use_responses_api": true}
						toolModel := model
						if model == "gpt-image-2.6" && entry == "image_only" {
							toolModel = "gpt-image-2.6-flare"
							account.Credentials["model_mapping"] = map[string]any{model: toolModel}
						}
						payload := map[string]any{
							"model": model, "input": "draw a red cup", "stream": stream,
							"complexity": "low", "quality": "medium",
						}
						if entry != "image_only" {
							delete(payload, "complexity")
							delete(payload, "quality")
							payload["model"] = "gpt-5.6-luna"
							payload["tools"] = []any{map[string]any{"type": "image_generation", "model": toolModel, "complexity": "high", "quality": "medium"}}
						}
						if entry == "apikey_passthrough" {
							account.Extra["openai_passthrough"] = true
						}
						body, err := json.Marshal(payload)
						require.NoError(t, err)
						response := gpt26ResponsesPayload(toolModel)
						contentType := "application/json"
						if accountType != AccountTypeAPIKey || stream {
							response = "data: " + `{"type":"response.completed","response":` + response + "}\n\n"
							contentType = "text/event-stream"
						}
						upstream := &httpUpstreamRecorder{resp: gpt26Response(response, contentType)}
						svc := newOpenAIImagesTestService(upstream)
						c, rec := newOpenAIImagesTestContext(t, body)
						c.Request.URL.Path = "/v1/responses"
						SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
						result, err := svc.Forward(context.Background(), c, account, body)
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, rec.Code)
						require.Equal(t, 2, result.ImageCount)
						require.Equal(t, toolModel, result.BillingModel)
						require.Equal(t, 352, result.Usage.ImageInputTokens)
						require.Equal(t, 439, result.Usage.ImageOutputTokens)
						require.False(t, result.ImageEndpoint, "Responses retains mixed text and image token billing")
						require.Equal(t, toolModel, gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
						wantComplexity := "high"
						if entry == "image_only" {
							wantComplexity = "low"
							require.False(t, gjson.GetBytes(upstream.lastBody, "complexity").Exists())
							require.Equal(t, openAIImagesResponsesMainModelValue(), gjson.GetBytes(upstream.lastBody, "model").String())
						}
						require.Equal(t, wantComplexity, gjson.GetBytes(upstream.lastBody, "tools.0.complexity").String())
						require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "tools.0.quality").String())
						if entry == "apikey_passthrough" {
							require.Equal(t, body, upstream.lastBody)
						}
					})
				}
			}
		}
	}
}
