package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// usageInflightStoreStub 是 UsageInflightStore 的内存实现。
//
// 它复刻真实 Redis 实现的语义（请求级快照 + 分组内 userID → 在途数引用计数），
// 使服务层聚合逻辑可以在不连 Redis 的情况下验证；failAll 用来模拟 Redis 不可用。
type usageInflightStoreStub struct {
	mu       sync.Mutex
	byUser   map[int64]map[string]UsageInflightSnapshot
	byGroup  map[int64]map[int64]int
	failAll  error
	listErr  error
	countErr error
}

func newUsageInflightStoreStub() *usageInflightStoreStub {
	return &usageInflightStoreStub{
		byUser:  make(map[int64]map[string]UsageInflightSnapshot),
		byGroup: make(map[int64]map[int64]int),
	}
}

func (s *usageInflightStoreStub) Register(_ context.Context, snapshot UsageInflightSnapshot) error {
	if s.failAll != nil {
		return s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byUser[snapshot.UserID] == nil {
		s.byUser[snapshot.UserID] = make(map[string]UsageInflightSnapshot)
	}
	s.byUser[snapshot.UserID][snapshot.RequestID] = snapshot
	if snapshot.GroupID > 0 {
		if s.byGroup[snapshot.GroupID] == nil {
			s.byGroup[snapshot.GroupID] = make(map[int64]int)
		}
		s.byGroup[snapshot.GroupID][snapshot.UserID]++
	}
	return nil
}

func (s *usageInflightStoreStub) UpdateModel(_ context.Context, userID int64, requestID, model string, stream bool) error {
	if s.failAll != nil {
		return s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	requests, ok := s.byUser[userID]
	if !ok {
		return nil
	}
	snapshot, ok := requests[requestID]
	if !ok {
		return nil
	}
	snapshot.Model = model
	snapshot.Stream = stream
	requests[requestID] = snapshot
	return nil
}

func (s *usageInflightStoreStub) Release(_ context.Context, userID int64, requestID string, groupID int64) error {
	if s.failAll != nil {
		return s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	requests, ok := s.byUser[userID]
	if !ok {
		return nil
	}
	snapshot, ok := requests[requestID]
	if !ok {
		return nil
	}
	delete(requests, requestID)
	if len(requests) == 0 {
		delete(s.byUser, userID)
	}
	effectiveGroup := groupID
	if effectiveGroup <= 0 {
		effectiveGroup = snapshot.GroupID
	}
	if effectiveGroup <= 0 {
		return nil
	}
	counts := s.byGroup[effectiveGroup]
	if counts == nil {
		return nil
	}
	counts[userID]--
	if counts[userID] <= 0 {
		delete(counts, userID)
	}
	if len(counts) == 0 {
		delete(s.byGroup, effectiveGroup)
	}
	return nil
}

func (s *usageInflightStoreStub) ListByUser(_ context.Context, userID int64) ([]UsageInflightSnapshot, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.failAll != nil {
		return nil, s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UsageInflightSnapshot, 0, len(s.byUser[userID]))
	for _, snapshot := range s.byUser[userID] {
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
	return out, nil
}

func (s *usageInflightStoreStub) CountByUsers(_ context.Context, userIDs []int64) (map[int64]int, error) {
	if s.countErr != nil {
		return nil, s.countErr
	}
	if s.failAll != nil {
		return nil, s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		// 与真实实现一致：只计能解析的快照，这里 byUser 里存的就是已解析的。
		counts[userID] = len(s.byUser[userID])
	}
	return counts, nil
}

func (s *usageInflightStoreStub) ListByGroup(_ context.Context, groupID int64) ([]UsageInflightSnapshot, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.failAll != nil {
		return nil, s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UsageInflightSnapshot, 0)
	for userID := range s.byGroup[groupID] {
		for _, snapshot := range s.byUser[userID] {
			if snapshot.GroupID == groupID {
				out = append(out, snapshot)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
	return out, nil
}

func (s *usageInflightStoreStub) CountActiveUsersByGroups(_ context.Context, groupIDs []int64) (map[int64]int, error) {
	if s.countErr != nil {
		return nil, s.countErr
	}
	if s.failAll != nil {
		return nil, s.failAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		counts[groupID] = len(s.byGroup[groupID])
	}
	return counts, nil
}

// usageInflightGroupRepoStub 只实现分组名查询所需的最小能力，其余方法由
// GroupRepository 的 nil 内嵌提供（本测试不触碰其它方法）。
type usageInflightGroupRepoStub struct {
	GroupRepository
	names map[int64]string
}

func (r *usageInflightGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	name, ok := r.names[id]
	if !ok {
		return nil, errors.New("group not found")
	}
	return &Group{ID: id, Name: name}, nil
}

func newTestUsageInflightService(t *testing.T) (*UsageInflightService, *usageInflightStoreStub) {
	t.Helper()
	store := newUsageInflightStoreStub()
	repo := &usageInflightGroupRepoStub{names: map[int64]string{7: "主力分组", 9: "备用分组"}}
	return NewUsageInflightService(store, repo), store
}

func mustRegister(t *testing.T, store *usageInflightStoreStub, snapshot UsageInflightSnapshot) {
	t.Helper()
	if snapshot.StartedAt.IsZero() {
		snapshot.StartedAt = time.Now().UTC()
	}
	if err := store.Register(context.Background(), snapshot); err != nil {
		t.Fatalf("register %s: %v", snapshot.RequestID, err)
	}
}

// 登记后按 group 聚合出的 active_users 正确，且按 user 去重：
// 同一用户在同一分组有 3 个在途请求，人数必须是 1，不是 3。
func TestCountActiveUsersByGroupsDeduplicatesUsers(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()

	mustRegister(t, store, UsageInflightSnapshot{RequestID: "r1", UserID: 1, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "r2", UserID: 1, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "r3", UserID: 1, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "r4", UserID: 2, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "r5", UserID: 3, GroupID: 9})

	counts, err := svc.CountActiveUsersByGroups(ctx, []int64{7, 9, 11})
	if err != nil {
		t.Fatalf("count active users: %v", err)
	}
	if counts[7] != 2 {
		t.Fatalf("group 7 active users = %d, want 2 (two distinct users, five requests)", counts[7])
	}
	if counts[9] != 1 {
		t.Fatalf("group 9 active users = %d, want 1", counts[9])
	}
	if counts[11] != 0 {
		t.Fatalf("group 11 active users = %d, want 0", counts[11])
	}
}

// 注销后数字归零：最后一个在途请求结束时，该分组人数回到 0；
// 还有并发请求时只减少请求数，人数不变。
func TestActiveUsersDropToZeroAfterRelease(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()

	mustRegister(t, store, UsageInflightSnapshot{RequestID: "a", UserID: 42, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "b", UserID: 42, GroupID: 7})

	counts, err := svc.CountActiveUsersByGroups(ctx, []int64{7})
	if err != nil {
		t.Fatalf("count after register: %v", err)
	}
	if counts[7] != 1 {
		t.Fatalf("active users with two inflight requests = %d, want 1", counts[7])
	}

	if err := store.Release(ctx, 42, "a", 7); err != nil {
		t.Fatalf("release first: %v", err)
	}
	counts, err = svc.CountActiveUsersByGroups(ctx, []int64{7})
	if err != nil {
		t.Fatalf("count after first release: %v", err)
	}
	if counts[7] != 1 {
		t.Fatalf("active users with one inflight request left = %d, want 1", counts[7])
	}

	if err := store.Release(ctx, 42, "b", 7); err != nil {
		t.Fatalf("release second: %v", err)
	}
	counts, err = svc.CountActiveUsersByGroups(ctx, []int64{7})
	if err != nil {
		t.Fatalf("count after second release: %v", err)
	}
	if counts[7] != 0 {
		t.Fatalf("active users after all requests finished = %d, want 0", counts[7])
	}

	// 重复注销不得把计数减成负数。
	if err := store.Release(ctx, 42, "b", 7); err != nil {
		t.Fatalf("release twice: %v", err)
	}
	counts, err = svc.CountActiveUsersByGroups(ctx, []int64{7})
	if err != nil {
		t.Fatalf("count after duplicate release: %v", err)
	}
	if counts[7] != 0 {
		t.Fatalf("active users after duplicate release = %d, want 0", counts[7])
	}
}

// SummarizeUserInflight 按分组聚合单个用户的在途明细，并带回分组名与最早开始时间。
func TestSummarizeUserInflightGroupsByGroupID(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()

	// 用相对当前的时间：快照超过 UsageInflightMaxAge 会被判陈旧并过滤掉，
	// 写死的历史时间戳会让这个用例随真实日期推移而失败。
	earliest := time.Now().UTC().Add(-2 * time.Minute)
	later := earliest.Add(30 * time.Second)
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "x1", UserID: 5, GroupID: 7, Model: "claude-sonnet-4-5", StartedAt: later})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "x2", UserID: 5, GroupID: 7, Model: "claude-opus-4-1", StartedAt: earliest})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "x3", UserID: 5, GroupID: 7, Model: "claude-sonnet-4-5", StartedAt: later})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "x4", UserID: 5, GroupID: 9, Model: "gpt-5", StartedAt: later})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "x5", UserID: 5, GroupID: 0, Model: "gpt-5-mini", StartedAt: later})

	summaries, total, err := svc.SummarizeUserInflight(ctx, 5)
	if err != nil {
		t.Fatalf("summarize user inflight: %v", err)
	}
	if total != 5 {
		t.Fatalf("total inflight = %d, want 5", total)
	}
	if len(summaries) != 3 {
		t.Fatalf("group summaries = %d, want 3 (two groups plus ungrouped)", len(summaries))
	}
	// 按在途数降序：分组 7（3 条）在最前。
	first := summaries[0]
	if first.GroupID != 7 || first.Inflight != 3 {
		t.Fatalf("first summary = {group %d, inflight %d}, want {7, 3}", first.GroupID, first.Inflight)
	}
	if first.GroupName != "主力分组" {
		t.Fatalf("group name = %q, want 主力分组", first.GroupName)
	}
	if len(first.Models) != 2 || first.Models[0] != "claude-opus-4-1" || first.Models[1] != "claude-sonnet-4-5" {
		t.Fatalf("models = %v, want sorted unique [claude-opus-4-1 claude-sonnet-4-5]", first.Models)
	}
	if first.EarliestStartedAt == nil || !first.EarliestStartedAt.Equal(earliest) {
		t.Fatalf("earliest started at = %v, want %v", first.EarliestStartedAt, earliest)
	}
	// 未分组请求单独成行，GroupID=0、无分组名。
	var ungrouped *UserInflightGroupSummary
	for i := range summaries {
		if summaries[i].GroupID == 0 {
			ungrouped = &summaries[i]
		}
	}
	if ungrouped == nil {
		t.Fatal("missing ungrouped row for GroupID=0")
	}
	if ungrouped.GroupName != "" || ungrouped.Inflight != 1 {
		t.Fatalf("ungrouped row = %+v, want empty name and inflight 1", *ungrouped)
	}
}

