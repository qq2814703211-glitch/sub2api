package service

import "context"

// A live connection never changes its selected group, but permissions and pricing
// must be read again before the next turn.
func (s *OpenAIGatewayService) RevalidateAPIKeyRoute(ctx context.Context, key *APIKey) (*APIKey, error) {
	if key == nil || key.GroupID == nil {
		return nil, ErrGroupNotAllowed
	}
	resolver := s.keyRouteResolver()
	if resolver.groups == nil || resolver.users == nil {
		return nil, ErrSchedulerCacheNotReady
	}
	user, err := resolver.users.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, ErrGroupNotAllowed
	}
	group, err := resolver.groups(ctx, *key.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil || !group.IsActive() {
		return nil, ErrGroupNotAllowed
	}
	if !group.IsSubscriptionType() && !user.CanBindGroup(group.ID, group.IsExclusive) {
		return nil, ErrGroupNotAllowed
	}
	routed := cloneAPIKeyWithGroupID(key, group)
	routed.User = user
	if _, err := resolveAPIKeyRouteSubscription(ctx, resolver.subscriptions, routed, nil); err != nil {
		return nil, err
	}
	return routed, nil
}
