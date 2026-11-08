//go:build unit

package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// currentConcurrencySortUserRepo 是「按当前并发排序」路径所需的仓储桩：
// 同时实现 ListIDsWithFilters（全量 ID）与 ListByIDsWithFilters（分批回表），
// 用来验证排序发生在全量集合上、分页切口在排序结果之后。
type currentConcurrencySortUserRepo struct {
	userRepoStub
	users []User
	err   error

	// 记录调用次数，确认没有退化成只排当前页。
	listIDsCalls     int
	listByIDsCalls   int
	lastDetailIDArgs []int64
}

func (s *currentConcurrencySortUserRepo) ListWithFilters(_ context.Context, params pagination.PaginationParams, _ UserListFilters) ([]User, *pagination.PaginationResult, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	out := make([]User, len(s.users))
	copy(out, s.users)
	// 这条路径只应在「在途不可用」时被走到，用真实分页语义返回。
	start := params.Offset()
	if start > len(out) {
		start = len(out)
	}
	end := start + params.Limit()
	if end > len(out) {
		end = len(out)
	}
	return out[start:end], &pagination.PaginationResult{
		Total:    int64(len(out)),
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

func (s *currentConcurrencySortUserRepo) ListIDsWithFilters(_ context.Context, _ UserListFilters) ([]int64, error) {
	s.listIDsCalls++
	if s.err != nil {
		return nil, s.err
	}
	ids := make([]int64, 0, len(s.users))
	for i := range s.users {
		ids = append(ids, s.users[i].ID)
	}
	return ids, nil
}

func (s *currentConcurrencySortUserRepo) ListByIDsWithFilters(_ context.Context, ids []int64, _ UserListFilters) ([]User, error) {
	s.listByIDsCalls++
	s.lastDetailIDArgs = append([]int64{}, ids...)
	if s.err != nil {
		return nil, s.err
	}
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	out := make([]User, 0, len(ids))
	for i := range s.users {
		if _, ok := wanted[s.users[i].ID]; ok {
			out = append(out, s.users[i])
		}
	}
	// 故意打乱返回顺序：调用方必须按排好的顺序重排，否则分页与排序不一致。
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (s *currentConcurrencySortUserRepo) GetLatestUsedAtByUserIDs(_ context.Context, _ []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// inflightCountStub 是 UserInflightCounter 的内存实现，直接给定每个用户的值。
type inflightCountStub struct {
	counts map[int64]int
	err    error
	// 记录最后一次被请求的用户集合，用来断言是一次批量读而不是 N+1。
	lastIDs []int64
	calls   int
}

func (s *inflightCountStub) CountUserInflightBatch(_ context.Context, userIDs []int64) (map[int64]int, error) {
	s.calls++
	s.lastIDs = append([]int64{}, userIDs...)
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[int64]int, len(userIDs))
	for _, id := range userIDs {
		out[id] = s.counts[id]
	}
	return out, nil
}

func newCurrentConcurrencySortService(repo UserRepository, counter UserInflightCounter) *adminServiceImpl {
	svc := &adminServiceImpl{userRepo: repo}
	if counter != nil {
		svc.SetUsageInflightCounter(counter)
	}
	return svc
}

// 验收点 1：按当前并发降序的第一页确实是全局最大的几个。
func TestAdminService_ListUsers_SortByCurrentConcurrency_DescFirstPageIsGlobalTop(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{
		{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5},
	}}
	counter := &inflightCountStub{counts: map[int64]int{
		1: 0,
		2: 3,
		3: 91,
		4: 7,
		5: 39,
	}}
	svc := newCurrentConcurrencySortService(repo, counter)

	users, total, err := svc.ListUsers(context.Background(), 1, 2, UserListFilters{}, "current_concurrency", "desc")
	require.NoError(t, err)
	require.Equal(t, int64(5), total, "总数必须是全量匹配数，不是当页数")
	require.Len(t, users, 2)
	// 91 与 39 是全局最大的两个，而不是 ID 最大的两个。
	require.Equal(t, []int64{3, 5}, []int64{users[0].ID, users[1].ID})
	require.Equal(t, 1, repo.listIDsCalls, "全量 ID 只取一次")
	require.Equal(t, 1, counter.calls, "在途数必须一次批量读，不能 N+1")
}

// 验收点 2：翻页后不重复不遗漏——第 1 页 + 第 2 页 + 第 3 页拼起来是完整全序。
func TestAdminService_ListUsers_SortByCurrentConcurrency_PagesDoNotOverlapOrDrop(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{
		{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}, {ID: 6}, {ID: 7},
	}}
	counter := &inflightCountStub{counts: map[int64]int{
		1: 5, 2: 0, 3: 91, 4: 7, 5: 39, 6: 12, 7: 0,
	}}
	svc := newCurrentConcurrencySortService(repo, counter)

	seen := make(map[int64]int)
	var order []int64
	for page := 1; page <= 4; page++ {
		users, total, err := svc.ListUsers(context.Background(), page, 2, UserListFilters{}, "current_concurrency", "desc")
		require.NoError(t, err)
		require.Equal(t, int64(7), total)
		for _, u := range users {
			seen[u.ID]++
			order = append(order, u.ID)
		}
	}

	require.Len(t, order, 7, "三页半合计必须正好覆盖全量 7 个用户")
	for id, n := range seen {
		require.Equal(t, 1, n, "用户 %d 出现在多页，翻页有重复", id)
	}
	// 期望全序：按在途数降序，等值时按 ID 降序（稳定兜底）。
	require.Equal(t, []int64{3, 5, 6, 4, 1, 7, 2}, order)
}

// 验收点 3：升序同样作用于全量，而不是当页。
func TestAdminService_ListUsers_SortByCurrentConcurrency_AscUsesGlobalOrder(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{
		{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4},
	}}
	counter := &inflightCountStub{counts: map[int64]int{1: 91, 2: 0, 3: 7, 4: 0}}
	svc := newCurrentConcurrencySortService(repo, counter)

	users, total, err := svc.ListUsers(context.Background(), 1, 2, UserListFilters{}, "current_concurrency", "asc")
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	// 在途 0 的两个最小；等值时升序按 ID 升序。ID 1 虽然 ID 最小但在途 91，不该出现在第一页。
	require.Equal(t, []int64{2, 4}, []int64{users[0].ID, users[1].ID})
}

