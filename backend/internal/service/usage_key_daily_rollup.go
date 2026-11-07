package service

import (
	"context"
	"time"
)

// UsageKeyDailyRollupRepository 维护 usage_key_daily（按 key 按本地日的用量汇总）。
//
// 这张表是 usage_logs 的派生数据：任何时刻都可以整表重建，删除它不会丢任何
// 计费事实。开关在 usage_key_daily_state.enabled，关掉后读侧完全回落到直接
// 扫 usage_logs 的原有路径。
type UsageKeyDailyRollupRepository interface {
	UsageKeyDailyReader
	// Sync 把「已关闭的自然日」增量发布进用法汇总表。
	Sync(ctx context.Context, now time.Time) error
	// BackfillAll 按本地日分片重建全部历史，返回处理的分片数。
	BackfillAll(ctx context.Context, chunkDays int) (int, error)
}

// UsageKeyDailyPoint 是某 key 某一天的汇总值。字段与 usage_key_daily 一一对应。
type UsageKeyDailyPoint struct {
	APIKeyID            int64
	Day                 time.Time
	Requests            int64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	TotalCost           float64
	ActualCost          float64
	DurationSum         int64
	DurationCount       int64
}

// UsageKeyDailyReader 是读侧能力。与写侧分开，读路径不依赖同步实现。
//
// 三个方法都显式接收 now：日界线取决于「今天是哪一天」（服务端配置时区的
// 自然日），由调用方传进来才能让测试固定时钟，也让读侧不必自己猜时钟。
type UsageKeyDailyReader interface {
	// RollupUsable 报告汇总表当前能否被信任（存在、已开启、时区口径一致、
	// 且有数据）。false 时调用方必须走原路径。
	RollupUsable(ctx context.Context) (bool, error)
	// DailyPoints 返回某 key 在 [start, end) 内的按日汇总。
	// 已关闭的日来自汇总表；今天由 usage_logs 现算。
	DailyPoints(ctx context.Context, apiKeyID int64, start, end, now time.Time) ([]UsageKeyDailyPoint, error)
	// TotalsForKeys 返回一批 key 在 [start, end) 内的合计。
	TotalsForKeys(ctx context.Context, apiKeyIDs []int64, start, end, now time.Time) (map[int64]UsageKeyDailyPoint, error)
}
