package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestPriceCollectCoverageArgonThinkingAliases(t *testing.T) {
	const base = "gemini-4-argon"
	bodies := priceCoverageOfficialBodies(t)
	bodies[modelPricingSyncRegistryURL] = []byte(`{"google":{"models":{
 "gemini-4-argon":{"id":"gemini-4-argon","cost":{"input":7,"output":17,"cache_read":0.15}},
 "gemini-4-carbon":{"id":"gemini-4-carbon","cost":{"input":8,"output":18,"cache_read":0.18}},
 "gemini-4-hydrogen":{"id":"gemini-4-hydrogen","cost":{"input":9,"output":19,"cache_read":0.19}},
 "gemini-4-neon":{"id":"gemini-4-neon","cost":{"input":10,"output":20,"cache_read":0.2}}
 }}}`)
	got, err := collectPriceSyncV2(context.Background(), priceCoverageFetcher(bodies), "", nil)
	require.NoError(t, err)
	require.Equal(t, "gemini-4-argon-high", domain.DefaultAntigravityModelMapping[base])
	account := &Account{Platform: PlatformAntigravity}
	const alias = "gemini-4-argon-high"
	require.Equal(t, alias, (&AntigravityGatewayService{}).getMappedModelForThinkingLevel(account, base, "high"))
	require.Equal(t, base, normalizeGeminiThinkingTierAlias(alias))
	q, ok := got.Quotes[alias]
	require.True(t, ok, got.Problems[alias])
	require.Equal(t, alias, q.Model)
	require.Equal(t, got.Quotes[base].Rates, q.Rates)
	require.Equal(t, "USD", q.Currency)
	require.Contains(t, q.Evidence, "transport=antigravity")
	for _, alias := range []string{"gemini-4-argon-low", "gemini-4-argon-medium", "gemini-4-argon-cyber-permissive", "gemini-4-argon-xhigh", "gemini-4-argon-tiered", "gemini-4-carbon-high", "gemini-4-hydrogen-xhigh", "gemini-4-neon-low"} {
		require.NotContains(t, got.Quotes, alias)
	}
}

func TestPriceCollectCoverageArgonPreservesExactSourceAndConflicts(t *testing.T) {
	bodies := priceCoverageOfficialBodies(t)
	bodies[modelPricingSyncRegistryURL] = []byte(`{"google":{"models":{
 "gemini-4-argon":{"id":"gemini-4-argon","cost":{"input":7,"output":17,"cache_read":0.15}},
 "gemini-4-argon-high":{"id":"gemini-4-argon-high","cost":{"input":40,"output":80}},
 "gemini-4-argon-low":{"id":"gemini-4-argon-low","cost":{"input":1,"output":null}}
 }}}`)
	got, err := collectPriceSyncV2(context.Background(), priceCoverageFetcher(bodies), "", nil)
	require.NoError(t, err)
	require.Equal(t, 80e-6, got.Quotes["gemini-4-argon-high"].Rates[officialOutput])
	require.NotContains(t, got.Quotes, "gemini-4-argon-low")
	require.Contains(t, got.Problems["gemini-4-argon-low"], "expected finite")
}
