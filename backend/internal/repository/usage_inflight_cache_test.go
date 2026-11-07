//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// newUsageInflightTestCache 用 miniredis 起一个真实的 Redis 协议端点，
// 走的仍是生产代码里的 HSet/HIncrBy/HLen/Pipeline 路径，而不是手写 stub。
func newUsageInflightTestCache(t *testing.T) (service.UsageInflightStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewUsageInflightCache(client, 15), server
}

func snapshotFor(requestID string, userID, groupID int64) service.UsageInflightSnapshot {
	return service.UsageInflightSnapshot{
		RequestID: requestID,
		UserID:    userID,
		GroupID:   groupID,
		Email:     "user@example.com",
		Username:  "user",
		Model:     "claude-sonnet-4-5",
		StartedAt: time.Now().UTC(),
	}
}

// 分组实时人数按 user 去重：同一用户 3 条在途只算 1 人。
func TestUsageInflightCountActiveUsersDeduplicatesAcrossRequests(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("r1", 1, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("r2", 1, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("r3", 1, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("r4", 2, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("r5", 3, 9)))

	counts, err := store.CountActiveUsersByGroups(ctx, []int64{7, 9, 11})
	require.NoError(t, err)
	require.Equal(t, 2, counts[7], "group 7 should count two distinct users, not five requests")
	require.Equal(t, 1, counts[9])
	require.Equal(t, 0, counts[11])
}

// 注销后分组人数归零；重复注销不得把引用计数减成负数。
func TestUsageInflightReleaseReturnsCountToZero(t *testing.T) {
	store, server := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("a", 42, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("b", 42, 7)))

	counts, err := store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 1, counts[7], "one user with two requests is still one active user")

	require.NoError(t, store.Release(ctx, 42, "a", 7))
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 1, counts[7], "still one request in flight, so still one active user")

	require.NoError(t, store.Release(ctx, 42, "b", 7))
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 0, counts[7], "no requests left in flight")

	// 重复注销：快照已不存在，分组引用计数不应被再次递减。
	require.NoError(t, store.Release(ctx, 42, "b", 7))
	counts, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 0, counts[7])
	require.False(t, server.Exists(usageInflightGroupKey(7)), "empty group hash should be dropped, not left with a stale field")
}

// ListByGroup 跨用户读回明细，并按快照里的 group_id 二次过滤。
func TestUsageInflightListByGroupFiltersBySnapshotGroup(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("p1", 11, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("p2", 12, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("p3", 13, 9)))

	snapshots, err := store.ListByGroup(ctx, 7)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	for _, snapshot := range snapshots {
		require.Equal(t, int64(7), snapshot.GroupID)
	}

	// 请求进行中分组被改写：分组 key 里还留着旧用户，但快照已不再属于该分组，
	// ListByGroup 必须把它过滤掉。
	require.NoError(t, store.Release(ctx, 12, "p2", 7))
	require.NoError(t, store.Register(ctx, service.UsageInflightSnapshot{
		RequestID: "p2b", UserID: 12, GroupID: 9, StartedAt: time.Now().UTC(),
	}))
	snapshots, err = store.ListByGroup(ctx, 7)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, "p1", snapshots[0].RequestID)
}

// ListByUser 读取单个用户的全部在途快照。
func TestUsageInflightListByUser(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("u1", 21, 7)))
	require.NoError(t, store.Register(ctx, snapshotFor("u2", 21, 9)))
	require.NoError(t, store.Register(ctx, snapshotFor("u3", 22, 7)))

	snapshots, err := store.ListByUser(ctx, 21)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)

	require.NoError(t, store.Release(ctx, 21, "u1", 7))
	snapshots, err = store.ListByUser(ctx, 21)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, "u2", snapshots[0].RequestID)
}

// UpdateModel 补写模型名；快照已注销时不重建幽灵条目。
func TestUsageInflightUpdateModelSkipsReleasedSnapshot(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("m1", 31, 7)))
	require.NoError(t, store.UpdateModel(ctx, 31, "m1", "claude-opus-4-1", true))

	snapshots, err := store.ListByUser(ctx, 31)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, "claude-opus-4-1", snapshots[0].Model)
	require.True(t, snapshots[0].Stream)

	require.NoError(t, store.Release(ctx, 31, "m1", 7))
	require.NoError(t, store.UpdateModel(ctx, 31, "m1", "claude-opus-4-1", true))
	snapshots, err = store.ListByUser(ctx, 31)
	require.NoError(t, err)
	require.Empty(t, snapshots, "released snapshot must not be recreated by a late model update")
}

// two keys 都是不续期的：TTL 到期后自然消失，不会留下长期幽灵。
// 真实 Redis 里由 PExpire 设置，这里用 miniredis 的时间推进验证。
func TestUsageInflightKeysExpireWithoutRenewal(t *testing.T) {
	store, server := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("t1", 41, 7)))
	require.True(t, server.Exists(usageInflightKey(41)))
	require.True(t, server.Exists(usageInflightGroupKey(7)))

	// 超过 15 分钟 TTL（配置的最长请求时长）。
	server.FastForward(16 * time.Minute)

	require.False(t, server.Exists(usageInflightKey(41)), "user inflight hash should expire")
	require.False(t, server.Exists(usageInflightGroupKey(7)), "group refcount hash should expire")

	counts, err := store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 0, counts[7])
}

// Redis 不可用时，所有写方法只返回错误，不 panic；读方法同样返回错误。
func TestUsageInflightSurvivesUnavailableRedis(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	store := NewUsageInflightCache(client, 15)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server.Close()

	require.Error(t, store.Register(ctx, snapshotFor("d1", 51, 7)))
	require.Error(t, store.Release(ctx, 51, "d1", 7))
	_, err := store.ListByUser(ctx, 51)
	require.Error(t, err)
	_, err = store.ListByGroup(ctx, 7)
	require.Error(t, err)
	_, err = store.CountActiveUsersByGroups(ctx, []int64{7})
	require.Error(t, err)
}

// nil redis client 时退化为 no-op，绝不能让网关因为没配 Redis 而 panic。
func TestUsageInflightNilClientIsNoop(t *testing.T) {
	store := NewUsageInflightCache(nil, 15)
	ctx := context.Background()
	require.NoError(t, store.Register(ctx, snapshotFor("n1", 61, 7)))
	require.NoError(t, store.Release(ctx, 61, "n1", 7))
	snapshots, err := store.ListByUser(ctx, 61)
	require.NoError(t, err)
	require.Empty(t, snapshots)
	counts, err := store.CountActiveUsersByGroups(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 0, counts[7])
}
