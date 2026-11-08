# 0.2.20-GloryCode-0.1.2 验收清单

对照 Hui 的原始诉求逐条验收。每条都必须有**实测证据**，不接受「看代码觉得对了」。

## L1 渠道监控 V3（`admin/channels/monitor` 的 V3 tab）

### L1-1 已删除的分组不要出现在选项里
**Hui 原话**：「你把已删除的分组的选项也展示出来干啥？」

**已查明的事实**（不要重复踩）：
- 后端 `channel_monitor_v3_repo.go:249-255` 的 SQL 已经是 `WHERE deleted_at IS NULL AND status = 'active'`，**没有泄漏**
- 「已删除」行来自 `MonitorV3SettingsPanel.vue:218-245` 的 `groupRows`：候选 ∪ 已配置的并集，已配置但不在候选里的会插一行显示 i18n 的 `deletedGroup`
- 设计意图（代码注释写明）是对的：不这样显示，配置里会卡着删不掉的分组

**验收判据**：
- [ ] `/admin/channel-monitor-v3/groups` 候选里不含任何 `deleted_at IS NOT NULL` 的分组
- [ ] 已配置里指向已删分组的条目仍可见，但**有明确视觉区分**（危险色/删除线/图标）
- [ ] 多个已删分组时能区分彼此（不能显示成好几行一样的「已删除分组」）
- [ ] 已删分组行**不可勾选**（disabled），或提供「移除」按钮
- [ ] `status='active'` 但 `deleted_at` 有值的分组（生产实测存在，如 `Cursor`）也被正确排除

**验收数据**：验收库已造好 `gpt-valid`(有效) / `claude-valid`(有效) / `old-deleted`(inactive+已删) / `ghost-active`(active+已删) / `paused-only`(inactive未删，因 SQL 有 status 条件故不出现，属既有行为)

### L1-2 每个分组独立的探针身份
**Hui 原话**：「探针身份不是统一全部分组用一个key啊！每个分组包含的模型和对应的分组key都不一样，怎么能统一用1个key呢？都要有各自单独的探针，也就是每个分组都要单独可配置探针身份」

**验收判据**：
- [ ] 每个分组条目上都有自己的探针配置（api key + 模型），不是全局单一
- [ ] 不同分组可配不同的 key
- [ ] 不同分组可配不同的模型集合
- [ ] 后端 `ProbeGroup(ctx, probe, groupID, models)` 按组传对应 probe（接口已支持，只需改调用方）
- [ ] **不得把 `model_allowlist` / `model_pricing` / `platforms` 这三个前置加回来**（ADR 0008 明确不看，`channel_monitor_v3.go:16` 有注释，`channel_monitor_v3_test.go:178` 有测试守着）
- [ ] 模型候选提示可用 `admin_group.go:85` 的 `GetGroupModelsListCandidates(ctx, id, platform)`

### L1-3 缓存率均值窗口可选 分钟 / 天
**Hui 原话**：「缓存率均值窗口填写选项要可选 分钟 or 天」

**验收判据**：
- [ ] 配置里有窗口值 + 窗口单位两个字段
- [ ] 单位可以是「分钟」或「天」
- [ ] 两种单位都能保存并回读
- [ ] 后端按单位换算成实际时间窗（分钟级窗口要能真的生效，不能只存不用）
- [ ] 旧的 `cache_average_window_days` 存量配置能兼容（或迁移）

## L2 用户管理 `admin/users`

### L2-1 列表并发数与详情请求数口径统一
**Hui 原话**：「页面展示的并发数是39，可点开后…那一个分组就是91个请求数，却是91？？？这是什么情况？」

**已查明的根因**（两个不同数据源，必然不等）：
- 列表 `current_concurrency` ← Redis **槽位** ZSET `concurrency:apikey:<id>`（`concurrency_service.go:466 GetAPIKeyConcurrencyBatch`），是并发槽位占用数
- 详情 `/admin/users/:id/inflight` 的 `total_inflight` ← Redis **在途登记** `usage:inflight:<uid>` hash（`usage_inflight.go:79-91 SummarizeUserInflight` → `len(snapshots)`），是在途请求数
- 还有一个双写：`api_key_service.go` 的 `fillCurrentConcurrency` 先调 `GetAPIKeyConcurrencyBatch`，紧接着 `FillAPIKeyQueueStats` 又在同循环里写 `keys[i].CurrentConcurrency = v.Active`（`api_key_queue_stats.go:21`），后者赢

