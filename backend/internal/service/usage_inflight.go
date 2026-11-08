package service

import (
	"context"
	"sort"
	"strings"
	"time"
)

// UsageInflightSnapshot 是一条在途请求的请求级快照。
//
// 与 ConcurrencyCache（只有计数）和 BillingCache 的在途金额（只有钱）不同，
// 这里保留 model / group / started_at，使管理面能回答「谁正在用、用哪个分组、
// 用什么模型、从什么时候开始」。
type UsageInflightSnapshot struct {
	RequestID  string    `json:"request_id"`
	UserID     int64     `json:"user_id"`
	APIKeyID   int64     `json:"api_key_id,omitempty"`
	APIKeyName string    `json:"api_key_name,omitempty"`
	GroupID    int64     `json:"group_id,omitempty"`
	Email      string    `json:"email,omitempty"`
	Username   string    `json:"username,omitempty"`
	Model      string    `json:"model,omitempty"`
	Stream     bool      `json:"stream,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// UsageInflightStore 是在途请求登记表的存储能力。
//
// 所有写方法都必须是 best-effort 语义：调用方（网关中间件）失败时只记日志，
// 不得因为 Redis 故障阻断业务请求。
type UsageInflightStore interface {
	// Register 登记一条在途请求，并累加其分组内的用户引用计数。
	Register(ctx context.Context, snapshot UsageInflightSnapshot) error
	// UpdateModel 补写模型名；快照已注销时不重建。
	UpdateModel(ctx context.Context, userID int64, requestID, model string, stream bool) error
	// Release 注销在途请求；只有快照确实存在过才递减分组引用计数。
	Release(ctx context.Context, userID int64, requestID string, groupID int64) error
	// ListByUser 读取某个用户的全部在途快照。
	ListByUser(ctx context.Context, userID int64) ([]UsageInflightSnapshot, error)
	// CountByUsers 批量返回一批用户此刻的在途请求数。
	// 口径必须与 ListByUser 一致（只计可解析的快照），列表与详情才能得到同一个数。
	CountByUsers(ctx context.Context, userIDs []int64) (map[int64]int, error)
	// ListByGroup 读取某个分组内全部在途快照（跨用户）。
	ListByGroup(ctx context.Context, groupID int64) ([]UsageInflightSnapshot, error)
	// CountActiveUsersByGroups 批量返回分组当前实时使用中的人数（COUNT(DISTINCT user_id)）。
	CountActiveUsersByGroups(ctx context.Context, groupIDs []int64) (map[int64]int, error)
}

// UserInflightGroupSummary 是「该用户在某个分组里的在途明细」。
type UserInflightGroupSummary struct {
	GroupID           int64      `json:"group_id"`
	GroupName         string     `json:"group_name"`
	Inflight          int        `json:"inflight"`
	Models            []string   `json:"models"`
	EarliestStartedAt *time.Time `json:"earliest_started_at"`
}

// GroupActiveUser 是分组「当前实时使用中的用户」名单条目。
// 与会话（sessions_used）语义无关：这里只看此刻是否有请求正在处理。
type GroupActiveUser struct {
	UserID        int64    `json:"user_id"`
	Username      string   `json:"username"`
	Email         string   `json:"email"`
	InflightCount int      `json:"inflight_count"`
	Models        []string `json:"models"`
}

// UsageInflightService 聚合在途登记表，供管理面读取。
type UsageInflightService struct {
	store     UsageInflightStore
	groupRepo GroupRepository
}

// NewUsageInflightService 创建在途请求聚合服务。
func NewUsageInflightService(store UsageInflightStore, groupRepo GroupRepository) *UsageInflightService {
	return &UsageInflightService{store: store, groupRepo: groupRepo}
}

// SummarizeUserInflight 按分组聚合该用户的在途请求。
// 结果按在途数降序、分组 ID 升序排列，保证管理面每次刷新顺序稳定。
func (s *UsageInflightService) SummarizeUserInflight(ctx context.Context, userID int64) ([]UserInflightGroupSummary, int, error) {
	if s == nil || s.store == nil || userID <= 0 {
		return []UserInflightGroupSummary{}, 0, nil
	}
	snapshots, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	summaries := summarizeUsageInflightByGroup(snapshots)
	names := s.groupNames(ctx, summaries)
	for i := range summaries {
		summaries[i].GroupName = names[summaries[i].GroupID]
	}
	return summaries, len(snapshots), nil
}

// CountUserInflightBatch 批量返回一批用户此刻的在途请求数。
//
// 列表页原先展示的是 Redis 并发槽位数（concurrency:api_key:<id> 的 ZCARD），
// 而详情弹层展示的是在途请求数（usage:inflight:<uid> 的 HLEN），两者不同源，
// 同一个用户在列表显示 39、点开详情却是 91。这里把列表也切到在途登记表，
// 与 SummarizeUserInflight 的 len(snapshots) 严格同源，两个数才能对上。
func (s *UsageInflightService) CountUserInflightBatch(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	if s == nil || s.store == nil || len(userIDs) == 0 {
		return map[int64]int{}, nil
	}
	return s.store.CountByUsers(ctx, userIDs)
}

// ListGroupActiveUsers 返回某分组此刻正在处理请求的用户名单。
func (s *UsageInflightService) ListGroupActiveUsers(ctx context.Context, groupID int64) ([]GroupActiveUser, error) {
	if s == nil || s.store == nil || groupID <= 0 {
		return []GroupActiveUser{}, nil
	}
	snapshots, err := s.store.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return aggregateGroupActiveUsers(groupID, snapshots), nil
}

// CountActiveUsersByGroups 返回分组当前实时使用中的人数。
// 这是分组容量里 active_users 的唯一数据源，与会话数（sessions）完全独立。
func (s *UsageInflightService) CountActiveUsersByGroups(ctx context.Context, groupIDs []int64) (map[int64]int, error) {
	if s == nil || s.store == nil || len(groupIDs) == 0 {
		return map[int64]int{}, nil
	}
	return s.store.CountActiveUsersByGroups(ctx, groupIDs)
}

// groupNames 批量解析分组名；解析失败只留空名，不让管理面整体失败。
func (s *UsageInflightService) groupNames(ctx context.Context, summaries []UserInflightGroupSummary) map[int64]string {
	names := make(map[int64]string, len(summaries))
	if s.groupRepo == nil {
		return names
	}
	for _, summary := range summaries {
		if summary.GroupID <= 0 {
			continue
		}
		group, err := s.groupRepo.GetByIDLite(ctx, summary.GroupID)
		if err != nil || group == nil {
			continue
		}
		names[summary.GroupID] = group.Name
	}
	return names
}

// summarizeUsageInflightByGroup 按 group_id 聚合快照：计数、去重模型、最早开始时间。
// 未绑定分组（GroupID<=0）的请求归入 GroupID 0 一行，前端显示为「未分组」。
func summarizeUsageInflightByGroup(snapshots []UsageInflightSnapshot) []UserInflightGroupSummary {
	type bucket struct {
		count   int
		models  map[string]struct{}
		earlies time.Time
	}
	buckets := make(map[int64]*bucket, len(snapshots))
	for _, snapshot := range snapshots {
		item, ok := buckets[snapshot.GroupID]
		if !ok {
			item = &bucket{models: make(map[string]struct{})}
			buckets[snapshot.GroupID] = item
		}
		item.count++
		if model := strings.TrimSpace(snapshot.Model); model != "" {
			item.models[model] = struct{}{}
		}
		if !snapshot.StartedAt.IsZero() && (item.earlies.IsZero() || snapshot.StartedAt.Before(item.earlies)) {
			item.earlies = snapshot.StartedAt
		}
	}

	summaries := make([]UserInflightGroupSummary, 0, len(buckets))
	for groupID, item := range buckets {
		summary := UserInflightGroupSummary{
			GroupID:  groupID,
			Inflight: item.count,
			Models:   sortedModelSet(item.models),
		}
		if !item.earlies.IsZero() {
			earliest := item.earlies
			summary.EarliestStartedAt = &earliest
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Inflight != summaries[j].Inflight {
			return summaries[i].Inflight > summaries[j].Inflight
		}
		return summaries[i].GroupID < summaries[j].GroupID
	})
	return summaries
}

// aggregateGroupActiveUsers 把分组内的在途快照按用户合并，人数天然去重。
func aggregateGroupActiveUsers(groupID int64, snapshots []UsageInflightSnapshot) []GroupActiveUser {
	type bucket struct {
		user  GroupActiveUser
		model map[string]struct{}
	}
	buckets := make(map[int64]*bucket, len(snapshots))
	order := make([]int64, 0, len(snapshots))
	for _, snapshot := range snapshots {
		// 分组可能在请求过程中被改写，只统计确实属于该分组的快照。
		if snapshot.GroupID != groupID {
			continue
		}
		item, ok := buckets[snapshot.UserID]
		if !ok {
			item = &bucket{
				user:  GroupActiveUser{UserID: snapshot.UserID, Username: snapshot.Username, Email: snapshot.Email},
				model: make(map[string]struct{}),
			}
			buckets[snapshot.UserID] = item
			order = append(order, snapshot.UserID)
		}
		item.user.InflightCount++
		if model := strings.TrimSpace(snapshot.Model); model != "" {
			item.model[model] = struct{}{}
		}
	}

	users := make([]GroupActiveUser, 0, len(buckets))
	for _, userID := range order {
		item := buckets[userID]
		item.user.Models = sortedModelSet(item.model)
		users = append(users, item.user)
	}
	sort.SliceStable(users, func(i, j int) bool {
		if users[i].InflightCount != users[j].InflightCount {
			return users[i].InflightCount > users[j].InflightCount
		}
		return users[i].UserID < users[j].UserID
	})
	// 名字缺失时回落到邮箱，避免管理面出现无标识的行。
	for i := range users {
		if strings.TrimSpace(users[i].Username) == "" {
			users[i].Username = users[i].Email
		}
	}
	return users
}

func sortedModelSet(models map[string]struct{}) []string {
	if len(models) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
