package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// usageGroupDailyRollupRepository 维护 usage_group_daily 这张派生的分组日汇总表。
//
// 语义与 usage_key_daily 的同名实现逐条对齐（时区写死、整日重算、只发布已关闭日），
// 这样两个维度不会各自演化出不同的日界线口径：
//
//  1. 日桶 = 服务端配置时区内的自然日，SQL 里显式写时区名，不依赖 session 默认值。
//  2. 整日重算，不做累加。同一天可能有迟到日志（重试、补录），累加会重复计费。
//  3. 只发布「已关闭」的日（day < 本地今日）。今天还在增长，读侧用 usage_logs
//     补这一段，避免把不完整的日桶当成最终值返回给用户。
type usageGroupDailyRollupRepository struct {
	sql sqlExecutor
}

// NewUsageGroupDailyRollupRepository 创建分组日汇总仓储。
func NewUsageGroupDailyRollupRepository(db *sql.DB) service.UsageGroupDailyRollupRepository {
	if db == nil {
		return nil
	}
	return &usageGroupDailyRollupRepository{sql: db}
}

func newUsageGroupDailyRollupRepositoryWithSQL(sqlq sqlExecutor) *usageGroupDailyRollupRepository {
	return &usageGroupDailyRollupRepository{sql: sqlq}
}

// groupDailyRollupState 是 usage_group_daily_state 的一次快照。
type groupDailyRollupState struct {
	valid        bool
	enabled      bool
	closedBefore string // date，已关闭日的右开边界
	timezoneName string
}

