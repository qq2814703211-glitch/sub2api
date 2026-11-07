-- Prototype of usage_key_daily rollup, Asia/Shanghai day bucketing.
DROP TABLE IF EXISTS perf_key_daily;
CREATE TABLE perf_key_daily (
  api_key_id BIGINT NOT NULL,
  day DATE NOT NULL,
  requests BIGINT NOT NULL DEFAULT 0,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
  cache_read_tokens BIGINT NOT NULL DEFAULT 0,
  total_cost NUMERIC(20,10) NOT NULL DEFAULT 0,
  actual_cost NUMERIC(20,10) NOT NULL DEFAULT 0,
  duration_sum BIGINT NOT NULL DEFAULT 0,
  duration_count BIGINT NOT NULL DEFAULT 0,
  computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (api_key_id, day)
);

\timing on
INSERT INTO perf_key_daily (api_key_id, day, requests, input_tokens, output_tokens,
       cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
       duration_sum, duration_count)
SELECT api_key_id,
       (created_at AT TIME ZONE 'Asia/Shanghai')::date,
       COUNT(*),
       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
       COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
       COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0),
       COALESCE(SUM(COALESCE(duration_ms,0)),0),
       COUNT(*)
FROM usage_logs
GROUP BY 1,2;
\timing off
ANALYZE perf_key_daily;
SELECT count(*) AS rows, pg_size_pretty(pg_total_relation_size('perf_key_daily')) AS size FROM perf_key_daily;
