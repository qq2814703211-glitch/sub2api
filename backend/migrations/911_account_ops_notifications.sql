-- Account operations and upstream monitoring. Source cdf13f1991c369dbc5a4585ad197c103f0772451.

-- One durable, coalesced alert per account and error category. Raw upstream responses are never stored.
CREATE TABLE IF NOT EXISTS account_ops_alerts (
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('balance_low','weekly_quota')),
 account_name TEXT NOT NULL,
 signal TEXT NOT NULL,
 http_status INTEGER NOT NULL,
 first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 occurrences BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','failed','suppressed')),
 last_sent_at TIMESTAMPTZ,
 next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0,
 lease TEXT NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 PRIMARY KEY(account_id,kind)
);
CREATE INDEX IF NOT EXISTS account_ops_alerts_delivery ON account_ops_alerts(state,next_send_at);

-- Extend the existing durable queue; secrets remain in encrypted settings only.
ALTER TABLE account_ops_alerts DROP CONSTRAINT account_ops_alerts_kind_check;
ALTER TABLE account_ops_alerts ADD CONSTRAINT account_ops_alerts_kind_check CHECK(kind IN ('balance_low','weekly_quota','balance_threshold','quota_threshold'));
ALTER TABLE account_ops_alerts DROP CONSTRAINT account_ops_alerts_state_check;
ALTER TABLE account_ops_alerts ADD CONSTRAINT account_ops_alerts_state_check CHECK(state IN ('pending','sending','sent','failed','suppressed','resolved'));
ALTER TABLE account_ops_alerts ADD COLUMN details JSONB NOT NULL DEFAULT 'null'::jsonb;
ALTER TABLE account_ops_alerts ADD COLUMN deliveries JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE account_ops_alerts ADD COLUMN identity TEXT NOT NULL DEFAULT '';


-- Immutable transition history and a separately durable threshold baseline.
CREATE TABLE account_ops_threshold_monitors (
 account_id BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('balance_threshold','quota_threshold')),
 criteria TEXT NOT NULL DEFAULT '',
 identity TEXT NOT NULL DEFAULT '',
 episode_id TEXT NOT NULL DEFAULT '',
 adverse BOOLEAN NOT NULL DEFAULT FALSE,
 eligible BOOLEAN NOT NULL DEFAULT FALSE,
 healthy_since TIMESTAMPTZ,
 checked_at TIMESTAMPTZ,
 PRIMARY KEY(account_id,kind)
);
CREATE TABLE account_ops_threshold_events (
 id TEXT PRIMARY KEY,
 episode_id TEXT NOT NULL,
 phase TEXT NOT NULL CHECK(phase IN ('alert','recovery')),
 notification_enabled BOOLEAN NOT NULL,
 account_id BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('balance_threshold','quota_threshold')),
 account_name TEXT NOT NULL DEFAULT '',
 signal TEXT NOT NULL DEFAULT '',
 http_status INTEGER NOT NULL DEFAULT 0,
 first_seen TIMESTAMPTZ NOT NULL,
 last_seen TIMESTAMPTZ NOT NULL,
 occurrences BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL CHECK(state IN ('pending','sending','sent','failed','suppressed','resolved')),
 last_sent_at TIMESTAMPTZ,
 next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0,
 lease TEXT NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 details JSONB NOT NULL DEFAULT 'null'::jsonb,
 deliveries JSONB NOT NULL DEFAULT '{}'::jsonb,
 identity TEXT NOT NULL,
 criteria TEXT NOT NULL,
 UNIQUE(episode_id,phase)
);
CREATE INDEX account_ops_threshold_events_due ON account_ops_threshold_events(next_send_at) WHERE state IN ('pending','failed','sending');
CREATE INDEX account_ops_threshold_events_history ON account_ops_threshold_events(last_seen DESC,id);
-- Every old adverse row starts an existing episode, including partially delivered
-- warnings. First fresh scan adopts its criteria; no old warning is replayed.
INSERT INTO account_ops_threshold_monitors(account_id,kind,identity,episode_id,adverse)
SELECT account_id,kind,identity,'legacy:'||account_id||':'||kind,state<>'resolved'
FROM account_ops_alerts WHERE kind IN ('balance_threshold','quota_threshold');
-- Transfer incomplete delivery work exactly once, keeping receipt and retry
-- budgets. Blank criteria prevent claims until the first valid scan adopts the
-- live rule. Stable IDs make the transferred episode/event traceable to history.
INSERT INTO account_ops_threshold_events(
 id,episode_id,phase,notification_enabled,account_id,kind,account_name,signal,http_status,
 first_seen,last_seen,occurrences,state,last_sent_at,next_send_at,attempts,details,deliveries,identity,criteria)