// ListGroupActiveUsers 返回的是「人」而不是「请求」：一个用户多条请求只出现一次，
// 并带上该用户在此分组的在途数与模型集合。
func TestListGroupActiveUsersAggregatesPerUser(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()

	mustRegister(t, store, UsageInflightSnapshot{RequestID: "g1", UserID: 11, GroupID: 7, Username: "alice", Email: "alice@example.com", Model: "claude-sonnet-4-5"})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "g2", UserID: 11, GroupID: 7, Username: "alice", Email: "alice@example.com", Model: "claude-opus-4-1"})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "g3", UserID: 12, GroupID: 7, Username: "bob", Email: "bob@example.com", Model: "claude-sonnet-4-5"})
	// 其它分组的请求不得混进来。
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "g4", UserID: 13, GroupID: 9, Username: "carol", Email: "carol@example.com", Model: "gpt-5"})

	users, err := svc.ListGroupActiveUsers(ctx, 7)
	if err != nil {
		t.Fatalf("list group active users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("active users = %d, want 2", len(users))
	}
	// 按在途数降序：alice（2 条）在前。
	if users[0].UserID != 11 || users[0].InflightCount != 2 {
		t.Fatalf("first user = {id %d, inflight %d}, want {11, 2}", users[0].UserID, users[0].InflightCount)
	}
	if users[0].Username != "alice" || users[0].Email != "alice@example.com" {
		t.Fatalf("first user identity = %q/%q, want alice/alice@example.com", users[0].Username, users[0].Email)
	}
	if len(users[0].Models) != 2 || users[0].Models[0] != "claude-opus-4-1" || users[0].Models[1] != "claude-sonnet-4-5" {
		t.Fatalf("first user models = %v, want sorted unique two models", users[0].Models)
	}
	if users[1].UserID != 12 || users[1].InflightCount != 1 {
		t.Fatalf("second user = {id %d, inflight %d}, want {12, 1}", users[1].UserID, users[1].InflightCount)
	}
	for _, user := range users {
		if user.UserID == 13 {
			t.Fatal("user from another group leaked into the active list")
		}
	}
}

