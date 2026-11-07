-- Removes the account-operations alerting store and the upstream operations
-- monitor tables. Both features (the account ops dashboard and the upstream
-- monitor dashboard) are gone from the admin UI, and no Go code reads or writes
-- these tables any more.
--
-- Deliberately kept: ops_metrics_hourly, ops_metrics_daily, ops_error_logs,
-- ops_retry_attempts, ops_system_metrics, ops_job_heartbeats, ops_system_logs
-- and ops_ingress_reject_aggregates. They back the separate /admin/ops
-- operations center, which is not part of this removal. The ops_alert_rules /
-- ops_alert_events / ops_alert_silences tables stay for the same reason: they
-- hold the /admin/ops alert rules, events and silences, not the account ops
-- notifications.

DROP TABLE IF EXISTS account_ops_alerts;
DROP TABLE IF EXISTS account_ops_threshold_monitors;
DROP TABLE IF EXISTS account_ops_threshold_events;

DROP TABLE IF EXISTS upstream_balance_samples;
DROP TABLE IF EXISTS upstream_rate_changes;
DROP TABLE IF EXISTS upstream_monitor_state;
DROP TABLE IF EXISTS upstream_monitor_daily;
DROP TABLE IF EXISTS upstream_monitor_alert_rules;
DROP TABLE IF EXISTS upstream_monitor_alerts;

-- Only the account ops snapshot reader used this cast helper.
DROP FUNCTION IF EXISTS account_ops_snapshot_time(text);

-- The global probe schedule setting has no reader left. Scoped to a single key
-- so unrelated settings rows are untouched.
DELETE FROM settings WHERE key = 'upstream_billing_probe_settings';
