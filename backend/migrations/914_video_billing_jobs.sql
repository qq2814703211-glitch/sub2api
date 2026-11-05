-- Durable asynchronous video settlement; adapted from kiss-kedaya media jobs.
-- Upstream: cdf13f1991c369dbc5a4585ad197c103f0772451
-- Native SQL owner (no Ent schema). Retain completed rows for reconciliation.
CREATE TABLE IF NOT EXISTS video_billing_jobs (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('seedance', 'grok')),
    task_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('creating','submitted','ready','billed','done','failed','expired','reconciliation')),
    snapshot JSONB NOT NULL,
    intent JSONB,
    provider_status TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    lease_token TEXT,
    lease_until TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, api_key_id, task_id)
);
CREATE INDEX IF NOT EXISTS video_billing_jobs_due ON video_billing_jobs (available_at)
    WHERE state IN ('creating','submitted','ready','billed');
CREATE INDEX IF NOT EXISTS video_billing_jobs_admin ON video_billing_jobs (created_at DESC, id);
INSERT INTO settings (key, value) VALUES ('video_billing_worker_enabled', 'true') ON CONFLICT (key) DO NOTHING;
