-- Migration: 919_usage_group_daily_rollups
--
-- Per-group per-calendar-day usage rollup.
--
-- Why: the channel-monitor V3 board asks for each group's cache-hit rate over
-- the last N days (ADR 0009: probe buckets take their cache rate from real
-- traffic, not from the probe itself). That query reads and aggregates
-- usage_logs across the whole window, and the return value is one float per
-- group. Measured on production (17,627,096 rows, all with a non-null group_id
-- so no index can prune the scan):
--
--   SELECT group_id, SUM(cache_read_tokens) / NULLIF(SUM(input+cc+cr),0)
--   FROM usage_logs WHERE group_id = ANY($1) AND created_at >= now() - '30 days'
--   GROUP BY group_id
--   -> Parallel Seq Scan, 19,714 ms
--
-- With this table the same read is an index scan over a few thousand rows:
--   0.117 ms  (measured on a 17.55M-row production-shaped copy)
--
-- This is derived data. usage_logs stays the single source of truth and the
-- table can be dropped and rebuilt from it at any time; see
-- internal/repository/group_usage_daily_rollup_repo.go for the rebuild path.
--
-- The day bucket is the local calendar day of the configured server timezone,
-- written explicitly rather than inherited from the session so bucketing cannot
-- drift if the server's `timezone` GUC changes. Rows record which calendar built
-- them in usage_group_daily_state.timezone_name, and a mismatch forces a full
-- rebuild instead of silently mixing two calendars.
CREATE TABLE IF NOT EXISTS usage_group_daily (
    group_id              BIGINT        NOT NULL,
    day                   DATE          NOT NULL,
    requests              BIGINT        NOT NULL DEFAULT 0,
    input_tokens          BIGINT        NOT NULL DEFAULT 0,
    output_tokens         BIGINT        NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT        NOT NULL DEFAULT 0,
    cache_read_tokens     BIGINT        NOT NULL DEFAULT 0,
    total_cost            NUMERIC(20,10) NOT NULL DEFAULT 0,
    actual_cost           NUMERIC(20,10) NOT NULL DEFAULT 0,
    computed_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, day)
);

-- Serves "every group, one day range" (the cache-rate read filters by day only).
CREATE INDEX IF NOT EXISTS idx_usage_group_daily_day
    ON usage_group_daily (day, group_id);

-- Watermark, same shape and same semantics as usage_key_daily_state:
-- every local day < closed_before is final.
CREATE TABLE IF NOT EXISTS usage_group_daily_state (
    id            SMALLINT    NOT NULL PRIMARY KEY,
    closed_before DATE        NOT NULL DEFAULT '1970-01-01',
    timezone_name TEXT        NOT NULL DEFAULT 'Asia/Shanghai',
    enabled       BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT usage_group_daily_state_id_check CHECK (id = 1)
);

INSERT INTO usage_group_daily_state (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE usage_group_daily IS
    'Derived per-group per-local-day usage rollup; rebuildable from usage_logs at any time.';