**验收判据**：
- [ ] 列表的并发数与详情的 `total_inflight` 对同一个用户返回相同值
- [ ] 取向：统一到「在途请求数」（用户理解的就是他在跑几个请求）
- [ ] 若保留槽位语义，则两个数都要显示且各自标注含义
- [ ] 双写问题已消除（不要再有后写覆盖前写）

**验收数据**：`seed-inflight.sh` 造用户 2/3/4/5 = 3/7/1/15 条在途，列表与详情必须都返回这些数

### L2-2 感叹号提示文本不能为空
**Hui 原话**：「把鼠标放到旁边的感叹号圆圈提示符上，展示的文本内容是空的？！」

**已查明的真根因**（子代理发现，父级已独立复核确认）：
- i18n 键 `sinceHint` **确实存在**：`locales/zh/admin/overview.ts:624`、`locales/en/admin/overview.ts:646`
- 真问题在 prop 名错配：`components/common/HelpTooltip.vue` 的 props 是 `content?: string`，而 `components/user/UserConcurrencyCell.vue:72` 传的是 `:text=`
- 全仓只有这 **1 处**错配（其余 24 处 `:content=` 都正确）

**验收判据**：
- [ ] 该处改为 `:content=`
- [ ] 悬停提示显示「最早一条请求开始于 {时间}」
- [ ] zh/en 都能显示（不是只有一边）

### L2-3 按当前并发排序要真的生效
**Hui 原话**：「按照并发数进行排序怎么不是按照 当前并发数 的 升序降序 进行排序的？而是按ID进行升序降序的？」

**已查明的根因**：
- handler 里 `case "concurrency": field = dbuser.FieldConcurrency`，排的是 `users.concurrency`（并发**上限**配额），不是 `current_concurrency`
- 上限大多相同（很多人是 5 或 10），所以看起来像按 ID
- 实测：`sort_by=id` → `5,4,3,2,1`；`sort_by=current_concurrency` → `5,4,3,2,1`（完全相同，参数没生效）；`sort_by=concurrency` → `5,3,2,1,4`（按上限 50,10,5,5,1）

**验收判据**：
- [ ] `sort_by=current_concurrency` 升/降序结果不同，且按实时值单调
- [ ] 前端点该列表头传的是正确的字段名
- [ ] 实时值在 Redis 里，SQL 排不了 → 需批量取在途数后在应用层排（分页要正确，不能只排当前页）

## L3 生图广场 `image-studio`

### L3-1 两个密钥在同一卡片内且明确区分
**Hui 原话**：「AI改写配置要和生图Key放在一个卡片内，并且用"生图密钥""AI改写密钥"进行明确区分开两个密钥的不同之处。页面中只放配置两个密钥的填写框 和 相关说明 和 校验令牌按钮 和 详细配置按钮（点开后剩余未展示的配置弹窗）」

**验收判据**：
- [ ] 生图密钥与 AI改写密钥在**同一张卡片**里
- [ ] 两个密钥各有明确标注（「生图密钥」「AI改写密钥」）与用途说明
- [ ] 卡片内只有：两个密钥输入框 + 说明 + 校验令牌按钮 + 详细配置按钮
- [ ] 点「详细配置」弹出剩余配置（模型、思考强度等），主卡片不堆

### L3-2 协议自适应（不给用户选）
**Hui 原话**：「AI改写Key使用的协议能自适应选择吗？用户可能搞不懂协议，所以像账号管理的上游协议自适应一样让程序自适应协议吧」

**验收判据**：
- [ ] 界面上**没有**协议选择控件
- [ ] 程序能自动判定协议（`openai-chat` / `openai-responses` / `anthropic-messages`）
- [ ] 判定失败时有可读的失败提示，不是静默失败

