#!/bin/bash
set -uo pipefail
R="/tmp/rollup_q.sql"
echo "=== jit on (run1) ==="
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -f - < "$R" 2>&1 | grep -E "JIT:|Execution Time|Functions:"
echo "=== jit on (run2) ==="
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -f - < "$R" 2>&1 | grep -E "JIT:|Execution Time|Functions:"
echo "=== jit off (run1) ==="
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -c "set jit=off" -f - < "$R" 2>&1 | grep -E "Execution Time"
echo "=== jit off (run2) ==="
docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -c "set jit=off" -f - < "$R" 2>&1 | grep -E "Execution Time"
