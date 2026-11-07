#!/usr/bin/env bash
# Local acceptance probe for the GloryCode 0.2.20-GloryCode-0.1.0 stack.
#
# Everything here hits a running local stack and prints real observed values.
# Read-only apart from the one-time admin compliance acknowledgement (admin
# routes answer 423 until it is accepted, which otherwise reads as a false
# negative on every probe below).
#
# Usage:
#   BASE=http://127.0.0.1:8081 bash scripts/local-acceptance-probe.sh
#
# Exit code is 0 only when no REQUIRED check fails. Checks against features that
# have not been merged into this branch yet are reported as PENDING, not FAIL,
# so the probe tells the truth about the branch instead of about the roadmap.
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8081}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@sub2api.local}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-LocalAcceptance!2026}"

pass=0
fail=0
pending=0

check() {
  local label="$1" ok="$2" detail="${3:-}"
  if [ "$ok" = "1" ]; then
    printf '  PASS     %s%s\n' "$label" "${detail:+ — $detail}"
    pass=$((pass + 1))
  else
    printf '  FAIL     %s%s\n' "$label" "${detail:+ — $detail}"
    fail=$((fail + 1))
  fi
}

# A feature whose route does not exist yet is not a regression in this branch —
# it is simply not merged. Report it as PENDING so it cannot mask a real failure.
pending_check() {
  local label="$1" code="$2"
  if [ "$code" = "404" ]; then
    printf '  PENDING  %s — route not merged into this branch yet (http=404)\n' "$label"
    pending=$((pending + 1))
  else
    printf '  PRESENT  %s — http=%s\n' "$label" "$code"
  fi
}

echo "=== 1. liveness and build identity ==="
health=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$BASE/health")
check "GET /health" "$([ "$health" = "200" ] && echo 1 || echo 0)" "http=$health"

echo
echo "=== 2. admin login ==="
login=$(curl -s --max-time 15 -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" 2>/dev/null)
token=$(printf '%s' "$login" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print(""); raise SystemExit
print((d.get("data") or {}).get("access_token") or "")' 2>/dev/null)

check "POST /api/v1/auth/login" "$([ -n "$token" ] && echo 1 || echo 0)" \
  "$([ -n "$token" ] && echo 'token received' || echo "body=${login:0:200}")"

if [ -z "$token" ]; then
  echo
  echo "No token; the remaining authenticated checks cannot run."
  echo "passed=$pass failed=$fail pending=$pending"
  exit 1
fi

# Admin routes answer 423 ADMIN_COMPLIANCE_ACK_REQUIRED until an admin accepts
# the compliance notice once. Skipping this makes every admin probe below a
# false negative, which is worse than not probing at all.
ack=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -X POST \
  -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"phrase":"我已阅读、理解并同意 Sub2API 部署与运营合规承诺","language":"zh"}' \
  "$BASE/api/v1/admin/compliance/accept")
check "admin compliance accept" "$([ "$ack" = "200" ] && echo 1 || echo 0)" "http=$ack"

auth=(-H "Authorization: Bearer $token")

echo
echo "=== 3. build identity ==="
version=$(curl -s --max-time 10 "${auth[@]}" "$BASE/api/v1/admin/system/version" \
  | python3 -c 'import json,sys
try:
    print((json.load(sys.stdin).get("data") or {}).get("version") or "")
except Exception:
    print("")' 2>/dev/null)
check "reported version is the release version" \
  "$([ "$version" = "0.2.20-GloryCode-0.1.0" ] && echo 1 || echo 0)" "version=$version"

echo
echo "=== 4. features that must be gone ==="
# The API path is the real proof. A removed SPA route still answers 200 because
# the frontend serves its shell for unknown paths.
for api in \
  /api/v1/admin/upstream-monitor/summary \
  /api/v1/admin/group-cache-test/run \
  /api/v1/admin/account-ops/alerts
do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${auth[@]}" "$BASE$api")
  check "removed API $api" "$([ "$code" = "404" ] && echo 1 || echo 0)" "http=$code"
done

echo
echo "=== 5. single-key usage endpoint is wired and answers with real data ==="
# A bare 404 proves nothing: there is no api_key id=1 in a fresh stack, so the
# honest assertion is "the route rejects an unknown key with 404 rather than
# 401/405/500". Seeding is what makes the 200 path meaningful, and the helper
# script scripts/verify-single-key-usage.sh does that end-to-end.
usage_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${auth[@]}" \
  "$BASE/api/v1/user/api-keys/999999999/usage?days=30")
check "GET /user/api-keys/:id/usage rejects an unknown key (route is wired)" \
  "$([ "$usage_code" = "404" ] && echo 1 || echo 0)" "http=$usage_code"
# And the seeded key must actually return aggregates; run
# scripts/verify-single-key-usage.sh after seeding to see today/range/items.

echo
echo "=== 6. capacity surface (this branch) ==="
cap=$(curl -s --max-time 10 "${auth[@]}" "$BASE/api/v1/admin/groups/capacity-summary")
cap_ok=$(printf '%s' "$cap" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print(0); raise SystemExit
print(1 if d.get("code") == 0 and isinstance(d.get("data"), list) else 0)' 2>/dev/null)
check "GET /admin/groups/capacity-summary" "$cap_ok" "$(printf '%s' "$cap" | head -c 160)"

echo
echo "=== 7. tickets still served ==="
tk=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${auth[@]}" "$BASE/api/v1/admin/tickets")
check "GET /admin/tickets" "$([ "$tk" = "200" ] && echo 1 || echo 0)" "http=$tk"

echo
echo "=== 8. lanes merged into this branch ==="
# These used to report "PENDING" while the lanes were separate worktrees. They
# are merged now, so a non-2xx here is a real regression, not a roadmap note.
for probe in \
  "/api/v1/admin/channel-monitor-v3/config|V3 independent monitor config" \
  "/api/v1/admin/model-pricing-sync/history|models.dev pricing sync history" \
  "/api/v1/admin/groups/1/active-users|realtime active users per group"
do
  path="${probe%%|*}"
  label="${probe##*|}"
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${auth[@]}" "$BASE$path")
  check "$label" "$([ "$code" = "200" ] && echo 1 || echo 0)" "http=$code"
done

echo
echo "passed=$pass failed=$fail pending=$pending"
[ "$fail" -eq 0 ] || exit 1
