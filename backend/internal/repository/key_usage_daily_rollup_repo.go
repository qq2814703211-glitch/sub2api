package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// usageKeyDailyRollupRepository 维护 usage_key_daily 这张派生日汇总表。
//
// 设计要点（与 usage_group_daily_rollups 保持同一套语义，避免两处口径分叉）：
//
//  1. 日桶 = 服务端配置时区内的自然日，SQL 里显式写时区名，不依赖 session
//     默认值。PG 的 timezone GUC 一旦被改成 UTC，`(created_at)::date` 会让
//     上海时间 00:00-08:00 的日志落到前一天；本地实测 1755 万行里有 585 万
//     行（33.4%）在 UTC 与 Asia/Shanghai 下落入不同日期。
//  2. 整日重算，不做累加。同一天可能有迟到日志（重试、补录），累加会重复
//     计费；DELETE+INSERT 或 ON CONFLICT DO UPDATE 覆盖才是幂等的。
//  3. 只发布「已关闭」的日（day < 本地今日）。今天还在增长，读侧用
//     usage_logs 补这一段，避免把不完整的日桶当成最终值返回给用户。
type usageKeyDailyRollupRepository struct {
	sql sqlExecutor
}

// NewUsageKeyDailyRollupRepository 创建键日汇总仓储。
func NewUsageKeyDailyRollupRepository(db *sql.DB) service.UsageKeyDailyRollupRepository {
	if db == nil {
		return nil
	}
	return &usageKeyDailyRollupRepository{sql: db}
}

func newUsageKeyDailyRollupRepositoryWithSQL(sqlq sqlExecutor) *usageKeyDailyRollupRepository {
	return &usageKeyDailyRollupRepository{sql: sqlq}
}

// keyDailyRollupState 是 usage_key_daily_state 的一次快照。
type keyDailyRollupState struct {
	valid        bool
	enabled      bool
	closedBefore string // date，已关闭日的右开边界
	timezoneName string
}

// readState 读水位。时区不一致或水位在未来一律按「无效」处理：宁可全量重算，
// 也不能把两份不同日历口径的日桶混在一起。
func (r *usageKeyDailyRollupRepository) readState(ctx context.Context, timezoneName, todayDate string) (keyDailyRollupState, error) {
	var rowCount int
	var closedBefore sql.NullString
	var stateTimezone sql.NullString
	var enabled sql.NullBool
	if err := scanSingleRow(ctx, r.sql, `
		SELECT COUNT(*), MAX(closed_before)::text, MAX(timezone_name), MAX(enabled)
		FROM usage_key_daily_state
		WHERE id = 1
	`, nil, &rowCount, &closedBefore, &stateTimezone, &enabled); err != nil {
		return keyDailyRollupState{}, fmt.Errorf("读取键日汇总水位: %w", err)
	}
	state := keyDailyRollupState{
		closedBefore: "1970-01-01",
		timezoneName: timezoneName,
	}
	if !enabled.Valid || !enabled.Bool {
		return state, nil
	}
	state.enabled = true
	if rowCount != 1 || !closedBefore.Valid || !stateTimezone.Valid ||
		stateTimezone.String != timezoneName || closedBefore.String > todayDate {
		return state, nil
	}
	state.valid = true
	state.closedBefore = closedBefore.String
	return state, nil
}

// Sync 把今日以前的用量发布进 usage_key_daily。
//
// 每次只重算 [closedBefore, todayStart) 这一段：已关闭的日不重复扫。水位前进
// 到今日零点，因此稳态下每次只处理昨天一天。
func (r *usageKeyDailyRollupRepository) Sync(ctx context.Context, now time.Time) error {
	if r == nil || r.sql == nil {
		return nil
	}
	todayStart := service.GroupUsageTodayStart(now)
	todayDate := service.GroupUsageDate(todayStart)
	timezoneName := service.GroupUsageTimezoneName()

	state, err := r.readState(ctx, timezoneName, todayDate)
	if err != nil {
		return err
	}
	if !state.enabled || !state.valid {
		return nil
	}
	if state.closedBefore == todayDate {
		return nil
	}

	rebuildStart, err := service.ParseGroupUsageDate(state.closedBefore)
	if err != nil {
		return fmt.Errorf("解析键日汇总水位 %q: %w", state.closedBefore, err)
	}

	// 该日之前的桶全部作废重算。带 created_at 下界，避免无谓的全表扫描。
	if _, err := r.sql.ExecContext(ctx, `
		DELETE FROM usage_key_daily
		WHERE day >= $1::date AND day < $2::date
	`, state.closedBefore, todayDate); err != nil {
		return fmt.Errorf("清理键日汇总日桶: %w", err)
	}

	if _, err := r.sql.ExecContext(ctx, keyUsageDailyUpsertSQL,
		rebuildStart.UTC(), todayStart.UTC(), timezoneName); err != nil {
		return fmt.Errorf("重建键日汇总日桶: %w", err)
	}

	if _, err := r.sql.ExecContext(ctx, `
		UPDATE usage_key_daily_state
		SET closed_before = $1::date, timezone_name = $2, updated_at = now()
		WHERE id = 1
	`, todayDate, timezoneName); err != nil {
		return fmt.Errorf("更新键日汇总水位: %w", err)
	}
	return nil
}

