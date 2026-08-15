# Micro-One-API v0.20.3 发布：数据库迁移门禁与 nightly E2E 稳定性补强

> 2026-08-15 · 上一版：[v0.20.2](./release-v0.20.2.md)（2026-08-14）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.3)

v0.20.3 是 v0.20.2 之后的 **PATCH 质量门禁版本**（7 个提交，`c366dd9` → `a338a1c`），主线是「迁移安全前置 + nightly 可观测稳定」：CI 新增真实 MySQL / Postgres migration smoke，覆盖 fresh apply、repeat no-op、状态审计与失败注入；修复 compose MySQL healthcheck 过早 healthy 的竞态与 admin users export E2E 的 React Router 异步提交竞态；建立 P3-0 季度观察基线模板并补充 relay-gateway 延迟分位数、429/502 与熔断面板。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无应用配置变更、无服务运行时代码变更**。受影响范围：CI / nightly E2E、`deployments/docker-compose`、Grafana dashboard 与 v0.21 执行文档；生产服务无需重新部署。

## 1. 真实 MySQL / Postgres migration smoke 门禁（`c366dd9`）

- **根因**：此前迁移验证主要依赖静态治理与单元测试，缺少真实 MySQL / Postgres 方言下「全部迁移可执行、重复执行幂等、状态记录完整、失败不落账」的自动化证据。
- **修复**：CI 新增 `Migration smoke (mysql)` 与 `Migration smoke (postgres)` service-container job；`Makefile` 新增 `migration-smoke-mysql` / `migration-smoke-postgres`；`scripts/test-migration-smoke.sh` 依次执行 fresh apply、repeat apply（必须输出 `nothing to apply`）、status audit（逐一确认 applied）与无效 SQL 失败注入（必须失败且不得记录版本）。
- **影响服务**：CI 门禁；不改变服务运行时行为。

## 2. compose MySQL TCP readiness 修复（`034e264`）

- **根因**：MySQL healthcheck 使用 `localhost` 时可能命中 entrypoint 临时 initdb 服务器的 Unix socket，在 TCP 3306 尚未可用时提前 healthy，导致 migrate one-shot 偶发 connection refused。
- **修复**：healthcheck 强制使用 `mysqladmin ping -h 127.0.0.1`，确保以 TCP readiness 作为迁移启动条件。
- **影响服务**：`deployments/docker-compose` 的本地 / CI compose 环境；不改生产应用配置。

## 3. admin users export E2E 竞态修复（`1d5c2d9` / `9752789`）

- **根因**：React Router 异步提交 searchParams 时，URL 可先到达 `status=1`，但 UsersPage 旧 render 中的 export handler 仍闭包旧 `exportHref`；此时点击导出会发出未带筛选条件的请求，测试断言因此 flaky。
- **修复**：E2E 等待 committed URL 与按钮可用后点击，并直接 poll 实际后端导出请求，直到请求同时包含 `status=1` 与 `format=csv`，不再用早期 URL 状态间接推断 handler 已更新。
- **影响服务**：nightly Playwright admin smoke；不改前端产品代码。

## 4. P3-0 观察基线与 Grafana 面板（`63f92d3` / `123864b` / `a338a1c`）

- **根因**：P3-0 触发式议题需要可复用的季度指标基线，而现有 dashboard 缺少入口延迟分位数、429/502 比例与熔断状态的直接视图；nightly 稳定性也需要可审计的失败归因和重新计数记录。
- **修复**：新增 `docs/observability/p3-quarterly-baseline-template.md`；relay-gateway dashboard 增加 P50/P95/P99 latency、429/502 ratio 与 circuit breaker state/trips 面板；记录 nightly 失败归因、main nightly 双 suite 成功与按规则重新计数。
- **影响服务**：Grafana / v0.21 执行文档；不改变指标采集与服务运行时。

## 兼容性说明

- **API / 公共 proto**：无变更。
- **数据库**：无新增迁移；migration smoke 在 CI scratch database 中执行，不触碰生产库。
- **配置**：应用配置无变更；仅 `deployments/docker-compose` 的 MySQL healthcheck 目标从 `localhost` 改为 `127.0.0.1`。
- **行为变化**：无服务运行时行为变化。CI 对迁移失败、重复迁移非 no-op、状态漏记和无效 SQL 的接受条件变严格；nightly export 用例改为等待实际请求携带 committed filter。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.3
# 无数据库迁移、无应用配置变更。
# 生产服务运行 v0.20.2 时无需重新部署。
# 如使用仓库 compose / Grafana 配置，同步 docker-compose.yml 与 relay-gateway.json 以获得 readiness 修复和新增面板。
```

## 验证

- develop CI run `31862505979`：Backend、Frontend、Integration、Migration smoke (mysql)、Migration smoke (postgres)、Deployment and docs drift 通过。
- main nightly run `31861619643`：Compose E2E + Playwright admin smoke 双 suite 成功。
- 失败注入验证：无效 SQL migration 报错并保持未 applied；repeat apply 输出 `nothing to apply`。

## 完整变更日志

- test(ci): add mysql and postgres migration smoke
- fix(ci): harden mysql readiness and record nightly stability
- docs(ci): record nightly e2e baseline success
- docs(observability): add p3 quarterly baseline framework
- fix(e2e): poll export URL until filters commit
- fix(e2e): wait for committed user export filters
- docs(ci): restart nightly e2e stability count
