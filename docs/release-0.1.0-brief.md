# 0.2.20-GloryCode-0.1.0 实施设计简报

**状态**：已定稿（决策已收集），进入实施
**基线**：`839945e2d`（= `v0.2.20-GloryCode-0.0.45`），工作树 `~/Global-repos/sub2api-release-0210`，分支 `release/0.2.20-GloryCode-0.1.0`
**目标版本**：`0.2.20-GloryCode-0.1.0`

---

## 0. 已核实的事实（动手前的现场证据）

### 0.1 0.0.45 是否包含 e8c44c57

**包含**。`git merge-base --is-ancestor e8c44c57 v0.2.20-GloryCode-0.0.45^{commit}` 为真，且内容真实在树上：`frontend/src/views/admin/TicketsView.vue`、`GroupCacheTestView.vue`、`UpstreamMonitorView.vue`、`OpsCenterView.vue` 全部可在 `839945e2d` 树中读到。用户「好像没包含」的观感，来自下一节。

### 0.2 媒体 / GPT-Image 2.6 候选确实丢了（空合并）

- `d43511214`（`feature/media-gpt-image-26`）是用户所说的「媒体和 GPT-Image 2.6 候选提交」。
- `9c7426caa` 的提交说明写「include d435 media lease recovery source」，`d43511214` 确实是它的第二父提交，**但**：

  ```
  git rev-parse 9c7426caa^{tree}  = 235474cca5...
  git rev-parse fa876b6ee^{tree}  = 235474cca5...   ← 第一父提交
  git rev-parse d43511214^{tree}  = ad8837005a...   ← 真正的内容
  ```

  合并结果 tree 与第一父提交**完全相同**。这是一次只有父指针、没有内容的空合并。
- 后果：媒体/2.6 候选的 76 个文件在 0.0.45 里全部缺失，包括
  `backend/internal/service/media_pricing.go`、`media_usage.go`、`media_reference_usage.go`、
  `media_account_stats_pricing.go`、`backend/internal/repository/channel_repo_media_pricing.go`、
  `backend/internal/handler/compatible_video.go`、`backend/migrations/252_media_pricing.sql`、
  `frontend/src/components/admin/channel/MediaPricingEditor.vue`、`frontend/src/utils/mediaPricing.ts`、
  以及 `docs/gpt-image-26-direct.md` 等。
- `MediaPricing` 字面量在 0.0.45 上只有 3 处命中（旧的分组价卡字段），`media_pricing`、`reference_input`、
  `multidimensional` 全部 0 命中 —— 这套能力等于完全没有进主线。
- GPT-Image 2.6 的**上游协议支持**（`gpt-image-2.6-flare` / `-sunburst` 常量、`openai_images*.go`
  的处理）在 0.0.45 里存在，丢的是**我们自己的多维媒体计价引擎 + 前端编辑器 + 迁移**，以及
  候选分支上对 `openai_images*.go` 的后续修订（`75efdf187`、`4e80d5b5f` 三处文件内容与 0.0.45 不同）。

### 0.3 V3 状态柱「跑中间」的根因

`frontend/src/components/user/monitor/ChannelMonitorV3Timeline.vue` 的 `displayBars`：

```ts
const real = [...props.buckets].sort(...).slice(-props.length)   // 有数据的桶
for (const bucket of real) { bars.push(...) }                    // 先放真实柱
const missing = Math.max(0, props.length - real.length)
for (let index = 0; index < missing; index += 1) {               // 空格补在【右侧】
  bars.push({ key: `empty-${index}`, ... })
}
```

注释写着「Missing slots sit at the right (NOW) end」，轴标签也是「past ← → now」。于是 18 格里只有 6 格有数据时：6 根真实柱贴左，12 个空格贴右，而轴说右边是 now —— 用户看到的最新那根柱落在卡片中偏左，与「now」标签完全错位。

**修法**：空格补在**左侧**（past 端），真实柱保持靠右贴 now。真实桶不足时才补，且补的数量按 `length - real.length`。

### 0.4 单 key 用量慢的根因

