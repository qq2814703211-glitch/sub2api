package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func officialFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/price_sync_official/" + name)
	require.NoError(t, err)
	return body
}

func TestPriceSyncOfficialKimi(t *testing.T) {
	for _, tc := range []struct {
		fixture    string
		want, high []float64
	}{
		{"kimi.html", []float64{13e-6, 68e-6, 1.3e-6, 13e-6, 26e-6}, []float64{26e-6, 136e-6, 1.3e-6, 26e-6, 52e-6}},
		{"kimi-changed.html", []float64{19.12e-6, 77.34e-6, 2.21e-6, 21.39e-6, 33.79e-6}, []float64{28.41e-6, 155.87e-6, 3.43e-6, 30.44e-6, 53.94e-6}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			quotes, problems := parseOfficialKimi(officialFixture(t, tc.fixture))
			require.Empty(t, problems)
			fields := []string{"input_cost_per_token", "output_cost_per_token", "cache_read_input_token_cost", "cache_creation_input_token_cost", "cache_creation_input_token_cost_above_1hr"}
			for i, key := range fields {
				require.InDelta(t, tc.want[i], quotes["kimi-k3.1-pro"].Rates[key], 1e-16, key)
				require.InDelta(t, tc.high[i], quotes["kimi-k3.1-pro-highspeed"].Rates[key], 1e-16, key)
			}
			require.NotContains(t, quotes["kimi-k2.7-code"].Rates, "cache_creation_input_token_cost")
			require.NotContains(t, quotes["kimi-k3.1-pro"].Rates, "cache_creation_input_token_cost_30m")
		})
	}
	_, problems := parseOfficialKimi([]byte(`<table><thead><tr/></thead><tbody/></table>`))
	require.NotEmpty(t, problems["kimi-k3.1-pro"])
}

func TestPriceSyncOfficialDeepSeek(t *testing.T) {
	doc := officialFixture(t, "deepseek.md")
	quotes, problems := parseOfficialDeepSeek(doc)
	flash := quotes["deepseek-flash"]
	require.Equal(t, map[string]float64{"input_cost_per_token": 1e-6, "output_cost_per_token": 4e-6, "cache_read_input_token_cost": .02e-6}, flash.Rates)
	require.Len(t, flash.Windows, 2)
	require.Equal(t, PriceSyncWindow{Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "12:00", ExcludeCNHolidays: true, Rates: map[string]float64{"input_cost_per_token": 2e-6, "output_cost_per_token": 8e-6, "cache_read_input_token_cost": .04e-6}}, flash.Windows[0])
	require.Equal(t, "14:00", flash.Windows[1].Start)
	require.Equal(t, "18:00", flash.Windows[1].End)
	require.Equal(t, flash.Rates, quotes["deepseek-v4-flash"].Rates)
	require.Equal(t, flash.Rates, quotes["deepseek-v4-flash-vision-exp"].Rates)
	require.NotContains(t, quotes, "deepseek-v4.1-flash-1017")
	require.NotEmpty(t, problems["deepseek-v4.1-flash-1017"])
	require.NotContains(t, quotes, "deepseek-v4.1-flash")
	require.NotContains(t, quotes, "deepseek-chat")
	require.NotContains(t, flash.Rates, "cache_creation_input_token_cost")
	require.InDelta(t, 4.5e-6, quotes["deepseek-v4-pro"].Rates["input_cost_per_token"], 1e-16)
	changed := strings.NewReplacer("0.02元", "0.03元", "0.04元", "0.06元", "9:00 - 12:00", "8:15 - 11:30").Replace(string(doc))
	quotes, _ = parseOfficialDeepSeek([]byte(changed))
	require.InDelta(t, .03e-6, quotes["deepseek-flash"].Rates["cache_read_input_token_cost"], 1e-16)
	require.Equal(t, "08:15", quotes["deepseek-flash"].Windows[0].Start)
	quotes, problems = parseOfficialDeepSeek([]byte(strings.Replace(string(doc), "不含中国法定节假日", "含中国法定节假日", 1)))
	require.NotContains(t, quotes, "deepseek-flash")
	require.NotEmpty(t, problems["deepseek-flash"])
	quotes, problems = parseOfficialDeepSeek([]byte(strings.Replace(string(doc), "0.04元", "0.08元", 1)))
	require.NotContains(t, quotes, "deepseek-flash")
	require.Contains(t, problems["deepseek-flash"], "disagree")
	require.Contains(t, quotes, "deepseek-v4-pro")
}

