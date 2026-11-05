//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type routeAuthCache struct {
	authCacheStub
	entries   map[string]*APIKeyAuthCacheEntry
	published []string
}

func (c *routeAuthCache) GetAuthCache(_ context.Context, key string) (*APIKeyAuthCacheEntry, error) {
	return c.entries[key], nil
}
func (c *routeAuthCache) SetAuthCache(_ context.Context, key string, e *APIKeyAuthCacheEntry, _ time.Duration) error {
	c.entries[key] = e
	return nil
}
func (c *routeAuthCache) DeleteAuthCache(_ context.Context, key string) error {
	delete(c.entries, key)
	return nil
}
func (c *routeAuthCache) PublishAuthCacheInvalidation(_ context.Context, key string) error {
	c.published = append(c.published, key)
	return nil
}
func TestAPIKeyRoutesCacheInvalidation(t *testing.T) {
	ctx := context.Background()
	id := int64(1)
	key := &APIKey{ID: 8, UserID: 2, GroupID: &id, RouteGroupIDs: []int64{1, 2}, User: &User{ID: 2, Status: StatusActive}, Group: &Group{ID: 1, Status: StatusActive, Platform: PlatformOpenAI}}
	calls := 0
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) { calls++; return key, nil }, listKeysByGroupID: func(_ context.Context, gid int64) ([]string, error) {
		require.EqualValues(t, 2, gid)
		return []string{"test-key"}, nil
	}}
	cache := &routeAuthCache{entries: map[string]*APIKeyAuthCacheEntry{}}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 32, L1TTLSeconds: 60, L2TTLSeconds: 60}}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	if svc.authCacheL1 != nil {
		defer svc.authCacheL1.Close()
	}
	got, err := svc.GetByKey(ctx, "test-key")
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, got.CandidateGroupIDs())
	if svc.authCacheL1 != nil {
		svc.authCacheL1.Wait()
	}
	got, err = svc.GetByKey(ctx, "test-key")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	key.RouteGroupIDs = []int64{1, 3, 2}
	svc.InvalidateAuthCacheByGroupID(ctx, 2)
	if svc.authCacheL1 != nil {
		svc.authCacheL1.Wait()
	}
	got, err = svc.GetByKey(ctx, "test-key")
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 2}, got.CandidateGroupIDs())
	require.Equal(t, 2, calls)
	require.NotEmpty(t, cache.published)
	// A second instance must hydrate the ordered list from shared L2 without DB.
	second := NewAPIKeyService(repo, nil, nil, nil, nil, cache, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}})
	got, err = second.GetByKey(ctx, "test-key")
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 2}, got.CandidateGroupIDs())
	require.Equal(t, 2, calls)
}
