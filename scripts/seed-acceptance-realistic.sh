#!/usr/bin/env bash
# Reseed usage_logs with a realistic key distribution.
#
# Why this matters: the first seed put all 200k rows under a single api_key_id,
# which makes idx_usage_logs_api_key_created_at perfectly non-selective and the
# planner correctly falls back to a sequential scan. That shape does not exist in
# production, where thousands of keys share the table, and it hides exactly the
# index benefit we are trying to measure. This version seeds one target key plus
# many other keys so the target is a small fraction of the table.
set -euo pipefail

PGHOST="${PGHOST:-127.0.0.1}"
PGPORT="${PGPORT:-5433}"
PGUSER="${PGUSER:-sub2api}"
PGDATABASE="${PGDATABASE:-sub2api}"
export PGPASSWORD="${PGPASSWORD:-localacceptance}"

TARGET_ROWS="${TARGET_ROWS:-20000}"   # rows belonging to the key under test
OTHER_KEYS="${OTHER_KEYS:-1800}"      # noise keys sharing the table
ROWS_PER_KEY="${ROWS_PER_KEY:-100}"
DAYS="${DAYS:-90}"

if [ "$PGPORT" != "5433" ] && [ "${ALLOW_NON_LOCAL:-0}" != "1" ]; then
  echo "refusing: PGPORT=$PGPORT is not the local acceptance port (5433)." >&2
  exit 1
fi

echo "target key rows: $TARGET_ROWS"
echo "noise: $OTHER_KEYS keys x $ROWS_PER_KEY rows = $((OTHER_KEYS * ROWS_PER_KEY))"
echo "total after reseed: $((TARGET_ROWS + OTHER_KEYS * ROWS_PER_KEY))"

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -v ON_ERROR_STOP=1 \
  -v target_rows="$TARGET_ROWS" -v other_keys="$OTHER_KEYS" \
  -v rows_per_key="$ROWS_PER_KEY" -v days="$DAYS" <<'SQL'
TRUNCATE usage_logs CASCADE;

-- Admin owns the key under test so an authenticated request can read it.
INSERT INTO accounts (id, name, platform, type, credentials, extra, status)
SELECT 990001, 'acceptance-seed-account', 'anthropic', 'api_key', '{}'::jsonb, '{}'::jsonb, 'active'
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE id = 990001);

INSERT INTO api_keys (id, user_id, key, name, status)
SELECT 990001, 1, 'sk-acceptance-seed-key-0000000000000001', 'acceptance-seed', 'active'
WHERE NOT EXISTS (SELECT 1 FROM api_keys WHERE id = 990001);

-- The key under test: a dense 90-day history.
INSERT INTO usage_logs (
  user_id, api_key_id, account_id, model,
  input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
  total_cost, actual_cost, duration_ms, created_at
)
SELECT
  1, 990001, 990001, 'claude-sonnet-4.5',
  400 + (g % 300), 200 + (g % 150), (g % 7) * 20, (g % 11) * 300,
  0.001 * (g % 97), 0.0009 * (g % 97), 800 + (g % 4000),
  now() - ((g % :days)::text || ' days')::interval
           - ((g % 86400)::text || ' seconds')::interval
FROM generate_series(1, :target_rows) AS g;

-- usage_logs.api_key_id is also a hard FK, so the noise keys need real rows.
INSERT INTO api_keys (id, user_id, key, name, status)
SELECT 800000 + k, 1, 'sk-acceptance-noise-' || lpad(k::text, 8, '0'), 'noise-' || k, 'active'
FROM generate_series(1, :other_keys) AS k
ON CONFLICT (id) DO NOTHING;

-- Noise keys: same table, same window, so the index has real selectivity to
-- exploit. account_id reuses the single seeded account to satisfy the FK.
INSERT INTO usage_logs (
  user_id, api_key_id, account_id, model,
  input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
  total_cost, actual_cost, duration_ms, created_at
)
SELECT
  1, 800000 + k, 990001, 'claude-sonnet-4.5',
  400 + (g % 300), 200 + (g % 150), 0, (g % 11) * 300,
  0.001 * (g % 97), 0.0009 * (g % 97), 800 + (g % 4000),
  now() - ((g % :days)::text || ' days')::interval
           - (((k * 37 + g) % 86400)::text || ' seconds')::interval
FROM generate_series(1, :other_keys) AS k,
     generate_series(1, :rows_per_key) AS g;

ANALYZE usage_logs;

SELECT count(*) AS total_rows,
       count(DISTINCT api_key_id) AS distinct_keys,
       round(100.0 * count(*) FILTER (WHERE api_key_id = 990001) / count(*), 2) AS target_share_pct
FROM usage_logs;
SQL

echo "reseed done"