| | 我们 | kiss |
|---|---|---|
| 路由 | 只有 `/user/api-keys/:id/usage/daily` | 多一个 `/user/api-keys/:id/usage` |
| 聚合路径 | `GetUsageTrendWithFilters(..., apiKeyID>0, ...)` | `GetAPIKeyStatsAggregated` + today 汇总 |
| 预聚合 | `shouldUsePreaggregatedTrend` 要求 `apiKeyID == 0`，**必然绕过** | 走 `idx_usage_logs_api_key_created_at` 有界聚合 |
| 结果 | 按天 `TO_CHAR(created_at)` 对 `usage_logs` 全量分组，最长 90 天 | 只聚合有界区间，key 名取索引 |

我们的 `keyusage` 按小时 rollup 机制存在（`backend/internal/keyusage/`，`SettingKey = api_key_usage_rollup`），但 `Config` 只在管理员显式 `PUT` 过才落库，**默认关闭**，所以线上等于没有。

**决定**：只走索引优化 —— 不改存储模型、不改默认开关。新增有界聚合查询 + today 汇总，并保持 `daily` 接口为趋势图数据源。

### 0.5 工单 UI「一大片白」的根因

`frontend/src/components/tickets/TicketWorkspace.vue` 自带 700+ 行 `<style scoped>`，其中 `.tickets-inbox` / `.tickets-detail` / `.ticket-list-item` / `.ticket-page-button` 全部**手写死色值**（`rgb(229 231 235)`、`#fff`、`rgb(249 250 251)`、`rgb(240 249 255)`），不引用站内设计系统的 `card` / `glass-card` / Tailwind token。同一仓库其它 11 个管理页全部用 `TablePageLayout` + `card` 类。深色模式靠一堆 `:global(.dark)` 覆盖补丁。这就是「不符合我们站内设计语言」和视觉上发白/发空的来源。

### 0.6 V3 与 V1/V2 的耦合现状

- `ChannelMonitorRuntime.ActiveProbesAllowed()` = `Enabled && (Mode==v1 || Mode==v3)` —— **V3 探针受全局 `channel_monitor_enabled` + `channel_monitor_mode` 控制**。
- `RunCheck` 在 `Mode==v3` 且分组有真实流量时直接 `ErrChannelMonitorOrganicTrafficPresent` 跳过探针。
- 探针身份来自 `channel_monitors` 表（V1 监控条目），V3 自己没有配置面。
- `channel_monitor_v2_config`（单行表 `id=1`）持有 `enabled` / `platforms` / `group_ids` / `health_thresholds`，V3 复用这张表读分组名单。
- 探针回退桶 `probeSamplesToMetrics` 在探测成功时把缓存率写成 `1/1 = 100%`（测试 `channel_monitor_v2_probe_fallback_test.go` 明确断言这一点）。
- 用户所说「必须要在分组里配置白名单和价格才能启用状态展示和监控」，对应的是 V2 seed 侧的 `platforms` + `group_ids` 门控，以及模型广场侧的价卡依赖；V3 应该只有自己的开关。

---

## 1. 十二项里要删的两项

| 项 | 前端 | 后端 | 数据库 |
|---|---|---|---|
| 分组缓存实测面板 | `GroupCacheTestView.vue`、`api/groupCacheTest.ts`、`utils/groupCacheTest.ts`、路由 `/admin/group-cache-test`、两处 `__tests__` | 无独立 handler（走 gateway URL + usage list） | 无自有表 |
| 账号告警与上游运营看板 | `OpsCenterView.vue`、`UpstreamMonitorView.vue`、`AccountOpsView.vue`、`api/admin/accountOps.ts`、`api/admin/upstreamMonitor.ts`、`components/admin/operations/*`、路由 `/admin/ops-center`、`/admin/upstream-monitor`、`/admin/account-ops` | `routes/account_ops.go`（21 个路由）、`handler/admin/account_ops_handler.go`、`upstream_monitor.go`、`account_upstream_billing_probe.go`、`account_upstream_billing_rates.go`、`service/account_ops*.go`（14 文件）、`service/upstream_monitor*.go`、`repository/account_ops*.go`、`repository/upstream_monitor.go` | `911_account_ops_notifications.sql` 建的 `account_ops_alerts` / `account_ops_threshold_monitors` / `account_ops_threshold_events` / `upstream_balance_samples`，另加 `033_ops_monitoring_vnext.sql` 的 `ops_alert_rules` / `ops_alert_events`、`037_ops_alert_silences.sql` 的 `ops_alert_silences` |

