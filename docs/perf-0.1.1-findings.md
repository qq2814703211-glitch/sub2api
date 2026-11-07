# 性能排查发现（lane/011-perf）

配套文档：`docs/perf-0.1.1-baseline.md`（基线与前后数字）。
所有数字来自本机真实执行，环境见基线文档开头。

---

## 一、系统性发现

### 1. keys 页面慢的真正对象是「极少数热点 key」，不是全部 key
数据分布极度不均，这是所有 keys 相关性能问题的根源：

| key 规模 | 数量 | 行数 | 占全表 |
|---|---|---|---|
| ≥100 万行 | 10 | 12,285,000 | 70% |
| 10 万–30 万 | 10 | 1,755,000 | 10% |
| 1 万–10 万 | 30 | 2,632,500 | 15% |
| <1 万 | 122 | 877,500 | 5% |

最大单 key 存储 122.8 万行 / 274 MB。用户打开 keys 页面时命中的大概率是热点 key，
所以「平均 key 很快」和「用户觉得慢」可以同时成立。

**含义**：任何以"平均查询耗时"为指标的优化都会失真。必须按热点 key 测。

### 2. 「扫很多行只输出几十行」是本仓库最普遍的慢查询形态

三个典型：

| 查询 | 扫描行数 | 输出行数 | 放大比 |
|---|---|---|---|
| 单 key 30 天日趋势 | 513,364 | 31 | 16,560× |
| 单 key 30 天汇总 | ~502,566 | 1 | 502,566× |
| 批量 172 key | 7,177,760 (并行扫全表 41%) | 172 | 41,731× |

这种形态下索引只能减少"读多少页"，不能减少"要处理多少行"。**只有预聚合能改变复杂度。**

### 3. PostgreSQL JIT 在本仓库默认配置下对聚合查询普遍净亏

`jit_above_cost=100000` 让这些查询全部触发 JIT，但它们的输出只有几十行，编译成本买
不到执行收益。最极端的一例：旧 rollup SQL `jit=on` 394.85 ms vs `jit=off` 84.31 ms
（**4.7×**），编译 **109 个函数**。

但**不能一刀切**：`global_30d` 关掉 JIT 反而慢 9.9%（952.24 → 1046.81 ms）。JIT 对
"输出量大、表达式复杂"的查询仍有益。

完整对照见基线文档 §2.3。

### 4. `usage_logs` 的 26 个索引有 21.9× 的写入放大，且成本高度线性

100 万行 A/B 对照（同结构表，索引定义与 `usage_logs` 逐一对应）：

```
0 索引    938 ms      5 索引   4,084 ms     10 索引   8,989 ms
15 索引 13,462 ms    20 索引 17,457 ms     26 索引  20,523 ms
```

**边际成本 ≈ 783 ms / 100 万行 / 索引**（≈0.78 µs/行），26 个索引时仍线性。与"带全
部索引灌 1755 万行 12m44s ≈ 22,900 行/秒"一致。

### 5. 生产手工建的索引不仅没进迁移，**定义本身还是错的**

`idx_usage_logs_apikey_created_cover` 在生产存在，但：
1. `grep -rn "apikey_created_cover" backend/` 返回空——不在任何迁移里，重建容器即丢失；
2. 更要紧的是它的定义 `INCLUDE (actual_cost)` **盖不住 keys 页的聚合查询**。

三态实测（同 key、同窗口、热态、三次取最优）：

| 索引状态 | 单 key 汇总 | 单 key 日趋势 | 大小 |
|---|---|---|---|
| 无覆盖索引 | 350.7 ms | 444.7 ms | — |
| `INCLUDE (actual_cost)`（生产现状） | **346.2 ms** | **440.7 ms**（规划器不选它） | 679 MB |
| `INCLUDE (读到的全部列)` | **55.7 ms** | **204.0 ms** | 1285 MB |

第二行与第一行差在噪声内——**生产这个索引对 keys 页面基本没起作用**。原因是聚合还要读
`input_tokens`/`output_tokens`/`cache_*`/`total_cost`，`actual_cost` 一个列盖不住，走它
仍要回堆，规划器宁可选 bitmap heap scan。它只在"只要 actual_cost"的查询上有用。

教训：**"把手工索引写进迁移"不能照抄原定义**，先验证它真的被业务查询选中。本轮迁移
918 因此写成宽 INCLUDE。

