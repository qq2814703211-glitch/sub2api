#!/usr/bin/env bash
# Configure and fire the V3 probe for real, then read back what it recorded.
#
# The V3 probe is supposed to work with ONLY this page's config: no group
# allowlist, no price card, no V1/V2 settings. So the honest test is to point it
# at an endpoint, enable one group, press probe, and check a status bucket or a
# probe result actually appears — not merely that the route answers 200.
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8081}"
PG="docker exec sub2api-acceptance-postgres psql -U sub2api -d sub2api -Atqc"

TOKEN=$(curl -s -m 15 -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@sub2api.local","password":"LocalAcceptance!2026"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["access_token"])' 2>/dev/null)
[ -z "${TOKEN:-}" ] && { echo "FATAL: no token"; exit 1; }
AUTH="Authorization: Bearer $TOKEN"
JSON='Content-Type: application/json'

curl -s -o /dev/null -m 15 -X POST "$BASE/api/v1/admin/compliance/accept" \
  -H "$AUTH" -H "$JSON" \
  -d '{"phrase":"我已阅读、理解并同意 Sub2API 部署与运营合规承诺","language":"zh"}'

echo "=== 1. a group to monitor, entirely from the V3 page ==="
GID=$($PG "SELECT id FROM groups ORDER BY id LIMIT 1")
echo "  group_id=$GID (no model allowlist, no price card touched)"

echo
echo "=== 2. enable V3 with its own probe identity ==="
# The endpoint validator hard-requires https (inherited V1 rule), so a loopback
# http URL is rejected before any probe runs. Use a real https target: the probe
# will come back as an auth/404 failure, which is exactly what proves V3 reached
# the upstream on its own config and recorded the outcome.
# The config row is version-guarded (optimistic locking): sending a stale
# version fails with 409 and silently leaves V3 disabled. Read the current one.
VER=$(curl -s -m 15 -H "$AUTH" "$BASE/api/v1/admin/channel-monitor-v3/config" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["version"])' 2>/dev/null)
echo "  current version=$VER"
body=$(python3 - "$GID" "$VER" <<'PY'
import json, sys
gid = int(sys.argv[1]); ver = int(sys.argv[2])
print(json.dumps({
    "version": ver,
    "enabled": True,
    "groups": [{"group_id": gid, "enabled": True, "models": [], "probe_enabled": True}],
    "probe": {
        "endpoint": "https://api.openai.com/v1",
        "provider": "openai",
        "primary_model": "gpt-4o-mini",
        "extra_models": [],
        "interval_seconds": 300,
    },
    "api_key": "sk-local-probe-placeholder",
    "cache_average_window_days": 30,
}, ensure_ascii=False))
PY
)
curl -s -m 15 -X PUT "$BASE/api/v1/admin/channel-monitor-v3/config" \
  -H "$AUTH" -H "$JSON" -d "$body" | head -c 500; echo

echo
echo "=== 3. read the saved config back ==="
curl -s -m 15 -H "$AUTH" "$BASE/api/v1/admin/channel-monitor-v3/config" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin)["data"]; print("  enabled:", d["enabled"], "| key_set:", d["api_key_set"], "| groups:", len(d["groups"]), "| probe:", d["probe"]["endpoint"], d["probe"]["primary_model"])'

echo
echo "=== 4. fire the probe now ==="
code=$(curl -s -o /tmp/v3_probe.json -w '%{http_code}' -m 60 \
  -X POST "$BASE/api/v1/admin/channel-monitor-v3/probe" -H "$AUTH")
echo "  http=$code"; head -c 400 /tmp/v3_probe.json; echo

echo
echo "=== 5. what did the probe record? (buckets / status) ==="
$PG "SELECT 'buckets='||count(*) FROM channel_monitor_v3_buckets" 2>/dev/null \
  || $PG "SELECT 'no buckets table'"
curl -s -m 20 -H "$AUTH" "$BASE/api/v1/admin/channel-monitor-v3/status?group_id=$GID" | head -c 400; echo
