package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRateHistoryMigration(t *testing.T) {
	content, err := FS.ReadFile("904_group_rate_history.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 只追加历史表，精度与 groups.rate_multiplier 一致。
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS group_rate_history (")
	require.Contains(t, sql, "id BIGSERIAL PRIMARY KEY")
	require.Contains(t, sql, "group_id BIGINT NOT NULL")
	require.Contains(t, sql, "old_rate DECIMAL(10, 4) NOT NULL")
	require.Contains(t, sql, "new_rate DECIMAL(10, 4) NOT NULL")
	require.Contains(t, sql, "changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_group_rate_history_group_changed ON group_rate_history (group_id, changed_at DESC)")
	// 显式保留策略按时间裁剪。
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_group_rate_history_changed_at ON group_rate_history (changed_at)")

	// 行触发器让历史与每一次 groups.rate_multiplier 写入同事务，且只记录真实变化。
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION record_group_rate_history()")
	require.Contains(t, sql, "IF NEW.rate_multiplier IS DISTINCT FROM OLD.rate_multiplier THEN")
	require.Contains(t, sql, "INSERT INTO group_rate_history (group_id, old_rate, new_rate) VALUES (NEW.id, OLD.rate_multiplier, NEW.rate_multiplier)")
	require.Contains(t, sql, "DROP TRIGGER IF EXISTS trg_groups_rate_history ON groups")
	require.Contains(t, sql, "CREATE TRIGGER trg_groups_rate_history AFTER UPDATE OF rate_multiplier ON groups FOR EACH ROW WHEN (OLD.rate_multiplier IS DISTINCT FROM NEW.rate_multiplier) EXECUTE FUNCTION record_group_rate_history()")
}

// 迁移必须是普通、追加式、可事务执行的脚本，不回溯或改写既有业务数据。
func TestGroupRateHistoryMigrationStaysAdditiveAndTransactional(t *testing.T) {
	content, err := FS.ReadFile("904_group_rate_history.sql")
	require.NoError(t, err)

	require.NotContains(t, strings.ToLower(string(content)), "concurrent")

	var statements []string
	for _, line := range strings.Split(string(content), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			statements = append(statements, trimmed)
		}
	}
	lower := strings.ToLower(strings.Join(statements, " "))

	for _, forbidden := range []string{"drop table", "alter table", "truncate", "delete from", "update groups", "insert into groups"} {
		require.NotContains(t, lower, forbidden)
	}
	require.NotContains(t, lower, "foreign key")
	require.NotContains(t, lower, "references")

	// 只有触发器函数插入历史；迁移本身不预置任何行。
	require.Equal(t, 1, strings.Count(lower, "insert into"))
}
