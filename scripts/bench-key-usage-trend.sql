-- Side-by-side comparison of the two daily-trend shapes for one API key.
--
-- A: what the code does today — GROUP BY a TO_CHAR expression over created_at.
--    The expression is not indexable, so every matching row must be read and
--    formatted before grouping.
-- B: same aggregate, GROUP BY date_trunc('day', created_at) over a range, which
--    is the shape the pre-aggregated rollup could also serve later.
--
-- Both read exactly the same rows; the difference is what the planner can do
-- with the grouping key.

\timing on

\echo '=== A: TO_CHAR grouping (current code) ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT
  TO_CHAR(created_at, 'YYYY-MM-DD') AS date,
  COUNT(*) AS requests,
  COALESCE(SUM(input_tokens), 0) AS input_tokens,
  COALESCE(SUM(output_tokens), 0) AS output_tokens,
  COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
  COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
  COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS total_tokens,
  COALESCE(SUM(total_cost), 0) AS cost,
  COALESCE(SUM(actual_cost), 0) AS actual_cost
FROM usage_logs
WHERE created_at >= now() - interval '30 days'
  AND created_at < now()
  AND user_id = 1
  AND api_key_id = 990001
GROUP BY date
ORDER BY date ASC;

\echo '=== B: date_trunc grouping over the same window ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT
  TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD') AS date,
  COUNT(*) AS requests,
  COALESCE(SUM(input_tokens), 0) AS input_tokens,
  COALESCE(SUM(output_tokens), 0) AS output_tokens,
  COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
  COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
  COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS total_tokens,
  COALESCE(SUM(total_cost), 0) AS cost,
  COALESCE(SUM(actual_cost), 0) AS actual_cost
FROM usage_logs
WHERE created_at >= now() - interval '30 days'
  AND created_at < now()
  AND user_id = 1
  AND api_key_id = 990001
GROUP BY date_trunc('day', created_at)
ORDER BY date_trunc('day', created_at) ASC;

\echo '=== C: same aggregate without user_id (key-only predicate) ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT
  TO_CHAR(created_at, 'YYYY-MM-DD') AS date,
  COUNT(*) AS requests,
  COALESCE(SUM(actual_cost), 0) AS actual_cost
FROM usage_logs
WHERE created_at >= now() - interval '30 days'
  AND created_at < now()
  AND api_key_id = 990001
GROUP BY date
ORDER BY date ASC;
