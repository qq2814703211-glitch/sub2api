EXPLAIN (ANALYZE, BUFFERS, TIMING)
SELECT
  TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD') as date,
  COUNT(*) as requests,
  COALESCE(SUM(input_tokens), 0) as input_tokens,
  COALESCE(SUM(output_tokens), 0) as output_tokens,
  COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
  COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
  COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
  COALESCE(SUM(total_cost), 0) as cost,
  COALESCE(SUM(actual_cost), 0) as actual_cost
FROM usage_logs
WHERE created_at >= (date_trunc('day', now()) - interval '30 days') AND created_at < (date_trunc('day', now()) + interval '1 day')
  AND api_key_id = 1000001
GROUP BY date_trunc('day', created_at) ORDER BY date_trunc('day', created_at) ASC;