// 验收点 4：原有的「按并发上限排序」仍然可用，语义不被新排序键抢走。
func TestAdminService_ListUsers_ConcurrencyLimitSortStillWorks(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{
		{ID: 1, Concurrency: 5},
		{ID: 2, Concurrency: 50},
	}}
	counter := &inflightCountStub{counts: map[int64]int{1: 0, 2: 0}}
	svc := newCurrentConcurrencySortService(repo, counter)

	_, _, err := svc.ListUsers(context.Background(), 1, 20, UserListFilters{}, "concurrency", "desc")
	require.NoError(t, err)
	// 走的是 SQL 分页路径：不该触发全量 ID 扫描与在途批量读。
	require.Equal(t, 0, repo.listIDsCalls)
	require.Equal(t, 0, counter.calls)
}

// 在途不可用时不能给出错序：回落到按并发上限排，并且不在途批量读。
func TestAdminService_ListUsers_SortByCurrentConcurrency_FallsBackWhenCounterMissing(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{{ID: 1}, {ID: 2}}}
	svc := newCurrentConcurrencySortService(repo, nil)

	users, total, err := svc.ListUsers(context.Background(), 1, 20, UserListFilters{}, "current_concurrency", "desc")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, users, 2)
	require.Equal(t, 0, repo.listIDsCalls)
}

// 在途读失败同样回落，不返回看似排过实则随机的顺序。
func TestAdminService_ListUsers_SortByCurrentConcurrency_FallsBackOnCounterError(t *testing.T) {
	repo := &currentConcurrencySortUserRepo{users: []User{{ID: 1}, {ID: 2}}}
	counter := &inflightCountStub{err: errors.New("redis down")}
	svc := newCurrentConcurrencySortService(repo, counter)

	users, total, err := svc.ListUsers(context.Background(), 1, 20, UserListFilters{}, "current_concurrency", "desc")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, users, 2)
}

// isCurrentConcurrencySort 只认这一个键，避免误伤 concurrency（上限）。
func TestIsCurrentConcurrencySort(t *testing.T) {
	require.True(t, isCurrentConcurrencySort("current_concurrency"))
	require.True(t, isCurrentConcurrencySort("  Current_Concurrency  "))
	require.False(t, isCurrentConcurrencySort("concurrency"))
	require.False(t, isCurrentConcurrencySort("current_currency"))
	require.False(t, isCurrentConcurrencySort(""))
}
