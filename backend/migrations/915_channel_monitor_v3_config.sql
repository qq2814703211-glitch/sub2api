-- Channel Monitor V3 standalone configuration (single row, id = 1).
--
-- V3 used to borrow V1's `channel_monitors` rows for probe identity and V2's
-- `channel_monitor_v2_config.group_ids` for the board's group list, and its
-- probes were gated by the global `channel_monitor_enabled` + `channel_monitor_mode`
-- switches. That coupling is removed here: V3 gets its own table, its own
-- enable switch, its own probe identity and its own group list.
--
--   enabled                    V3 master switch. Controls V3 only; V1/V2 keep
--                              reading `channel_monitor_enabled` / `channel_monitor_mode`.
--   groups                     JSONB array of
--                              [{group_id, enabled, models[], probe_enabled}]
--   probe                      JSONB {endpoint, api_key_encrypted, provider,
--                              primary_model, extra_models[], interval_seconds}
--   cache_average_window_days  Look-back window for the real-traffic cache rate
--                              average written into probe-filled buckets
--                              (see ADR 0009: probe buckets never invent 100%).
--
-- The initial values are copied from what V1/V2 actually use today so an
-- upgrade does not require re-configuring anything by hand. `enabled` is copied
-- from the effective runtime mode: a deployment already running hybrid (v3)
-- keeps probing, everything else starts with V3 off until an operator turns it on.

CREATE TABLE IF NOT EXISTS channel_monitor_v3_config (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    groups JSONB NOT NULL DEFAULT '[]'::jsonb,
    probe JSONB NOT NULL DEFAULT '{}'::jsonb,
    cache_average_window_days INTEGER NOT NULL DEFAULT 30
        CHECK (cache_average_window_days BETWEEN 1 AND 90),
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO channel_monitor_v3_config (id, enabled, groups, probe, cache_average_window_days)
SELECT
    1,
    COALESCE((SELECT LOWER(TRIM(value)) = 'v3' FROM settings WHERE key = 'channel_monitor_mode'), FALSE),
    COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'group_id', gid,
            'enabled', TRUE,
            'models', '[]'::jsonb,
            'probe_enabled', TRUE
        ))
        FROM unnest(COALESCE(
            (SELECT group_ids FROM channel_monitor_v2_config WHERE id = 1),
            '{}'::bigint[]
        )) AS gid
    ), '[]'::jsonb),
    COALESCE((
        SELECT jsonb_build_object(
            'endpoint', m.endpoint,
            'api_key_encrypted', COALESCE(m.api_key_encrypted, ''),
            'provider', m.provider,
            'primary_model', m.primary_model,
            'extra_models', COALESCE(m.extra_models, '[]'::jsonb),
            'interval_seconds', m.interval_seconds
        )
        FROM channel_monitors m
        WHERE m.enabled
          AND COALESCE(m.check_mode, 'probe') <> 'quota'
          AND COALESCE(m.endpoint, '') <> ''
        ORDER BY m.id
        LIMIT 1
    ), '{}'::jsonb),
    30
WHERE NOT EXISTS (SELECT 1 FROM channel_monitor_v3_config WHERE id = 1);

COMMENT ON TABLE channel_monitor_v3_config IS
    '单行 V3 配置：独立开关 / 分组名单 / 探针身份 / 缓存均值窗口。V1、V2 不读取本表。';
