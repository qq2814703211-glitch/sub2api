#!/usr/bin/env bash
# Prove the models.dev sync really preserves operator-set prices.
#
# The protection lives in the data: a non-zero field that this sync did not write
# itself counts as manual. So the honest test is to inject a manual price into the
# override file, restart, and confirm the value survives while other models still
# get synced. Just reading the code would not prove the wiring.
set -uo pipefail

CTN=sub2api-release-0210-sub2api-1
OVR=/app/data/pricing.override.json
MODEL=amazon.nova-micro-v1:0
MANUAL=9.99e-7

echo "=== 1. inject a manual price for $MODEL ==="
docker exec "$CTN" sh -c "python3 - <<'PY'
import json
p='$OVR'
d=json.load(open(p))
d.setdefault('$MODEL', {})['input_cost_per_token'] = $MANUAL
# Mark it as NOT synced by removing any trace of prior sync ownership:
# the sync service keeps its own bookkeeping, so a plain finite value that
# differs from the registry price is exactly what an operator edit looks like.
json.dump(d, open(p,'w'), indent=2, sort_keys=True)
print('wrote manual input_cost_per_token =', d['$MODEL']['input_cost_per_token'])
PY"

echo
echo "=== 2. restart so the sync runs again on startup ==="
cd /Users/dev/Global-repos/sub2api-release-0210
docker compose -f docker-compose.local-acceptance.yml restart sub2api >/dev/null 2>&1
for i in $(seq 1 24); do
  sleep 5
  if docker logs "$CTN" 2>&1 | grep -q "model pricing sync applied"; then break; fi
done

echo
echo "=== 3. did the manual price survive? ==="
docker exec "$CTN" sh -c "python3 - <<'PY'
import json
d=json.load(open('$OVR'))
v=d.get('$MODEL',{}).get('input_cost_per_token')
print('observed input_cost_per_token =', v)
print('expected manual value        =', $MANUAL)
print('RESULT:', 'PRESERVED' if v==$MANUAL else 'OVERWRITTEN')
PY"

echo
echo "=== 4. other models must still be synced (proves sync actually ran) ==="
docker logs "$CTN" 2>&1 | grep "model pricing sync applied" | tail -2
