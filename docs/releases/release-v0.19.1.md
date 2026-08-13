# Micro-One-API v0.19.1 发布：工具链版本固定与工程体验收尾（v0.19 P2）

> 2026-08-13 · 上一版：[v0.19.0](./release-v0.19.0.md)（2026-08-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.19.1)

v0.19.1 是 v0.19.0 之后的 **PATCH 工程收尾版本**（1 个提交，`23d6c8e`），**不含任何生产代码变更、无运行时行为变化**：落地 v0.19 路线图 P2 的全部三项——工具链版本固定（消除 `@latest` 隐式漂移）、`make clean` / `make verify` 与外部服务前置检查、热点文件拆分评估（结论：维持触发式，不主动拆分）。

**无 API 变更、无数据库迁移、无 proto 变更、无配置变更**；生产运行 v0.19.0 的用户无需为本版重新部署。

## 1. 工具链版本固定（P2.1）

- **根因**：`make init` 的 `buf` / `wire` 与 `security-*` 目标、`security.yml` 的 `gosec` / `govulncheck` / `gitleaks` / `syft` / `go-licenses` 均使用 `go install ...@latest`。同一 commit 在不同日期 clone 后工具版本不同，导致生成代码、安全扫描结果、SBOM 内容不可复现，本地与 CI 也可能装到不同版本。
- **修复**：新增唯一版本源 `scripts/tool-versions.env`（KEY=VALUE 格式，Make 与 shell 双解析）——buf v1.72.0 / wire v0.7.0 / gosec v2.28.0 / govulncheck v1.6.0 / gitleaks v8.30.1 / syft v1.51.0 / go-licenses v1.6.0。本地侧 `Makefile` 顶部 `include` 该文件并 `export`，`make init` 与全部 `security-*` 目标改用 `$(VAR)` 精确版本；CI 侧 `security.yml` 各安装步骤先 `set -a; . scripts/tool-versions.env; set +a` 再 `go install ...@${VAR}`。新增 `make tools-upgrade-check`（只读对比 pinned vs latest，供计划性升级 PR 使用）。前端固定 `web/.nvmrc`（24）+ `package.json engines: >=24 <25`，与 CI `setup-node` 24 对齐。升级策略文档化于 `docs/design/v0.19-toolchain-pinning.md`。
- **影响服务**：无（构建/扫描工具链）；`ci.yml` backend job 运行 `make init`，版本文件缺失会让 make 直接失败，存在性天然被门禁覆盖。
- **已知边界**：CI 的 secret scanning 使用 `gitleaks/gitleaks-action@v3`（action 自身版本），不走 env 文件；`GITLEAKS_VERSION` 固定的是本地 `make security-*` 安装路径。

## 2. 清理与开发体验（P2.2）

- **根因**：`make clean` 只清 `bin/` / `logs/`，根目录 ad-hoc `go build` 产物（`channel` / `identity` / `relay-gateway` 等）、e2e 二进制、coverage 与安全扫描报告长期残留；本地复现 CI 门禁需要逐条对照 `ci.yml` 手敲命令，容易漂移；`test-e2e-*` / `migrate` 在缺少 docker 或 DSN 时要跑到很后面才报错。
- **修复**：`make clean` 扩展——根二进制 6 个、`test/e2e/e2e-test`、`coverage.out`、`web/test-results`、`web/playwright-report`、安全报告文件，全部显式列出、`rm -f` 幂等，不使用宽泛通配。新增 `make verify` 聚合目标：unit → race → architecture → migration-check → frontend（lint/test/build），与 `ci.yml` PR 门禁一一对齐，本地与 CI 不再漂移。新增前置检查：`compose-prereq`（docker + compose 文件存在性，挂到 `test-e2e` / `test-e2e-suite`）、`migrate-prereq`（`MIGRATIONS_DSN` / `SQL_DSN` 非空，挂到 `migrate` / `migrate-status`）；`test-e2e-local` 增加本地服务运行提示。
- **影响服务**：无（Makefile 开发面）。

## 3. 热点文件拆分评估（P2.3）

- **结论**：维持触发式，不主动拆分。当前热点文件（`app/channel/internal/data/data.go` 约 3000 行、`app/admin/internal/service/admin.go` 约 2600 行、`app/billing/internal/biz/billing.go` 约 2300 行）无进行中的业务需求驱动拆分；按既定策略，仅当对应资源有实质功能改动时随业务 PR 增量拆分，避免为拆而拆引入无收益的重构风险。
- **影响服务**：无（评估结论，无代码变更）。

## 兼容性说明

- **API / proto / 数据库 / 配置**：全部无变更。
- **行为变化**：无运行时行为变化；所有改动位于 Makefile、CI workflow、文档与前端版本声明。
- **升级**：无强制升级需求，无需重新部署任何服务。

## 升级步骤

```bash
git fetch --tags
git checkout v0.19.1
# 纯工程面版本，无需重新部署；本地开发可 `make verify` 一键对齐 CI 门禁。
```

## 验证

- `go build ./...`：通过。
- `./scripts/check-architecture.sh`：通过。
- `make migration-check`：通过（历史重复前缀 allowlist 警告为预期输出）。
- `make -n verify`：聚合目标构造正确（unit / race / architecture / migration-check / frontend 五段）。
- 全仓库 `@latest` 安装已清零（仅剩注释与 `tools-upgrade-check` 的只读查询）。

## 完整变更日志

- chore(build): pin toolchain versions and add verify/clean gates (v0.19 P2)
