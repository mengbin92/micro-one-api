# Micro-One-API v0.18.0 发布：admin 资金写路径请求级幂等

> 2026-08-10 · 上一版：[v0.17.1](./release-v0.17.1.md)（2026-08-10）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.18.0)

v0.18.0 是 v0.17.1 之后的 **MINOR 功能版本**（4 个提交，`fd09278` → `636fdd4`），交付 v0.18 路线图 P0 并修复 P1 首周期发现：admin 资金写路径的**请求级幂等**（方案 B，DB 唯一键）。购买（`PurchaseSubscription`）与充值（`TopUpQuota`）的钱包扣款/充值 ledger 携带基于 `(user_id, request_id)` 的显式去重键，复用 `billing_ledgers.ledger_dedupe_key` 既有全局唯一索引作为闸门——并发同键重复请求命中唯一约束、整笔事务回滚，钱包绝不被扣两次（关闭 M6 已知边界 #1，这是仓库此前唯一已知的资金正确性开放项）。同时顺带修复存量 bug：购买扣款 ledger 此前回退到不含 `user_id` 的 legacy 键，导致**同一 group 的第二次购买（即使不同用户）永远唯一键冲突失败**。另修复 P1 对账首周期（C6）发现的卡单检测误报（余额订单被当卡单），并归档首周期校准 / 告警观察 / 强制失败验证执行记录。

**无数据库迁移、无新增配置项**。API 为 additive（两份 proto 各新增 `request_id` 字段）。受影响服务为 `admin-api`、`billing-service`（含前端购买流程）。P1 修复涉及 billing 对账卡单检测（billing-service）。

## 功能内容

### 1. 请求级幂等：并发重复请求不产生第二笔资金变动

**根因**：M6 实施（v0.13.0，`5759b82`）用 InTx 行锁解决了订阅行的并发覆盖，但明确了边界：admin 层钱包扣款（`PurchaseSubscription` RPC）是行锁事务**之前**的独立调用，并发升级/购买请求仍会各自成功扣款——双扣款此前是仓库唯一已知的资金正确性开放项。

**修复（方案 B，DB 唯一键为主）**：

- **billing**：`PurchaseSubscription` / `TopUpQuota` 的 ledger 显式携带 `LedgerDedupeKey`，格式 `{action}:{user_id截断48}:{request_id≤100}`；复用 `billing_ledgers.ledger_dedupe_key` 既有全局唯一索引（迁移 `045`）作为去重闸门，**零新表零迁移**。并发同键第二次 INSERT 触发唯一约束冲突 → `UpdateBalanceInTx` + `CreateLedgerInTx` 同一事务整体回滚 → 不产生第二笔资金变动。新增 `ErrDuplicateRequest` typed error；唯一约束冲突（MySQL 1062 / Postgres 23505）统一映射。
- **协议**：客户端发送 `Idempotency-Key` 请求头（与 relay 幂等中间件同协议）；空键映射为 `auto:{hex}`（legacy 兼容，不提供幂等保证）；service 边界校验 request_id 长度/字符（超长或含控制字符直接 400 拒绝，防键膨胀/注入）。
- **去重语义**：重复请求 → gRPC `AlreadyExists` → HTTP **409 Conflict**，贯穿 billing service、admin service、admin HTTP 处理器三层；本地业务错误保持 legacy 200 + `success:false` 形态。
- **前端**：购买流程携带 session 级 `Idempotency-Key`（成功/409 时清除，瞬时重试期间保留），双击/重试不再造成重复扣款。
- **测试**：并发 N×同键购买只扣款一次（sqlite 唯一约束，`-race` 守护）、legacy 冲突回归、回滚语义、充值去重、键格式/校验、409 映射。`make test-race` 现已覆盖 `app/billing` + `app/admin`。

**影响服务**：`admin-api`、`billing-service`。

### 2. 顺带修复：group 级 legacy 去重键冲突 bug

**根因**：`PurchaseSubscription` 写扣款 ledger 时未显式设置 `LedgerDedupeKey`，`CreateLedgerInTx` 回退到 `legacyLedgerDedupeKey`（`{ref}:{type}:legacy`）。PurchaseSubscription 的 ReferenceID = groupID，因此每笔扣款 ledger 的键固定为 `{group_id}:subscription:legacy`——**不含 user_id、不含 request_id**。而 `ledger_dedupe_key` 是全局唯一索引（迁移 `045`），实测**同一 group 的购买扣款只能成功一次**：不同用户购买同一 group 时第二笔直接失败。

**修复**：显式键（含 user_id/request_id）与 legacy 格式天然区分，互不冲突；同一 group 多用户可各自购买，续费/二次购买同 group 恢复正常。补回归测试。

**影响服务**：`billing-service`（资金正确性修复）。

## 兼容性说明

- **API**：additive。`PurchaseSubscriptionRequest` / `TopUpQuotaRequest` 各新增 `request_id = 5` 字段（向后兼容）。重复请求由原先的数据库错误变为 HTTP 409。
- **数据库**：**无新增迁移文件**（复用既有 `ledger_dedupe_key` 全局唯一索引）。回滚 = 代码回退，无需数据回滚。
- **配置**：无新增配置项。
- **运行时**：无键旧客户端获得 `auto:{hex}` 键，行为与现状一致（无幂等保证）；完整幂等防护需要客户端接入 `Idempotency-Key`。

## 升级步骤

```bash
git fetch --tags
git checkout v0.18.0

# 无数据库迁移；重新构建受影响的两个后端服务 + 前端走 web/dist 挂载
./scripts/deploy-update.sh admin-api billing-service

# 前端已在提交内（web/dist 由宿主 /opt/web/dist 挂载，非镜像内），按发版流程另行推送
cd web && npm run build && tar -czf /tmp/web-dist.tar.gz -C dist .
```

## 验证

- `go build ./...`、`go vet`、`./scripts/check-architecture.sh` 通过。
- `make test-unit`、`make test-race`（now covers app/billing + app/admin）全部通过。
- 并发 N×同键购买只扣款一次；同一 group 多用户可各自购买（legacy 冲突回归）。
- 重复请求返回 HTTP 409，钱包余额不变。

## 完整变更日志

- feat(billing): v0.18 P0 request-level idempotency for admin money paths
- docs(release): v0.18.0
- fix(billing): exclude balance orders from stuck-issuance detection
- docs(ops): v0.18 P1 first-period calibration, alert observation, verification archive
- fix(docs): remove broken .workbuddy roadmap link in v0.18 design decision
