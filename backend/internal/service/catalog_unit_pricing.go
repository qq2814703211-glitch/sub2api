package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
)

func (m BillingMode) isCatalogUnit() bool {
	return m == BillingModePerRequest || m == BillingModePerSecond || m == BillingModePerImage
}

func (e LiteLLMRawEntry) catalogUnitPrice() *float64 {
	switch e.GloryBillingMode {
	case BillingModePerRequest:
		return e.CostPerRequest
	case BillingModePerSecond:
		return e.CostPerSecond
	case BillingModePerImage:
		return e.OutputCostPerImage
	default:
		return nil
	}
}

// Deliberately bypass the token resolver's family, version and basename aliases.
func (s *BillingService) exactCatalogRow(model string) *LiteLLMModelPricing {
	if s == nil {
		return nil
	}
	return s.pricingService.exactCatalogRow(model)
}

func (p *PricingService) exactCatalogRow(model string) *LiteLLMModelPricing {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pricingData[strings.TrimSpace(model)]
}

func (s *BillingService) exactCatalogUnit(model string) *ResolvedPricing {
	return s.exactCatalogRow(model).resolvedCatalogUnit()
}

func (row *LiteLLMModelPricing) explicitMode() bool {
	return row != nil && (row.GloryBillingMode.isCatalogUnit() || row.GloryBillingMode == BillingModeToken)
}

func (row *LiteLLMModelPricing) resolvedCatalogUnit() *ResolvedPricing {
	if row == nil || !row.GloryBillingMode.isCatalogUnit() {
		return nil
	}
	r := &ResolvedPricing{Mode: row.GloryBillingMode, Source: PricingSourceLiteLLM, catalogUnitPrice: row.unitPrice, catalogUnitIdentity: row.rowIdentity}
	// Preserve the scalar for existing quote consumers. Settlement uses the typed unit.
	if row.unitPrice != nil {
		r.DefaultPerRequestPrice = *row.unitPrice
	}
	return r
}

func (r *ResolvedPricing) isCatalogUnit() bool {
	return r != nil && r.Source == PricingSourceLiteLLM && r.Mode.isCatalogUnit()
}

func (r *ResolvedPricing) catalogUnitCost(units, multiplier float64) (*CostBreakdown, error) {
	if r.catalogUnitPrice == nil || *r.catalogUnitPrice < 0 || !mediaFinite(*r.catalogUnitPrice) {
		return nil, fmt.Errorf("invalid %s catalog rate", r.Mode)
	}
	total := *r.catalogUnitPrice * units
	actual := total * math.Max(multiplier, 0)
	if units < 0 || !mediaFinite(total) || !mediaFinite(actual) {
		return nil, fmt.Errorf("invalid %s usage or cost", r.Mode)
	}
	return &CostBreakdown{priceBasis: priceBasis{Scope: "catalog", Model: r.catalogUnitIdentity.SKU, Row: r.catalogUnitIdentity}, BillingMode: string(r.Mode), TotalCost: total, ActualCost: actual, catalogUnitMode: r.Mode, catalogUnits: units, catalogUnitMultiplier: multiplier}, nil
}

// Only real billing identities may select global unit prices. In particular a
// normalized token fallback or a suffix stripped by the legacy candidate loop
// is not evidence of the price of an upstream SKU.
func (s *BillingService) catalogBillingModel(ctx context.Context, resolver *ModelPricingResolver, key *APIKey, selected, upstream string, fields ChannelUsageFields) string {
	explicitSource := fields.BillingModelSource
	if explicitSource == BillingModelSourceChannelMapped && fields.ChannelMappedModel == fields.OriginalModel {
		explicitSource = ""
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" || upstream == selected || explicitSource == BillingModelSourceRequested || explicitSource == BillingModelSourceChannelMapped {
		return selected
	}
	if !s.exactCatalogRow(upstream).explicitMode() {
		return selected
	}
	if resolver != nil && key != nil {
		// Keep the selling card on the requested/mapped name, even if a provider has its own exact SKU.
		if matchGroupModelPricing(key.Group, selected) != nil || (key.GroupID != nil && resolver.lookupChannelPricingNormalized(ctx, *key.GroupID, selected) != nil) {
			return selected
		}
	}
	if explicitSource != BillingModelSourceUpstream && s.exactCatalogRow(selected).explicitMode() {
		return selected
	}
	return upstream
}

type catalogUsage struct {
	success bool
	images  int
	seconds *float64 // aggregated by the provider adapter, never request or elapsed duration
}

func (u catalogUsage) cost(r *ResolvedPricing, multiplier float64) (*CostBreakdown, error) {
	units, err := u.units(r.Mode)
	if err != nil {
		return nil, err
	}
	return r.catalogUnitCost(units, multiplier)
}

func (u catalogUsage) units(mode BillingMode) (float64, error) {
	units := 0.0
	switch mode {
	case BillingModePerRequest:
		if u.success {
			units = 1
		}
	case BillingModePerImage:
		units = float64(u.images)
	case BillingModePerSecond:
		if u.success {
			if u.seconds == nil || *u.seconds < 0 || !mediaFinite(*u.seconds) {
				return 0, fmt.Errorf("%w: provider billable seconds required for per_second settlement", ErrMediaUsageIncomplete)
			}
			units = *u.seconds
		}
	default:
		return 0, fmt.Errorf("invalid catalog unit %q", mode)
	}
	return units, nil
}

