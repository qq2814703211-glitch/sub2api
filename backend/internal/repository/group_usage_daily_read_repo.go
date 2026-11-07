package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 编译期断言：写侧与读侧能力都在同一个实现上。
var (
	_ service.UsageGroupDailyRollupRepository = (*usageGroupDailyRollupRepository)(nil)
	_ service.UsageGroupDailyReader           = (*usageGroupDailyRollupRepository)(nil)
)

// RollupUsable 报告 usage_group_daily 现在能否被信任。
//
// 四个条件同时成立才算可用：表里有行（回填跑过）、开关打开、state 里的时区
// 与当前配置一致（否则历史桶是另一套日历）、水位没有跑到未来。
func (r *usageGroupDailyRollupRepository) RollupUsable(ctx context.Context) (bool, error) {
	if r == nil || r.sql == nil {
		return false, nil
	}
	timezoneName := service.GroupUsageTimezoneName()
	todayStart := service.GroupUsageTodayStart(time.Now())
	todayDate := service.GroupUsageDate(todayStart)

	state, err := r.readState(ctx, timezoneName, todayDate)
	if err != nil {
		return false, err
	}
	if !state.enabled || !state.valid {
		return false, nil
	}

	var hasRows bool
	if err := scanSingleRow(ctx, r.sql,
		`SELECT EXISTS (SELECT 1 FROM usage_group_daily)`, nil, &hasRows); err != nil {
		return false, fmt.Errorf("检查分组日汇总是否有数据: %w", err)
	}
	return hasRows, nil
}

// CacheRates 返回各分组在最近 windowDays 天真实流量的缓存率。
//
// 已关闭的日来自汇总表，今天由 usage_logs 现算 —— 与 key 维度的读路径同一套
// 思路：汇总表只承担稳定历史，增长中的今天不写入，避免返回不完整的日桶。
//
// 探针请求本身被 usageLogProbeExcludeUL 排除，不会污染均值（ADR 0009）。
func (r *usageGroupDailyRollupRepository) CacheRates(
	ctx context.Context,
	groupIDs []int64,
	windowDays int,
	now time.Time,
) (map[int64]float64, error) {
	out := map[int64]float64{}
	if r == nil || r.sql == nil || len(groupIDs) == 0 {
		return out, nil
	}
	if windowDays <= 0 {
		windowDays = service.ChannelMonitorV3DefaultCacheAverageWindowDays
	}

	timezoneName := service.GroupUsageTimezoneName()
	todayStart := service.GroupUsageTodayStart(now)
	windowStart := todayStart.AddDate(0, 0, -windowDays)

	// 已关闭的日：直接从汇总表读。窗口起点可能落在窗口外，用 day 下界裁掉。
	const closedSQL = `
		SELECT group_id,
		       COALESCE(SUM(cache_read_tokens), 0)::float8 AS cache_read,
		       COALESCE(SUM(input_tokens + cache_creation_tokens + cache_read_tokens), 0)::float8 AS total_in
		FROM usage_group_daily
		WHERE group_id = ANY($1)
		  AND day >= $2::date
		  AND day < $3::date
		GROUP BY group_id
	`
	rows, err := r.sql.QueryContext(ctx, closedSQL,
		pq.Array(groupIDs), service.GroupUsageDate(windowStart), service.GroupUsageDate(todayStart))
	if err != nil {
		return nil, fmt.Errorf("读取分组日汇总缓存率: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type sums struct{ cacheRead, totalIn float64 }
	acc := map[int64]*sums{}
	for rows.Next() {
		var groupID int64
		var cacheRead, totalIn float64
		if err := rows.Scan(&groupID, &cacheRead, &totalIn); err != nil {
			return nil, fmt.Errorf("扫描分组日汇总缓存率: %w", err)
		}
		acc[groupID] = &sums{cacheRead: cacheRead, totalIn: totalIn}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 今天：还在增长，现算。时间窗是 [todayStart, now)，与汇总表的日历口径一致。
	const todaySQL = `
		SELECT ul.group_id,
		       COALESCE(SUM(ul.cache_read_tokens), 0)::float8,
		       COALESCE(SUM(ul.input_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0)::float8
		FROM usage_logs ul
		WHERE ul.group_id = ANY($1)
		  AND ul.created_at >= $2
		  AND ul.created_at <= $3
		  AND ` + usageLogProbeExcludeUL + `
		GROUP BY ul.group_id
	`
	todayRows, err := r.sql.QueryContext(ctx, todaySQL, pq.Array(groupIDs), todayStart.UTC(), now.UTC())
	if err != nil {
		return nil, fmt.Errorf("读取分组今日缓存率: %w", err)
	}
	defer func() { _ = todayRows.Close() }()
	for todayRows.Next() {
		var groupID int64
		var cacheRead, totalIn float64
		if err := todayRows.Scan(&groupID, &cacheRead, &totalIn); err != nil {
			return nil, fmt.Errorf("扫描分组今日缓存率: %w", err)
		}
		if existing, ok := acc[groupID]; ok {
			existing.cacheRead += cacheRead
			existing.totalIn += totalIn
		} else {
			acc[groupID] = &sums{cacheRead: cacheRead, totalIn: totalIn}
		}
	}
	if err := todayRows.Err(); err != nil {
		return nil, err
	}

	// 分母为 0 的分组不出现 —— 调用方据此标「未知」，而不是编一个 0%。
	for groupID, s := range acc {
		if s.totalIn <= 0 {
			continue
		}
		rate := s.cacheRead / s.totalIn
		if rate < 0 {
			rate = 0
		} else if rate > 1 {
			rate = 1
		}
		out[groupID] = rate
	}
	_ = timezoneName // 时区口径已体现在 day 桶里；保留变量便于日后加诊断
	_ = sql.ErrNoRows
	return out, nil
}
