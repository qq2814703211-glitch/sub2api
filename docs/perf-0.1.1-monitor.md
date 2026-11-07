# 性能补充：monitor V3 缓存率（本人实测）

lane/011-perf 的基线文档如实说明「perf 库的 monitor 相关表基本为空，无法测出
可信耗时，不拿空表的 0.0x ms 冒充基线」，据此没有动 monitor 读路径。

但生产上这些表有真实数据，所以这一项由我在**生产**上补测并修复。

## 生产真实数据量

```
channel_monitor_v2_latency_histograms_1m      1,949,629 行   753 MB
channel_monitor_v2_latency_histograms_rollup  1,379,800 行   484 MB
channel_monitor_v2_user_metrics_rollup          163,577 行    79 MB
channel_monitor_v2_metrics_1m                   149,184 行   101 MB
```

## 定位过程

先测 V2/V3 的 board 主链路（`getMatrix` 串行 4 条）：

| 步骤 | 生产耗时（热） |
|---|---|
| `loadCoverage` | 10.0 ms |
| `loadFacts` | 34.8 ms |
| `loadHistograms` | 0.8 ms |
| V3 探针样本 | 0.9 ms |
| **board 合计** | **约 47 ms** |

board 本身不慢。继续测 V3 探针回退里的「30 天缓存率均值」：

```sql
SELECT group_id,
       SUM(cache_read_tokens)::float8
         / NULLIF(SUM(input_tokens + cache_creation_tokens + cache_read_tokens), 0)::float8
FROM usage_logs
WHERE group_id = ANY($1) AND created_at >= now() - interval '30 days'
GROUP BY group_id
```

**19,714 ms**（`Parallel Seq Scan`，扫 1,560 万行）。

根因：生产 `usage_logs` 1,762 万行**全部带非空 group_id**，任何索引都无法裁剪，
只能全库扫描，而返回值只是每分组一个比率。

## 修复

新增 `usage_group_daily(group_id, day)` 预聚表（迁移 919），语义与 lane/011-perf
的 `usage_key_daily` 逐条对齐：时区显式写死、整日重算不累加、只发布已关闭的日。

| | 行数 | 读取耗时 |
|---|---|---|
| `usage_logs` 直接扫 | 17,550,000 | 19,714 ms |
| `usage_group_daily` | 2,275 | **0.295 ms** |

缩减 7,716×。读侧有 `RollupUsable` 守卫：未回填/时区漂移/表被删任一成立即完全
回落到原查询，行为与引入前逐字相同。

**正确性对账**：25 个分组 × 30 天窗口，预聚与直扫逐分组比对，**0 mismatch**。

## 端到端（真实 HTTP，验收栈）

| 路径 | 实际耗时 |
|---|---|
| 走预聚表 | 5.8 / 6.2 / 9.3 ms |
| 关掉开关回落原查询 | 23.7 / 28.1 / 38.7 ms |

验收库只有 33 万行，所以差距被压缩；生产 1,761 万行时是几十倍量级。
关键是回落路径真的生效——关掉开关立即走原查询，不需要重启。

## 顺带修掉的两个静默失败

验收环境真跑时发现两个预聚表都是 0 行、且无任何报错：

1. **`MAX(enabled)` 语法错误**。PostgreSQL 没有 boolean 的 max 聚合，
   `readState` 直接报 `function max(boolean) does not exist`，整个同步失败。
   `usage_key_daily`（lane/011-perf 引入）从上线起就是这个状态。
   改为 `BOOL_AND(enabled)`。
2. **键/组同步互斥**。`UsageKeyDailyRollupRepository` 与
   `UsageGroupDailyRollupRepository` 的 `Sync` 签名完全相同，Go 不能在同一类型上
   用同名方法区分。给 `dashboardAggregationRepository` 加分组版 `Sync` 之后，
   键维度的接口断言被顶掉，键同步静默停止。键维度改走显式命名的
   `SyncKeyUsageDaily`。

修完后验收环境实测：`usage_key_daily` 161,812 行、`usage_group_daily` 有真实日桶，
两个水位都前进到今日，零报错。

## 仍未做

`getMatrix` 串行 4 条查询可以合并成 2 条（减少往返），但当前 board 热态合计
约 47 ms 不是用户可感知的矛盾，且改动涉及 matrix 语义重构，本轮不动。
真要做的话方向是：`loadCoverage` 与 `loadFacts` 合并（coverage 可从 facts 的
bucket 范围推得）。
