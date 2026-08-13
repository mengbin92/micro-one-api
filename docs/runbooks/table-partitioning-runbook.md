# 日志/账本表分区启用 Runbook（v0.19 P3）

> 适用范围：MySQL 8.0；**手动**执行的可选运维操作，不属于自动 migrate。
>
> 目标表位于不同 schema，且 `created_at` 的存储类型不同：
>
> - `oneapi_log.logs`：Unix epoch 秒（`BIGINT`），按 `RANGE(created_at)` 分区；
> - `oneapi_billing.billing_ledgers`：`DATETIME(3)`，按
>   `RANGE(TO_DAYS(created_at))` 分区。
>
> 状态：截至 **2026-08-13**，两表均未达到启用阈值；默认关闭维护开关。

## 一、生产现状（2026-08-11 实测）

| 表 | Schema | 大小 | 行数 | 当前状态 |
|---|---|---:|---:|---|
| `logs` | `oneapi_log` | 13 MB | ~22.9k | 未分区；`created_at` 是 Unix 秒 |
| `billing_ledgers` | `oneapi_billing` | 24 MB | ~22.7k | 未分区；全局 dedupe claim 已由迁移 `078` 提供 |

## 二、触发阈值

满足以下任一条件后，才创建 ADR、安排低峰窗口并执行本 runbook：

1. 单表达到 **1 GB** 或 **1,000 万行**；
2. 出现可归因于表规模的慢查询，且 `created_at` 范围过滤可从分区裁剪受益；
3. 已批准数据生命周期策略，需要以按月 `DROP PARTITION` 代替大范围 `DELETE`。

当前表规模远低于阈值，**不得仅因 DDL 已准备而启用分区**。

## 三、前置条件

### 1. 共通

- MySQL ≥ 8.0；在低峰窗口执行。`ALTER TABLE ... PARTITION BY` 会重建表并阻塞写入。
- 已备份目标 schema，并记录 `SHOW CREATE TABLE`、`SHOW INDEX` 与行数。
- 维护器只能由拥有该 schema 的服务运行：log-service 维护 `logs`，billing-service
  维护 `billing_ledgers`；不允许跨 schema 维护。

### 2. `billing_ledgers` 的并发幂等闸门

MySQL 分区表的所有唯一键（包括主键）都必须包含分区列，因此
`billing_ledgers` 不能继续使用全局 `UNIQUE(ledger_dedupe_key)`。

迁移 `078_create_billing_ledger_dedupe_claims.sql` 创建非分区表
`billing_ledger_dedupe_claims`，以 `ledger_dedupe_key` 主键作为全局、并发安全的
claim。`CreateLedgerInTx` 在**与 ledger INSERT 相同的事务**中先插入 claim：

- 首个请求 claim 成功后写入 ledger；
- 并发或重放请求命中 claim 主键，返回 `ErrLedgerDedupeExists`，再统一映射为
  `ErrDuplicateRequest`；
- 事务回滚时 claim 一同回滚，可安全重试。

执行分区前必须确认迁移 `078` 已应用并完成历史 ledger key 回填：

```sql
USE oneapi_billing;
SHOW TABLES LIKE 'billing_ledger_dedupe_claims';
SELECT COUNT(*) AS ledgers_without_claim
FROM billing_ledgers bl
LEFT JOIN billing_ledger_dedupe_claims c
  ON c.ledger_dedupe_key = bl.ledger_dedupe_key
WHERE bl.ledger_dedupe_key <> ''
  AND c.ledger_dedupe_key IS NULL;
```

结果必须为 `0`。**禁止**以 `SELECT COUNT(*)` 再 `INSERT` 代替 claim 主键。

### 3. 保留策略

- `logs`：由 log-service 既有保留策略决定；分区维护可自动删除过期日志分区。
- `billing_ledgers`：默认只创建未来分区，**不会自动 DROP 历史分区**。财务账本归档、
  保留期与删除必须通过独立 ADR/审批后另行实施。

## 四、执行步骤

### 步骤 1：先应用普通迁移

先运行常规 migrate，确保 `078_create_billing_ledger_dedupe_claims` 已落库：

```bash
MIGRATIONS_DRIVER=mysql \
MIGRATIONS_DSN='root:password@tcp(mysql:3306)/oneapi_billing?parseTime=true' \
  go run ./cmd/migrate -dir ./migrations -ownership billing
```

### 步骤 2：分区 `logs`（仅 log schema）

```bash
# 先在 oneapi_log 备份并确认 PK/created_at 类型。
docker exec -i mysql mysql -uroot -p'<pass>' oneapi_log \
  < migrations/manual/phase3_logs_partitioning.sql

# 验证表达式、分区和实际路由。
docker exec mysql mysql -uroot -p'<pass>' -N -e \
  "SELECT PARTITION_NAME, PARTITION_EXPRESSION, PARTITION_DESCRIPTION
   FROM information_schema.PARTITIONS
   WHERE TABLE_SCHEMA='oneapi_log' AND TABLE_NAME='logs'
   ORDER BY PARTITION_ORDINAL_POSITION;"
```

### 步骤 3：分区 `billing_ledgers`（仅 billing schema）

```bash
# 再次确认 migration 078 的 claims 完整；然后才移除 ledger 上的旧唯一索引。
docker exec -i mysql mysql -uroot -p'<pass>' oneapi_billing \
  < migrations/manual/phase3_billing_ledgers_partitioning.sql

# 验证分区、非唯一 lookup index 和全局 claim 主键。
docker exec mysql mysql -uroot -p'<pass>' -N -e \
  "SHOW INDEX FROM oneapi_billing.billing_ledgers;
   SHOW INDEX FROM oneapi_billing.billing_ledger_dedupe_claims;"
```

### 步骤 4：按 owner 开启维护器

```yaml
# app/log/configs/config.yaml
partition:
  enabled: true
  interval: 24h

# app/billing/configs/config.yaml
partition:
  enabled: true
  interval: 24h
```

分别重启对应服务。运行时维护 SQL 与初始化 DDL 的边界一致：

- logs：`UNIX_TIMESTAMP('YYYY-MM-01')`；
- billing ledgers：`TO_DAYS('YYYY-MM-01')`。

### 步骤 5：验证

```bash
# log-service 不应报告 billing_ledgers 维护错误；billing-service 不应访问 logs。
docker logs log-service --tail 100 | grep -i partition
docker logs billing-service --tail 100 | grep -i partition
```

还应执行一次同 key 并发资金请求的回归测试，确认只有一个 claim 与一个 ledger row，
其余请求映射为 `ErrDuplicateRequest`。

## 五、回滚

1. 先设置 `PARTITION_ENABLED=false`，仅停止后续维护；现有分区表仍可查询。
2. 若必须取消分区，在低峰窗口、完成备份后分别执行：

```sql
ALTER TABLE oneapi_log.logs REMOVE PARTITIONING;
ALTER TABLE oneapi_billing.billing_ledgers REMOVE PARTITIONING;
```

3. **不要删除** `billing_ledger_dedupe_claims`；它仍是资金幂等的全局数据库闸门。
4. 恢复旧的 ledger 唯一索引前，必须先移除分区或采用符合 MySQL 分区唯一键规则的设计。

## 六、关联

- 入口索引：`migrations/phase3_partitioning.sql`
- logs DDL：`migrations/manual/phase3_logs_partitioning.sql`
- billing DDL：`migrations/manual/phase3_billing_ledgers_partitioning.sql`
- 全局 claim 迁移：`migrations/078_create_billing_ledger_dedupe_claims.sql`
- 运行时维护：`platform/database/partition`
