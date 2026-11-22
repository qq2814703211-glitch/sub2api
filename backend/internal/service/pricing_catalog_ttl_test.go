//go:build unit

package service

import (
	"context"

	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const ttlCatalogModel = "gpt-6-luna"

const ttlCatalog = `{"gpt-6-luna":{
  "litellm_provider":"openai","input_cost_per_token":0.000002,"output_cost_per_token":0.000003,
  "cache_read_input_token_cost":0.000001,"cache_creation_input_token_cost":0.000004,
  "cache_creation_input_token_cost_above_1hr":0.000031,
  "cache_creation_input_token_cost_30m":0.000007,"cache_creation_input_token_cost_12h":0.000019,
  "long_context_input_token_threshold":100,"long_context_input_cost_multiplier":3,
  "long_context_output_cost_multiplier":1.5
}}`

func TestPricingCatalogTTLForwardSettlement(t *testing.T) {
	gpt26RestoreGinMode(t)
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "override.json")
	catalog := NewPricingService(cfg, nil)
	require.NoError(t, os.WriteFile(catalog.getPricingFilePath(), []byte(ttlCatalog), 0600))
	// Model the published override file, not the manual settings writer (owned by publish).
	writeOverride := func(body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, []byte(body), 0600))
		require.NoError(t, catalog.reloadCustomPricingLayers())
	}
	writeOverride(`{}`)

	for _, tc := range []struct {
		name, patch, details, tier string
		input                      int
		wantWrite, wantTotal       float64 // micro site units
		wantLong                   bool
	}{
		{"catalog", "", `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 616, 641, false},
		{"official override", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":0.000011,"cache_creation_input_token_cost_12h":0.000023}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 776, 801, false},
		{"fast and long", `{}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "priority", 110, 3696, 4539, true},
		{"manual free 30m", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":0,"cache_creation_input_token_cost_12h":0.000013}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 364, 389, false},
		{"manual free 12h", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":0.000017,"cache_creation_input_token_cost_12h":0}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 204, 229, false},
		{"missing 30m", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":null,"cache_creation_input_token_cost_12h":0}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 48, 73, false},
		{"missing 12h", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":0,"cache_creation_input_token_cost_12h":null}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 140, 165, false},
		{"unknown detail", "", `{"ttl_12h_tokens":40}`, "default", 50, 160, 185, false},
		{"remove override", `{}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 616, 641, false},
		{"remove exact fields", `{"gpt-6-luna":{"cache_creation_input_token_cost_30m":null,"cache_creation_input_token_cost_12h":null}}`, `{"ttl_30m_tokens":12,"ttl_12h_tokens":28}`, "default", 50, 188, 213, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.patch != "" {
				writeOverride(tc.patch)
			}
			// Send real JSON/SSE forwarding results into RecordUsage without reconstructing usage.
			stream := tc.tier == "priority"
			payload := fmt.Sprintf(`{"id":"resp_catalog_ttl","object":"response","status":"completed","model":%q,"service_tier":%q,"output":[],"usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":40,"cache_write_tokens_details":%s},"output_tokens":3}}`, ttlCatalogModel, tc.tier, tc.input, tc.details)
			ct := "application/json"
			if stream {
				progress := "data: {\"type\":\"response.in_progress\",\"response\":" + payload + "}\n\n"
				payload, ct = progress+progress+"data: {\"type\":\"response.completed\",\"response\":"+payload+"}\n\n", "text/event-stream"
			}
			forward := newOpenAIImagesTestService(&httpUpstreamRecorder{resp: gpt26Response(payload, ct)})
			body := []byte(fmt.Sprintf(`{"model":%q,"input":"hello","service_tier":%q,"stream":%t,"prompt_cache_options":{"ttl":"12h"}}`, ttlCatalogModel, tc.tier, stream))
			c, _ := newOpenAIImagesTestContext(t, body)
			c.Request.URL.Path = "/v1/responses"
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := &Account{
				ID: 35, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: float64Ptr(1.75),
				Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.openai.com/v1"},
				Extra:       map[string]any{"use_responses_api": true, "quota_limit": 100.0},
			}
			result, err := forward.Forward(ctx, c, account, body)
			require.NoError(t, err)

			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.billingService = NewBillingService(cfg, catalog)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			group := &Group{ID: 77, Platform: PlatformOpenAI, RateMultiplier: 0.625, LongContextPricingEnabled: true}
			require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
				Result: result, APIKey: &APIKey{ID: 1, GroupID: &group.ID, Group: group, Quota: 100},
				APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, User: &User{ID: 1}, Account: account,
			}))
			log := usageRepo.lastLog
			require.NotNil(t, log)
			require.Equal(t, tc.input-44, log.InputTokens)
			require.Equal(t, 4, log.CacheReadTokens)
			require.Equal(t, 40, log.CacheCreationTokens)
			require.Zero(t, log.CacheCreation5mTokens)
			require.Zero(t, log.CacheCreation1hTokens)
			if tc.name == "unknown detail" {
				require.Zero(t, log.CacheCreation30mTokens)
				require.Zero(t, log.CacheCreation12hTokens)
			} else {
				require.Equal(t, 12, log.CacheCreation30mTokens)
				require.Equal(t, 28, log.CacheCreation12hTokens)
			}
			require.InDelta(t, tc.wantWrite/1e6, log.CacheCreationCost, 1e-12)
			require.InDelta(t, tc.wantTotal/1e6, log.TotalCost, 1e-12)
			require.Equal(t, tc.wantLong, log.LongContextBillingApplied)
			require.InDelta(t, tc.wantTotal*0.625/1e6, log.ActualCost, 1e-12)
			require.Equal(t, 1, billingRepo.calls)
			const halfLedgerUnit = 5.001e-9 // commands round to NUMERIC(20,8)
			require.InDelta(t, tc.wantTotal*0.625/1e6, billingRepo.lastCmd.BalanceCost, halfLedgerUnit)
			require.Equal(t, billingRepo.lastCmd.BalanceCost, billingRepo.lastCmd.APIKeyQuotaCost)
			require.InDelta(t, tc.wantTotal*1.75/1e6, billingRepo.lastCmd.AccountQuotaCost, halfLedgerUnit)
			if tc.name == "manual free 30m" || tc.name == "manual free 12h" || tc.name == "remove override" {
				pricing, err := svc.billingService.GetModelPricing(ttlCatalogModel)
				require.NoError(t, err)
				require.Equal(t, tc.name != "remove override", pricing.priceOverrides.cacheWrite30m)
				require.Equal(t, tc.name != "remove override", pricing.priceOverrides.cacheWrite12h)
			}
		})
	}
}

func TestPricingCatalogTTLPlazaProjection(t *testing.T) {
	catalog := newStubPricingServiceFromJSON(t, strings.Replace(ttlCatalog, `"cache_creation_input_token_cost_30m":0.000007`, `"cache_creation_input_token_cost_30m":0`, 1))
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, ttlCatalogModel)}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 2}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI}, catalog)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	official := out[0].Models[0].OfficialPricing
	require.NotNil(t, official)
	require.Len(t, official.Intervals, 2)
	check := func(short, long, hour *float64, multiplier float64) {
		t.Helper()
		require.NotNil(t, short)
		require.Zero(t, *short)
		require.NotNil(t, long)
		require.InDelta(t, 19e-6*multiplier, *long, 1e-15)
		require.NotNil(t, hour)
		require.InDelta(t, 31e-6*multiplier, *hour, 1e-15)
	}
	check(official.CacheWrite30mPrice, official.CacheWrite12hPrice, official.CacheWrite1hPrice, 1)
	for i, tier := range official.Intervals {
		check(tier.CacheWrite30mPrice, tier.CacheWrite12hPrice, tier.CacheWrite1hPrice, []float64{1, 3}[i])
	}
}

func TestPricingCatalogTTLOperatorPrecedence(t *testing.T) {
	ctx := context.Background()
	bs := NewBillingService(nil, newStubPricingServiceFromJSON(t, ttlCatalog))
	for _, tc := range []struct {
		name, source string
		channel      ChannelModelPricing
		group        []ChannelModelPricing
		wantWrite    float64
	}{
		{name: "blank card inherits exact rates", source: PricingSourceChannel, wantWrite: 616},
		{name: "legacy channel flat", source: PricingSourceChannel, channel: ChannelModelPricing{CacheWritePrice: float64Ptr(13e-6)}, wantWrite: 520},
		{name: "channel legacy short and explicit long", source: PricingSourceChannel, channel: ChannelModelPricing{CacheWritePrice: float64Ptr(13e-6), CacheWrite12hPrice: float64Ptr(29e-6)}, wantWrite: 968},
		{name: "channel interval scales both TTL rates", source: PricingSourceChannel, channel: ChannelModelPricing{Intervals: []PricingInterval{{CacheWriteMultiplier: float64Ptr(4)}}}, wantWrite: 2464},
		{name: "group explicit zero outranks channel", source: PricingSourceGroup, channel: ChannelModelPricing{CacheWritePrice: float64Ptr(13e-6), CacheWrite12hPrice: float64Ptr(29e-6)}, group: []ChannelModelPricing{{Models: []string{ttlCatalogModel}, CacheWrite12hPrice: float64Ptr(0)}}, wantWrite: 84},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{ID: 77, Platform: PlatformOpenAI, LongContextPricingEnabled: true, ModelPricing: tc.group}
			tc.channel.Models, tc.channel.Platform = []string{ttlCatalogModel}, PlatformOpenAI
			channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{tc.channel}}
			cs := &ChannelService{}
			cs.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{group.ID: PlatformOpenAI}))
			resolver := NewModelPricingResolver(cs, bs)
			resolved := resolver.Resolve(ctx, PricingInput{Model: ttlCatalogModel, Group: group, GroupID: &group.ID})
			require.Equal(t, tc.source, resolved.Source)
			cost, err := bs.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: ttlCatalogModel, Group: group, GroupID: &group.ID, Resolved: resolved, Resolver: resolver,
				Tokens: UsageTokens{CacheCreationTokens: 40, CacheCreation30mTokens: 12, CacheCreation12hTokens: 28}, RateMultiplier: 1,
			})
			require.NoError(t, err)
			require.InDelta(t, tc.wantWrite/1e6, cost.CacheCreationCost, 1e-12)
		})
	}
}
