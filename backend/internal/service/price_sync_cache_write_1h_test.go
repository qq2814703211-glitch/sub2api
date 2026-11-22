//go:build unit

package service

import (
	"encoding/json"
	"testing"
	"time"
)

func decodeCost(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var cost map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cost); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return cost
}

// Anthropic publishes a 1-hour cache write at twice the base input rate. The
// registry only carries the 5-minute component, so the collector fills the
// published multiple; without it every 1h cache request is refused at
// settlement as "requires an unquoted component".
func TestPriceSyncV2DerivesPublishedAnthropicCacheWrite1h(t *testing.T) {
	cost := decodeCost(t, `{
		"input": 2, "output": 10, "cache_read": 0.1, "cache_write": 2.5
	}`)
	priceSyncV2ApplyPublishedCacheWrite1h("anthropic", cost)

	quote := PriceSyncQuote{Model: "claude-sonnet-5-5", Provider: "anthropic",
		Currency: "USD", SourceURL: "https://models.dev/api.json", Evidence: "test", VerifiedAt: time.Now()}
	if err := priceSyncV2DecodeTariff(&quote, cost); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := quote.Rates["cache_creation_input_token_cost_above_1hr"]; got != 4e-6 {
		t.Fatalf("derived 1h cache write = %v, want 4e-06 (2x base input)", got)
	}
	if got := quote.Rates["cache_creation_input_token_cost"]; got != 2.5e-6 {
		t.Fatalf("5m cache write changed to %v", got)
	}
}

func TestPriceSyncV2KeepsQuotedCacheWrite1h(t *testing.T) {
	cost := decodeCost(t, `{
		"input": 2, "output": 10, "cache_write": 2.5, "cache_write_1h": 7
	}`)
	priceSyncV2ApplyPublishedCacheWrite1h("anthropic", cost)

	quote := PriceSyncQuote{Model: "m", Provider: "anthropic", Currency: "USD",
		SourceURL: "u", Evidence: "e", VerifiedAt: time.Now()}
	if err := priceSyncV2DecodeTariff(&quote, cost); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := quote.Rates["cache_creation_input_token_cost_above_1hr"]; got != 7e-6 {
		t.Fatalf("quoted 1h price was replaced: %v", got)
	}
}

func TestPriceSyncV2DoesNotDeriveForOtherProviders(t *testing.T) {
	cost := decodeCost(t, `{"input": 2, "output": 10, "cache_write": 2.5}`)
	priceSyncV2ApplyPublishedCacheWrite1h("openai", cost)

	quote := PriceSyncQuote{Model: "m", Provider: "openai", Currency: "USD",
		SourceURL: "u", Evidence: "e", VerifiedAt: time.Now()}
	if err := priceSyncV2DecodeTariff(&quote, cost); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, exists := quote.Rates["cache_creation_input_token_cost_above_1hr"]; exists {
		t.Fatal("non-anthropic provider acquired a derived 1h cache write")
	}
}

func TestPriceSyncV2DerivesCacheWrite1hPerTier(t *testing.T) {
	cost := decodeCost(t, `{
		"input": 2, "output": 10, "cache_write": 2.5,
		"tiers": [
			{"tier": {"type": "context", "size": 200000},
			 "input": 4, "output": 20, "cache_write": 5}
		]
	}`)
	priceSyncV2ApplyPublishedCacheWrite1h("anthropic", cost)

	quote := PriceSyncQuote{Model: "m", Provider: "anthropic", Currency: "USD",
		SourceURL: "u", Evidence: "e", VerifiedAt: time.Now()}
	if err := priceSyncV2DecodeTariff(&quote, cost); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(quote.Tiers) != 1 {
		t.Fatalf("tiers = %d", len(quote.Tiers))
	}
	if got := quote.Rates["cache_creation_input_token_cost_above_1hr"]; got != 4e-6 {
		t.Fatalf("base 1h = %v, want 4e-06", got)
	}
	if got := quote.Tiers[0].Rates["cache_creation_input_token_cost_above_1hr"]; got != 8e-6 {
		t.Fatalf("tier 1h = %v, want 8e-06 (2x tier input)", got)
	}
}

func TestPriceSyncV2SkipsDerivationWithoutCacheWrite(t *testing.T) {
	cost := decodeCost(t, `{"input": 2, "output": 10}`)
	priceSyncV2ApplyPublishedCacheWrite1h("anthropic", cost)

	quote := PriceSyncQuote{Model: "m", Provider: "anthropic", Currency: "USD",
		SourceURL: "u", Evidence: "e", VerifiedAt: time.Now()}
	if err := priceSyncV2DecodeTariff(&quote, cost); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, exists := quote.Rates["cache_creation_input_token_cost_above_1hr"]; exists {
		t.Fatal("1h cache write invented for a model without any cache write price")
	}
}
