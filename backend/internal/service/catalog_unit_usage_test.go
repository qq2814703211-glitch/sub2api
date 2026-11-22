package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Read the same on-disk catalog as a gateway process. The unrelated token row
// is a positive control even when a baseline parser drops all unit-only rows.
func unitCatalogGateway(t *testing.T, rows string) (*OpenAIGatewayService, *OpenAIRecordUsageInput, *openAIRecordUsageLogRepoStub, *openAIRecordUsageBillingRepoStub) {
	t.Helper()
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, ledger, nil, nil, nil)
	catalog := NewPricingService(&config.Config{}, nil)
	path := filepath.Join(t.TempDir(), "catalog.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"legacy-text":{"input_cost_per_token":0.25},`+rows+`}`), 0600))
	require.NoError(t, catalog.loadPricingData(path))
	svc.billingService = NewBillingService(svc.cfg, catalog)
	key := openAIRecordUsageAPIKeyWithGroup(svc, 2, false)
	key.Group.RateMultiplier = 2
	key.GroupID = &key.Group.ID
	key.Quota = 1000
	in := &OpenAIRecordUsageInput{APIKey: key, User: &User{ID: 1}, Account: &Account{ID: 3}, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, Result: &OpenAIForwardResult{Model: "example-call", RequestID: "unit-test", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}}}
	return svc, in, logs, ledger
}

func TestCatalogUnitsOrdinarySuccessfulRequest(t *testing.T) {
	svc, in, logs, ledger := unitCatalogGateway(t, `"example-call":{"glory_billing_mode":"per_request","cost_per_request":0.375}`)
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.NotNil(t, ledger.lastCmd)
	require.Equal(t, 0.75, ledger.lastCmd.BalanceCost)
	require.Equal(t, 0.75, ledger.lastCmd.APIKeyQuotaCost)
	require.Equal(t, 0.375, logs.lastLog.TotalCost)
	require.Equal(t, "per_request", *logs.lastLog.BillingMode)
	require.Zero(t, logs.lastLog.InputCost)
}

func unitForwardContext(body string, path string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}
func unitUpstream(body string) *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}}
}
func TestCatalogUnitsImagesForwardToLedger(t *testing.T) {
	for _, tc := range []struct {
		name, row string
		want      float64
	}{
		{"one request", `"glory_billing_mode":"per_request","cost_per_request":0.375`, 1.5},
		{"two actual images", `"glory_billing_mode":"per_image","output_cost_per_image":0.375`, 3},
		{"explicit free", `"glory_billing_mode":"per_image","output_cost_per_image":0`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, in, logs, ledger := unitCatalogGateway(t, `"gpt-image-2":{`+tc.row+`}`)
			in.APIKey.Group.ImageRateIndependent = true
			in.APIKey.Group.ImageRateMultiplier = 4
			body := `{"model":"gpt-image-2","prompt":"x","n":7}`
			c := unitForwardContext(body, "/v1/images/generations")
			svc.httpUpstream = unitUpstream(`{"data":[{"b64_json":"aGVsbG8="},{"error":{"message":"failed"}},{"b64_json":"d29ybGQ="}]}`)
			in.Account = &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://image.example/v1", "api_key": "fixture"}}
			parsed, err := svc.ParseOpenAIImagesRequest(c, []byte(body))
			require.NoError(t, err)
			in.Result, err = svc.ForwardImages(c.Request.Context(), c, in.Account, []byte(body), parsed, "")
			require.NoError(t, err)
			require.Equal(t, 2, in.Result.ImageCount)
			require.NoError(t, svc.RecordUsage(context.Background(), in))
			require.Equal(t, tc.want, ledger.lastCmd.BalanceCost)
			require.Equal(t, tc.want, logs.lastLog.ActualCost)
		})
	}
}
func TestCatalogUnitsVoiceForwardToLedger(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		missing    bool
	}{{"actual seconds", `{"text":"ok","usage":{"seconds":2.375}}`, false}, {"unknown", `{"text":"ok"}`, true}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, in, logs, ledger := unitCatalogGateway(t, `"stt":{"glory_billing_mode":"per_second","cost_per_second":0.25}`)
			in.Account = &Account{ID: 3, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}
			body := `{"duration_seconds":998}`
			c := unitForwardContext(body, "/stt")
			svc.httpUpstream = unitUpstream(tc.body)
			var err error
			in.Result, err = svc.ForwardGrokVoice(c.Request.Context(), c, in.Account, "stt", []byte(body), "application/json")
			require.NoError(t, err)
			err = svc.RecordUsage(context.Background(), in)
			if tc.missing {
				require.ErrorContains(t, err, "provider billable seconds")
				require.Zero(t, ledger.calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1.1875, ledger.lastCmd.BalanceCost)
			require.Equal(t, "per_second", *logs.lastLog.BillingMode)
		})
	}
}
func TestCatalogUnitsFailedTurnAndRetry(t *testing.T) {
	svc, in, logs, ledger := unitCatalogGateway(t, `"example-call":{"glory_billing_mode":"per_request","cost_per_request":0.375,"input_cost_per_token":91}`)
	in.Result.OpenAIWSMode = true
	in.Result.UpstreamTerminalEvent = "response.failed"
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.Zero(t, logs.lastLog.ActualCost)
	require.Zero(t, ledger.calls, "a failed attempt cannot occupy the durable request key")
	failedKey := logs.lastLog.RequestID
	in.Result.UpstreamTerminalEvent = "response.completed"
	// Retry retains its logical request id.
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.Equal(t, 0.75, ledger.lastCmd.BalanceCost)
	require.NotEqual(t, failedKey, logs.lastLog.RequestID)
}
func TestCatalogUnitsMappedCommercialPriority(t *testing.T) {
	svc, in, _, ledger := unitCatalogGateway(t, `"dealignai/glm-5.3-uncensored-fp8":{"glory_billing_mode":"per_request","cost_per_request":0.375}`)
	in.Result.Model = "glm-5.3"
	in.Result.UpstreamModel = "dealignai/glm-5.3-uncensored-fp8"
	in.ChannelUsageFields = ChannelUsageFields{BillingModelSource: BillingModelSourceChannelMapped, OriginalModel: "glm-5.3", ChannelMappedModel: "glm-5.3"}
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.Equal(t, 0.75, ledger.lastCmd.BalanceCost)
	price := 0.125
	in.APIKey.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"glm-5.3"}, BillingMode: BillingModeToken, InputPrice: &price, OutputPrice: new(float64)}}
	require.NoError(t, svc.RecordUsage(context.Background(), in))
	require.Equal(t, 25., ledger.lastCmd.BalanceCost)
}