func TestPriceSyncOfficialOpenAI(t *testing.T) {
	docs := map[string][]byte{"gpt-6.2-astra": officialFixture(t, "astra.md"), "gpt-6.2-sol": officialFixture(t, "sol.md")}
	pricing := officialFixture(t, "openai-pricing.md")
	quotes, problems := parseOfficialOpenAI(pricing, docs)
	require.Empty(t, problems)
	for model, want := range map[string][]float64{"gpt-6.2-astra": {10e-6, 20e-6, 20e-6, 40e-6, 60e-6}, "gpt-6.2-sol": {2.5e-6, 5e-6, 5e-6, 10e-6, 15e-6}} {
		q := quotes[model]
		require.InDelta(t, want[0], q.Rates["cache_creation_input_token_cost_30m"], 1e-16)
		require.InDelta(t, want[1], q.Rates["cache_creation_input_token_cost_12h"], 1e-16)
		require.NotContains(t, q.Rates, "cache_creation_input_token_cost_above_1hr")
		require.NotContains(t, q.Rates, "cache_creation_input_token_cost")
		require.Len(t, q.Tiers, 1)
		require.Equal(t, 272000, q.Tiers[0].Threshold)
		require.False(t, q.Tiers[0].Inclusive)
		require.InDelta(t, want[2], q.Tiers[0].Rates["cache_creation_input_token_cost_30m"], 1e-16)
		require.InDelta(t, want[3], q.Tiers[0].Rates["cache_creation_input_token_cost_12h"], 1e-16)
		require.InDelta(t, want[4], q.Tiers[0].Rates["output_cost_per_token"], 1e-16)
	}
	docs["gpt-6.2-astra"] = []byte(strings.Replace(string(docs["gpt-6.2-astra"]), "$20 per million", "$21 per million", 1))
	quotes, problems = parseOfficialOpenAI(pricing, docs)
	require.NotContains(t, quotes, "gpt-6.2-astra")
	require.Contains(t, problems["gpt-6.2-astra"], "disagree")
	require.Contains(t, quotes, "gpt-6.2-sol")
}

func TestPriceSyncOfficialAliyun(t *testing.T) {
	body := officialFixture(t, "aliyun.mdx")
	quotes, problems := parseOfficialAliyun(body)
	want := map[string]struct {
		mode, key string
		rate      float64
	}{
		"qwen3-asr-flash":           {"per_second", "cost_per_second", .00022},
		"qwen3-asr-flash-filetrans": {"per_second", "cost_per_second", .00022},
		"qwen3-asr-flash-realtime":  {"per_second", "cost_per_second", .00033},
		"wan2.6-t2i":                {"per_image", "output_cost_per_image", .2},
		"facechain-finetune":        {"per_request", "cost_per_request", 2.5},
	}
	for model, v := range want {
		q := quotes[model]
		require.Equal(t, v.mode, q.BillingMode, model)
		require.Equal(t, map[string]float64{v.key: v.rate}, q.Rates, model)
	}
	require.NotContains(t, quotes, "qwen-image-3.0-pro")
	require.Contains(t, problems["qwen-image-3.0-pro"], "input")
	require.NotContains(t, quotes, "happyoyster-1.0-adventure")
	require.Contains(t, problems["happyoyster-1.0-adventure"], "mixed")
	quotes, problems = parseOfficialAliyun([]byte(strings.Replace(string(body), `<Tab title="华北2（北京）">`, `<Tab title="华东1（杭州）">`, 1)))
	require.NotContains(t, quotes, "qwen3-asr-flash")
	require.Contains(t, problems["qwen3-asr-flash"], "Beijing")
	require.Contains(t, quotes, "wan2.6-t2i")
}

func TestPriceSyncOfficialRetainsCompleteQuoteAndBacksOffIndependently(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	f := &officialFixtureFetcher{bodies: map[string][]byte{
		officialKimiURL: officialFixture(t, "kimi.html"), officialDeepSeekURL: officialFixture(t, "deepseek.md"), officialAliyunURL: officialFixture(t, "aliyun.mdx"), officialOpenAIURL: officialFixture(t, "openai-pricing.md"),
		officialOpenAIModelURL("gpt-6.2-astra"): officialFixture(t, "astra.md"), officialOpenAIModelURL("gpt-6.2-sol"): officialFixture(t, "sol.md"),
	}, count: map[string]int{}, err: map[string]error{}}
	state := newOfficialPriceCache()
	first := state.collect(context.Background(), f, true, now)
	require.Empty(t, first.SourceErrors)
	require.Equal(t, now, first.Quotes["kimi-k3.1-pro"].VerifiedAt)
	require.Contains(t, first.Quotes["gpt-6.2-astra"].Evidence, "sha256:")
	first.Quotes["kimi-k3.1-pro"].Rates["input_cost_per_token"] = 0 // caller must not be able to mutate cache
	f.bodies[officialKimiURL] = []byte(strings.Replace(string(f.bodies[officialKimiURL]), "¥52.00 / MTok", "-", 1))
	f.err[officialDeepSeekURL] = errors.New("HTTP 503")
	next := state.collect(context.Background(), f, true, now.Add(time.Minute))
	require.Contains(t, next.SourceErrors[officialDeepSeekURL], "503")
	require.Contains(t, next.SourceErrors[officialKimiURL], "incomplete")
	require.Equal(t, now, next.Quotes["deepseek-flash"].VerifiedAt)
	require.Equal(t, now, next.Quotes["kimi-k3.1-pro-highspeed"].VerifiedAt)
	require.InDelta(t, 52e-6, next.Quotes["kimi-k3.1-pro-highspeed"].Rates["cache_creation_input_token_cost_above_1hr"], 1e-16)
	require.InDelta(t, 13e-6, next.Quotes["kimi-k3.1-pro"].Rates["input_cost_per_token"], 1e-16)
	require.Equal(t, now.Add(time.Minute), next.Quotes["gpt-6.2-astra"].VerifiedAt)
	f.bodies[officialKimiURL] = officialFixture(t, "kimi-changed.html")
	during := state.collect(context.Background(), f, true, now.Add(time.Minute+time.Second))
	require.Contains(t, during.SourceErrors[officialKimiURL], "backoff")
	require.Equal(t, 2, f.count[officialKimiURL])
	require.Equal(t, 3, f.count[officialOpenAIURL])
	recovered := state.collect(context.Background(), f, true, now.Add(20*time.Minute))
	require.NotContains(t, recovered.SourceErrors, officialKimiURL)
	require.InDelta(t, 19.12e-6, recovered.Quotes["kimi-k3.1-pro"].Rates["input_cost_per_token"], 1e-16)
	require.NotEqual(t, first.Quotes["kimi-k3.1-pro"].Evidence, recovered.Quotes["kimi-k3.1-pro"].Evidence)
	onlyUSD := newOfficialPriceCache().collect(context.Background(), f, false, now)
	require.Empty(t, onlyUSD.NativeModels)
	require.Equal(t, 2, len(onlyUSD.Quotes))
}

