-- 分组基础倍率变更历史。仅供用户侧「调价箭头 / NEW」提示读取，
-- 计费与调度从不读取本表。
--
-- 行由 groups 上的行级触发器写入，因此后台保存、批量 UPDATE、SQL 手改
-- 等所有写入路径都会与倍率修改本身处于同一事务：编辑回滚不会留下历史。
-- 本迁移不追溯迁移前的历史；既有分组在下一次真实调价前没有记录。
--
-- 刻意不加外键：历史不得阻塞分组的编辑或删除，分组 id 也不会复用。
CREATE TABLE IF NOT EXISTS group_rate_history (
    id         BIGSERIAL PRIMARY KEY,
    group_id   BIGINT NOT NULL,
    old_rate   DECIMAL(10, 4) NOT NULL,
    new_rate   DECIMAL(10, 4) NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_group_rate_history_group_changed
    ON group_rate_history (group_id, changed_at DESC);

-- 保留策略需要按时间裁剪；显式维护命令见
-- backend/cmd/cleanup-group-rate-history，不会自动在后台执行。
CREATE INDEX IF NOT EXISTS idx_group_rate_history_changed_at
    ON group_rate_history (changed_at);

CREATE OR REPLACE FUNCTION record_group_rate_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.rate_multiplier IS DISTINCT FROM OLD.rate_multiplier THEN
        INSERT INTO group_rate_history (group_id, old_rate, new_rate)
        VALUES (NEW.id, OLD.rate_multiplier, NEW.rate_multiplier);
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_rate_history ON groups;
CREATE TRIGGER trg_groups_rate_history
AFTER UPDATE OF rate_multiplier ON groups
FOR EACH ROW
WHEN (OLD.rate_multiplier IS DISTINCT FROM NEW.rate_multiplier)
EXECUTE FUNCTION record_group_rate_history();
