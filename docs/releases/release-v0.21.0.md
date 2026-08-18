# Micro-One-API v0.21.0 发布：v0.21 阶段稳定性与质量门禁收尾

> 2026-08-18 · 上一版：[v0.20.5](./release-v0.20.5.md)（2026-08-18）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.21.0)

v0.21.0 是 v0.20.5 之后的 **阶段收尾版本**。本版本完成 v0.21 路线图定义的资金安全验证、真实 MySQL / PostgreSQL migration smoke、发布前 E2E 门禁和 P3-0 观察基线闭环；不主动扩张未触发的产品议题，也不引入新的运行时功能。

**无 API 破坏性变更、无新增数据库迁移、无 proto 变更、无应用配置变更**。相对 v0.20.5，本版本主要固化 v0.21 阶段的验收结论、发布证据和后续触发式治理边界。

## 1. v0.20 迁移后的首个结算周期对账

- **背景**：迁移 `078` 将分区账本的全局幂等闸门迁移到非分区 `billing_ledger_dedupe_claims` 表，必须通过真实结算数据验证，而不能只依赖单测。
- **验收**：首个完整结算周期已完成对账；重复 `ledger_dedupe_key`、claim 覆盖、重复请求错误映射和对账脚本输出均完成复核，异常项有归因记录。
- **结论**：资金幂等语义风险已闭环，后续继续使用 ledger ↔ claim 双向覆盖检查作为 DB 侧守护。
- **记录**：见 [v0.21-p0-reconciliation-record.md](../design/v0.21-p0-reconciliation-record.md)。

## 2. MySQL / PostgreSQL migration smoke

- CI 已使用真实 MySQL / PostgreSQL service-container 执行 fresh、repeat no-op、状态审计和失败注入路径。
- 迁移日志与 `schema_migrations` 状态可审计，方言语法和约束错误能够在 CI 中被捕获。
- 该门禁与 SQLite smoke、静态 migration check 一起构成发布前迁移质量证据。

## 3. Release E2E 门禁真实闭环

- nightly compose E2E + Playwright admin smoke 已达到连续 5 次成功的准入标准。
- nightly 与 release 共用 reusable `e2e.yml`，避免两套 E2E 语义漂移。
- `v0.20.5` 真实 Release run `32110577994` 已验证：目标 tag 上先执行 E2E，随后 Docker 多架构镜像和 GitHub Release 全部成功。
- E2E 失败时，镜像推送与 GitHub Release 均会被阻断，同时保留 compose 日志、容器状态和 Playwright report 等诊断工件。

## 4. P3-0 观察基线与触发式治理

- 已形成可复用的 2026Q3 P3-0 观察模板和首次快照，覆盖入口 RPS、延迟分位数、429 / 502、熔断、dedupe claim 增长和 ledger ↔ claim 覆盖。
- 首次快照中的重复 key 与双向覆盖检查均通过，429 / 502 未达到 P3 议题触发条件。
- Prometheus retention 与 Grafana 只读访问已核实；dedupe 冲突 counter 等数据质量缺口已登记为后续观察项，不为观察目的改动资金热路径。
- 手动分区 DDL、热点文件拆分及五大 P3 议题继续遵循触发式准入，不属于本版本前置。

## 兼容性说明

- **API / 公共 proto**：无变更。
- **数据库**：无新增自动迁移；迁移 `078` 已在 v0.20 阶段完成并经过首个结算周期复核。
- **配置**：无应用配置变更。
- **运行时行为**：相对 v0.20.5 无新增运行时行为；本版本固化阶段验收和发布质量门禁。
- **部署**：已运行 v0.20.5 的生产环境无需因本阶段收尾文档重新部署服务；如按统一版本策略更新镜像，可直接使用 v0.21.0 标签。

## 升级步骤

```bash
git fetch --tags
git checkout v0.21.0
# 无新增数据库迁移、无应用配置变更。
# 按环境的统一版本策略更新镜像即可；已运行 v0.20.5 且不要求版本标识切换的环境无需重启。
```

## 验证

- develop CI run `32106145061`：Backend、Frontend、Integration、MySQL / Postgres migration smoke、架构 / 生成文件检查与 Docker build matrix 全部通过。
- develop Security Pipeline run `32106145066`：CodeQL、gosec、govulncheck、gitleaks、Trivy、SBOM 与 license scan 全部通过。
- nightly scheduled run `31989324792`：最终修复后连续第 5 次双 suite 成功。
- reusable workflow 远端验证 run `32087512854`：Compose E2E 与 Playwright admin smoke 双 suite 成功。
- v0.20.5 Release run `32110577994`：Release E2E、Docker 多架构镜像与 GitHub Release 全部成功，完成 v0.21 路线图最后一个硬门禁的真实验证。

## 完整变更日志

- docs(release): v0.21.0
