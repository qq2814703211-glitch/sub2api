package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	officialKimiURL     = "https://platform.kimi.com/docs/pricing/chat"
	officialDeepSeekURL = "https://api-docs.deepseek.com/zh-cn/quick_start/pricing.md"
	officialAliyunURL   = "https://help.aliyun.com/zh/model-studio/model-pricing.md?mode=pure"
	officialOpenAIURL   = "https://developers.openai.com/api/docs/pricing.md"
	officialLimit       = 2 << 20
	officialTimeout     = 8 * time.Second

	officialInput      = "input_cost_per_token"
	officialOutput     = "output_cost_per_token"
	officialCacheRead  = "cache_read_input_token_cost"
	officialCacheWrite = "cache_creation_input_token_cost"
	officialCache1h    = "cache_creation_input_token_cost_above_1hr"
	officialCache30m   = "cache_creation_input_token_cost_30m"
	officialCache12h   = "cache_creation_input_token_cost_12h"
	officialMillion    = 1e6
)

var officialOpenAIModels = []string{"gpt-6.2-astra", "gpt-6.2-sol"}

func officialOpenAIModelURL(model string) string {
	return "https://developers.openai.com/api/docs/models/" + model + ".md"
}

func newOfficialCollection() PriceSyncCollection {
	return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{}, NativeModels: map[string]bool{}, Problems: map[string]string{}, SourceErrors: map[string]string{}}
}

type officialSource struct {
	url, region string
	native      bool
	models      []string // exact managed IDs, not pricing data
	load        func(context.Context, modelPricingSyncFetcher) (map[string]PriceSyncQuote, map[string]string, error)
}

func officialSimpleSource(url, region string, models []string, parse func([]byte) (map[string]PriceSyncQuote, map[string]string)) officialSource {
	return officialSource{url: url, region: region, native: true, models: models, load: func(ctx context.Context, f modelPricingSyncFetcher) (map[string]PriceSyncQuote, map[string]string, error) {
		body, err := officialFetch(ctx, f, url)
		if err != nil {
			return nil, nil, err
		}
		quotes, problems := parse(body)
		for model, q := range quotes {
			q.SourceURL = url
			q.Evidence = fmt.Sprintf("region=%s; sha256:%x", region, sha256.Sum256(body))
			quotes[model] = q
		}
		return quotes, problems, nil
	}}
}

var officialSources = []officialSource{
	officialSimpleSource(officialKimiURL, "CN", officialKimiModels, parseOfficialKimi),
	officialSimpleSource(officialDeepSeekURL, "CN", officialDeepSeekModels, parseOfficialDeepSeek),
	officialSimpleSource(officialAliyunURL, "CN-Beijing", officialAliyunModels, parseOfficialAliyun),
	{url: officialOpenAIURL, region: "OpenAI API Standard", models: officialOpenAIModels, load: loadOfficialOpenAI},
}

var defaultOfficialPriceCache = newOfficialPriceCache()

func collectOfficialPriceSupplements(ctx context.Context, fetcher modelPricingSyncFetcher) PriceSyncCollection {
	return officialPriceCacheForFetcher(fetcher).collect(ctx, fetcher, true, time.Now().UTC())
}

// The USD supplement stays applicable when the domestic pricing setting is off.
func collectOpenAIOfficialPriceSupplements(ctx context.Context, fetcher modelPricingSyncFetcher) PriceSyncCollection {
	return officialPriceCacheForFetcher(fetcher).collect(ctx, fetcher, false, time.Now().UTC())
}

func officialPriceCacheForFetcher(fetcher modelPricingSyncFetcher) *officialPriceCache {
	if _, bounded := fetcher.(officialDocumentFetcher); bounded {
		return defaultOfficialPriceCache
	}
	// Legacy/injected fetchers may serve fixtures, not production documents.
	// Never let their quotes or destination failures enter the shared cache.
	return newOfficialPriceCache()
}

func officialPriceRequiresVendor(model string) bool {
	for _, source := range officialSources {
		if slices.Contains(source.models, model) {
			return true
		}
	}
	return false
}

type officialPriceCache struct {
	states     []officialSourceState
	fetchSlots chan struct{}
}

