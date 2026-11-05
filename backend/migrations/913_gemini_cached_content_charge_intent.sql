-- Additive to the existing cachedContents and mutation lease tables.
-- Existing rows are already billable resources and stay visible.
ALTER TABLE gemini_cached_contents
 ADD COLUMN IF NOT EXISTS billing_ready BOOLEAN NOT NULL DEFAULT TRUE,
 ADD COLUMN IF NOT EXISTS charge_intent JSONB,
 ADD COLUMN IF NOT EXISTS charge_target_expire_time TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS charge_lease_token VARCHAR(64),
 ADD COLUMN IF NOT EXISTS charge_lease_until TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS charge_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 ADD COLUMN IF NOT EXISTS charge_attempts INTEGER NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS charge_last_error TEXT,
 ADD COLUMN IF NOT EXISTS charge_completed_request_id VARCHAR(64);

-- Include soft-deleted resources: deleting a cache must never discard its bill.
CREATE INDEX IF NOT EXISTS idx_gemini_cached_contents_pending_charge
 ON gemini_cached_contents (charge_next_attempt_at, id)
 WHERE charge_intent IS NOT NULL;
