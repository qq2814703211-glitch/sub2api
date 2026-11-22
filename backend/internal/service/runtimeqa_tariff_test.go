//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRuntimeQATariffNoGroupRequestClock(t *testing.T) {
	p := newStubPricingServiceFromJSON(t, tariffWindowFixture)
	b := NewBillingService(&config.Config{}, p)
	ctx := context.Background()
	peak := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	trough := peak.Add(4 * time.Hour)
	old := pinRequestCatalog(ctx, b, nil, false)
	RequestCatalogFromContext(old).at = peak
	RequestCatalogFromContext(old).billing.requestPricingAt = peak
	// Publish a different row while the pinned request is in flight.
	p.pricingData = newStubPricingServiceFromJSON(t, strings.ReplaceAll(tariffWindowFixture, `"input_cost_per_token":7`, `"input_cost_per_token":19`)).pricingData
	for _, tc := range []struct {
		name string
		ctx  context.Context
		at   time.Time
		want float64
	}{
		{"old_pin_request_time", old, time.Time{}, 14},
		{"explicit_request_time_old_version", old, trough, 4},
		{"explicit_peak_new_version", ctx, peak, 38},
		{"explicit_trough_new_version", ctx, trough, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.CalculateTokenCostForRequest(TokenCostRequest{Ctx: tc.ctx, Model: "deepseek-flash", Tokens: UsageTokens{InputTokens: 2}, RateMultiplier: 1, PricingAt: tc.at})
			require.NoError(t, err)
			require.Equal(t, tc.want, got.ActualCost)
		})
	}
	// A non-commercial pre-resolution from a newer view cannot replace the
	// pinned row when there is no group to own a selling card.
	resolver := NewModelPricingResolver(nil, b)
	pre := resolver.Resolve(ctx, PricingInput{Model: "deepseek-flash"})
	got, err := b.CalculateTokenCostForRequest(TokenCostRequest{Ctx: old, Model: "deepseek-flash", Tokens: UsageTokens{InputTokens: 2}, RateMultiplier: 1, Resolver: resolver, Resolved: pre})
	require.NoError(t, err)
	require.Equal(t, 14., got.ActualCost)
}

// The SQL usage repository routes unresolved prices to the existing Ops error
// store, not to settled usage or to the monetary dedup table.
type runtimeQAUsageAudit struct {
	openAIRecordUsageLogRepoStub
	incidents   []*OpsInsertErrorLogInput
	incidentErr error
}

func (r *runtimeQAUsageAudit) RecordTariffIssue(_ context.Context, in *OpsInsertErrorLogInput) error {
	r.incidents = append(r.incidents, in)
	return r.incidentErr
}

