package service

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

// MediaPricing prices successful media outputs in the site's settlement unit.
// Token prices are per token, not per million. No currency conversion is applied.
// A nil configuration leaves the legacy pricing path unchanged.
type MediaPricing struct {
	Rules []MediaPricingRule `json:"rules"`
	// Input allowances and surcharges apply once per successful request.
	ExtraInputImagePrice *float64             `json:"extra_input_image_price,omitempty"`
	FreeInputImages      *int                 `json:"free_input_images,omitempty"`
	BillingDuration      MediaBillingDuration `json:"duration_source,omitempty"`
}

type MediaBillingDuration string

const (
	MediaBillingDurationOutput MediaBillingDuration = "output"
	MediaBillingDurationTotal  MediaBillingDuration = "total"
)

type MediaPricingRule struct {
	Name             string                 `json:"name"`
	Unit             string                 `json:"unit"`
	Conditions       MediaPricingConditions `json:"conditions"`
	Price            *float64               `json:"price,omitempty"`
	InputPrice       *float64               `json:"input_price,omitempty"`
	OutputPrice      *float64               `json:"output_price,omitempty"`
	ImageInputPrice  *float64               `json:"image_input_price,omitempty"`
	ImageOutputPrice *float64               `json:"image_output_price,omitempty"`
	PromptPrice      *float64               `json:"prompt_price,omitempty"`
}

type MediaPricingConditions struct {
	Resolution     string   `json:"resolution,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	InputType      string   `json:"input_type,omitempty"`
	Audio          *bool    `json:"audio,omitempty"`
	ReferenceVideo *bool    `json:"reference_video,omitempty"`
	Layer          *bool    `json:"layer,omitempty"`
	MinPixels      *int64   `json:"min_pixels,omitempty"`
	MaxPixels      *int64   `json:"max_pixels,omitempty"`
	MinDuration    *float64 `json:"min_duration,omitempty"`
	MaxDuration    *float64 `json:"max_duration,omitempty"`
	FPS            *float64 `json:"fps,omitempty"`
	MinFPS         *float64 `json:"min_fps,omitempty"`
	MaxFPS         *float64 `json:"max_fps,omitempty"`
}

// Count is the number of successful outputs, never the requested output count.
// Images, when present, contains one entry per successful output. Scalar zero
// dimensions/duration/FPS mean unknown; booleans distinguish unknown and false.
type MediaUsage struct {
	Resolution      string  `json:"resolution,omitempty"`
	Mode            string  `json:"mode,omitempty"`
	InputType       string  `json:"input_type,omitempty"`
	Audio           *bool   `json:"audio,omitempty"`
	ReferenceVideo  *bool   `json:"reference_video,omitempty"`
	Layer           *bool   `json:"layer,omitempty"`
	Width           int64   `json:"width,omitempty"`
	Height          int64   `json:"height,omitempty"`
	Pixels          int64   `json:"pixels,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	// nil means unknown; explicit zero survives snapshots and authoritative merges.
	InputImageCount      *int         `json:"input_image_count,omitempty"`
	InputDurationSeconds *float64     `json:"input_duration_seconds,omitempty"`
	TotalDurationSeconds *float64     `json:"total_duration_seconds,omitempty"`
	FPS                  float64      `json:"fps,omitempty"`
	Frames               int64        `json:"frames,omitempty"`
	Count                int          `json:"count"`
	Images               []MediaUsage `json:"images,omitempty"`
}

var (
	ErrMediaPricingUnavailable = errors.New("media pricing rule unavailable")
	ErrMediaUsageIncomplete    = errors.New("media usage incomplete")
)

func mediaCopyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Clone isolates all mutable rules, conditions and explicit zero prices.
func (p *MediaPricing) Clone() *MediaPricing {
	if p == nil {
		return nil
	}
	cp := &MediaPricing{
		ExtraInputImagePrice: mediaCopyPtr(p.ExtraInputImagePrice),
		FreeInputImages:      mediaCopyPtr(p.FreeInputImages),
		BillingDuration:      p.BillingDuration,
	}
	if p.Rules != nil {
		cp.Rules = append([]MediaPricingRule{}, p.Rules...)
	}
	for i := range cp.Rules {
		r := &cp.Rules[i]
		r.Price = mediaCopyPtr(r.Price)
		r.InputPrice = mediaCopyPtr(r.InputPrice)
		r.OutputPrice = mediaCopyPtr(r.OutputPrice)
		r.ImageInputPrice = mediaCopyPtr(r.ImageInputPrice)
		r.ImageOutputPrice = mediaCopyPtr(r.ImageOutputPrice)
		r.PromptPrice = mediaCopyPtr(r.PromptPrice)
		c := &r.Conditions
		c.Audio = mediaCopyPtr(c.Audio)
		c.ReferenceVideo = mediaCopyPtr(c.ReferenceVideo)
		c.Layer = mediaCopyPtr(c.Layer)
		c.MinPixels = mediaCopyPtr(c.MinPixels)
		c.MaxPixels = mediaCopyPtr(c.MaxPixels)
		c.MinDuration = mediaCopyPtr(c.MinDuration)
		c.MaxDuration = mediaCopyPtr(c.MaxDuration)
		c.FPS = mediaCopyPtr(c.FPS)
		c.MinFPS = mediaCopyPtr(c.MinFPS)
		c.MaxFPS = mediaCopyPtr(c.MaxFPS)
	}
	return cp
}

func mediaFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ValidateMediaPricing validates configuration, without inventing metadata,
// prices or restricting provider-defined labels. Overlaps are deterministic:
// more specified condition fields win, then the earliest declaration wins.
func ValidateMediaPricing(p *MediaPricing) error {
	if p == nil {
		return nil
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("media_pricing.rules must not be empty; use null to disable")
	}
	if p.ExtraInputImagePrice != nil && (!mediaFinite(*p.ExtraInputImagePrice) || *p.ExtraInputImagePrice < 0) {
		return fmt.Errorf("media_pricing.extra_input_image_price must be finite and >= 0")
	}
	if p.FreeInputImages != nil && *p.FreeInputImages < 0 {
		return fmt.Errorf("media_pricing.free_input_images must be >= 0")
	}
	if p.BillingDuration != "" && p.BillingDuration != MediaBillingDurationOutput && p.BillingDuration != MediaBillingDurationTotal {
		return fmt.Errorf("media_pricing.duration_source must be output or total")
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		prefix := fmt.Sprintf("media_pricing.rules[%d] (%q)", i, r.Name)
		if p.BillingDuration == MediaBillingDurationTotal && r.Unit != "second" {
			return fmt.Errorf("%s: duration_source total requires unit second", prefix)
		}
		switch r.Unit {
		case "image", "second", "frame", "request":
			if r.Price == nil {
				return fmt.Errorf("%s: price is required (explicit 0 is allowed)", prefix)
			}
			if r.InputPrice != nil || r.OutputPrice != nil || r.ImageInputPrice != nil || r.ImageOutputPrice != nil {
				return fmt.Errorf("%s: token prices require unit token", prefix)
			}
		case "token":
			if r.Price != nil {
				return fmt.Errorf("%s: price is not used by unit token", prefix)
			}
			if r.InputPrice == nil && r.OutputPrice == nil && r.ImageInputPrice == nil && r.ImageOutputPrice == nil {
				return fmt.Errorf("%s: at least one explicit token price is required", prefix)
			}
		default:
			return fmt.Errorf("%s: unsupported unit %q", prefix, r.Unit)
		}
		for _, f := range []struct {
			name  string
			value *float64
		}{
			{"price", r.Price}, {"input_price", r.InputPrice}, {"output_price", r.OutputPrice},
			{"image_input_price", r.ImageInputPrice}, {"image_output_price", r.ImageOutputPrice}, {"prompt_price", r.PromptPrice},
		} {
			if f.value != nil && (!mediaFinite(*f.value) || *f.value < 0) {
				return fmt.Errorf("%s: %s must be finite and >= 0", prefix, f.name)
			}
		}
		c := r.Conditions
		for _, f := range []struct {
			name  string
			value *int64
		}{{"min_pixels", c.MinPixels}, {"max_pixels", c.MaxPixels}} {
			if f.value != nil && *f.value < 0 {
				return fmt.Errorf("%s: %s must be >= 0", prefix, f.name)
			}
		}
		if c.MinPixels != nil && c.MaxPixels != nil && *c.MinPixels > *c.MaxPixels {
			return fmt.Errorf("%s: min_pixels exceeds max_pixels", prefix)
		}
		for _, f := range []struct {
			name  string
			value *float64
		}{
			{"min_duration", c.MinDuration}, {"max_duration", c.MaxDuration}, {"fps", c.FPS}, {"min_fps", c.MinFPS}, {"max_fps", c.MaxFPS},
		} {
			if f.value != nil && (!mediaFinite(*f.value) || *f.value < 0) {
				return fmt.Errorf("%s: %s must be finite and >= 0", prefix, f.name)
			}
		}
		if c.MinDuration != nil && c.MaxDuration != nil && *c.MinDuration > *c.MaxDuration {
			return fmt.Errorf("%s: min_duration exceeds max_duration", prefix)
		}
		if c.MinFPS != nil && c.MaxFPS != nil && *c.MinFPS > *c.MaxFPS {
			return fmt.Errorf("%s: min_fps exceeds max_fps", prefix)
		}
		if c.FPS != nil && ((c.MinFPS != nil && *c.FPS < *c.MinFPS) || (c.MaxFPS != nil && *c.FPS > *c.MaxFPS)) {
			return fmt.Errorf("%s: fps is outside its bounds", prefix)
		}
	}
	return nil
}

