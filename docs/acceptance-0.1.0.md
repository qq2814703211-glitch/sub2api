# 0.2.20-GloryCode-0.1.0 本地验收记录

**日期**：2026-11-08
**环境**：本机 Docker（`docker-compose.local-acceptance.yml`），app `127.0.0.1:8081`，
postgres `127.0.0.1:5433`，redis `127.0.0.1:6380`，独立 project 与卷。
**镜像**：`sub2api:0.2.20-glorycode-0.1.0-acceptance`，`--build-arg VERSION=0.2.20-GloryCode-0.1.0`
**构建**：rc=0
**版本回读**：`GET /api/v1/admin/system/version` → `0.2.20-GloryCode-0.1.0`

## 十项交付的落地证据

| # | 交付项 | 证据 | 状态 |
|---|---|---|---|
| 1 | 删掉分组缓存实测面板 + 账号告警与上游运营看板 | `839945e2d..HEAD` 删除 74 个文件；`/admin/upstream-monitor/summary`、`/admin/group-cache-test/run`、`/admin/account-ops/alerts` 全部 404 | 完成 |
| 2 | 站内工单 UI 改设计语言 | `TicketWorkspace.vue` 917 行重写；手写色值 18 → 0；`dark:` 变体 60 处（站内标准卡片 `UserDashboardStats.vue` 为 64 处） | 完成 |
| 3 | 单 key 用量统计移植 + 查询性能 | 新接口 `GET /user/api-keys/:id/usage` http=200，today + range 双汇总，30 个日趋势点；见下文性能段落 | 完成 |
| 4 | 用户当前并发展示 | `UserConcurrencyCell.vue` 已接入 `UsersView.vue`（577 行引用，821 行 import） | 完成 |
| 5 | 分组「实时使用中」人数 + 名单 | `GroupsView.vue` 使用 `capacityMap.activeUsers`；接口 `active_users` 字段实测返回 | 完成 |
| 6 | models.dev 实时价格 → 覆盖层 + OpenAI 缓存写入分档 | 迁移 `915_openai_cache_write_ttl_tiers.sql`、`916_model_pricing_sync_history.sql` 已执行；`usage_logs` 实测存在 `cache_write_30m_tokens` / `cache_write_12h_tokens` / `cache_write_ttl_cost` 三列；11 个相关单测全过 | 完成 |
| 7 | 状态柱最新柱贴右 | `ChannelMonitorV3Timeline.vue` 补位改左侧；3 个 vitest 全过；stash 反向对照证明测试能区分修复前后 | 完成 |
| 8 | V3 监控完全独立 | 独立表 `channel_monitor_v3_config`；`GET /admin/channel-monitor-v3/config` 返回 `enabled:false, groups:[], probe:{...}, cache_average_window_days:30`；V1/V2 表 `channel_monitor_v2_config` 仍独立存在 | 完成 |
| 9 | 探针缓存率改取 30 天均值 | ADR 0009；配置项 `cache_average_window_days:30` 在 V3 config 中实测可见 | 完成 |
| 10 | 核查 `e8c44c57` 并补媒体 / GPT-Image 2.6 | `e8c44c57` 已包含（四个页面文件在树上可读）；媒体能力从 `d43511214` 以 `25608d216` 为基点三层恢复，178 个文件 | 完成 |

## 单 key 用量性能（生产实测，567 万行 key）

见 `docs/adr/0012`。重要更正：**22.5 s 不是优化前基线**，那是磁盘冷读；
`ANALYZE` 后原样重跑同一查询即 5.7 s、计划不变。可归因于改动的收益是：

- 覆盖索引 `idx_usage_logs_apikey_created_cover`（1131 MB，`valid=true`，回表 0.8%）
- 去掉冗余 `user_id` 谓词：**8.9 s → 4.9 s**
- `date_trunc` 分组：本地 100 万行 34.5 ms → 20.8 ms（约 40%）

本地验收栈上接口本身的成本（33 万行，目标 key 15 万行）：http=200，
`today.requests=157`、`range.requests=48500`、`items=30`，与旧 `/usage/daily`
接口同为 30 个点，输出一致。

## 已知的基线失败（非本次引入）

以下 5 个测试在基线 `sub2api-verify-baseline` 上同样失败，已逐个对照确认：

- `TestLoginWritesPasswordCopyAfterSuccessfulTokenGeneration`
- `TestLoginFallbackAccessTokenWritesPasswordCopy`
- `TestPasswordMFAWritesCopyOnlyAfterValidTOTPAndTokenSuccess`
- `TestProductionOpenAIPricingOverrideShortLongAndFast`
- `go vet ./internal/service/` 的 `undefined: apiKeyQueueCacheStub`（stub 分散在别的测试文件）

另有并行执行下的偶发失败（单独跑全过）：
`TestInflightEstimate_AccountMappingNoDBAndBoundedMemory`、`TestGPT26*`、
`TestGetMyAPIKeyUsage*`。属测试间资源竞争，非功能回归。

## 已补验的两项（此前标为「未验收」）

### models.dev 同步端到端（在本地栈真实跑通）

给 `docker-compose.local-acceptance.yml` 配上 `PRICING_OVERRIDE_FILE` 后重启，
启动即同步一轮：

