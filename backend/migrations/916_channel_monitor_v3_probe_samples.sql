-- Channel Monitor V3 active-probe samples.
--
-- V3 fills its traffic-free time buckets from its own probe identity, so the
-- samples live in their own table rather than in V1's `channel_monitor_histories`
-- (keyed by monitor_id + group_name). Keeping them apart is what makes V3
-- independent: V1 probe data can never colour the V3 board, and V3 probes do not
-- show up in V1's history views.
--
-- Cache rate is deliberately NOT stored here. A probe request is one request that
-- does not exercise prompt caching, so any rate derived from it would describe
-- "the probe ran" rather than the channel's real cache behaviour (see ADR 0009).
-- The V3 board fills the cache rate from the group's real-traffic average over
-- `channel_monitor_v3_config.cache_average_window_days` and shows "—" when there
-- is no real traffic to average.

CREATE TABLE IF NOT EXISTS channel_monitor_v3_probe_samples (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL,
    ttft_ms INTEGER,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_monitor_v3_probe_samples_status_check
        CHECK (status IN ('operational', 'degraded', 'failed', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_samples_group_time
    ON channel_monitor_v3_probe_samples (group_id, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_samples_time
    ON channel_monitor_v3_probe_samples (checked_at);
