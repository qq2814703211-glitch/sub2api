package handler

import (
	"context"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strings"
)

type keyRouteBillingProvider interface {
	inflightReservationEstimator
	ResolveAPIKeyRouteSubscription(context.Context, *service.APIKey, *service.UserSubscription) (*service.UserSubscription, error)
	ResolveChannelMappingAndRestrict(context.Context, *int64, string) (service.ChannelMappingResult, bool)
	RebindKeyRouteProfitControl(context.Context, *int64) context.Context
}
type keyRouteOriginalBodyContextKey struct{}
type keyRouteEstimateContextKey struct{}

func withKeyRouteEstimate(ctx context.Context, req service.InflightEstimateRequest) context.Context {
	return context.WithValue(ctx, keyRouteEstimateContextKey{}, req)
}

type keyRouteBindingState struct {
	groupID int64
	done    func()
	body    []byte
}

func newKeyRouteBindingState(c *gin.Context, key *service.APIKey, gateway keyRouteBillingProvider, billing *service.BillingCacheService) *keyRouteBindingState {
	if key.HasMultipleRoutes() {
		c.Request = c.Request.WithContext(service.ContextWithAPIKeyRouteCanMove(c.Request.Context(), func() bool { return c.Writer.Size() <= 0 }))
		c.Request = c.Request.WithContext(service.ContextWithAPIKeyRouteEligibility(c.Request.Context(), func(ctx context.Context, candidate *service.APIKey) error {
			sub, err := gateway.ResolveAPIKeyRouteSubscription(ctx, candidate, nil)
			if err != nil {
				return err
			}
			if billing != nil {
				return billing.CheckAPIKeyRouteEligibility(ctx, candidate, sub)
			}
			return nil
		}))
	}
	if key.HasMultipleRoutes() {
		c.Request = c.Request.WithContext(service.ContextWithPendingAPIKeyRoute(c.Request.Context()))
	}
	return &keyRouteBindingState{}
}
func (s *keyRouteBindingState) Close() {
	if s.done != nil {
		s.done()
		s.done = nil
	}
}
func (s *keyRouteBindingState) bind(c *gin.Context, gateway keyRouteBillingProvider, billing *service.BillingCacheService, previous, selected *service.APIKey, sub **service.UserSubscription, mapping *service.ChannelMappingResult, model string, body []byte) error {
	if selected == nil || !selected.HasMultipleRoutes() {
		return nil
	}
	if previous != nil && previous.GroupID != nil && *previous.GroupID != *selected.GroupID && c.Writer.Size() > 0 {
		return service.ErrNoAvailableAccounts
	}
	if s.groupID == *selected.GroupID {
		return nil
	}
	policyBody := body
	if original, ok := c.Request.Context().Value(keyRouteOriginalBodyContextKey{}).([]byte); ok {
		policyBody = original
	}
	var policyErr error
	if strings.Contains(c.Request.URL.Path, "/messages") {
		policyBody, _, policyErr = service.ApplyReasoningEffortPolicy(policyBody, selected.Group.MaxReasoningEffort, selected.Group.ReasoningEffortMappings, selected.Group.MaxReasoningEffortOverLimit)
	} else if strings.Contains(c.Request.URL.Path, "/responses") || strings.Contains(c.Request.URL.Path, "/chat/completions") {
		policyBody, _, policyErr = service.ApplyOpenAIReasoningEffortPolicy(policyBody, selected.Group.MaxReasoningEffort, selected.Group.ReasoningEffortMappings, selected.Group.MaxReasoningEffortOverLimit)
	}
	if policyErr != nil {
		return policyErr
	}
	s.body = policyBody
	ctx := gateway.RebindKeyRouteProfitControl(service.ContextWithAPIKeyRoute(c.Request.Context(), selected), selected.GroupID)
	subscription, err := gateway.ResolveAPIKeyRouteSubscription(ctx, selected, *sub)
	if err != nil {
		return err
	}
	if billing != nil {
		if err := billing.CheckBillingEligibility(ctx, selected.User, selected, selected.Group, subscription, service.QuotaPlatform(ctx, selected)); err != nil {
			return err
		}
	}
	resolved, restricted := gateway.ResolveChannelMappingAndRestrict(ctx, selected.GroupID, model)
	if restricted {
		return service.ErrNoAvailableAccounts
	}
	if selected.RouteDecision != nil && !resolved.Mapped && selected.RouteDecision.UpstreamModel != "" && selected.RouteDecision.UpstreamModel != model {
		resolved.Mapped = true
		resolved.MappedModel = selected.RouteDecision.UpstreamModel
	}
	estimate := tokenInflightEstimate(model, body)
	if saved, ok := ctx.Value(keyRouteEstimateContextKey{}).(service.InflightEstimateRequest); ok {
		estimate = saved
		estimate.Model = model
	}
	if resolved.Mapped {
		estimate.Model = resolved.MappedModel
	}
	s.Close()
	ctx = service.WithoutInflightReservation(ctx)
	ctx, done, err := reserveInflightBalanceCtx(ctx, billing, gateway, selected, subscription, estimate)
	if err != nil {
		return err
	}
	s.done = done
	s.groupID = *selected.GroupID
	*sub = subscription
	if mapping != nil {
		*mapping = resolved
	}
	c.Set(string(middleware.ContextKeyAPIKey), selected)
	c.Set(string(middleware.ContextKeySubscription), subscription)
	c.Request = c.Request.WithContext(ctx)
	return nil
}
func releaseRejectedKeyRouteSelection(selection *service.AccountSelectionResult) {
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
