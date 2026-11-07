//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// 这组测试跑在真实 PostgreSQL 上：键日汇总的正确性完全由 SQL 表达（日界、
// 时区口径、ON CONFLICT 重算），mock 无法证明。
//
//	PERF_DATABASE_URL='postgres://sub2api:localperf@127.0.0.1:5434/sub2api?sslmode=disable' \
//	  go test -tags integration ./internal/repository/ -run TestKeyDaily -v
//
// 每个用例自己造数据并在结束时清掉自己写的那部分，不依赖也不改动库里既有内容。

func keyDailyTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("PERF_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PERF_DATABASE_URL to run key daily rollup integration tests")
	}
	// 汇总表的日界来自服务端配置时区。生产启动时会 Init，测试进程必须自己
	// 做一次，否则 timezone.Name() 返回 "Local"，日桶会按容器时区（UTC）切，
	// 与断言里的 Asia/Shanghai 自然日不一致。
	require.NoError(t, timezone.Init("Asia/Shanghai"))
	require.Equal(t, "Asia/Shanghai", timezone.Name())

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })
	return db, context.Background()
}

// keyDailySeedID 造一个不与库里既有数据碰撞的 api_key_id，并在 api_keys 里
// 建对应行（usage_logs 有外键），返回 id 与清理函数。
//
// 用高位随机段而不是固定值：测试可能和真实数据或并发跑的其他用例共存。
func keyDailySeedKey(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	id := 9_100_000_000 + (time.Now().UnixNano() % 100_000_000)
	var userID int64
	err := db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID)
	require.NoError(t, err, "测试库需要至少一个 user")
	_, err = db.Exec(`
		INSERT INTO api_keys (id, user_id, key, name, status)
		VALUES ($1, $2, $3, $4, 'active')`,
		id, userID, fmt.Sprintf("kd-test-%d", id), fmt.Sprintf("kd-test-%d", id))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM usage_logs WHERE api_key_id = $1`, id)
		_, _ = db.Exec(`DELETE FROM usage_key_daily WHERE api_key_id = $1`, id)
		_, _ = db.Exec(`DELETE FROM api_keys WHERE id = $1`, id)
	})
	return id
}

func keyDailyCleanup(t *testing.T, db *sql.DB, keyID int64) {
	t.Helper()
	_, err := db.Exec(`DELETE FROM usage_logs WHERE api_key_id = $1`, keyID)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM usage_key_daily WHERE api_key_id = $1`, keyID)
	require.NoError(t, err)
}

// rawDailyTotals 是「直接扫 usage_logs」的参照实现，逐字对应
// usageLogRepository.GetAPIKeyStatsAggregated 的聚合列与 trend 的日分组。
//
// 元组布局（day 之外的 8 个数值，顺序与 SELECT 一致）：
//
//	[0] requests  [1] input  [2] output  [3] cache_creation  [4] cache_read
//	[5] total_cost  [6] actual_cost  [7] duration_sum
func rawDailyTotals(t *testing.T, db *sql.DB, keyID int64, start, end time.Time, tzName string) map[string][8]float64 {
	t.Helper()
	rows, err := db.Query(`
		SELECT (created_at AT TIME ZONE $4)::date::text AS day,
		       COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0),
		       COALESCE(SUM(COALESCE(duration_ms,0)),0)
		FROM usage_logs
		WHERE api_key_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY 1`, keyID, start, end, tzName)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string][8]float64{}
	for rows.Next() {
		var day string
		var v [8]float64
		require.NoError(t, rows.Scan(&day, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7]))
		out[day] = v
	}
	require.NoError(t, rows.Err())
	return out
}

// rebuildRollupFor 用生产同一段 SQL 把某窗口的日桶写进 usage_key_daily。
func rebuildRollupFor(t *testing.T, db *sql.DB, start, end time.Time, tzName string) {
	t.Helper()
	_, err := db.Exec(keyUsageDailyUpsertSQL, start, end, tzName)
	require.NoError(t, err)
}

