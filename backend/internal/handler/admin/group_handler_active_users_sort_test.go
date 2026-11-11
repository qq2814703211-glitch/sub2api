//go:build unit

package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 锁定「实时使用中」(active_users) 排序语义：
// 1) desc 按在途人数从多到少；2) 平级按 sort_order 升序再 id 升序兜底；
// 3) counts 缺失按 0 计；4) 分页边界安全。
func TestSortGroupsByActiveUsers_DescAndTieBreak(t *testing.T) {
	groups := []service.Group{
		{ID: 1, SortOrder: 10},
		{ID: 2, SortOrder: 20},
		{ID: 3, SortOrder: 5},
		{ID: 4, SortOrder: 30}, // 无 counts 条目 → 0
	}
	counts := map[int64]int{1: 3, 2: 3, 3: 7}

	// desc：7(id3) > 3(id1,id2 平级) > 0(id4)
	got := sortGroupsByActiveUsers(cloneGroups(groups), counts, true, 1, 100)
	gotIDs := idsOf(got)
	require.Equal(t, []int64{3, 1, 2, 4}, gotIDs, "desc: 7 first, then tie by sort_order(10<20), then 0")

	// asc：0(id4) < 3(id1,id2) < 7(id3)
	gotAsc := sortGroupsByActiveUsers(cloneGroups(groups), counts, false, 1, 100)
	require.Equal(t, []int64{4, 1, 2, 3}, idsOf(gotAsc))
}

func TestSortGroupsByActiveUsers_Pagination(t *testing.T) {
	groups := []service.Group{
		{ID: 1, SortOrder: 1}, {ID: 2, SortOrder: 2},
		{ID: 3, SortOrder: 3}, {ID: 4, SortOrder: 4}, {ID: 5, SortOrder: 5},
	}
	counts := map[int64]int{1: 5, 2: 4, 3: 3, 4: 2, 5: 1}

	page1 := sortGroupsByActiveUsers(cloneGroups(groups), counts, true, 1, 2)
	require.Equal(t, []int64{1, 2}, idsOf(page1))

	page3 := sortGroupsByActiveUsers(cloneGroups(groups), counts, true, 3, 2)
	require.Equal(t, []int64{5}, idsOf(page3))

	// 越界页返回空而不 panic
	page9 := sortGroupsByActiveUsers(cloneGroups(groups), counts, true, 9, 2)
	require.Empty(t, page9)
}

func cloneGroups(in []service.Group) []service.Group {
	out := make([]service.Group, len(in))
	copy(out, in)
	return out
}

func idsOf(gs []service.Group) []int64 {
	ids := make([]int64, 0, len(gs))
	for i := range gs {
		ids = append(ids, gs[i].ID)
	}
	return ids
}
