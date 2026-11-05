package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type routeSubscriptions struct {
	UserSubscriptionRepository
	subs map[int64]*UserSubscription
}

func (r *routeSubscriptions) GetActiveByUserIDAndGroupID(_ context.Context, _ int64, id int64) (*UserSubscription, error) {
	s := r.subs[id]
	if s == nil {
		return nil, ErrSubscriptionNotFound
	}
	return s, nil
}
func TestAPIKeyRoutesSubscriptionRevokedAndBillingIdentity(t *testing.T) {
	user := &User{ID: 7, Status: StatusActive}
	ids := []int64{1, 2}
	key := &APIKey{UserID: 7, User: user, GroupID: &ids[0], RouteGroupIDs: ids}
	groups := &routeTestGroups{groups: map[int64]*Group{1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}, 2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}}}
	subs := &routeSubscriptions{subs: map[int64]*UserSubscription{1: {ID: 11, UserID: 7, GroupID: 1, Status: SubscriptionStatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour)}, 2: {ID: 12, UserID: 7, GroupID: 2, Status: SubscriptionStatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour)}}}
	resolver := keyRouteResolver{groups: groups.GetByIDLite, subscriptions: subs}
	candidates, err := resolver.candidates(context.Background(), key, "gpt-test", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	delete(subs.subs, 1)
	candidates, err = resolver.candidates(context.Background(), key, "gpt-test", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.EqualValues(t, 2, *candidates[0].key.GroupID)
	sub, err := resolveAPIKeyRouteSubscription(context.Background(), subs, candidates[0].key, &UserSubscription{ID: 999, UserID: 7, GroupID: 1})
	require.NoError(t, err)
	require.EqualValues(t, 12, sub.ID)
	require.Error(t, validateAPIKeyRouteSubscriptionIdentity(candidates[0].key, user, &UserSubscription{UserID: 7, GroupID: 1}))
	subs.subs[2].ExpiresAt = time.Now().Add(-time.Second)
	_, err = resolver.candidates(context.Background(), key, "gpt-test", PlatformOpenAI)
	require.Error(t, err)
}
