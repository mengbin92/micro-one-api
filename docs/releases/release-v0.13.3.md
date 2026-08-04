# Micro-One-API v0.13.3 发布：订阅完成幂等性、中继限流熔断修复与 CI 矩阵加固

> 2026-08-03 · 上一版：[v0.13.2](./release-v0.13.2.md)（2026-08-02）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.13.3)

v0.13.3 是 v0.13.2 的 **PATCH 修复版本**，包含 7 个提交（4 fix + 2 ci + 1 docs），修复订阅购买完成的幂等性漏洞（资金相关 M10）、relay-gateway 将上游限流（429/423/529）误计为故障导致断路器误开的 503 问题、admin 补偿失败时的错误信息透出与卡单检测，以及 CI 矩阵/缓存/多架构推送的全面加固。

**无 API 破坏性变更、无新增数据库迁移文件**。受影响的运行时服务为 `admin-api`、`billing-service`、`relay-gateway`。

## 修复内容

### 1. 订阅购买完成幂等性 —— 资产发放声明（M10，资金相关）

**根因**：`admin` 的 `CompleteSubscriptionPurchase` 端点从未回写
`asset_issue_status=issued`，导致历史/异常状态的 paid+pending 订单可以被
反复完成，每次调用都会延长用户的权益期限。

**修复（claim-before-fulfil）**：

- **billing**：新增 `MarkPaymentOrderAssetIssued` /
  `UnmarkPaymentOrderAssetIssued` RPC；`asset_issue_status` 在行锁下做
  pending↔issued CAS（拒绝非当前用户 / 已退款 / 非 paid / 已 issued）。
  `PaymentOrderResponse` 新增 `claimed` 字段区分抢占成功/失败。
- **admin**：完成流程先 claim，仅 winning（`claimed=true`）调用才执行
  fulfil；replay 时观察到已 issued，返回当前订阅；fulfil 失败时释放 claim
  以便重试。fulfilment 逻辑提取为 `fulfilPaidOrder`。
- **测试**：billing biz + data 的 CAS 语义；admin 幂等性回放、失败补偿、
  claim 拒绝错误（`subscription_m10_test.go`）；扩展三个现有 mock repo
  以支持新接口方法。

**影响服务**：`admin-api`、`billing-service`。

### 2. Relay 上游限流不再触发断路器

**根因**：在高并发场景下（config 模型不做本地限流），对上游 codex
订阅账户的突发请求自然触发 429/423/529 限流。v0.13 的账户断路器将这些
状态码计为错误，导致少量并发请求即可打开 30s 熔断窗口，期间所有选择都
返回 503 "no available channel"，而服务和上游均健康。

**修复**：

- `executeAndMeter`：限流状态码不再调用
  `RecordSubscriptionAccountHealth(false)`，429/423/529 不再触发断路器。
  真实故障（5xx、超时、连接错误）仍正常计入。
- `handleSubscriptionAccountViaAdaptor`：被限流的账户对当前请求排除
  （跨账户 failover），但 **不做 cooldown** —— `runSubscriptionAttempt`
  内的同账户重试已有 3×500ms 退避；cooldown 会让单账户池在整个窗口内 503。
- **测试**：429/529 failover 用例断言无 runtime block；新增 429 不触发
  breaker 的用例；5xx 仍触发。

**影响服务**：`relay-gateway`。

### 3. Admin 补偿失败错误透出 + 卡单测试

**根因**：`CompleteSubscriptionPurchase` 在 fulfilment 失败后，如果释放
asset-issuance claim 也失败，操作者无法看到卡单状态（paid + issued +
subscription_id=0），reconciler 也无法检测。

**修复**：

- `CompleteSubscriptionPurchase`：当 fulfilment 失败且释放 claim 也
  失败时，使用 `errors.Join` 携带 **两个** 错误，操作者可看到卡单状态，
  reconciler 可检测。扩展幂等回放注释。
- `subscription_m10_test.go`：mock 正确处理 `UserId` 用于跨用户 claim，
  支持 `unmarkErr` 注入；新增 `FulfilFailureAndUnmarkFailure`（卡单双重
  失败）和 `WrongUserClaimRefused` 用例。

**影响服务**：`admin-api`。

### 4. Billing 卡单发放 reconciliation + CI 矩阵/缓存加固

**Billing（M1，资金相关）**：

