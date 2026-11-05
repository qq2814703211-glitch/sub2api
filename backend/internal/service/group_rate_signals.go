package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 分组提示的展示窗口。它们只是只读的展示信号，不参与计费或调度。
const (
	// GroupRateChangeWindow 基础倍率变化保持可见的时长。
	GroupRateChangeWindow = 48 * time.Hour
	// GroupNewWindow 新分组保持 NEW 标记的时长。
	GroupNewWindow = 24 * time.Hour

	// groups.rate_multiplier 与 group_rate_history 都存 4 位小数。
	groupRateEpsilon = 5e-5
	groupRateScale   = 1e4

	// 允许的时钟偏差。超过该值的「未来」时间视为异常数据，不据此
	// 断言新分组或调价，避免把导入/时区错误展示成刚刚发生的事实。
	groupSignalFutureSkew = 5 * time.Minute
)

// GroupRateChange 是 group_rate_history 的一行：数据库触发器在倍率修改的
// 同一事务内记录的真实变化。
type GroupRateChange struct {
	ID        int64
	GroupID   int64
	OldRate   float64
	NewRate   float64
	ChangedAt time.Time
}

// GroupRateHistoryReader 由分组仓储实现。使用可选能力接口，既有的
// GroupRepository 测试替身无需改动。
type GroupRateHistoryReader interface {
	// ListGroupRateChangesSince 返回给定分组在 changed_at >= since 的调价
	// 记录，按分组、时间、id 排序。
	ListGroupRateChangesSince(ctx context.Context, groupIDs []int64, since time.Time) ([]GroupRateChange, error)
}

// GroupRateDirection 表示基础倍率的变化方向。
type GroupRateDirection string

const (
	GroupRateUp   GroupRateDirection = "up"
	GroupRateDown GroupRateDirection = "down"
)

// GroupRecentRateChange 描述分组最近一次基础倍率调整。
type GroupRecentRateChange struct {
	Direction GroupRateDirection
	// Delta 是变化量的绝对值（不会为负），符号由 Direction 表达。
	Delta        float64
	PreviousRate float64
	ChangedAt    time.Time
}

// GroupSignals 是一个分组对某个查看者的可选展示提示。
type GroupSignals struct {
	// RecentRateChange 仅在基础倍率于 GroupRateChangeWindow 内变化，且该
	// 查看者确实按基础倍率计费时存在。
	RecentRateChange *GroupRecentRateChange
	// IsNew 表示分组创建时间在 GroupNewWindow 内。
	IsNew      bool
	LaunchedAt time.Time
	// LaunchRate 是分组上线时的基础倍率。查看者有专属倍率或历史不可读时
	// 不下发，避免把基础倍率误称为用户自己的价格。
	LaunchRate *float64
}

// IsZero 表示没有任何可展示内容。
func (s GroupSignals) IsZero() bool {
	return s.RecentRateChange == nil && !s.IsNew
}

// GroupSignalOptions 描述查看者的实际计费方式，确保提示不会声称用户
// 并未支付的价格。
type GroupSignalOptions struct {
	// PersonalRate 表示查看者对该分组有固定专属倍率（或无法确定）。
	// 此时不下发基础倍率提示：专属倍率不跟随基础倍率变化。
	PersonalRate bool
	// HistoryUnavailable 表示倍率历史读取失败。
	HistoryUnavailable bool
}

func roundGroupRate(v float64) float64 {
	return math.Round(v*groupRateScale) / groupRateScale
}

