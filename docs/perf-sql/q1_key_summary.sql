-- Baseline SQL for keys page / usage queries.
-- Parameterized via psql variables: :key :user :days
\set ON_ERROR_STOP on
\timing off

-- Q1: GetAPIKeyStatsAggregated (keys page summary, range)
EXPLAIN (ANALYZE, BUFFERS, TIMING) 
SELECT
  COUNT(*) as total_requests,
  COALESCE(SUM(input_tokens), 0) as total_input_tokens,
  COALESCE(SUM(output_tokens), 0) as total_output_tokens,
  COALESCE(SUM(cache_creation_tokens + cache_read_tokens), 0) as total_cache_tokens,
  COALESCE(SUM(cache_creation_tokens), 0) as total_cache_creation_tokens,
  COALESCE(SUM(cache_read_tokens), 0) as total_cache_read_tokens,
  COALESCE(SUM(total_cost), 0) as total_cost,
  COALESCE(SUM(actual_cost), 0) as total_actual_cost,
  COALESCE(AVG(COALESCE(duration_ms, 0)), 0) as avg_duration_ms
FROM usage_logs
WHERE api_key_id = 1000001 AND created_at >= now() - interval '30 days' AND created_at < now();
