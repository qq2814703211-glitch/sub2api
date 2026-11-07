#!/bin/bash
# Measure the same query shapes with jit on vs off, warm cache both times.
set -uo pipefail
P="docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A"

q() { # $1 label, $2 jit setting, $3 sql
  local out
  out=$($P -c "set jit=$2" -c "EXPLAIN (ANALYZE, BUFFERS) $3" 2>&1 | grep -E 'Execution Time')
  printf '%-28s jit=%-3s %s\n' "$1" "$2" "$out"
}

KEY_SUMMARY="SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_tokens+cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0), COALESCE(AVG(COALESCE(duration_ms,0)),0) FROM usage_logs WHERE api_key_id = 1000001 AND created_at >= now() - interval '30 days' AND created_at < now()"

KEY_DAILY="SELECT TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD'), COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE created_at >= (date_trunc('day', now()) - interval '30 days') AND created_at < (date_trunc('day', now()) + interval '1 day') AND api_key_id = 1000001 GROUP BY date_trunc('day', created_at) ORDER BY date_trunc('day', created_at) ASC"

BATCH_KEYS="SELECT api_key_id, COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= now()-interval '30 days' AND created_at < now()),0), COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= date_trunc('day', now())),0) FROM usage_logs WHERE api_key_id = ANY(ARRAY(SELECT generate_series(1000001,1000172))) AND created_at >= LEAST(now()-interval '30 days', date_trunc('day', now())) GROUP BY api_key_id"

GLOBAL_30D="SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_tokens+cache_read_tokens),0), COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0), COALESCE(AVG(duration_ms),0) FROM usage_logs WHERE created_at >= now() - interval '30 days' AND created_at < now()"

USER_30D="SELECT user_id, COALESCE(SUM(actual_cost),0), COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= date_trunc('day',now())),0) FROM usage_logs WHERE user_id = ANY(ARRAY(SELECT generate_series(1,50))) AND created_at >= now()-interval '30 days' GROUP BY user_id"

for pair in on off; do
  q "key_summary_30d"  "$pair" "$KEY_SUMMARY"
  q "key_daily_trend"  "$pair" "$KEY_DAILY"
  q "batch_keys_172"   "$pair" "$BATCH_KEYS"
  q "global_30d"       "$pair" "$GLOBAL_30D"
  q "users_50_30d"     "$pair" "$USER_30D"
  echo
done
