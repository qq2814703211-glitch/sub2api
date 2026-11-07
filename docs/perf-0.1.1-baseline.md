# 全站查询性能：基线、修复与实测（lane/011-perf）

数据规模：本地 perf 库 `sub2api-perf-postgres`（`127.0.0.1:5434`），**17,550,000 行
`usage_logs`**，13 GB，与生产 17,554,936 行同量级。172 个 api_key、900 用户、25 分组、
60 账号、近 90 天且越近越密。分布模拟生产的不均：10 个 key 占 70% 流量，最大单 key
122.8 万行。

配置与生产一致：`shared_buffers=2GB`、`work_mem=32MB`、`effective_cache_size=6GB`、
`max_parallel_workers_per_gather=2`。

## 关于"冷/热"和时区，先说清楚

本机无 `pg_prewarm`。"冷"= 重启容器后首次执行；"热"= 同 sessions 立即复跑。下文一律
以**热态为准**（生产热点数据常驻缓存，热态更接近真实），并标注冷态。

**时区陷阱（本轮实测踩到并修正）**：容器里 psql 的 session 默认是 **UTC**，而生产的
DSN 钉的是 `timezone=Asia/Shanghai`（`config.DSNWithTimezone`）。新旧两条路径算日期的
方式不同——旧路径靠 session 默认（`date_trunc('day', created_at)`），新路径在 SQL 里
写死 `AT TIME ZONE 'Asia/Shanghai'`。**这两者一旦不一致，同一天的数据会算到不同日期。**
实测同一窗口、同一 key：

| session timezone | 新路径 vs 旧路径 逐日不一致 |
|---|---|
| `Asia/Shanghai`（生产配置） | **0 / 31 天** |
| `UTC`（容器默认，配置漂移时） | **32 / 32 天全部不一致** |

因此下文所有测量都显式 `SET timezone='Asia/Shanghai'`，与生产对齐。这也正是新代码
必须写死时区名、并用 `usage_key_daily_state.timezone_name` 校验的理由。

---

## 1. 结论先行

| 路径 | 修复前（热） | 修复后（热） | 倍数 |
|---|---|---|---|
| keys 页单 key 30 天日趋势（repository 端到端） | 151.5 ms | **2.0 ms** | 76× |
| keys 页批量 100 key 30 天合计（repository 端到端） | 737.1 ms | **9.4 ms** | 78× |
| 单 key 30 天区间汇总（仅靠宽覆盖索引，不开 rollup） | 350.7 ms | **55.7 ms** | 6.3× |

前两行是一个**真实 Go test 打到真实 perf 库**测出来的时延（见 §5），也就是用户开页面
时会经历的量级，而不是单条 `EXPLAIN` 的 `Execution Time`。第三行是回落路径的下限参考。

修复前的瓶颈分类：

| 现象 | 证据 |
|---|---|
| 扫 51.3 万行只为产出 31 个日桶 | `rows=513364` → `GroupAggregate rows=31`，放大 **16,560×** |
| 排序溢写 | `Sort Method: external merge  Disk: 27696kB` |
| 批量 172 key 无索引可救 | 172 key 30 天共 717 万行 = 全表 **40.9%**，任何索引都要遍历 350 万条索引项 |
| 生产同形状查询 | **4769 ms**，`Disk: 139760kB`，`Index Only Scan rows=5,711,294` |

---

## 2. 修复前基线

### 2.1 单 key 30 天日趋势（keys 页面趋势图）

```
GroupAggregate  (actual time=... rows=31 loops=1)
  ->  Sort  (rows=513364)
        Sort Method: external merge  Disk: 27696kB
        ->  Bitmap Heap Scan on usage_logs  (rows=513364)
              Heap Blocks: exact=206145
Execution Time: 203.4 ms   （无覆盖索引时 439.6 ms）
```

生产同形状 **4769 ms** / `Disk: 139760kB`。本地行数少一些（51 万 vs 571 万），但
**计划形状完全一致**，瓶颈性质相同。

### 2.2 批量 172 key

`Parallel Seq Scan on usage_logs` 读 7.18M 行，或建了覆盖索引后
`Parallel Index Only Scan rows=3588881 loops=2`，两种都 **~940–1624 ms**。占全表 41%
时索引不再是捷径——**这不是索引问题，是"要读的行数本身太多"**。