func TestKeyDailyRollupMatchesRawAcrossDayBoundary(t *testing.T) {
	db, ctx := keyDailyTestDB(t)
	_ = ctx
	keyID := keyDailySeedKey(t, db)
	t.Cleanup(func() { keyDailyCleanup(t, db, keyID) })

	// 关键构造：上海时间某日 00:30 与 23:30 各一条。UTC 下它们分别落在前一
	// 天 16:30 和当天 15:30 —— 若分桶表达式误用 UTC，两条会落到两个 UTC 日，
	// 与本地自然日不一致，这个用例就会红。
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, loc)
	early := day.Add(30 * time.Minute)    // 本地 00:30
	late := day.Add(23*time.Hour + 30*time.Minute) // 本地 23:30
	future := day.Add(48*time.Hour + 5*time.Minute)

	_, err = db.Exec(`
		INSERT INTO usage_logs (user_id, api_key_id, account_id, model, request_id, input_tokens,
			output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
			duration_ms, created_at)
		VALUES (1,$1,1,'m','kd-early',10,20,30,40,0.1,0.2,100,$2),
		       (1,$1,1,'m','kd-late', 11,21,31,41,0.3,0.4,200,$3),
		       (1,$1,1,'m','kd-next', 12,22,32,42,0.5,0.6,300,$4)`,
		keyID, early, late, future)
	require.NoError(t, err)

	tzName := timezone.Name()
	rebuildRollupFor(t, db, day.Add(-24*time.Hour), day.Add(96*time.Hour), tzName)

	start := day.Add(-24 * time.Hour)
	end := day.Add(96 * time.Hour)
	raw := rawDailyTotals(t, db, keyID, start, end, tzName)

	var got int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM usage_key_daily
		WHERE api_key_id=$1 AND day >= ($2 AT TIME ZONE $4)::date AND day < ($3 AT TIME ZONE $4)::date`,
		keyID, start, end, tzName).Scan(&got))

	require.Equal(t, len(raw), got, "预聚日数必须与原始查询的日分组数一致")

	for dayStr, want := range raw {
		var requests, in, out, cc, cr, dsum, dcount sql.NullInt64
		var totalCost, actualCost sql.NullFloat64
		require.NoError(t, db.QueryRow(`
			SELECT requests, input_tokens, output_tokens, cache_creation_tokens,
			       cache_read_tokens, total_cost, actual_cost, duration_sum, duration_count
			FROM usage_key_daily WHERE api_key_id=$1 AND day=$2::date`,
			keyID, dayStr).Scan(&requests, &in, &out, &cc, &cr, &totalCost, &actualCost, &dsum, &dcount))
		require.Equal(t, int64(want[0]), requests.Int64, "day %s requests", dayStr)
		require.Equal(t, int64(want[1]), in.Int64, "day %s input_tokens", dayStr)
		require.Equal(t, int64(want[2]), out.Int64, "day %s output_tokens", dayStr)
		require.Equal(t, int64(want[3]), cc.Int64, "day %s cache_creation", dayStr)
		require.Equal(t, int64(want[4]), cr.Int64, "day %s cache_read", dayStr)
		require.InDelta(t, want[5], totalCost.Float64, 1e-9, "day %s total_cost", dayStr)
		require.InDelta(t, want[6], actualCost.Float64, 1e-9, "day %s actual_cost", dayStr)
		require.Equal(t, int64(want[7]), dsum.Int64, "day %s duration_sum", dayStr)
		require.Equal(t, int64(want[0]), dcount.Int64, "day %s duration_count", dayStr)
	}

	// 明确断言跨日界的两条落在同一个本地日，而不是两个 UTC 日。
	var dayCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_key_daily WHERE api_key_id=$1`, keyID).Scan(&dayCount))
	require.Equal(t, 2, dayCount, "00:30 与 23:30 必须是同一个本地日；48h 后那条是第二天")
}

