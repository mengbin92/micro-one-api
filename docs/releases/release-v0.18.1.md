# Micro-One-API v0.18.1 发布：修复 Token 创建零配额缺陷 + 错误码映射 + 工程卫生

> 2026-08-11 · 上一版：[v0.18.0](./release-v0.18.0.md)（2026-08-10）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.18.1)

v0.18.1 是 v0.18.0 之后的 **PATCH 修复版本**（3 个提交，`e6c1673` → `0ebe48a`），核心是修复两个影响生产可用性的缺陷：（1）Token 创建默认配额 bug——前端仅发送 `{name}` 时 `unlimited_quota` 被解析为 `false`，导致创建出的 Token 处于永久耗尽状态（首次使用即被 `TOKEN_EXHAUSTED` 拒绝）；（2）identity → relay 错误码链路映射错误——`ErrTokenExhausted` 被映射为 gRPC `NotFound` → HTTP 401，误导客户端将「有效但额度耗尽」的 key 当成「错误的 key」。同时完成 v0.18 P2 工程卫生（C2/C4/C5）：修复 MySQL `RowsAffected==0` 导致幂等更新误判 NotFound 的 DSN 边界 bug（影响 10+ 调用点），补齐 billing commit/reserve Prometheus 指标 Observe 与 admin-api gRPC 客户端延迟拦截器，归档表分区运维手册与工程决策文档。

**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为 `identity-service`、`relay-gateway`、`admin-api`、`billing-service`。

## 修复内容

### 1. fix(identity): Token 创建默认无限 + 错误码映射（3b36034）

两个关联 bug 导致有效 API Key 被误拒并返回误导性的 HTTP 401：

#### 1a. Token-create 默认配额 bug

**根因**：`POST /api/token` 处理器将 `unlimited_quota` 解析为 `bool`（值类型）。JSON 反序列化时，省略该字段得到零值 `false`，因此前端仅发送 `{"name": "my-key"}`（最常见路径）时 `unlimited_quota=false`、`remain_quota=0`——Token 以**永久耗尽**状态写入数据库，首次使用即被 `TOKEN_EXHAUSTED` 拒绝。`bool` 无法区分「省略」与「显式 false」。

**修复**：
- 请求结构改用 `*bool`（指针），JSON 省略 → `nil`，与显式 `false` 可区分；省略时默认为无限（`true`）。
- 新增 biz 层守卫 `ErrTokenQuotaInvalid`，拒绝创建退化的「有限但零配额」Token。

**测试**：`TestIdentityHTTPTokenCreateDefaultsUnlimited`——`{name}`-only POST 必须创建一个可用的无限 Token。

#### 1b. 错误码映射链路修复

**根因**：`ErrTokenExhausted` 在 `mapIdentityErrorToGRPC` 被映射为 gRPC `codes.NotFound`，relay-gateway 将 `NotFound` 翻译为 HTTP 401 Unauthorized。这误导客户端（cc-switch、SDK）将「有效但额度耗尽的 key」当作「错误的 key」处理。`ErrTokenSubnetViolation`（子网限制）此前无 HTTP 状态码映射，fallback 为 500。`ErrTokenDisabled`（已禁用）也走 `NotFound` → 401。

**修复**（identity → relay 全链路）：

| 场景 | 修复前 | 修复后 |
|------|--------|--------|
| 不存在 / 过期的 Token | 401 Unauthorized | 401 Unauthorized（不变） |
| 已禁用的 Token | 401 Unauthorized（NotFound） | **403 Forbidden**（PermissionDenied） |
| 子网限制违反 | **500**（未映射） | **403 Forbidden** |
| 额度耗尽 | **401 Unauthorized**（NotFound） | **429 Too Many Requests**（ResourceExhausted） |

具体改动：
- `mapIdentityErrorToGRPC`：拆分 `exhausted → ResourceExhausted`、`disabled/subnet → PermissionDenied`、真实鉴权失败 → `Unauthenticated`（原全部 `NotFound`）。
- `pkg/errors`：新增 `ReasonTokenSubnetViolation`（原未映射 → 500）；`TokenExhausted` HTTP 码 401 → 429；从 `IsUnauthorized` 移除 `TokenExhausted`。
- `MapIdentityError`：处理 `ErrTokenSubnetViolation`。
- relay-gateway 全部错误处理器（`handleIdentityError`、`handleRelayPlanError`、orchestrator、`anthropic_inbound`、`http_enhanced`）：新增 `codes.Unauthenticated → 401` 分支，防止新映射的鉴权失败码 fallback 到 500。

**测试**：`TestGetAuthSnapshotErrorCodeMapping`——不存在 → `Unauthenticated`、耗尽 → `ResourceExhausted`、禁用 → `PermissionDenied`。

**影响服务**：`identity-service`、`relay-gateway`。

### 2. feat(observability): v0.18 P2 工程卫生 C2/C4/C5（e6c1673）

#### C2：MySQL RowsAffected==0 边界修复

