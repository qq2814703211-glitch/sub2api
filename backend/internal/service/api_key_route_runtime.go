package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"time"
)

type selectedAPIKeyRouteContextKey struct{}
type pendingAPIKeyRouteContextKey struct{}
type apiKeyRouteResolverContextKey struct{}
type apiKeyRouteMessagesContextKey struct{}

func WithOpenAIMessagesKeyRoute(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiKeyRouteMessagesContextKey{}, true)
}

type apiKeyRouteEndpointContextKey struct{}
type apiKeyRouteModelsContextKey struct{}
type pinnedAPIKeyRouteContextKey struct{}
type apiKeyRouteCanMoveContextKey struct{}

func ContextWithAPIKeyRouteCanMove(ctx context.Context, canMove func() bool) context.Context {
	return context.WithValue(ctx, apiKeyRouteCanMoveContextKey{}, canMove)
}

func ContextWithPinnedAPIKeyRoute(ctx context.Context, groupID int64) context.Context {
	return context.WithValue(ctx, pinnedAPIKeyRouteContextKey{}, groupID)
}
func ContextWithAPIKeyRouteModels(ctx context.Context, models []string) context.Context {
	return context.WithValue(ctx, apiKeyRouteModelsContextKey{}, append([]string(nil), models...))
}
func ContextWithAPIKeyRouteResolver(ctx context.Context, resolver *CompositeRouteResolver, endpoint string) context.Context {
	ctx = context.WithValue(ctx, apiKeyRouteResolverContextKey{}, resolver)
	return context.WithValue(ctx, apiKeyRouteEndpointContextKey{}, endpoint)
}
func ContextWithPendingAPIKeyRoute(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, selectedAPIKeyRouteContextKey{}, int64(0))
	return context.WithValue(ctx, pendingAPIKeyRouteContextKey{}, true)
}
func ContextWithAPIKeyRoute(ctx context.Context, key *APIKey) context.Context {
	if key == nil || key.GroupID == nil || key.Group == nil {
		return ctx
	}
	if pin := RequestCatalogFromContext(ctx); pin != nil {
		ctx = pin.WithPricingGroup(key.Group).Context(ctx)
	}
	ctx = context.WithValue(ctx, ctxkey.Group, key.Group)
	if key.HasMultipleRoutes() {
		ctx = context.WithValue(ctx, gatewayTokenRequestBillingGroupCtxKey{}, key.Group)
	}
	if key.RouteDecision != nil {
		ctx = WithCompositeRouteDecision(ctx, *key.RouteDecision)
	} else if key.Group.Platform != PlatformComposite {
		ctx = context.WithValue(ctx, ctxkey.ResolvedTargetPlatform, key.Group.Platform)
		ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, "")
		ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "")
		ctx = context.WithValue(ctx, ctxkey.CompositeRouteSource, "")
	}
	return context.WithValue(ctx, selectedAPIKeyRouteContextKey{}, *key.GroupID)
}
func APIKeyRouteSelected(ctx context.Context, key *APIKey) bool {
	if key == nil || !key.HasMultipleRoutes() {
		return true
	}
	pending, _ := ctx.Value(pendingAPIKeyRouteContextKey{}).(bool)
	if !pending {
		return true
	}
	id, _ := ctx.Value(selectedAPIKeyRouteContextKey{}).(int64)
	return key.GroupID != nil && id == *key.GroupID
}
func shouldContinueAlongKeyRoutes(err error) bool {
	return errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrSchedulerCacheNotReady) || errors.Is(err, ErrClaudeCodeOnly) || errors.Is(err, ErrNoAvailableCompactAccounts) || errors.Is(err, ErrGroupNotFound)
}
func groupAllowsRequestedModel(group *Group, model string) bool {
	return group != nil && (!group.ModelAllowlistEnabled() || group.ModelAllowlist.Allows(model))
}
func groupPlatformFitsRequest(group, request string) bool {
	return request == "" || (request == PlatformOpenAI && domain.UsesOpenAIGateway(group)) || group == request || group == PlatformComposite || (group == PlatformAntigravity && (request == PlatformGemini || request == PlatformAnthropic))
}
func isOpenAICompatibleUpstreamPlatform(platform string) bool {
	return domain.UsesOpenAIGateway(platform)
}