func TestRuntimeQATariffRecordUsage(t *testing.T) {
	const sku = "qa-managed"
	const plain = `{"qa-managed":{"glory_price_source":{"provider":"vendor","currency":"USD"},"input_cost_per_token":2,"output_cost_per_token":3},"qa-sibling":{"input_cost_per_token":50,"output_cost_per_token":30}}`
	brokenCalendar := strings.ReplaceAll(tariffWindowFixture, "deepseek-flash", sku)
	for _, protocol := range []string{"gateway", "openai"} {
		for _, tc := range []struct {
			name, rows, component string
			at                    time.Time
		}{
			{"missing_cache", plain, "cache_creation_input_token_cost", time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)},
			{"unknown_holiday", brokenCalendar, "calendar", time.Date(2028, 3, 6, 2, 0, 0, 0, time.UTC)},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				p := newStubPricingServiceFromJSON(t, tc.rows)
				cfg := &config.Config{}
				cfg.Default.RateMultiplier = 1
				billing := NewBillingService(cfg, p)
				logs := &runtimeQAUsageAudit{}
				ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
				user := &User{ID: 1}
				key := &APIKey{ID: 2, Quota: 99}
				account := &Account{ID: 3, Type: AccountTypeAPIKey, Platform: PlatformOpenAI}
				ctx := context.Background()
				// Both include an explicitly viable sibling and a non-zero search surcharge;
				// neither is evidence for the selected SKU's missing tariff.
				asGateway := &GatewayService{cfg: cfg, billingService: billing, usageBillingRepo: ledger, usageLogRepo: logs, deferredService: &DeferredService{}}
				asOpenAI := &OpenAIGatewayService{cfg: cfg, billingService: billing, usageBillingRepo: ledger, usageLogRepo: logs, deferredService: &DeferredService{}}
				failTime := func() context.Context {
					pin := RequestCatalogFromContext(asOpenAI.WithRequestCatalog(ctx))
					pin.at = tc.at
					return pin.Context(ctx)
				}
				old := failTime()
				record := func(ctx context.Context) error {
					if protocol == "gateway" {
						u := ClaudeUsage{InputTokens: 2, OutputTokens: 1}
						if tc.name == "missing_cache" {
							u.CacheCreationInputTokens = 2
							u.CacheCreation5mTokens = 2
						}
						return asGateway.RecordUsage(ctx, &RecordUsageInput{User: user, APIKey: key, Account: account, PricingAt: tc.at, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, ChannelUsageFields: ChannelUsageFields{OriginalModel: sku, BillingModelSource: BillingModelSourceRequested}, Result: &ForwardResult{Model: sku, UpstreamModel: "qa-sibling", RequestID: "event", Usage: u, SearchCount: 1}})
					}
					u := OpenAIUsage{InputTokens: 2, OutputTokens: 1}
					if tc.name == "missing_cache" {
						u.InputTokens = 4
						u.CacheCreationInputTokens = 2
						u.CacheCreation12hInputTokens = 2
					}
					return asOpenAI.RecordUsage(ctx, &OpenAIRecordUsageInput{User: user, APIKey: key, Account: account, PricingAt: tc.at, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, Result: &OpenAIForwardResult{Model: "qa-sibling", BillingModel: sku, RequestID: "event", Usage: u, SearchCount: 1}})
				}
				err := record(old)
				var issue *TariffPriceIssue
				require.ErrorAs(t, err, &issue)
				require.Equal(t, sku, issue.Model)
				require.Contains(t, issue.Reason, tc.component)
				require.Zero(t, ledger.calls)
				require.Zero(t, logs.calls, "no ordinary zero-cost row may own the retry key")
				require.Len(t, logs.incidents, 1)
				inc := logs.incidents[0]
				require.Equal(t, "event", inc.RequestID)
				require.Equal(t, int64(2), *inc.APIKeyID)
				require.Equal(t, sku, inc.Model)
				require.Contains(t, inc.ErrorMessage, tc.component)
				var body struct {
					PricingAt   time.Time   `json:"pricing_at"`
					Tokens      UsageTokens `json:"tokens"`
					SearchCount int         `json:"search_count"`
				}
				require.NoError(t, json.Unmarshal([]byte(inc.ErrorBody), &body))
				require.True(t, body.PricingAt.Equal(tc.at))
				require.Equal(t, 2, body.Tokens.InputTokens)
				require.Equal(t, 1, body.Tokens.OutputTokens)
				require.Equal(t, 1, body.SearchCount)
				if tc.name == "missing_cache" {
					require.Equal(t, 2, body.Tokens.CacheCreationTokens)
					if protocol == "openai" {
						require.Equal(t, 2, body.Tokens.CacheCreation12hTokens)
					} else {
						require.Equal(t, 2, body.Tokens.CacheCreation5mTokens)
					}
				}
				// Audit storage failure cannot mask the tariff error, the lost evidence,
				// or leave a charge behind.
				logs.incidentErr = errors.New("test audit unavailable")
				err = record(old)
				require.ErrorIs(t, err, logs.incidentErr)
				require.ErrorAs(t, err, &issue)
				require.Zero(t, ledger.calls)
				logs.incidentErr = nil
				// An operator supplies verified rates for the original request time; only
				// this accounting replay (not provider HTTP) may claim the original key.
				fixed := strings.Replace(plain, `"output_cost_per_token":3}`, `"output_cost_per_token":3,"cache_creation_input_token_cost":5,"cache_creation_input_token_cost_12h":5}`, 1)
				p.pricingData = newStubPricingServiceFromJSON(t, fixed).pricingData
				require.NoError(t, record(failTime()))
				require.Equal(t, 1, ledger.calls)
				require.Equal(t, "event", ledger.lastCmd.RequestID)
				want := 7.005
				if tc.name == "missing_cache" {
					want = 17.005
				}
				require.InDelta(t, want, ledger.lastCmd.BalanceCost, 1e-10)
				require.InDelta(t, want, ledger.lastCmd.APIKeyQuotaCost, 1e-10)
				require.Equal(t, ledger.lastCmd.BalanceCost, logs.lastLog.ActualCost)
			})
		}
	}
}

