# 异步视频任务与结算

本模块接入已有 Seedance 原生任务、Grok generation/edit/extension/status/content 协议。迁移为 `914_video_billing_jobs.sql`，表由原生 SQL 仓储拥有，不属于 Ent schema。

## 接受与恢复边界

- 上游 create 前持久化 `creating`、用户/Key/组/账号归属及冻结计价快照；上游返回 task ID 后更新 `submitted`，成功保存才向客户端写任务响应。
- create 请求一旦进入转发，无论网络错误、上游错误或持久化失败均不自动重发。接受结果未知且没有 task ID 的记录在十分钟后转 `reconciliation`；不能假定未收费，也不能自动创建替代视频。
- 完成观察来自客户端 status/content 或后台轮询，共用同一带令牌租约的状态机。后台只对原绑定账号发 GET；账号停用、移出原组或失去协议能力时进入待核对，不跨账号查询。
- `ready` 保存最终账单；任务行锁/租约围栏、现有账本去重、余额/订阅/Key/账号配额扣减及 `billed` 转换在同一 SQL 事务中。用量成功写入后转 `done`。中途崩溃可恢复，`billed` 不再查询供应商或重复扣款。
- 计费复用原 `RecordUsage` 的定价、倍率、订阅及配额命令。Grok 按次不乘秒，按秒采用真实完成秒数或已明确提交给供应商的秒数；两者都没有则待核对。非整数实际秒数不会截断或猜测，进入待核对。Seedance 使用原 completion tokens 单价和真实 completion tokens；明确零用量与缺失用量不同。
- 待结算元数据不依赖 Redis TTL，不自动删除。超过 24 小时未完成或持续供应商错误转待核对；恢复操作可继续查询已有 task ID。账本已经准备好的任务继续完成原账单，不因当前改价重算。

## 管理入口

运维监控页面中的“异步视频结算”卡片显示任务、owner ID、供应商状态、重试次数、错误代码、金额和更新时间；支持状态过滤、游标分页、恢复结算及后台开关。API 均位于现有 admin 鉴权和审计链下：

- `GET /api/v1/admin/ops/video-billing/jobs?state=&before=&limit=50`
- `POST /api/v1/admin/ops/video-billing/jobs/:id/retry`
- `GET /api/v1/admin/ops/video-billing/worker`
- `PUT /api/v1/admin/ops/video-billing/worker`，正文 `{"enabled":false}`

`video_billing_worker_enabled` 为持久设置，默认开启；每次后台批次及每个后续任务前读取，关闭后不领取任务或请求供应商，正在执行的一个任务允许收尾。客户端显式完成查询仍可结算。重新开启会恢复已知未结任务。worker 随 OpenAI gateway provider 启动、应用清理时取消并等待结束。

没有上游 task ID 的 unknown create 只保留审计，不开放“重发”按钮；需运营者结合供应商记录确认原任务。已撤销账号恢复权限后，管理员可恢复已有任务结算。失败/取消、过期任务不产生费用。

## 校验

- `go test -tags=unit ./internal/service ./internal/handler ./internal/handler/admin ./internal/server/routes -run '^(TestDurableVideo|TestSeedance|TestVideoCost|TestGrokVideo|TestExtractGrokVideo|TestIsGrokVideo)'`
- `VIDEO_BILLING_TEST_DSN=<专用临时数据库> go test -tags=unit,videodb ./internal/repository -run '^TestVideoDB' -count=1 -timeout=180s`
- `pnpm exec vitest run src/views/admin/ops/components/__tests__/VideoBillingJobsCard.spec.ts`

数据库专项只允许专用临时库，会应用现有迁移并清理本专项任务表。覆盖旧数据升级、独立进程重启无客户 poll、客户/worker 并发完成、旧租约围栏、扣款前故障、扣款后日志故障、日志后确认故障、事务回滚、改价、无 usage、owner/账号撤销、订阅/Key/账号配额、热关闭和长期 pending 恢复。

余额/订阅/Key/账号配额由数据库事务保障；现有平台配额缓存及通知继续复用原 best-effort 后处理政策，未引入全站 outbox 或预授权改造。

## 停用与卸载

先关闭后台开关；核对并处理 `creating/submitted/ready/billed/reconciliation` 任务，再移除 handler/provider/worker/admin/frontend 接线。导出任务审计后可删除 `video_billing_jobs` 表及对应设置。账本去重和 usage 记录保留，禁止为卸载删除已应用迁移文件或修改其校验和；需要删除表时使用新的运维迁移。本模块没有额外触发器、外部队列、凭据副本或供应商创建恢复器。

参考实现：kiss-kedaya `cdf13f1991c369dbc5a4585ad197c103f0772451` 的 media billing 任务与 before-publish 模式；保留仓库已有 NOTICE。
