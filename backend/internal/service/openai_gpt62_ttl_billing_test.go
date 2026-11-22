package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gpt62OfficialQuotes(t *testing.T) map[string]PriceSyncQuote {
	t.Helper()
	pricing, err := os.ReadFile(filepath.Join("testdata", "price_sync_official", "openai-pricing.md"))
	require.NoError(t, err)
	docs := map[string][]byte{}
	for _, model := range officialOpenAIModels {
		file := "sol.md"
		if model == "gpt-6.2-astra" {
			file = "astra.md"
		}
		body, err := os.ReadFile(filepath.Join("testdata", "price_sync_official", file))
		require.NoError(t, err)
		docs[model] = body
	}
	quotes, problems := parseOfficialOpenAI(pricing, docs)
	require.Empty(t, problems)
	require.Len(t, quotes, 2)
	for model, quote := range quotes {
		quote.SourceURL = officialOpenAIURL + "#standard-pricing-data"
		quote.Evidence = "fixture official OpenAI Standard"
		quote.VerifiedAt = time.Now().UTC()
		quotes[model] = quote
	}
	return quotes
}

func gpt62UsageFromProtocol(t *testing.T, payload string, sse bool) UsageTokens {
	t.Helper()
	if !sse {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload))
		require.True(t, ok)
		return usage.billingUsageTokens()
	}
	var usage OpenAIUsage
	(&OpenAIGatewayService{}).parseSSEUsageBytesWithType([]byte(payload), "response.completed", &usage)
	return usage.billingUsageTokens()
}

func TestOpenAIGPT62ProtocolTTLBucketsAndAggregateDiagnostic(t *testing.T) {
	ctx := context.Background()
	quotes := gpt62OfficialQuotes(t)
	s, billing := publishFixture(t, &publishTestStore{})
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{Quotes: quotes}, nil
	}
	_, err := s.Sync(ctx)
	require.NoError(t, err)

	shortBody := `{"usage":{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":300,"cache_write_tokens_details":{"ttl_30m_tokens":100,"ttl_12h_tokens":200}}}}`
	shortSSE := `{"type":"response.completed","response":{"usage":{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":300,"cache_write_tokens_details":{"ttl_30m_tokens":100,"ttl_12h_tokens":200}}}}}`
	longBody := `{"usage":{"input_tokens":272301,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":300,"cache_write_tokens_details":{"ttl_30m_tokens":100,"ttl_12h_tokens":200}}}}`
	aggregateOnly := `{"usage":{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":300}}}`
	badSplit := `{"usage":{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":300,"cache_write_tokens_details":{"ttl_30m_tokens":400,"ttl_12h_tokens":200}}}}`

	for model, q := range quotes {
		t.Run(model, func(t *testing.T) {
			short30, short12 := q.Rates["cache_creation_input_token_cost_30m"], q.Rates["cache_creation_input_token_cost_12h"]
			long30, long12 := q.Tiers[0].Rates["cache_creation_input_token_cost_30m"], q.Tiers[0].Rates["cache_creation_input_token_cost_12h"]

			for _, tc := range []struct {
				name    string
				payload string
				sse     bool
			}{{"json", shortBody, false}, {"sse", shortSSE, true}} {
				tokens := gpt62UsageFromProtocol(t, tc.payload, tc.sse)
				require.Equal(t, 700, tokens.InputTokens, tc.name)
				require.Equal(t, 300, tokens.CacheCreationTokens, tc.name)
				require.Equal(t, 100, tokens.CacheCreation30mTokens, tc.name)
				require.Equal(t, 200, tokens.CacheCreation12hTokens, tc.name)

				cost, err := billing.CalculateCost(model, tokens, 1)
				require.NoError(t, err, tc.name)
				require.InDelta(t, 100*short30+200*short12, cost.CacheCreationCost, 1e-14)
				want := float64(tokens.InputTokens)*q.Rates["input_cost_per_token"] +
					float64(tokens.OutputTokens)*q.Rates["output_cost_per_token"] + cost.CacheCreationCost
				require.InDelta(t, want, cost.ActualCost, 1e-12)
				require.False(t, cost.LongContextBillingApplied, tc.name)
			}

			longTokens := gpt62UsageFromProtocol(t, longBody, false)
			require.Equal(t, 272001, longTokens.InputTokens)
			longCost, err := billing.CalculateCost(model, longTokens, 1)
			require.NoError(t, err)
			require.True(t, longCost.LongContextBillingApplied)
			require.InDelta(t, 100*long30+200*long12, longCost.CacheCreationCost, 1e-14)
			require.InDelta(t, float64(longTokens.InputTokens)*q.Tiers[0].Rates["input_cost_per_token"]+
				float64(longTokens.OutputTokens)*q.Tiers[0].Rates["output_cost_per_token"]+longCost.CacheCreationCost,
				longCost.ActualCost, 1e-12)

			// Aggregate-only writes have no proven lifetime split. The GPT quote
			// has no ordinary cache-write component, so the tariff must raise a
			// reconciliation issue instead of charging either 30m or 12h.
			tokens := gpt62UsageFromProtocol(t, aggregateOnly, false)
			require.Zero(t, tokens.CacheCreation30mTokens)
			require.Zero(t, tokens.CacheCreation12hTokens)
			_, err = billing.CalculateCost(model, tokens, 1)
			require.Error(t, err)
			require.True(t, isTariffPriceIssue(err))
			require.Contains(t, err.Error(), "cache_creation_input_token_cost")

			// A split that does not add up is rejected by the protocol adapter;
			// the aggregate stays and gets the same explicit diagnostic.
			tokens = gpt62UsageFromProtocol(t, badSplit, false)
			require.Zero(t, tokens.CacheCreation30mTokens)
			require.Zero(t, tokens.CacheCreation12hTokens)
			require.Equal(t, 300, tokens.CacheCreationTokens)
			_, err = billing.CalculateCost(model, tokens, 1)
			require.True(t, isTariffPriceIssue(err))
		})
	}
}
