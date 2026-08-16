# Micro-One-API v0.20.4 发布：路由死端熔断修复与手动分区安全守卫

> 2026-08-16 · 上一版：[v0.20.3](./release-v0.20.3.md)（2026-08-15）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.4)

v0.20.4 是 v0.20.3 之后的 **PATCH 生产稳定性版本**（7 个提交，`3ea0a6f` → `d96b3c0`），主线是「路由死端与熔断语义隔离 + 手动 DDL 风险前置拦截」：修复无可用渠道 / route dead-end 以 `codes.Unknown` 穿越 gRPC 后被 relay 侧 circuit breaker 计为失败，导致 channel-service 整体熔断且无法自愈的生产事故；为手动 ledger / logs 分区脚本增加 schema 治理、迁移 `078` 与 claim 覆盖完整性硬前置检查；继续沉淀 nightly E2E 稳定性记录与 2026Q3 P3 观察基线。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无应用配置变更**。受影响范围：relay-gateway、channel-service 客户端熔断语义、运维侧手动分区 SQL、nightly E2E 与执行文档。生产 relay-gateway 已完成热部署验证；其他服务无需重新部署。

## 1. 路由死端不再触发 channel-service 熔断（`cccbdf1`）

- **根因**：`SelectChannel` / `SelectSubscriptionAccount` 的确定性死端（“no available channel”、route dead-end）此前作为 `codes.Unknown` 穿越 gRPC。relay 侧 circuit breaker 将 Unknown 计入上游失败；针对无 channel ability 模型的请求风暴足以触发熔断，随后订阅账号选择、凭据刷新等所有 channel-service 调用都被拒绝，relay 请求整体 500。half-open probe 继续落在同一死端上，熔断无法自愈（2026-08-16 incident）。
- **修复**：
  - `pkg/errors.*Error` 实现 `GRPCStatus()`：`CHANNEL_NOT_FOUND` 映射 `NotFound`，`ROUTE_DEAD_END` 映射 `FailedPrecondition`，均为非 retryable / 非熔断计数错误；未登记 reason 保持 `Unknown`；
  - `platform/grpc.isRetryableError` 不再把 `Unknown` 视为可重试；真实传输故障仍以 `Unavailable` / `DeadlineExceeded` 暴露并计入熔断；
  - relay HTTP / Anthropic inbound 边界先识别 channel-unavailable 业务语义，再进入 gRPC code switch，避免死端的 `NotFound` 被泛化映射为 401，保持预期 503。
- **影响服务**：relay-gateway（含其调用 channel-service 的 gRPC resilience 语义）。
- **行为变化**：模型无可用路由继续返回 503，但不再污染 channel-service 可用性统计；真实连接失败、超时仍会触发熔断保护。

## 2. 手动分区 DDL 硬前置守卫（`c58df1e`）

- **根因**：ledger / logs 手动分区脚本属于高风险 DDL。若在 schema 未纳入 `schema_migrations` 治理、迁移 `078` 未应用或 ledger → dedupe claim 存在覆盖缺口时执行，会绕过 v0.20 建立的资金幂等前提。
- **修复**：
  - ledger 分区脚本执行前硬校验：目标 schema 已纳入 `schema_migrations`、迁移 `078` 已 applied、`billing_ledger_dedupe_claims` 存在且 ledger → claim 覆盖缺失为 0；
  - logs 分区脚本硬校验目标 schema 已纳入迁移治理；
  - 任一条件不满足立即 `SIGNAL` 中止，后续 DDL 不会执行。
- **影响服务**：运维侧 `migrations/manual/phase3_*.sql`；不改自动迁移与服务运行时。
- **验证**：守卫已用 MySQL 8.0 scratch 容器覆盖通过路径与阻断路径。

## 3. nightly E2E 竞态修复与稳定性证据（`c4883d1` / `25d6599` / `e0f7f6b` / `d96b3c0`）

- **根因**：users export 用例此前只等待 URL 到达目标状态。React Router 异步提交期间，UsersPage 旧 render 的 handler 仍可能闭包旧导出地址，点击后发出缺少 `status=1` 的请求；该竞态在 mobile / desktop 项目中两次打断 nightly 计数。
- **修复**：用例改为等待同源 users list committed query，确保页面新 render 已提交后再点击导出；稳定性文档记录失败归因、修复验证和按规则归零重计。
- **结果**：最终修复后 main nightly 连续 3 次双 suite 成功（`31872252648`、`31922896563`、`31924463747`），当前计数 3 / 5；release 挂钩仍等待准入标准，未提前接入。
- **影响服务**：nightly Playwright admin smoke 与文档；不改前端产品代码。

## 4. 2026Q3 P3 观察基线（`3ea0a6f`）

- **内容**：建立 2026Q3 首次 P3-0 指标快照，覆盖入口 RPS、P50/P95/P99、429/502、熔断与 dedupe claim 覆盖；同时登记 Prometheus 保留窗口约 41h、Grafana 只读凭据缺失与应用级 dedupe conflict counter 缺口。
- **结论**：claim ↔ ledger 双向覆盖与重复 key 检查 PASS，429/502 未满足 P3 触发条件；P3 五大议题维持触发式准入，不直接立项。
- **影响服务**：观测文档；不改指标采集与服务运行时。

## 兼容性说明

- **API / 公共 proto**：无变更；对外错误状态保持不变，路由死端仍为 503。
- **数据库**：无新增自动迁移。手动分区脚本新增执行前置守卫，不改变合法路径的分区 DDL。
- **配置**：无应用配置变更。
- **行为变化**：确定性路由死端不再被 relay circuit breaker 计为 channel-service 失败；`Unknown` gRPC 错误不再被自动重试。真实 `Unavailable` / `DeadlineExceeded` 仍按原语义计数与重试。
- **部署**：仅 relay-gateway 需要更新；channel-service 及其他服务无需重建或重启。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.4
# 无数据库迁移、无应用配置变更。
# 按仓库标准流程重新构建并部署 relay-gateway。
# 如后续需要执行手动分区 DDL，同步 migrations/manual 脚本以获得硬前置守卫。
```

## 验证

- develop CI run `31922297076`（`cccbdf1`）：Backend、Frontend、Integration、MySQL / Postgres migration smoke 与 18 个 Docker build matrix 全部通过。
- main nightly run `31924463747`（包含本修复的 merge `d5b0024`）：Compose E2E + Playwright admin smoke 双 suite 成功。
- 生产热部署验证：relay-gateway 镜像 `sha256:7dab0c185810...` 重启后 HTTP / gRPC 正常启动，真实 `glm-5.3` 路由成功，异步 settlement 入队正常；近 2 小时无 breaker / route dead-end / panic / ERROR 日志。
- 定向回归：`pkg/errors` gRPC status 映射、relay HTTP channel-unavailable 边界、`platform/grpc` retry 分类测试通过。

## 完整变更日志

- docs(observability): establish 2026Q3 P3 baseline
- fix(e2e): await committed users list query before export
- docs(ci): record export fix nightly success
- fix(migrations): guard manual partitioning with hard preflight checks
- fix(relay): stop routing dead-ends from tripping the channel-service breaker
- docs(ci): record scheduled nightly success
- docs(ci): record routing merge nightly success
