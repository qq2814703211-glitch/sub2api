# V3 监控独立于 V1/V2 运行

V3 目前只是 `channel_monitor_mode` 的一个枚举值，探针执行受全局 `channel_monitor_enabled` 控制、探针身份来自 V1 的 `channel_monitors` 表、分组名单来自 V2 的 `channel_monitor_v2_config`。这导致三个独立产品被一根开关串在一起：关 V1 的探针会顺带改 V3 的行为，改 V2 的分组名单会顺带改 V3 展示哪些分组，而 V3 自己没有配置面。

决定：V3 拥有独立的配置表 `channel_monitor_v3_config`（单行 `id=1`）、独立的探针执行器，只受自己的 `enabled` 控制。V1、V2 保持原样，互不影响。首次初始化时把当前 V1/V2 实际生效的探针条目与分组配置复制一份进 V3 作为初始值。

V3 的主动探针按分组配置，不再读取统一探针。升级时，迁移 922 把原统一探针（包括加密后的密钥）复制给此前继承它的分组，保留已有独立探针，然后清空统一配置。此后修改一组不会影响其他组；只监控真实流量的分组不必配置探针。

管理客户端保存时使用 `groups[].probe`，轮换密钥使用 `group_api_keys`（键为分组 ID）；不传新密钥会保留该组已保存的密钥。请移除请求顶层的 `probe` 和 `api_key`。仍提交统一探针或为启用主动探测的分组漏填地址、主模型时，接口返回 HTTP 400 并说明原因，不会覆盖原配置。

配置界面的落点后来调整过：V3 的配置**不是**独立的侧边栏页面，而是 `/admin/channels/monitor` 页面上的第三个页签（V1 / V2 / V3 并排），侧边栏不再有单独的一级入口。旧路径 `/admin/channel-monitor-v3` 保留为重定向（`→ /admin/channels/monitor?tab=v3`），老书签仍可用。这只改变「设置界面在哪个页面」，配置表、探针执行器与 V3 自己的开关完全不变。

对应的两处前端约束（改动这类页面时必须守住）：
- 该页签在 V1/V2 模式、甚至 `channel_monitor_enabled=false` 时都必须可见可点；V3 的四处管理接口（`/admin/channel-monitor-v3/{config,groups,probe}`）在 `registerChannelMonitorV3Routes` 里刻意没有挂 `channel_monitor_enabled` / `channel_monitor_mode` 的任何 gate。
- 因此管理端「渠道监控」菜单项不能再用 `channel_monitor_enabled` 的 feature flag 过滤——否则总开关关掉时会连带藏掉 V3 配置入口。它与「渠道定价」同级，不做 flag 过滤。

代价：`channel_monitor_mode = v3` 这个枚举值仍然存在（避免破坏已有 API 消费者与迁移），但不再作为 V3 的运行门控 —— 一个值存在却不生效，是这里最容易误解的地方。
