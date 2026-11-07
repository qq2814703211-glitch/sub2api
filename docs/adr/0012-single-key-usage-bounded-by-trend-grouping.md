# ADR 0012 — 单 key 用量查询按行数而非索引优化

**状态**：已接受（部分实施）
**背景**：用户反馈「kiss 的 key 用量查询很快，我们的很慢」，并要求抄回单 key 用量统计。

## 决策

单 key 用量面板改为「一个有界 summary 聚合 + 一条趋势查询」，并且趋势查询的分组表达式从 `TO_CHAR(created_at, ...)` 换成 `date_trunc(..., created_at)`。

## 依据（生产实测，不是推测）

在 `203.0.113.10` 生产库（`usage_logs` 1755 万行 / heap 6.9 GB / 含索引 22 GB）上：

| 场景 | 耗时 |
|---|---|
| 旧查询，`TO_CHAR` 分组，单 key 30 天（567 万行） | **22.5 s** |
| 改 `date_trunc('day')` 分组，同一窗口同一 key | **4.3 s** |
| 再叠加 `work_mem=256MB` | 3.6 s |
| `enable_seqscan=off` 强制走索引 | 仍选 `idx_usage_logs_user_id`，3.6 s |

关键事实：30 天内单 key 的行数分布是 170 个 key、平均 9.2 万、p99 **282 万**、最大 **567 万**。所以「慢」不是索引缺失（`idx_usage_logs_api_key_created_at` 存在，547 MB），也不是数据库参数没调（`shared_buffers` 已是 6 GB、`effective_cache_size` 16 GB）。

真正的机制是两件事叠加：
1. **回表量**。聚合要读 `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `total_cost` / `actual_cost` 六个非索引列，`api_key_id` 索引拿到行号之后仍要逐行回表，567 万行的回表代价让规划器宁可走 `user_id` 索引再过滤。
2. **分组键不可索引**。`TO_CHAR` 产出字符串，必须先对每一行做格式化才能排序/归并；`date_trunc` 的键保持时间戳原类型，可比较。这一项单独贡献了 22.5 s → 4.3 s。

## 为什么不在 SQL 里去掉 `user_id`

现有查询同时带 `user_id` 和 `api_key_id`。`user_id` 在多 key 用户上反而把可选索引引向 `idx_usage_logs_user_id`。但删掉它会改变查询语义（防御性地确保 key 与 user 归属一致），而归属校验已经在 handler 层做过一次。此项保留，不在此 ADR 里改。

## 出参兼容

`TO_CHAR` 仍然留在 SELECT 列表里，只是分组键换成 `date_trunc`。已用 `EXCEPT` 双向核对同一窗口两种写法的输出：31 行、0 差异，字段值与顺序完全一致。

`week` / `month` 两种粒度**不动**。它们的现有桶是 ISO 周（`IYYY-IW`）和日历月字符串，而 `date_trunc('week')` 是周一起始，与现有周边界处理没有逐字核对过，不值得为了没人报告过慢的路径冒静默错位的风险。

## 未实施、需要单独的运维决定

以下三项都能再提速，但都有代价，不夹在这个发布里偷偷做：

- **覆盖索引** `(api_key_id, created_at) INCLUDE (input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost)`，让聚合走 Index Only Scan，彻底消掉回表。代价是数百 MB 磁盘，且会拖慢写入。
- **生产 `ANALYZE usage_logs`**：`SHARE UPDATE EXCLUSIVE`，不阻塞读写。表的 `last_analyze` 已明显陈旧，而日均新增 40–56 万行，规划器缺新鲜统计时更容易选错索引。
- **`work_mem` 从 4 MB 提到 64–256 MB**：只影响容器 `command` 段，需重启 postgres。排序溢写在 567 万行场景下会写 71 MB × 4 workers 到磁盘。

## 结论

代码层已经把这 567 万行场景从 22.5 s 压到 4.3 s，出参不变。要再往下走必须动数据库结构或参数，那是运维决定，不是代码提交能顺手带上的东西。