// Group media scalars, including explicit zero, keep their existing units.
func catalogMediaOverride(key *APIKey, model, imageSize, resolution string, image, video bool, audio *AudioUsage) bool {
	if key == nil || key.Group == nil {
		return false
	}
	if image && apiKeyHasConfiguredImagePrice(key, NormalizeImageBillingTierOrDefault(imageSize)) {
		return true
	}
	if video && apiKeyHasConfiguredVideoPrice(key, model, NormalizeVideoBillingResolutionOrDefault(resolution)) {
		return true
	}
	if audio != nil {
		g := key.Group
		switch audio.Mode {
		case "realtime":
			return g.AudioRealtimePricePerMin != nil
		case "stt":
			return g.AudioSTTPricePerHour != nil
		case "tts":
			return g.AudioTTSPricePerMillionChars != nil
		}
	}
	return false
}

func (r *OpenAIForwardResult) catalogUsage() catalogUsage {
	images := r.ImageCount
	if r.actualImageCount != nil {
		images = *r.actualImageCount
	} else if r.ImageEndpoint && r.MediaUsage != nil {
		images = r.MediaUsage.Count
	}
	video := r.VideoCount > 0 || strings.HasPrefix(r.ResponseID, "seedance:") || strings.HasPrefix(r.ResponseID, "compat-video:")
	success := !r.UnitRequestFailed && r.SucceededForScheduling()
	if r.ImageEndpoint {
		success = success && images > 0
	}
	if video {
		success = success && (r.VideoCount > 0 || (r.MediaUsage != nil && r.MediaUsage.Count > 0))
	}
	seconds := r.ProviderBillableSeconds
	if r.AudioUsage != nil {
		seconds = r.AudioUsage.ProviderBillableSeconds
	}
	return catalogUsage{success: success, images: images, seconds: seconds}
}

// Called on upstream completion metadata before merging any create-time dimensions.
func reportedVideoSeconds(u *MediaUsage, count int) *float64 {
	if u == nil || count <= 0 {
		return nil
	}
	if u.TotalDurationSeconds != nil {
		return mediaCopyPtr(u.TotalDurationSeconds)
	}
	duration := u.DurationSeconds
	if duration <= 0 && u.Frames > 0 && u.FPS > 0 && mediaFinite(u.FPS) {
		duration = float64(u.Frames) / u.FPS
	}
	if duration > 0 {
		seconds := duration * float64(count)
		return &seconds
	}
	return nil
}

// CatalogVideoQuote is saved in the owner-bound pending record before the task
// ID is exposed. Polls use the original unit/rate even after a catalog reload.
type CatalogVideoQuote struct {
	Mode       BillingMode        `json:"mode"`
	Price      float64            `json:"price"`
	Multiplier float64            `json:"multiplier"`
	PriceTrace *RequestPriceTrace `json:"price_trace,omitempty"` // nil on pre-trace snapshots
}

func (s *OpenAIGatewayService) FreezeVideoCatalogQuote(ctx context.Context, key *APIKey, userID int64, pending *GrokVideoPendingBilling) error {
	// One handle owns the rate, row identity and revision even if the caller did not pin it.
	ctx = s.WithRequestCatalog(ctx)
	pin := RequestCatalogFromContext(ctx)
	key = pin.groupFor(key)
	billing := s.billingService.forRequest(ctx)
	model := billing.catalogBillingModel(ctx, s.resolver, key, pending.BillingModel, pending.UpstreamModel, ChannelUsageFields{})
	resolved := billing.exactCatalogUnit(model)
	if resolved == nil {
		return nil
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	if s.resolveOpenAIChannelPricing(ctx, model, key) != nil || catalogMediaOverride(key, model, "", pending.VideoResolution, false, true, nil) {
		return nil
	}
	unit, err := resolved.catalogUnitCost(1, 1)
	if err != nil {
		return err
	}
	rate := 1.0
	if key != nil && key.Group != nil {
		rate = s.ResolveUserGroupRateMultiplier(ctx, userID, key.Group.ID, key.Group.RateMultiplier)
	} else if s.cfg != nil {
		rate = s.cfg.Default.RateMultiplier
	}
	var group *Group
	if key != nil {
		group = key.Group
	}
	pending.CatalogQuote = &CatalogVideoQuote{Mode: resolved.Mode, Price: *resolved.catalogUnitPrice, Multiplier: resolveVideoRateMultiplier(key, rate), PriceTrace: pin.trace(unit, model, group)}
	return nil
}

func (r *ResolvedPricing) plazaUnitPricing() *ChannelModelPricing {
	return &ChannelModelPricing{BillingMode: r.Mode, CatalogUnitPrice: mediaCopyPtr(r.catalogUnitPrice)}
}

// Do not round a fractional provider meter into the legacy integer duration column.
func wholeProviderSeconds(seconds *float64) *int {
	if seconds == nil || *seconds <= 0 || math.Trunc(*seconds) != *seconds || *seconds >= float64(math.MaxInt32) {
		return nil
	}
	n := int(*seconds)
	return &n
}

func (r *ForwardResult) catalogUsage() catalogUsage {
	images := r.ImageCount
	if r.actualImageCount != nil {
		images = *r.actualImageCount
	}
	u := catalogUsage{success: !r.UnitRequestFailed && (!r.ImageEndpoint || images > 0), images: images}
	if r.AudioUsage != nil {
		u.seconds = r.AudioUsage.ProviderBillableSeconds
	}
	return u
}

// A failed attempt can be logged without claiming the money-event ID of its
// successful retry. The hash fits the usage_logs request_id limit.
func recordFailedCatalogAttempt(ctx context.Context, repo UsageLogRepository, log *UsageLog, cost *CostBreakdown, success bool, component string) bool {
	if success || cost == nil || !cost.catalogUnitMode.isCatalogUnit() || cost.catalogUnits != 0 || cost.TotalCost != 0 {
		return false
	}
	digest := sha256.Sum256([]byte(log.RequestID))
	log.RequestID = fmt.Sprintf("unit-attempt:%x", digest[:24])
	writeUsageLogBestEffort(ctx, repo, log, component)
	return true
}
