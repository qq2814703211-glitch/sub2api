# ADR 0012 — 单 key 用量查询按行数而非索引优化

**状态**：已接受（部分实施）
**背景**：用户反馈「kiss 的 key 用量查询很快，我们的很慢」，并要求抄回单 key 用量统计。

## 决策

单 key 用量面板改为「一个有界 summary 聚合 + 一条趋势查询」，并且趋势查询的分组表达式从 `TO_CHAR(created_at, ...)` 换成 `date_trunc(..., created_at)`。

## 依据（生产实测，不是推测）

在 `203.0.113.10` 生产库（`usage_logs` 1755 万行 / heap 6.9 GB / 含索引 22 GB）上：

| 场景 | 耗时 |
|---|---|
| 旧查询，`TO_CHAR` 分组，单 key 30 天（567 万行），冷缓存 | **22.5 s** |
| 改 `date_trunc('day')` 分组，同一窗口同一 key | **4.3 s** |
| 再叠加 `work_mem=256MB` | 3.6 s |
| `enable_seqscan=off` 强制走索引 | 仍选 `idx_usage_logs_user_id`，3.6 s |

### 更正：22.5 s → 5.7 s 不能归因于 `date_trunc`

后续补测发现上表首行与次行不可直接相减。`ANALYZE usage_logs` 之后重跑**原始那条
`TO_CHAR` 查询、不改一个字符**，耗时 **5.7 s**，执行计划也完全相同
（`Parallel Index Scan using idx_usage_logs_user_id`），`BUFFERS` 显示
`shared hit=575043`（≈4.6 GB，全落在 `shared_buffers=6GB` 内）。

也就是说 22.5 s 是**磁盘冷读**，5.7 s 是**热缓存**。把差的 17 秒记在分组表达式头上
是错的。`date_trunc` 的真实收益只有本地 100 万行上可复现的那部分：34.5 ms → 20.8 ms
（约 40%）。这个量级不足以解释线上体感，必须如实说明。

关键事实：30 天内单 key 的行数分布是 170 个 key、平均 9.2 万、p99 **282 万**、最大 **567 万**。所以「慢」不是索引缺失（`idx_usage_logs_api_key_created_at` 存在，547 MB），也不是数据库参数没调（`shared_buffers` 已是 6 GB、`effective_cache_size` 16 GB）。

真正的机制是两件事叠加：
1. **回表量**。聚合要读 `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `total_cost` / `actual_cost` 六个非索引列，`api_key_id` 索引拿到行号之后仍要逐行回表，567 万行的回表代价让规划器宁可走 `user_id` 索引再过滤。
2. **分组键不可索引**。`TO_CHAR` 产出字符串，必须先对每一行做格式化才能排序/归并；`date_trunc` 的键保持时间戳原类型，可比较。

## 已实施：去掉单 key 趋势查询里的 `user_id`

原版本 ADR 写的「此项保留，不在此 ADR 里改」已被推翻，因为拿到了可归因的实测数字：

建了覆盖索引之后，同一 567 万行 key、同一 30 天窗口：

| 谓词 | 计划 | 耗时 |
|---|---|---|
| `api_key_id` + `user_id` | `Index Scan using idx_usage_logs_user_id` | **8.9 s** |
| 仅 `api_key_id` | `Index Only Scan using idx_usage_logs_apikey_created_cover`（回表 48,191 次 = 0.8%） | **4.9 s** |

`user_id` 在这里是纯冗余：`api_key_id` 唯一确定归属，且归属校验已由 handler 用
`apiKeyRepo` 单独做过。留着它唯一的后果是把规划器引向单列索引。已改为只传 `api_key_id`。

对应测试 `TestGetMyAPIKeyDailyUsageAggregatesByDayForOwnedKey` 原先断言
`usageRepo.userID == 42`，现在断言 `== 0` 并注明这是契约而非疏漏。

## 出参兼容

`TO_CHAR` 仍然留在 SELECT 列表里，只是分组键换成 `date_trunc`。已用 `EXCEPT` 双向核对同一窗口两种写法的输出：31 行、0 差异，字段值与顺序完全一致。

`week` / `month` 两种粒度**不动**。它们的现有桶是 ISO 周（`IYYY-IW`）和日历月字符串，而 `date_trunc('week')` 是周一起始，与现有周边界处理没有逐字核对过，不值得为了没人报告过慢的路径冒静默错位的风险。

## 已实施（原先列为「需要单独的运维决定」）

- **覆盖索引**：`idx_usage_logs_apikey_created_cover`，
  `(api_key_id, created_at) INCLUDE (input_tokens, output_tokens, cache_creation_tokens,
  cache_read_tokens, total_cost, actual_cost)`。已用 `CREATE INDEX CONCURRENTLY` 建好，
  实测 **1131 MB、`valid=true`**。聚合走 `Index Only Scan`，567 万行回表仅 48,191 次（0.8%）。
  建索引前生产盘可用 33 GB（已用 92%），索引占用在此余量内，未清理 Docker 任何内容。
- **生产 `ANALYZE usage_logs`**：已完成，`last_analyze` 从 `2026-10-21` 刷新到 `00:22:03`。

## 仍未实施、需要单独的运维决定

- **`work_mem` 从 4 MB 提到 64–256 MB**：只影响容器 `command` 段，需重启 postgres。
  实测 `SET work_mem='256MB'` 后扫描从 2142 ms 降到 1733 ms，但并行 worker 各自受限，
  溢写文件仍在（139 MB），只提速约 20%。要真正消除需连带调 `max_parallel_workers`，
  属生产配置变更。

## 结论

代码层（`date_trunc` 分组 + 去掉冗余 `user_id`）加数据库层（覆盖索引 + `ANALYZE`）
都已落地。同一 567 万行 key、同一 30 天窗口的可复现实测：**走 `user_id` 索引 8.9 s，
走覆盖索引 4.9 s**，出参格式不变。

另外必须说明：最初记录的「22.5 s → 4.3 s」是把冷缓存和热缓存相减，不是改动收益。
按热缓存口径，代码与索引合计把这条查询压到 4.9 s；剩下的大头是 `work_mem` 溢写与
`shared_buffers=6GB` 对 22 GB 表的冷读，属生产参数范畴，需单独决定。
