package service

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// TariffPriceIssue means a managed quote cannot determine the request price.
// Do not turn missing evidence into a free request or a guessed peak price.
type TariffPriceIssue struct{ Model, Reason string }

func isTariffPriceIssue(err error) bool { var issue *TariffPriceIssue; return errors.As(err, &issue) }

func (e *TariffPriceIssue) Error() string { return fmt.Sprintf("tariff for %s: %s", e.Model, e.Reason) }

// Managed rates are absolute native site units, not ratios. This snapshot is
// immutable after catalog reload. Parsing clocks/zones is not a billing operation.
type priceSyncTariff struct {
	model    string
	base     map[string]float64
	priority bool
	tiers    []PriceSyncTier
	windows  []tariffWindow
}

type tariffWindow struct {
	rule       PriceSyncWindow
	zone       *time.Location
	start, end int   // minutes since local midnight; end can be 24:00
	days       uint8 // bit 0 = Sunday
}

// Legacy rows, and flat non-token rows handled by the unit resolver, are not
// managed token tariffs. The cheap first check avoids another JSON decode on
// ordinary LiteLLM entries. Unknown context/time cross-products must not be
// silently skipped as if that SKU never had a price.
func declaresTokenTariff(raw json.RawMessage) bool {
	if !bytes.Contains(raw, []byte(`"glory_price_`)) {
		return false
	}
	var h struct {
		Tiers   json.RawMessage `json:"glory_price_tiers"`
		Windows json.RawMessage `json:"glory_price_windows"`
		Source  json.RawMessage `json:"glory_price_source"`
		Mode    string          `json:"glory_billing_mode"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return false
	}
	return len(h.Tiers) > 0 || len(h.Windows) > 0 || (len(h.Source) > 0 && !bytes.Equal(h.Source, []byte("null")) && (h.Mode == "token" || h.Mode == ""))
}

// Decode null distinctly from zero. encoding/json otherwise accepts a JSON null
// into map[string]float64 and silently gives that component a free price.
type tariffRates map[string]float64

// Optional controls default only when absent. An explicit null is unknown.
type tariffBool bool

func (b *tariffBool) UnmarshalJSON(raw []byte) error {
	var v *bool
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return fmt.Errorf("price schedule boolean must be true or false")
	}
	*b = tariffBool(*v)
	return nil
}

type tariffWeekdays []int

func (d *tariffWeekdays) UnmarshalJSON(raw []byte) error {
	var days []*int
	if err := json.Unmarshal(raw, &days); err != nil || len(days) == 0 {
		return fmt.Errorf("price window weekdays must be a non-empty list when supplied")
	}
	for _, day := range days {
		if day == nil {
			return fmt.Errorf("null price window weekday")
		}
		*d = append(*d, *day)
	}
	return nil
}

func (r *tariffRates) UnmarshalJSON(data []byte) error {
	var values map[string]*float64
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*r = make(tariffRates, len(values))
	for key, v := range values {
		if v == nil || *v < 0 {
			return fmt.Errorf("missing or negative %s", key)
		}
		if !tariffTokenField(key) {
			return fmt.Errorf("unsupported scheduled price %s", key)
		}
		(*r)[key] = *v
	}
	return nil
}

func tariffTokenField(key string) bool {
	switch key {
	case "input_cost_per_token", "output_cost_per_token", "cache_read_input_token_cost", "cache_creation_input_token_cost", "cache_creation_input_token_cost_above_1hr", "cache_creation_input_token_cost_30m", "cache_creation_input_token_cost_12h",
		"input_cost_per_token_priority", "output_cost_per_token_priority", "cache_read_input_token_cost_priority", "cache_creation_input_token_cost_priority",
		"input_cost_per_image_token", "output_cost_per_image_token", "cache_read_input_image_token_cost":
		return true
	}
	return false
}

func (p *LiteLLMModelPricing) tariffMatches(model string) bool {
	return p == nil || p.tariff == nil || p.tariff.model == strings.TrimSpace(model)
}

func (t *priceSyncTariff) has(k string) bool {
	if t == nil {
		return false
	}
	_, ok := t.base[k]
	return ok
}
func (t *priceSyncTariff) hasPriority() bool { return t != nil && t.priority }

func (t *priceSyncTariff) validateBand(rates map[string]float64) error {
	for k := range t.base {
		if _, ok := rates[k]; !ok {
			return &TariffPriceIssue{t.model, "missing scheduled component " + k}
		}
	}
	for k := range rates {
		if !t.has(k) {
			return &TariffPriceIssue{t.model, "base quote missing component " + k}
		}
	}
	return nil
}

func newPriceSyncTariff(model string, entry LiteLLMRawEntry, raw json.RawMessage) (*priceSyncTariff, error) {
	t := &priceSyncTariff{model: model, base: map[string]float64{}}
	if len(entry.PriceTiers) > 0 && len(entry.PriceWindows) > 0 {
		return nil, &TariffPriceIssue{model, "cannot combine context and time prices without a joint quote"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for k, rawRate := range fields {
		if !tariffTokenField(k) {
			continue
		}
		var v *float64
		if err := json.Unmarshal(rawRate, &v); err != nil || v == nil || *v < 0 {
			return nil, &TariffPriceIssue{model, "missing or invalid " + k}
		}
		t.base[k] = *v
		t.priority = t.priority || strings.HasSuffix(k, "_priority")
	}
	if len(entry.PriceTiers) != 0 || len(entry.PriceWindows) != 0 {
		for _, k := range []string{"input_cost_per_token", "output_cost_per_token"} {
			if !t.has(k) {
				return nil, &TariffPriceIssue{model, "scheduled token price requires " + k}
			}
		}
	}
	decode := func(raw json.RawMessage, dst any) error {
		if len(raw) == 0 {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return &TariffPriceIssue{model, "null price schedule"}
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(dst); err != nil {
			return &TariffPriceIssue{model, err.Error()}
		}
		return nil
	}
	var tiers []struct {
		PriceSyncTier
		CheckedRates tariffRates `json:"rates"`
		Boundary     tariffBool  `json:"inclusive"`
	}
	if err := decode(entry.PriceTiers, &tiers); err != nil {
		return nil, err
	}
	// Equal effective boundaries cannot be disambiguated (e.g. >9 and >=10).
	for _, row := range tiers {
		b := row.PriceSyncTier
		b.Inclusive = bool(row.Boundary)
		b.Rates = row.CheckedRates
		if b.Threshold <= 0 || b.Threshold == math.MaxInt {
			return nil, &TariffPriceIssue{model, "invalid context threshold"}
		}
		if err := t.validateBand(b.Rates); err != nil {
			return nil, err
		}
		t.tiers = append(t.tiers, b)
	}
	slices.SortFunc(t.tiers, func(a, b PriceSyncTier) int { return cmp.Compare(a.Threshold, b.Threshold) })
	for i := 1; i < len(t.tiers); i++ {
		if t.tiers[i-1].Threshold == t.tiers[i].Threshold || tariffUpperBound(t.tiers[i-1]) >= tariffUpperBound(t.tiers[i]) {
			return nil, &TariffPriceIssue{model, "duplicate or ambiguous context threshold"}
		}
	}
	var windows []struct {
		PriceSyncWindow
		Days         tariffWeekdays `json:"weekdays"`
		Exclude      tariffBool     `json:"exclude_cn_holidays"`
		CheckedRates tariffRates    `json:"rates"`
	}
	if err := decode(entry.PriceWindows, &windows); err != nil {
		return nil, err
	}
	for _, row := range windows {
		w := row.PriceSyncWindow
		w.Rates = row.CheckedRates
		w.Weekdays, w.ExcludeCNHolidays = row.Days, bool(row.Exclude)
		if err := t.validateBand(w.Rates); err != nil {
			return nil, err
		}
		zone, err := loadChannelTimePricingLocation(w.Timezone)
		if err != nil {
			return nil, &TariffPriceIssue{model, "invalid price window timezone: " + err.Error()}
		}
		start, a := tariffMinute(w.Start, false)
		end, b := tariffMinute(w.End, true)
		if !a || !b || start == end {
			return nil, &TariffPriceIssue{model, "invalid price window bounds " + w.Start + "-" + w.End}
		}
		win := tariffWindow{rule: w, zone: zone, start: start, end: end}
		for _, d := range w.Weekdays {
			if d < 0 || d > 6 || win.days&(1<<d) != 0 {
				return nil, &TariffPriceIssue{model, "invalid/duplicate price window weekday"}
			}
			win.days |= 1 << d
		}
		t.windows = append(t.windows, win)
	}
	return t, nil
}

func tariffUpperBound(b PriceSyncTier) int {
	if b.Inclusive {
		return b.Threshold - 1
	}
	return b.Threshold
}

// The override file can also contain a legacy flat price patch. Carry only
// its explicit fields into inherited bands (including zero/null). The bands
// delivered by an entire managed quote are already complete; never flatten them.
func propagateTariffFlatPatch(merged, patch map[string]any) {
	for _, key := range []string{"glory_price_tiers", "glory_price_windows"} {
		if _, supplied := patch[key]; supplied {
			continue
		}
		if patch["glory_price_source"] != nil {
			delete(merged, key)
			continue
		}
		rows, _ := merged[key].([]any)
		for _, row := range rows {
			object, _ := row.(map[string]any)
			rates, _ := object["rates"].(map[string]any)
			if rates == nil {
				continue
			}
			for field, v := range patch {
				if !tariffTokenField(field) {
					continue
				}
				if v == nil {
					delete(rates, field)
				} else {
					rates[field] = v
				}
			}
		}
	}
}

// Standard quote components are authoritative. Operator service-class policy
// can multiply the selected band; it must not replace it with stale base-class
// absolute prices or synthesize unquoted cache lifetimes.
func managedTariffServicePolicy(model string, p *ModelPricing, at time.Time) *ModelPricing {
	n := normalizeKnownOpenAICodexModel(model)
	clone := *p
	if clone.FastMultiplier == nil && !p.tariff.hasPriority() {
		ratio := openAIModelFastPricingRatio(n)
		if ratio == 0 && claude.IsOpus55(model) {
			ratio = 2
		}
		if ratio > 0 {
			clone.FastMultiplier = &ratio
		}
	}
	if openAIModelSupportsUltrafast(n) {
		clone.UltrafastMultiplier = openAIUltrafastMultiplier(n, at)
	}
	return &clone
}

func tariffMinute(s string, end bool) (int, bool) {
	if end && s == "24:00" {
		return 24 * 60, true
	}
	if len(s) != len("15:04") {
		return 0, false
	}
	clk, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return clk.Hour()*60 + clk.Minute(), true
}

func (w tariffWindow) contains(local time.Time) bool {
	minute := local.Hour()*60 + local.Minute()
	weekday := local.Weekday()
	if w.start > w.end { // after midnight, eligibility is the previous start weekday
		if minute < w.end {
			weekday = (weekday + 6) % 7
		} else if minute < w.start {
			return false
		}
	} else if minute < w.start || minute >= w.end {
		return false
	}
	return w.days == 0 || w.days&(1<<uint(weekday)) != 0
}

func (t *priceSyncTariff) windowAt(at time.Time) (map[string]float64, error) {
	if at.IsZero() {
		at = timezone.Now()
	}
	var rates map[string]float64
	for _, w := range t.windows {
		local := at.In(w.zone)
		if !w.contains(local) {
			continue
		}
		if w.rule.ExcludeCNHolidays {
			holiday, known := chinaTariffHoliday(local)
			if !known {
				return nil, &TariffPriceIssue{t.model, "missing CN holiday calendar for " + local.Format("2006-01-02") + "; refresh the official calendar"}
			}
			if holiday {
				continue
			}
		}
		if rates != nil {
			return nil, &TariffPriceIssue{t.model, "overlapping price windows at " + local.Format(time.RFC3339)}
		}
		rates = w.rule.Rates
	}
	return rates, nil
}

func (t *priceSyncTariff) tierAt(n int) *PriceSyncTier {
	var selected *PriceSyncTier
	for i := range t.tiers {
		b := &t.tiers[i]
		if (n > b.Threshold || n == b.Threshold && b.Inclusive) && (selected == nil || b.Threshold > selected.Threshold) {
			selected = b
		}
	}
	return selected
}

// A missing component is not a quoted zero. In particular a 30m write is not
// the ordinary or the 1h write rate. Legacy rows and commercial cards retain
// their historical fallback behavior. An unclassified aggregate may use an
// explicitly quoted ordinary price, but not an invented split of exact TTLs.
func (t *priceSyncTariff) requireUsage(u UsageTokens) error {
	missing := func(k string) error { return &TariffPriceIssue{t.model, "usage requires an unquoted component: " + k} }
	if u.CacheReadTokens > 0 && !t.has("cache_read_input_token_cost") {
		return missing("cache_read_input_token_cost")
	}
	if u.CacheCreation30mTokens > 0 || u.CacheCreation12hTokens > 0 {
		if u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 {
			return &TariffPriceIssue{t.model, "conflicting cache write TTL families"}
		}
		if u.CacheCreationTokens > 0 && u.CacheCreation30mTokens >= 0 && u.CacheCreation30mTokens <= u.CacheCreationTokens && u.CacheCreation12hTokens == u.CacheCreationTokens-u.CacheCreation30mTokens {
			if u.CacheCreation30mTokens > 0 && !t.has("cache_creation_input_token_cost_30m") {
				return missing("cache_creation_input_token_cost_30m")
			}
			if u.CacheCreation12hTokens > 0 && !t.has("cache_creation_input_token_cost_12h") {
				return missing("cache_creation_input_token_cost_12h")
			}
			return nil
		}
		if u.CacheCreationTokens <= 0 {
			return &TariffPriceIssue{t.model, "TTL detail lacks aggregate cache write usage"}
		}
	}
	if u.CacheCreation1hTokens > 0 && !t.has("cache_creation_input_token_cost_above_1hr") {
		return missing("cache_creation_input_token_cost_above_1hr")
	}
	if (u.CacheCreationTokens > u.CacheCreation1hTokens || u.CacheCreation5mTokens > 0) && !t.has("cache_creation_input_token_cost") {
		return missing("cache_creation_input_token_cost")
	}
	return nil
}

func (s *BillingService) computeTariffCost(base *ModelPricing, input CostInput, longCtx, catalog bool) (*CostBreakdown, error) {
	if base != nil && base.legacyQuarantineReason != "" {
		return s.computeQuarantinedTokenCost(base, input, catalog)
	}
	if base.tariff == nil {
		return s.computeTokenBreakdown(base, input.Tokens, input.RateMultiplier, input.ServiceTier, longCtx), nil
	}
	// A configured selling card has precedence over a global absolute schedule.
	// Never overlay a catalog band onto operator prices, or reapply the legacy ladder.
	if !catalog {
		return s.computeTokenBreakdown(base, input.Tokens, input.RateMultiplier, input.ServiceTier, false), nil
	}
	if err := base.tariff.requireUsage(input.Tokens); err != nil {
		return nil, err
	}
	pricing := *base
	var rates map[string]float64
	var tier *PriceSyncTier
	if longCtx {
		n := input.Tokens.InputTokens + input.Tokens.CacheReadTokens + input.Tokens.CacheCreationTokens
		tier = base.tariff.tierAt(n)
		if tier != nil {
			rates = tier.Rates
		}
	}
	// The subscription's own peak policy is already included in RateMultiplier.
	if len(base.tariff.windows) > 0 && !groupOwnsTariffWindow(input.Group) {
		var err error
		rates, err = base.tariff.windowAt(input.PricingAt)
		if err != nil {
			return nil, err
		}
	}
	applyTariffRates(&pricing, rates)
	cost := s.computeTokenBreakdown(&pricing, input.Tokens, input.RateMultiplier, input.ServiceTier, false)
	if tier != nil {
		normal := s.computeTokenBreakdown(base, input.Tokens, input.RateMultiplier, input.ServiceTier, false)
		cost.LongContextBillingApplied = cost.ActualCost > normal.ActualCost
	}
	return cost, nil
}

func (s *BillingService) computeQuarantinedTokenCost(base *ModelPricing, input CostInput, catalog bool) (*CostBreakdown, error) {
	issue := func(component string) (*CostBreakdown, error) {
		model := base.legacyQuarantineModel
		if model == "" {
			model = input.Model
		}
		reason := base.legacyQuarantineReason
		if component != "" {
			reason += "; usage requires an unquoted component: " + component
		}
		return nil, &TariffPriceIssue{model, reason}
	}
	rates := base.commercialRates
	if catalog || rates == 0 {
		return issue("")
	}
	u := input.Tokens
	imageIn := min(max(u.ImageInputTokens, 0), max(u.InputTokens, 0))
	imageCache := min(max(u.ImageCacheReadTokens, 0), max(u.CacheReadTokens, 0))
	if u.InputTokens > imageIn && rates&commercialInput == 0 {
		return issue("input_cost_per_token")
	}
	if u.OutputTokens > max(u.ImageOutputTokens, 0) && rates&commercialOutput == 0 {
		return issue("output_cost_per_token")
	}
	if imageIn > 0 && rates&commercialImageInput == 0 && (rates&commercialInput == 0 || base.ImageInputPricePerToken != 0 || base.tariff.has("input_cost_per_image_token")) {
		return issue("input_cost_per_image_token")
	}
	if u.ImageOutputTokens > 0 && rates&commercialImageOutput == 0 && (rates&commercialOutput == 0 || base.ImageOutputPriceExplicit || base.ImageOutputPricePerToken != 0) {
		return issue("output_cost_per_image_token")
	}
	if imageCache > 0 && (base.ImageCacheReadPricePerToken > 0 || base.tariff.has("cache_read_input_image_token_cost")) {
		return issue("cache_read_input_image_token_cost")
	}
	if u.CacheReadTokens > 0 && rates&commercialCacheRead == 0 {
		return issue("cache_read_input_token_cost")
	}

	pricing := *base
	if u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 {
		if u.CacheCreation30mTokens > 0 || u.CacheCreation12hTokens > 0 {
			return issue("conflicting cache write TTL families")
		}
		pricing.SupportsCacheBreakdown = true
	}
	if hasCompleteOpenAICacheWriteSplit(u) {
		if u.CacheCreation30mTokens > 0 && rates&commercialCacheWrite == 0 {
			return issue("cache_creation_input_token_cost_30m")
		}
		if u.CacheCreation12hTokens > 0 && rates&commercialCacheWrite12h == 0 {
			return issue("cache_creation_input_token_cost_12h")
		}
	} else {
		if u.CacheCreationTokens <= 0 && (u.CacheCreation30mTokens > 0 || u.CacheCreation12hTokens > 0) {
			return issue("cache write TTL detail lacks aggregate usage")
		}
		ordinary := u.CacheCreationTokens
		if pricing.usesCacheCreationBreakdown() {
			var oneHour int
			ordinary, oneHour = allocateCacheCreationBillingTokens(u)
			if oneHour > 0 && rates&commercialCacheWrite1h == 0 {
				return issue("cache_creation_input_token_cost_above_1hr")
			}
		} else if u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 {
			return issue("cache_creation_input_token_cost")
		}
		if ordinary > 0 && rates&commercialCacheWrite == 0 {
			return issue("cache_creation_input_token_cost")
		}
	}

	// Keep the selected commercial interval, not ratios or bands from the
	// quarantined catalog. Service-tier policy still applies to selling rates.
	pricing.tariff = nil
	pricing.InputPricePerTokenPriority = 0
	pricing.OutputPricePerTokenPriority = 0
	pricing.CacheCreationPricePerTokenPriority = 0
	pricing.CacheReadPricePerTokenPriority = 0
	priced := managedTariffServicePolicy(input.Model, &pricing, input.PricingAt)
	return s.computeTokenBreakdown(priced, u, input.RateMultiplier, input.ServiceTier, false), nil
}

func applyTariffRates(p *ModelPricing, rates map[string]float64) {
	for key, value := range rates {
		switch key {
		case "input_cost_per_token":
			p.InputPricePerToken = value
		case "output_cost_per_token":
			p.OutputPricePerToken = value
		case "cache_read_input_token_cost":
			p.CacheReadPricePerToken = value
		case "cache_creation_input_token_cost":
			p.CacheCreationPricePerToken, p.CacheCreation5mPrice, p.CacheCreationPriceExplicit = value, value, true
		case "cache_creation_input_token_cost_above_1hr":
			p.CacheCreation1hPrice, p.SupportsCacheBreakdown = value, true
		case "cache_creation_input_token_cost_30m":
			p.CacheCreation30mPricePerToken = &value
		case "cache_creation_input_token_cost_12h":
			p.CacheCreation12hPricePerToken = &value
		case "input_cost_per_token_priority":
			p.InputPricePerTokenPriority = value
		case "output_cost_per_token_priority":
			p.OutputPricePerTokenPriority = value
		case "cache_creation_input_token_cost_priority":
			p.CacheCreationPricePerTokenPriority = value
		case "cache_read_input_token_cost_priority":
			p.CacheReadPricePerTokenPriority = value
		case "input_cost_per_image_token":
			p.ImageInputPricePerToken = value
		case "cache_read_input_image_token_cost":
			p.ImageCacheReadPricePerToken = value
		case "output_cost_per_image_token":
			p.ImageOutputPricePerToken, p.ImageOutputPriceExplicit = value, true
		}
	}
}
