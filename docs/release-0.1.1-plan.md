# 0.2.20-GloryCode-0.1.1 方案

基线 `e0f5a47e0`（= 生产在跑的 0.1.0）。七件事，逐项写清现状、目标、验收。

## 1. 渠道监控 V3 设置页并回 `/admin/channels/monitor`

**现状**：V3 是侧边栏独立项 `/admin/channel-monitor-v3`（`AppSidebar.vue:794`、`router/index.ts:566`），单独一页。

**目标**：并回 `/admin/channels/monitor`，与 V1/V2 并排成同一个页面里的一个分区/页签。

**做法**：
- `ChannelMonitorView.vue` 内加 V3 分区（或页签），嵌入现有的 `ChannelMonitorV3View` 内容（拆出可复用子组件，不留两套 UI）。
- 删侧边栏 `/admin/channel-monitor-v3` 入口；路由改为重定向到 `/admin/channels/monitor`，避免旧书签 404。
- 保留 V3 独立的后端配置表与探针身份（这是 ADR 0008 的实质，不动），只改「设置界面在哪」。
- 三条 `channel_monitor_mode`（v1/v2/v3）的 gate 不动。

**验收**：`/admin/channels/monitor` 页面上能同时看到 V1/V2 与 V3 两套配置；侧边栏只剩一个「渠道监控」；旧路径不 404。

## 2. 删除请求采集（admin/request-captures）

**现状**：`97da89342` / `a17916247` 引入，对应 kiss-kedaya 的 `270_request_captures.sql` + `RequestCaptureView.vue`。库里有 `request_capture_tasks`、`request_capture_records` 两表，`DATA_DIR/request-captures` 有落盘。

**已核实**：确实来自 kiss-kedaya（本地仓库 `~/Global-repos/sub2api-kiss-kedaya` 有同名迁移与视图）。

**目标**：整组删除 —— 路由、侧边栏入口、前端视图、API client、后端 handler/routes/service/repository、迁移表、落盘目录逻辑。

**做法**：按 §1 的 `remove-ops` 车道同一模式（保留新增、执行删除），新增一条迁移 `917_drop_request_capture_tables.sql` 删两表 + 清相关 settings 键。删除要能整体删干净，不留孤儿路由或死代码。

**验收**：`/admin/request-captures` 404；侧边栏无入口；两表不存在；`go build ./...` 与前端 typecheck 通过；全仓 grep 无残留引用（除迁移与历史注释）。

## 3. admin/users 实时并发 + 点开看分组名单

**现状**：功能**已做**（`UserConcurrencyCell.vue` 已接 `UsersView.vue:577`），但 `concurrency` 列写在 `DEFAULT_HIDDEN_COLUMNS` 里（`UsersView.vue:920`），**默认隐藏**，所以页面上看不到。

**目标**：默认可见；点开能看到该用户此刻在途请求的分组名单。

**做法**：把 `concurrency` 从 `DEFAULT_HIDDEN_COLUMNS` 移除。核对 `UserConcurrencyCell` 的明细弹窗是否真的按分组聚合（后端 `/admin/usage/inflight`）。

**验收**：默认打开 `/admin/users` 就能看到并发列；点数字弹出分组名单；`current_concurrency` 非 0 时数字真实变化。

## 4. keys 页面单 key 用量查询提速

**现状**：上一版已加覆盖索引 `idx_usage_logs_apikey_created_cover`（1135MB，valid），把 567 万行窗口从 8.9s 降到 4.9s。但用户实测仍慢。

**目标**：接近 kiss-kedaya 的「实时」体感。

**方向**（先量后改，不猜）：
- 逐条 `EXPLAIN (ANALYZE, BUFFERS)` 量出真实耗时构成：是扫描慢、还是聚合慢、还是「每次请求都现算」。
- kiss-kedaya 快的原因要**实测确认**：读它的实现，看是预聚合表、还是限了小窗口、还是缓存。
- 候选改动：预聚合日表（`usage_log_daily_rollups` 之类）或 Redis 短 TTL 缓存；按实测收益排序实施。

