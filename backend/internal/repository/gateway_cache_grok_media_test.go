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

func TestGatewayCacheGrokVideoBillingLeaseRecoversAndProtectsNewOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	ctx := context.Background()

	claimed, err := cache.ClaimGrokVideoBilled(ctx, "17:23:task-a", "old-worker", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Greater(t, server.TTL(grokVideoBilledPrefix+"17:23:task-a"), 59*time.Second)

	claimed, err = cache.ClaimGrokVideoBilled(ctx, "17:23:task-a", "racing-worker", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)

	// Simulate process death after claim but before DB commit; lease expiry makes
	// the same durable task recoverable without waiting for a long claim TTL.
	server.FastForward(61 * time.Second)
	claimed, err = cache.ClaimGrokVideoBilled(ctx, "17:23:task-a", "recovered-worker", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	// A late failure callback from the dead/old worker must not delete recovery's lease.
	require.NoError(t, cache.ReleaseGrokVideoBilled(ctx, "17:23:task-a", "old-worker"))
	claimed, err = cache.ClaimGrokVideoBilled(ctx, "17:23:task-a", "third-worker", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)

	// Ownership includes user+API key in the key, so another owner can claim
	// the same upstream task ID independently.
	claimed, err = cache.ClaimGrokVideoBilled(ctx, "18:23:task-a", "other-owner", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, cache.ReleaseGrokVideoBilled(ctx, "17:23:task-a", "recovered-worker"))
	claimed, err = cache.ClaimGrokVideoBilled(ctx, "17:23:task-a", "retry-worker", 0)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, 2*time.Minute, server.TTL(grokVideoBilledPrefix+"17:23:task-a"))

	claimed, err = cache.ClaimGrokVideoBilled(ctx, "17:23:invalid-owner", " ", time.Minute)
	require.Error(t, err)
	require.False(t, claimed)
	require.Error(t, cache.ReleaseGrokVideoBilled(ctx, "17:23:task-a", ""))
}

func TestGatewayCacheGrokVideoPendingSnapshotSurvivesFourteenDays(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	ctx := context.Background()
	key := "17:23:seedance-task"
	payload := []byte(`{"model":"seedance","video_resolution":"1080p"}`)

	require.NoError(t, cache.SetGrokVideoPendingBilling(ctx, key, payload, 0))
	server.FastForward(25 * time.Hour)
	got, err := cache.GetGrokVideoPendingBilling(ctx, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	server.FastForward(13*24*time.Hour + 22*time.Hour)
	got, err = cache.GetGrokVideoPendingBilling(ctx, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	require.Greater(t, server.TTL(grokVideoPendingBillingPrefix+key), 0*time.Second)
	server.FastForward(time.Hour)
	got, err = cache.GetGrokVideoPendingBilling(ctx, key)
	require.NoError(t, err)
	require.Empty(t, got)
}
