# V3 监控独立于 V1/V2 运行

V3 目前只是 `channel_monitor_mode` 的一个枚举值，探针执行受全局 `channel_monitor_enabled` 控制、探针身份来自 V1 的 `channel_monitors` 表、分组名单来自 V2 的 `channel_monitor_v2_config`。这导致三个独立产品被一根开关串在一起：关 V1 的探针会顺带改 V3 的行为，改 V2 的分组名单会顺带改 V3 展示哪些分组，而 V3 自己没有配置面。

决定：V3 拥有独立的配置表 `channel_monitor_v3_config`（单行 `id=1`）、独立的探针执行器，只受自己的 `enabled` 控制。V1、V2 保持原样，互不影响。首次初始化时把当前 V1/V2 实际生效的探针条目与分组配置复制一份进 V3 作为初始值。

配置界面的落点后来调整过：V3 的配置**不是**独立的侧边栏页面，而是 `/admin/channels/monitor` 页面上的第三个页签（V1 / V2 / V3 并排），侧边栏不再有单独的一级入口。旧路径 `/admin/channel-monitor-v3` 保留为重定向（`→ /admin/channels/monitor?tab=v3`），老书签仍可用。这只改变「设置界面在哪个页面」，配置表、探针执行器与 V3 自己的开关完全不变。

对应的两处前端约束（改动这类页面时必须守住）：
- 该页签在 V1/V2 模式、甚至 `channel_monitor_enabled=false` 时都必须可见可点；V3 的四处管理接口（`/admin/channel-monitor-v3/{config,groups,probe}`）在 `registerChannelMonitorV3Routes` 里刻意没有挂 `channel_monitor_enabled` / `channel_monitor_mode` 的任何 gate。
- 因此管理端「渠道监控」菜单项不能再用 `channel_monitor_enabled` 的 feature flag 过滤——否则总开关关掉时会连带藏掉 V3 配置入口。它与「渠道定价」同级，不做 flag 过滤。

代价：`channel_monitor_mode = v3` 这个枚举值仍然存在（避免破坏已有 API 消费者与迁移），但不再作为 V3 的运行门控 —— 一个值存在却不生效，是这里最容易误解的地方。