### L3-3 baseurl 写死
**Hui 原话**：「不要改写接口地址，baseurl地址写死是我们站内内的地址也就是 https://api.example.com 不给用户选择权」

**已查明**：`rewriteConfig.ts:115-116` 现在是可填字符串，`:125` 默认空，`:261-262` 有 `isValidRewriteBaseUrl` 校验并会 `{ok:false, reason:'bad-base-url'}`

**验收判据**：
- [ ] 界面上**没有**地址输入框
- [ ] 实际请求打到 `https://api.example.com`
- [ ] 存量 localStorage 里的脏 baseUrl 不会把改写卡死（`bad-base-url` 分支不会因旧值触发）
- [ ] 密钥下拉用用户自己的 keys（`api/keys.ts` 的 `list()`），只用 id 做选择依据，不展示完整 key

## L4 连接诊断 `admin/connections`

### L4 归属与删除
**Hui 原话**：「是不是从kiss-kedaya那抄来的？还是sub2api官方项目自带的？从kiss-kedaya那抄来的就删掉，sub2api官方项目仓库自带的就留着」

**已查明的归属**：**都不是**。官方上游 `Wei-Shaw/sub2api` 完整树里没有；本仓 NOTICE 写明来自第三方 fork `ranxi2001/sub2api`（commit `9620d315f`）。kiss 侧虽有同名文件但本仓版本更完整。Hui 已决定**删掉**。

**已核实的依赖链**：
- 前端 `ConnectionDiagnosticsView.vue` → `api/admin/connectionDiagnostics.ts`
- → `/admin/usage/connections`、`/admin/usage/connection-diagnostics`（`routes/admin.go:777-778`）
- → `handler/admin/usage_connections.go`
- 采集层 `server/middleware/request_timing.go`，由 `router.go:95` 挂载，recorder 就是 `UsageHandler`
- `pkg/requestoutcome` 只被 `request_timing.go` 与 `openai_ws_connection_diagnostics.go` 用，同属这套功能

**验收判据**：
- [ ] `/api/v1/admin/usage/connections` 返回 404
- [ ] `/api/v1/admin/usage/connection-diagnostics` 返回 404
- [ ] 前端页面、路由、菜单项、i18n 都清掉
- [ ] `requesttiming` 中间件不再挂载（它没有独立页面，属这套功能）
- [ ] `requestoutcome` 包已删
- [ ] 连接诊断相关的表有建表迁移删除（幂等）
- [ ] settings 里的诊断配置键已清理
- [ ] `go build ./...` 通过
- [ ] 后端测试相对基线零新增失败
- [ ] 保留功能未受影响（用量日志、渠道监控、计费、网关）

## 通用（每版都要过）

- [ ] `backend`：`go build ./...` 通过
- [ ] `backend`：`go test -tags unit -count=1 ./internal/service/ ./internal/repository/ ./internal/handler/...` 相对基线零新增失败（已知基线失败：`TestLoginWritesPasswordCopy*`、`TestPasswordMFAWritesCopy*`、`TestProductionOpenAIPricingOverrideShortLongAndFast`）
- [ ] `frontend`：`npx --yes pnpm@9 run typecheck` 干净
- [ ] `frontend`：`npx --yes pnpm@9 exec vitest run` 全绿（基线 456 文件 / 4018 过 / 3 skipped）
- [ ] VERSION 为 `0.2.20-GloryCode-0.1.2`
- [ ] 镜像身份三元组齐全（VERSION + `--build-arg COMMIT` + `--build-arg DATE`）
- [ ] 本地验收栈实测通过（Hui 明确要求本地验收）
- [ ] 蓝绿上线后公网 200 率、容器零重启、错误日志为零

## 本地验收怎么跑

```bash
# 1. 造数据（分组+模型+多用户+在途）
bash ~/Global-session/xiao-012/seed-acceptance.sh
bash ~/Global-session/xiao-012/seed-inflight.sh

# 2. 逐条验收
bash ~/Global-session/xiao-012/verify-012.sh
```

`verify-012.sh` 在 0.1.1 上有 10 项失败，每一项都精确对应 Hui 的某条诉求。0.1.2 必须把这些全部转绿。