type officialFixtureFetcher struct {
	mu     sync.Mutex
	bodies map[string][]byte
	count  map[string]int
	err    map[string]error
}

func (f *officialFixtureFetcher) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count[url]++
	if err := f.err[url]; err != nil {
		return nil, err
	}
	if body := f.bodies[url]; body != nil {
		return body, nil
	}
	return nil, fmt.Errorf("unexpected URL %s", url)
}

type officialTestTransport func(*http.Request) (*http.Response, error)

func (r officialTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return r(req)
}

func TestPriceSyncOfficialHTTPBounds(t *testing.T) {
	ctx := context.Background()
	var requested []string
	client := &http.Client{Transport: officialTestTransport(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "", req.Header.Get("Cookie"))
		_, hasDeadline := req.Context().Deadline()
		require.True(t, hasDeadline)
		response := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(strings.NewReader("abcdef"))}
		switch req.URL.String() {
		case officialKimiURL:
			require.Equal(t, "text/html", req.Header.Get("Accept"))
		case officialDeepSeekURL:
			require.Equal(t, "text/markdown", req.Header.Get("Accept"))
			response.StatusCode = 302
			response.Header.Set("Location", "https://example.invalid/")
		case modelPricingSyncRegistryURL:
			require.Equal(t, "application/json", req.Header.Get("Accept"))
		default:
			t.Errorf("unexpected network request to %s", req.URL)
		}
		return response, nil
	})}
	_, err := FetchOfficialPricingDocument(ctx, client, "https://example.invalid/", 8)
	require.ErrorContains(t, err, "allowlist")
	_, err = FetchOfficialPricingDocument(ctx, client, officialDeepSeekURL, 8)
	require.ErrorContains(t, err, "HTTP 302")
	_, err = FetchOfficialPricingDocument(ctx, client, officialKimiURL, 5)
	require.ErrorContains(t, err, "exceeds 5 bytes")
	body, err := FetchOfficialPricingDocument(ctx, client, modelPricingSyncRegistryURL, 6)
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(body))
	require.Equal(t, []string{officialDeepSeekURL, officialKimiURL, modelPricingSyncRegistryURL}, requested)
}

func TestPriceSyncOfficialPublicGET(t *testing.T) {
	proxy := os.Getenv("PRICE_SYNC_OFFICIAL_READONLY_PROXY")
	if proxy == "" {
		t.Skip("set PRICE_SYNC_OFFICIAL_READONLY_PROXY to exercise actual public sources")
	}
	proxyURL, err := url.Parse(proxy)
	require.NoError(t, err)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	t.Cleanup(transport.CloseIdleConnections)
	fetcher := modelPricingSyncHTTPFetcher{client: &http.Client{Transport: transport}}
	collection := newOfficialPriceCache().collect(context.Background(), fetcher, true, time.Now().UTC())
	for endpoint, issue := range collection.SourceErrors {
		t.Errorf("%s: %s", endpoint, issue)
	}
	for model, why := range collection.Problems {
		t.Logf("gap %s: %s", model, why)
	}
	for _, model := range []string{"kimi-k3.1-pro", "kimi-k3.1-pro-highspeed", "deepseek-flash", "qwen3-asr-flash", "facechain-finetune", "wan2.6-t2i", "gpt-6.2-astra", "gpt-6.2-sol"} {
		quote, ok := collection.Quotes[model]
		if !ok {
			t.Errorf("missing %s: %s", model, collection.Problems[model])
			continue
		}
		t.Logf("%s %s/%s %s normal=%v tiers=%v windows=%v", model, quote.Currency, quote.BillingMode, quote.Evidence, quote.Rates, quote.Tiers, quote.Windows)
	}
}
