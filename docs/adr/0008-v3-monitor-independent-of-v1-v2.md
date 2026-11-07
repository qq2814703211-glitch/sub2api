# V3 监控独立于 V1/V2 运行

V3 目前只是 `channel_monitor_mode` 的一个枚举值，探针执行受全局 `channel_monitor_enabled` 控制、探针身份来自 V1 的 `channel_monitors` 表、分组名单来自 V2 的 `channel_monitor_v2_config`。这导致三个独立产品被一根开关串在一起：关 V1 的探针会顺带改 V3 的行为，改 V2 的分组名单会顺带改 V3 展示哪些分组，而 V3 自己没有配置面。

决定：V3 拥有独立的配置表 `channel_monitor_v3_config`（单行 `id=1`）、独立的管理设置页 `/admin/channel-monitor-v3`、独立的探针执行器，只受自己的 `enabled` 控制。V1、V2 保持原样，互不影响。首次初始化时把当前 V1/V2 实际生效的探针条目与分组配置复制一份进 V3 作为初始值。

代价：`channel_monitor_mode = v3` 这个枚举值仍然存在（避免破坏已有 API 消费者与迁移），但不再作为 V3 的运行门控 —— 一个值存在却不生效，是这里最容易误解的地方。
