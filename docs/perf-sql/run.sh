#!/bin/bash
# Run one EXPLAIN ANALYZE SQL file twice; capture first (cold-ish) and second (warm) runs.
# Usage: ./run.sh <sqlfile> <label>
set -uo pipefail
SQL="$1"; LABEL="${2:-$(basename "$SQL" .sql)}"
OUT="/Users/dev/Global-repos/sub2api-011-perf/docs/perf-sql/out"
mkdir -p "$OUT"
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -f - < "$SQL" > "$OUT/${LABEL}.cold.txt" 2>&1
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -f - < "$SQL" > "$OUT/${LABEL}.warm.txt" 2>&1
printf '=== %s ===\n' "$LABEL"
for f in cold warm; do
  printf -- '-- %s: ' "$f"
  grep -oE 'Execution Time: [0-9.]+ ms|Planning Time: [0-9.]+ ms' "$OUT/${LABEL}.${f}.txt" | tr '\n' ' '
  printf '\n'
  grep -oE 'Buffers: shared hit=[0-9]+ read=[0-9]+( dirtied=[0-9]+)?( written=[0-9]+)?' "$OUT/${LABEL}.${f}.txt" | tail -1 | sed 's/^/   /'
  grep -oE '(external merge|external sort)  Disk: [0-9]+kB' "$OUT/${LABEL}.${f}.txt" | sed 's/^/   SORT: /'
done
