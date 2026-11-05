# 单 Key 并发与排队

在用户「API 密钥」创建/编辑表单设置「Key 并发上限」；管理员在用户密钥弹窗中也可设置。非负整数，0 保留旧行为、不增加 Key 限流。用户和账号的既有并发限制独立生效，用户并发 0 仍不限。修改即时生效于下一次准入和队列轮询，旧 auth cache 版本不接受。

```yaml
gateway:
  api_key_queue:
    max_waiting: 0
    timeout_seconds: 30
```

全局排队策略由启动配置读取，修改 YAML 后需重启；单 Key 限额通过配置 UI/API 立即生效。

`max_waiting: 0` 关闭等待但保留正数 Key 限额；设为正数开启每 Key 等待队列。截止时间固定，不因轮询延长。满队列/超时使用协议原有 429 格式；Redis/租约/配置变动故障以 503 拒绝。Key 限额为 0 时仍按原方式 best-effort 观测，不因 Redis 故障拒绝。

HTTP 在 Key 等待结束后才进入用户/账号抢槽，不写 SSE 心跳来提前提交错误状态。Responses WS 每轮重验并重新抢槽，空闲不占执行槽；连接 reader 在排队时保持读取以观察断连。Live 创建先排 Key，再原子转移普通 Key/用户/账号成员为联合 Live lease，SDP 重试只迁移账号成员，终态失败/结束回收。

Redis 使用 TIME、Lua、有序集合、精确 attempt ID 和关闭围栏。租约续期只更新仍有效的成员，独立 watchdog 在租约安全期限内取消上游。客户端断连后正在 drain 的工作仍保有容量，交由 forwarding owner 在上游停止后释放。启动关闭队列后不再交付迟到 grant。统计错误返回 unavailable，不显示假的等待 0。

仅支持现有单 Redis 实例/非 Cluster 多实例部署；跨 Key Lua 使用现有命名布局，没有声称 Redis Cluster 兼容。滚动升级时先排空旧版本流量，再启用正数限额，旧二进制不具备限流能力。队列没有独立扫描/采集 worker，只有实际请求的续租/等待任务。

## 停用和卸载

1. 将所有 Key 的 concurrency_limit 调为 0，即停止额外限制与排队；将 max_waiting 设为 0 可单独停用等待。已存在的请求由其 owner 完成回收。
2. 停服前调用 StopAPIKeyQueue（主服务 SIGTERM/SIGINT 已接入），取消等待并围栏迟到授予，然后进行既有优雅关闭。
3. 下线本版本后可按部署命名空间清理 `concurrency:wait:api_key:*`、`concurrency:wait_closed:api_key:*`、`concurrency:live_transfer_closed:api_key:*`；不要在仍运行时清理活跃租约。所有本功能 Redis 残留有 TTL。
4. 908 是加法迁移。默认保留列便于回退；完全卸载时确认没有应用引用后再删除 api_keys.concurrency_limit（连同非负约束），不删除已应用迁移记录或篡改历史 checksum。
