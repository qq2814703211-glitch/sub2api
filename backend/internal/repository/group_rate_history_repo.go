package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// groupRateHistoryRowCap 限制单次查询。行按从旧到新返回，因此极端编辑
// 循环最多丢掉最后一组的最新行；服务端看到过期的「最新」行后会扣留提示。
const groupRateHistoryRowCap = 5000

// ListGroupRateChangesSince 返回给定分组在 changed_at >= since 的基础倍率
// 变化记录，按分组、时间、id 排序。只通过 (group_id, changed_at) 索引读取
// group_rate_history。
func (r *groupRepository) ListGroupRateChangesSince(ctx context.Context, groupIDs []int64, since time.Time) ([]service.GroupRateChange, error) {
	if r == nil || r.sql == nil || len(groupIDs) == 0 {
		return []service.GroupRateChange{}, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, group_id, old_rate, new_rate, changed_at
		FROM group_rate_history
		WHERE group_id = ANY($1) AND changed_at >= $2
		ORDER BY group_id, changed_at, id
		LIMIT $3`, pq.Array(groupIDs), since.UTC(), groupRateHistoryRowCap)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupRateChange, 0, len(groupIDs))
	for rows.Next() {
		var row service.GroupRateChange
		if err := rows.Scan(&row.ID, &row.GroupID, &row.OldRate, &row.NewRate, &row.ChangedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
