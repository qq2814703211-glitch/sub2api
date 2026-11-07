-- models.dev 价格同步的变更史（ADR 0010）。
--
-- 每次同步写入 pricing.override_file 时，每个被改动的字段留一行：
-- 哪个模型、哪个字段、从多少改到多少、来源、生效时间、为什么改。
-- 后台 /admin/model-pricing-sync 直接读这张表展示。
--
-- old_value / new_value 单位为 USD / token（与 pricing.override_file 一致），
-- 不用 NUMERIC 的定点精度做展示换算时再乘 1e6 得到 USD / 1M tokens。
CREATE TABLE IF NOT EXISTS model_pricing_sync_history (
    id          BIGSERIAL PRIMARY KEY,
    model       VARCHAR(200)   NOT NULL,
    field       VARCHAR(80)    NOT NULL,
    old_value   NUMERIC(24, 14) NOT NULL DEFAULT 0,
    new_value   NUMERIC(24, 14) NOT NULL DEFAULT 0,
    source      VARCHAR(40)    NOT NULL DEFAULT 'models.dev',
    applied_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    reason      TEXT           NOT NULL DEFAULT ''
);

-- 列表页按时间倒序分页，模型 / 字段 / 来源三个筛选条件都是等值匹配。
CREATE INDEX IF NOT EXISTS idx_model_pricing_sync_history_applied_at
    ON model_pricing_sync_history (applied_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_model_pricing_sync_history_model
    ON model_pricing_sync_history (model, applied_at DESC);
-- ListSyncedFields 按来源取全量 (model, field)，用覆盖索引避免回表。
CREATE INDEX IF NOT EXISTS idx_model_pricing_sync_history_source_model_field
    ON model_pricing_sync_history (source, model, field);

COMMENT ON TABLE model_pricing_sync_history IS 'models.dev 官方参考价同步到 pricing.override_file 的字段级变更史；手动定价的模型不参与同步';
COMMENT ON COLUMN model_pricing_sync_history.model IS '模型名（小写，与覆盖层索引一致）';
COMMENT ON COLUMN model_pricing_sync_history.field IS '价表字段名，如 input_cost_per_token';
COMMENT ON COLUMN model_pricing_sync_history.old_value IS '变更前每 token 单价（USD），0 表示此前无值';
COMMENT ON COLUMN model_pricing_sync_history.new_value IS '变更后每 token 单价（USD）';
COMMENT ON COLUMN model_pricing_sync_history.source IS '变更来源，当前恒为 models.dev';
COMMENT ON COLUMN model_pricing_sync_history.reason IS '变更原因：补空缺 / 上一轮同步价漂移';
