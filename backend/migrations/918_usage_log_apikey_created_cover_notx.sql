-- Migration: 918_usage_log_apikey_created_cover_notx
--
-- Promote the covering index that was created by hand on production into a
-- migration, so a fresh deployment or a rebuilt container has it too.
--
-- Production had `idx_usage_logs_apikey_created_cover` created out-of-band and
-- it was in no migration file, so any new database silently lost it.
--
-- Width matters, and the production version was too narrow. Measured on this
-- 17.55M-row production-shaped DB, one hot key with 513,364 rows in the window
-- and warm cache:
--
--   no covering index         summary 350.7 ms  trend 444.7 ms  (bitmap heap scan)
--   INCLUDE (actual_cost)     summary 346.2 ms  trend 440.7 ms  (NOT selected)
--   INCLUDE (the list below)  summary  55.7 ms  trend 204.0 ms  (index-only scan)
--
-- The production definition carried only actual_cost, so the keys-page
-- aggregates still had to visit the heap for the token and cost columns and the
-- planner kept preferring the bitmap heap scan: as built, that index does not
-- cover these queries. INCLUDE therefore lists every column they read.
--
-- This index is not the main fix for the keys page. The per-key per-day rollup
-- in 918_usage_key_daily_rollups.sql answers those in ~12 ms without touching
-- usage_logs. The index stays useful as the fallback path (rollup disabled or
-- not yet backfilled) and for the today tail the rollup cannot cover.
--
-- Trend still spills to disk (Sort Method: external merge Disk: 28144kB) because
-- 513k rows must be sorted down to 31 day buckets. That is what the rollup
-- removes.
--
-- Cost: 1285 MB at 17.55M rows, roughly +0.8 us per inserted row (measured
-- marginal index cost: 1M rows with 0 indexes 938 ms, with 26 indexes 20523 ms).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_apikey_created_cover
    ON usage_logs (api_key_id, created_at)
    INCLUDE (
        input_tokens,
        output_tokens,
        cache_creation_tokens,
        cache_read_tokens,
        total_cost,
        actual_cost,
        duration_ms
    );
