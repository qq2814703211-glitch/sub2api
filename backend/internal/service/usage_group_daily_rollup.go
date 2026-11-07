package service

import (
	"context"
	"time"
)

// UsageGroupDailyRollupRepository 维护 usage_group_daily（按分组按本地日的用量汇总）。
//
// 这张表是 usage_logs 的派生数据：任何时刻都可以整表重建，删除它不会丢任何
// 计费事实。开关在 usage_group_daily_state.enabled，关掉后读侧完全回落到直接
// 扫 usage_logs 的原有路径。
//
// 语义与 UsageKeyDailyRollupRepository 逐条对齐（时区写死、整日重算、只发布
// 已关闭日），避免两个维度各自演化出不同的日界线口径。
type UsageGroupDailyRollupRepository interface {
	// Sync 把「已关闭的自然日」增量发布进分组日汇总表。
	Sync(ctx context.Context, now time.Time) error
	// BackfillAll 按本地日分片重建全部历史，返回处理的分片数。
	BackfillAll(ctx context.Context, chunkDays int) (int, error)
}

// UsageGroupDailyReader 是读侧能力。与写侧分开，读路径不依赖同步实现。
type UsageGroupDailyReader interface {
	// RollupUsable 报告汇总表当前能否被信任（存在、已开启、时区口径一致、
	// 且有数据）。false 时调用方必须走原路径。
	RollupUsable(ctx context.Context) (bool, error)
	// CacheRates 返回各分组在最近 windowDays 天真实流量的缓存率。
	//
	// 这是 ADR 0009 的实现：探针桶的缓存率来自真实流量，不是探针自己。
	// 没有任何真实 token 的分组不会出现在结果里，调用方据此标未知。
	// 今天这一段由 usage_logs 现算，避免返回不完整的日桶。
	CacheRates(ctx context.Context, groupIDs []int64, windowDays int, now time.Time) (map[int64]float64, error)
}