// active_users 与 sessions_used 是两条互不影响的路径。
//
// 在途登记服务缺失或读取失败时，active_users 保持 0，而 sessions_used 等
// 其它容量字段照常返回；反过来，在途请求存在也不会改变 sessions_used。
func TestActiveUsersIsIndependentFromSessions(t *testing.T) {
	ctx := context.Background()

	// 场景一：没有在途服务时，会话字段照常，active_users 为 0。
	withoutInflight := NewGroupCapacityService(nil, nil, nil, nil, nil)
	summaries := []GroupCapacitySummary{{GroupID: 7, SessionsUsed: 3, SessionsMax: 10}}
	withoutInflight.applyActiveUsers(ctx, summaries)
	if summaries[0].ActiveUsers != 0 {
		t.Fatalf("active users without inflight service = %d, want 0", summaries[0].ActiveUsers)
	}
	if summaries[0].SessionsUsed != 3 {
		t.Fatalf("sessions used = %d, want untouched 3", summaries[0].SessionsUsed)
	}

	// 场景二：在途服务正常，active_users 来自在途表，sessions_used 一字不改。
	inflightSvc, store := newTestUsageInflightService(t)
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "s1", UserID: 21, GroupID: 7})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "s2", UserID: 22, GroupID: 7})

	withInflight := NewGroupCapacityService(nil, nil, nil, nil, nil).WithUsageInflight(inflightSvc)
	summaries = []GroupCapacitySummary{{GroupID: 7, SessionsUsed: 3, SessionsMax: 10}}
	withInflight.applyActiveUsers(ctx, summaries)
	if summaries[0].ActiveUsers != 2 {
		t.Fatalf("active users = %d, want 2", summaries[0].ActiveUsers)
	}
	if summaries[0].SessionsUsed != 3 {
		t.Fatalf("sessions used = %d, want still 3 (active_users must not touch sessions)", summaries[0].SessionsUsed)
	}

	// 场景三：在途表读失败只让 active_users 退化为 0，不冒充会话数、不报错。
	brokenStore := newUsageInflightStoreStub()
	brokenStore.countErr = errors.New("redis unavailable")
	brokenSvc := NewUsageInflightService(brokenStore, nil)
	summaries = []GroupCapacitySummary{{GroupID: 7, SessionsUsed: 5}}
	NewGroupCapacityService(nil, nil, nil, nil, nil).WithUsageInflight(brokenSvc).applyActiveUsers(ctx, summaries)
	if summaries[0].ActiveUsers != 0 {
		t.Fatalf("active users on store error = %d, want 0", summaries[0].ActiveUsers)
	}
	if summaries[0].SessionsUsed != 5 {
		t.Fatalf("sessions used on inflight error = %d, want untouched 5", summaries[0].SessionsUsed)
	}
}

