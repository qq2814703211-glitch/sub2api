-- OpenAI 缓存写入按 TTL 分档计费（ADR 0011）。
--
-- 30 分钟写入 = 输入价 × 1.25（默认 TTL），12 小时写入 = 输入价 × 2.5
-- （Responses API 的 prompt_cache_options.ttl = "12h"）。这两档与 Anthropic 的
-- 5 分钟 / 1 小时 ephemeral 缓存是两套语义，因此新增独立列，不复用
-- cache_creation_5m_tokens / cache_creation_1h_tokens。
--
-- 成本也独立成列：cache_write_ttl_cost 与 cache_creation_cost 分开记账，
-- 避免两个 provider 的口径混在同一个金额里。
--
-- 全部为可空默认 0 的新增列，历史行不需要回填，读取侧零值即为「无分档写入」。
ALTER TABLE usage_logs
 ADD COLUMN IF NOT EXISTS cache_write_30m_tokens INT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS cache_write_12h_tokens INT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS cache_write_ttl_cost NUMERIC(20, 10) NOT NULL DEFAULT 0;
