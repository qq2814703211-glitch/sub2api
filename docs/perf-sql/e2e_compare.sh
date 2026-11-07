#!/bin/bash
# 端到端对比：走预聚表 vs 直扫 usage_logs。
# 三条路径都是真实业务 SQL 的等价形式（见 docs/perf-0.1.1-baseline.md 的出处）。
set -uo pipefail
P() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A "$@"; }
OUT=/Users/dev/Global-repos/sub2api-011-perf/docs/perf-sql/out
mkdir -p "$OUT"

IDS=$(python3 -c "print(','.join(str(1000001+i) for i in range(172)))")
# 热 key：跑库里最大那一个
HOT=$(P -c "select api_key_id from usage_logs group by 1 order by count(*) desc limit 1" | tr -d ' ')
echo "hot key = $HOT"

measure() { # $1 label, $2 runs, $3 sql
  local label="$1" runs="${2:-3}" sql="$3"
  P -c "EXPLAIN (ANALYZE, BUFFERS) $sql" >/dev/null 2>&1   # warm once
  local best=999999 total=0 i ms
  for i in $(seq 1 "$runs"); do
    ms=$(P -c "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) $sql" 2>/dev/null \
         | jq -r '.[0]."Execution Time" // 999999')
    total=$(echo "$total + $ms" | bc)
    best=$(echo "if ($ms < $best) $ms else $best" | bc)
  done
  local avg; avg=$(echo "scale=3; $total / $runs" | bc)
  printf '%-44s avg=%10.3f ms   min=%10.3f ms  (n=%s, warm)\n' "$label" "$avg" "$best" "$runs"
  printf '{"label":"%s","avg_ms":%s,"min_ms":%s,"runs":%s}\n' "$label" "$avg" "$best" "$runs" >> "$OUT/e2e.jsonl"
}

: > "$OUT/e2e.jsonl"

# ---- 单 key 日趋势（GetAPIKeyDailyUsage 原路径） ----
RAW_TREND="SELECT TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD') AS date, COUNT(*),
  COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
  COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
  COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens),0),
  COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0)
FROM usage_logs
WHERE created_at >= (date_trunc('day', now()) - interval '30 days')
  AND created_at < (date_trunc('day', now()) + interval '1 day')
  AND api_key_id = $HOT
GROUP BY date_trunc('day', created_at) ORDER BY date_trunc('day', created_at)"

# 新路径：已关闭日来自预聚表 + 今天由 usage_logs 补（与 keyDailyPointsSQL 同形）
ROLL_TREND="WITH rollup_part AS (
  SELECT day, requests, input_tokens, output_tokens, cache_creation_tokens,
         cache_read_tokens, total_cost, actual_cost
  FROM usage_key_daily
  WHERE api_key_id = $HOT
    AND day >= ((date_trunc('day', now()) - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::date
    AND day <  (date_trunc('day', now()) AT TIME ZONE 'Asia/Shanghai')::date
), tail_part AS (
  SELECT (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day, COUNT(*),
         COALESCE(SUM(ul.input_tokens),0), COALESCE(SUM(ul.output_tokens),0),
         COALESCE(SUM(ul.cache_creation_tokens),0), COALESCE(SUM(ul.cache_read_tokens),0),
         COALESCE(SUM(ul.total_cost),0), COALESCE(SUM(ul.actual_cost),0)
  FROM usage_logs ul
  WHERE ul.api_key_id = $HOT
    AND ul.created_at >= date_trunc('day', now())
    AND ul.created_at <  (date_trunc('day', now()) + interval '1 day')
  GROUP BY 1
)
SELECT day, SUM(requests), SUM(input_tokens), SUM(output_tokens),
       SUM(cache_creation_tokens), SUM(cache_read_tokens),
       SUM(total_cost), SUM(actual_cost)
FROM (SELECT * FROM rollup_part UNION ALL SELECT * FROM tail_part) m
GROUP BY day ORDER BY day"

# ---- 单 key 区间汇总（GetAPIKeyUsageSummary 原路径） ----
RAW_SUMMARY="SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
  COALESCE(SUM(cache_creation_tokens+cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0),
  COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0),
  COALESCE(AVG(COALESCE(duration_ms,0)),0)
FROM usage_logs WHERE api_key_id = $HOT
  AND created_at >= date_trunc('day', now()) - interval '30 days'
  AND created_at <  date_trunc('day', now()) + interval '1 day'"
ROLL_SUMMARY="SELECT SUM(requests), SUM(input_tokens), SUM(output_tokens),
  SUM(cache_creation_tokens+cache_read_tokens), SUM(cache_creation_tokens), SUM(cache_read_tokens),
  SUM(total_cost), SUM(actual_cost), 0
FROM usage_key_daily WHERE api_key_id = $HOT
  AND day >= ((date_trunc('day', now()) - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::date
  AND day <  ((date_trunc('day', now()) + interval '1 day') AT TIME ZONE 'Asia/Shanghai')::date"

# ---- 批量 172 key（GetBatchAPIKeyUsageStats 原路径） ----
RAW_BATCH="SELECT api_key_id,
  COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= now() - interval '30 days' AND created_at < now()), 0),
  COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= date_trunc('day', now())), 0)
FROM usage_logs
WHERE api_key_id = ANY(ARRAY[$IDS]::bigint[])
  AND created_at >= LEAST(now() - interval '30 days', date_trunc('day', now()))
GROUP BY api_key_id"
ROLL_BATCH="WITH rollup_part AS (
  SELECT api_key_id, SUM(actual_cost) AS actual_cost
  FROM usage_key_daily
  WHERE api_key_id = ANY(ARRAY[$IDS]::bigint[])
    AND day >= ((now() - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::date
    AND day <  (date_trunc('day', now()) AT TIME ZONE 'Asia/Shanghai')::date
  GROUP BY api_key_id
), tail_part AS (
  SELECT ul.api_key_id, COALESCE(SUM(ul.actual_cost),0) AS actual_cost
  FROM usage_logs ul
  WHERE ul.api_key_id = ANY(ARRAY[$IDS]::bigint[])
    AND ul.created_at >= date_trunc('day', now())
    AND ul.created_at < now()
  GROUP BY ul.api_key_id
), today_part AS (
  SELECT ul.api_key_id, COALESCE(SUM(ul.actual_cost),0) AS actual_cost
  FROM usage_logs ul
  WHERE ul.api_key_id = ANY(ARRAY[$IDS]::bigint[])
    AND ul.created_at >= date_trunc('day', now())
  GROUP BY ul.api_key_id
)
SELECT k.id,
       COALESCE(r.actual_cost,0) + COALESCE(t.actual_cost,0),
       COALESCE(td.actual_cost,0)
FROM unnest(ARRAY[$IDS]::bigint[]) AS k(id)
LEFT JOIN rollup_part r ON r.api_key_id = k.id
LEFT JOIN tail_part   t ON t.api_key_id = k.id
LEFT JOIN today_part td ON td.api_key_id = k.id"

echo
echo "=== 单 key 30 天日趋势（hot key ${HOT}） ==="
measure "trend.raw_usage_logs"   2 "$RAW_TREND"
measure "trend.rollup_key_daily" 2 "$ROLL_TREND"
echo
echo "=== 单 key 30 天区间汇总 ==="
measure "summary.raw_usage_logs"   2 "$RAW_SUMMARY"
measure "summary.rollup_key_daily" 2 "$ROLL_SUMMARY"
echo
echo "=== 批量 172 key today/total ==="
measure "batch.raw_usage_logs"   2 "$RAW_BATCH"
measure "batch.rollup_key_daily" 2 "$ROLL_BATCH"
