package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type routeTestGroups struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *routeTestGroups) GetByID(ctx context.Context, id int64) (*Group, error) {
	return r.GetByIDLite(ctx, id)
}
func (r *routeTestGroups) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	g := r.groups[id]
	if g == nil {
		return nil, ErrGroupNotFound
	}
	return g, nil
}

type routeTestUsers struct {
	UserRepository
	user *User
}

func (r *routeTestUsers) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type routeTestAccounts struct {
	schedulerGroupAwareOpenAIAccountRepo
	byGroup map[int64][]Account
}

func (r *routeTestAccounts) ListSchedulableByGroupID(_ context.Context, id int64) ([]Account, error) {
	return r.byGroup[id], nil
}
func (r *routeTestAccounts) ListSchedulableByGroupIDAndPlatform(_ context.Context, id int64, p string) ([]Account, error) {
	var out []Account
	for _, a := range r.byGroup[id] {
		if a.Platform == p {
			out = append(out, a)
		}
	}
	return out, nil
}
func TestAPIKeyRoutesRuntimeAuthorizationAndProtocol(t *testing.T) {
	user := &User{ID: 7, Status: StatusActive, AllowedGroups: []int64{1, 2, 3}}
	groups := &routeTestGroups{groups: map[int64]*Group{1: {ID: 1, Platform: PlatformAnthropic, Status: StatusActive}, 2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true}, 3: {ID: 3, Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true}}}
	users := &routeTestUsers{user: user}
	accounts := &routeTestAccounts{byGroup: map[int64][]Account{2: {{Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-test": "gpt-test"}}}}, 3: {{Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-test": "gpt-test"}}}}}}
	resolver := keyRouteResolver{groups: groups.GetByIDLite, users: users, accounts: accounts}
	id := int64(1)
	key := &APIKey{UserID: 7, GroupID: &id, RouteGroupIDs: []int64{1, 2, 3}, User: user, Group: groups.groups[1]}
	routes, err := resolver.candidates(context.Background(), key, "gpt-test", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	require.EqualValues(t, 2, *routes[0].key.GroupID)
	// Cached key user still grants 2; the repository has revoked it immediately.
	users.user = &User{ID: 7, Status: StatusActive, AllowedGroups: []int64{3}}
	routes, err = resolver.candidates(context.Background(), key, "gpt-test", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.EqualValues(t, 3, *routes[0].key.GroupID)
	require.EqualValues(t, 1, *key.GroupID)
	ctx := ContextWithAPIKeyRoute(context.Background(), routes[0].key)
	require.EqualValues(t, 3, ctx.Value(ctxkey.Group).(*Group).ID)
}
func TestAPIKeyRoutesRuntimeSchedulerAndBilling(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	user := &User{ID: 7, Status: StatusActive, Balance: 100}
	users := &routeTestUsers{user: user}
	groups := &routeTestGroups{groups: map[int64]*Group{1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 9}, 2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2.5}}}
	account := Account{ID: 3001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{2}}
	repo := &routeTestAccounts{byGroup: map[int64][]Account{1: {}, 2: {account}}}
	repo.accounts = []Account{account}
	svc := &OpenAIGatewayService{accountRepo: repo, userRepo: users, schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, repo, groups, &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true}}}), cache: &schedulerTestGatewayCache{}, cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true}}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	id := int64(1)
	key := &APIKey{ID: 1001, UserID: user.ID, User: user, GroupID: &id, Group: groups.groups[1], RouteGroupIDs: []int64{1, 2}}
	selection, _, routed, err := svc.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(context.Background(), key, "", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.EqualValues(t, 2, *routed.GroupID)
	require.Equal(t, 2.5, routed.Group.RateMultiplier)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	balances := &openAIRecordUsageUserRepoStub{}
	billing := newOpenAIRecordUsageServiceForTest(logs, balances, &openAIRecordUsageSubRepoStub{}, nil)
	usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 200}
	require.NoError(t, billing.RecordUsage(ContextWithAPIKeyRoute(context.Background(), routed), &OpenAIRecordUsageInput{APIKey: routed, User: user, Account: &account, Result: &OpenAIForwardResult{RequestID: "route-billing", Usage: usage, Model: "gpt-5.1", Duration: time.Second}}))
	require.EqualValues(t, 2, *logs.lastLog.GroupID)
	require.Equal(t, 2.5, logs.lastLog.RateMultiplier)
	want := expectedOpenAICost(t, billing, "gpt-5.1", usage, 2.5)
	require.InDelta(t, want.ActualCost, balances.lastAmount, 1e-12)
	// Non-reconstructible response chains cannot move to a different group.
	_, _, _, err = svc.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(context.Background(), key, "resp_bound", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
	require.Error(t, err)
}
func TestAPIKeyRoutesCompositeCandidateKeepsBillingParent(t *testing.T) {
	user := &User{ID: 7, Status: StatusActive}
	groups := &routeTestGroups{groups: map[int64]*Group{1: {ID: 1, Platform: PlatformAnthropic, Status: StatusActive}, 2: {ID: 2, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 2.5}}}
	id := int64(1)
	key := &APIKey{UserID: 7, User: user, GroupID: &id, Group: groups.groups[1], RouteGroupIDs: []int64{1, 2}}
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{routes: []CompositeModelRoute{{GroupID: 2, PublicModel: "alias", MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointResponses, TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5.1", Enabled: true}}})
	ctx := ContextWithAPIKeyRouteResolver(context.Background(), resolver, CompositeRouteEndpointResponses)
	routeResolver := keyRouteResolver{groups: groups.GetByIDLite}
	candidates, err := routeResolver.candidates(ctx, key, "alias", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	routed := candidates[0].key
	require.EqualValues(t, 2, *routed.GroupID)
	require.Equal(t, "gpt-5.1", candidates[0].model)
	selectedCtx := ContextWithAPIKeyRoute(ctx, routed)
	platform, ok := ResolvedTargetPlatformFromContext(selectedCtx)
	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, platform)
	require.EqualValues(t, 2, selectedCtx.Value(ctxkey.Group).(*Group).ID)
}

func TestAPIKeyRoutesRuntimeImageAndEligibility(t *testing.T) {
	user := &User{ID: 7, Status: StatusActive}
	groups := &routeTestGroups{groups: map[int64]*Group{1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: false}, 2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}}}
	id := int64(1)
	key := &APIKey{GroupID: &id, RouteGroupIDs: []int64{1, 2}, User: user}
	resolver := keyRouteResolver{groups: groups.GetByIDLite}
	routes, err := resolver.candidates(WithOpenAIImageGenerationIntent(context.Background()), key, "gpt-image-1", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.EqualValues(t, 2, *routes[0].key.GroupID)
	ctx := ContextWithAPIKeyRouteEligibility(context.Background(), func(ctx context.Context, key *APIKey) error {
		if *key.GroupID == 1 {
			return ErrDailyLimitExceeded
		}
		return nil
	})
	routes, err = resolver.candidates(ctx, key, "new-model", PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.EqualValues(t, 2, *routes[0].key.GroupID)
}