type apiKeyRouteEligibilityContextKey struct{}
type apiKeyRouteProbeContextKey struct{}

func ContextWithAPIKeyRouteEligibility(ctx context.Context, check func(context.Context, *APIKey) error) context.Context {
	return context.WithValue(ctx, apiKeyRouteEligibilityContextKey{}, check)
}
func (s *BillingCacheService) CheckAPIKeyRouteEligibility(ctx context.Context, key *APIKey, sub *UserSubscription) error {
	ctx = context.WithValue(ContextWithAPIKeyRoute(ctx, key), apiKeyRouteProbeContextKey{}, true)
	return s.CheckBillingEligibility(ctx, key.User, key, key.Group, sub, QuotaPlatform(ctx, key))
}

type keyRouteResolver struct {
	groups        func(context.Context, int64) (*Group, error)
	users         UserRepository
	subscriptions UserSubscriptionRepository
	accounts      AccountRepository
	mapping       func(context.Context, *int64, string) (ChannelMappingResult, bool)
}
type keyRouteCandidate struct {
	key   *APIKey
	model string
	known bool
}

func (r keyRouteResolver) candidates(ctx context.Context, key *APIKey, model, platform string) ([]keyRouteCandidate, error) {
	if public, ok := RequestedPublicModelFromContext(ctx); ok && key.HasMultipleRoutes() {
		model = public
	}
	user := key.User
	// Never authorize a fallback from a stale L1/L2 user permission projection.
	if r.users != nil {
		var err error
		user, err = r.users.GetByID(ctx, key.UserID)
		if err != nil {
			return nil, err
		}
	}
	if user == nil || !user.IsActive() {
		return nil, ErrGroupNotAllowed
	}
	var out []keyRouteCandidate
	var lastErr error
	for _, id := range key.CandidateGroupIDs() {
		if canMove, ok := ctx.Value(apiKeyRouteCanMoveContextKey{}).(func() bool); ok && !canMove() && key.GroupID != nil && id != *key.GroupID {
			continue
		}
		if pinned, _ := ctx.Value(pinnedAPIKeyRouteContextKey{}).(int64); pinned > 0 && id != pinned {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.groups == nil {
			return nil, ErrSchedulerCacheNotReady
		}
		group, err := r.groups(ctx, id)
		if err != nil {
			lastErr = err
			continue
		}
		if group == nil || !group.IsActive() || !groupAllowsRequestedModel(group, model) {
			continue
		}
		allowed := true
		models, _ := ctx.Value(apiKeyRouteModelsContextKey{}).([]string)
		for _, m := range models {
			if !groupAllowsRequestedModel(group, m) {
				allowed = false
				break
			}
		}
		if !allowed {
			continue
		}
		if !groupPlatformFitsRequest(group.Platform, platform) {
			continue
		}
		if !group.IsSubscriptionType() && !user.CanBindGroup(group.ID, group.IsExclusive) {
			continue
		}
		routed := cloneAPIKeyWithGroupID(key, group)
		routed.RouteDecision = nil
		messages, _ := ctx.Value(apiKeyRouteMessagesContextKey{}).(bool)
		if messages && group.Platform != PlatformComposite && !IsMultiProtocolAPIKeyProvider(group.Platform) && group.Platform != PlatformGrok && !group.AllowMessagesDispatch {
			continue
		}
		if group.Platform == PlatformComposite {
			resolver, _ := ctx.Value(apiKeyRouteResolverContextKey{}).(*CompositeRouteResolver)
			endpoint, _ := ctx.Value(apiKeyRouteEndpointContextKey{}).(string)
			decision, e := resolver.Resolve(ctx, id, model, endpoint)
			if e != nil {
				lastErr = e
				continue
			}
			if decision.OAuthAmbiguous() {
				return nil, ErrNoAvailableAccounts
			}
			if !decision.Matched || !groupPlatformFitsRequest(decision.TargetPlatform, platform) {
				continue
			}
			routed.RouteDecision = &decision
			if messages && !IsMultiProtocolAPIKeyProvider(decision.TargetPlatform) && decision.TargetPlatform != PlatformGrok && !group.AllowMessagesDispatch {
				continue
			}
		}
		currentUser := *user
		currentUser.UserGroupRPMOverride = nil
		routed.User = &currentUser
		if group.IsSubscriptionType() {
			if _, err := resolveAPIKeyRouteSubscription(ctx, r.subscriptions, routed, nil); err != nil {
				lastErr = err
				continue
			}
		}
		if OpenAIImageGenerationIntentFromContext(ctx) && !GroupAllowsImageGeneration(group) {
			continue
		}
		if check, ok := ctx.Value(apiKeyRouteEligibilityContextKey{}).(func(context.Context, *APIKey) error); ok {
			if e := check(ContextWithAPIKeyRoute(ctx, routed), routed); e != nil {
				lastErr = e
				continue
			}
		}
		selectedModel := model
		if messages {
			selectedModel = NormalizeOpenAICompatRequestedModel(model)
			if alias := group.ResolveMessagesDispatchModel(model); alias != "" {
				selectedModel = alias
			}
		}
		if routed.RouteDecision != nil {
			selectedModel = routed.RouteDecision.UpstreamModel
		}
		if r.mapping != nil {
			mapping, restricted := r.mapping(ContextWithAPIKeyRoute(ctx, routed), routed.GroupID, model)
			if restricted {
				continue
			}
			if mapping.Mapped {
				selectedModel = mapping.MappedModel
			}
		}
		// A catalog is a hint, never an implicit denylist for newly introduced models.
		known := group.ModelAllowlistEnabled() || routed.RouteDecision != nil
		if r.accounts != nil {
			accounts, err := r.accounts.ListSchedulableByGroupID(ctx, id)
			if err != nil {
				lastErr = err
				continue
			}
			if len(accounts) == 0 {
				continue
			}
			for _, acc := range accounts {
				if platform != "" && !groupPlatformFitsRequest(acc.Platform, platform) {
					continue
				}
				for pattern := range acc.GetModelMapping() {
					if matchWildcard(pattern, model) || matchWildcard(pattern, selectedModel) {
						known = true
					}
				}
			}
		}
		out = append(out, keyRouteCandidate{routed, selectedModel, known})
	}
	if len(out) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, ErrNoAvailableAccounts
	}
	// Unknown catalogs yield to an explicitly supporting later authorized group.
	hasKnown := false
	for _, c := range out {
		hasKnown = hasKnown || c.known
	}
	if hasKnown {
		filtered := out[:0]
		for _, c := range out {
			if c.known {
				filtered = append(filtered, c)
			}
		}
		out = filtered
	}
	return out, nil
}
func resolveAPIKeyRouteSubscription(ctx context.Context, repo UserSubscriptionRepository, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	if key == nil || key.Group == nil || !key.Group.IsSubscriptionType() {
		return nil, nil
	}
	if repo == nil || key.GroupID == nil {
		return nil, ErrSubscriptionInvalid
	}
	sub, err := repo.GetActiveByUserIDAndGroupID(ctx, key.UserID, *key.GroupID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if sub == nil || sub.UserID != key.UserID || sub.GroupID != *key.GroupID || sub.Status != SubscriptionStatusActive || sub.DeletedAt != nil || sub.StartsAt.After(now) || !sub.ExpiresAt.After(now) {
		return nil, ErrSubscriptionInvalid
	}
	return sub, nil
}
func (s *OpenAIGatewayService) keyRouteResolver() keyRouteResolver {
	r := keyRouteResolver{users: s.userRepo, subscriptions: s.userSubRepo, accounts: s.accountRepo, mapping: s.ResolveChannelMappingAndRestrict}
	if s.schedulerSnapshot != nil {
		r.groups = s.schedulerSnapshot.GetGroupByIDLite
	} else if s.channelService != nil && s.channelService.groupRepo != nil {
		r.groups = s.channelService.groupRepo.GetByIDLite
	}
	return r
}
func (s *GatewayService) keyRouteResolver() keyRouteResolver {
	r := keyRouteResolver{users: s.userRepo, subscriptions: s.userSubRepo, accounts: s.accountRepo, mapping: s.ResolveChannelMappingAndRestrict}
	if s.groupRepo != nil {
		r.groups = s.groupRepo.GetByIDLite
	}
	return r
}
func (s *OpenAIGatewayService) ResolveAPIKeyRouteSubscription(ctx context.Context, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	return resolveAPIKeyRouteSubscription(ctx, s.userSubRepo, key, current)
}
func (s *GatewayService) ResolveAPIKeyRouteSubscription(ctx context.Context, key *APIKey, current *UserSubscription) (*UserSubscription, error) {
	return resolveAPIKeyRouteSubscription(ctx, s.userSubRepo, key, current)
}
func (s *OpenAIGatewayService) RebindKeyRouteProfitControl(ctx context.Context, id *int64) context.Context {
	return s.withOpenAIProfitControlGate(ctx, id)
}
func (s *GatewayService) RebindKeyRouteProfitControl(ctx context.Context, id *int64) context.Context {
	return s.withGatewayProfitControlGate(ctx, id)
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(ctx context.Context, key *APIKey, previous, session, model string, excluded map[int64]struct{}, transport OpenAIUpstreamTransport, capability OpenAIEndpointCapability, compact, canMove, tokenCost bool, platform ...string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	if !key.HasMultipleRoutes() {
		a, d, e := s.SelectAccountWithSchedulerForCapability(ctx, key.GroupID, previous, session, model, excluded, transport, capability, compact, canMove, tokenCost, platform...)
		return a, d, key, e
	}
	// A non-reconstructible previous_response_id is group-scoped; never move it.
	candidates, err := s.keyRouteResolver().candidates(ctx, key, model, PlatformOpenAI)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, key, err
	}
	var decision OpenAIAccountScheduleDecision
	for _, candidate := range candidates {
		routed := candidate.key
		if previous != "" && !canMove && (key.GroupID == nil || *key.GroupID != *routed.GroupID) {
			continue
		}
		p := platform
		if routed.RouteDecision != nil {
			p = []string{routed.RouteDecision.TargetPlatform}
		}
		if isOpenAICompatibleUpstreamPlatform(routed.Group.Platform) {
			p = []string{routed.Group.Platform}
		} else if routed.Group.Platform != PlatformComposite {
			continue
		}
		routeCtx := s.withOpenAIProfitControlGate(ContextWithAPIKeyRoute(ctx, routed), routed.GroupID)
		a, d, e := s.SelectAccountWithSchedulerForCapability(routeCtx, routed.GroupID, previous, session, candidate.model, excluded, transport, capability, compact, canMove, tokenCost, p...)
		decision, err = d, e
		if e == nil {
			return a, d, routed, nil
		}
		if !shouldContinueAlongKeyRoutes(e) {
			return nil, d, key, e
		}
	}
	if err == nil {
		err = ErrNoAvailableAccounts
	}
	return nil, decision, key, err
}
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForImagesAlongKeyRoutes(ctx context.Context, key *APIKey, session, model string, excluded map[int64]struct{}, capability OpenAIImagesCapability) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	if !key.HasMultipleRoutes() {
		a, d, e := s.SelectAccountWithSchedulerForImages(ctx, key.GroupID, session, model, excluded, capability)
		return a, d, key, e
	}
	candidates, err := s.keyRouteResolver().candidates(WithOpenAIImageGenerationIntent(ctx), key, model, PlatformOpenAI)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, key, err
	}
	var d OpenAIAccountScheduleDecision
	for _, candidate := range candidates {
		if candidate.key.Group.Platform != PlatformOpenAI && (candidate.key.RouteDecision == nil || candidate.key.RouteDecision.TargetPlatform != PlatformOpenAI) {
			continue
		}
		a, decision, e := s.SelectAccountWithSchedulerForImages(ContextWithAPIKeyRoute(ctx, candidate.key), candidate.key.GroupID, session, candidate.model, excluded, capability)
		d, err = decision, e
		if e == nil {
			return a, d, candidate.key, nil
		}
		if !shouldContinueAlongKeyRoutes(e) {
			return nil, d, key, e
		}
	}
	if err == nil {
		err = ErrNoAvailableAccounts
	}
	return nil, d, key, err
}
func (s *GatewayService) SelectAccountAlongKeyRoutes(ctx context.Context, key *APIKey, session, model string, excluded map[int64]struct{}, metadata string, userID int64, platform string) (*AccountSelectionResult, *APIKey, error) {
	if !key.HasMultipleRoutes() {
		a, e := s.SelectAccountWithLoadAwareness(ctx, key.GroupID, session, model, excluded, metadata, userID)
		return a, key, e
	}
	candidates, err := s.keyRouteResolver().candidates(ctx, key, model, platform)
	if err != nil {
		return nil, key, err
	}
	for _, candidate := range candidates {
		routed := candidate.key
		group, resolvedID, e := s.resolveGatewayGroup(ContextWithAPIKeyRoute(ctx, routed), routed.GroupID)
		if e != nil {
			err = e
			if shouldContinueAlongKeyRoutes(e) {
				continue
			}
			return nil, key, e
		}
		if group != nil && resolvedID != nil && *resolvedID != *routed.GroupID {
			routed = cloneAPIKeyWithGroupID(routed, group)
		}
		routeCtx := ContextWithAPIKeyRoute(ctx, routed)
		if _, ok := gatewayTokenRequestPricingAtFromContext(routeCtx); ok {
			routeCtx = context.WithValue(routeCtx, gatewayTokenRequestBillingGroupCtxKey{}, routed.Group)
		}
		routeCtx = s.withGatewayProfitControlGate(routeCtx, routed.GroupID)
		a, e := s.SelectAccountWithLoadAwareness(routeCtx, routed.GroupID, session, candidate.model, excluded, metadata, userID)
		err = e
		if e == nil {
			return a, routed, nil
		}
		if !shouldContinueAlongKeyRoutes(e) {
			return nil, key, e
		}
	}
	if err == nil {
		err = ErrNoAvailableAccounts
	}
	return nil, key, err
}

