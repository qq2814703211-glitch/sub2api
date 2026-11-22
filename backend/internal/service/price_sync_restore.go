package service

import (
	"encoding/json"
	"maps"
	"slices"
)

// Compare amounts exactly, including field presence. Source evidence and
// verification times belong to the restore gate, not the billing tariff.
func samePriceSyncTariff(a, b *PriceSyncQuote) bool {
	return a != nil && b != nil && a.Model == b.Model && a.BillingMode == b.BillingMode && a.Currency == b.Currency &&
		maps.Equal(a.Rates, b.Rates) && slices.EqualFunc(a.Tiers, b.Tiers, func(a, b PriceSyncTier) bool {
		return a.Threshold == b.Threshold && a.Inclusive == b.Inclusive && maps.Equal(a.Rates, b.Rates)
	}) && slices.EqualFunc(a.Windows, b.Windows, func(a, b PriceSyncWindow) bool {
		return a.Timezone == b.Timezone && a.Start == b.Start && a.End == b.End &&
			a.ExcludeCNHolidays == b.ExcludeCNHolidays && slices.Equal(a.Weekdays, b.Weekdays) && maps.Equal(a.Rates, b.Rates)
	})
}

func equivalentPriceSyncRestore(m *priceSyncModel, entry json.RawMessage, manual CNYOfficialManualPrice) (*PriceSyncQuote, bool) {
	auto, prior := m.Auto, *m.Effective
	if prior.Currency == "SITE" && prior.Provider == "operator" && prior.SourceURL == "settings:pricing.cny_official" {
		prior.Currency = "USD"
		if m.NativeCNY || isDeepSeekModel(prior.Model) {
			prior.Currency = "CNY"
		}
	}
	if auto.Currency != prior.Currency || auto.BillingMode != prior.BillingMode {
		return nil, false
	}
	bridged := priceSyncOfficialTTLNotApplicable(auto, m.Effective, officialCacheWrite)
	legacy := bridged && !declaresTokenTariff(entry)
	if legacy {
		if !matchesLegacyOpenAIRestoreEntry(entry, m.Effective, manual) {
			return nil, false
		}
	} else {
		var configured *CNYOfficialManualPrice
		if m.Effective.Provider == "operator" {
			configured = &manual
		}
		expected, err := priceSyncRender(entry, m.Effective, configured, m.NativeCNY || isDeepSeekModel(prior.Model))
		if err != nil || !samePriceSyncRow(entry, expected) {
			return nil, false
		}
	}
	if !bridged {
		if samePriceSyncTariff(auto, &prior) {
			return auto, false
		}
		return nil, false
	}
	if len(prior.Tiers) != 1 {
		return nil, false
	}
	candidate := *auto
	candidate.Tiers = slices.Clone(auto.Tiers)
	prior.Tiers = slices.Clone(prior.Tiers)
	candidate.Rates, prior.Rates = priceSyncNormalizeRestoreTTL(auto.Rates, prior.Rates)
	candidate.Tiers[0].Rates, prior.Tiers[0].Rates = priceSyncNormalizeRestoreTTL(auto.Tiers[0].Rates, prior.Tiers[0].Rates)
	// A manual ladder derives the tier after scaling the base price to a
	// per-token amount. The official tier is stated per million and scaled
	// afterward. Accept the two exact arithmetic results, never an epsilon.
	// The effective projection continues to use the previously billed rate.
	if l := cnyOfficialManualLongContext(manual); l != nil && l.Threshold == prior.Tiers[0].Threshold &&
		prior.Rates[officialOutput] == manual.Output/officialMillion &&
		prior.Tiers[0].Rates[officialOutput] == prior.Rates[officialOutput]*l.OutputMultiplier &&
		auto.Tiers[0].Rates[officialOutput] == manual.Output*l.OutputMultiplier/officialMillion {
		candidate.Tiers[0].Rates[officialOutput] = prior.Tiers[0].Rates[officialOutput]
	}
	if !samePriceSyncTariff(&candidate, &prior) {
		return nil, false
	}
	return &candidate, true
}

// The official contract was checked by priceSyncOfficialTTLNotApplicable.
// Compare explicit pairs, retaining both names in the effective projection.
// A manual rate with both names must independently match both amounts.
func priceSyncNormalizeRestoreTTL(auto, prior map[string]float64) (map[string]float64, map[string]float64) {
	a, p := maps.Clone(auto), maps.Clone(prior)
	for _, pair := range [][2]string{{officialCacheWrite, officialCache30m}, {officialCache1h, officialCache12h}} {
		v, declared := auto[pair[1]]
		old, present := prior[pair[0]]
		if !declared || !present {
			continue
		}
		a[pair[0]] = v
		if _, exists := p[pair[1]]; !exists {
			p[pair[1]] = old
		}
	}
	return a, p
}

// Keep the actual V1 projection byte-for-byte when it is exactly the legacy
// form of the operator card. Replacing it would change the runtime's legacy
// TTL fallback and the order of floating-point operations in tier settlement.
func matchesLegacyOpenAIRestoreEntry(raw json.RawMessage, prior *PriceSyncQuote, manual CNYOfficialManualPrice) bool {
	l := cnyOfficialManualLongContext(manual)
	if l == nil {
		return false
	}
	q, err := priceSyncManualQuote(prior.Model, manual, prior.VerifiedAt)
	if err != nil || !samePriceSyncTariff(&q, prior) {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	expected := map[string]any{
		"cny_official": true, cnyOfficialFieldThreshold: l.Threshold,
		cnyOfficialFieldInputMult: l.InputMultiplier, cnyOfficialFieldOutputMult: l.OutputMultiplier,
	}
	for k, v := range prior.Rates {
		expected[k] = v
	}
	for k := range fields {
		if priceSyncManagedField(k) && expected[k] == nil {
			return false
		}
	}
	for k, want := range expected {
		var got any
		if json.Unmarshal(fields[k], &got) != nil || !priceSyncEqual(got, want) {
			return false
		}
	}
	return true
}

func (m *priceSyncModel) preserveLegacyRestoreEntry(entry json.RawMessage) bool {
	return m.PreserveRestoreProjection && !declaresTokenTariff(entry)
}