// ComputeGroupSignals 计算一个分组的展示提示。history 可以包含其他分组
// 的行，会被忽略。now 显式传入，便于测试边界。
//
// 规则：
//   - NEW：0 <= now - created_at < 24h。launch_rate 在有调价记录时取最早的
//     old_rate，否则取当前倍率；未来 created_at 视为异常，不显示 NEW。
//   - 调价：最近一条记录在 48h 内，且 new_rate 仍等于分组当前倍率
//     （否则记录已过期或与更新的修改竞争，什么都不声称）；差值四舍五入
//     到存储精度后为 0 时不显示箭头。
func ComputeGroupSignals(group *Group, history []GroupRateChange, opts GroupSignalOptions, now time.Time) GroupSignals {
	var out GroupSignals
	if group == nil {
		return out
	}
	rows := groupRateRows(group.ID, history)

	if !group.CreatedAt.IsZero() {
		age := now.Sub(group.CreatedAt)
		if age >= 0 && age < GroupNewWindow {
			out.IsNew = true
			out.LaunchedAt = group.CreatedAt
			if !opts.PersonalRate && !opts.HistoryUnavailable {
				rate := group.RateMultiplier
				if len(rows) > 0 {
					rate = rows[0].OldRate
				}
				out.LaunchRate = &rate
			}
		}
	}

	if opts.PersonalRate || opts.HistoryUnavailable || len(rows) == 0 {
		return out
	}
	last := rows[len(rows)-1]
	changedAgo := now.Sub(last.ChangedAt)
	if changedAgo >= GroupRateChangeWindow || changedAgo < -groupSignalFutureSkew {
		return out
	}
	if math.Abs(last.NewRate-group.RateMultiplier) > groupRateEpsilon {
		return out
	}
	delta := roundGroupRate(last.NewRate - last.OldRate)
	if delta == 0 {
		return out
	}
	direction := GroupRateUp
	if delta < 0 {
		direction = GroupRateDown
	}
	out.RecentRateChange = &GroupRecentRateChange{
		Direction:    direction,
		Delta:        math.Abs(delta),
		PreviousRate: last.OldRate,
		ChangedAt:    last.ChangedAt,
	}
	return out
}

// groupRateRows 保留一个分组的行并按时间先后排序。
func groupRateRows(groupID int64, history []GroupRateChange) []GroupRateChange {
	rows := make([]GroupRateChange, 0, len(history))
	for _, row := range history {
		if row.GroupID == groupID {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].ChangedAt.Equal(rows[j].ChangedAt) {
			return rows[i].ChangedAt.Before(rows[j].ChangedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

// GetGroupSignals 计算给定分组对某个查看者的展示提示。它从不让调用方
// 失败：专属倍率或历史读取失败时按 fail closed 扣留相应提示并记录日志。
// 没有任何提示的分组不会出现在结果中。
func (s *APIKeyService) GetGroupSignals(ctx context.Context, userID int64, groups []Group, now time.Time) map[int64]GroupSignals {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}

	personal, err := s.GetUserGroupRates(ctx, userID)
	personalUnknown := err != nil
	if personalUnknown {
		logger.LegacyPrintf("service.api_key", "[GroupSignals] load personal rates failed: user=%d err=%v", userID, err)
	}

	historyByGroup := make(map[int64][]GroupRateChange, len(groups))
	historyUnavailable := true
	if reader, ok := s.groupRepo.(GroupRateHistoryReader); ok {
		rows, readErr := reader.ListGroupRateChangesSince(ctx, ids, now.Add(-GroupRateChangeWindow))
		if readErr != nil {
			logger.LegacyPrintf("service.api_key", "[GroupSignals] load rate history failed: groups=%d err=%v", len(ids), readErr)
		} else {
			historyUnavailable = false
			for _, row := range rows {
				historyByGroup[row.GroupID] = append(historyByGroup[row.GroupID], row)
			}
		}
	}

	out := make(map[int64]GroupSignals, len(groups))
	for i := range groups {
		group := &groups[i]
		_, hasPersonal := personal[group.ID]
		signals := ComputeGroupSignals(group, historyByGroup[group.ID], GroupSignalOptions{
			PersonalRate:       personalUnknown || hasPersonal,
			HistoryUnavailable: historyUnavailable,
		}, now)
		if !signals.IsZero() {
			out[group.ID] = signals
		}
	}
	return out
}
