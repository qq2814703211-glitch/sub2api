package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// keyUsageDay 是 service.UsageKeyDailyPoint 的日期字段：汇总表的 day 是 DATE，
// 扫描出来是 time.Time（UTC 零点）。放在这里而不是 service 包，是因为 service
// 层不需要关心 SQL 的 DATE 表示。

// 读侧：已关闭的日走 usage_key_daily，今天（还没关闭的日）走 usage_logs 补算。
//
// 两段的切点作为**查询参数**传进 SQL，不在 SQL 里由子查询现算：现算出来的
// created_at 下界对 planner 是运行期值，既进不了 index cond 也估不准选择性。
// 同类问题在 usage_group_daily_rollups 上实测过（全表扫 48.7s vs 参数化 34ms）。

// keyDailyRollupCoversThrough 返回该时刻之前（不含）的日可以由汇总表服务。
//
// 汇总表只发布「已关闭的自然日」，所以上界就是本地今日零点。
func keyDailyRollupCoversThrough(now time.Time) time.Time {
	return service.GroupUsageTodayStart(now)
}

// rollupStateUsable 判断汇总表当前能否被信任。
//
// 三种情况一律不可用，读侧必须回落原路径：
//   - 表或 state 行不存在（迁移刚跑完、还没 backfill）
//   - enabled = false（运维手动关闭）
//   - 记录的时区口径与当前配置不一致（历史行是另一个日历，混读会算错钱）
func (r *usageKeyDailyRollupRepository) rollupStateUsable(ctx context.Context, timezoneName string) (bool, error) {
	var rowCount int
	var enabled sql.NullBool
	var stateTimezone sql.NullString
	var hasRows bool
	if err := scanSingleRow(ctx, r.sql, `
		SELECT COUNT(*), MAX(s.enabled::int) = 1, MAX(s.timezone_name),
		       EXISTS(SELECT 1 FROM usage_key_daily)
		FROM usage_key_daily_state s
		WHERE s.id = 1
	`, nil, &rowCount, &enabled, &stateTimezone, &hasRows); err != nil {
		return false, err
	}
	if rowCount != 1 || !enabled.Valid || !enabled.Bool || !hasRows {
		return false, nil
	}
	if !stateTimezone.Valid || stateTimezone.String != timezoneName {
		return false, nil
	}
	return true, nil
}

// RollupUsable 报告汇总表是否可服务读请求。
func (r *usageKeyDailyRollupRepository) RollupUsable(ctx context.Context) (bool, error) {
	if r == nil || r.sql == nil {
		return false, nil
	}
	return r.rollupStateUsable(ctx, service.GroupUsageTimezoneName())
}

