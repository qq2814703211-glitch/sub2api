# 账号运营与上游看板

## 入口和开启

- `/admin/ops-center`：运营概览；原 `/admin/ops` 系统监控保持原状。
- `/admin/account-ops`：模块热开关、历史间隔、系统 SMTP 收件人、加密机器人通道、账号余额/额度规则、独立回执。
- `/admin/upstream-monitor`：按币种余额、额度窗口与到期、余额历史、日下降估算、异常规则及确认。

默认停用，不发送额外的 `/v1/usage` 请求。开启模块后，保存一条余额/额度规则或开启历史看板采样，再选择账号“加入定时探测”。现有倍率探测的全局调度必须启用；读取间隔与账号加入开关复用既有接口，显示并保存原值，不改其默认值。手动“读取一次”复用同一个账号、密钥、代理、传输画像及 CAS。没有供应商登录、验证码、Key reveal 或远端充值。仅 API-key 自定义上游支持 `/v1/usage` 的公开响应契约；官方供应商 URL 不追加请求。OAuth 额度使用本地已有被动观测/额度读模型，不进行远端登录。

模块启用但只做看板时，不必配置通知通道。通知使用系统现有 SMTP，以及可单独配置的 custom HTTPS Webhook、Bark、企业微信、钉钉和飞书通道。机器人 URL/签名密钥需配置持久 TOTP 加密密钥后才能保存；GET 配置仅返回已配置标志。管理 API 遵守本地 full-admin 角色、现有 step-up 开关；scoped 管理 Key 未获得这些全局设置权限。

## 数据含义

余额和倍率是独立子快照。成功余额的 `received_at` 是 HTTP 响应采集时间，`fresh_until` 为两倍读取间隔；读取/采样/倍率更新/重试失败不替旧数据换时间。上游未提供更精确的余额观测时间，因此不声称这是供应商账本时间。失败保留旧值供诊断，但消费者只将成功且在有效期内的数据视为可用；陈旧、未来时间、缺少有效期或失败不当作零余额，也不制造恢复。额度缺少用量或窗口已重置而未重新观测时，保持 unknown/stale。余额单位来自响应，不将缺失单位默认成 USD；不同币种不相加、不折算。

采样历史使用原始生产端时间，唯一索引避免重复记入日汇总。同账号同单位的正向余额下降计为 `spend` 估算；充值、共享钱包、并发使用和采样缺口会影响估算。UI 将其标为“余额下降估算”，不是精确消耗或利润。日汇总边界为固定 UTC+08:00。

看板异常（低余额、额度剩余、到期、倍率变动）记录在 `upstream_monitor_alerts`，可以确认；余额/额度阈值的外部通知及恢复由账号运营 episode/outbox 独立管理，两类规则在 UI 有明确说明。

## 送达与故障边界

- 每个账号/指标持久保存阈值基线；首次低于阈值生成告警 episode，一直低于阈值不重复生成。恢复需有效健康观测持续至少 15 秒，生成相同 episode 的恢复事件。unknown 不结束 episode。规则或账号身份变化会使旧工作失效；暂停模块不伪造恢复。
- PostgreSQL `FOR UPDATE SKIP LOCKED` 原子 claim，租约两分钟；过期可接管，旧租约不能保存回执或覆盖新状态。跨实例配置修改通过事务 advisory lock 串行化。
- 每通道回执在下一通道发送前持久保存；已成功通道不随失败通道重发。每事件/通道最多三次尝试，失败重试间隔五分钟。更换通道/模板会产生新的通道身份。
- Webhook 附带稳定 `Idempotency-Key`；提供方未承诺去重时不能保证 exactly-once。SMTP 或机器人已经接收，但进程在落回执前崩溃，接管后仍可能重复发送。此不可原子化窗口如实保留，不以提前记成功来掩盖丢信。
- `sent` 表示 SMTP/HTTP 提供方接受，不是终端已读。官方机器人还检查响应业务码。投递错误只记通用状态，不把响应正文、URL 或签名密钥写入回执/报告。

## 热停用与生命周期

关闭模块即阻止新的余额读取、阈值扫描、通知投递及历史业务采样。余额请求前重新读取共享配置；发送每个通道前复查配置及租约。已在网络栈中的请求可能结束，SMTP 使用既有服务超时语义。停用时仍保留一个 15 秒设置观察循环以识别其他实例的热启用；它不查询账号、不 claim、不发外部请求；配置变更时会抑制未送达的禁用事件。原倍率调度属于已有能力，不因该模块停用而改变。应用 cleanup 调用 `AccountOpsService.Stop()`，取消循环并等待本模块工作结束；历史采样没有独立常驻 worker。

## 保留与整体清理

采样保留 14 天，倍率变动/看板异常 90 天，日汇总 120 天。阈值基线和不可变告警/恢复及通道回执保留到显式清理，避免删除活动 episode 造成重复告警。停用期间不自动做业务清理。

完整卸载步骤（本次实现没有在任何业务库执行这些删除）：

1. 在 UI 关闭模块，等待所有实例观察设置并完成已开始的网络请求，停止/升级应用以移除账号运营 providers/routes/UI。保留现有倍率 probe 的 caller、默认配置、账号凭据及 CAS。
2. 备份下列表和设置。删除 `settings.key='account_ops_notifications_v1'` 和 `settings.key LIKE 'account_ops_robot_slot:%'`。仅删除账号 `extra.upstream_billing_probe.balance` 子节点，保留倍率快照及其他 extra。
3. 删除本模块九张表：`account_ops_alerts`、`account_ops_threshold_monitors`、`account_ops_threshold_events`、`upstream_balance_samples`、`upstream_rate_changes`、`upstream_monitor_state`、`upstream_monitor_daily`、`upstream_monitor_alert_rules`、`upstream_monitor_alerts`（共九张表）。删除函数 `account_ops_snapshot_time(text)`；相关索引随表删除。没有新增触发器或全局邮件 worker。
4. 迁移使用唯一 `911_account_ops_notifications.sql` 的完整 filename/checksum。已应用后保留迁移文件与迁移记录；不要编辑已应用 checksum。若卸载后将来重装，需另一个加法迁移恢复表，不能只删除迁移记录强制重跑。

## 本地验证

测试入口：`backend/internal/accountopstest/flow_test.go`（`-tags=unit`，需隔离 `ACCOUNT_OPS_TEST_DSN`）、服务/路由定向 Go 测试、`AccountOpsView.spec.ts` 组件行为及路由测试。共享 runner 和独立 DB runner 的日志、命令、退出码及最终核对见 lane REPORT.md/result.json；生产供应商、真实邮件/机器人以及最终合并提交的浏览器验收由父级统一处理。

来源：只读修正版 `cdf13f1991c369dbc5a4585ad197c103f0772451`，沿用 `backend/internal/service/account_ops_notice.md` 对原始 LGPL-3.0 来源的 NOTICE。