**用户决定：一起删 —— 账号不再有上游余额快照和倍率同步，账号列表里的「上游倍率同步」标记也去掉。**

这意味着要动的耦合点（已核实存在）：
- `service/ratelimit_service.go:23,335` —— `accountOps` 字段 + `observeAccountOps` 调用
- `service/upstream_billing_probe.go:238,299,303,663,835` —— `accountOps` 注入与 `needsBalanceProbe`
- `frontend/src/views/admin/AccountsView.vue:354-368` —— `upstream_billing_rate_sync_enabled` 标记与列头
- `handler/handler.go:11` / `handler/wire.go:55,65,317` —— Handler 装配

**删除形态**：代码、路由、Handler、Service、Repository、前端页面与 API 客户端、i18n 词条、settings 项全部移除；新增一条迁移 `DROP TABLE`。**不保留**半死代码。

---

## 2. 站内工单 UI 重做

参照 `AuditLogView.vue` / `GroupsView.vue` 的既有语言：`AppLayout` + `TablePageLayout` + `card` + `input` + `btn btn-*` + `Select`，色值全部来自 token（`text-gray-900 dark:text-gray-100`、`border-gray-200 dark:border-dark-700`、`bg-white dark:bg-dark-800`、`bg-primary-*`）。

实施要点：
1. 删掉 `TicketWorkspace.vue` 的 700+ 行 scoped 手写样式，改成布局用 Tailwind + 设计系统类。
2. 页面外壳改为 `TablePageLayout`（`#filters` 放搜索/筛选/操作，`#default` 放主区）。
3. 会话气泡、时间线事件条、分页按钮统一到 `card` / `bg-primary-600 text-white` / `btn btn-secondary`。
4. 列表项选中态用 `bg-primary-50 ring-1 ring-primary-200 dark:bg-primary-900/30`，不再手写 `rgb(240 249 255)`。
5. 保留全部既有功能（未读角标、主动发起、通知设置、回复状态选择、分页、移动端单列切换）。
6. i18n 已核：88 个 `t('tickets.*')` 键全部在 `zh/tickets.ts` 里存在，只做视觉重做，不动文案。

---

## 3. 单 key 用量统计（抄 kiss）

### 3.1 后端

新增：
- `GET /api/v1/user/api-keys/:id/usage?days=1..90&timezone=` → `GetMyAPIKeyUsage`
  - 载荷：`{ api_key_id, start_date, end_date, timezone, today: {requests,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,total_tokens,cost,actual_cost}, range: {...}, items: [{date, ...}] }`
- `UsageService.GetAPIKeyUsageSummary(ctx, apiKeyID, start, end)` → 复用已存在的 `GetAPIKeyStatsAggregated`（走 `idx_usage_logs_api_key_created_at`）
- `handler/usage_handler.go` 增加 `GetMyAPIKeyUsage`，归属校验与 `GetMyAPIKeyDailyUsage` 一致（`apiKey.UserID != subject.UserID → 403`）
- 复用 `panelRateLimiter.Heavy()`

### 3.2 前端

`KeyUsageDialog.vue` 改为：顶部「今日」四格 + 「区间合计」四格，下方保留按天趋势。调用新接口。

### 3.3 性能判据（必须实测，不接受"应该快了"）

同一 key、同一区间，在**生产库只读副本或生产等价数据量**上对比：
- 旧路径（`GetUsageTrendWithFilters`，apiKeyID>0）耗时
- 新路径（`GetAPIKeyStatsAggregated` + today）耗时

---

## 4. 并发与实时使用中

### 4.1 用户当前并发（抄 kiss）

kiss 的做法（已读源码）：
- `middleware/usage_inflight.go` 在鉴权通过后包一层 `trackUsageInflight`，把 `{request_id,user_id,api_key_id,group_id,email,model,stream,ws,started_at,reserved_amount}` 写 Redis；1 秒一次，首字到达再补写一次。
- `repository/usage_inflight_cache.go` 存快照；`service.SummarizeUserGroupLoad` 按分组合并出 `inflight` 与 `models`。
- `UserConcurrencyCell.vue` 悬浮展开「按分组的在途明细」。

