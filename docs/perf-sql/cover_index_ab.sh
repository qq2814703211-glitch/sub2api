#!/usr/bin/env bash
# Three-state A/B for the per-key covering index on the keys page queries.
#   state A: no covering index          (what a fresh DB without the migration has)
#   state B: INCLUDE (actual_cost)      (the definition that exists on production today)
#   state C: INCLUDE (all read columns) (what migration 918 creates)
# Each state is measured with the same two queries, warm cache, three runs, best taken.
set -uo pipefail
P() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A "$@"; }

KEY=1000001
SUM_Q="SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_tokens+cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0), COALESCE(AVG(COALESCE(duration_ms,0)),0) FROM usage_logs WHERE api_key_id = ${KEY} AND created_at >= date_trunc('day', now()) - interval '30 days' AND created_at < date_trunc('day', now()) + interval '1 day'"
TREND_Q="SELECT TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD'), COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE created_at >= (date_trunc('day', now()) - interval '30 days') AND created_at < (date_trunc('day', now()) + interval '1 day') AND api_key_id = ${KEY} GROUP BY date_trunc('day', created_at) ORDER BY date_trunc('day', created_at)"

measure() { # $1 = label, $2 = sql
  local label="$1" sql="$2" best="" line
  for _ in 1 2 3; do
    line=$(P -c "EXPLAIN (ANALYZE, BUFFERS) $sql" | grep -E '^Execution Time' | sed 's/Execution Time: //; s/ ms//')
    best=$(printf '%s\n%s\n' "$best" "$line" | grep -v '^$' | sort -g | head -1)
  done
  local plan
  plan=$(P -c "EXPLAIN (ANALYZE, BUFFERS) $sql" | grep -oE 'Index Only Scan using [a-z_0-9]+|Bitmap Heap Scan|Parallel Seq Scan|Seq Scan on usage_logs|Sort Method: [a-z ]+(Disk: [0-9]+kB)?|Heap Fetches: [0-9]+' | sort -u | tr '\n' ' | ')
  printf '  %-8s %10s ms   %s\n' "$label" "$best" "$plan"
}

echo "=== state A: NO covering index ==="
P -c "DROP INDEX CONCURRENTLY IF EXISTS idx_usage_logs_apikey_created_cover;" >/dev/null 2>&1
P -c "VACUUM ANALYZE usage_logs;" >/dev/null 2>&1
measure summary "$SUM_Q"
measure trend   "$TREND_Q"

echo "=== state B: INCLUDE (actual_cost)  [production definition] ==="
P -c "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_apikey_created_cover ON usage_logs (api_key_id, created_at) INCLUDE (actual_cost);" 2>&1 | tail -1
P -c "VACUUM ANALYZE usage_logs;" >/dev/null 2>&1
echo "  index size: $(P -c "select pg_size_pretty(pg_relation_size('idx_usage_logs_apikey_created_cover'));")"
measure summary "$SUM_Q"
measure trend   "$TREND_Q"

echo "=== state C: INCLUDE (all read columns)  [migration 918] ==="
P -c "DROP INDEX CONCURRENTLY IF EXISTS idx_usage_logs_apikey_created_cover;" >/dev/null 2>&1
P -c "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_apikey_created_cover ON usage_logs (api_key_id, created_at) INCLUDE (input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms);" 2>&1 | tail -1
P -c "VACUUM ANALYZE usage_logs;" >/dev/null 2>&1
echo "  index size: $(P -c "select pg_size_pretty(pg_relation_size('idx_usage_logs_apikey_created_cover'));")"
measure summary "$SUM_Q"
measure trend   "$TREND_Q"

echo
echo "=== write cost of each state (100k-row COPY into a clone, index rebuilt) ==="
