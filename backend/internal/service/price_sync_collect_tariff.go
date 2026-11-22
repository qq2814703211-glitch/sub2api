package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

var priceSyncV2TokenFields = map[string]string{
	"input": "input_cost_per_token", "output": "output_cost_per_token",
	"cache_read": "cache_read_input_token_cost", "cache_write": "cache_creation_input_token_cost",
	"cache_write_5m": "cache_creation_input_token_cost", "cache_write_1h": "cache_creation_input_token_cost_above_1hr",
	"cache_write_30m": "cache_creation_input_token_cost_30m", "cache_write_12h": "cache_creation_input_token_cost_12h",
}

var priceSyncV2FixedFields = map[string]string{
	"per_request": "cost_per_request", "per_second": "cost_per_second", "per_image": "output_cost_per_image",
}

func priceSyncV2DecodeTariff(quote *PriceSyncQuote, cost map[string]json.RawMessage) error {
	var declared string
	if raw, ok := cost["currency"]; ok {
		if err := json.Unmarshal(raw, &declared); err != nil || declared == "" {
			return fmt.Errorf("invalid model tariff currency")
		}
	}
	if quote.Currency == "CNY" {
		var currencies map[string]json.RawMessage
		if raw, ok := cost["currency_options"]; ok {
			if err := json.Unmarshal(raw, &currencies); err != nil || currencies == nil {
				return fmt.Errorf("invalid model currency options")
			}
		}
		if raw, ok := currencies["CNY"]; ok {
			if declared == "CNY" {
				return fmt.Errorf("ambiguous duplicate CNY tariff branches")
			}
			var branch map[string]json.RawMessage
			if err := json.Unmarshal(raw, &branch); err != nil || branch == nil {
				return fmt.Errorf("malformed model currency_options.CNY tariff")
			}
			if raw, ok := branch["currency"]; ok {
				if err := json.Unmarshal(raw, &declared); err != nil || declared != "CNY" {
					return fmt.Errorf("contradictory currency in CNY tariff")
				}
			}
			// A USD schedule or tier outside the branch is not evidence of the
			// CNY amount (or that the domestic tariff is flat).
			for _, key := range []string{"tiers", "context_over_200k", "schedule"} {
				if _, outside := cost[key]; outside && branch[key] == nil && (key != "context_over_200k" || branch["tiers"] == nil) {
					return fmt.Errorf("CNY tariff missing %s; cannot inherit another currency", key)
				}
			}
			cost = branch
		} else if declared != "CNY" {
			return fmt.Errorf("reference only; missing model-level CNY tariff")
		}
	} else if declared != "" && declared != "USD" {
		return fmt.Errorf("reference only; non-USD tariff in USD registry")
	}

	quote.BillingMode = "token"
	for key := range priceSyncV2FixedFields {
		if _, ok := cost[key]; ok {
			if quote.BillingMode != "token" {
				return fmt.Errorf("conflicting billing units")
			}
			quote.BillingMode = key
		}
	}
	base, err := priceSyncV2ReadComponents(cost, quote.BillingMode, nil, "base")
	if err != nil {
		return err
	}
	quote.Rates = base
	if raw, ok := cost["tiers"]; ok {
		var tiers []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tiers); err != nil || tiers == nil || quote.BillingMode != "token" {
			return fmt.Errorf("malformed context tiers")
		}
		seen := map[int]bool{}
		for _, row := range tiers {
			var tier struct {
				Size int    `json:"size"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(row["tier"], &tier); err != nil || tier.Type != "context" || tier.Size <= 0 || seen[tier.Size] {
				return fmt.Errorf("unknown or duplicate context tier")
			}
			seen[tier.Size] = true
			rates, err := priceSyncV2ReadComponents(row, quote.BillingMode, base, "tier")
			if err != nil {
				return fmt.Errorf("context tier >%d: %w", tier.Size, err)
			}
			quote.Tiers = append(quote.Tiers, PriceSyncTier{Threshold: tier.Size, Rates: rates})
		}
		slices.SortFunc(quote.Tiers, func(a, b PriceSyncTier) int { return a.Threshold - b.Threshold })
	}
	if raw, ok := cost["context_over_200k"]; ok {
		// In the real feed this compatibility field can duplicate the first
		// exact tier even when it is not at 200k (e.g. 272k).
		var legacy map[string]json.RawMessage
		if err := json.Unmarshal(raw, &legacy); err != nil || legacy == nil || quote.BillingMode != "token" {
			return fmt.Errorf("malformed legacy context band")
		}
		rates, err := priceSyncV2ReadComponents(legacy, quote.BillingMode, base, "tier")
		if err != nil {
			return fmt.Errorf("legacy context band: %w", err)
		}
		if len(quote.Tiers) == 0 {
			quote.Tiers = []PriceSyncTier{{Threshold: 200000, Rates: rates}}
		} else if !maps.Equal(quote.Tiers[0].Rates, rates) {
			return fmt.Errorf("legacy context band disagrees with explicit tiers")
		}
	}
	if raw, ok := cost["schedule"]; ok {
		if len(quote.Tiers) != 0 {
			return fmt.Errorf("context and time cross-product is not representable")
		}
		quote.Windows, err = priceSyncV2DecodeWindows(raw, quote.BillingMode, base)
		if err != nil {
			return err
		}
	}
	return priceSyncV2ValidateQuote(*quote)
}

func priceSyncV2ReadComponents(row map[string]json.RawMessage, mode string, base map[string]float64, scope string) (map[string]float64, error) {
	if raw, exists := row["unit"]; exists {
		var unit string
		if err := json.Unmarshal(raw, &unit); err != nil || (mode == "token" && unit != "per_million_tokens") || (mode != "token" && unit != mode) {
			return nil, fmt.Errorf("unsupported or contradictory tariff unit")
		}
	}
	rates := map[string]float64{}
	for _, key := range sortedStrings(row) {
		raw := row[key]
		field, token := priceSyncV2TokenFields[key]
		if fixed, ok := priceSyncV2FixedFields[key]; ok {
			if mode != key {
				return nil, fmt.Errorf("conflicting %s billing component", key)
			}
			field = fixed
		}
		if field == "" {
			switch key {
			case "unit":
				continue
			case "currency", "currency_options", "tiers", "context_over_200k", "schedule":
				if scope == "base" {
					continue
				}
			case "tier":
				if scope == "tier" {
					continue
				}
			case "name", "weekdays", "hours", "exclude_cn_holidays":
				if scope == "window" {
					continue
				}
			case "reasoning", "input_audio", "output_audio":
				continue // checked against the corresponding billable rate below
			}
			return nil, fmt.Errorf("reference only; unsupported tariff component %s", key)
		}
		if token && mode != "token" {
			return nil, fmt.Errorf("mixed token and fixed billing units")
		}
		if _, duplicate := rates[field]; duplicate {
			return nil, fmt.Errorf("duplicate definition of %s", field)
		}
		value, err := priceSyncV2ReadAmount(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if token {
			value /= 1e6
		}
		rates[field] = value
	}
	for _, component := range [][2]string{{"reasoning", "output"}, {"input_audio", "input"}, {"output_audio", "output"}} {
		key, target := component[0], component[1]
		if raw, ok := row[key]; ok {
			amount, err := priceSyncV2ReadAmount(raw)
			baseAmount, exists := rates[priceSyncV2TokenFields[target]]
			if !exists && scope == "window" {
				baseAmount, exists = base[priceSyncV2TokenFields[target]]
			}
			if err != nil || !exists || baseAmount != amount/1e6 {
				return nil, fmt.Errorf("reference only; distinct or unknown %s pricing", key)
			}
		}
	}
	for k, v := range base {
		if _, ok := rates[k]; !ok {
			if scope == "window" {
				// basellm windows explicitly inherit components from their own
				// currency branch; the schema does not promise this for tiers.
				rates[k] = v
			} else {
				return nil, fmt.Errorf("missing component %s", k)
			}
		}
	}
	return rates, priceSyncV2CheckComponents(mode, rates)
}

func priceSyncV2ReadAmount(raw json.RawMessage) (float64, error) {
	var price *float64
	if err := json.Unmarshal(raw, &price); err != nil || price == nil || *price < 0 || math.IsNaN(*price) || math.IsInf(*price, 0) {
		return 0, fmt.Errorf("expected finite non-negative amount")
	}
	return *price, nil
}

func priceSyncV2CheckComponents(mode string, rates map[string]float64) error {
	if mode == "token" {
		for _, k := range []string{priceSyncV2TokenFields["input"], priceSyncV2TokenFields["output"]} {
			if _, ok := rates[k]; !ok {
				return fmt.Errorf("missing component %s", k)
			}
		}
		for _, field := range priceSyncV2FixedFields {
			if _, ok := rates[field]; ok {
				return fmt.Errorf("mixed token and fixed billing units")
			}
		}
	} else if field := priceSyncV2FixedFields[mode]; field == "" || len(rates) != 1 {
		return fmt.Errorf("unknown or mixed billing mode")
	} else if _, ok := rates[field]; !ok {
		return fmt.Errorf("missing component %s", field)
	}
	for _, v := range rates {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("invalid price amount")
		}
	}
	return nil
}

func priceSyncV2ValidateQuote(quote PriceSyncQuote) error {
	validate := func(rates map[string]float64) error {
		if err := priceSyncV2CheckComponents(quote.BillingMode, rates); err != nil {
			return err
		}
		for field := range quote.Rates {
			if _, ok := rates[field]; !ok {
				return fmt.Errorf("missing component %s", field)
			}
		}
		for field := range rates {
			if _, ok := quote.Rates[field]; !ok {
				return fmt.Errorf("base tariff missing component %s", field)
			}
		}
		return nil
	}
	if err := validate(quote.Rates); err != nil {
		return err
	}
	if len(quote.Tiers) != 0 && len(quote.Windows) != 0 {
		return fmt.Errorf("context and time cross-product is not representable")
	}
	last := 0
	for _, tier := range quote.Tiers {
		if quote.BillingMode != "token" || tier.Threshold <= last {
			return fmt.Errorf("invalid or unordered context thresholds")
		}
		if err := validate(tier.Rates); err != nil {
			return err
		}
		last = tier.Threshold
	}
	for _, window := range quote.Windows {
		if _, err := time.LoadLocation(window.Timezone); err != nil || window.Timezone == "" || window.Timezone == "Local" ||
			!priceSyncV2Clock(window.Start, false) || !priceSyncV2Clock(window.End, true) || window.Start == window.End {
			return fmt.Errorf("invalid price window")
		}
		for _, day := range window.Weekdays {
			if day < 0 || day > 6 {
				return fmt.Errorf("invalid price window weekday")
			}
		}
		if err := validate(window.Rates); err != nil {
			return err
		}
	}
	return nil
}

func priceSyncV2DecodeWindows(raw json.RawMessage, mode string, base map[string]float64) ([]PriceSyncWindow, error) {
	var schedule struct {
		Timezone string                       `json:"timezone"`
		Fallback string                       `json:"fallback"`
		Windows  []map[string]json.RawMessage `json:"windows"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil || schedule.Fallback == "" || schedule.Timezone == "" || schedule.Timezone == "Local" || len(schedule.Windows) == 0 {
		return nil, fmt.Errorf("invalid price schedule")
	}
	if _, err := time.LoadLocation(schedule.Timezone); err != nil {
		return nil, fmt.Errorf("invalid price schedule timezone")
	}
	var windows []PriceSyncWindow
	for _, row := range schedule.Windows {
		var hours []string
		var days []int
		var holidays bool
		if err := json.Unmarshal(row["hours"], &hours); err != nil || len(hours) == 0 {
			return nil, fmt.Errorf("price window has no valid bounds")
		}
		if raw, exists := row["weekdays"]; exists {
			if err := json.Unmarshal(raw, &days); err != nil || len(days) == 0 {
				return nil, fmt.Errorf("invalid price window weekdays")
			}
		}
		seenDays := map[int]bool{}
		for _, day := range days {
			if day < 0 || day > 6 || seenDays[day] {
				return nil, fmt.Errorf("invalid price window weekday")
			}
			seenDays[day] = true
		}
		if raw, exists := row["exclude_cn_holidays"]; exists {
			if err := json.Unmarshal(raw, &holidays); err != nil {
				return nil, fmt.Errorf("invalid holiday policy")
			}
		}
		rates, err := priceSyncV2ReadComponents(row, mode, base, "window")
		if err != nil {
			return nil, err
		}
		for _, hour := range hours {
			start, end, ok := strings.Cut(hour, "-")
			if !ok || !priceSyncV2Clock(start, false) || !priceSyncV2Clock(end, true) || start == end {
				return nil, fmt.Errorf("invalid price window interval")
			}
			windows = append(windows, PriceSyncWindow{Timezone: schedule.Timezone, Weekdays: days, Start: start, End: end, ExcludeCNHolidays: holidays, Rates: rates})
		}
	}
	return windows, nil
}

func priceSyncV2Clock(s string, allowEndOfDay bool) bool {
	if allowEndOfDay && s == "24:00" {
		return true
	}
	if len(s) != 5 {
		return false
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}