- 日志：`model pricing sync applied {"changes": 1435, "models": 479}`
- 产出 `/app/data/pricing.override.json`，76,717 字节、479 个模型，
  字段形状为 `input_cost_per_token` / `output_cost_per_token` /
  `cache_read_input_token_cost` / `cache_creation_input_token_cost`
- 容器可直连 `https://models.dev/api.json`

**同步过程中发现并修复了一个真 bug。** 归属判据原本只看「字段名是否出现在
变更史里」，而真实顺序常常是同步先写入、运营后改价。实测把
`amazon.nova-micro-v1:0.input_cost_per_token` 手工设成 `9.99e-07` 后重启，
同步把它改回了 `3.5e-08`（日志 `changes: 1`，历史表留下
`old=9.99e-07 → new=3.5e-08`）。

改为比对**值**：仓库用 `DISTINCT ON (model, field) ... ORDER BY applied_at DESC`
返回上次同步写入的 `new_value`，当前值与之相等才允许跟随官方价。修复后同一场景
复验 `PRESERVED`，且该模型在历史表里**没有**新增记录（说明整模型被正确跳过），
同时其他模型照常同步（1435 变更 / 479 模型）。

新增 `TestModelPricingSync_ManualEditAfterPriorSyncSurvives`（预置变更史）。
现有 8 个同步测试全部建立在空变更史上，覆盖不到这个顺序。已做反向对照：
把判据退回旧版该用例失败，恢复后通过。

补充事实：`override_file` 没有人工编辑接口，只有同步任务会写它；运营真正的
手动定价位置是分组价卡，解析顺序是先查价卡、命中则不读全局覆盖层。

### V3 探针真实探测（在本地栈真实跑通）

- 该路由的 endpoint 校验硬要求 `https`（V1 遗留规则），`http://127.0.0.1:8080`
  会被 `CHANNEL_MONITOR_ENDPOINT_SCHEME` 拒绝
- 配置保存是乐观锁，`version` 过期会返回 409
- 用 `https://api.openai.com/v1` + `gpt-4o-mini` 保存成功：
  `enabled=true, groups=1, api_key_set=true`
- `POST /admin/channel-monitor-v3/probe` → `http=200 {"ok":true}`
- 样本表 `channel_monitor_v3_probe_samples` 写入 1 行：
  `group_id=1, model='', status=failed, checked_at=02:39:06`
  —— 假 key 导致 failed 是正确结果，证明探针确实发出了请求并落库
- 监控分组只在本页勾选，未触碰模型白名单或价卡

### 交付项 9（缓存率不写 100%）的代码级证据

`applyV3ProbeFallbackToMatrix` 里 `cacheRate` 是 `*float64`，只有
`GroupCacheRates(groupIDs, CacheAverageWindowDays)` 在 30 天真实流量里查到值才赋值，
否则保持 nil。`probeSamplesToMetricsWithCacheRate` 仅在
`cacheRate != nil && 0 <= *cacheRate <= 1` 时填 `CacheRate` 并置
`CacheRateKnown=true`；否则 `CacheRate=0`、`CacheRateKnown=false`（前端显「—」）。
任何分支都不会写入 100%。对应 10 个单测全过，含
`TestChannelMonitorV3ProbeBucketHasNoCacheRateWithoutRealTraffic`
与 `TestChannelMonitorV3ProbeBucketUsesRealTrafficCacheAverage`。

## 仍需你决定

- **`work_mem` 调优**：见 ADR 0012，属生产参数变更，未实施。

## 已完成的视觉验收（浏览器实际渲染 + 截图判读）

- **管理员工单页 `/admin/tickets`**：深色主题与站内一致，无大片白、无错位/重叠/高度塌陷；
  空状态（「还没有工单」）居中。此前「一大片白」的问题不再出现。
- **V3 独立设置页 `/admin/channel-monitor-v3`**：侧边栏有独立「渠道监控 V3」入口，
  页面文案明确「不受渠道监控总开关与 V1/V2 模式影响」「不需要模型白名单或价卡」。
- **发现并修复一个真实缺陷**：该页「缓存率均值窗口」的数字输入框是全页唯一发白控件。
  根因是 `frontend/src/style.css` 里 `.dark .input` 的 `color-scheme: dark` 列表
  只覆盖 date/time/datetime-local/month/week，漏了 `type=number`，浏览器给它套了
  浅色原生外观。已补 number（连带 search / color）。修复后实测计算样式
  `bg=rgba(15,17,19,0.8)`、`color=rgb(241,242,244)`、`colorScheme=dark`，
  截图复核确认已融入深色卡片。这是全站共性修复，不止 V3 页。

## 未验收 / 需另行决定

- **models.dev 同步的端到端写入**：同步是后端定时任务，默认 `enabled:false`、
  `override_file` 为空。路由只暴露只读的 `/status` 与 `/history`，没有手动触发接口。
  行为由单测覆盖（`TestModelPricingSync_SuccessfulWriteReplacesAtomically`、
  `TestModelPricingSync_NeverOverridesManualPricing` 等 8 个），但**未在本地栈上
  真实启用跑过一次**。要在本机验证需改配置启用同步并等一个周期。
- **V3 监控的真实探针**：配置默认关闭、无 endpoint/api key，未做真实探测。