func (s *GatewayService) PrepareAPIKeyRoute(ctx context.Context, key *APIKey, model, platform string) (*APIKey, error) {
	if platform == "" {
		if detected, ok := DetectModelPlatform(model); ok {
			platform = detected
		}
	}
	candidates, err := s.keyRouteResolver().candidates(ctx, key, model, platform)
	if err != nil {
		return nil, err
	}
	return candidates[0].key, nil
}

// ResolveAPIKeyRouteAdmission finds a currently authorized entry group, preserving
// original route order. Full request/model eligibility is repeated by selection.
func (s *APIKeyService) ResolveAPIKeyRouteAdmission(ctx context.Context, key *APIKey) (*APIKey, error) {
	if !key.HasMultipleRoutes() {
		return key, nil
	}
	user, err := s.userRepo.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, ErrGroupNotAllowed
	}
	for _, id := range key.CandidateGroupIDs() {
		group, e := s.groupRepo.GetByID(ctx, id)
		if e != nil || group == nil || !group.IsActive() {
			continue
		}
		if s.canUserBindGroup(ctx, user, group) {
			routed := cloneAPIKeyWithGroupID(key, group)
			routed.User = user
			return routed, nil
		}
	}
	return nil, ErrGroupNotAllowed
}

// UsesRequestTargetPlatform lets model dispatch reach compatible backup groups.
func (k *APIKey) UsesRequestTargetPlatform() bool {
	return k != nil && (k.HasMultipleRoutes() || (k.Group != nil && k.Group.Platform == PlatformComposite))
}