- `ReconciliationRepo` 新增 `ListStuckIssuedOrders`：检测 paid 但
  `asset_issue_status=issued` 且 `subscription_id=0` 的订单 —— 即 admin
  `CompleteSubscriptionPurchase` 补偿（Unmark）失败后的卡单状态。
  Reconciler 报告这些订单以便操作者重新触发完成；**不自动修复**。
- `ReconciliationResult` 新增 `StuckIssuanceInconsistencies` + 新指标类型
  `ReconciliationDiscrepancyTypeStuckIssuance`。
- `ReconciliationJob` 记录每个卡单并在告警内容中包含修复指引。
- 测试：`TestRunReconciliation_StuckIssuance`（检测卡单）、
  `TestRunReconciliation_NoStuckIssuance`（干净运行无卡单）。

**CI（M2/L4/L5）**：

- 将 9 个服务的路径/Dockerfile 矩阵提取为单一 JSON 源
  （`.github/workflows/matrix/services.json`），ci.yml / release.yml /
  dockerhub-verify.yml 共享，添加/重命名服务只需修改 JSON。
- release.yml 的 Docker Hub `latest` 标签仅限非预发布 semver tag
  （不含 `-`），防止 v0.14.0-rc1 覆盖 stable latest。
- release.yml 和 dockerhub-verify.yml 新增 GitHub Actions 构建缓存
  （`cache-from/cache-to gha, mode=max`），避免重复发布时从零构建。
- **修复 matrix 输出格式**：`set-matrix` 步骤原先将裸 JSON 数组直接写入
  `$GITHUB_OUTPUT`，缺少 `matrix=` 前缀，导致 GitHub Actions 拒绝解析
  （`Invalid format`），所有依赖 `needs.services.outputs.matrix` 的下游
  job 被**跳过**，v0.13.3 首次发布时 Docker 镜像构建与推送全部失败。
  修复为 `echo "matrix=$(jq -c . < ...services.json)" >> "$GITHUB_OUTPUT"`。

## 其他变更

### CI：多架构 Docker Hub 推送（release 流水线）

- `release.yml`：新增 docker matrix job，为全部 9 个服务构建
  linux/amd64+arm64 并推送到 `mengbin92/micro-one-api-<service>`，
  带 `<tag>` + `latest` 标签；GitHub Release 依赖 docker job 完成。
- `dockerhub-verify.yml`：手动 workflow，验证 Docker Hub 登录和
  relay-gateway + admin-api 的构建健康。

### Docs：订阅代码审查修复状态回填

- 审查文档此前将所有 High 项标记为未修复，但 commit 5cc07fb
  （2026-07-08）已解决 P0/P1（H1-H10）和部分 P2。补充修复追踪
  头部 + section 0.1 状态总览（High/Medium/Low/部署说明/回归覆盖），
  为每个 High section 添加状态徽章。
- 仍待处理：M2（renewal_strategy）、M9（multi-replica fault injection）。

## 兼容性说明

- **API**：无破坏性变更。`PaymentOrderResponse` 新增 `claimed` 字段
  （向后兼容）。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **CI**：需要 `.github/workflows/matrix/services.json` 文件存在（本次提交
  已包含）。

## 升级步骤

```bash
git fetch --tags
git checkout v0.13.3

# cross-build 并部署受影响的三个服务
./scripts/deploy-update.sh admin-api billing-service relay-gateway
```

## 验证

- `admin-api`、`billing-service`、`relay-gateway` 部署后 0 重启、日志无异常。
- 订阅购买完成接口幂等回放：重复调用不再延长权益。
- relay-gateway 在并发场景下不再因 429/423/529 触发断路器 503。
- reconciliation 检测到 2026-07-23 的历史卡单并发出告警。
- `go build ./...`、`go vet`、`check-architecture.sh`、全部单元测试通过。

## 完整变更日志

- ci: push multi-arch images to Docker Hub on release (per-service repos)
- docs(review): backfill subscription code-review fix status per 5cc07fb
- fix(admin,billing): idempotent subscription completion via asset-issued claim (M10)
- fix(relay): treat upstream rate limits as busy, not faults
- fix(admin): surface composite unmark failure + stuck-state tests
- fix(billing,ci): stuck-issuance reconciliation + CI matrix/cache hardening
- fix(ci): set matrix output as key=value for $GITHUB_OUTPUT
