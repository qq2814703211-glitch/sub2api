-- Write-cost A/B: marginal cost of each usage_logs index on INSERT.
-- Stages 1M rows once into a heap-only table, then repeatedly copies into
-- targets carrying 0 / 5 / 10 / 15 / 20 / 26 indexes of identical definition.
DROP TABLE IF EXISTS ab_src;
CREATE TABLE ab_src (LIKE usage_logs INCLUDING DEFAULTS);
INSERT INTO ab_src SELECT * FROM usage_logs ORDER BY id LIMIT 1000000;
ANALYZE ab_src;
SELECT count(*) AS staged FROM ab_src;
