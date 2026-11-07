-- Migration: 918_usage_key_daily_rollups
--
-- Per-API-key per-calendar-day usage rollup.
--
-- Why: the keys page asks for one key's daily trend and each key's 30-day
-- totals. On production the hottest keys hold 2.4M-5.7M rows, so that query
-- reads and sorts millions of rows to produce ~30 output points. Measured on a
-- 17.55M-row copy of production shape, warm cache:
--   GetAPIKeyDailyUsage (single hot key)  = 4769 ms   (production, external merge 139MB)
--   GetBatchAPIKeyUsageStats (172 keys)   = 1128 ms   (local, parallel seq scan)
-- With this table both collapse to index lookups over a few thousand rows.
--
-- This is derived data. usage_logs stays the single source of truth and the
-- table can be dropped and rebuilt from it at any time (see the rebuild path in
-- internal/repository/key_usage_daily_rollup_repo.go).
--
-- The day bucket is the *local calendar day* of the configured server timezone,
-- not UTC: (created_at AT TIME ZONE 'Asia/Shanghai')::date. The timezone name is
-- written explicitly rather than inherited from the session so the bucketing
-- cannot drift if the server's `timezone` GUC is ever changed. Existing rows
-- carry the timezone they were built with in usage_key_daily_state, and a
-- mismatch forces a full rebuild instead of silently mixing two calendars.
CREATE TABLE IF NOT EXISTS usage_key_daily (
    api_key_id            BIGINT        NOT NULL,
    day                   DATE          NOT NULL,
    requests              BIGINT        NOT NULL DEFAULT 0,
    input_tokens          BIGINT        NOT NULL DEFAULT 0,
    output_tokens         BIGINT        NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT        NOT NULL DEFAULT 0,
    cache_read_tokens     BIGINT        NOT NULL DEFAULT 0,
    total_cost            NUMERIC(20,10) NOT NULL DEFAULT 0,
    actual_cost           NUMERIC(20,10) NOT NULL DEFAULT 0,
    duration_sum          BIGINT        NOT NULL DEFAULT 0,
    duration_count        BIGINT        NOT NULL DEFAULT 0,
    computed_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id, day)
);

-- The primary key already serves "one key, day range". This one serves the
-- "every key, one day range" shape used by the batch endpoint when it filters
-- by day rather than by key.
CREATE INDEX IF NOT EXISTS idx_usage_key_daily_day
    ON usage_key_daily (day, api_key_id);

-- Watermark. closed_before is the first local day whose rollup is NOT final:
-- every day < closed_before is complete. timezone_name records what calendar
-- the rows were bucketed in.
CREATE TABLE IF NOT EXISTS usage_key_daily_state (
    id            SMALLINT    NOT NULL PRIMARY KEY,
    closed_before DATE        NOT NULL DEFAULT '1970-01-01',
    timezone_name TEXT        NOT NULL DEFAULT 'Asia/Shanghai',
    enabled       BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT usage_key_daily_state_id_check CHECK (id = 1)
);

-- Belt and braces: the table is only ever created by this migration, but an
-- interrupted rollout of an earlier draft could leave it without a primary key,
-- which would make the ON CONFLICT seed below fail. Add it if missing.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'usage_key_daily_state'::regclass AND contype = 'p'
    ) THEN
        ALTER TABLE usage_key_daily_state ADD CONSTRAINT usage_key_daily_state_pkey PRIMARY KEY (id);
    END IF;
END $$;

INSERT INTO usage_key_daily_state (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE usage_key_daily IS
    'Derived per-key per-local-day usage rollup; rebuildable from usage_logs at any time.';
