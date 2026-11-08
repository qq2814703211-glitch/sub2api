package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 920：缓存率均值窗口支持分钟单位。
//
// 三条硬要求：
//  1. 老列 cache_average_window_days 保留（旧实例还在读它），新列加 value + unit；
//  2. 存量行必须迁移成「30 天」，不能变成「30 分钟」；
//  3. 迁移幂等，重跑不会覆盖用户后来选的分钟窗口。
func TestChannelMonitorV3CacheWindowUnitMigration(t *testing.T) {
	content, err := FS.ReadFile("920_channel_monitor_v3_cache_window_unit.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS cache_average_window_value INTEGER")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS cache_average_window_unit TEXT")

	// 存量值按「天」解释：value 拷贝 days，unit 显式写 day。
	require.Contains(t, sql, "SET cache_average_window_value = cache_average_window_days, cache_average_window_unit = 'day'")
	// 只在尚未迁移时搬，重跑不覆盖。
	require.Contains(t, sql, "WHERE id = 1 AND cache_average_window_unit = '' AND cache_average_window_value = 0")

	// 老列与老约束都不能删：旧版本后端只读 days，且 CHECK(1..90) 约束的是它。
	require.NotContains(t, sql, "DROP COLUMN cache_average_window_days")
	require.NotContains(t, sql, "DROP CONSTRAINT channel_monitor_v3_config_cache_average_window_days_check")

	// 单位有数据库兜底，且允许空串（表示尚未迁移，读侧按天解释）。
	require.Contains(t, sql, "CHECK (cache_average_window_unit IN ('', 'minute', 'day'))")
	// 约束是幂等添加的。
	require.Contains(t, sql, "IF NOT EXISTS ( SELECT 1 FROM pg_constraint WHERE conname = 'channel_monitor_v3_config_window_unit_check'")
}

// 老迁移 915 不能被改写：它已经跑在生产上，checksum 校验会拒。
// 这条测试是提示，防止后来的人「顺手把 days 列改成 value+unit」。
func TestChannelMonitorV3BaseMigrationStillOwnsTheDaysColumn(t *testing.T) {
	content, err := FS.ReadFile("915_channel_monitor_v3_config.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "cache_average_window_days INTEGER NOT NULL DEFAULT 30")
	require.Contains(t, sql, "CHECK (cache_average_window_days BETWEEN 1 AND 90)")
	// 新列属于 920，不属于 915。
	require.NotContains(t, sql, "cache_average_window_unit")
}
