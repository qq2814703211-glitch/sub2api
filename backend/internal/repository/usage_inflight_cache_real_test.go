//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 真 Redis 集成测试：验证在途登记表在真实 Redis 上的原子语义
// （HINCRBY/HDEL 的引用计数、HLEN 去重人数、PEXPIRE 不续期）。
//
// 由环境变量 USAGE_INFLIGHT_REDIS_ADDR 指定地址（例如 127.0.0.1:6399）；
// 未设置时跳过，避免在没起 Redis 的机器上误报失败。
func newUsageInflightRealRedis(t *testing.T) (service.UsageInflightStore, *redis.Client) {
	t.Helper()
	addr := os.Getenv("USAGE_INFLIGHT_REDIS_ADDR")
	if addr == "" {
		t.Skip("USAGE_INFLIGHT_REDIS_ADDR not set; skipping real-Redis integration test")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, client.FlushDB(ctx).Err(), "clean the integration DB before the test")
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanCancel()
		_ = client.FlushDB(cleanCtx).Err()
		_ = client.Close()
	})
	return NewUsageInflightCache(client, 15), client
}

// 真实 Redis 上：同一用户 3 条在途只算 1 人，注销后精确归零，重复注销不出现负引用。
func TestUsageInflightRealRedisRefcount(t *testing.T) {
	store, client := newUsageInflightRealRedis(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "r1", UserID: 1, GroupID: 7, StartedAt: time.Now().UTC()}))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "r2", UserID: 1, GroupID: 7, StartedAt: time.Now().UTC()}))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "r3", UserID: 1, GroupID: 7, StartedAt: time.Now().UTC()}))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "r4", UserID: 2, GroupID: 7, StartedAt: time.Now().UTC()}))

	counts, err := store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 2, counts[7], "HLEN on the group hash is the deduped user count")

	// 引用计数确实按请求增长，而不是按人去重存储。
	refcount, err := client.HGet(ctx, usageInflightGroupKey(7), "1").Int()
	require.NoError(t, err)
	require.Equal(t, 3, refcount, "three in-flight requests from user 1 must be three references")

	require.NoError(t, store.Release(ctx, 1, "r1", 7))
	require.NoError(t, store.Release(ctx, 1, "r2", 7))
	refcount, err = client.HGet(ctx, usageInflightGroupKey(7), "1").Int()
	require.NoError(t, err)
	require.Equal(t, 1, refcount)
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 2, counts[7], "user 1 still has one request in flight")

	require.NoError(t, store.Release(ctx, 1, "r3", 7))
	_, err = client.HGet(ctx, usageInflightGroupKey(7), "1").Result()
	require.ErrorIs(t, err, redis.Nil, "the last release must drop the user field, not leave a zero")
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 1, counts[7])

	require.NoError(t, store.Release(ctx, 2, "r4", 7))
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 0, counts[7])
}

// 真实 Redis 上：TTL 由登记时设置，后续读取/补写都不续期。
func TestUsageInflightRealRedisTTLIsNotRenewed(t *testing.T) {
	store, client := newUsageInflightRealRedis(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "t1", UserID: 41, GroupID: 7, StartedAt: time.Now().UTC()}))
	first, err := client.PTTL(ctx, usageInflightKey(41)).Result()
	require.NoError(t, err)
	require.Positive(t, first.Milliseconds())

	// 读路径不应改写 TTL。
	_, err = store.ListByUser(ctx, 41)
	require.NoError(t, err)
	_, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	after, err := client.PTTL(ctx, usageInflightKey(41)).Result()
	require.NoError(t, err)
	require.LessOrEqual(t, after.Milliseconds(), first.Milliseconds(), "reads must not renew the TTL")

	// 补写模型是 HSET + 不续期，因此 TTL 只会自然递减。
	require.NoError(t, store.UpdateModel(ctx, 41, "t1", "claude-opus-4-1", true))
	updated, err := client.PTTL(ctx, usageInflightKey(41)).Result()
	require.NoError(t, err)
	require.LessOrEqual(t, updated.Milliseconds(), after.Milliseconds(), "model updates must not renew the TTL")
}

// 真实 Redis 上：跨用户读回分组明细，并按快照 group_id 过滤。
func TestUsageInflightRealRedisListByGroup(t *testing.T) {
	store, _ := newUsageInflightRealRedis(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "g1", UserID: 11, GroupID: 7, Model: "claude-sonnet-4-5", StartedAt: time.Now().UTC()}))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "g2", UserID: 12, GroupID: 7, Model: "claude-opus-4-1", StartedAt: time.Now().UTC()}))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{RequestID: "g3", UserID: 13, GroupID: 9, Model: "gpt-5", StartedAt: time.Now().UTC()}))

	snapshots, err := store.ListByGroup(ctx, 7)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	for _, snapshot := range snapshots {
		require.Equal(t, int64(7), snapshot.GroupID)
	}
}
