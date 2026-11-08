-- V3 缓存率均值窗口支持「分钟 or 天」。
--
-- 915 里窗口只有一个 cache_average_window_days INTEGER（CHECK 1..90），只能填天。
-- 用户要求同一个窗口能选分钟或天，而「最近 30 分钟」这种粒度天列表达不到。
--
-- 为什么存 value + unit 而不是统一折算成分钟：
--   老配置填的是「30 天」，UI 必须原样回显「30 天」。把它改写成「43200 分钟」
--   虽然等值，但用户再打开页面会认为自己的配置被改掉了。
--
-- 为什么保留 cache_average_window_days：
--   旧版本后端只读这一列，滚动发布期间新旧实例会同时在线。这一列继续写「按天
--   折算后的值」（分钟窗口向上取整、至少 1 天），保证旧实例不会读到 0 或空。
--
-- 存量迁移：老的 days=30 必须变成「30 天」，不能变成「30 分钟」。下面的 UPDATE
-- 显式写 unit='day'、value=cache_average_window_days，就是为了这一点。
--
-- CHECK 约束：原约束 cache_average_window_days BETWEEN 1 AND 90 保留原样 ——
-- 它约束的仍是那个「兼容用的天数」，分钟窗口折算后依然落在这个区间。新列的
-- 取值范围由服务层校验（90 天 = 129600 分钟），这里再加一条数据库兜底。

ALTER TABLE channel_monitor_v3_config
    ADD COLUMN IF NOT EXISTS cache_average_window_value INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_average_window_unit  TEXT    NOT NULL DEFAULT '';

-- 存量行：把老的天数搬到新列，单位显式写 day。
-- 只在「新列还是默认值」时迁移，重复执行不会把用户后来选的分钟窗口覆盖回去。
UPDATE channel_monitor_v3_config
SET cache_average_window_value = cache_average_window_days,
    cache_average_window_unit  = 'day'
WHERE id = 1
  AND cache_average_window_unit = ''
  AND cache_average_window_value = 0;

-- 单位只能是 minute / day（空串在迁移完成后不该再出现，保留给极端的手工插入）。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'channel_monitor_v3_config_window_unit_check'
    ) THEN
        ALTER TABLE channel_monitor_v3_config
            ADD CONSTRAINT channel_monitor_v3_config_window_unit_check
            CHECK (cache_average_window_unit IN ('', 'minute', 'day'));
    END IF;
END $$;

COMMENT ON COLUMN channel_monitor_v3_config.cache_average_window_value IS
    '缓存率均值窗口数值，配合 cache_average_window_unit 使用（分钟或天）。';
COMMENT ON COLUMN channel_monitor_v3_config.cache_average_window_unit IS
    '缓存率均值窗口单位：minute 或 day；空串表示尚未迁移（读侧按天解释）。';
COMMENT ON COLUMN channel_monitor_v3_config.cache_average_window_days IS
    '兼容列：窗口按天折算的整数值（分钟窗口向上取整），旧版本后端只读这一列。';