我们需要的新东西：**在途请求登记表**（我们目前只有 `billing:inflight:{uid}` 的金额 ZSET / 金额 HASH，没有 model / group / started_at 快照）。

实施：
- 新增 `repository/usage_inflight_cache.go`：key `usage:inflight:{uid}` = HASH，field=requestID，value=JSON 快照，TTL 跟随最长请求（与 billing inflight 一致，不续期）。
- 新增 middleware 在已验证身份后登记/注销；必须跳过 `/api/v1/...` 之外的探测与 OPTIONS。
- `GET /admin/users` 已返回 `current_concurrency`（`user_handler.go:26`），保持；新增 `GET /admin/users/:id/inflight` → 按分组明细。
- `UserConcurrencyCell.vue` 重做：复用现有 `BaseDialog` / `HelpTooltip` 与 token，不引入 kiss 的 Nuxt UI（我们没有）。

### 4.2 分组「当前实时使用中的用户数」（用户的改写）

**口径（用户已定）**：此刻有请求正在处理的用户 —— 即在途请求去重后的 user_id 数。请求结束即不算。

- 数据源：上面新增的在途登记表，按 `group_id` 聚合 `COUNT(DISTINCT user_id)`。
- `GET /admin/groups/capacity` 已有 `GroupCapacitySummary`；扩一个新字段 `active_users`（在途去重人数），**不复用** `sessions_used`（会话语义不同）。
- 点开名单：`GET /admin/groups/:id/active-users` → `[{user_id, username, email, inflight_count, models[]}]`。
- **可见性**：分组管理页是管理员专属，人数与名单都只给管理员。普通用户侧不新增任何入口（用户已明确纠正）。
- 展示位置：`GroupsView.vue` 里 `GroupCapacityBadge` 旁边新增「实时使用中 N」徽标，点击开 `BaseDialog` 列表。

---

## 5. models.dev 实时价格 + 覆盖层

### 5.1 抄 kiss 的机制（已读源码）

`service/model_metadata_catalog.go`：
- `ModelMetadataSourceModelsDev = "models.dev"`
- `Sync(ctx)` 拉 models.dev registry，`fetchOnce.Do("models.dev", ...)` 去重，失败保留上一份 good 数据（不阻塞）
- 价格三元优先序：**已配置价卡（`ChannelModelPricing`）> models.dev 参考价 > 内置兜底**
- `ModelCatalogPricing` = USD / 1M tokens 块；`ModelCatalogRequestPricing` = 按次价块
- 明确注释：models.dev 的价格**只作展示**，实际计费始终走价卡

### 5.2 我们的落法（用户决定：自动写入 + 热更新 + 留变更记录）

- 新增 `service/model_pricing_sync.go`：定时（沿用 `pricing.hash_check_interval_minutes`）拉 models.dev，算出与当前 `pricing.override_file` 的差异。
- 写入规则：**自动写入并热更新**，只补/改「我们价表里没有手动定价」的模型；已有手动定价的模型**不覆盖**（这是同一个决定的实现细节，写进 ADR）。
- 变更记录：新增表 `model_pricing_sync_history(id, model, field, old_value, new_value, source, applied_at, reason)`；后台列表页 `/admin/model-pricing-sync` 显示「什么时候、哪个模型、哪个字段、从多少改到多少」。
- 热更新：写完后直接调用 `PricingService.reloadIfCustomFilesChanged()` 同款路径（它已按文件指纹热重载，不需要重启）。
- 安全边界：写入前校验 JSON 可解析、写临时文件 + `rename` 原子替换、失败保留原文件并记录错误。

### 5.3 未被要求、但必须点出来的一件事

models.dev 的价是**参考价**，我们的价卡是**结算价**。自动覆盖层如果优先级高于分组价卡，会把商业定价搞坏。所以写入的覆盖层只在 `ChannelModelPricing` 缺失时生效，这条必须在实现里用测试钉死。

---

## 6. OpenAI 30 分钟 / 12 小时缓存写入计费

### 6.1 官方口径（已查证）