SELECT 'legacy-transition:'||account_id||':'||kind,'legacy:'||account_id||':'||kind,
 'alert',TRUE,account_id,kind,account_name,signal,http_status,
 first_seen,last_seen,occurrences,CASE WHEN state='sending' THEN 'pending' ELSE state END,
 last_sent_at,next_send_at,attempts,details,deliveries,identity,''
FROM account_ops_alerts WHERE kind IN ('balance_threshold','quota_threshold')
 AND state IN ('pending','sending','failed')
 -- Old periodic rearming retained last_sent_at but cleared receipts. Such rows
 -- cannot be distinguished from a later unsent cycle; conservatively avoid
 -- replaying a warning already delivered under the legacy storage model.
 AND (last_sent_at IS NULL OR deliveries<>'{}'::jsonb);
-- Keep the original history/receipts. Cancel only its obsolete periodic queue.
UPDATE account_ops_alerts SET state='suppressed',lease='',lease_until=NULL
WHERE kind IN ('balance_threshold','quota_threshold') AND state IN ('pending','sending','failed');


-- Use a durable upstream account-group key while retaining the triggering local account.
ALTER TABLE account_ops_alerts ADD COLUMN IF NOT EXISTS group_id TEXT NOT NULL DEFAULT '';
ALTER TABLE account_ops_alerts ADD COLUMN IF NOT EXISTS group_name TEXT NOT NULL DEFAULT '';
UPDATE account_ops_alerts SET group_id='account:'||account_id::text WHERE group_id='';
UPDATE account_ops_alerts SET group_name=account_name WHERE group_name='';
ALTER TABLE account_ops_alerts DROP CONSTRAINT IF EXISTS account_ops_alerts_pkey;
ALTER TABLE account_ops_alerts ADD CONSTRAINT account_ops_alerts_pkey PRIMARY KEY(group_id,kind);
CREATE INDEX IF NOT EXISTS account_ops_alerts_account ON account_ops_alerts(account_id,kind);

ALTER TABLE account_ops_threshold_monitors ADD COLUMN IF NOT EXISTS group_id TEXT NOT NULL DEFAULT '';
UPDATE account_ops_threshold_monitors SET group_id='account:'||account_id::text WHERE group_id='';
ALTER TABLE account_ops_threshold_monitors DROP CONSTRAINT IF EXISTS account_ops_threshold_monitors_pkey;
ALTER TABLE account_ops_threshold_monitors ADD CONSTRAINT account_ops_threshold_monitors_pkey PRIMARY KEY(group_id,kind);
CREATE INDEX IF NOT EXISTS account_ops_threshold_monitors_account ON account_ops_threshold_monitors(account_id,kind);

ALTER TABLE account_ops_threshold_events ADD COLUMN IF NOT EXISTS group_id TEXT NOT NULL DEFAULT '';
ALTER TABLE account_ops_threshold_events ADD COLUMN IF NOT EXISTS group_name TEXT NOT NULL DEFAULT '';
UPDATE account_ops_threshold_events SET group_id='account:'||account_id::text WHERE group_id='';
UPDATE account_ops_threshold_events SET group_name=account_name WHERE group_name='';
CREATE INDEX IF NOT EXISTS account_ops_threshold_events_group ON account_ops_threshold_events(group_id,kind,last_seen DESC);


-- 307_upstream_monitor.sql
-- Upstream monitor: compact, time-windowed history for the upstream balance and
-- billing-rate snapshots that the account-level probe already stores in
-- accounts.extra. No credentials, no new upstream calls, no gateway changes.