**验收**：生产实测前后数字并列（同一条查询、同一执行计划口径）；不许拿冷热缓存差当优化成果。

## 5. image-studio 拆分 AI 改写 Key 与生图 Key

**现状**：`rewritePrompt()`（`features/imagegen/api.ts:149`）的 `key` 参数直接复用生图的 `apiKey`，模型只有一个 `rewriteModel`（默认 `gpt-6-astra`）。GloryCode 原本是两把 Key 分开。

**目标**：
- AI 改写有**独立的 Key 配置**，与生图 Key 分开。
- AI 改写可配置**协议**（如 OpenAI 兼容 / Anthropic / Responses）、**模型**、**思考强度**。

**做法**：加一套独立的 rewrite 配置（key + protocol + model + reasoning effort），存 localStorage（与生图配置同源），请求按 protocol 走对应端点与报文形状。思考强度按协议映射（OpenAI `reasoning_effort`、Anthropic `thinking.budget_tokens` 等）。

**验收**：只配改写 Key 不配生图 Key 时改写可用、生图报错提示缺失；反之亦然；三种协议各发一次真实请求成功；思考强度不同档在报文里可观测。

## 6. 媒体多维定价改可视化配置

**现状**：`MediaPricingEditor.vue` 42 行，就是一个 `textarea` 写 JSON。数据模型见 `types/mediaPricing.ts`：5 种 unit、13 个 conditions 维度、6 个价格字段、3 个顶层字段。

**目标**：做成与其它计价规则一致的可视化编辑器，**功能不缩水**（JSON 能表达的都能配），计费不出 bug。

**做法**：
- 规则列表 + 逐条卡片编辑：unit 下拉、conditions 分组折叠（字符串/布尔/数值区间）、价格字段按 unit 动态显示（`token` 显示 4 个 token 价；其它显示 `price`）。
- 顶层：`extra_input_image_price` / `free_input_images` / `duration_source`。
- 保留「高级：直接编辑 JSON」折叠区做逃生舱，双向同步。
- 校验复用现有 `validateMediaPricing`（164 行规则一条不减）。
- 计费验证：`formMediaPricingToAPI` 输出与手写 JSON 逐字节等价；跑后端计费单测。

**验收**：可视化配置出的规则能存能读；与等价 JSON 的结算金额逐分相同；非法组合（token 配 price、非 second 配 duration_source=total）被拦。

## 7. 全站查询性能（主线，我自己主力做）

**现状**：monitor 渠道状态页、以及用户说的「所有页面」查询都慢。

**做法**：
- **先量后改**：对每个慢接口取真实生产数据跑 `EXPLAIN (ANALYZE, BUFFERS)`，建基线表（接口 → 耗时 → 计划 → 瓶颈）。
- 归类：缺索引 / 索引没被选中 / N+1 / 全表扫 / 现算聚合 / 无缓存。
- 逐类修，每修一项用同一查询复测并留前后数字。
- **不用代理信号**：不拿「加了索引」代替「查询变快」，不拿冷热缓存差当优化。

**重点对象（按用户点名排序）**：monitor 渠道状态（V1/V2/V3 三套 board 查询）、users 列表、groups 列表、keys 用量、usage 统计、dashboard。

**验收**：一张前后对照表，每行是真实接口的真实耗时；慢于 500ms 的接口要给出原因分类（可接受/已修/需改架构）。

## 分工

- 我自己（主力）：第 4、7 项（性能诊断与优化）、第 6 项的可视化编辑器核心与计费正确性、整体验收。
- SubAgent（kedaya-CCMAX / Claude-Opus-5-5 / max thinking）：第 1 项页面合并、第 2 项删除、第 5 项 Key 拆分的前端实现、以及性能项里的机械重复工作（如批量跑 EXPLAIN 采集）。

## 版本与部署

`0.2.20-GloryCode-0.1.1`。全部完成后：本地 Docker 验收 → 完整回归 + typecheck → 打 tag → 蓝绿部署生产（沿用 0.1.0 那套验证过的流程）。