| | 倍率 |
|---|---|
| 缓存读 | 0.1×（GPT-6.1 Sol / Terra 为 0.05×） |
| 30 分钟缓存写入 | 1.25× 输入价 |
| 12 小时缓存写入 | 2.5× 输入价 |

- 12 小时 TTL 通过 Responses API 的 `prompt_cache_options.ttl = "12h"` 请求；默认 30 分钟。
- 写入价是**总价**，不是在输入价上加收。
- 30 分钟升 12 小时会**重新计一次 12 小时写入，不抵扣**。
- 读价两种 TTL 相同。

### 6.2 我们现在的状态（已核实）

- `billing_service.go` 里 `CacheCreationPricePerToken` / `CacheCreation5mPrice` / `CacheCreation1hPrice` 是**Anthropic 语义**（5 分钟 / 1 小时 ephemeral），不是 OpenAI 的 30 分钟 / 12 小时。
- `openai_gateway_forward.go:661` 把 `prompt_cache_options` 当**不支持字段直接删掉**。
- `openai_gateway_usage.go:185` 的 `actualInputTokens = InputTokens - CacheReadInputTokens - CacheCreationInputTokens` 只有三段，没有区分 30m/12h 写入。
- 结论：**OpenAI 的 30m/12h 缓存写入计费完全没做**，用户判断正确。

### 6.3 要做的

1. `Usage` 结构增加两个字段：`CacheWrite30mTokens` / `CacheWrite12hTokens`（DTO + DB 列 + 日志）。
2. 计价：按 `input_price × 1.25` 与 `input_price × 2.5` 分别算，**不与 Anthropic 的 5m/1h 混用**（两个 provider 语义不同，字段分开）。
3. `prompt_cache_options` 不再无条件删除：OpenAI 平台（API-key 与 OAuth 各自核对）保留并透传，其它平台仍删。
4. 上游不返回写入分档时的降级：只拿得到 `cached_tokens` 时按读计，不臆造写入。
5. 记录口径：`usage_logs` 新增两列，`ModelPlaza` / 用量页展示时分开显示。

---

## 7. V3 监控独立化（本次最大的一块）

### 7.1 现状耦合（见 §0.6）

### 7.2 目标形态

**V3 是独立产品，不是 V1/V2 的一个 mode。**

1. **自己的开关**：`channel_monitor_v3_config`（单行表 `id=1`），字段：
   - `enabled`（V3 总开关，只控 V3）
   - `groups`（JSONB：`[{group_id, enabled, models[], probe_enabled}]`）
   - `probe`（JSONB：`{endpoint, api_key_encrypted, provider, primary_model, extra_models[], interval_seconds}`）
   - `cache_average_window_days`（默认 30）
   - 不再读 `channel_monitor_enabled` / `channel_monitor_mode` 做门控。
2. **自己的设置页**：`/admin/channel-monitor-v3`，独立路由、独立菜单项，包含分组勾选、探针配置、间隔、缓存均值窗口。
3. **自己的探针执行器**：`ChannelMonitorV3ProbeRunner`，只受 `channel_monitor_v3_config.enabled` 控制；不再查 `channel_monitors` 表取身份（用户已定：V3 设置页自己配一条监控专用 key）。
4. **去掉分组白名单/价格前置**：V3 seed 只看 `channel_monitor_v3_config.groups`，不看分组的 `model_allowlist` / `model_pricing` / `platforms`。
5. **探针成功的缓存率**：不再写 100%。改为读该分组最近 N 天（默认 30）真实流量的缓存率均值：
   - 有真实均值 → 用均值
   - 无真实流量、无法算均值 → **显示「—」未知**（用户已定），不编造
6. **状态柱几何**：修 `displayBars` 补位方向（§0.3）。
7. **迁移**：初始化时把当前 V1/V2 里实际生效的探针条目与分组配置**复制一份**进 `channel_monitor_v3_config`（用户已定）。之后两套互不影响。

### 7.3 不做什么

- 不删 V1 / V2。它们继续存在、继续各管各的。
- 不改 V1/V2 的任何既有门控语义。
- 不把 V3 塞回 `channel_monitor_mode` 枚举里（虽然枚举值 `v3` 已存在，但 V3 的运行不再依赖它）。

---

## 8. 媒体 / GPT-Image 2.6 候选合回

