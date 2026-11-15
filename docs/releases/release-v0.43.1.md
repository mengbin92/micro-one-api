# Micro-One API v0.43.1 发布：OpenTelemetry 安全修复与 README 发布信息同步

> 2026-11-15 · 上一版：[v0.43.0](./release-v0.43.0.md)（2026-11-14）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.43.1)

v0.43.1 是 v0.43.0 之后的 **PATCH 安全修复版本**：把 OpenTelemetry Go 依赖族从 v1.46.0 升到 v1.47.0，修复 `GHSA-q7gw-24fr-5p69` 对应的两条 medium Dependabot 告警；同时补齐 v0.43.0 发版时遗漏的 README 发布信息同步。无新增迁移、无 API 变更、无计费契约变化。

版本判 PATCH 而非 MINOR 的依据：仅依赖安全升级与文档同步，不新增功能、不改变既有行为。

## 安全修复

- `GHSA-q7gw-24fr-5p69`：`opentelemetry-go` 的 `AttributeValueLengthLimit` 可被由合法 U+FFFD rune 组成的属性值完全绕过。影响 `otel/sdk` ≥ 1.33.0 / `otel/trace` ≥ 1.35.0 且 < 1.47.0，仓库原为 v1.46.0，对应告警 #79 / #80（medium）。
- 整族同步升级到 **v1.47.0**：`go.opentelemetry.io/otel`、`metric`、`sdk`、`trace`、`exporters/otlp/otlptrace/otlptracehttp`，以及间接依赖 `otlptrace`、新增的 `otel/log`；`genproto/googleapis/api` 与 `rpc` 由 exporter 拉动升级。`go.sum` 无 v1.46 残留。

## README 同步

- `README.md` / `README.en.md`：顶部「最新发布 / Latest release」从 v0.42.0 更新到 v0.43.1，发布列表补入 v0.43.1 与 v0.43.0，并在功能概览中说明 v0.43.0 起 registry mapping 是唯一路由来源、未注册模型不再兜底。
- `docs/README.md`：最新发布登记与 `releases/` 索引补齐 v0.41.0–v0.43.1，「最新」标记移到位。
- `CHANGELOG.md` 增加 `## [0.43.1]`。

## 兼容性与升级

- 无 schema 变更：v0.43.0 已在生产应用迁移 151，本版**不需要**再执行迁移或备份。
- 无 API / 配置 / 计费变更；`restrict_models` 相关契约与 v0.43.0 一致。
- 升级只需用同一 commit 重新构建并部署 `channel-service`、`relay-gateway`、`admin-api`，并把复用 channel 镜像的 `channel-account-ops-worker` 切到同一 tag（部署脚本只改具名服务块，worker 不会自动跟随；详见 [v0.43.0 发布公告](./release-v0.43.0.md)）。
- 主生产 Compose 的前端由宿主机 `/opt/web/dist` 独立挂载，本版无前端改动，不需要更新前端产物。

回滚：使用本次部署生成的共享 `rollback-<timestamp>` 镜像 tag 回退即可，无数据回写。

## 验证

- 本地：`go build ./...`、`go mod tidy -diff`、全仓非 E2E Go 测试 131 包（带 MySQL / PostgreSQL DSN）、`-race`（channel biz/data 与 internal/server）、`make rbac-contract-check`（962 行）、`git diff --check` 全绿。
- GitHub：main 推送后依赖图重新扫描完成，告警 #79 / #80 已自动 `fixed`，全仓 `fixed: 30 / open: 0`。
- 线上：见本次部署验证记录（4 个实例镜像 tag 一致、容器健康、健康端点 200、无 panic / `no such table` 日志）。
- 已知限制：本机 web 全量 vitest 存在既有 jsdom 超时基线，本轮无前端改动未重跑；以 CI Frontend job 为准。

## 完整变更日志

见 [CHANGELOG.md](../../CHANGELOG.md) `## [0.43.1]` 与 `git log v0.43.0..v0.43.1`。