// match returns the number of specified fields, whether known metadata
// contradicts the rule, and missing fields. A rule with missing metadata that
// outranks the best fully matched rule blocks fallback rather than underbilling.
func (c MediaPricingConditions) match(u MediaUsage) (score int, mismatch bool, missing []string) {
	check := func(set, known, matches bool, name string) {
		if !set {
			return
		}
		score++
		if !known {
			missing = append(missing, name)
		} else if !matches {
			mismatch = true
		}
	}
	for _, f := range []struct{ name, want, got string }{{"resolution", c.Resolution, u.Resolution}, {"mode", c.Mode, u.Mode}, {"input_type", c.InputType, u.InputType}} {
		check(f.want != "", f.got != "", strings.EqualFold(f.want, f.got), f.name)
	}
	for _, f := range []struct {
		name      string
		want, got *bool
	}{{"audio", c.Audio, u.Audio}, {"reference_video", c.ReferenceVideo, u.ReferenceVideo}, {"layer", c.Layer, u.Layer}} {
		same := f.want != nil && f.got != nil && *f.want == *f.got
		check(f.want != nil, f.got != nil, same, f.name)
	}
	if c.MinPixels != nil {
		check(true, u.Pixels > 0, u.Pixels >= *c.MinPixels, "pixels")
	}
	if c.MaxPixels != nil {
		check(true, u.Pixels > 0, u.Pixels <= *c.MaxPixels, "pixels")
	}
	if c.MinDuration != nil {
		check(true, u.DurationSeconds > 0, u.DurationSeconds >= *c.MinDuration, "duration_seconds")
	}
	if c.MaxDuration != nil {
		check(true, u.DurationSeconds > 0, u.DurationSeconds <= *c.MaxDuration, "duration_seconds")
	}
	if c.FPS != nil {
		check(true, u.FPS > 0, u.FPS == *c.FPS, "fps")
	}
	if c.MinFPS != nil {
		check(true, u.FPS > 0, u.FPS >= *c.MinFPS, "fps")
	}
	if c.MaxFPS != nil {
		check(true, u.FPS > 0, u.FPS <= *c.MaxFPS, "fps")
	}
	return
}

func selectMediaPricingRule(p *MediaPricing, u MediaUsage) (int, error) {
	best, bestScore := -1, -1
	pending, pendingScore := -1, -1
	var missing []string
	for i := range p.Rules {
		score, mismatch, fields := p.Rules[i].Conditions.match(u)
		if mismatch {
			continue
		}
		if len(fields) > 0 {
			if score > pendingScore {
				pending, pendingScore, missing = i, score, fields
			}
		} else if score > bestScore {
			best, bestScore = i, score
		}
	}
	if pending >= 0 && (pendingScore > bestScore || (pendingScore == bestScore && pending < best)) {
		return -1, fmt.Errorf("%w: rule %q requires %s", ErrMediaUsageIncomplete, p.Rules[pending].Name, strings.Join(missing, ", "))
	}
	if best < 0 {
		return -1, fmt.Errorf("%w: no matching media rule", ErrMediaPricingUnavailable)
	}
	return best, nil
}