### 8.1 事实

`d43511214` 相对 `25608d216` 有 320 个提交（其中大量是历史），真正需要的是 21 个非合并提交里的媒体相关部分。与当前 HEAD 做三方合并（`--merge-base=25608d216`）有 **46 处冲突**，集中在：
- `backend/cmd/server/{VERSION,main.go,wire.go,wire_gen.go,wire_gen_test.go}`
- `backend/go.sum`
- `backend/internal/handler/{grok_media.go,seedance.go,...}`
- `backend/internal/pkg/openai/constants.go`
- `backend/internal/repository/{gateway_cache.go,http_upstream.go}`
- `backend/internal/service/{gateway_service.go,grok_media.go,model_plaza_service.go,openai_gateway_forward.go,openai_gateway_service.go,openai_gateway_usage.go,openai_images*.go}`

### 8.2 策略

**不整树合并**。按能力逐组挑提交、逐文件移植，每组移植后跑测试：

| 组 | 提交 | 内容 |
|---|---|---|
| A 多维媒体计价 | `1f8a2757f` `a8bd096a5` `4815122b4` | `media_pricing.go` + 迁移 252 + 前端编辑器 |
| B 协议用量与结算 | `2bb6443d5` `ed8cc94fe` `88ed3b1e1` | 异步完成计费携带协议用量 |
| C GPT-Image 2.6 协议 | `b2605854d` `f99a9c757` `4e80d5b5f` `75efdf187` | direct 路由、settlement、hosted usage |
| D 广场价卡一致性 | `b3651936f` `4452ab9ac` `3bf839a80` | 价卡对齐与 lint |
| E 租约恢复 | `d43511214` | 过期 owner lease 的异步计费恢复 |

每组单独 commit，测试绿后再进下一组。**反向对照**：每组移植后必须能证明原始提交上的行为缺陷在移植前存在、移植后消失。

---

## 9. 验收矩阵

| 项 | 验收方式 |
|---|---|
| 两项删除 | 全仓 `grep` 无残留符号；`/admin/ops-center`、`/admin/upstream-monitor`、`/admin/group-cache-test` 路由 404；账号列表无上游倍率列；迁移后四张表不存在 |
| 工单 UI | 真实浏览器打开 `/admin/tickets` 与 `/tickets`，浅/深色各一遍，无裸色值；与 `AuditLogView` 并排比对视觉语言 |
| 单 key 用量 | 新接口返回 today + range + items；同 key 同区间新旧路径耗时对比 |
| 实时并发 | 制造 N 个在途请求，`/admin/users` 的 `current_concurrency` 与明细一致；请求结束后归零 |
| 分组实时用户 | 制造在途请求，分组徽标数字与名单人数一致；请求结束即减 |
| models.dev | 断网时保留上一份 good 数据；价格变更写入覆盖层并热生效；变更记录有行 |
| 缓存写入计费 | 构造 30m / 12h 两种 `prompt_cache_options` 请求，账本分别按 1.25× / 2.5× 扣；不返回分档时不臆造 |
| V3 独立 | 关掉 `channel_monitor_enabled` 后 V3 仍能按自己开关工作；分组无白名单无价卡也能出现；探针成功且无真实流量时显示「—」而非 100%；有真实流量时显示 30 天均值 |
| 状态柱 | 少数据场景下最新柱贴卡片右侧，轴标签对齐 |
| 媒体合回 | 五组各自测试绿；价卡、按次价、分时倍率、参考输入计费逐项回读 |

---

## 10. 未解决 / 需要现场确认

1. **生产库实际数据量**：单 key 用量提速的实测基准要在生产等价数据上做，不能只在本地空库跑。
2. **`pricing.override_file` 生产实际路径**：写入前必须现场读回真实路径与权限，不能按代码默认值假设。
3. **GitHub 账号仍被停用**（实测 `/user` 返回 `Sorry. Your account was suspended`，`git ls-remote` 403）。远端分支、tag、Release 这次仍无法交付，只能本地 tag + 生产换代；此项单列为未完成。
4. **V3 探针专用 key**：需要用户在 V3 设置页填一次（endpoint / key / model）；代码就位后需要真实填写才能验收探针路径。
