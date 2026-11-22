package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	CompatibleVideoEndpointCreate           GrokMediaEndpoint        = "compatible_video_create"
	CompatibleVideoEndpointStatus           GrokMediaEndpoint        = "compatible_video_status"
	OpenAIEndpointCapabilityCompatibleVideo OpenAIEndpointCapability = "video_generations"
)

func (e GrokMediaEndpoint) IsCompatibleVideo() bool {
	return e == CompatibleVideoEndpointCreate || e == CompatibleVideoEndpointStatus
}
func (e GrokMediaEndpoint) IsOpenAIAsyncVideo() bool { return e.IsSeedance() || e.IsCompatibleVideo() }
func CompatibleVideoTaskKey(id string) string        { return "compat-video:" + strings.TrimSpace(id) }

// CompatibleVideo uses the documented singular /video/generations protocol
// (WhatsTokenAI doc0007), not the xAI plural /videos API or a guessed vendor URL.
func (s *OpenAIGatewayService) ForwardCompatibleVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskID string, body []byte) (*OpenAIForwardResult, error) {
	if !endpoint.IsCompatibleVideo() || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityCompatibleVideo) {
		return nil, fmt.Errorf("compatible video requires an OpenAI API-key account with a custom base URL")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	target := buildOpenAIEndpointURL(base, "/v1/video/generations")
	method := http.MethodGet
	model, upstreamModel := "", ""
	if endpoint == CompatibleVideoEndpointCreate {
		if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() || gjson.GetBytes(body, "model").Type != gjson.String {
			return nil, fmt.Errorf("video request requires a JSON object with model")
		}
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if model == "" {
			return nil, fmt.Errorf("model is required")
		}
		upstreamModel = account.GetMappedModel(model)
		body, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, err
		}
		method = http.MethodPost
	} else {
		id := strings.TrimPrefix(taskID, "compat-video:")
		if err = validateUpstreamPathSegment("video task ID", id); err != nil || id == "" {
			return nil, fmt.Errorf("invalid video task ID")
		}
		target += "/" + id
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("video account missing api_key")
	}
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	started := time.Now()
	req, err := http.NewRequestWithContext(upstreamCtx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
		return nil, fmt.Errorf("video upstream status %d", resp.StatusCode)
	}
	result := &OpenAIForwardResult{Model: model, BillingModel: model, UpstreamModel: upstreamModel, Duration: time.Since(started), ResponseHeaders: resp.Header.Clone()}
	if endpoint == CompatibleVideoEndpointCreate {
		id := firstNonEmpty(gjson.GetBytes(responseBody, "id").String(), gjson.GetBytes(responseBody, "task_id").String())
		if id == "" || mediaResponseHasError(gjson.ParseBytes(responseBody)) {
			writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
			return nil, fmt.Errorf("video create response missing successful task ID")
		}
		result.ResponseID = CompatibleVideoTaskKey(id)
		result.MediaUsage = MergeMediaUsage(ExtractMediaRequestUsage(body), extractMediaReferenceResponse(gjson.ParseBytes(responseBody)))
		result.DeferredHTTPStatus = resp.StatusCode
		result.DeferredHTTPBody = append([]byte(nil), responseBody...)
		result.DeferredHTTPHeaders = resp.Header.Clone()
	} else {
		result.ResponseID = taskID
		root := gjson.ParseBytes(responseBody)
		envelope := root
		nativeTask := !root.Get("status").Exists() && root.Get("task").IsObject()
		if nativeTask {
			envelope = root.Get("task")
		}
		result.UpstreamTerminalEvent = envelope.Get("status").String()
		result.MediaUsage = &MediaUsage{}
		if (result.UpstreamTerminalEvent == "completed" || (nativeTask && result.UpstreamTerminalEvent == "succeeded")) && !mediaResponseHasError(root) && !mediaResponseHasError(envelope) {
			var outputSeconds float64
			measuredOutputs := 0
			for _, item := range envelope.Get("data").Array() {
				if strings.TrimSpace(item.Get("url").String()) != "" && !mediaResponseHasError(item) {
					result.MediaUsage.Count++
					if seconds := reportedVideoSeconds(extractMediaDimensions(item), 1); seconds != nil {
						outputSeconds += *seconds
						measuredOutputs++
					}
				}
			}
			if result.MediaUsage.Count == 0 && nativeTask && strings.TrimSpace(envelope.Get("content.url").String()) != "" {
				result.MediaUsage.Count = 1
			}
			if result.MediaUsage.Count > 0 {
				result.UpstreamTerminalEvent = "completed"
				count := result.MediaUsage.Count
				result.MediaUsage = extractMediaDimensions(envelope)
				result.MediaUsage = MergeMediaUsage(result.MediaUsage, extractCompatibleVideoResponseUsage(responseBody))
				result.MediaUsage.Count = count
				switch {
				case result.MediaUsage.TotalDurationSeconds != nil:
					result.ProviderBillableSeconds = mediaCopyPtr(result.MediaUsage.TotalDurationSeconds)
				case measuredOutputs == count:
					result.ProviderBillableSeconds = &outputSeconds
				case measuredOutputs == 0:
					result.ProviderBillableSeconds = reportedVideoSeconds(result.MediaUsage, count)
				}
				result.Usage.OutputTokens = max(0, int(envelope.Get("usage.completion_tokens").Int()))
				result.UpstreamModel = envelope.Get("model").String()
				result.VideoCount = count
			}
		}
	}
	// The handler commits creates after storing their billing snapshot.
	if endpoint != CompatibleVideoEndpointCreate {
		writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
	}
	return result, nil
}
