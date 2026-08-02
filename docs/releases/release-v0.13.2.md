# Micro-One-API v0.13.2 发布：修复 billing-service crash loop 致间歇性 402 + 干净建库迁移适配

> 2026-08-02 · 上一版：[v0.13.1](./release-v0.13.1.md)（2026-08-01）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.13.2)

v0.13.2 是 v0.13.1 的 **PATCH 修复版本**，包含两个 `fix` 提交：修复 v0.13.0 落地异步结算分账恢复路径后引入的 billing-service crash loop（线上观测到约 1274 次重启，表现为 relay-gateway 的 `ReserveQuota` gRPC 间歇失败、客户端收到空消息体的 402），以及修复迁移文件默认了旧版共享 DB 历史导致全新 MySQL / PostgreSQL / SQLite 环境无法干净建库的问题。

**无 API 破坏性变更、无新增数据库迁移文件**（仅修改既有迁移文件的守卫逻辑）。受影响的运行时服务为 `billing-service` 与 `relay-gateway`；迁移修复不涉及运行时镜像，但新部署/重建库时需使用本版。

## 修复内容

### 1. billing-service crash loop 致间歇性 402（生产事故级）

**根因**：异步结算 worker 在执行 `CommitQuotaWithUsageAndSplit` 时调用
`ledgerRepo.FindByDedupeKey(ctx, nil, key)` 做 best-effort 分账恢复。`FindByDedupeKey`
将 `nil` 事务透传给 `txDB()`，得到 `nil`，随后 `nil.WithContext(ctx)` 触发空指针
panic。panic 杀死 worker 协程，进而拖垮整个 `billing-service`，导致 docker 反复重启
（线上观测到约 1274 次重启）。重启窗口期内 relay-gateway 的 `ReserveQuota` gRPC
调用失败，对外表现为间歇性 402 且响应消息体为空。

**三处修复**：

- `app/billing/internal/data/ledger_repo.go`：`FindByDedupeKey` 在 `tx` 为 `nil` 时
  回退到 `r.data.DB`，不再 panic；同时把空 key 守卫移到 `txDB()` 调用之前，避免对
  无效 key 也触发空指针。
- `app/billing/internal/biz/async_billing.go`：`processSettlement` 增加
  `defer/recover`，单个结算任务 panic 不再击穿整个 settlement worker。
- `internal/server/http_response.go`：`gatewayErrorMessage` 对每个状态码都返回非空、
  客户端安全的文案（如 `402 → insufficient quota`），杜绝空消息体返回。

**影响服务**：`billing-service`、`relay-gateway`。

### 2. 干净建库（clean-room）迁移适配所有驱动

迁移文件默认了旧版共享 DB 的历史（表/结构从现有安装复制而来），导致全新 MySQL
单库或 per-service schema 构建失败：

- `061` 无条件创建 `oneapi_billing.system_options` → 改为基于 prepared statement
  的 schema/table 存在性守卫（旧版全量执行也被修复）。
- `031` / `067` 同时 ALTER `logs` 与 `billing_ledgers` → 对每个表/列单独加守卫，
  使 billing（logs 为视图）、log（无 `billing_ledgers`）以及重入执行都安全。
- `ownership.yaml`：log 补 `016`（`031`/`037`/`067` 依赖的用量列）；billing 补
  `031`（`billing_ledgers.cache_read_tokens`）并移除 `037`（billing schema 下 logs
  是视图而非表）。
- `schema_split.sql` 硬编码旧版 oneapi 源，属参考 DDL，现与
  `phase3_partitioning.sql` 一样从自动应用中排除。
- PostgreSQL 基线 `000`：表内 UNIQUE 约束里使用 `COLLATE` 非法 → 改为普通 unique
  约束（canonical 索引已覆盖大小写不敏感）。
- SQLite：runner 落实文档约定的「duplicate column name 幂等 no-op」契约；
  `009` 将多列 ALTER 拆分为单列语句（SQLite 单次 ALTER 仅支持一列）。

**本地验证**：MySQL per-service（8 schema）、legacy 单库、`071→076` 升级；PostgreSQL
clean + 升级；SQLite clean + 升级；新增 runner 单测（schema_split 跳过、duplicate
column 容忍）。

## 兼容性说明

- **API**：无破坏性变更。
- **数据库**：无新增迁移文件（仅修复 `031`/`061`/`067` 等既有文件的守卫逻辑与
  ownership 归属）。
- **配置**：无新增配置项。
- **部署**：
  - 已部署环境：重新构建并部署 `billing-service` 与 `relay-gateway`（迁移修复不
    影响运行时镜像）。
  - 全新部署 / 重建库：使用本版迁移文件可对 MySQL / PostgreSQL / SQLite 干净建库。

## 升级步骤

已部署环境（仅需运行时修复）：

```bash
git fetch --tags
git checkout v0.13.2

# cross-build 并部署受影响的两个服务
./scripts/deploy-update.sh billing-service relay-gateway
```

全新部署 / 重建库：直接使用本版，迁移文件已适配全部驱动。

## 验证

- `billing-service` 与 `relay-gateway` 部署后 0 重启、日志无 panic。
- 异步结算分账路径不再触发 `FindByDedupeKey` 空指针。
- `402` 等网关错误现在返回明确文案而非空消息体。
- MySQL / PostgreSQL / SQLite 三驱动 clean-room 建库与升级路径均通过。

## 完整变更日志

- fix: resolve intermittent 402 caused by billing-service crash loop
- fix(migrations): make clean-room DB provisioning work for all drivers
- docs: backfill CHANGELOG with v0.9.0–v0.13.1 entries and close cache_creation TODO