func TestRuntimeQATariffCommercialAndMissingControls(t *testing.T) {
	const sku = "qa-managed"
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	rows := `{"qa-managed":{"glory_price_source":{"provider":"vendor","currency":"USD"},"input_cost_per_token":2,"output_cost_per_token":3},"qa-priced-legacy":{"input_cost_per_token":5,"cache_read_input_token_cost":1}}`
	b := NewBillingService(cfg, newStubPricingServiceFromJSON(t, rows))
	ch := &ChannelService{}
	flat := func(p float64) ChannelModelPricing {
		return ChannelModelPricing{Models: []string{sku}, Platform: PlatformOpenAI, InputPrice: &p, CacheReadPrice: tariffTestPtr(0.)}
	}
	ch.cache.Store(populateChannelCache([]Channel{{ID: 8, Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{flat(7)}}}, map[int64]string{7: PlatformOpenAI}))
	for _, protocol := range []string{"gateway", "openai"} {
		for _, tc := range []struct {
			name, requested, upstream string
			group                     bool
			want                      float64
		}{
			{"group_card", sku, sku, true, 22}, {"channel_card", sku, sku, false, 14}, {"legacy_all_missing", "qa-absent", "qa-absent", false, 0}, {"legacy_fallback", "qa-absent", "qa-priced-legacy", false, 11},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				log := &runtimeQAUsageAudit{}
				ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
				deferred := &DeferredService{}
				grp := &Group{ID: 7, Platform: PlatformOpenAI, RateMultiplier: 1}
				if tc.group {
					grp.ModelPricing = []ChannelModelPricing{flat(11)}
				}
				k := &APIKey{ID: 2, Group: grp, GroupID: &grp.ID, Quota: 100}
				u := &User{ID: 1}
				a := &Account{ID: 3, Type: AccountTypeAPIKey}
				r := NewModelPricingResolver(ch, b)
				ctx := context.Background()
				fields := ChannelUsageFields{BillingModelSource: BillingModelSourceRequested, OriginalModel: tc.requested}
				if protocol == "gateway" {
					g := &GatewayService{cfg: cfg, channelService: ch, resolver: r, billingService: b, usageLogRepo: log, usageBillingRepo: ledger, deferredService: deferred}
					require.NoError(t, g.RecordUsage(g.WithRequestCatalog(ctx), &RecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, ChannelUsageFields: fields, Result: &ForwardResult{Model: tc.requested, UpstreamModel: tc.upstream, RequestID: tc.name, Usage: ClaudeUsage{InputTokens: 2, CacheReadInputTokens: 1}}}))
				} else {
					g := &OpenAIGatewayService{cfg: cfg, channelService: ch, resolver: r, billingService: b, usageLogRepo: log, usageBillingRepo: ledger, deferredService: deferred}
					require.NoError(t, g.RecordUsage(g.WithRequestCatalog(ctx), &OpenAIRecordUsageInput{User: u, APIKey: k, Account: a, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, ChannelUsageFields: fields, Result: &OpenAIForwardResult{Model: tc.requested, UpstreamModel: tc.upstream, RequestID: tc.name, Usage: OpenAIUsage{InputTokens: 3, CacheReadInputTokens: 1}}}))
				}
				require.Equal(t, 1, ledger.calls)
				require.Empty(t, log.incidents)
				require.Equal(t, tc.want, ledger.lastCmd.BalanceCost)
				require.Equal(t, tc.want, ledger.lastCmd.APIKeyQuotaCost)
				require.Equal(t, tc.want, log.lastLog.ActualCost)
			})
		}
	}
}

// Explicit cache storage reservation is not the ledger: an unresolved token
// window does not waive an independently known storage charge during preflight.
func TestRuntimeQATariffStorageEstimateDoesNotSettle(t *testing.T) {
	const model = "deepseek-flash"
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	p := newStubPricingServiceFromJSON(t, strings.Replace(tariffWindowFixture, `"cny_official":true`, `"cny_official":true,"cache_storage_cost_per_token_per_hour":0.25`, 1))
	log := &runtimeQAUsageAudit{}
	ledger := &openAIRecordUsageBillingRepoStub{}
	g := &GatewayService{cfg: cfg, billingService: NewBillingService(cfg, p), usageLogRepo: log, usageBillingRepo: ledger}
	in := &RecordUsageInput{User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3}, Result: &ForwardResult{RequestID: "storage", Model: model, Usage: ClaudeUsage{InputTokens: 1}, CacheStorageTokenHours: 4}, PricingAt: time.Date(2028, 3, 6, 2, 0, 0, 0, time.UTC)}
	require.Equal(t, 1., g.QuoteUsageCost(context.Background(), in))
	require.Empty(t, log.incidents)
	var issue *TariffPriceIssue
	require.ErrorAs(t, g.RecordUsage(context.Background(), in), &issue)
	require.Zero(t, ledger.calls)
	require.Len(t, log.incidents, 1)
}
