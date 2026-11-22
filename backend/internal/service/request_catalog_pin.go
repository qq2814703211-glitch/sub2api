package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type requestCatalogKey struct{}

// RequestCatalog is a read-only handle, not a registry of historical prices.
// Publishers build a private map and replace it under PricingService.mu. A
// request borrows that map (including SKUs only discovered after forwarding).
// No request, body, context, writer, or previous handle is retained here.
type RequestCatalog struct {
	group                          *Group // immutable auth/repository snapshot; only consumed for the same billing identity
	at                             time.Time
	billing                        *BillingService
	channels                       *ChannelService
	baseHash, layersHash, revision string
}

func RequestCatalogFromContext(ctx context.Context) *RequestCatalog {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(requestCatalogKey{}).(*RequestCatalog)
	return p
}

func (p *RequestCatalog) Context(ctx context.Context) context.Context {
	if p == nil || RequestCatalogFromContext(ctx) == p {
		return ctx
	}
	return context.WithValue(ctx, requestCatalogKey{}, p)
}

func requestCatalogAt(ctx context.Context, fallback time.Time) time.Time {
	if p := RequestCatalogFromContext(ctx); p != nil {
		return p.at
	}
	return fallback
}

// CopyRequestPricing transfers only pricing and scalar platform selection to a
// bounded worker context. Never retain the incoming context for settlement.
func CopyRequestPricing(parent, dst context.Context) context.Context {
	if parent == nil {
		return dst
	}
	if force, _ := parent.Value(ctxkey.ForcePlatform).(string); force != "" {
		dst = context.WithValue(dst, ctxkey.ForcePlatform, force)
	}
	if platform, ok := ResolvedTargetPlatformFromContext(parent); ok {
		dst = WithResolvedTargetPlatform(dst, platform)
	}
	return RequestCatalogFromContext(parent).Context(dst)
}

func pinRequestCatalog(ctx context.Context, b *BillingService, channels *ChannelService, replace bool) context.Context {
	if b == nil || (!replace && RequestCatalogFromContext(ctx) != nil) {
		return ctx
	}
	p := &RequestCatalog{at: timezone.Now()}
	if live := b.pricingService; live != nil {
		live.mu.RLock()
		view := &PricingService{pricingData: live.pricingData}
		p.baseHash, p.layersHash, p.revision = live.catalogBaseHash, live.customFilesHash, live.catalogRevision
		live.mu.RUnlock()
		p.billing = &BillingService{cfg: b.cfg, pricingService: view, fallbackPrices: b.fallbackPrices, requestPricingAt: p.at, sharedFallbackWarnings: &b.fallbackWarnSeen}
	} else {
		p.billing = b
	}
	if channels != nil && (channels.repo != nil || channels.cache.Load() != nil) {
		cache, err := channels.loadCache(ctx)
		if err != nil || cache == nil || cache.readFailure {
			cache = channels.lastConfirmedPricing.Load()
		}
		if cache != nil {
			p.channels = &ChannelService{fixedPricingCache: cache}
		}
	}
	return p.Context(ctx)
}

func (s *GatewayService) WithRequestCatalog(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return pinRequestCatalog(ctx, s.billingService, s.channelService, false)
}

func (s *OpenAIGatewayService) WithRequestCatalog(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return pinRequestCatalog(ctx, s.billingService, s.channelService, false)
}

// NewTurnCatalog starts a distinct logical WS request on an existing connection.
func (s *OpenAIGatewayService) NewTurnCatalog(ctx context.Context) *RequestCatalog {
	if s == nil {
		return nil
	}
	return RequestCatalogFromContext(pinRequestCatalog(ctx, s.billingService, s.channelService, true))
}

// WithPricingGroup binds the chosen group, without retaining credentials or the user.
// Route authorization remains live. Never reuse one group's tariff for another.
func (p *RequestCatalog) WithPricingGroup(group *Group) *RequestCatalog {
	if p == nil || group == nil || (p.group != nil && p.group.ID == group.ID && p.group.Platform == group.Platform && p.group.SubscriptionType == group.SubscriptionType) {
		return p
	}
	next := *p
	next.group = group
	return &next
}

func (p *RequestCatalog) groupFor(key *APIKey) *APIKey {
	if p == nil || p.group == nil || key == nil || key.GroupID == nil || key.Group == nil || key.Group == p.group || *key.GroupID != p.group.ID || key.Group.ID != p.group.ID || key.Group.Platform != p.group.Platform || key.Group.SubscriptionType != p.group.SubscriptionType {
		return key
	}
	k := *key
	k.Group = p.group
	return &k
}

func (b *BillingService) forRequest(ctx context.Context) *BillingService {
	if p := RequestCatalogFromContext(ctx); p != nil && b != nil {
		return p.billing
	}
	return b
}

func (s *ChannelService) forRequestPricing(ctx context.Context) *ChannelService {
	if p := RequestCatalogFromContext(ctx); p != nil && p.channels != nil && s != nil {
		return p.channels
	}
	return s
}