func TestCatalogUnitsGatewayMeasuresOnlyPricedUnits(t *testing.T) {
	for _, tc := range []struct {
		name, row string
		missing   bool
		cost      float64
	}{{"one request", `"glory_billing_mode":"per_request","cost_per_request":0.375`, false, 0.75}, {"two images", `"glory_billing_mode":"per_image","output_cost_per_image":0.375`, false, 1.5}, {"unmeasured seconds", `"glory_billing_mode":"per_second","cost_per_second":0.375`, true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			openai, in, logs, ledger := unitCatalogGateway(t, `"example-call":{`+tc.row+`}`)
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, ledger, nil, nil)
			svc.billingService = openai.billingService
			svc.resolver = openai.resolver
			err := svc.RecordUsage(context.Background(), &RecordUsageInput{APIKey: in.APIKey, APIKeyService: in.APIKeyService, User: in.User, Account: in.Account, Result: &ForwardResult{Model: in.Result.Model, RequestID: "measured", ImageEndpoint: true, ImageCount: 2, Usage: ClaudeUsage{InputTokens: 100}}})
			if tc.missing {
				require.ErrorContains(t, err, "provider billable seconds")
				require.Zero(t, ledger.calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.cost, ledger.lastCmd.BalanceCost)
		})
	}
}

