package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// collectPriceSyncV2 only returns prices eligible for publication. Problems and
// SourceErrors describe reference-only rows and unavailable sources; a failure
// in one source is not a failure of already verified quotes from another.
func collectPriceSyncV2(ctx context.Context, fetcher modelPricingSyncFetcher, registryURL string, settings *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
	result := PriceSyncCollection{
		Quotes: map[string]PriceSyncQuote{}, NativeModels: map[string]bool{},
		Problems: map[string]string{}, SourceErrors: map[string]string{},
		LegacyUSDEstimates: map[string]string{},
	}
	if fetcher == nil {
		return result, fmt.Errorf("price sync collector requires a fetcher")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	nativeEnabled := settings != nil && settings.Enabled
	usdSource := strings.TrimSpace(registryURL)
	if usdSource == "" {
		usdSource = modelPricingSyncRegistryURL
	}
	collector := priceSyncV2Collector{result: &result, claims: map[string]string{}, conflicts: map[string]bool{}}
	collector.readRegistry(ctx, fetcher, usdSource, false, nativeEnabled)
	if nativeEnabled {
		source := strings.TrimSpace(settings.SourceURL)
		if source == "" {
			source = cnyOfficialDefaultSourceURL
		}
		collector.readRegistry(ctx, fetcher, source, true, true)
		if ctx.Err() == nil {
			collector.mergeSupplements(collectOfficialPriceSupplements(ctx, fetcher))
		}
	} else if ctx.Err() == nil {
		collector.mergeSupplements(collectOpenAIOfficialPriceSupplements(ctx, fetcher))
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	collector.collectArgonDefaultTransportAlias()
	collector.reportUnverifiedAliases()
	if len(result.Quotes) == 0 && len(result.SourceErrors) > 0 {
		return result, fmt.Errorf("price sync: no publishable quotes from available sources")
	}
	return result, nil
}

type priceSyncV2Collector struct {
	result *PriceSyncCollection
	// Identity claims include valid first-party rows that had no usable rates.
	claims    map[string]string
	conflicts map[string]bool
}

type priceSyncV2RegistryProvider struct {
	ID     string                     `json:"id"`
	Models map[string]json.RawMessage `json:"models"`
}

type priceSyncV2RegistryModel struct {
	ID   string                     `json:"id"`
	Cost map[string]json.RawMessage `json:"cost"`
}

func (c *priceSyncV2Collector) readRegistry(ctx context.Context, fetcher modelPricingSyncFetcher, endpoint string, nativeSource, nativeEnabled bool) {
	label, currency := "models.dev", "USD"
	if nativeSource {
		label, currency = "basellm", "CNY"
	}
	if !priceSyncV2RegistryURLAllowed(endpoint, nativeSource) {
		c.result.SourceErrors[label] = "pricing registry is not an allowed source"
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var body []byte
	var err error
	if bounded, ok := fetcher.(interface {
		FetchOfficialPricingDocument(context.Context, string, int64) ([]byte, error)
	}); ok {
		body, err = bounded.FetchOfficialPricingDocument(callCtx, endpoint, modelPricingSyncBodyLimit)
	} else {
		body, err = fetcher.FetchPricingJSON(callCtx, endpoint)
	}
	if err != nil {
		c.result.SourceErrors[label] = fmt.Sprintf("fetch pricing registry: %v", err)
		return
	}
	if len(body) == 0 || len(body) > modelPricingSyncBodyLimit {
		c.result.SourceErrors[label] = "pricing registry is empty or exceeds the response limit"
		return
	}
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(body, &providers); err != nil || len(providers) == 0 {
		c.result.SourceErrors[label] = "pricing registry is not a non-empty JSON object"
		return
	}
	evidence := fmt.Sprintf("%s sha256:%x", label, sha256.Sum256(body))
	verifiedAt := time.Now().UTC()
	if nativeSource {
		// The legacy CNY writer selected one vendor row per SKU. Record whether
		// that exact row is an upstream USD estimate before ownership claims can
		// filter it out; the publisher uses it only for legacy auto provenance.
		for model, estimate := range legacyCNYUpstreamEstimates(decodeCNYRegistry(providers), evidence) {
			c.result.LegacyUSDEstimates[model] = estimate
		}
	}
	legacyExcluded := map[string]string{}
	for _, providerID := range sortedStrings(providers) {
		known := priceSyncV2ProviderKnown(providerID)
		_, legacyCNY := cnyOfficialProviderWhitelist[providerID]
		if !known && (!nativeSource || !legacyCNY) {
			continue
		}
		var provider priceSyncV2RegistryProvider
		if err := json.Unmarshal(providers[providerID], &provider); err != nil || (provider.ID != "" && provider.ID != providerID) || provider.Models == nil {
			c.result.SourceErrors[label+"/"+providerID] = "malformed first-party provider or provider ID mismatch"
			continue
		}
		for _, sourceID := range sortedStrings(provider.Models) {
			model := normalizeModelPricingSyncModelName(sourceID)
			if model == "" || strings.TrimSpace(sourceID) != sourceID {
				continue
			}
			owner, native := priceSyncV2Ownership(providerID, model)
			if nativeSource && legacyCNY && (owner == "" || !priceSyncV2NativePaygo[providerID]) {
				legacyExcluded[model] = evidence + " " + providerID + "/" + sourceID + ": reference only; legacy CNY source excluded from exact first-party pay-as-you-go pricing"
			}
			if !known {
				continue
			}
			if native {
				c.result.NativeModels[model] = true
				if q, ok := c.result.Quotes[model]; ok && q.Currency != "CNY" {
					c.reject(model, "domestic ownership conflicts with USD registry row")
				}
			}
			if owner == "" || (nativeSource && !priceSyncV2NativePaygo[providerID]) || (!nativeSource && native) {
				if native && nativeEnabled && !nativeSource && c.result.Problems[model] == "" {
					c.result.Problems[model] = "reference only; no verified model-level CNY tariff"
				}
				continue
			}
			// A USD registry never supplies currency-policy fallback for a domestic SKU.
			if !nativeSource && c.result.NativeModels[model] {
				c.reject(model, "domestic ownership conflicts with USD registry row")
				continue
			}
			if !c.claim(model, owner, providerID, sourceID) {
				continue
			}
			var row priceSyncV2RegistryModel
			rowData := provider.Models[sourceID]
			if err := priceSyncV2UniqueJSON(rowData); err != nil {
				c.reject(model, evidence+" "+providerID+"/"+sourceID+": "+err.Error())
				continue
			}
			if err := json.Unmarshal(rowData, &row); err != nil || row.ID != sourceID {
				c.reject(model, evidence+" "+providerID+"/"+sourceID+": malformed model row or exact ID mismatch")
				continue
			}
			if row.Cost == nil {
				c.reject(model, evidence+" "+providerID+"/"+sourceID+": no model tariff")
				continue
			}
			if why := priceSyncV2NeedsVendor(model, owner); why != "" {
				c.reject(model, evidence+" "+providerID+"/"+sourceID+": reference only; "+why)
				continue
			}
			quote := PriceSyncQuote{Model: model, Provider: providerID, Currency: currency,
				SourceURL: endpoint, Evidence: evidence + " " + providerID + "/" + sourceID, VerifiedAt: verifiedAt}
			if err := priceSyncV2DecodeTariff(&quote, row.Cost); err != nil {
				c.reject(model, quote.Evidence+": "+err.Error())
				continue
			}
			if currency == "USD" && quote.BillingMode == "token" && quote.Rates["input_cost_per_token"] == 0 && quote.Rates["output_cost_per_token"] == 0 {
				c.reject(model, quote.Evidence+": reference only; all-zero USD catalog entry has no metered/free offer evidence")
				continue
			}
			c.result.Quotes[model] = quote
			delete(c.result.Problems, model)
		}
	}
	for model, issue := range legacyExcluded {
		// Diagnostics only. The excluded row acquires no ownership and may
		// not displace an independently resolved quote or a stronger problem.
		if _, eligible := c.result.Quotes[model]; !eligible && c.result.Problems[model] == "" {
			c.result.Problems[model] = issue
		}
	}
}

// encoding/json otherwise silently replaces a duplicated currency/rate/ID with
// the last occurrence. Inspect eligible rows only, isolating corrupt SKUs.
func priceSyncV2UniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var read func(int) error
	read = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("model tariff nesting exceeds limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		seen := map[string]bool{}
		for decoder.More() {
			if delim == '{' {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate or invalid JSON field %q", key)
				}
				seen[key] = true
			}
			if err := read(depth + 1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return read(0)
}

func (c *priceSyncV2Collector) claim(model, owner, endpoint, sourceID string) bool {
	identity := owner + "/" + endpoint + "/" + sourceID
	if prior := c.claims[model]; prior != "" && prior != identity {
		c.conflicts[model] = true
		c.reject(model, fmt.Sprintf("conflicting first-party model identity: %s and %s", prior, identity))
		return false
	}
	c.claims[model] = identity
	return !c.conflicts[model]
}

func (c *priceSyncV2Collector) reject(model, reason string) {
	delete(c.result.Quotes, model)
	c.result.Problems[model] = reason
}

func (c *priceSyncV2Collector) mergeSupplements(supplement PriceSyncCollection) {
	for key, issue := range supplement.SourceErrors {
		c.result.SourceErrors[key] = issue
	}
	for model, estimate := range supplement.LegacyUSDEstimates {
		c.result.LegacyUSDEstimates[model] = estimate
	}
	for model, native := range supplement.NativeModels {
		if native {
			c.result.NativeModels[model] = true
			if c.result.Quotes[model].Currency == "USD" {
				c.reject(model, "domestic model cannot fall back to USD")
			}
		}
	}
	for model, problem := range supplement.Problems {
		// First-party diagnostics supersede the aggregate for the managed SKU;
		// unrelated upstream failures remain in SourceErrors, not here.
		if !c.conflicts[model] {
			c.reject(model, problem)
		}
	}
	for model, quote := range supplement.Quotes {
		if priceSyncV2DeepSeekAmbiguousAlias(model) {
			continue
		}
		owner, _ := priceSyncV2Ownership(quote.Provider, model)
		if model != quote.Model || model != normalizeModelPricingSyncModelName(model) || model == "" || owner == "" || quote.SourceURL == "" || quote.Evidence == "" || quote.VerifiedAt.IsZero() ||
			(quote.Currency != "CNY" && quote.Currency != "USD") || (c.result.NativeModels[model] && quote.Currency != "CNY") ||
			priceSyncV2ValidateQuote(quote) != nil {
			if _, ok := c.result.Quotes[model]; !ok {
				c.reject(model, "official supplement did not provide a complete exact-SKU tariff")
			}
			continue
		}
		if quote.Currency == "CNY" {
			c.result.NativeModels[model] = true
		}
		if c.conflicts[model] {
			continue
		}
		if _, documentedGap := supplement.Problems[model]; documentedGap {
			continue
		}
		// Supplements encode authoritative first-party lists, not a second copy
		// of a registry row. They replace only a whole tariff for the same SKU.
		if priorOwner, _, _ := strings.Cut(c.claims[model], "/"); priorOwner != "" && priorOwner != owner {
			c.reject(model, "official quote conflicts with existing first-party ownership")
			continue
		}
		c.result.Quotes[model] = quote
		delete(c.result.Problems, model)
	}
}
