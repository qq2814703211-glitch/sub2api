//go:build unit

package repository

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// 列表页的在途批量计数必须与详情页逐条聚合得到同一个数。
// 这正是「列表显示 39、点开详情却是 91」的回归防线：两个数只能来自同一份数据。
func TestUsageInflightCountByUsersMatchesListByUser(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	// 用户 2 有 3 条，用户 3 有 7 条（分两组），用户 4 有 1 条，用户 5 一条都没有。
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Register(ctx, snapshotFor("u2-"+strconv.Itoa(i), 2, 100)))
	}
	for i := 0; i < 4; i++ {
		require.NoError(t, store.Register(ctx, snapshotFor("u3g1-"+strconv.Itoa(i), 3, 100)))
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Register(ctx, snapshotFor("u3g2-"+strconv.Itoa(i), 3, 200)))
	}
	require.NoError(t, store.Register(ctx, snapshotFor("u4-0", 4, 100)))

	counts, err := store.CountByUsers(ctx, []int64{2, 3, 4, 5})
	require.NoError(t, err)
	require.Equal(t, 3, counts[2])
	require.Equal(t, 7, counts[3])
	require.Equal(t, 1, counts[4])
	require.Equal(t, 0, counts[5], "没有在途的用户返回 0，而不是缺 key")

	// 与详情路径逐用户比对：必须完全一致。
	for _, userID := range []int64{2, 3, 4, 5} {
		snapshots, err := store.ListByUser(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, len(snapshots), counts[userID],
			"用户 %d 的批量计数与逐条明细不一致（列表与详情会对不上）", userID)
	}
}

// 空入参与非法 ID 不应炸，也不应去读 Redis。
func TestUsageInflightCountByUsersHandlesEmptyInput(t *testing.T) {
	store, _ := newUsageInflightTestCache(t)
	ctx := context.Background()

	counts, err := store.CountByUsers(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, counts)

	counts, err = store.CountByUsers(ctx, []int64{0, -1})
	require.NoError(t, err)
	require.Empty(t, counts, "非正数 ID 直接跳过，不落进结果集")
}

// 单条脏快照不应让整批失败，也不应被计入——与 ListByUser 的容错口径一致。
func TestUsageInflightCountByUsersSkipsCorruptSnapshot(t *testing.T) {
	store, server := newUsageInflightTestCache(t)
	ctx := context.Background()

	require.NoError(t, store.Register(ctx, snapshotFor("ok-1", 7, 100)))
	require.NoError(t, store.Register(ctx, snapshotFor("ok-2", 7, 100)))
	// 直接往 hash 里塞一条坏 JSON，模拟历史脏数据。
	server.HSet("usage:inflight:7", "bad", "not-json")

	counts, err := store.CountByUsers(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, 2, counts[7], "坏 JSON 不计入，但不能让整批读失败")

	snapshots, err := store.ListByUser(ctx, 7)
	require.NoError(t, err)
	require.Len(t, snapshots, counts[7], "两条路径对脏数据的处理必须一致")
}