type officialSourceState struct {
	mu        sync.Mutex
	running   bool
	failures  int
	retryAt   time.Time
	lastError string
	good      map[string]PriceSyncQuote
	problems  map[string]string
}

func newOfficialPriceCache() *officialPriceCache {
	return &officialPriceCache{states: make([]officialSourceState, len(officialSources)), fetchSlots: make(chan struct{}, 3)}
}

func (c *officialPriceCache) collect(ctx context.Context, f modelPricingSyncFetcher, domestic bool, now time.Time) PriceSyncCollection {
	out := newOfficialCollection()
	results := make(chan PriceSyncCollection, len(officialSources))
	count := 0
	for i, source := range officialSources {
		if !domestic && source.native {
			continue
		}
		count++
		go func() { results <- c.collectSource(ctx, f, source, &c.states[i], now) }()
	}
	for range count {
		item := <-results
		maps.Copy(out.Quotes, item.Quotes)
		maps.Copy(out.NativeModels, item.NativeModels)
		maps.Copy(out.Problems, item.Problems)
		maps.Copy(out.SourceErrors, item.SourceErrors)
	}
	return out
}

func (c *officialPriceCache) collectSource(ctx context.Context, f modelPricingSyncFetcher, source officialSource, state *officialSourceState, now time.Time) PriceSyncCollection {
	state.mu.Lock()
	if state.running || now.Before(state.retryAt) || ctx.Err() != nil {
		reason := "refresh already in flight"
		if ctx.Err() != nil {
			reason = ctx.Err().Error()
		} else if !state.running {
			reason = fmt.Sprintf("backoff until %s: %s", state.retryAt.Format(time.RFC3339), state.lastError)
		}
		out := officialSnapshot(state, source, reason)
		state.mu.Unlock()
		return out
	}
	state.running = true
	state.mu.Unlock()

	var quotes map[string]PriceSyncQuote
	var problems map[string]string
	var err error
	select {
	case c.fetchSlots <- struct{}{}:
		bounded, cancel := context.WithTimeout(ctx, officialTimeout)
		quotes, problems, err = source.load(bounded, f)
		cancel()
		<-c.fetchSlots
	case <-ctx.Done():
		err = ctx.Err()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.running = false
	if ctx.Err() != nil {
		return officialSnapshot(state, source, ctx.Err().Error())
	}
	if state.good == nil {
		state.good = make(map[string]PriceSyncQuote)
	}
	state.problems = maps.Clone(problems)
	if state.problems == nil {
		state.problems = make(map[string]string)
	}
	for model, q := range quotes {
		q.VerifiedAt = now
		state.good[model] = q
	}
	var missing []string
	for _, model := range source.models {
		if _, ok := quotes[model]; ok || officialExpectedGap(model) {
			continue
		}
		missing = append(missing, model)
	}
	message := ""
	if err != nil {
		message = err.Error()
	} else if len(missing) > 0 {
		var details []string
		for _, model := range missing {
			issue := state.problems[model]
			if issue == "" {
				issue = "exact quote missing"
			}
			details = append(details, model+": "+issue)
		}
		message = "incomplete official document: " + strings.Join(details, "; ")
	}
	if message != "" {
		state.failures = min(state.failures+1, 7)
		state.retryAt = now.Add(time.Minute * time.Duration(1<<uint(state.failures-1)))
		state.lastError = message
	} else {
		state.failures = 0
		state.retryAt = time.Time{}
		state.lastError = ""
	}
	return officialSnapshot(state, source, message)
}

func officialSnapshot(state *officialSourceState, source officialSource, message string) PriceSyncCollection {
	out := newOfficialCollection()
	maps.Copy(out.Problems, state.problems)
	if message != "" {
		out.SourceErrors[source.url] = message
	}
	for _, model := range source.models {
		if source.native {
			out.NativeModels[model] = true
		}
		if q, ok := state.good[model]; ok {
			out.Quotes[model] = cloneOfficialQuote(q)
		} else if _, ok := out.Problems[model]; !ok {
			out.Problems[model] = fmt.Sprintf("%s: no complete %s official quote (%s)", source.url, source.region, message)
		}
	}
	return out
}

func cloneOfficialQuote(q PriceSyncQuote) PriceSyncQuote {
	q.Rates = maps.Clone(q.Rates)
	q.Tiers = slices.Clone(q.Tiers)
	for i := range q.Tiers {
		q.Tiers[i].Rates = maps.Clone(q.Tiers[i].Rates)
	}
	q.Windows = slices.Clone(q.Windows)
	for i := range q.Windows {
		q.Windows[i].Rates = maps.Clone(q.Windows[i].Rates)
		q.Windows[i].Weekdays = slices.Clone(q.Windows[i].Weekdays)
	}
	return q
}

// Expected gaps are diagnosed per SKU, without treating a healthy source as down.
func officialExpectedGap(model string) bool {
	switch model {
	case "deepseek-v4.1-flash", "deepseek-v4.1-flash-1017", "qwen-image-3.0", "qwen-image-3.0-pro", "happyoyster-1.0-adventure":
		return true
	default:
		return false
	}
}

type officialDocumentFetcher interface {
	FetchOfficialPricingDocument(context.Context, string, int64) ([]byte, error)
}

func officialFetch(ctx context.Context, f modelPricingSyncFetcher, url string) ([]byte, error) {
	if !officialAllowedURL(url) {
		return nil, fmt.Errorf("endpoint not in official pricing allowlist")
	}
	if f == nil {
		return nil, fmt.Errorf("pricing fetcher not configured")
	}
	var body []byte
	var err error
	if limited, ok := f.(officialDocumentFetcher); ok {
		body, err = limited.FetchOfficialPricingDocument(ctx, url, officialLimit)
	} else {
		body, err = f.FetchPricingJSON(ctx, url)
	}
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > officialLimit {
		return nil, fmt.Errorf("invalid official pricing document size: %d", len(body))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

func officialAllowedURL(url string) bool {
	switch url {
	case officialKimiURL, officialDeepSeekURL, officialAliyunURL, officialOpenAIURL:
		return true
	}
	return slices.ContainsFunc(officialOpenAIModels, func(model string) bool { return url == officialOpenAIModelURL(model) })
}

func (f modelPricingSyncHTTPFetcher) FetchOfficialPricingDocument(ctx context.Context, url string, limit int64) ([]byte, error) {
	return FetchOfficialPricingDocument(ctx, f.client, url, limit)
}

// FetchOfficialPricingDocument reuses an existing client's proxy and transport,
// disables redirects/cookies, and bounds decompressed bytes before allocation.
func FetchOfficialPricingDocument(ctx context.Context, client *http.Client, endpoint string, limit int64) ([]byte, error) {
	accept, maxBytes, timeout := "application/json", int64(modelPricingSyncBodyLimit), 25*time.Second
	if officialAllowedURL(endpoint) {
		accept, maxBytes, timeout = "text/markdown", officialLimit, officialTimeout
		if endpoint == officialKimiURL {
			accept = "text/html"
		}
	} else if !officialAllowedRegistryURL(endpoint) {
		return nil, fmt.Errorf("endpoint not in pricing source allowlist")
	}
	if client == nil || limit <= 0 || limit > maxBytes {
		return nil, fmt.Errorf("invalid official pricing request")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	safeClient := &http.Client{Transport: client.Transport, Timeout: client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rsp, err := safeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rsp.Body.Close() }()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", rsp.StatusCode)
	}
	if rsp.ContentLength > limit {
		return nil, fmt.Errorf("official document exceeds %d bytes", limit)
	}
	body, err := io.ReadAll(io.LimitReader(rsp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("official document exceeds %d bytes", limit)
	}
	return body, nil
}

// The same bounded production transport is shared by the two aggregate feeds.
func officialAllowedRegistryURL(endpoint string) bool {
	if endpoint == modelPricingSyncRegistryURL || endpoint == cnyOfficialDefaultSourceURL {
		return true
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	const prefix, suffix = "/basellm/llm-metadata/", "/dist/api/all.json"
	if !strings.HasPrefix(u.Path, prefix) || !strings.HasSuffix(u.Path, suffix) {
		return false
	}
	rev := strings.TrimSuffix(strings.TrimPrefix(u.Path, prefix), suffix)
	return len(rev) == 40 && strings.Trim(rev, "0123456789abcdef") == ""
}