// TestUsageInflight_StaleSnapshotsAreNotCounted 钉死「并发数远大于真实并发」。
//
// 生产实测：用户桶里有 started_at 距今 6 小时以上的条目，key 每来一个新请求
// 就把整个 HASH 的 TTL 重置回满值，于是这些漏注销的条目永远等不到过期。
// 读取侧必须按年龄过滤，否则并发数只增不减。
func TestUsageInflight_StaleSnapshotsAreNotCounted(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// 两条新鲜的、三条陈旧的（其中一条没有时间戳 = 历史脏数据）。
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "fresh-1", UserID: 9, GroupID: 3, StartedAt: now.Add(-30 * time.Second)})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "fresh-2", UserID: 9, GroupID: 3, StartedAt: now.Add(-5 * time.Minute)})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "ghost-1", UserID: 9, GroupID: 3, StartedAt: now.Add(-2 * time.Hour)})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "ghost-2", UserID: 9, GroupID: 3, StartedAt: now.Add(-6 * time.Hour)})
	// 零值时间戳的历史脏数据：必须直接塞进 store，走 mustRegister 会被补成当前时间。
	require.NoError(t, store.Register(ctx, UsageInflightSnapshot{RequestID: "ghost-3", UserID: 9, GroupID: 3}))

	summaries, total, err := svc.SummarizeUserInflight(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, 2, total, "只应统计新鲜条目")
	require.Len(t, summaries, 1)
	require.Equal(t, 2, summaries[0].Inflight)
}

// TestUsageInflight_StaleSnapshotsExcludedFromGroupActiveUsers 分组实时使用人数
// 也不能被陈旧条目抬高。
func TestUsageInflight_StaleSnapshotsExcludedFromGroupActiveUsers(t *testing.T) {
	svc, store := newTestUsageInflightService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustRegister(t, store, UsageInflightSnapshot{RequestID: "a", UserID: 11, GroupID: 5, StartedAt: now.Add(-time.Minute)})
	mustRegister(t, store, UsageInflightSnapshot{RequestID: "b", UserID: 12, GroupID: 5, StartedAt: now.Add(-3 * time.Hour)})

	users, err := svc.ListGroupActiveUsers(ctx, 5)
	require.NoError(t, err)
	require.Len(t, users, 1, "只有新鲜条目对应的用户算活跃")
	require.Equal(t, int64(11), users[0].UserID)
}
