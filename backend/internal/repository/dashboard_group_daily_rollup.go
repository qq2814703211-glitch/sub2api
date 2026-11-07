package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 日汇总（分组 + 键）挂在 DashboardAggregationService 的调度上，而那个服务的
// s.repo 字段类型是 service.DashboardAggregationRepository。因此
// dashboardAggregationRepository 必须同时满足两个汇总接口，服务里的类型断言
// 才成立。真正干活的是各维度的 usageXxxDailyRollupRepository（与读侧共用同一份
// SQL 口径），这里只做转发，避免把同一段 SQL 写两遍。
//
// 为什么不在 dashboardAggregationRepository 里另写一遍 SQL：日桶口径只能有一处
// 定义。写两处就一定会有一天两边漂移，而这条链路上漂移的后果是计费与缓存率口径
// 不一致。
//
// 注意：UsageKeyDailyRollupRepository 与 UsageGroupDailyRollupRepository 的
// Sync/BackfillAll 签名完全相同，Go 不允许同一个类型上用两个同名方法区分实现，
// 所以这里用「显式命名」的两个转发方法承载维度差异 —— dashboardAggregationRepository
// 本身只实现分组维度（Sync 转发分组），键维度由 syncKeyUsageDailyViaRepo 显式调用，
// 见下面的 SyncKeyUsageDaily。
var (
	_ service.UsageGroupDailyRollupRepository = (*dashboardAggregationRepository)(nil)
)

// groupDailyRollup 惰性创建分组日汇总仓储，复用同一个 sqlExecutor。
func (r *dashboardAggregationRepository) groupDailyRollup() *usageGroupDailyRollupRepository {
	return &usageGroupDailyRollupRepository{sql: r.sql}
}

// keyDailyRollup 惰性创建键日汇总仓储，复用同一个 sqlExecutor。
func (r *dashboardAggregationRepository) keyDailyRollup() *usageKeyDailyRollupRepository {
	return &usageKeyDailyRollupRepository{sql: r.sql}
}

// Sync 把「今天以前」的日发布进 usage_group_daily。
func (r *dashboardAggregationRepository) Sync(ctx context.Context, now time.Time) error {
	return r.groupDailyRollup().Sync(ctx, now)
}

// BackfillAll 按本地日分片重建全部分组日汇总历史。
func (r *dashboardAggregationRepository) BackfillAll(ctx context.Context, chunkDays int) (int, error) {
	return r.groupDailyRollup().BackfillAll(ctx, chunkDays)
}

// SyncKeyUsageDaily 把「今天以前」的日发布进 usage_key_daily。
//
// 单独命名是因为它与 Sync（分组维度）签名相同，无法重载。服务侧对键维度
// 优先调用本方法，这样两个维度各自独立更新，一个失败不影响另一个。
func (r *dashboardAggregationRepository) SyncKeyUsageDaily(ctx context.Context, now time.Time) error {
	return r.keyDailyRollup().Sync(ctx, now)
}

// BackfillKeyUsageDaily 按本地日分片重建全部键日汇总历史。
func (r *dashboardAggregationRepository) BackfillKeyUsageDaily(ctx context.Context, chunkDays int) (int, error) {
	return r.keyDailyRollup().BackfillAll(ctx, chunkDays)
}
