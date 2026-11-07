#!/bin/bash
# Full perf baseline / after harness. Every query mirrors real repo SQL
# (code locations recorded in docs/perf-0.1.1-baseline.md).
#
# Usage: bash bench.sh <label>
#   writes out/<label>.jsonl + prints one line per measurement.
#
# Cold = fresh backend (DISCARD ALL) with no plan cache; note that shared_buffers
# may still hold pages from earlier runs, so "cold" here is a *plan-cold* run.
# Warm = immediate rerun in the same backend. Both are reported; the report says
# which. To force a truly buffer-cold run the harness calls drop_caches() first.
set -uo pipefail
LABEL="${1:-baseline}"
OUT=/Users/dev/Global-repos/sub2api-011-perf/docs/perf-sql/out
mkdir -p "$OUT"
JSONL="$OUT/${LABEL}.jsonl"; : > "$JSONL"

PSQL() { docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -t -A -v ON_ERROR_STOP=1 "$@"; }

drop_caches() {
  # Restart-free buffer flush: the perf container has no pg_prewarm/clear_cache.
  # `pg_ctl restart` is too heavy for every query, so we ask Postgres to forget
  # the shared buffers via a crash-free restart only when COLD=1.
  if [ "${COLD:-0}" = "1" ]; then
    docker exec sub2api-perf-postgres psql -U sub2api -d postgres -X -q -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='sub2api' AND pid<>pg_backend_pid()" >/dev/null 2>&1
    docker restart sub2api-perf-postgres >/dev/null 2>&1
    for _ in $(seq 1 60); do
      docker exec sub2api-perf-postgres pg_isready -U sub2api -d sub2api >/dev/null 2>&1 && break
      sleep 1
    done
  fi
}

run() {
  local name="$1" sql="$2"
  local cold warm
  cold=$(PSQL -c "DISCARD ALL" -c "EXPLAIN (ANALYZE, BUFFERS, TIMING, FORMAT JSON) $sql" 2>&1)
  warm=$(PSQL -c "EXPLAIN (ANALYZE, BUFFERS, TIMING, FORMAT JSON) $sql" 2>&1)

  local c_exec w_exec w_sort w_scan c_sort c_hit c_read w_hit w_read
  c_exec=$(printf '%s' "$cold" | jq -r '.[0]."Execution Time" // empty' 2>/dev/null)
  w_exec=$(printf '%s' "$warm" | jq -r '.[0]."Execution Time" // empty' 2>/dev/null)
  c_sort=$(printf '%s' "$cold" | jq -r '[.[0].Plan|..|objects|select(has("Sort Method"))|"\(.Node Type)=\(.Sort Method // "?")"]|unique|join(";")' 2>/dev/null)
  w_sort=$(printf '%s' "$warm" | jq -r '[.[0].Plan|..|objects|select(has("Sort Method"))|"\(.Node Type)=\(.Sort Method // "?")"]|unique|join(";")' 2>/dev/null)
  w_scan=$(printf '%s' "$warm" | jq -r '[.[0].Plan|..|objects|select(.Node Type|test("Scan"))|.Node Type]|group_by(.)|map("\(.[0])x\(length)")|join(",")' 2>/dev/null)
  c_hit=$(printf '%s' "$cold" | jq -r '[.[0].Plan|..|objects|.Shared Hit Blocks//empty]|add//0' 2>/dev/null)
  c_read=$(printf '%s' "$cold" | jq -r '[.[0].Plan|..|objects|.Shared Read Blocks//empty]|add//0' 2>/dev/null)
  w_hit=$(printf '%s' "$warm" | jq -r '[.[0].Plan|..|objects|.Shared Hit Blocks//empty]|add//0' 2>/dev/null)
  w_read=$(printf '%s' "$warm" | jq -r '[.[0].Plan|..|objects|.Shared Read Blocks//empty]|add//0' 2>/dev/null)

  if [ -z "$c_exec" ]; then
    printf '%-42s ERROR: %s\n' "$name" "$(printf '%s' "$cold" | head -3 | tr '\n' ' ')"
    printf '{"name":"%s","error":%s}\n' "$name" "$(printf '%s' "$cold" | jq -Rs .)" >> "$JSONL"
    return
  fi
  printf '%-42s cold=%9.1f warm=%9.1f ms | warm hit=%-8s read=%-6s | %s%s\n' \
    "$name" "$c_exec" "$w_exec" "$w_hit" "$w_read" "$w_scan" "${w_sort:+ | SORT:$w_sort}"
  printf '{"name":"%s","cold_ms":%s,"warm_ms":%s,"cold_hit":%s,"cold_read":%s,"warm_hit":%s,"warm_read":%s,"warm_sort":"%s","warm_scan":"%s"}\n' \
    "$name" "$c_exec" "$w_exec" "$c_hit" "$c_read" "$w_hit" "$w_read" "$w_sort" "$w_scan" >> "$JSONL"
}

echo "### bench: $LABEL  (COLD=${COLD:-0})"
drop_caches
source /Users/dev/Global-repos/sub2api-011-perf/docs/perf-sql/queries.sh
echo "### wrote $JSONL"
