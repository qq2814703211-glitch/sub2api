package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type routeBindingProvider struct{}

func (routeBindingProvider) EstimateInflightReservation(context.Context, *service.APIKey, service.InflightEstimateRequest) (float64, bool) {
	return 0, true
}
func (routeBindingProvider) ResolveAPIKeyRouteSubscription(context.Context, *service.APIKey, *service.UserSubscription) (*service.UserSubscription, error) {
	return nil, nil
}
func (routeBindingProvider) ResolveChannelMappingAndRestrict(_ context.Context, _ *int64, model string) (service.ChannelMappingResult, bool) {
	return service.ChannelMappingResult{MappedModel: model}, false
}
func (routeBindingProvider) RebindKeyRouteProfitControl(ctx context.Context, _ *int64) context.Context {
	return ctx
}
func TestAPIKeyRoutesBindingCompositeMapping(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	primary, backup := int64(1), int64(2)
	old := &service.APIKey{GroupID: &primary, RouteGroupIDs: []int64{1, 2}, Group: &service.Group{ID: 1, Platform: service.PlatformOpenAI}}
	selected := &service.APIKey{GroupID: &backup, RouteGroupIDs: []int64{1, 2}, Group: &service.Group{ID: 2, Platform: service.PlatformComposite}, RouteDecision: &service.CompositeRouteDecision{Matched: true, GroupID: 2, TargetPlatform: service.PlatformOpenAI, PublicModel: "alias", UpstreamModel: "gpt-5.1"}}
	var sub *service.UserSubscription
	mapping := service.ChannelMappingResult{}
	state := &keyRouteBindingState{}
	require.NoError(t, state.bind(c, routeBindingProvider{}, nil, old, selected, &sub, &mapping, "alias", []byte(`{"model":"alias"}`)))
	require.True(t, mapping.Mapped)
	require.Equal(t, "gpt-5.1", mapping.MappedModel)
	require.EqualValues(t, 2, *selected.GroupID)
}