func normalizeMediaPricingUsage(u MediaUsage) (MediaUsage, error) {
	if u.InputImageCount != nil && *u.InputImageCount < 0 {
		return u, fmt.Errorf("invalid media usage: input_image_count must be non-negative")
	}
	for _, duration := range []*float64{u.InputDurationSeconds, u.TotalDurationSeconds} {
		if duration != nil && (!mediaFinite(*duration) || *duration < 0) {
			return u, fmt.Errorf("invalid media usage: input/total duration must be finite and non-negative")
		}
	}
	if u.Width < 0 || u.Height < 0 || u.Pixels < 0 || u.Frames < 0 || u.Count < 0 || !mediaFinite(u.DurationSeconds) || u.DurationSeconds < 0 || !mediaFinite(u.FPS) || u.FPS < 0 {
		return u, fmt.Errorf("invalid media usage: dimensions, duration, fps, frames and count must be finite and non-negative")
	}
	if u.Width > 0 && u.Height > 0 {
		if u.Width > math.MaxInt64/u.Height {
			return u, fmt.Errorf("invalid media usage: pixel count overflow")
		}
		pixels := u.Width * u.Height
		if u.Pixels > 0 && u.Pixels != pixels {
			return u, fmt.Errorf("invalid media usage: pixels conflicts with width × height")
		}
		u.Pixels = pixels
	}
	return u, nil
}

// Per-output values override request metadata. Pixels from another output must
// not leak into an output with its own dimensions (or vice versa).
func mediaOutputUsage(parent, output MediaUsage) MediaUsage {
	u := parent
	u.Images = nil
	u.Count = 1
	if output.Resolution != "" {
		u.Resolution = output.Resolution
	}
	if output.Mode != "" {
		u.Mode = output.Mode
	}
	if output.InputType != "" {
		u.InputType = output.InputType
	}
	if output.Audio != nil {
		u.Audio = output.Audio
	}
	if output.ReferenceVideo != nil {
		u.ReferenceVideo = output.ReferenceVideo
	}
	if output.Layer != nil {
		u.Layer = output.Layer
	}
	if output.Pixels != 0 || output.Width != 0 || output.Height != 0 {
		u.Width, u.Height, u.Pixels = output.Width, output.Height, output.Pixels
	}
	if output.DurationSeconds != 0 {
		u.DurationSeconds = output.DurationSeconds
	}
	if output.FPS != 0 {
		u.FPS = output.FPS
	}
	if output.Frames != 0 {
		u.Frames = output.Frames
	} else if output.DurationSeconds != 0 || output.FPS != 0 {
		u.Frames = 0
	}
	return u
}

