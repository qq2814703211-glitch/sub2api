//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheGrokVideoBillingLeaseFencesExpiredOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	ctx := context.Background()
	key := "17:23:task-a"

	claimed, err := cache.ClaimGrokVideoBilled(ctx, key, "owner-old", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimGrokVideoBilled(ctx, key, "owner-race", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)

	server.FastForward(time.Minute + time.Second)
	claimed, err = cache.ClaimGrokVideoBilled(ctx, key, "owner-new", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, cache.ReleaseGrokVideoBilled(ctx, key, "owner-old"))
	claimed, err = cache.ClaimGrokVideoBilled(ctx, key, "owner-third", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed, "late release from the expired owner must preserve the successor lease")

	require.NoError(t, cache.ReleaseGrokVideoBilled(ctx, key, "owner-new"))
	claimed, err = cache.ClaimGrokVideoBilled(ctx, key, "owner-retry", 0)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, 2*time.Minute, server.TTL(grokVideoBilledPrefix+key))
}
