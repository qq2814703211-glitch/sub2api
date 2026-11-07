#!/bin/bash
# Write-cost A/B on INSERT: marginal cost per index.
# Each stage copies the same 1M staged rows into a fresh table with N indexes.
set -uo pipefail
PSQL="docker exec -i sub2api-perf-postgres psql -U sub2api -d sub2api -X -q -v ON_ERROR_STOP=1"

run_stage() {
  local N="$1"; shift
  local idx=("$@")
  $PSQL <<SQL
DROP TABLE IF EXISTS ab_t;
CREATE TABLE ab_t (LIKE usage_logs INCLUDING DEFAULTS);
SQL
  for d in "${idx[@]:-}"; do
    [ -z "$d" ] && continue
    $PSQL -c "CREATE INDEX ON ab_t $d" >/dev/null
  done
  $PSQL -c "ANALYZE ab_t" >/dev/null
  local out
  out=$($PSQL -c "\\timing on" -c "INSERT INTO ab_t SELECT * FROM ab_src" 2>&1 | grep -oE 'Time: [0-9.]+ ms' | tail -1)
  local nidx
  nidx=$($PSQL -tAc "select count(*) from pg_indexes where tablename='ab_t'")
  printf 'stage=%-2s indexes=%-2s insert=%s\n' "$N" "$nidx" "$out"
}

ALL=(
 "(account_id, created_at)"
 "(account_id)"
 "(api_key_id, created_at)"
 "(api_key_id)"
 "(billing_type)"
 "(created_at)"
 "(created_at, model, upstream_model)"
 "(created_at, requested_model, upstream_model)"
 "(COALESCE(NULLIF(btrim(requested_model::text), ''::text), model::text), created_at DESC, id DESC)"
 "(COALESCE(NULLIF(btrim(upstream_model::text), ''::text), model::text), created_at DESC, id DESC)"
 "(group_id, created_at) WHERE group_id IS NOT NULL"
 "(group_id)"
 "(ip_address)"
 "(model)"
 "(model, created_at)"
 "(request_id, api_key_id)"
 "(request_type, created_at)"
 "(service_tier, created_at)"
 "(subscription_id, created_at)"
 "(subscription_id)"
 "(created_at DESC, id DESC) WHERE upstream_model_mismatch IS TRUE"
 "(upstream_request_id) WHERE upstream_request_id IS NOT NULL"
 "(user_id, created_at)"
 "(user_id)"
 "(api_key_id, created_at DESC, id DESC) INCLUDE (ip_address) WHERE ip_address IS NOT NULL AND ip_address::text <> ''::text"
)

run_stage 0
run_stage 5   "${ALL[@]:0:5}"
run_stage 10  "${ALL[@]:0:10}"
run_stage 15  "${ALL[@]:0:15}"
run_stage 20  "${ALL[@]:0:20}"
run_stage 26  "${ALL[@]:0:26}"
$PSQL -c "DROP TABLE IF EXISTS ab_t" >/dev/null
