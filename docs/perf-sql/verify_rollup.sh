#!/bin/bash
# Correctness: rollup read must produce byte-identical totals vs the raw scan.
set -uo pipefail
IDS=$(python3 -c "print(','.join(str(1000001+i) for i in range(172)))")
RAW="SELECT api_key_id, ROUND(COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= now() - interval '30 days' AND created_at < now()),0),8) t, ROUND(COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= date_trunc('day', now())),0),8) d FROM usage_logs WHERE api_key_id = ANY(ARRAY[$IDS]::bigint[]) AND created_at >= LEAST(now() - interval '30 days', date_trunc('day', now())) GROUP BY api_key_id ORDER BY api_key_id"
P="docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A"

$P -c "$RAW" > /tmp/raw_totals.txt
# rollup readSQL with the same window -> id,total,today
sed -e "s/EXPLAIN (ANALYZE, BUFFERS, TIMING) //" /tmp/rollup_q.sql \
  | sed -e 's/SELECT requested.id,COALESCE(t.total,0),COALESCE(t.today,0),active.enabled/SELECT requested.id,ROUND(COALESCE(t.total,0),8),ROUND(COALESCE(t.today,0),8)/' \
  > /tmp/rollup_q_plain.sql
$P -f - < /tmp/rollup_q_plain.sql | awk -F'|' '{print $1"|"$2"|"$3}' | sort -t'|' -k1,1n > /tmp/rollup_totals.txt
sort -t'|' -k1,1n /tmp/raw_totals.txt > /tmp/raw_totals_sorted.txt

echo "--- raw rows: $(wc -l < /tmp/raw_totals_sorted.txt)  rollup rows: $(wc -l < /tmp/rollup_totals.txt)"
if diff -q /tmp/raw_totals_sorted.txt /tmp/rollup_totals.txt >/dev/null; then
  echo "IDENTICAL: rollup totals match raw scan for all 172 keys"
else
  echo "MISMATCH — first 10 diff lines:"
  diff /tmp/raw_totals_sorted.txt /tmp/rollup_totals.txt | head -20
fi