### 2.3 JIT：小结果集聚合上净亏，但不能一刀切

| 查询 | jit=on | jit=off | 结论 |
|---|---|---|---|
| 单 key 30 天汇总 | 64.6 ms | **54.1 ms** | 关好 −16% |
| 单 key 30 天日趋势 | 213.7 ms | **185.8 ms** | 关好 −13% |
| 批量 172 key | 1146.7 ms | **1127.6 ms** | 略好 |
| 全局 30 天统计 | **952.2 ms** | 1046.8 ms | **关掉更慢 +9.9%** |
| 50 用户 30 天 | 122.1 ms | **96.1 ms** | 关好 −21% |

生产 `jit_above_cost=100000` 让这些"输出只有几十行"的聚合全部触发 JIT，编译成本买不到
收益。最极端一例：某 rollup SQL `jit=on` **394.9 ms** vs `jit=off` **84.3 ms**（4.7×），
编译 **109 个函数**，而输出只有 172 行。

### 2.4 覆盖索引：生产的定义其实盖不住 keys 页查询

这一条是本轮修正的一个**既有认知错误**。生产上 `idx_usage_logs_apikey_created_cover`
的定义是 `INCLUDE (actual_cost)`。三态实测（同 key、同窗口、热态、三次取最优）：

| 索引状态 | 单 key 汇总 | 单 key 日趋势 | 索引大小 |
|---|---|---|---|
| 无覆盖索引 | 350.7 ms（Bitmap Heap Scan） | 444.7 ms（Bitmap Heap Scan + 溢写） | — |
| `INCLUDE (actual_cost)`（**生产现状**） | **346.2 ms** | **440.7 ms** | 679 MB |
| `INCLUDE (读到的全部列)`（本次迁移） | **55.7 ms** | **204.0 ms** | 1285 MB |

中间那行是关键：**生产那个索引对 keys 页查询几乎没有作用（346 vs 351 ms，在噪声内），
规划器根本没选它**——因为聚合要读 `input_tokens`/`output_tokens`/`cache_*`/`total_cost`，
`actual_cost` 一个列盖不住，走它反而要回堆。它只在"只要 actual_cost"的查询上有用。

所以"把生产手工索引写进迁移"如果照抄原定义，等于**固化一个对 keys 页面无效的索引**。
迁移 918 因此写的是宽 INCLUDE，实测把汇总从 350.7 → 55.7 ms。

代价：索引从 679 MB 涨到 1285 MB，写入约 +0.8 µs/行（见 §4）。

### 2.5 渠道监控状态页

如实说明：V1/V2/V3 的 board 读路径**不扫 `usage_logs`**，读的是
`channel_monitor_v2_metrics_1m` / `_rollup` / `_error_metrics_1m` /
`_latency_histograms_1m`（`channel_monitor_v2_repo.go:1249-1275` 按桶大小选表）。perf 库
里这些表**基本为空**，无法测出可信耗时，我不拿空表的 0.0x ms 冒充基线。

可确认的结构性问题（来自代码与计划形状，不是编的耗时）：

- `getMatrix`（`channel_monitor_v2_repo.go:392`）单次请求**串行 4 条聚合**：
  `loadCoverage` → `loadFacts` → `loadHistograms` → `loadIgnoredErrorCountsByMatrixKey`
- `applyV3ProbeFallbackToMatrix`（`channel_monitor_v3_board_repo.go:74`）之后**再串行 2 条**：
  `ListProbeSamples` + `GroupCacheRates`，V3 最多 **6 次串行往返**
- 索引形状本身没问题（都有 `bucket_start` 与 `(bucket_start, platform, group_id, model)`）

**处置：本轮不改 monitor 读路径**——没有可信基线就不动，避免"看起来快了"。

---

## 3. 修复：按 key 按本地日的预聚表 `usage_key_daily`

### 3.1 时区口径

写入侧**写死**时区名，不依赖 session：

```sql
(ul.created_at AT TIME ZONE $3::text)::date   -- $3 = 'Asia/Shanghai'
```

