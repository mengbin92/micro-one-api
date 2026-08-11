# 日志/账本表分区启用 Runbook（v0.18 P2 C4）

> 对应 `migrations/phase3_partitioning.sql`（手动应用，非自动 migrate）与
> `platform/database/partition`（后台维护）。
> 目标表：`logs`（log-service）、`billing_ledgers`（billing-service），按月
> `RANGE (TO_DAYS(created_at))` 分区。
> 状态：**评估完成（2026-08-11），未触发启用条件 —— 触发阈值见 §二**。

## 一、生产现状（2026-08-11 实测）

| 表 | Schema | 大小 | 行数 | 已分区 | 可分区性 |
|----|--------|------|------|--------|---------|
| `logs` | oneapi_log | 13 MB | ~22.9k | 否 | ✅ 无全局唯一索引（PK id 除外），可直接按月分区 |
| `billing_ledgers` | oneapi_billing | 24 MB | ~22.7k | 否 | ⚠️ **唯一索引 `idx_ledger_dedupe_key(ledger_dedupe_key)` 不含分区列 created_at，分区 DDL 会失败（MySQL 1503）** |
| `billing_reservations` | oneapi_billing | 19 MB | ~23k | 否 | （未列入 phase3 分区范围） |

MySQL 版本：8.0（原生分区支持 ✅）。

## 二、触发阈值（明确后才启用）

以下任一满足即启用分区（当前均未满足）：

1. `logs` 或 `billing_ledgers` 单表 ≥ **1 GB**，或行数 ≥ **1000 万**；
2. 出现与该表规模相关的慢查询（EXPLAIN 显示全表扫描 / 索引失效，
   与 `created_at` 范围过滤相关）；
3. 清理需求出现：需要按月 DROP 分区代替 DELETE 以快速回收空间。

**当前结论（2026-08-11）：表规模 ≤ 25 MB，远低于阈值，不启用分区。**
维持现状（无分区、维护开关默认关闭）即可；日志按既有保留策略处理。

## 三、启用步骤（触发后执行）

### 前置约束

- **`billing_ledgers` 必须先行解决唯一索引约束**（否则 `ALTER TABLE ... PARTITION BY`
  报 1503）。二选一：
  - **A（推荐，成本高）**：唯一键重建为 `UNIQUE (created_at, ledger_dedupe_key)`，
    分区列前置。语义影响：dedupe key 全局唯一性不变（key 本身唯一，复合唯一更宽）；
    需在低峰窗口 `ALTER TABLE DROP INDEX / ADD UNIQUE`（大表重建，窗口风险）。
  - **B（暂缓）**：`billing_ledgers` 不分区，仅 `logs` 分区。账本表 24 MB
    增长远慢于 logs，清理需求低。
- MySQL ≥ 8.0（生产 ✅）；表无外键引用（logs/billing_ledgers 无外键 ✅）。

### 步骤 1：低峰窗口应用分区 DDL

```bash
# 在 mysql 容器执行（低峰窗口，ALTER 会重建表，期间写阻塞）
docker exec -i mysql mysql -uroot -p'<pass>' oneapi_log < migrations/phase3_partitioning.sql
# 若只分 logs 表：仅执行该文件 logs 段；billing_ledgers 段单独评估后执行
```

> **一致性核对（review 2026-08-11）**：`phase3_partitioning.sql` 含 `logs` +
> `billing_ledgers` 两段 DDL，与 billing-service `partitionTables()` 默认
> `["logs", "billing_ledgers"]`、log-service `LogTable` 一致。启用前务必先处理
> `billing_ledgers` 的唯一索引 1503 约束（见上），否则维护开关一开，
> `PartitionMaintenanceForTable(billing_ledgers)` 会对未分区表持续 Warn。

### 步骤 2：开启维护开关（log-service）

```yaml
# app/log/configs/config.yaml（或生产环境变量）
partition:
  enabled: true            # 或 env PARTITION_ENABLED=true
  interval: 86400s         # 默认 24h
```

billing-service 同理（若启用 billing_ledgers 分区）：
`billing_service` 配置的 `partition.enabled` / `PARTITION_ENABLED`。

### 步骤 3：重启服务并验证

```bash
docker compose restart log-service
# 验证：分区已创建 + 维护无错
docker exec mysql mysql -uroot -p'<pass>' -N -e \
  "SELECT PARTITION_NAME, TABLE_ROWS FROM information_schema.PARTITIONS
   WHERE TABLE_SCHEMA='oneapi_log' AND TABLE_NAME='logs';"
docker logs log-service | grep -i partition   # 应无 Warn
```

### 回滚

- 开关回退：`PARTITION_ENABLED=false` + 重启（维护停止，分区表保留不影响查询）；
- 完全回退：`ALTER TABLE logs REMOVE PARTITIONING`（低峰窗口，需评估停机时间）。

## 四、维护机制（代码路径，已就绪无需改动）

- `platform/database/partition.PartitionManager.PartitionMaintenance`：
  自动 `REORGANIZE PARTITION pmax` 创建下月分区 + 按保留策略 DROP 旧分区
  （`phase3_partitioning.sql` §103/121/139 的 SQL）。
- log-service：`app/log/cmd/log/partition.go:startPartitionMaintenance`，
  `cfg.Bootstrap.Partition.Enabled` 门控（默认关），in-memory store 时 no-op。
- billing-service：`app/billing/cmd/billing/billing_helpers.go` 同款。
- 幂等/安全：维护失败仅 Warn 日志，不影响主流程。

## 五、关联

- DDL 源：`migrations/phase3_partitioning.sql`
- 架构设计：`docs/design/ARCHITECTURE_REFACTOR.md` §5.3
- 决策记录：`docs/design/v0.18-engineering-hygiene-decision.md`（v0.18 P2 C2/C4）
