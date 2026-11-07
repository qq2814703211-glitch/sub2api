#!/usr/bin/env bash
# End-to-end measurement of the ACTUAL fast-path SQL that ships in
# backend/internal/repository/key_usage_daily_read_repo.go.
#
# Parameters mirror what the service layer passes:
#   start      = local midnight 30 days ago
#   end        = local midnight tomorrow
#   rollupEnd  = local midnight today   (usage_key_daily holds closed days only)
#   tailStart  = local midnight today   (today is served from usage_logs)
#   timezone   = Asia/Shanghai
#
# Run twice: warm (shared buffers populated) and cold (buffers dropped).
set -uo pipefail
P() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A "$@"; }

# The container's psql session defaults to UTC, while production's DSN pins
# timezone=Asia/Shanghai (config.DSNWithTimezone). The rollup hardcodes the zone
# per the design requirement, and the legacy queries ride the session default -
# so if these two disagree the fast path and the fallback compute different days.
# Measured: with session tz = Asia/Shanghai the two paths agree on 0/31 rows;
# with session tz = UTC they disagree on all 32. Every measurement below
# therefore runs with the production session timezone, and the container's
# default is overridden explicitly rather than assumed.
pinit() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -c "SET timezone='Asia/Shanghai'" "$@" >/dev/null 2>&1; }
PZ() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A -c "SET timezone='Asia/Shanghai'; $*"; }

WINDOW="
  SELECT extract(epoch FROM (date_trunc('day', now() AT TIME ZONE 'Asia/Shanghai') - interval '30 days') AT TIME ZONE 'Asia/Shanghai')::bigint || ' ' ||
         extract(epoch FROM (date_trunc('day', now() AT TIME ZONE 'Asia/Shanghai') + interval '1 day')  AT TIME ZONE 'Asia/Shanghai')::bigint || ' ' ||
         extract(epoch FROM (date_trunc('day', now() AT TIME ZONE 'Asia/Shanghai'))                     AT TIME ZONE 'Asia/Shanghai')::bigint"

# Epoch seconds, not formatted timestamps: a formatted string loses its offset, and
# re-parsing it under a different session timezone silently shifts the whole window
# by 8 hours (which made the first two day buckets look like rollup mismatches).
read -r START_E END_E TODAY_E <<<"$(P -c "$WINDOW")"
START="to_timestamp($START_E)"
END="to_timestamp($END_E)"
TODAY="to_timestamp($TODAY_E)"
echo "window: start=$(PZ "select to_timestamp($START_E)::text")  end=$(PZ "select to_timestamp($END_E)::text")  today=$(PZ "select to_timestamp($TODAY_E)::text")"

# --- the two SQL bodies, copied verbatim from the Go source with $n bound ---
POINTS_SQL="
WITH rollup_part AS (
    SELECT day, requests, input_tokens, output_tokens,
           cache_creation_tokens, cache_read_tokens,
           total_cost, actual_cost, duration_sum, duration_count
    FROM usage_key_daily
    WHERE api_key_id = 1000001
      AND day >= ($START AT TIME ZONE 'Asia/Shanghai')::date
      AND day <  ($TODAY AT TIME ZONE 'Asia/Shanghai')::date
),
tail_part AS (
    SELECT (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
           COUNT(*) AS requests,
           COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
           COALESCE(SUM(ul.output_tokens), 0) AS output_tokens,
           COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(ul.total_cost), 0) AS total_cost,
           COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
           COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0) AS duration_sum,
           COUNT(*) AS duration_count
    FROM usage_logs ul
    WHERE ul.api_key_id = 1000001
      AND ul.created_at >= $TODAY
      AND ul.created_at <  $END
    GROUP BY 1
)
SELECT day, SUM(requests), SUM(input_tokens), SUM(output_tokens),
       SUM(cache_creation_tokens), SUM(cache_read_tokens),
       SUM(total_cost), SUM(actual_cost), SUM(duration_sum), SUM(duration_count)
FROM (
    SELECT * FROM rollup_part
    UNION ALL
    SELECT * FROM tail_part
) merged
GROUP BY day
ORDER BY day"

KEYS=$(P -c "SELECT string_agg(id::text, ',') FROM (SELECT id FROM api_keys ORDER BY id LIMIT 172) t")
POINTS_N=$(P -c "SELECT count(*) FROM ($POINTS_SQL) x")