CREATE TABLE IF NOT EXISTS upstream_balance_samples (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    balance     NUMERIC(20, 8) NOT NULL,
    unit        VARCHAR(16) NOT NULL DEFAULT '',
    sampled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_balance_samples_account_time
    ON upstream_balance_samples (account_id, sampled_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_balance_samples_time
    ON upstream_balance_samples (sampled_at);

CREATE TABLE IF NOT EXISTS upstream_rate_changes (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    old_rate    NUMERIC(20, 6) NOT NULL,
    new_rate    NUMERIC(20, 6) NOT NULL,
    source      VARCHAR(32) NOT NULL DEFAULT 'probe',
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_rate_changes_account_time
    ON upstream_rate_changes (account_id, changed_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_rate_changes_time
    ON upstream_rate_changes (changed_at DESC);

CREATE TABLE IF NOT EXISTS upstream_monitor_state (
    account_id     BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    last_balance   NUMERIC(20, 8),
    last_unit      VARCHAR(16) NOT NULL DEFAULT '',
    last_rate      NUMERIC(20, 6),
    last_sample_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


-- 308_upstream_monitor_ops.sql
-- Second batch for the operations-center upstream monitor: spend tracking,
-- daily rollups, and low-balance / rate-jump alerts. Everything here is fed by
-- the snapshots accounts.extra already stores; no credentials, no upstream
-- calls, no gateway or billing changes.

-- Per-sample spend derived from the previous balance. Existing rows default to
-- 0 because their delta was never computed.
ALTER TABLE upstream_balance_samples
    ADD COLUMN IF NOT EXISTS spend NUMERIC(20, 8) NOT NULL DEFAULT 0;

-- One bounded row per account per Beijing day. The overview and the trend chart
-- read this table instead of scanning raw samples.
CREATE TABLE IF NOT EXISTS upstream_monitor_daily (
    day           DATE NOT NULL,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    unit          VARCHAR(16) NOT NULL DEFAULT '',
    spend         NUMERIC(20, 8) NOT NULL DEFAULT 0,
    balance_close NUMERIC(20, 8),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (day, account_id, unit)
);

CREATE INDEX IF NOT EXISTS idx_upstream_monitor_daily_day
    ON upstream_monitor_daily (day DESC);

-- Alert thresholds are per account and opt-in; no row means no alerting.
CREATE TABLE IF NOT EXISTS upstream_monitor_alert_rules (
    account_id   BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    threshold    NUMERIC(20, 8) NOT NULL,
    unit         VARCHAR(16) NOT NULL DEFAULT '',
    rate_min_pct NUMERIC(10, 2) NOT NULL DEFAULT 10,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS upstream_monitor_alerts (
    id         BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind       VARCHAR(32) NOT NULL,
    level      VARCHAR(16) NOT NULL DEFAULT 'warning',
    balance    NUMERIC(20, 8),
    threshold  NUMERIC(20, 8),
    unit       VARCHAR(16) NOT NULL DEFAULT '',
    old_rate   NUMERIC(20, 6),
    new_rate   NUMERIC(20, 6),
    message    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_time
    ON upstream_monitor_alerts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_account_kind
    ON upstream_monitor_alerts (account_id, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_open
    ON upstream_monitor_alerts (created_at DESC) WHERE acked_at IS NULL;

-- 309_upstream_monitor_usage.sql
-- Third batch for the operations-center upstream monitor: plan, quota and
-- subscription state. The probe already parses GET /v1/usage into
-- accounts.extra.upstream_billing_probe.balance; this migration only adds the
-- two alert thresholds those numbers need. No upstream request, no gateway,
-- billing or credential change.

ALTER TABLE upstream_monitor_alert_rules
    ADD COLUMN IF NOT EXISTS usage_min_pct NUMERIC(10, 2) NOT NULL DEFAULT 20;

ALTER TABLE upstream_monitor_alert_rules
    ADD COLUMN IF NOT EXISTS expire_days INTEGER NOT NULL DEFAULT 7;


CREATE UNIQUE INDEX IF NOT EXISTS upstream_balance_samples_observation ON upstream_balance_samples(account_id,unit,sampled_at);

-- Legacy/manual extra values must never make the whole operational read fail.
CREATE OR REPLACE FUNCTION account_ops_snapshot_time(value text) RETURNS timestamptz
LANGUAGE plpgsql STABLE STRICT AS $$
BEGIN
 RETURN value::timestamptz;
EXCEPTION WHEN OTHERS THEN RETURN NULL;
END;
$$;