§0 的对照已经证明这不是洁癖：session tz 漂成 UTC 时，新旧路径在**全部 32 天**上分歧。
用 UTC 分桶则全表 **5,857,200 行（33.37%）** 会落到错误日期。

`usage_key_daily_state.timezone_name` 记录每批数据用的日历，读到不一致就整表重建，不混读。

### 3.2 表结构与规模

```sql
CREATE TABLE usage_key_daily (
    api_key_id BIGINT NOT NULL, day DATE NOT NULL,
    requests, input_tokens, output_tokens, cache_creation_tokens,
    cache_read_tokens BIGINT, total_cost/actual_cost NUMERIC(20,10),
    duration_sum, duration_count BIGINT, computed_at TIMESTAMPTZ,
    PRIMARY KEY (api_key_id, day)
);
```

迁移 `backend/migrations/918_usage_key_daily_rollups.sql`（可重复执行）。
实测 **15,652 行 / 3.3 MB**，对应 1755 万行原始数据（**缩减 1121×**）。

### 3.3 回填

全量 1755 万行 → **4.5 秒** / 15,652 行。`BackfillAll(ctx, chunkDays)` 按本地日 7 天
一片提交，中断后重跑从有数据的片继续，纯 `INSERT ... ON CONFLICT` 不锁表。

### 3.4 增量维护

挂在既有 `DashboardAggregationService` 调度上，独立 leader lock
（`dashboard:aggregation:key-usage-daily-backfill:leader`）、独立
`ScheduleRecurring("dashboard:key-usage-daily", ...)`。

**整日重算，不累加**：同日迟到日志若累加会重复计费，`ON CONFLICT DO UPDATE` 覆盖整日
才幂等。已由 `TestKeyDailyRollupIsIdempotentAndRecomputesWholeDay` 证明（同日补一条后
重算，`requests` 收敛到 2 而不是 3；再重算不变）。

**只发布已关闭的日**（`day < 本地今日`），今天仍在增长，由 `usage_logs` 现算补足。
稳态下每次同步只重算"昨天"，成本与历史深度无关。

### 3.5 读路径与「今天」

`keyDailyPointsSQL` / `keyDailyTotalsSQL` 把两段拼起来，切点**作为参数**传入：

- 已关闭日 → `usage_key_daily`
- `[本地今日零点, 查询终点)` → `usage_logs`（走覆盖索引）

切点不做成 SQL 子查询现算：现算的下界对 planner 是运行期值，进不了 index cond 也估不准
选择性。`custom_group_usage_rollup_repo.go:84` 记录过同类坑（48.7s vs 34ms）。

**延迟是显式的、且实际为零**：今天那段永远现算，读侧返回的是此刻真实值，不存在"最多
滞后一个同步间隔"的陈旧窗口。汇总表只承担历史。

**窗口必须对齐日界**：`onLocalDayBoundary` 检查两端都是本地零点，否则回落原 SQL。

### 3.6 开关与可删除性

- 开关：`usage_key_daily_state.enabled`
- 关掉后 `RollupUsable` 立即 false（秒级回落），`Sync` 空转，无需重启
- 删除：`DROP TABLE usage_key_daily, usage_key_daily_state` + 移除调度；`usage_logs`
  完全不受影响。**纯派生数据，任何时刻可从 `usage_logs` 重建。**

`RollupUsable` 在下列任一情况返回 false，读侧一律回落（宁可慢不给错数）：能力未接线、
开关关闭、表为空（迁移刚跑未回填）、`state` 时区与当前配置不一致。

---

## 4. 索引写入成本（A/B 对照）

同一批 100 万行灌进同结构表，索引定义与 `usage_logs` 逐一对应：

| 索引数 | INSERT 100 万行 | 相对裸表 |
|---|---|---|
| 0 | 938 ms | 1.0× |
| 5 | 4,084 ms | 4.4× |
| 10 | 8,989 ms | 9.6× |
| 15 | 13,462 ms | 14.4× |
| 20 | 17,457 ms | 18.6× |
| 26 | 20,523 ms | **21.9×** |

**边际成本 ≈ 783 ms / 100 万行 / 索引**（≈0.78 µs/行），高度线性。与"带全部索引灌
1755 万行 12m44s ≈ 22,900 行/秒"互相印证。