// DailyPoints 返回某 key 在 [startDay, endDay) 内的按日汇总。
//
// 返回的每一天都等于直接扫 usage_logs 同一天的分组结果：已关闭日来自汇总表，
// 今天这一日由 usage_logs 现算（它还没关闭，汇总表里没有）。
func (r *usageKeyDailyRollupRepository) DailyPoints(
	ctx context.Context, apiKeyID int64, start, end time.Time, now time.Time,
) ([]service.UsageKeyDailyPoint, error) {
	if r == nil || r.sql == nil || apiKeyID <= 0 || !end.After(start) {
		return nil, nil
	}
	timezoneName := service.GroupUsageTimezoneName()
	closedThrough := keyDailyRollupCoversThrough(now)

	// tailStart 是「汇总表已经不覆盖」的那一瞬。若查询窗口整体在它之前，
	// 就没有 tail，参数取 end 本身让 tail 分支自然为空。
	tailStart := start
	if closedThrough.After(tailStart) {
		tailStart = closedThrough
	}
	rollupEnd := end
	if closedThrough.Before(rollupEnd) {
		rollupEnd = closedThrough
	}

	rows, err := r.sql.QueryContext(ctx, keyDailyPointsSQL,
		apiKeyID, start, end, rollupEnd, tailStart, timezoneName)
	if err != nil {
		return nil, fmt.Errorf("读取键日汇总: %w", err)
	}
	defer func() { _ = rows.Close() }()

	points := make([]service.UsageKeyDailyPoint, 0, 32)
	for rows.Next() {
		var p service.UsageKeyDailyPoint
		p.APIKeyID = apiKeyID
		if err := rows.Scan(
			&p.Day, &p.Requests,
			&p.InputTokens, &p.OutputTokens,
			&p.CacheCreationTokens, &p.CacheReadTokens,
			&p.TotalCost, &p.ActualCost,
			&p.DurationSum, &p.DurationCount,
		); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

// keyDailyPointsSQL 把「已关闭日」和「今天」拼成一个按日序列。
//
// 参数：$1 api_key_id, $2 窗口起点, $3 窗口终点, $4 汇总表上界（本地今日零点）,
// $5 tail 起点, $6 时区名。
//
// 日桶口径与写入侧逐字相同：(created_at AT TIME ZONE $6)::date。
const keyDailyPointsSQL = `
WITH rollup_part AS (
    SELECT day, requests, input_tokens, output_tokens,
           cache_creation_tokens, cache_read_tokens,
           total_cost, actual_cost, duration_sum, duration_count
    FROM usage_key_daily
    WHERE api_key_id = $1
      AND day >= ($2 AT TIME ZONE $6::text)::date
      AND day <  ($4 AT TIME ZONE $6::text)::date
),
tail_part AS (
    SELECT (ul.created_at AT TIME ZONE $6::text)::date AS day,
           COUNT(*) AS requests,
           COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
           COALESCE(SUM(ul.output_tokens), 0) AS output_tokens,
           COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(ul.total_cost), 0) AS total_cost,
           COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
           COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0) AS duration_sum,
           COUNT(*) AS duration_count
    FROM usage_logs ul
    WHERE ul.api_key_id = $1
      AND ul.created_at >= $5
      AND ul.created_at < $3
    GROUP BY 1
)
SELECT day, SUM(requests), SUM(input_tokens), SUM(output_tokens),
       SUM(cache_creation_tokens), SUM(cache_read_tokens),
       SUM(total_cost), SUM(actual_cost), SUM(duration_sum), SUM(duration_count)
FROM (
    SELECT * FROM rollup_part
    UNION ALL
    SELECT * FROM tail_part
) merged
GROUP BY day
ORDER BY day
`

// TotalsForKeys 返回一批 key 在 [startDay, endDay) 的合计。
//
// 「今天」这一段永远由 usage_logs 现算，所以调用方不需要区分窗口是否包含今天。
func (r *usageKeyDailyRollupRepository) TotalsForKeys(
	ctx context.Context, apiKeyIDs []int64, start, end time.Time, now time.Time,
) (map[int64]service.UsageKeyDailyPoint, error) {
	out := make(map[int64]service.UsageKeyDailyPoint, len(apiKeyIDs))
	if r == nil || r.sql == nil || len(apiKeyIDs) == 0 || !end.After(start) {
		return out, nil
	}
	timezoneName := service.GroupUsageTimezoneName()
	closedThrough := keyDailyRollupCoversThrough(now)

	tailStart := start
	if closedThrough.After(tailStart) {
		tailStart = closedThrough
	}
	rollupEnd := end
	if closedThrough.Before(rollupEnd) {
		rollupEnd = closedThrough
	}

	rows, err := r.sql.QueryContext(ctx, keyDailyTotalsSQL,
		pq.Array(apiKeyIDs), start, end, rollupEnd, tailStart, timezoneName)
	if err != nil {
		return nil, fmt.Errorf("读取键日汇总合计: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int64
		var p service.UsageKeyDailyPoint
		if err := rows.Scan(
			&id, &p.Requests,
			&p.InputTokens, &p.OutputTokens,
			&p.CacheCreationTokens, &p.CacheReadTokens,
			&p.TotalCost, &p.ActualCost,
			&p.DurationSum, &p.DurationCount,
		); err != nil {
			return nil, err
		}
		p.APIKeyID = id
		out[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// keyDailyTotalsSQL 把汇总表分片与今天的 tail 分片合计成一批 key 的汇总。
const keyDailyTotalsSQL = `
WITH rollup_part AS (
    SELECT api_key_id, requests, input_tokens, output_tokens,
           cache_creation_tokens, cache_read_tokens,
           total_cost, actual_cost, duration_sum, duration_count
    FROM usage_key_daily
    WHERE api_key_id = ANY($1)
      AND day >= ($2 AT TIME ZONE $6::text)::date
      AND day <  ($4 AT TIME ZONE $6::text)::date
),
tail_part AS (
    SELECT ul.api_key_id,
           COUNT(*) AS requests,
           COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
           COALESCE(SUM(ul.output_tokens), 0) AS output_tokens,
           COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(ul.total_cost), 0) AS total_cost,
           COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
           COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0) AS duration_sum,
           COUNT(*) AS duration_count
    FROM usage_logs ul
    WHERE ul.api_key_id = ANY($1)
      AND ul.created_at >= $5
      AND ul.created_at < $3
    GROUP BY ul.api_key_id
)
SELECT api_key_id, SUM(requests), SUM(input_tokens), SUM(output_tokens),
       SUM(cache_creation_tokens), SUM(cache_read_tokens),
       SUM(total_cost), SUM(actual_cost), SUM(duration_sum), SUM(duration_count)
FROM (
    SELECT * FROM rollup_part
    UNION ALL
    SELECT * FROM tail_part
) merged
GROUP BY api_key_id
`

var _ service.UsageKeyDailyRollupRepository = (*usageKeyDailyRollupRepository)(nil)
