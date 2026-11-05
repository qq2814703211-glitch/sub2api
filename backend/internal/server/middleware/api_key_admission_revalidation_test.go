package middleware

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAPIKeyQueueRevalidationRejectsRevokedIdentityPermissionsAndLimits(t *testing.T) {
	gid := int64(11)
	initial := &service.APIKey{ID: 7, UserID: 9, GroupID: &gid, Status: service.StatusActive, ConcurrencyLimit: 1, User: &service.User{ID: 9, Status: service.StatusActive}, Group: &service.Group{ID: gid, Status: service.StatusActive, Platform: service.PlatformOpenAI, AllowLive: true}}
	latest := *initial
	callback := newAPIKeyAuthRevalidator(func(context.Context, string) (*service.APIKey, error) { return &latest, nil }, "synthetic-key", "127.0.0.1", initial)
	ctx := service.WithAPIKeyQueueCapability(context.Background(), service.APIKeyQueueCapabilityLive)
	n, err := callback(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	latest.ConcurrencyLimit = 0
	n, err = callback(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	latest.Status = service.StatusAPIKeyDisabled
	_, err = callback(ctx)
	require.Equal(t, 401, infraerrors.Code(err))
	latest.Status = service.StatusActive
	group := *latest.Group
	group.AllowLive = false
	latest.Group = &group
	_, err = callback(ctx)
	require.Equal(t, 403, infraerrors.Code(err))
	require.Equal(t, "LIVE_NOT_ALLOWED", infraerrors.Reason(err))
	latest = *initial
	other := int64(12)
	latest.GroupID = &other
	_, err = callback(ctx)
	require.Equal(t, 503, infraerrors.Code(err))
	require.Equal(t, "API_KEY_GROUP_CHANGED", infraerrors.Reason(err))
}