**根因**：MySQL 默认的 `CLIENT_FOUND_ROWS` 语义是「changed rows」（值未变不计数），而代码中大量幂等更新检查 `RowsAffected == 0 → ErrXxxNotFound`。当 UPDATE 将某列设为其当前值时，`RowsAffected` 返回 0（值未变），触发 false-positive NotFound。此前仅 gorm 的 `openMySQL` 路径设置了 `clientFoundRows=true`，而 `*sql.DB` 的 `OpenSQLWithPool` 路径（喂给 admin `system_options` 和 migrate CLI）是盲区。

**修复**：抽取共享 DSN helper `withClientFoundRows`，被 gorm 和 `*sql.DB` 两条 MySQL 开路径共用，确保每条通过 `xdb` 打开的 MySQL 连接都获得 matched-rows 语义。影响 10+ 调用点。

**守护测试**：`TestOpenMySQLForcesClientFoundRows` + `TestWithClientFoundRowsSharedByBothMySQLPaths`。

**影响服务**：全部使用 `xdb` 的服务（admin-api、billing-service、identity-service 等）。

#### C4：日志分区触发文档化

**内容**：`billing_ledgers` 的分区机制已存在于 `partition.enabled` 配置开关后（默认 off），但此前未文档化触发阈值与生产 sizing 证据。本提交补充分区触发阈值（1GB / 10M rows / slow query）、生产 sizing 证据（logs 13MB、ledgers 24MB——远低于阈值）、运维手册 `docs/runbooks/table-partitioning-runbook.md`，并标注 `billing_ledgers` unique-index 约束（MySQL 1503）为前置条件。**无代码逻辑变更，仅文档**。

#### C5：Prometheus 指标补齐

**根因**：`BASELINE.md` 的服务依赖延迟表一直显示「N/A — not scraped」，根因不是 scrape 配置而是**缺少 `Observe` 调用**——`ServiceDependencyLatency` 指标已注册但无客户端拦截器调用它。

**修复**：
- **billing commit/reserve 延迟**：`BillingService.CommitQuota` 在异步 early-return 后同步 `Observe`（`mode=sync`）；`runCommitPipeline` 异步路径 Observe（`mode=async`）。刻意不放在 `CommitQuotaWithUsage` 中——该函数被 `CommitQuotaWithUsageAndSplit` 委托，异步 pipeline 也调用它，放那里会导致 async 提交被重复计数到 sync 标签。
- **admin-api gRPC 客户端延迟**：新增 `xgrpc.UnaryClientMetricsInterceptor`，接入 admin-api 对 identity/channel/billing 三个 dial 点。
- **BASELINE 回填**：补充生产路由 P50/P95/P99 与缓存命中率。

**影响服务**：`admin-api`、`billing-service`。

### 3. fix(test): 排除网络依赖的集成测试（0ebe48a）

**根因**：`make test-unit` 因两个独立原因失败：
1. `TestRelayIntegration/DisabledToken` 的过期断言——commit `3b36034` 有意将 `ErrTokenDisabled` 从 `NotFound`（→ HTTP 401）重映射为 `PermissionDenied`（→ HTTP 403），但该测试未同步更新。修正断言为 `StatusForbidden` 并加注释说明。
2. 5 个 `*/internal/integration` 包绑定真实 TCP listener（固定端口 19000–19013），需要网络权限，但在受限环境（CI 容器、sandbox）中 `bind: operation not permitted`。此前由 Go 测试缓存掩盖。从 `test-unit` 排除 `/internal/integration$` 包，新增 `make test-integration` 专用目标。

**影响**：CI / 本地测试门禁修复，无运行时行为变更。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **错误码**：Token 错误的 HTTP 状态码有**纠正性变更**（均为语义修正，非破坏）：
  - `TOKEN_EXHAUSTED`：401 → **429**
  - `TOKEN_DISABLED`：401 → **403**
  - `TOKEN_SUBNET_VIOLATION`：500 → **403**
  - 客户端若硬编码了旧的 401 分支，需确认是否依赖了误导性行为。绝大多数 SDK 按 4xx 通用处理，不受影响。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **运行时**：MySQL 连接现在统一启用 `clientFoundRows=true`，此前 `*sql.DB` 路径（admin system_options、migrate CLI）的幂等更新行为得到修正。

## 升级步骤

```bash
git fetch --tags
git checkout v0.18.1

# 无数据库迁移；重新构建受影响的服务：
./scripts/deploy-update.sh identity-service relay-gateway admin-api billing-service
```

## 验证

- `go build ./...`、`go vet` 通过。
- `make test-unit` 全部通过（集成测试已分离为 `make test-integration`）。
- Token 创建：`POST /api/token {"name":"test"}` 产生 unlimited、可用的 Token。
- Token 鉴权错误码映射：
  - 不存在 / 过期 → 401
  - 禁用 → 403
  - 子网限制 → 403
  - 额度耗尽 → 429
- MySQL `clientFoundRows` DSN round-trip 守护测试通过。

## 完整变更日志

- feat(observability): v0.18 P2 engineering hygiene (C2/C4/C5)
- fix(identity): token create defaults to unlimited + correct error-code mapping
- fix(test): exclude network-bound integration tests from unit gate
