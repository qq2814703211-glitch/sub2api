# 0.2.20-GloryCode-0.1.1

## 本次修复与新增

### 修复：admin/users 实时并发列看不到
上一版已实现 `current_concurrency` 与该用户的在途分组明细接口，但并发列被写在
`DEFAULT_HIDDEN_COLUMNS` 里，线上打开 `admin/users` 默认看不到，功能等于没做。

- 并发列改为默认可见。
- 新增反向列设置迁移（版本 4）：老用户 localStorage 里遗留的「隐藏」记录会被清除，
  升级后立即可见；之后用户在列设置里的手动选择仍然生效。
- 点数字弹出该用户此刻在途请求的**分组名单**（分组名 + 在途请求数），可刷新。

### 修复：渠道监控 V3 设置页归位
V3 的配置界面并入 `admin/channels/monitor`，与 V1/V2 并排。
侧边栏不再有独立的 `admin/channel-monitor-v3` 一级菜单。
旧路径保留重定向，旧书签不会 404。
V3 的表结构、探针执行器、`channel_monitor_mode` 门控保持独立，不依赖 V1/V2。

### 新增：AI 改写 Key 与生图 Key 拆开
`image-studio` 的 AI 改写原本复用生图 Key。现在有独立的一套配置：

- 独立的 Key 与 Base URL
- 协议可选：OpenAI 兼容 / Anthropic Messages / OpenAI Responses
- 模型可选
- 思考强度可选，按协议映射到对应参数

未单独配置改写 Key 时回落原有行为，并在界面提示。

### 改进：媒体多维定价可视化配置
`media_pricing` 原本是在 textarea 里手写 JSON。现在是可视化编辑器：

- 规则列表可增删排序，每条独立折叠
- 计量单位下拉；选 `token` 显示四个 token 价格字段，其它单位显示单一定价
- 条件按字符串 / 布尔三态 / 数值区间分组
- 顶层 `extra_input_image_price`、`free_input_images`、`duration_source`
- 保留 JSON 逃生舱，双向同步

校验规则沿用原有 `validateMediaPricing` 全量约束，不缩减。

### 移除：请求采集（request-captures）
整套功能移除：前端视图、路由、菜单、i18n，后端 handler / service / repository / routes，
中间件钩子，以及 `request_capture_tasks`、`request_capture_records` 两张表。

### 性能
见 `docs/perf-0.1.1-baseline.md`。

## 升级注意

- 迁移编号：917（删请求采集表）、918（性能索引）
- 无需手动干预，容器启动自动迁移