**推论**：本次对 `usage_logs` 新增的写入成本只来自迁移 918 那一个宽覆盖索引，约
**+0.8 µs/行**；换来的是回落路径 350.7 → 55.7 ms，以及主路径（rollup）完全不再扫
`usage_logs` 的历史部分。`usage_key_daily` 自己的索引不挂在 `usage_logs` 上。

---

## 5. 对账（计费相关，逐字段）

**Go 层端到端**（`internal/repository/key_usage_daily_perf_integration_test.go`，真实 perf
库，`-tags integration`）：

```
PERF_DATABASE_URL='postgres://...' go test -tags integration -run TestPerfKeysPage -v ./internal/repository/

rollup usable = true
hottest keys (by row count): [1000003 1000010 1000002 1000005 1000006]
FAST/DailyPoints single hot key     key=1000003 days=31  fastest=1.99ms  median=2.27ms
FAST/TotalsForKeys 100 keys         keys=100 returned=100 fastest=9.43ms median=9.56ms
BEFORE/DailyPoints raw 30d          key=1000003 days=31  fastest=151.45ms median=153.37ms
BEFORE/TotalsForKeys 100 keys raw   keys=100 returned=100 fastest=737.11ms median=739.61ms
PASS
ok  .../internal/repository  7.602s
```

这是**用户实际会感受到的**数字（含 repository 层的扫描与拼接），不是单条 SQL 的
`Execution Time`：

| 场景 | 修复前 | 修复后 | 倍数 |
|---|---|---|---|
| 单 key 30 天日趋势 | 151.45 ms | **1.99 ms** | 76× |
| 100 个 key 30 天合计 | 737.11 ms | **9.43 ms** | 78× |

**等价性**（`TestPerfKeysPageEquivalence`，5 个热点 key 各 31 天）：

```
key_1000001..1000005   days=31   all fields equal   （requests / 4 组 tokens / total_cost / actual_cost，金额 1e-9）
```

**SQL 层逐日逐字段**（`docs/perf-sql/fastpath_e2e.sh`，输出 `out/fastpath_e2e.txt`）：

```
=== result equality: fast path vs BEFORE, row by row ===
31|31|0        ← fast_rows | before_rows | mismatches
(0 rows)       ← 不一致明细为空
```

**全表全日期**（`docs/perf-sql/reconcile.sh`，输出 `out/reconcile.txt`）：

```
raw_rows | pre_rows | mismatches
    15652 |    15652 |          0
```

8 个字段（多一个 `duration_sum`）逐 (key, 日) 单元格比对，`FULL OUTER JOIN` 保证不多行
不漏行。**0 处不一致。** 172 个 key 的窗口合计金额与请求数同样全部相同。

**集成测试**（`key_usage_daily_rollup_integration_test.go`，**4/4 通过**）：

1. `TestKeyDailyRollupMatchesRawAcrossDayBoundary` — 本地 00:30 与 23:30 两条日志必须落在
   **同一个**本地日（UTC 分桶会把它们分开），并逐字段对账
2. `TestKeyDailyRollupIsIdempotentAndRecomputesWholeDay` — 迟到日志后整日重算收敛，再算不变
3. `TestKeyDailyTotalsForKeysMatchesRawForManyKeys` — 批量 5 key 合计 vs 直扫
4. `TestKeyDailyRollupUsability` — 关掉开关后读侧必须回落

这组测试在开发中**真的抓到三个真实缺陷**：`MAX(boolean)` 在 PostgreSQL 不存在；测试进程
未调 `timezone.Init` 导致日界按 `Local` 切；以及迁移 918 的注释里含分号被语句切分器
当成语句边界（在新库上直接迁移失败）。都不是读代码能发现的。

---

## 6. 改动清单

**迁移**

| 文件 | 内容 |
|---|---|
| `backend/migrations/918_usage_key_daily_rollups.sql` | `usage_key_daily` + `usage_key_daily_state` + 索引，可重复执行 |
| `backend/migrations/918_usage_log_apikey_created_cover_notx.sql` | 宽覆盖索引 `CONCURRENTLY IF NOT EXISTS`，把生产手工索引固化为更宽的正式定义 |

