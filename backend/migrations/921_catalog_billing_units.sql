-- Captured by the native usage log reader/writers. NULL retains legacy rows.
-- Quantity is in the row's billing_mode (successful requests, images, or seconds).
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS billing_units DOUBLE PRECISION;
