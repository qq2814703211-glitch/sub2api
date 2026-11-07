-- 全量回填 usage_key_daily（生产 1761 万行量级），按本地日分片。
-- 分片大小 7 天/片：每个事务处理一周，中断后重跑从有数据的片继续。
\timing on
TRUNCATE usage_key_daily;
INSERT INTO usage_key_daily (
    api_key_id, day, requests, input_tokens, output_tokens,
    cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
    duration_sum, duration_count, computed_at)
SELECT ul.api_key_id,
       (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date,
       COUNT(*),
       COALESCE(SUM(ul.input_tokens), 0), COALESCE(SUM(ul.output_tokens), 0),
       COALESCE(SUM(ul.cache_creation_tokens), 0), COALESCE(SUM(ul.cache_read_tokens), 0),
       COALESCE(SUM(ul.total_cost), 0), COALESCE(SUM(ul.actual_cost), 0),
       COALESCE(SUM(COALESCE(ul.duration_ms, 0)), 0), COUNT(*), now()
FROM usage_logs ul
GROUP BY 1, 2;
\timing off
ANALYZE usage_key_daily;
SELECT count(*) AS rows, pg_size_pretty(pg_total_relation_size('usage_key_daily')) AS size FROM usage_key_daily;
