package service

import (
	"encoding/hex"
	"slices"
	"strings"
)

// The official OpenAI adapter cross-checks the exact Standard tariff and the
// exact model page. That contract sells 30m and 12h separately. The legacy
// generic write and 1h columns are not additional rates for these SKUs.
// Keep the exception at the ownership boundary; registry rows and other
// vendors cannot use the absence of a field as proof of inapplicability.
func priceSyncOfficialTTLNotApplicable(q, prior *PriceSyncQuote, field string) bool {
	if field != officialCacheWrite && field != officialCache1h {
		return false
	}
	if !slices.Contains(officialOpenAIModels, q.Model) || prior.Model != q.Model ||
		prior.Provider != "operator" || prior.Currency != "SITE" || prior.SourceURL != "settings:pricing.cny_official" ||
		q.Provider != "openai" || q.Currency != "USD" || q.BillingMode != "token" ||
		q.SourceURL != officialOpenAIURL+"#standard-pricing-data" {
		return false
	}
	// Both hashes are produced by loadOfficialOpenAI after cross-validation.
	evidence := strings.Split(q.Evidence, "; ")
	if len(evidence) != 4 || evidence[0] != "region=OpenAI API Standard" || evidence[2] != "model_rule="+officialOpenAIModelURL(q.Model) {
		return false
	}
	for _, s := range []string{evidence[1], evidence[3]} {
		h, ok := strings.CutPrefix(s, "sha256:")
		if !ok || len(h) != 64 {
			return false
		}
		if _, err := hex.DecodeString(h); err != nil {
			return false
		}
	}
	if len(q.Tiers) != 1 || q.Tiers[0].Threshold != 272000 || q.Tiers[0].Inclusive || len(q.Windows) != 0 || priceSyncV2ValidateQuote(*q) != nil {
		return false
	}
	for _, k := range officialOpenAIFields {
		if _, ok := q.Rates[k]; !ok {
			return false
		}
	}
	for _, k := range []string{officialCacheWrite, officialCache1h} {
		if _, ok := q.Rates[k]; ok {
			return false
		}
	}
	return true
}