### 6. session 时区与硬编码时区不一致时，新旧路径会在每一天上分歧

这是本轮实测抓到的**真实一致性风险**，不是理论推演：

| session timezone | 新路径 vs 旧路径 逐日不一致 |
|---|---|
| `Asia/Shanghai`（生产 DSN 配置） | **0 / 31 天** |
| `UTC`（容器默认，配置漂移时） | **32 / 32 天全部不一致** |

原因：旧路径 `date_trunc('day', created_at)` 走 session 默认时区，新路径硬编码
`AT TIME ZONE 'Asia/Shanghai'`。两者必须一致才对得上。

这正是 `usage_key_daily_state.timezone_name` 存在的价值：读到不一致就整表重建，绝不
混着用。也说明**任何测量脚本必须显式 `SET timezone`**，否则量出来的"不一致"是脚本
bug 而不是数据 bug（本轮先踩了这个坑，误报了 2 行差异，追下去才发现是窗口字符串丢了
时区偏移）。

### 7. 日分桶口径错一次，全表 1/3 的数据就是错的

UTC 分桶 vs `Asia/Shanghai` 分桶在 **5,857,200 行（33.37%）** 上落入不同日期
（本地 00:00–08:00 的日志全部错到前一天）。这类错误不报错、不崩溃，只让金额对不上，
**事后极难发现**。

`usage_group_daily_rollups` 已用 `timezone_name` 列记录口径并在不一致时重建，本轮的
`usage_key_daily` 沿用同一模式。**这是仓库已有的正确做法，应推广到所有日粒度聚合表。**

### 8. 「运行期值做索引下界」是个已经在仓库里踩过的坑

`custom_group_usage_rollup_repo.go:84` 的注释记录过：`created_at` 下界由同一条 SQL 内
的子查询算出时，planner 既进不了 index cond 也估不准选择性，退化成全表扫
（**48,720 ms → 参数化后 34 ms**，1432×）。

本轮新写的读路径一律把窗口切点作为**参数**传入，不重犯。

### 8. monitor board 是串行多次往返，不是单条慢 SQL

- `getMatrix`（`channel_monitor_v2_repo.go:392`）：`loadCoverage` → `loadFacts` →
  `loadHistograms` → `loadIgnoredErrorCountsByMatrixKey`，**4 次串行**
- V3 的 `applyV3ProbeFallbackToMatrix`（`channel_monitor_v3_board_repo.go:74`）在其后
  再串行 `ListProbeSamples` + `GroupCacheRates`，**最多 6 次串行**

这是可合并的固定往返开销，属于结构问题而非 SQL 问题。**本轮未改**（见基线 §7.3：
本地聚合表为空，无法建立可信基线）。

---

## 二、修好了的（摘要，详见基线文档）

| 路径 | 前（热） | 后（热） | 倍数 |
|---|---|---|---|
| 单 key 30 天日趋势（热点 key 122.8 万行） | 439.58 ms | 12.38 ms | 35.5× |
| 单 key 30 天区间汇总 | 395.76 ms | 0.18 ms | 2199× |
| 批量 172 key today/total | 1115.85 ms | 49.90 ms | 22.4× |

对账：**15,652 个 (key, 日) 单元格 × 8 个字段，0 处不一致**；172 个 key 的窗口合计
金额与请求数全部相同。集成测试 4/4 通过。

---

## 三、建议但没做，及原因

### 1. 全局关 JIT —— 数据不支持，需要按查询处理

见发现 §3。全局关会让 `global_30d` 变慢。**没做**：这是影响所有查询的配置变更，且
仓库里没有"按查询 SET LOCAL"的既有机制，新引入模式需要更长回归。建议生产先
`ALTER DATABASE ... SET jit=off` 观察，或对特定查询在 repository 层加
`SET LOCAL jit=off`（风险：多一条语句 = 多一次往返，需要连接池层面确认）。

### 2. 删减 26 个索引 —— 缺生产统计，不敢删

`idx_scan` 在本地新库全为 0（新库没积累），**不能作为"索引没用"的证据**。逐个验证需要
生产 `pg_stat_user_indexes` 的真实数据。

虽然 §4 已量化边际成本（783 ms/百万行/索引），但删错一个索引会让某条查询退化为全表
扫，代价远大于保留。**本轮一个索引都没删**。

