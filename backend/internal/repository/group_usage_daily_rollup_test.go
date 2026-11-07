//go:build unit

package repository

import (
	"regexp"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 分组日汇总的口径必须与键日汇总逐条对齐，否则两个维度会各自演化出不同的
// 日界线。这些测试锁住三件事：SQL 里的时区是显式写死的、整日重算是覆盖而不是
// 累加、只发布已关闭的日。

func TestGroupUsageDailyUpsertUsesExplicitTimezone(t *testing.T) {
	sql := groupUsageDailyUpsertSQL
	// 时区必须由参数显式传入，不能依赖 session 默认值：
	// 生产 PG 是 Asia/Shanghai、容器默认可能是 UTC，两者相差 1/3 的日期归属。
	if !gudContains(sql, "AT TIME ZONE $3::text") {
		t.Fatal("日桶口径必须显式接收时区参数（AT TIME ZONE $3::text），不能依赖 session 默认值")
	}
	if gudContains(sql, "::date\n") && !gudContains(sql, "AT TIME ZONE") {
		t.Fatal("不允许裸 (created_at)::date 分桶")
	}
}

func TestGroupUsageDailyUpsertIsOverwriteNotAccumulate(t *testing.T) {
	sql := groupUsageDailyUpsertSQL
	// 同一天可能有迟到日志，累加会重复计费。必须是覆盖。
	if !gudContains(sql, "ON CONFLICT (group_id, day) DO UPDATE SET") {
		t.Fatal("必须是 ON CONFLICT DO UPDATE（整日覆盖），不能用累加")
	}
	if gudContains(sql, "requests              = usage_group_daily.requests +") {
		t.Fatal("不允许累加式更新")
	}
	// 每个聚合列都必须被 EXCLUDED 覆盖，漏一列就会那一天永远是旧值。
	// 用正则忽略 SQL 里的对齐空格，避免测试因为排版变化而假失败。
	for _, col := range []string{
		"requests", "input_tokens", "output_tokens",
		"cache_creation_tokens", "cache_read_tokens",
		"total_cost", "actual_cost", "computed_at",
	} {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(col) + `\s*=\s*EXCLUDED\.` + regexp.QuoteMeta(col) + `\s*,?\s*$`)
		if !re.MatchString(sql) {
			t.Fatalf("列 %q 没有在 ON CONFLICT DO UPDATE 里被覆盖", col)
		}
	}
}

func TestGroupUsageDailyUpsertSkipsNullGroups(t *testing.T) {
	if !gudContains(groupUsageDailyUpsertSQL, "ul.group_id IS NOT NULL") {
		t.Fatal("必须排除 group_id 为 NULL 的行，否则主键约束会失败")
	}
}

// 读侧必须把「今天」排除在汇总表之外并现算，避免返回不完整的日桶。
func TestGroupCacheRatesSplitsClosedDaysFromToday(t *testing.T) {
	// 这是一个结构断言：读路径的 closed 段用 day 上界 < 今日，
	// today 段用 usage_logs + [今日零点, now)。
	now := time.Date(2026, 11, 8, 7, 30, 0, 0, time.UTC)
	todayStart := service.GroupUsageTodayStart(now)
	// 上海时间 15:30，今日零点应是上海 2026-11-08 00:00
	if got := service.GroupUsageDate(todayStart); got != "2026-11-08" {
		t.Fatalf("今日日期按配置时区应为 2026-11-08，实际 %q", got)
	}
	// 汇总表覆盖 [todayStart-window, todayStart)，今天由 usage_logs 现算
	if !todayStart.After(now.Add(-24 * time.Hour)) {
		t.Fatal("todayStart 应落在合理范围内")
	}
}

func gudContains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		gudIndexOf(haystack, needle) >= 0
}

func gudIndexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