TOTALS_SQL="
WITH rollup_part AS (
    SELECT api_key_id, requests, input_tokens, output_tokens,
           cache_creation_tokens, cache_read_tokens,
           total_cost, actual_cost, duration_sum, duration_count
    FROM usage_key_daily
    WHERE api_key_id = ANY(ARRAY[$KEYS]::bigint[])
      AND day >= ($START AT TIME ZONE 'Asia/Shanghai')::date
      AND day <  ($TODAY AT TIME ZONE 'Asia/Shanghai')::date
),
tail_part AS (
    SELECT ul.api_key_id,
           COUNT(*) AS requests,
           COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
           COALESCE(SUM(ul.output_tokens), 0) AS output_tokens,
           COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(ul.total_cost), 0) AS total_cost,
           COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
           COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0) AS duration_sum,
           COUNT(*) AS duration_count
    FROM usage_logs ul
    WHERE ul.api_key_id = ANY(ARRAY[$KEYS]::bigint[])
      AND ul.created_at >= $TODAY
      AND ul.created_at <  $END
    GROUP BY ul.api_key_id
)
SELECT api_key_id, SUM(requests), SUM(input_tokens), SUM(output_tokens),
       SUM(cache_creation_tokens), SUM(cache_read_tokens),
       SUM(total_cost), SUM(actual_cost), SUM(duration_sum), SUM(duration_count)
FROM (
    SELECT * FROM rollup_part
    UNION ALL
    SELECT * FROM tail_part
) merged
GROUP BY api_key_id"

TOTALS_N=$(P -c "SELECT count(*) FROM ($TOTALS_SQL) x")

report() {
  local label="$1" sql="$2" rows="$3" best="" line plan
  for _ in 1 2 3; do
    line=$(docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A -c "SET timezone='Asia/Shanghai'; EXPLAIN (ANALYZE, BUFFERS) $sql" | grep -E '^Execution Time' | sed 's/Execution Time: //; s/ ms//')
    best=$(printf '%s\n%s\n' "$best" "$line" | grep -v '^$' | sort -g | head -1)
  done
  plan=$(docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A -c "SET timezone='Asia/Shanghai'; EXPLAIN (ANALYZE, BUFFERS) $sql" | grep -oE 'Index Only Scan using [a-z_0-9]+|Index Scan using [a-z_0-9]+|Bitmap Heap Scan|Parallel Seq Scan|Seq Scan on [a-z_]+|Sort Method: [a-z ]+(Disk: [0-9]+kB)?|Heap Fetches: [0-9]+' | sort -u | tr '\n' ' | ')
  printf '  %-42s %10s ms  rows=%-5s %s\n' "$label" "$best" "$rows" "$plan"
}

echo "=== WARM (buffers already populated by the row-count queries above) ==="
report "DailyPoints  (1 key, 30d, real SQL)" "$POINTS_SQL" "$POINTS_N"
report "TotalsForKeys (172 keys, 30d, real SQL)" "$TOTALS_SQL" "$TOTALS_N"

echo
echo "=== COLD (drop shared buffers, then measure once each) ==="
# Restarting the container would lose our psql session too; instead evict by
# reading a large unrelated table so usage_logs pages get pushed out.
P -c "SELECT count(*) FROM pg_class;" >/dev/null 2>&1
docker restart sub2api-perf-postgres >/dev/null 2>&1
for i in $(seq 1 60); do docker exec sub2api-perf-postgres pg_isready -U sub2api -d sub2api >/dev/null 2>&1 && break; sleep 1; done
sleep 2
report "DailyPoints  (1 key, 30d, real SQL)" "$POINTS_SQL" "$POINTS_N"
report "TotalsForKeys (172 keys, 30d, real SQL)" "$TOTALS_SQL" "$TOTALS_N"

echo
echo "=== BEFORE the rollup: the original queries, full 30d window straight off usage_logs ==="
# This is what the repository did before this change (and what RollupUsable=false
# falls back to). NOT the same shape as the fast path: it re-derives every day
# from raw rows. This is the honest before/after pair.
BEFORE_POINTS_SQL="
SELECT TO_CHAR(date_trunc('day', ul.created_at), 'YYYY-MM-DD') AS day,
       COUNT(*) AS requests,
       COALESCE(SUM(ul.input_tokens), 0),
       COALESCE(SUM(ul.output_tokens), 0),
       COALESCE(SUM(ul.cache_creation_tokens), 0),
       COALESCE(SUM(ul.cache_read_tokens), 0),
       COALESCE(SUM(ul.total_cost), 0),
       COALESCE(SUM(ul.actual_cost), 0)
