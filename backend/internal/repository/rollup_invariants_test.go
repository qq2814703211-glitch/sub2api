//go:build unit

package repository

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 分组日预聚与键日预聚共用同一套口径约束。这些测试锁的是「改坏了会静默算错钱、
// 或静默退回慢查询」的几条线，不是实现细节。

// 两侧的 readState 都必须用 BOOL_AND 而不是 MAX 聚合布尔。
//
// PostgreSQL 没有 boolean 的 max 聚合：写成 MAX(enabled) 会报
// "function max(boolean) does not exist"，整个同步失败，而错误只在日志里，
// 表现为「预聚表永远是空的、查询还是慢」，极难定位。
// 这个 bug 真实发生过，两个维度各一次。
func TestRollupReadStateUsesBoolAnd(t *testing.T) {
	cases := map[string]string{
		"键日": "internal/repository/key_usage_daily_rollup_repo.go",
		"分组": "internal/repository/group_usage_daily_rollup_repo.go",
	}
	banned := regexp.MustCompile(`(?i)MAX\s*\(\s*enabled\s*\)`)

	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			src := readRepoSource(t, file)
			if banned.MatchString(src) {
				t.Fatalf("%s 用了 MAX(enabled)：PostgreSQL 没有 boolean 的 max 聚合，同步会整体失败", file)
			}
			if !strings.Contains(src, "BOOL_AND(enabled)") {
				t.Fatalf("%s 的 readState 缺少 BOOL_AND(enabled)", file)
			}
		})
	}
}

// 日桶必须显式带时区参数，不能依赖 PG session 时区。
//
// 应用默认 Asia/Shanghai，容器/库默认可能是 UTC；两者相差整整一个日界，
// 会让同一批用量落到不同的 day 上，既不幂等也会让读侧水位判断失真。
func TestGroupRollupPinsTimezone(t *testing.T) {
	src := readRepoSource(t, "internal/repository/group_usage_daily_rollup_repo.go")
	if !strings.Contains(src, "AT TIME ZONE") {
		t.Fatal("分组日预聚没有显式 AT TIME ZONE，会随 session 时区漂移")
	}
}

// 日桶必须整日覆盖，不能累加。
//
// 迟到日志如果走 ON CONFLICT DO UPDATE ... SET requests = requests + EXCLUDED.requests，
// 每次重算都会把同一天再加一遍，金额会翻倍。必须是 SET requests = EXCLUDED.requests。
func TestGroupRollupOverwritesNotAccumulates(t *testing.T) {
	src := readRepoSource(t, "internal/repository/group_usage_daily_rollup_repo.go")
	if regexp.MustCompile(`(?i)requests\s*=\s*[a-z_]*\.?requests\s*\+`).MatchString(src) {
		t.Fatal("分组日预聚在做累加，重算会让金额翻倍；应整日覆盖")
	}
	if !regexp.MustCompile(`(?i)ON\s+CONFLICT[\s\S]{0,200}DO\s+UPDATE`).MatchString(src) {
		t.Fatal("分组日预聚缺少 ON CONFLICT DO UPDATE，重入不安全")
	}
}

// 预聚只发布「已关闭的日」，今天由 usage_logs 现算。
//
// 今天的桶还在增长，把它当最终值发布会让用户看到中途截断的用量；
// 而且读侧水位才可能稳定前进。
func TestGroupRollupExcludesToday(t *testing.T) {
	src := readRepoSource(t, "internal/repository/group_usage_daily_rollup_repo.go")
	// 必须有「小于今天」的边界表达，不能无条件聚合全部历史（含今天）。
	if !regexp.MustCompile(`(?i)closed_before|<\s*\$|day\s*<`).MatchString(src) {
		t.Fatal("分组日预聚没有排除今日的边界，会把不完整的今天当最终值")
	}
}

func readRepoSource(t *testing.T, rel string) string {
	t.Helper()
	// 测试运行时的工作目录就是 internal/repository，源码与本测试同目录。
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	b, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("读取 %s: %v", rel, err)
	}
	return string(b)
}
