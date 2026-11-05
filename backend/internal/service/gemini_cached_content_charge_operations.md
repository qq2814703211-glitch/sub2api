# cachedContents 持久扣费恢复

913 只扩展既有 gemini_cached_contents。冻结意图是独立 GeminiCachedContentChargeIntent，复用 UsageBillingCommand 的 request_id / API Key / fingerprint 去重；不依赖媒体任务或余额预授权。token·小时、计价时刻、实际金额、倍率、最终 user/Key/group/account/subscription 同资源与续期目标一起保存。

## 运行与可见性

生产 provider 在 GatewayService 装配后启动恢复，启动时立即扫描，此后沿用一分钟 mutation recovery 周期；单次 50 条、单笔 15 秒、两分钟数据库租约。多实例只能由持有有效 token 的 worker 完成发布，租约失效后旧 worker 不能修改状态。账本和 usage 各自幂等；账本成功而 usage 失败时不发布，恢复只补缺失部分。

资源 GET/引用与 list 不公开 billing_ready=false 的内容；续期目标在结算完成时与 billing_ready 原子提交。删除仍过滤所有公开读取，但未完成账单保留并继续结算。原有 mutation lease 的未知上游结果继续走回滚恢复；它不会接管已经转为 charge_intent 的续期。

## 故障排查与停用

只读检查（不包含凭据/原始请求）：

```sql
SELECT id,public_id,user_id,api_key_id,group_id,account_id,deleted_at,
 charge_intent->'command'->>'RequestID' AS request_id,
 charge_attempts,charge_last_error,charge_next_attempt_at,charge_lease_until
FROM gemini_cached_contents WHERE charge_intent IS NOT NULL
ORDER BY charge_next_attempt_at,id;
```

失败保留完整意图，记录最后错误和尝试次数，一分钟后重试；不要清空 intent、人工置 ready 或换 request_id。重启自动接管到期租约，绝不按当前价格重算。

Stop 终止后续恢复工作，不删除待结算状态；已开始的单笔有界执行后退出。停用入口前先阻止新创建/续期，保持 worker 运行，直到上述查询为空，再停服务。若暂时不能结算，保留数据库字段和账本/usage 去重记录，恢复版本后继续；不得以停用清理为由丢账。

## 卸载

先停入口并排空待结算；确认没有 mutation/charge 运行后再删除本模块的 provider 接线和文件。确认所有实例都停止使用 913 字段后，可删除 pending_charge 索引及 billing_ready、charge_intent、charge_target_expire_time、charge_lease_token、charge_lease_until、charge_next_attempt_at、charge_attempts、charge_last_error、charge_completed_request_id 列。保留已应用的迁移文件/校验和及现有 usage_billing_dedup、archive、usage_logs；它们不是本模块独占数据。没有新增表或 trigger。
