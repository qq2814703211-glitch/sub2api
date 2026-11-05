-- Single-key admission is additive; all existing keys keep their old unlimited behavior.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS concurrency_limit integer NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_concurrency_limit_nonnegative CHECK (concurrency_limit >= 0);