**代码**

| 文件 | 内容 |
|---|---|
| `internal/service/usage_key_daily_rollup.go`（新） | `UsageKeyDailyRollupRepository` / `UsageKeyDailyReader` 接口、`UsageKeyDailyPoint` |
| `internal/repository/key_usage_daily_rollup_repo.go`（新） | `Sync`（增量）、`BackfillAll`（分批）、状态水位、唯一的日桶口径 SQL |
| `internal/repository/key_usage_daily_read_repo.go`（新） | `RollupUsable` / `DailyPoints` / `TotalsForKeys`，已关闭日 + 今天 tail |
| `internal/service/usage_service.go` | 三条查询加预聚快路径；`keyDailyReader`、`onLocalDayBoundary` |
| `internal/service/dashboard_aggregation_service.go` | 启动同步 + 周期调度 + 独立 leader lock |
| `internal/service/key_usage_runtime.go` | `ProvideUsageService` 注入读侧 |
| `internal/repository/wire.go`、`cmd/server/wire_gen.go` | 接线（`wire` 重新生成） |
| `internal/service/custom_group_usage_rollup.go` | 导出 `GroupUsageDateFormat` |

**测量脚本**（`docs/perf-sql/`，原始输出在 `out/`）：`bench.sh`、`queries.sh`、`run.sh`、
`jit_ab.sh`、`cover_index_ab.sh`、`fastpath_e2e.sh`、`reconcile.sh`、`verify_rollup.sh`、
`ab_write_cost.sh`、`ab_write_stage.sql`、`proto_key_daily.sql`、`backfill_key_daily.sql`、
`run_rollup.sh`

---

## 7. 建议但没有做的改动，及原因

1. **全局关 JIT** — §2.3 数据：`global_30d` 关掉反而慢 9.9%。正确做法是按查询关，需逐
   条验证。**没做**：影响所有查询的配置变更，且仓库没有"按查询 SET LOCAL"的既有机制，
   新引入模式要更长回归。建议先在生产 `ALTER DATABASE ... SET jit=off` 观察。
2. **删减 26 个索引** — `idx_scan` 在新库全为 0（无统计意义），逐个验证需生产
   `pg_stat_user_indexes`。**一个都没删**：删错导致某查询退化全表扫，代价远大于保留。
   §4 已把边际成本量化为 783 ms/百万行/索引，供后续决策。
3. **monitor 读路径合并往返** — §2.5，本地聚合表为空，无基线不改。
4. **kiss 式 11 维度通用表** — 对 keys 页面不如 `(api_key_id, day)` 直接，本轮已解决。
   若 dashboard 全局统计成为瓶颈再加。
5. **带过滤的 trend（`GetUsageTrendWithFilters` 带 group/model）** —
   `shouldUsePreaggregatedTrend` 只在**无任何过滤**时走 `usage_dashboard_*`；带过滤仍全扫。
   这是 keys 页面之外的**下一个大瓶颈**，需要 `(group_id, model, day)` 维度表，独立工作量。
6. **`work_mem` 调大** — 用户已实测 32/64/160/256 MB 溢写量恒为 139744 kB 不变。真因是
   行数，已由预聚表从根上消除。**本轮未改 `work_mem`。**
7. **`usage_logs` 分区化** — 1761 万行 / 13 GB 未到必须分区，但会到。重架构改动，本轮
   不混进来。

---

## 8. 回归

```
cd backend && go build ./...                              # 通过
go test -tags unit ./internal/repository/                 # ok
go test -tags unit ./internal/keyusage/                   # ok
go test -tags integration -run TestKeyDaily ...           # ok  5.106s（4/4）
go test -tags unit ./internal/service/                    # 1 个既有失败（与改动无关）
```

既有失败（干净基线上同样失败，已在 `git stash` 后复现）：
`TestProductionOpenAIPricingOverrideShortLongAndFast` — 缺
`model-pricing-current.json`（未随仓库分发）。

既有 vet 失败（同样在干净基线存在）：
`internal/service/api_key_route_runtime_test.go:88: undefined: apiKeyQueueCacheStub`。
