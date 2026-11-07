package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 分组日汇总挂在 DashboardAggregationService 的调度上，所以 dashboardAggregationRepository
// 需要同时满足 service.UsageGroupDailyRollupRepository。真正干活的是
// usageGroupDailyRollupRepository（与 V3 读侧共用同一份 SQL 口径），这里只做转发，
// 避免把两份实现写两遍。
//
// 为什么不在 dashboardAggregationRepository 里另写一遍 SQL：日桶口径只能有一处定义。
// 写两处就一定会有一天两边漂移，而这条链路上漂移的后果是计费/缓存率口径不一致。

var _ service.UsageGroupDailyRollupRepository = (*dashboardAggregationRepository)(nil)

// groupDailyRollup 惰性创建分组日汇总仓储，复用同一个 sqlExecutor。
func (r *dashboardAggregationRepository) groupDailyRollup() *usageGroupDailyRollupRepository {
	return &usageGroupDailyRollupRepository{sql: r.sql}
}

// Sync 把「今天以前」的日发布进 usage_group_daily。
func (r *dashboardAggregationRepository) Sync(ctx context.Context, now time.Time) error {
	return r.groupDailyRollup().Sync(ctx, now)
}

// BackfillAll 按本地日分片重建全部分组日汇总历史。
func (r *dashboardAggregationRepository) BackfillAll(ctx context.Context, chunkDays int) (int, error) {
	return r.groupDailyRollup().BackfillAll(ctx, chunkDays)
}
