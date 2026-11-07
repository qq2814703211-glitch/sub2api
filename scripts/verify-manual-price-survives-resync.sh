#!/usr/bin/env bash
# Verify on the running stack that a manual price edit survives the next sync,
# including the ordering that used to be broken: sync writes first, operator
# re-prices afterwards.
#
# This drives the real state instead of trusting unit tests: the override file
# gets a value, the history table gets a row saying "the sync wrote a DIFFERENT
# value", and the service is restarted so its startup sync really runs.
set -uo pipefail

CTN=sub2api-release-0210-sub2api-1
PG="docker exec sub2api-acceptance-postgres psql -U sub2api -d sub2api -Atqc"
MODEL=amazon.nova-micro-v1:0
MANUAL=9.99e-7
LAST_SYNCED=3.5e-8

echo "=== 1. set a manual price in the override file ==="
docker cp "$CTN":/app/data/pricing.override.json /tmp/ovr_manual.json 2>/dev/null
python3 - "$MODEL" "$MANUAL" <<'PY'
import json, sys
model, manual = sys.argv[1], float(sys.argv[2])
p = '/tmp/ovr_manual.json'
d = json.load(open(p))
d.setdefault(model, {})['input_cost_per_token'] = manual
json.dump(d, open(p, 'w'), indent=2, sort_keys=True)
print('manual value written:', manual, 'for', model)
PY
docker cp /tmp/ovr_manual.json "$CTN":/app/data/pricing.override.json

echo
echo "=== 2. make history say the sync's last write was a DIFFERENT value ==="
# That is exactly what the table looks like after the sync wrote 3.5e-08 and an
# operator then hand-edited the file to 9.99e-07.
$PG "INSERT INTO model_pricing_sync_history (model, field, old_value, new_value, source, applied_at, reason)
     VALUES ('$MODEL', 'input_cost_per_token', 0, $LAST_SYNCED, 'models.dev', now() - interval '1 hour', 'test-seed')" >/dev/null 2>&1
$PG "SELECT 'history latest: '||new_value FROM model_pricing_sync_history WHERE model='$MODEL' AND field='input_cost_per_token' ORDER BY applied_at DESC, id DESC LIMIT 1"

echo
echo "=== 3. restart so the startup sync runs ==="
cd /Users/dev/Global-repos/sub2api-release-0210
before=$(docker logs "$CTN" 2>&1 | grep -c "model pricing sync applied")
docker compose -f docker-compose.local-acceptance.yml restart sub2api >/dev/null 2>&1
for i in $(seq 1 36); do
  sleep 5
  now=$(docker logs "$CTN" 2>&1 | grep -c "model pricing sync applied")
  if [ "$now" -gt "$before" ]; then echo "  a new sync ran after ~$((i*5))s (runs: $before -> $now)"; break; fi
done

echo
echo "=== 4. did the manual price survive? ==="
docker cp "$CTN":/app/data/pricing.override.json /tmp/ovr_result.json 2>/dev/null
python3 - "$MODEL" "$MANUAL" <<'PY'
import json, sys
model, manual = sys.argv[1], float(sys.argv[2])
d = json.load(open('/tmp/ovr_result.json'))
got = d.get(model, {}).get('input_cost_per_token')
print('observed:', got)
print('expected:', manual)
ok = got is not None and abs(got - manual) < 1e-12
print('RESULT:', 'PRESERVED' if ok else 'OVERWRITTEN')
raise SystemExit(0 if ok else 1)
PY