func TestCatalogUnitsMappedEmbeddingsForwardToBilling(t *testing.T) {
	for _, card := range []bool{false, true} {
		t.Run(fmt.Sprint("requested-card=", card), func(t *testing.T) {
			const sku = "vendor/exact-embedding"
			svc, in, _, ledger := unitCatalogGateway(t, `"vendor/exact-embedding":{"glory_billing_mode":"per_request","cost_per_request":0.375,"input_cost_per_token":81}`)
			body := `{"model":"public-embedding","input":["a","b"]}`
			c := unitForwardContext(body, "/v1/embeddings")
			in.Account = &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://embed.example/v1", "api_key": "fixture", "model_mapping": map[string]any{"public-embedding": sku}}}
			svc.httpUpstream = unitUpstream(`{"data":[{"embedding":[0.1],"index":0},{"embedding":[0.3],"index":1}],"model":"vendor/exact-embedding","usage":{"prompt_tokens":12,"total_tokens":12}}`)
			if card {
				price := 0.125
				in.APIKey.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"public-embedding"}, BillingMode: BillingModeToken, InputPrice: &price}}
			}
			var err error
			in.Result, err = svc.ForwardEmbeddings(context.Background(), c, in.Account, []byte(body), "")
			require.NoError(t, err)
			require.Equal(t, sku, in.Result.UpstreamModel)
			require.NoError(t, svc.RecordUsage(context.Background(), in))
			want := 0.75
			if card {
				want = 3
			}
			require.Equal(t, want, ledger.lastCmd.BalanceCost)
		})
	}
}
func TestCatalogUnitsOfflineAndCommercialControls(t *testing.T) {
	for _, tc := range []struct {
		name, model, row string
		want             float64
	}{
		{"unmarked price fields do not enable unit billing", "example-call", `"example-call":{"cost_per_request":19,"input_cost_per_token":0.125}`, 25},
		{"token rows retain token semantics", "example-call", `"example-call":{"glory_billing_mode":"token","cost_per_second":2,"input_cost_per_token":0.125}`, 25},
		{"suffix not another SKU", "example-call-20261122", `"example-call":{"glory_billing_mode":"per_request","cost_per_request":999,"input_cost_per_token":91}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, in, _, ledger := unitCatalogGateway(t, tc.row)
			in.Result.Model = tc.model
			require.NoError(t, svc.RecordUsage(context.Background(), in))
			require.Equal(t, tc.want, ledger.lastCmd.BalanceCost)
		})
	}
}

func TestCatalogUnitsGeminiActualImagesThroughChat(t *testing.T) {
	const model = "gemini-2.5-flash-image"
	for _, tc := range []struct {
		name, parts string
		count       int
	}{{"no artifact", `{"text":"empty"}`, 0}, {"two artifacts", `{"fileData":{"mimeType":"image/png","fileUri":"https://example.test/a"}},{"fileData":{"mimeType":"image/png","fileUri":"https://example.test/b"}}`, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, in, logs, ledger := unitCatalogGateway(t, `"gemini-2.5-flash-image":{"glory_billing_mode":"per_image","output_cost_per_image":0.375}`)
			account := &Account{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}
			adapter := &GeminiMessagesCompatService{httpUpstream: unitUpstream(`{"candidates":[{"content":{"parts":[` + tc.parts + `]},"finishReason":"STOP"}]}`), cfg: &config.Config{}}
			body := `{"model":"gemini-2.5-flash-image","messages":[{"role":"user","content":"hi"}]}`
			c := unitForwardContext(body, "/v1/chat/completions")
			res, err := adapter.ForwardAsChatCompletions(context.Background(), c, account, []byte(body))
			require.NoError(t, err)
			gw := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, ledger, nil, nil)
			gw.billingService = svc.billingService
			gw.resolver = svc.resolver
			require.NoError(t, gw.RecordUsage(context.Background(), &RecordUsageInput{Result: res, APIKey: in.APIKey, User: in.User, Account: account}))
			require.Equal(t, tc.count, logs.lastLog.ImageCount)
			if tc.count == 0 {
				require.Zero(t, logs.lastLog.ActualCost)
				require.Zero(t, ledger.calls)
			} else {
				require.Equal(t, 1.5, ledger.lastCmd.BalanceCost)
			}
		})
	}
}

func TestCatalogUnitsCommercialShadowCards(t *testing.T) {
	// Minimal pricing fields from the sanitized Image and 视频分组 cards.
	for _, tc := range []struct {
		sku            string
		card           ChannelModelPricing
		image, seconds int
		want           float64
	}{
		{"gpt-image-2", ChannelModelPricing{BillingMode: BillingModeImage, PerRequestPrice: unitFixturePtr(0.05)}, 2, 0, 0.1},
		{"grok-imagine-video", ChannelModelPricing{BillingMode: BillingModeVideo, PerRequestPrice: unitFixturePtr(0.1), Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: unitFixturePtr(0.08)}}}, 0, 6, 0.48},
	} {
		t.Run(tc.sku, func(t *testing.T) {
			svc, in, _, ledger := unitCatalogGateway(t, fmt.Sprintf(`%q:{"glory_billing_mode":"per_request","cost_per_request":77}`, tc.sku))
			tc.card.Models = []string{tc.sku}
			in.APIKey.Group.RateMultiplier = 1
			in.APIKey.Group.ModelPricing = []ChannelModelPricing{tc.card}
			in.Result.Model = tc.sku
			in.Result.ImageCount = tc.image
			in.Result.ImageEndpoint = tc.image > 0
			if tc.seconds > 0 {
				in.Result.VideoCount = 1
				in.Result.VideoResolution = "720p"
				in.Result.VideoDurationSeconds = tc.seconds
			}
			require.NoError(t, svc.RecordUsage(context.Background(), in))
			require.Equal(t, tc.want, ledger.lastCmd.BalanceCost)
		})
	}
}
func unitFixturePtr(v float64) *float64 { return &v }

func TestCatalogUnitsForcedImageAndLegacyAudio(t *testing.T) {
	t.Run("group forces priced outputs", func(t *testing.T) {
		svc, in, _, ledger := unitCatalogGateway(t, `"unit-image":{"glory_billing_mode":"per_request","cost_per_request":9,"output_cost_per_image":0.25}`)
		in.Result.Model = "unit-image"
		in.Result.ImageCount = 2
		in.Result.ImageEndpoint = true
		in.Result.ImageSize = "1K"
		in.APIKey.Group.ImageCountBilling = true
		require.NoError(t, svc.RecordUsage(context.Background(), in))
		require.Equal(t, 1., ledger.lastCmd.BalanceCost)
	})
	t.Run("existing STT hour card keeps its own meter", func(t *testing.T) {
		svc, in, _, ledger := unitCatalogGateway(t, `"stt":{"glory_billing_mode":"per_second","cost_per_second":2}`)
		in.APIKey.Group.AudioSTTPricePerHour = unitFixturePtr(360)
		in.Result.Model = "stt"
		in.Result.AudioUsage = estimateGrokVoiceAudioUsage("stt", nil, "application/json", []byte(`{"duration":6,"usage":{"seconds":3}}`), 0)
		require.NoError(t, svc.RecordUsage(context.Background(), in))
		require.Equal(t, 1.2, ledger.lastCmd.BalanceCost)
	})
}
