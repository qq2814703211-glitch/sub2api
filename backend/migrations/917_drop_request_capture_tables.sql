-- 删除「请求采集」功能（源自 kiss-kedaya 的模块，未启用即整体移除）。
-- 采集正文本身存在 DATA_DIR/request-captures 下，不在 PostgreSQL；此处只清理元数据表。
-- 幂等：重复执行安全。
DROP TABLE IF EXISTS request_capture_records;
DROP TABLE IF EXISTS request_capture_tasks;

-- 采集开关/配额/保留期配置键（service.SettingKeyRequestCapture）。
DELETE FROM settings WHERE key = 'request_capture_config' OR key LIKE 'request_capture_config:%';
