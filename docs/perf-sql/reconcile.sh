#!/bin/bash
# 对账：预聚表结果 vs 直接扫 usage_logs，逐行逐字段比对。
# 这是计费相关，金额比到 numeric 全精度，tokens 精确到个。
set -uo pipefail
P() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A "$@"; }

echo "=== [1] 全部 172 个 key × 全部自然日：逐日逐字段比对 ==="
P -c "
WITH raw AS (
  SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS day, api_key_id,
         COUNT(*) req, SUM(input_tokens)::bigint ti, SUM(output_tokens)::bigint to_,
         SUM(cache_creation_tokens)::bigint cc, SUM(cache_read_tokens)::bigint cr,
         SUM(total_cost) tc, SUM(actual_cost) ac,
         SUM(COALESCE(duration_ms,0))::bigint ds
  FROM usage_logs GROUP BY 1,2
), pre AS (
  SELECT day, api_key_id, requests req, input_tokens ti, output_tokens to_,
         cache_creation_tokens cc, cache_read_tokens cr,
         total_cost tc, actual_cost ac, duration_sum ds
  FROM usage_key_daily
)
SELECT
  (SELECT count(*) FROM raw) AS raw_rows,
  (SELECT count(*) FROM pre) AS pre_rows,
  (SELECT count(*) FROM raw FULL OUTER JOIN pre USING (day, api_key_id)
     WHERE raw.req IS DISTINCT FROM pre.req
        OR raw.ti  IS DISTINCT FROM pre.ti
        OR raw.to_ IS DISTINCT FROM pre.to_
        OR raw.cc  IS DISTINCT FROM pre.cc
        OR raw.cr  IS DISTINCT FROM pre.cr
        OR raw.tc  IS DISTINCT FROM pre.tc
        OR raw.ac  IS DISTINCT FROM pre.ac
        OR raw.ds  IS DISTINCT FROM pre.ds) AS mismatches;
"

echo
echo "=== [2] 每 key 的「整自然日窗口」合计：逐 key 比对 ==="
# 窗口必须两端都落在本地日界上——这正是 service 层 onLocalDayBoundary 的存
# 在理由：日桶无法表达「从此刻往前 30 天」这种半点起点，那种窗口一律回落到
# 直扫 usage_logs。这里用同一套对齐窗口比，才是两条路径都该服务的形状。
P -c "
WITH w AS (
  SELECT ((date_trunc('day', now()) - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::date AS d0,
         ((date_trunc('day', now()) + interval '1 day') AT TIME ZONE 'Asia/Shanghai')::date AS d1
), raw AS (
  SELECT x.api_key_id, SUM(x.actual_cost) v, count(*)::bigint r
  FROM (
    SELECT api_key_id, (created_at AT TIME ZONE 'Asia/Shanghai')::date d, actual_cost
    FROM usage_logs
  ) x, w WHERE x.d >= w.d0 AND x.d < w.d1 GROUP BY 1
), pre AS (
  SELECT k.api_key_id, SUM(k.actual_cost) v, SUM(k.requests)::bigint r
  FROM usage_key_daily k, w WHERE k.day >= w.d0 AND k.day < w.d1 GROUP BY 1
)
SELECT count(*) AS keys_compared,
       count(*) FILTER (WHERE raw.v IS DISTINCT FROM pre.v) AS amount_mismatch,
       count(*) FILTER (WHERE raw.r IS DISTINCT FROM pre.r) AS requests_mismatch
FROM raw JOIN pre ON pre.api_key_id = raw.api_key_id;
"

echo
echo "=== [3] 抽样 10 个 key 打印明细（人眼可核，整自然日窗口） ==="
P -c "
WITH w AS (
  SELECT ((date_trunc('day', now()) - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::date AS d0,
         ((date_trunc('day', now()) + interval '1 day') AT TIME ZONE 'Asia/Shanghai')::date AS d1
), raw AS (
  SELECT x.api_key_id, SUM(x.actual_cost) v FROM (
    SELECT api_key_id, (created_at AT TIME ZONE 'Asia/Shanghai')::date d, actual_cost FROM usage_logs
  ) x, w WHERE x.d >= w.d0 AND x.d < w.d1 GROUP BY 1
), pre AS (
  SELECT k.api_key_id, SUM(k.actual_cost) v FROM usage_key_daily k, w WHERE k.day >= w.d0 AND k.day < w.d1 GROUP BY 1
)
SELECT raw.api_key_id, raw.v AS raw_30d, pre.v AS pre_30d,
       CASE WHEN raw.v IS NOT DISTINCT FROM pre.v THEN 'OK' ELSE 'MISMATCH' END
FROM raw JOIN pre ON pre.api_key_id = raw.api_key_id
ORDER BY raw.api_key_id LIMIT 10;
"

echo
echo "=== [4] 跨日界样本：本地 00:00-01:00 的日志，UTC 日 vs 上海日 ==="
P -c "
SELECT to_char(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD HH24:MI') AS local_time,
       (created_at AT TIME ZONE 'UTC')::date           AS utc_day,
       (created_at AT TIME ZONE 'Asia/Shanghai')::date AS shanghai_day
FROM usage_logs
WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::time >= '00:00'::time
  AND (created_at AT TIME ZONE 'Asia/Shanghai')::time <  '01:00'::time
LIMIT 3;
"

echo
echo "=== [5] 口径差异规模：用 UTC 分桶会让多少行落到错误的日 ==="
P -c "
SELECT count(*) AS total,
       count(*) FILTER (WHERE (created_at AT TIME ZONE 'UTC')::date
                            <> (created_at AT TIME ZONE 'Asia/Shanghai')::date) AS divergent,
       round(100.0 * count(*) FILTER (WHERE (created_at AT TIME ZONE 'UTC')::date
                            <> (created_at AT TIME ZONE 'Asia/Shanghai')::date) / count(*), 2) AS pct
FROM usage_logs;
"