// CalculateMediaPricingCost is independent of provider protocol parsing. It
// returns the existing ledger components, with the multiplier applied once.
// PromptPrice is charged once per successful request, never once per image.
func CalculateMediaPricingCost(pricing *MediaPricing, usage *MediaUsage, tokens UsageTokens, multiplier float64) (*CostBreakdown, error) {
	if pricing == nil {
		return nil, ErrMediaPricingUnavailable
	}
	if err := ValidateMediaPricing(pricing); err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, fmt.Errorf("%w: media_usage is required", ErrMediaUsageIncomplete)
	}
	if !mediaFinite(multiplier) {
		return nil, fmt.Errorf("invalid media multiplier: must be finite")
	}
	if multiplier < 0 {
		multiplier = 0
	}
	base, err := normalizeMediaPricingUsage(*usage)
	if err != nil {
		return nil, err
	}
	if len(base.Images) > 0 && base.Count > 0 && base.Count != len(base.Images) {
		return nil, fmt.Errorf("invalid media usage: count differs from successful images")
	}
	bd := &CostBreakdown{}
	if base.Count == 0 && len(base.Images) == 0 {
		return bd, nil
	}
	outputs := []MediaUsage{base}
	if len(base.Images) > 0 {
		outputs = make([]MediaUsage, len(base.Images))
		for i, image := range base.Images {
			outputs[i], err = normalizeMediaPricingUsage(mediaOutputUsage(base, image))
			if err != nil {
				return nil, fmt.Errorf("media output %d: %w", i, err)
			}
		}
	}
	var totalSeconds float64
	if pricing.BillingDuration == MediaBillingDurationTotal {
		if base.TotalDurationSeconds != nil {
			// This is already the request total; never add reference seconds again.
			totalSeconds = *base.TotalDurationSeconds
		} else {
			if base.InputDurationSeconds == nil {
				return nil, fmt.Errorf("%w: total duration requires exact input_duration_seconds", ErrMediaUsageIncomplete)
			}
			totalSeconds = *base.InputDurationSeconds
			for _, output := range outputs {
				if output.DurationSeconds <= 0 {
					return nil, fmt.Errorf("%w: total duration requires output duration_seconds", ErrMediaUsageIncomplete)
				}
				totalSeconds += output.DurationSeconds * float64(output.Count)
			}
		}
		if totalSeconds <= 0 || !mediaFinite(totalSeconds) {
			return nil, fmt.Errorf("%w: positive finite total duration required", ErrMediaUsageIncomplete)
		}
	}
	var prompt *float64
	selectedTotal := -1
	selectedAggregate := -1
	for i, u := range outputs {
		index, err := selectMediaPricingRule(pricing, u)
		if err != nil {
			return nil, fmt.Errorf("media output %d: %w", i, err)
		}
		r := pricing.Rules[index]
		promptValue := 0.0
		if r.PromptPrice != nil {
			promptValue = *r.PromptPrice
		}
		if prompt != nil && *prompt != promptValue {
			return nil, fmt.Errorf("media outputs select conflicting per-request prompt prices")
		}
		prompt = &promptValue
		count := float64(u.Count)
		switch r.Unit {
		case "token", "request":
			if i > 0 && selectedAggregate < 0 {
				return nil, fmt.Errorf("%w: mixed aggregate and per-output media units", ErrMediaUsageIncomplete)
			}
			// Tokens and requests are aggregate measurements. Multiple outputs must
			// select the same rule, otherwise there is no exact allocation available.
			if len(outputs) > 1 {
				if i > 0 && selectedAggregate != index {
					return nil, fmt.Errorf("%w: mixed per-output rules cannot allocate request/token usage", ErrMediaUsageIncomplete)
				}
				if i > 0 {
					continue
				}
			}
			selectedAggregate = index
			if r.Unit == "request" {
				bd.OutputCost += *r.Price
			} else {
				tokenCost, err := mediaTokenCost(r, tokens)
				if err != nil {
					return nil, fmt.Errorf("media rule %q: %w", r.Name, err)
				}
				bd.InputCost += tokenCost.InputCost
				bd.ImageInputCost += tokenCost.ImageInputCost
				bd.OutputCost += tokenCost.OutputCost
				bd.ImageOutputCost += tokenCost.ImageOutputCost
				bd.CacheCreationCost += tokenCost.CacheCreationCost
				bd.CacheReadCost += tokenCost.CacheReadCost
			}
		case "image", "second", "frame":
			if selectedAggregate >= 0 {
				return nil, fmt.Errorf("%w: mixed aggregate and per-output media units", ErrMediaUsageIncomplete)
			}
			units := count
			if r.Unit == "second" {
				if pricing.BillingDuration == MediaBillingDurationTotal {
					// A request total cannot be allocated across different tariffs.
					if selectedTotal >= 0 {
						if selectedTotal != index {
							return nil, fmt.Errorf("%w: mixed rules cannot allocate total duration", ErrMediaUsageIncomplete)
						}
						continue
					}
					selectedTotal = index
					units = totalSeconds
				} else {
					if u.DurationSeconds <= 0 {
						return nil, fmt.Errorf("%w: rule %q requires duration_seconds", ErrMediaUsageIncomplete, r.Name)
					}
					units *= u.DurationSeconds
				}
			}
			if r.Unit == "frame" {
				frames := float64(u.Frames)
				if frames == 0 && u.DurationSeconds > 0 && u.FPS > 0 {
					// JSON metadata is decimal: binary float multiplication can
					// turn an exact 0.29 × 100 into 28.999999999999996.
					exactFrames := decimal.NewFromFloat(u.DurationSeconds).Mul(decimal.NewFromFloat(u.FPS))
					if !exactFrames.IsInteger() || exactFrames.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
						return nil, fmt.Errorf("%w: exact integral frame count required", ErrMediaUsageIncomplete)
					}
					frames = exactFrames.InexactFloat64()
				}
				if frames <= 0 {
					return nil, fmt.Errorf("%w: rule %q requires frames or duration_seconds and fps", ErrMediaUsageIncomplete, r.Name)
				}
				units *= frames
			}
			if r.Unit == "image" {
				bd.ImageOutputCost += units * *r.Price
			} else {
				bd.OutputCost += units * *r.Price
			}
		}
	}
	if prompt != nil {
		bd.InputCost += *prompt
	}
	if pricing.ExtraInputImagePrice != nil {
		if base.InputImageCount == nil {
			return nil, fmt.Errorf("%w: input_image_count is required for extra input image pricing", ErrMediaUsageIncomplete)
		}
		free := 0
		if pricing.FreeInputImages != nil {
			free = *pricing.FreeInputImages
		}
		bd.ImageInputCost += float64(max(*base.InputImageCount-free, 0)) * *pricing.ExtraInputImagePrice
	}
	bd.TotalCost = bd.InputCost + bd.ImageInputCost + bd.OutputCost + bd.ImageOutputCost + bd.CacheCreationCost + bd.CacheReadCost
	bd.ActualCost = bd.TotalCost * multiplier
	if !mediaFinite(bd.TotalCost) || !mediaFinite(bd.ActualCost) {
		return nil, fmt.Errorf("media cost overflow")
	}
	return bd, nil
}

