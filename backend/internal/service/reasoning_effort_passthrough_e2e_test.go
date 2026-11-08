package service

// End-to-end proof that the gateway no longer lowers a requested reasoning
// level anywhere on the way to upstream. Each subtest starts from the body a
// real client sends, runs the actual forwarding path against a recorder, and
// reads the body that would have hit the wire.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestReasoningEffortReachesUpstreamVerbatim is the umbrella regression guard
// for the "silently weaker reasoning than requested" class of bug. It covers
// every combination of level x model family x wire protocol that used to be
// rewritten: Grok responses (xhigh/max/minimal on 4.5/4.6/4.7/composer), Grok
// raw chat completions, GLM chat completions, the GLM-5.3 Anthropic-native
// passthrough, and the OpenAI Responses minimal->none rewrite.
func TestReasoningEffortReachesUpstreamVerbatim(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		model   string
		path    string
		readKey string
		want    string
	}{
		{
			name:    "grok 4.5 responses xhigh",
			body:    `{"model":"grok-4.5","input":"hi","reasoning":{"effort":"xhigh"},"stream":false}`,
			model:   "grok-4.5",
			path:    "reasoning.effort",
			readKey: "reasoning.effort",
			want:    "xhigh",
		},
		{
			name:    "grok 4.5 responses max",
			body:    `{"model":"grok-4.5","input":"hi","reasoning":{"effort":"max"},"stream":false}`,
			model:   "grok-4.5",
			path:    "reasoning.effort",
			readKey: "reasoning.effort",
			want:    "max",
		},
		{
			name:    "grok 4.5 responses minimal",
			body:    `{"model":"grok-4.5","input":"hi","reasoning":{"effort":"minimal"},"stream":false}`,
			model:   "grok-4.5",
			path:    "reasoning.effort",
			readKey: "reasoning.effort",
			want:    "minimal",
		},
		{
			name:    "grok 4.6 responses ultra",
			body:    `{"model":"grok-4.6","input":"hi","reasoning":{"effort":"ultra"},"stream":false}`,
			model:   "grok-4.6",
			path:    "reasoning.effort",
			readKey: "reasoning.effort",
			want:    "ultra",
		},
		{
			name:    "grok 4.7 responses xhigh",
			body:    `{"model":"grok-4.7","input":"hi","reasoning":{"effort":"xhigh"},"stream":false}`,
			model:   "grok-4.7",
			path:    "reasoning.effort",
			readKey: "reasoning.effort",
			want:    "xhigh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patched, err := patchGrokResponsesBody([]byte(tc.body), tc.model)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(patched, tc.readKey).String())
		})
	}
}

// TestReasoningEffortPassthroughGrokChatVerbatim covers the raw Chat
// Completions Grok route, where values used to be collapsed to high.
func TestReasoningEffortPassthroughGrokChatVerbatim(t *testing.T) {
	for _, level := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		for _, model := range []string{"grok-4.3", "grok-4.5", "grok-4.6", "grok-composer-2.5-fast"} {
			t.Run(model+"/"+level, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":%q,"messages":[],"reasoning_effort":%q}`, model, level))
				out, err := normalizeGrokChatReasoningEffort(body, model)
				require.NoError(t, err)
				require.Equal(t, level, gjson.GetBytes(out, "reasoning_effort").String(),
					"gateway must not rewrite %s on %s", level, model)
			})
		}
	}
}

// TestReasoningEffortPassthroughGLMChatVerbatim covers GLM via the shared chat
// completions body normalizer.
func TestReasoningEffortPassthroughGLMChatVerbatim(t *testing.T) {
	for _, level := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"glm-5.2","messages":[],"reasoning_effort":%q}`, level))
			out, changed := NormalizeGLMOpenAIReasoningEffort(body, "glm-5.2")
			require.False(t, changed)
			require.Equal(t, level, gjson.GetBytes(out, "reasoning_effort").String())
		})
	}
}

// TestReasoningEffortPassthroughGLM53AnthropicVerbatim covers the Anthropic
// native passthrough, which used to force thinking.type=enabled and collapse
// the effort onto low/high/max.
func TestReasoningEffortPassthroughGLM53AnthropicVerbatim(t *testing.T) {
	body := []byte(`{"model":"glm-5.3","max_tokens":32,"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"},"messages":[]}`)
	out, changed := NormalizeGLM53AnthropicThinking(body, "glm-5.3")
	require.False(t, changed)
	require.Equal(t, "disabled", gjson.GetBytes(out, "thinking.type").String())
	require.Equal(t, "xhigh", gjson.GetBytes(out, "output_config.effort").String())
}

// TestUpstreamEffortRejectionIsForwardedVerbatim proves the second half of the
// contract: when upstream refuses a level, the client sees upstream's own
// status and wording instead of a rewritten success or a generic 502.
func TestUpstreamEffortRejectionIsForwardedVerbatim(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamMessage := "This model does not support `reasoning_effort` value `X`."
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"invalid_reasoning_effort","message":"` + upstreamMessage + `"}}`)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}

	// An API-key Grok account uses the plain xAI /v1/responses endpoint, which
	// is the direct path the incident went through.
	account := &Account{
		ID: 901, Name: "grok-api-key", Platform: PlatformGrok, Type: AccountTypeAPIKey,
		Concurrency: 2, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "xai-test-key", "base_url": "https://api.x.ai/v1"},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"grok-4.5","input":"hi","reasoning":{"effort":"ultra"},"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	_, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok-4.5", false, time.Now())
	require.Error(t, err)

	// The level that reached upstream is exactly the level the client asked for.
	require.Equal(t, "ultra", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())

	// The client sees upstream's own status and wording, not a generic gateway
	// error, so the failure is attributable to upstream rather than to us.
	require.Equal(t, http.StatusBadRequest, rec.Code)
	gotBody := rec.Body.String()
	require.Contains(t, gotBody, "invalid_reasoning_effort")
	require.Contains(t, gotBody, upstreamMessage)
	require.NotContains(t, gotBody, "Upstream request failed")
}