func TestKeyDailyRollupIsIdempotentAndRecomputesWholeDay(t *testing.T) {
	db, _ := keyDailyTestDB(t)
	keyID := keyDailySeedKey(t, db)
	t.Cleanup(func() { keyDailyCleanup(t, db, keyID) })

	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, loc)
	insert := func(requestID string, at time.Time, cost float64) {
		_, err := db.Exec(`
			INSERT INTO usage_logs (user_id, api_key_id, account_id, model, request_id,
				total_cost, actual_cost, created_at)
			VALUES (1,$1,1,'m',$2,$3,$3,$4)`, keyID, requestID, cost, at)
		require.NoError(t, err)
	}

	tzName := timezone.Name()
	insert("kd-a", day.Add(time.Hour), 1.5)
	rebuildRollupFor(t, db, day.Add(-24*time.Hour), day.Add(48*time.Hour), tzName)

	// 同日迟到一条（模拟重试/补录），整日重算后必须变成 2.0，而不是累加成 3.5。
	insert("kd-b", day.Add(2*time.Hour), 0.5)
	rebuildRollupFor(t, db, day.Add(-24*time.Hour), day.Add(48*time.Hour), tzName)

	var requests int64
	var actual float64
	require.NoError(t, db.QueryRow(`
		SELECT requests, actual_cost FROM usage_key_daily WHERE api_key_id=$1 AND day=$2::date`,
		keyID, day.Format("2006-01-02")).Scan(&requests, &actual))
	require.Equal(t, int64(2), requests, "两次重算必须收敛到 2 条，不能翻倍")
	require.InDelta(t, 2.0, actual, 1e-9, "金额必须是整日重算值，不是累加值")

	// 再重算一次，结果必须不变（幂等）。
	rebuildRollupFor(t, db, day.Add(-24*time.Hour), day.Add(48*time.Hour), tzName)
	require.NoError(t, db.QueryRow(`
		SELECT requests, actual_cost FROM usage_key_daily WHERE api_key_id=$1 AND day=$2::date`,
		keyID, day.Format("2006-01-02")).Scan(&requests, &actual))
	require.Equal(t, int64(2), requests)
	require.InDelta(t, 2.0, actual, 1e-9)
}

func TestKeyDailyTotalsForKeysMatchesRawForManyKeys(t *testing.T) {
	db, ctx := keyDailyTestDB(t)
	repo := newUsageKeyDailyRollupRepositoryWithSQL(db)

	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	day := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	tzName := timezone.Name()

	keyIDs := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		keyID := keyDailySeedKey(t, db)
		keyIDs = append(keyIDs, keyID)
		_, err := db.Exec(`
			INSERT INTO usage_logs (user_id, api_key_id, account_id, model, request_id,
				input_tokens, output_tokens, total_cost, actual_cost, created_at)
			VALUES (1,$1,1,'m',$2,10,20,0.25,0.5,$3),
			       (1,$1,1,'m',$4,10,20,0.25,0.5,$5)`,
			keyID, fmt.Sprintf("kd-multi-%d-a", keyID), day.Add(2*time.Hour),
			fmt.Sprintf("kd-multi-%d-b", keyID), day.Add(26*time.Hour))
		require.NoError(t, err)
	}

	rebuildRollupFor(t, db, day.Add(-24*time.Hour), day.Add(96*time.Hour), tzName)

	start := day.Add(-24 * time.Hour)
	end := day.Add(96 * time.Hour)
	got, err := repo.TotalsForKeys(ctx, keyIDs, start, end, time.Now())
	require.NoError(t, err)
	require.Len(t, got, len(keyIDs))

	// 参照：直接对 usage_logs 求同一窗口的合计。
	for _, keyID := range keyIDs {
		var requests int64
		var actual, total float64
		require.NoError(t, db.QueryRow(`
			SELECT COUNT(*), COALESCE(SUM(actual_cost),0), COALESCE(SUM(total_cost),0)
			FROM usage_logs WHERE api_key_id=$1 AND created_at >= $2 AND created_at < $3`,
			keyID, start, end).Scan(&requests, &actual, &total))
		row := got[keyID]
		require.Equal(t, requests, row.Requests, "key %d requests", keyID)
		require.InDelta(t, actual, row.ActualCost, 1e-9, "key %d actual_cost", keyID)
		require.InDelta(t, total, row.TotalCost, 1e-9, "key %d total_cost", keyID)
	}
}

func TestKeyDailyRollupUsability(t *testing.T) {
	db, ctx := keyDailyTestDB(t)
	repo := newUsageKeyDailyRollupRepositoryWithSQL(db)

	var enabled bool
	require.NoError(t, db.QueryRow(`SELECT enabled FROM usage_key_daily_state WHERE id=1`).Scan(&enabled))

	usable, err := repo.RollupUsable(ctx)
	require.NoError(t, err)
	// 可用性取决于开关与 state 时区；这里只断言「关掉时一定不可用」。
	_, err = db.Exec(`UPDATE usage_key_daily_state SET enabled=false WHERE id=1`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`UPDATE usage_key_daily_state SET enabled=$1 WHERE id=1`, enabled) })

	usable, err = repo.RollupUsable(ctx)
	require.NoError(t, err)
	require.False(t, usable, "开关关闭后必须回落到原始查询")
}

var _ = pq.Array