// readState 读水位。时区不一致或水位在未来一律按「无效」处理：宁可全量重算，
// 也不能把两份不同日历口径的日桶混在一起。
func (r *usageGroupDailyRollupRepository) readState(ctx context.Context, timezoneName, todayDate string) (groupDailyRollupState, error) {
	var rowCount int
	var closedBefore sql.NullString
	var stateTimezone sql.NullString
	var enabled sql.NullBool
	if err := scanSingleRow(ctx, r.sql, `
		SELECT COUNT(*), MAX(closed_before)::text, MAX(timezone_name), MAX(enabled)
		FROM usage_group_daily_state
		WHERE id = 1
	`, nil, &rowCount, &closedBefore, &stateTimezone, &enabled); err != nil {
		return groupDailyRollupState{}, fmt.Errorf("读取分组日汇总水位: %w", err)
	}
	state := groupDailyRollupState{
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

// Sync 把今日以前的用量发布进 usage_group_daily。
//
// 每次只重算 [closedBefore, todayStart) 这一段：已关闭的日不重复扫。水位前进
// 到今日零点，因此稳态下每次只处理昨天一天，成本与历史深度无关。
func (r *usageGroupDailyRollupRepository) Sync(ctx context.Context, now time.Time) error {
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
		return fmt.Errorf("解析分组日汇总水位 %q: %w", state.closedBefore, err)
	}

	// 该日之前的桶全部作废重算。带 day 下界，避免无谓的全表扫描。
	if _, err := r.sql.ExecContext(ctx, `
		DELETE FROM usage_group_daily
		WHERE day >= $1::date AND day < $2::date
	`, state.closedBefore, todayDate); err != nil {
		return fmt.Errorf("清理分组日汇总日桶: %w", err)
	}

	if _, err := r.sql.ExecContext(ctx, groupUsageDailyUpsertSQL,
		rebuildStart.UTC(), todayStart.UTC(), timezoneName); err != nil {
		return fmt.Errorf("重建分组日汇总日桶: %w", err)
	}

	if _, err := r.sql.ExecContext(ctx, `
		UPDATE usage_group_daily_state
		SET closed_before = $1::date, timezone_name = $2, updated_at = now()
		WHERE id = 1
	`, todayDate, timezoneName); err != nil {
		return fmt.Errorf("更新分组日汇总水位: %w", err)
	}
	return nil
}

// groupUsageDailyUpsertSQL 是唯一一处定义分组日桶口径的 SQL。
//
// 查询侧与回填侧都复用它，保证「回填的日」与「增量重算的日」逐行同口径。
// 参数：$1 窗口起点, $2 窗口终点（右开）, $3 时区名。
const groupUsageDailyUpsertSQL = `
INSERT INTO usage_group_daily (
    group_id, day, requests, input_tokens, output_tokens,
    cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
    computed_at
)
SELECT
    ul.group_id,
    (ul.created_at AT TIME ZONE $3::text)::date AS day,
    COUNT(*),
    COALESCE(SUM(ul.input_tokens), 0),
    COALESCE(SUM(ul.output_tokens), 0),
    COALESCE(SUM(ul.cache_creation_tokens), 0),
    COALESCE(SUM(ul.cache_read_tokens), 0),
    COALESCE(SUM(ul.total_cost), 0),
    COALESCE(SUM(ul.actual_cost), 0),
    now()
FROM usage_logs ul
WHERE ul.created_at >= $1 AND ul.created_at < $2
  AND ul.group_id IS NOT NULL
GROUP BY 1, 2
ON CONFLICT (group_id, day) DO UPDATE SET
    requests              = EXCLUDED.requests,
    input_tokens          = EXCLUDED.input_tokens,
    output_tokens         = EXCLUDED.output_tokens,
    cache_creation_tokens = EXCLUDED.cache_creation_tokens,
    cache_read_tokens     = EXCLUDED.cache_read_tokens,
    total_cost            = EXCLUDED.total_cost,
    actual_cost           = EXCLUDED.actual_cost,
    computed_at           = EXCLUDED.computed_at
`

// BackfillAll 一次性重建全部历史（分批，可重入）。
//
// 按本地日分片提交：每个事务只处理 chunkDays 天，中断后重跑会从有数据的下一片
// 继续。返回处理的分片数，便于运维判断进度。
func (r *usageGroupDailyRollupRepository) BackfillAll(ctx context.Context, chunkDays int) (int, error) {
	if r == nil || r.sql == nil {
		return 0, nil
	}
	if chunkDays <= 0 {
		chunkDays = 7
	}
	timezoneName := service.GroupUsageTimezoneName()

	var minText sql.NullString
	if err := scanSingleRow(ctx, r.sql,
		`SELECT MIN(created_at)::text FROM usage_logs WHERE group_id IS NOT NULL`,
		nil, &minText); err != nil {
		return 0, fmt.Errorf("扫描分组日汇总起点: %w", err)
	}
	if !minText.Valid {
		return 0, nil
	}
	cursor, err := time.Parse(time.RFC3339Nano, minText.String)
	if err != nil {
		return 0, fmt.Errorf("解析分组日汇总起点 %q: %w", minText.String, err)
	}
	cursorDate, err := service.ParseGroupUsageDate(service.GroupUsageDate(cursor))
	if err != nil {
		return 0, err
	}
	todayStart := service.GroupUsageTodayStart(time.Now())

	chunks := 0
	for cursorDate.Before(todayStart) {
		chunkEnd := cursorDate.AddDate(0, 0, chunkDays)
		if chunkEnd.After(todayStart) {
			chunkEnd = todayStart
		}
		if _, err := r.sql.ExecContext(ctx, groupUsageDailyUpsertSQL,
			cursorDate.UTC(), chunkEnd.UTC(), timezoneName); err != nil {
			return chunks, fmt.Errorf("回填分组日汇总 [%s,%s): %w",
				cursorDate.Format(service.GroupUsageDateFormat),
				chunkEnd.Format(service.GroupUsageDateFormat), err)
		}
		chunks++
		cursorDate = chunkEnd
	}

	if _, err := r.sql.ExecContext(ctx, `
		UPDATE usage_group_daily_state
		SET closed_before = $1::date, timezone_name = $2, updated_at = now()
		WHERE id = 1
	`, service.GroupUsageDate(todayStart), timezoneName); err != nil {
		return chunks, fmt.Errorf("更新分组日汇总水位: %w", err)
	}
	return chunks, nil
}