// keyUsageDailyUpsertSQL 是唯一一处定义日桶口径的 SQL。
//
// 查询侧与回填侧都复用它，保证「回填的日」与「增量重算的日」逐行同口径。
// 参数：$1 窗口起点, $2 窗口终点（右开）, $3 时区名。
const keyUsageDailyUpsertSQL = `
INSERT INTO usage_key_daily (
    api_key_id, day, requests, input_tokens, output_tokens,
    cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
    duration_sum, duration_count, computed_at
)
SELECT
    ul.api_key_id,
    (ul.created_at AT TIME ZONE $3::text)::date AS day,
    COUNT(*),
    COALESCE(SUM(ul.input_tokens), 0),
    COALESCE(SUM(ul.output_tokens), 0),
    COALESCE(SUM(ul.cache_creation_tokens), 0),
    COALESCE(SUM(ul.cache_read_tokens), 0),
    COALESCE(SUM(ul.total_cost), 0),
    COALESCE(SUM(ul.actual_cost), 0),
    COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0),
    COUNT(*)
    , now()
FROM usage_logs ul
WHERE ul.created_at >= $1 AND ul.created_at < $2
GROUP BY 1, 2
ON CONFLICT (api_key_id, day) DO UPDATE SET
    requests              = EXCLUDED.requests,
    input_tokens          = EXCLUDED.input_tokens,
    output_tokens         = EXCLUDED.output_tokens,
    cache_creation_tokens = EXCLUDED.cache_creation_tokens,
    cache_read_tokens     = EXCLUDED.cache_read_tokens,
    total_cost            = EXCLUDED.total_cost,
    actual_cost           = EXCLUDED.actual_cost,
    duration_sum          = EXCLUDED.duration_sum,
    duration_count        = EXCLUDED.duration_count,
    computed_at           = EXCLUDED.computed_at
`

// BackfillAll 一次性重建全部历史（分批，可重入）。
//
// 生产 1761 万行实测 4.4s 能跑完，但生产有并发负载，所以按本地日分片提交：
// 每个事务只处理 chunkDays 天，中断后重跑会从有数据的下一片继续。
// 返回处理的分片数，便于运维判断进度。
func (r *usageKeyDailyRollupRepository) BackfillAll(ctx context.Context, chunkDays int) (int, error) {
	if r == nil || r.sql == nil {
		return 0, nil
	}
	if chunkDays <= 0 {
		chunkDays = 7
	}
	timezoneName := service.GroupUsageTimezoneName()

	var earliest, latest sql.NullTime
	if err := scanSingleRow(ctx, r.sql,
		`SELECT MIN(created_at), MAX(created_at) FROM usage_logs`, nil,
		&earliest, &latest); err != nil {
		return 0, fmt.Errorf("读取用量时间范围: %w", err)
	}
	if !earliest.Valid || !latest.Valid {
		return 0, nil
	}

	cursor := service.GroupUsageTodayStart(earliest.Time)
	end := service.GroupUsageTodayStart(latest.Time.Add(24 * time.Hour))
	chunks := 0
	for cursor.Before(end) {
		next := cursor.AddDate(0, 0, chunkDays)
		if next.After(end) {
			next = end
		}
		if _, err := r.sql.ExecContext(ctx, keyUsageDailyUpsertSQL, cursor.UTC(), next.UTC(), timezoneName); err != nil {
			return chunks, fmt.Errorf("回填键日汇总 [%s,%s): %w",
				cursor.Format(service.GroupUsageDateFormat), next.Format(service.GroupUsageDateFormat), err)
		}
		chunks++
		cursor = next
		if err := ctx.Err(); err != nil {
			return chunks, err
		}
	}

	if _, err := r.sql.ExecContext(ctx, `
		UPDATE usage_key_daily_state
		SET closed_before = $1::date, timezone_name = $2, updated_at = now()
		WHERE id = 1
	`, service.GroupUsageDate(end), timezoneName); err != nil {
		return chunks, fmt.Errorf("更新键日汇总水位: %w", err)
	}
	return chunks, nil
}
