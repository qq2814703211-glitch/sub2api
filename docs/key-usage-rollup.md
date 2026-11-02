# 可选密钥用量汇总

此模块加速用户 `/keys` 的今日与近30天金额；API返回字段和旧计算区间保持兼容。前端改进单独生效；后端加速默认关闭，待合并版本在测试库验收后再按需启用。

## 管理接口

使用现有管理员登录态调用：

- `GET /api/v1/admin/settings/key-usage-rollup`：配置、运行状态、已覆盖小时数、待处理增量数和最近错误。
- `PUT /api/v1/admin/settings/key-usage-rollup`：`{"enabled":true,"interval_seconds":10}` 启用；`{"enabled":false}` 关闭。
- `DELETE /api/v1/admin/settings/key-usage-rollup`：停止并彻底清理自有表、触发器、函数和设置项；原始日志与金额不变。

默认关闭时不创建表、触发器或后台worker；统计调用只经过本地状态判断。设置保存在 `settings.api_key_usage_rollup`。单进程配置热更新；多实例部署时配置接口应固定到管理实例，其他实例从原查询读取仍正确；跨实例统一切换需要协调重载，不能假设所有实例收到同一个本地指针更新。

## 原子性与计算边界

原始 `usage_logs.actual_cost` 是统计来源。模块使用 UTC 固定小时桶，日界由服务器应用时区计算；金额在数据库中保持 NUMERIC 聚合，最终响应仍使用既有浮点字段。

- 数据库语句级 transition trigger 将 INSERT/UPDATE/DELETE 记录为追加的正负增量；去重冲突和事务回滚不会产生生效增量。
- 已关闭的小时按 REPEATABLE READ 快照回填，同快照删除该小时可见增量，并原子写覆盖标记；快照后提交的事务仍留在队列，不依赖自增ID提交顺序。
- worker对可见已覆盖增量执行原子删除并求和；多个worker通过事务advisory lock互斥。
- 读取在单条SQL快照内拼接汇总、尚未应用增量、未覆盖与不完整边界的原始范围。每条记录只落入一种来源。
- 关闭时移除写入捕获；重新开启清空自有覆盖与汇总再回填，避免关闭期间变更遗漏。进程正常重启保留已有捕获和进度。
- 原接口“今日”没有截止上界，模块保留这一兼容语义；`total_actual_cost`仍为近30天，前端保留已有近30天标签。
- 生命周期操作在数据库锁内重读持久配置；读、回填、清理采用源日志表优先的锁序。读取短事务仍以单条查询快照拼接金额，关闭/清理后跨实例读者回退原统计。

每次worker处理一个历史小时并应用至多10000条已覆盖增量。回填默认从最新小时开始，旧未覆盖范围读原日志；初次启用不会瞬间获得全部提速。后台单次工作有限时，失败保留原子状态供下次继续。

## 源码与完整撤回清单

- 自包含实现：`backend/internal/keyusage/`，schema按启用创建，未占用全局migration编号。
- 读取钩子：`backend/internal/service/usage_service.go` 与 `key_usage_runtime.go`。
- 依赖与生命周期：service/handler provider、`backend/cmd/server/wire.go` 和生成的 `wire_gen.go`。
- 管理接口：`backend/internal/handler/admin/key_usage_rollup.go`、SettingHandler字段、routes/admin.go。
- 前端：KeysView及对应测试、locale键。

彻底撤回时先通过DELETE完成数据库清理并验证无自有对象，再移除这些代码钩子并重新生成Wire。仅回退二进制不能自动删除数据库触发器，须先清理。

## 验证

`KEY_USAGE_TEST_DSN` 指向独立PostgreSQL库，再运行 `go test -race ./internal/keyusage`。测试为每个case创建独立schema并在结束时清理。大样本基准为单独显式启用用例，避免日常单元回归自动写入百万行。

合并时保留现有其他分支的 provider/cleanup 依赖，重新生成 Wire 并运行相关服务、管理接口、路由和前端测试。用户确认合并后再创建版本，此分支自身不包含发布或生产变更。
