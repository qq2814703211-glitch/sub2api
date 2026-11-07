# 十项交付的逐条实测记录

**版本**：`0.2.20-GloryCode-0.1.0`
**环境**：本机 Docker 栈，app `127.0.0.1:8081`
**方式**：每条都是运行中系统上的观测，不是代码阅读。

## 1. 删掉分组缓存实测面板 + 账号告警与上游运营看板

迁移 `915_drop_ops_and_upstream_monitor_tables.sql` 在真实库执行后逐表核对：

```
account_ops_alerts               -> ABSENT
account_ops_threshold_monitors   -> ABSENT
account_ops_threshold_events     -> ABSENT
upstream_balance_samples         -> ABSENT
upstream_rate_changes            -> ABSENT
upstream_monitor_state           -> ABSENT
upstream_monitor_daily           -> ABSENT
upstream_monitor_alert_rules     -> ABSENT
upstream_monitor_alerts          -> ABSENT
settings['upstream_billing_probe_settings'] -> ABSENT
```

刻意保留并有注释说明：`ops_alert_rules` / `ops_alert_events` / `ops_alert_silences`
（属于 `/admin/ops` 运维中心自己的告警，不是被删的账号告警）、
`channel_monitors` / `channel_monitor_v2_config`（V1/V2 监控在用）。

API 层：`/admin/upstream-monitor/summary`、`/admin/group-cache-test/run`、
`/admin/account-ops/alerts` 全部 404。

## 2. 站内工单 UI 改站内设计语言

- `TicketWorkspace.vue` 重写 917 行，手写色值从 18 处降到 0
- `dark:` 变体 60 处（站内标准卡片 `UserDashboardStats.vue` 为 64 处，密度相当）
- 浏览器渲染 `/admin/tickets` 截图判读：深色主题一致，无大片白，
  无错位/重叠/高度塌陷，空状态居中

## 3. 单 key 用量统计 + 查询性能

接口 `GET /api/v1/user/api-keys/:id/usage?days=30`：

| key 状态 | 观测 |
|---|---|
| 有数据（id 990001） | `today.requests=229`、`range.requests=48572`、`items=30` |
| 无数据（noise 系列） | 今日显「—」+「今日无记录」，不写假 0 |
| 不存在的 key | 404（路由已接，不是 401/405/500） |

浏览器点「明细」实开面板，确认结构：「今天 / 近 7 天 / 近 30 天」切换、
时区标注 `Asia/Shanghai`、今日头牌、所选区间汇总、`缓存读 / 缓存写`、
日期表头（日期 / 请求 / TOKEN / 缓存读 / 缓存写 / 实际扣费）。

性能见 `docs/adr/0012`：覆盖索引 1131 MB valid=true，
同样 567 万行窗口走 `user_id` 索引 8.9 s、走覆盖索引 4.9 s。

## 4. 用户当前并发展示

`UserConcurrencyCell.vue` 已接入 `UsersView.vue`（577 行使用、821 行 import）。

## 5. 分组「实时使用中」人数 + 名单

分组列表容量列实测渲染 `0` + 「实时使用中」标签。点开后弹层标题为
「『default』实时使用中的用户」，说明文字：

> 此刻有请求正在处理的用户。请求一结束就不算，与「会话数」是两个不同的口径。

空态：「当前没有用户正在使用该分组」。
接口 `/admin/groups/capacity-summary` 返回字段 `active_users`（不是 5 分钟计数）。

## 6. models.dev 价格同步 + OpenAI 缓存写入 TTL 分档

同步在同一栈上真实跑通：`changes: 1435, models: 479`，
产出 `/app/data/pricing.override.json`（76,717 字节）。

迁移已执行，`usage_logs` 实测存在三列：
`cache_write_30m_tokens` / `cache_write_12h_tokens` / `cache_write_ttl_cost`。
TTL 相关单测 11 个全过，含 `TestOpenAICacheWriteTTLTokensFromUsage_ParsesBothShapes`、
`TestOpenAICacheWriteTTLFromPromptCacheOptions`。

同步修复见 `docs/adr/0010` 与 `docs/acceptance-0.1.0.md`。

## 7. 状态柱最新柱贴右

`ChannelMonitorV3Timeline.vue` 补位改左侧。合并后复跑
`npx vitest run src/components/user/monitor/__tests__/ChannelMonitorV3Timeline.spec.ts`
→ **3 tests passed**。此前用 stash 反向对照证明过该测试能区分修复前后。

## 8. V3 监控完全独立

- 独立表 `channel_monitor_v3_config`（单行），与 V1 `channel_monitors`、
  V2 `channel_monitor_v2_config` 并存互不影响
- 独立页 `/admin/channel-monitor-v3`，侧边栏有「渠道监控 V3」独立入口
- 页面文案实测：「V3 是独立产品：本页开关只控制 V3，不受「渠道监控」总开关与
  V1/V2 模式影响；分组只要能在这里勾选即可监控，不需要模型白名单或价卡。」
- 真实探测跑通：endpoint `https://api.openai.com/v1`、模型 `gpt-4o-mini`、
  保存后 `enabled=true, groups=1, api_key_set=true`，
  `POST /probe` → `{"ok":true}`，
  样本表 `channel_monitor_v3_probe_samples` 落库 1 行
  （`group_id=1, status=failed, checked_at=02:39:06`；假 key 导致 failed 是正确结果）

## 9. 探针缓存率不写 100%

代码级证据：`applyV3ProbeFallbackToMatrix` 里 `cacheRate` 为 `*float64`，
仅当 `GroupCacheRates(groupIDs, CacheAverageWindowDays)` 在 30 天真实流量查到值才赋值；
`probeSamplesToMetricsWithCacheRate` 只在 `cacheRate != nil && 0 <= *cacheRate <= 1`
时填 `CacheRate` 并置 `CacheRateKnown=true`，否则 `CacheRate=0`、
`CacheRateKnown=false`（前端显「—」）。任何分支都不产生 100%。

10 个单测全过，含：
`TestProbeSamplesToMetricsLeaveCacheRateUnknownWithoutRealTraffic`、
`TestProbeSamplesToMetricsStampRealTrafficCacheAverage`、
`TestChannelMonitorV3ProbeBucketHasNoCacheRateWithoutRealTraffic`、
`TestChannelMonitorV3ProbeFallbackNeverOverwritesOrganicBuckets`。

## 10. 核查 e8c44c57 并恢复媒体 / GPT-Image 2.6

`e8c44c57` 已在基线中（四个页面文件在 `839945e2d` 树上可读）。
媒体与 GPT-Image 2.6 从 `d43511214` 以 `25608d216` 为共同基点三层恢复，
178 个文件。

最终树可用性实测（不是看文件是否存在，而是跑测试）：

- `service.MediaPricing` 类型、handler DTO 字段、repo 的
  `marshalMediaPricing` / `unmarshalMediaPricing` 均已接上
- `frontend/src/components/admin/channel/MediaPricingEditor.vue` 存在
- 媒体计价测试全过：`./internal/handler/`、`./internal/handler/admin/`、
  `./internal/repository/` 三个包 ok
- GPT-Image 2.6 兜底价实测：`pricing_service.go` 第 1272、1726 行均含
  `gpt-image-2.6` / `-flare` / `-sunburst` 三型号；
  `TestGPTImage26*` / `TestGPT26*` / `TestIssue8534*` 共 14+ 用例全过
