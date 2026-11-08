-- 删除「连接诊断」(admin/connections) 功能的存储层。
-- 该功能整体复制自 kiss-kedaya，官方上游 Wei-Shaw/sub2api 并无此模块，用户确认不需要。
-- 909 建表迁移保留不动（迁移校验和保护，已应用的文件不可改写），此处只做幂等清理。
DROP TABLE IF EXISTS request_connection_traces;

-- 该功能的启停开关只存在于进程内存（middleware.connectionDiagnosticsEnabled），
-- 没有落库的 settings 键；仍按前缀兜底清理历史遗留，重复执行安全。
DELETE FROM settings WHERE key = 'connection_diagnostics_config' OR key LIKE 'connection_diagnostics_config:%';
