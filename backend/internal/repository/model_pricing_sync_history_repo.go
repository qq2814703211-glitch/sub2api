package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// modelPricingSyncHistoryRepository 是 models.dev 价格同步变更史的仓储（raw SQL，append-only）。
// 变更史只追加，不提供更新与单条删除：它是「为什么这个价格是这个数字」的唯一证据。
type modelPricingSyncHistoryRepository struct {
	db *sql.DB
}

// NewModelPricingSyncHistoryRepository 创建变更史仓储。
func NewModelPricingSyncHistoryRepository(db *sql.DB) service.ModelPricingSyncHistoryRepository {
	return &modelPricingSyncHistoryRepository{db: db}
}

const modelPricingSyncHistoryColumns = `model, field, old_value, new_value, source, applied_at, reason`

// InsertBatch 批量写入变更记录。用 COPY 走一条语句，避免字段多时逐行往返。
func (r *modelPricingSyncHistoryRepository) InsertBatch(ctx context.Context, rows []*service.ModelPricingSyncHistory) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil model pricing sync history repository")
	}
	if len(rows) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(
		"model_pricing_sync_history",
		"model", "field", "old_value", "new_value", "source", "applied_at", "reason",
	))
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	now := time.Now().UTC()
	for _, row := range rows {
		if row == nil {
			continue
		}
		appliedAt := row.AppliedAt
		if appliedAt.IsZero() {
			appliedAt = now
		}
		source := strings.TrimSpace(row.Source)
		if source == "" {
			source = service.ModelPricingSyncSourceModelsDev
		}
		if _, err := stmt.ExecContext(ctx,
			truncateString(row.Model, 200),
			truncateString(row.Field, 80),
			row.OldValue,
			row.NewValue,
			truncateString(source, 40),
			appliedAt.UTC(),
			truncateString(row.Reason, 512),
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return 0, err
		}
	}
	if _, err := stmt.ExecContext(ctx); err != nil { // flush COPY
		_ = stmt.Close()
		_ = tx.Rollback()
		return 0, err
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(rows)), nil
}

// List 分页查询变更史，按 applied_at 倒序。
func (r *modelPricingSyncHistoryRepository) List(ctx context.Context, filter *service.ModelPricingSyncHistoryFilter) ([]*service.ModelPricingSyncHistory, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("nil model pricing sync history repository")
	}
	if filter == nil {
		filter = &service.ModelPricingSyncHistoryFilter{}
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	where, args := modelPricingSyncHistoryWhere(filter)

	var total int64
	countQuery := `SELECT COUNT(*) FROM model_pricing_sync_history` + where
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	query := `SELECT ` + modelPricingSyncHistoryColumns + ` FROM model_pricing_sync_history` + where +
		fmt.Sprintf(` ORDER BY applied_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	rows, err := r.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	records := make([]*service.ModelPricingSyncHistory, 0, pageSize)
	for rows.Next() {
		var (
			record   service.ModelPricingSyncHistory
			model    sql.NullString
			field    sql.NullString
			source   sql.NullString
			reason   sql.NullString
			applied  time.Time
		)
		if err := rows.Scan(&model, &field, &record.OldValue, &record.NewValue, &source, &applied, &reason); err != nil {
			return nil, 0, err
		}
		record.Model = model.String
		record.Field = field.String
		record.Source = source.String
		record.Reason = reason.String
		record.AppliedAt = applied
		records = append(records, &record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// ListSyncedFields 返回指定来源最近一次写入的 (模型, 字段) -> 值。
//
// 同步任务用它判定字段归属：值等于上次同步写入的值，说明这是同步自己的，
// 可以跟随官方价更新；值不等或字段不存在，说明当前值由别人（运营）写入，
// 绝不覆盖。取 DISTINCT ON 的最新一行，而不是只取字段名，是为了抓住
// 「同步先写入、运营后改价」这种顺序下被手工编辑过的值。
func (r *modelPricingSyncHistoryRepository) ListSyncedFields(ctx context.Context, source string) (map[string]map[string]float64, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil model pricing sync history repository")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = service.ModelPricingSyncSourceModelsDev
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT ON (model, field) model, field, new_value
		   FROM model_pricing_sync_history
		  WHERE source = $1
		  ORDER BY model, field, applied_at DESC, id DESC`, source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]map[string]float64)
	for rows.Next() {
		var model, field sql.NullString
		var value sql.NullFloat64
		if err := rows.Scan(&model, &field, &value); err != nil {
			return nil, err
		}
		if model.String == "" || field.String == "" {
			continue
		}
		fields, ok := out[model.String]
		if !ok {
			fields = make(map[string]float64)
			out[model.String] = fields
		}
		fields[field.String] = value.Float64
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// modelPricingSyncHistoryWhere 拼装筛选条件，返回 WHERE 子句与参数。
func modelPricingSyncHistoryWhere(filter *service.ModelPricingSyncHistoryFilter) (string, []any) {
	var conditions []string
	var args []any
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		args = append(args, strings.TrimSpace(value))
		conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	add("model", filter.Model)
	add("field", filter.Field)
	add("source", filter.Source)
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}
