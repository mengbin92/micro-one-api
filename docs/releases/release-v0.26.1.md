# Micro-One-API v0.26.1 发布：Go 1.27 运行时现代化

> 2026-08-31 · 上一版：[v0.26.0](./release-v0.26.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.1)

v0.26.1 是 v0.26.0 之后的 **PATCH 工具链与仓库现代化版本**：将项目统一到 Go 1.27，采用对应的标准库语法与集合辅助函数，升级兼容依赖，并修复 middleware 测试中的 goroutine 清理问题。

本版本不新增公共 API、proto 或数据库迁移，既有 usage/billing、模型定价和 Web 功能保持不变。由于 Go 1.27 改动覆盖所有服务，发布时应整体更新服务镜像；executor observation 期间仍不部署或重启 `relay-gateway`。

## 1. 统一 Go 1.27 工具链与代码风格

**根因**：代码库仍以 Go 1.26 为工具链目标，服务、Dockerfile、CI 和大量 Go 文件存在旧式 `interface{}`、手写集合处理和可由新标准库替代的循环写法，导致工具链目标和实现风格不一致。

**修复**：统一 `go.mod`、Dockerfile 和 CI 的 Go 1.27 目标；将 `interface{}` 迁移为 `any`，采用整数 range、`min` / `max`、slices/maps 辅助函数等 Go 1.27 语法，并保持 provider JSON `omitempty` 序列化契约。

**影响服务**：所有 Go 服务、构建镜像和 CI workflow；无业务规则变化。

## 2. 修复测试 goroutine 清理并对齐运行时依赖文档

**根因**：middleware idempotency 测试的后台 goroutine 清理存在不规范路径，运行时与依赖文档也没有完整反映 Go 1.27 目标。

**修复**：补齐测试 goroutine 的退出等待 / 清理语义，更新运行时和依赖参考文档，并保留 observation 文档的最新窗口、重启和 canary 记录。

**影响服务**：测试基础设施、开发文档和 observation 记录；生产运行时无新增协议行为。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API / proto 变更。
- **数据库**：无新增迁移；v0.26.0 的 `085`–`088` 迁移与数据保持不变。
- **工具链**：构建和开发环境需要 Go 1.27。生产镜像应从本地 / CI 交叉构建后发布，资源受限服务器不执行镜像构建。
- **依赖**：sonic 等兼容依赖随 Go 1.27 目标升级；JSON `omitempty` 行为保持不变。
- **回滚**：可回滚到 v0.26.0 的服务镜像；无 schema down migration。若环境无法升级 Go 1.27，应继续使用上一版镜像，不要在服务器现场构建。
- **Relay 观察**：observation 文档更新不要求单独发布。观察期间不要构建、加载、重建或重启 `relay-gateway`，不要修改 `RELAY_ORCHESTRATOR_ENABLED` 或 allowlist。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.1
```

1. 确认本地 / CI 使用 Go 1.27，并按 `linux/amd64` 与项目既有流程交叉构建镜像。
2. 备份当前镜像和配置；按显式服务列表滚动更新 `admin-api`、`identity-service`、`channel-service`、`billing-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`。
3. 每个服务使用 `docker compose up -d --no-deps <service>`；不要使用宽泛 Compose 命令，也不要在服务器构建镜像。
4. 验证服务健康、内部 gRPC、JSON 序列化、middleware idempotency 和 usage/billing 关键指标。
5. executor observation 期间不部署 `relay-gateway`；如发生安全紧急情况需要例外重启，记录事件并按 observation 手册重新计算窗口。

## 验证

- `make test-unit`：完整非 E2E Go 单元测试和 API/config 生成检查。
- `go test -race ./internal/server/... ./domain/upstream/provider/... ./app/billing/... ./pkg/usage/...`：通过现代化后的关键并发 / 用量链路测试。
- Web lint、测试、`build:clean`：确认 Go 现代化未破坏管理端构建。
- `make migration-check`、`./scripts/check-architecture.sh`、`./scripts/check-deployment-docs.sh` 和 Markdown 链接检查：通过。
- 生产部署验证保留各服务镜像 digest、Go 版本、启动日志和 usage/billing 指标；不得因本版本重启 relay。

## 完整变更日志

- refactor: modernize Go codebase for Go 1.27 toolchain
- docs: align runtime and dependency references
- test: clean up Go 1.27 middleware goroutine
- docs(observation): record executor window restart
- docs(observation): record canary restart interruption
- docs(observation): record executor observation snapshot after restart
- docs(observation): record second canary day
- docs(observation): record first chat canary samples
- docs(observation): record manual executor review
