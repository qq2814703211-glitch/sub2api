#!/usr/bin/env bash
# Seed the local acceptance database with enough usage_logs that the single-key
# usage perf claim can actually be measured instead of asserted.
#
# What matters here is row count and time distribution, not realistic content:
# one API key gets a dense 90-day history so the old day-grouping query and the
# indexed api_key_id aggregate can be compared on identical data.
#
# Refuses to run unless the target is the local acceptance Postgres.
set -euo pipefail

PGHOST="${PGHOST:-127.0.0.1}"
PGPORT="${PGPORT:-5433}"
PGUSER="${PGUSER:-sub2api}"
PGDATABASE="${PGDATABASE:-sub2api}"
export PGPASSWORD="${PGPASSWORD:-localacceptance}"

ROWS="${ROWS:-200000}"
DAYS="${DAYS:-90}"
KEY_ID="${KEY_ID:-990001}"
USER_ID="${USER_ID:-990001}"
ACCOUNT_ID="${ACCOUNT_ID:-990001}"

if [ "$PGPORT" != "5433" ] && [ "${ALLOW_NON_LOCAL:-0}" != "1" ]; then
  echo "refusing: PGPORT=$PGPORT is not the local acceptance port (5433)." >&2
  echo "set ALLOW_NON_LOCAL=1 only if you really mean it." >&2
  exit 1
fi

echo "target: ${PGUSER}@${PGHOST}:${PGPORT}/${PGDATABASE}  rows=${ROWS} days=${DAYS}"

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" \
  -v ON_ERROR_STOP=1 \
  -v rows="$ROWS" -v days="$DAYS" -v key_id="$KEY_ID" -v user_id="$USER_ID" -v account_id="$ACCOUNT_ID" <<'SQL'
-- The seed user is an ordinary user; the admin created by AUTO_SETUP is id 1.
INSERT INTO users (id, email, username, password_hash, role, status)
SELECT :user_id, 'acceptance-seed@local', 'acceptance-seed', 'x', 'user', 'active'
WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = :user_id);

INSERT INTO api_keys (id, user_id, key, name, status)
SELECT :key_id, :user_id, 'sk-acceptance-seed-key-0000000000000001', 'acceptance-seed', 'active'
WHERE NOT EXISTS (SELECT 1 FROM api_keys WHERE id = :key_id);

-- usage_logs.account_id is a hard FK, so the seed needs a real account row.
INSERT INTO accounts (id, name, platform, type, credentials, extra, status)
SELECT :account_id, 'acceptance-seed-account', 'anthropic', 'api_key', '{}'::jsonb, '{}'::jsonb, 'active'
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE id = :account_id);

INSERT INTO usage_logs (
  user_id, api_key_id, account_id, model,
  input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
  total_cost, actual_cost, duration_ms, created_at
)
SELECT
  :user_id,
  :key_id,
  :account_id,
  'claude-sonnet-4.5',
  400 + (g % 300),
  200 + (g % 150),
  (g % 7) * 20,
  (g % 11) * 300,
  0.001 * (g % 97),
  0.0009 * (g % 97),
  800 + (g % 4000),
  now() - ((g % :days)::text || ' days')::interval
           - ((g % 86400)::text || ' seconds')::interval
FROM generate_series(1, :rows) AS g;

ANALYZE usage_logs;

SELECT count(*)                              AS seeded_rows,
       count(DISTINCT date_trunc('day', created_at)) AS distinct_days,
       min(created_at)::date                 AS first_day,
       max(created_at)::date                 AS last_day
FROM usage_logs WHERE api_key_id = :key_id;
SQL

echo "seed done"