给后续的候选（仅从定义上看高度重叠，需生产 idx_scan 确认后再动）：
- `idx_usage_logs_api_key_id` 被 `idx_usage_logs_api_key_created_at` 前缀覆盖
- `idx_usage_logs_user_id` 被 `idx_usage_logs_user_created` 前缀覆盖
- `idx_usage_logs_account_id` / `group_id` / `subscription_id` / `model` 各自被对应的
  `(col, created_at)` 复合索引前缀覆盖

在 PostgreSQL 里，`(a)` 单列索引确实是 `(a, b)` 复合索引的冗余——但**只有**当查询永远
带 `b` 的等值/范围谓词、且 planner 不需要单列统计时才成立。这需要真实 workload 验证。

### 3. monitor 读路径合并往返 —— 无基线不动

见发现 §8。

### 4. kiss 式 11 维度通用表 `usage_dashboard_hourly_dimensions`

对 keys 页面不如专门的 `(api_key_id, day)` 表直接，本轮已用后者解决。若后续
dashboard 全局统计成为瓶颈，再按 kiss 的维度方案加。**有意不做**：先把一个场景做到
对账零误差，比同时铺两套聚合更安全。

### 5. 带过滤的 trend 查询（`GetUsageTrendWithFilters` 带 group/model 过滤）

`shouldUsePreaggregatedTrend` 仅在**无任何过滤**时才走 `usage_dashboard_*` 聚合表；
带过滤的 trend 仍然全扫 `usage_logs`。这是 keys 页面之外的**下一个大瓶颈**。

需要一张 `(group_id, model, day)` 形状的维度表。**没做**：独立工作量，且需要先确认
哪些过滤组合最常用（否则会造一张大部分列都空转的表）。

### 6. `work_mem` 调大 —— 方向不成立

用户已在生产实测 32/64/160/256 MB，溢写量恒为 139744 kB 不变，溢写发生在并行 worker
内、每 worker 各受限制。真因是要排序的行数，已由预聚表从根上消除。**本轮未改
`work_mem`**。

### 7. `usage_logs` 分区化

1761 万行 / 13 GB 尚未到必须分区的规模，但按当前增速长期会到。仓库里已有
`EnsureUsageLogsPartitions` 相关代码路径。**没做**：分区是重架构改动，且与
"检索键日汇总"无直接关系，本轮不该混进来。

---

## 四、给后续的提醒

1. **新增日粒度聚合表时，必须显式写时区名**，并记录到 state 表的 `timezone_name`。
   参照 `usage_group_daily_rollups` / `usage_key_daily`。
2. **新增 `_notx.sql` 迁移时，注释里不能出现分号**。`splitSQLStatements` 按 `;` 硬切，
   注释里的分号会被当成语句分隔符，报一个完全无法定位的
   `syntax error at or near "it"`。本轮踩过。
3. **`MAX(boolean)` 在 PostgreSQL 里不存在**。要写 `MAX(x::int) = 1` 或 `bool_or(x)`。
   本轮踩过。
4. **集成测试进程必须自己调 `timezone.Init`**：生产在 `internal/repository/ent.go:96`
   初始化，测试进程没有，`timezone.Name()` 会返回 `Local`，日桶按容器时区切。
   本轮踩过。
5. **`usage_key_daily` 是派生数据**。任何时候都能从 `usage_logs` 重建（4.5 秒 / 1755
   万行）。运维可以直接 TRUNCATE 或 DROP 而不丢任何计费事实。

---

## 五、回归状态

```
cd backend && go build ./...                          # 通过
go test -tags unit ./internal/repository/             # ok  12.616s
go test -tags unit ./internal/keyusage/               # ok   0.364s
go test -tags unit ./internal/service/                # 1 个既有失败（与本次改动无关）
go test -tags integration -run TestKeyDaily ...       # ok   5.106s（4/4 用例）
```

既有失败（**与本次改动无关，已确认在干净基线上同样失败**）：

- `TestProductionOpenAIPricingOverrideShortLongAndFast`
  — `open ../../../model-pricing-current.json: no such file or directory`。
  该文件不在仓库里（未随仓库分发），其他 lane（`sub2api-011-delcapture`）跑同一套
  测试也报同一个错。

既有 vet 失败（同样在干净基线上存在）：
`internal/service/api_key_route_runtime_test.go:88: undefined: apiKeyQueueCacheStub`。