// The media contract has no independent cache price: cached input is priced at
// the explicit input rate, separating image cache tokens just like input tokens.
// Nil image rates inherit the text rate; an explicit zero never inherits it.
func mediaTokenCost(r MediaPricingRule, t UsageTokens) (*CostBreakdown, error) {
	if t == (UsageTokens{}) {
		return nil, fmt.Errorf("%w: token rule requires reported token usage", ErrMediaUsageIncomplete)
	}
	for _, n := range []int{t.InputTokens, t.OutputTokens, t.ImageInputTokens, t.ImageOutputTokens, t.CacheReadTokens, t.ImageCacheReadTokens, t.CacheCreationTokens, t.CacheCreation5mTokens, t.CacheCreation1hTokens} {
		if n < 0 {
			return nil, fmt.Errorf("negative media token usage")
		}
	}
	if t.ImageInputTokens > t.InputTokens || t.ImageOutputTokens > t.OutputTokens || t.ImageCacheReadTokens > t.CacheReadTokens {
		return nil, fmt.Errorf("media token details exceed aggregate usage")
	}
	cost := func(n int, price, inherited *float64, name string) (float64, error) {
		if n == 0 {
			return 0, nil
		}
		if price == nil {
			price = inherited
		}
		if price == nil {
			return 0, fmt.Errorf("%w: %s is required for reported tokens", ErrMediaPricingUnavailable, name)
		}
		return float64(n) * *price, nil
	}
	bd := &CostBreakdown{}
	var err error
	if bd.InputCost, err = cost(t.InputTokens-t.ImageInputTokens, r.InputPrice, nil, "input_price"); err != nil {
		return nil, err
	}
	if bd.ImageInputCost, err = cost(t.ImageInputTokens, r.ImageInputPrice, r.InputPrice, "image_input_price"); err != nil {
		return nil, err
	}
	if bd.OutputCost, err = cost(t.OutputTokens-t.ImageOutputTokens, r.OutputPrice, nil, "output_price"); err != nil {
		return nil, err
	}
	if bd.ImageOutputCost, err = cost(t.ImageOutputTokens, r.ImageOutputPrice, r.OutputPrice, "image_output_price"); err != nil {
		return nil, err
	}
	if bd.CacheReadCost, err = cost(t.CacheReadTokens-t.ImageCacheReadTokens, r.InputPrice, nil, "input_price"); err != nil {
		return nil, err
	}
	imageCacheCost, err := cost(t.ImageCacheReadTokens, r.ImageInputPrice, r.InputPrice, "image_input_price")
	if err != nil {
		return nil, err
	}
	bd.CacheReadCost += imageCacheCost
	creation := t.CacheCreationTokens
	if creation == 0 {
		c5, c1 := normalizeCacheCreationBreakdown(t)
		if c5 > int(^uint(0)>>1)-c1 {
			return nil, fmt.Errorf("cache creation token overflow")
		}
		creation = c5 + c1
	}
	if bd.CacheCreationCost, err = cost(creation, r.InputPrice, nil, "input_price"); err != nil {
		return nil, err
	}
	return bd, nil
}