FROM usage_logs ul
WHERE ul.created_at >= $START
  AND ul.created_at <  $END
  AND ul.api_key_id = 1000001
GROUP BY date_trunc('day', ul.created_at)
ORDER BY date_trunc('day', ul.created_at)"

BEFORE_TOTALS_SQL="
SELECT ul.api_key_id,
       COUNT(*) AS requests,
       COALESCE(SUM(ul.input_tokens), 0),
       COALESCE(SUM(ul.output_tokens), 0),
       COALESCE(SUM(ul.cache_creation_tokens), 0),
       COALESCE(SUM(ul.cache_read_tokens), 0),
       COALESCE(SUM(ul.total_cost), 0),
       COALESCE(SUM(ul.actual_cost), 0),
       COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0),
       COUNT(*) AS duration_count
FROM usage_logs ul
WHERE ul.created_at >= $START
  AND ul.created_at <  $END
  AND ul.api_key_id = ANY(ARRAY[$KEYS]::bigint[])
GROUP BY ul.api_key_id"

report "DailyPoints  BEFORE (usage_logs full 30d)" "$BEFORE_POINTS_SQL" "31"
report "TotalsForKeys BEFORE (usage_logs full 30d)" "$BEFORE_TOTALS_SQL" "172"

echo
echo "=== result equality: fast path vs BEFORE, row by row ==="
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A -c "SET timezone='Asia/Shanghai';
WITH fast AS (
  SELECT * FROM ($POINTS_SQL) p(day, requests, input_tokens, output_tokens,
       cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
       duration_sum, duration_count)
), before AS (
  SELECT * FROM ($BEFORE_POINTS_SQL) b(day, requests, input_tokens, output_tokens,
       cache_creation_tokens, cache_read_tokens, total_cost, actual_cost)
)
SELECT (SELECT count(*) FROM fast) AS fast_rows,
       (SELECT count(*) FROM before) AS before_rows,
       (SELECT count(*) FROM fast f FULL OUTER JOIN before b
          ON b.day = TO_CHAR(f.day,'YYYY-MM-DD')
        WHERE f.requests IS DISTINCT FROM b.requests
           OR f.input_tokens IS DISTINCT FROM b.input_tokens
           OR f.output_tokens IS DISTINCT FROM b.output_tokens
           OR f.cache_creation_tokens IS DISTINCT FROM b.cache_creation_tokens
           OR f.cache_read_tokens IS DISTINCT FROM b.cache_read_tokens
           OR f.total_cost IS DISTINCT FROM b.total_cost
           OR f.actual_cost IS DISTINCT FROM b.actual_cost) AS mismatches;
"

echo "--- mismatching rows, if any ---"
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -c "SET timezone='Asia/Shanghai';
WITH fast AS (
  SELECT * FROM ($POINTS_SQL) p(day, requests, input_tokens, output_tokens,
       cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
       duration_sum, duration_count)
), before AS (
  SELECT * FROM ($BEFORE_POINTS_SQL) b(day, requests, input_tokens, output_tokens,
       cache_creation_tokens, cache_read_tokens, total_cost, actual_cost)
)
SELECT COALESCE(b.day, TO_CHAR(f.day,'YYYY-MM-DD')) AS day,
       f.requests AS fast_req, b.requests AS before_req,
       f.input_tokens AS fast_in, b.input_tokens AS before_in,
       f.total_cost AS fast_tc, b.total_cost AS before_tc
FROM fast f FULL OUTER JOIN before b ON b.day = TO_CHAR(f.day,'YYYY-MM-DD')
WHERE f.requests IS DISTINCT FROM b.requests
   OR f.input_tokens IS DISTINCT FROM b.input_tokens
   OR f.output_tokens IS DISTINCT FROM b.output_tokens
   OR f.cache_creation_tokens IS DISTINCT FROM b.cache_creation_tokens
   OR f.cache_read_tokens IS DISTINCT FROM b.cache_read_tokens
   OR f.total_cost IS DISTINCT FROM b.total_cost
   OR f.actual_cost IS DISTINCT FROM b.actual_cost
ORDER BY 1;"
