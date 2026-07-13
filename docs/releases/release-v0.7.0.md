# Micro-One-API v0.7.0 发布公告

> 2026-07-12 · 上一版: v0.6.1 (2026-07-09)

v0.7.0 是 v0.6.1 之后的 **Kratos 大仓结构迁移**版本。范围覆盖 `v0.6.1..v0.7.0` 共 9 次提交、560 个文件、+8.6k/-3.2k 行。

本版为**纯结构性重构**，不涉及 API 破坏性变更，**不新增数据库迁移**。现有部署的升级路径为重建镜像并滚动重启，无需执行 SQL 迁移。

## 亮点

- **Kratos 官方大仓结构对齐**：relay-gateway 保留为根项目（`cmd/relay-gateway/` + `internal/`），其余 8 个服务迁移到 `app/<service>/cmd/<service>/` + `app/<service>/internal/`，全部共用根 `go.mod`，与 `kratos-layout` 模板和 `kratos new app/<service> --nomod` CLI 约定一致。
- **基础设施分层**：原 `internal/pkg/` 提取为 `platform/`（15 个基础设施包）和 `pkg/`（7 个纯工具包），明确区分基础设施层与业务代码。
- **共享域库**：新增 `domain/subscription` 和 `domain/upstream` 共享域库，admin/billing/relay 直接嵌入订阅域 biz+data 代码，走模块化单体（modular-monolith）边界而非额外网络调用。
- **架构边界守卫**：新增 `scripts/check-architecture.sh`，7 条层级依赖规则 + wireinject 编译检查，集成到 CI，防止层间违规依赖。
- **配置布局重构**：各服务独立 `configs/config.yaml`，Dockerfile 设 `ENV CONF_PATH=/configs/config.yaml`，部署清单不再依赖根目录多配置文件。
- **前端 502/404 修复**：修正 SubscriptionPlansPage API 路径前缀和路由注册顺序。

## 变更内容

### Added

- `app/` 大仓结构：8 个子服务（admin/billing/channel/config/identity/log/monitor/notify）各含独立 `cmd/`、`internal/`、`configs/config.yaml`、`Dockerfile` 和 `Makefile`。
- `platform/` 基础设施层：15 个包（audit/cache/config/database/events/grpc/http/logging/metrics/middleware/registry/security/tls/tracing/websocket）。
- `domain/` 共享域库：`domain/subscription`（含 `README.md` 边界说明）和 `domain/upstream`。
- `scripts/check-architecture.sh`：架构边界守卫，7 条层间依赖规则 + wireinject 编译检查。
- `AGENTS.md`：仓库级编码规约，DTO/DO/PO 三层模型与依赖箭头规则。
- per-service `Makefile` 和 root Makefile `wire` / `wire-check` target。
- admin `biz` 层：`SystemOption` DO、`SystemOptionsRepo` interface、`SystemOptionsUsecase`，符合 DTO→DO→PO 分层。

### Changed

- **大仓结构迁移**：relay-gateway 保留为根 `cmd/relay-gateway/` + `internal/`；8 个子服务迁移到 `app/<service>/cmd/<service>/` + `app/<service>/internal/{biz,data,server,service}`。
- **配置布局**：原 `configs/<service>.yaml` 删除，改为 `app/<service>/configs/config.yaml`；relay-gateway 使用根 `configs/config.yaml`。
- **Dockerfile 拆分**：根 Dockerfile 保留用于 relay-gateway，新增 8 个 `app/*/Dockerfile`。
- `api/relay` → `api/relay-gateway`；`internal/config` → `internal/conf`。
- 导入路径更新：234 处 Go import 跨 132 文件更新。
- `make wire` 全量重生成 9 个 `wire_gen.go`，完全可复现。
- CI 工作流更新：Docker matrix 使用 `include` + path，新增架构检查和生成文件新鲜度验证 step。

### Fixed

- 前端 web API 502/404：`SubscriptionPlansPage.tsx` 移除冗余 `/api` 前缀；`http.go` 路由顺序修正；新增 `/admin/subscription-plans` SPA 路由。
- `go vet` lock-copy 告警：proto message 含 `sync.Mutex` 改用指针。
- `go vet` "using resp before checking for errors" 告警。
- relay-gateway `wire.go`：`newApp()` 返回 error，mTLS 改为 fail-closed。
- admin `http_test.go`：适配 `SystemOptionsStore` → `SystemOptionsRepo + SystemOptionsUsecase` 重构。
- Docker Compose 新增 `SSL_CERT_FILE` 和 `ca-certificates` 卷挂载，修复容器内上游 HTTPS 调用。

## 目录结构变化

```
v0.6.1 之前                          v0.7.0
─────────────────────────────────────────────────────────────
cmd/admin-api/                  →   app/admin/cmd/admin/
cmd/billing-service/            →   app/billing/cmd/billing/
cmd/channel-service/            →   app/channel/cmd/channel/
cmd/config-service/             →   app/config/cmd/config/
cmd/identity-service/           →   app/identity/cmd/identity/
cmd/log-service/                →   app/log/cmd/log/
cmd/monitor-worker/             →   app/monitor/cmd/monitor/
cmd/notify-worker/              →   app/notify/cmd/notify/
cmd/relay-gateway/               →   cmd/relay-gateway/ (保留在根)

internal/admin/                  →   app/admin/internal/
internal/billing/                →   app/billing/internal/
internal/channel/                →   app/channel/internal/
internal/config/                 →   app/config/internal/
internal/identity/               →   app/identity/internal/
internal/log/                    →   app/log/internal/
internal/monitor/                →   app/monitor/internal/
internal/notify/                 →   app/notify/internal/
internal/config (conf proto)     →   internal/conf/

internal/pkg/                    →   platform/ (基础设施) + pkg/ (纯工具)
internal/subscription/           →   domain/subscription/
internal/relay/provider + credential → domain/upstream/

api/relay/                       →   api/relay-gateway/

configs/<service>.yaml           →   app/<service>/configs/config.yaml
```

## 数据库迁移

**无新增迁移。** v0.7.0 不涉及 schema 变更。

## 升级指南

本版为纯结构性重构，升级步骤：

1. **拉取新代码**：`git pull origin develop` 或 checkout v0.7.0 tag。
2. **重建镜像**：各服务 Dockerfile 路径已变更，需重建所有服务镜像。
   ```bash
   # relay-gateway（根 Dockerfile）
   docker build -t micro-one-api/relay-gateway:v0.7.0 -f Dockerfile .

   # 子服务（per-service Dockerfile）
   docker build -t micro-one-api/admin:v0.7.0 -f app/admin/Dockerfile app/admin/
   docker build -t micro-one-api/identity:v0.7.0 -f app/identity/Dockerfile app/identity/
   # ... 其余服务同理
   ```
3. **更新部署清单**：docker-compose / k8s manifest 中的 `SERVICE_PATH` 和构建上下文路径已更新，使用仓库内最新版本。
4. **配置文件路径**：容器内配置路径为 `/configs/config.yaml`（`ENV CONF_PATH=/configs/config.yaml`），各服务 Dockerfile 已 COPY 对应配置。
5. **滚动重启**：按 identity → channel → billing → admin → relay → log/monitor/notify 顺序滚动重启。
6. **验证**：健康检查、API 请求、管理后台页面、订阅套餐管理页（特别验证 502/404 修复）。

### 从源码本地运行

```bash
# 重新生成 Wire 和 Proto
make wire
make proto

# 构建
make build

# 单元测试
make test-unit

# 前端测试
cd web && npm test
```

## 验证

本次发版前已执行：

```bash
go build ./...           # 通过
go vet ./...             # 无告警
make wire                # 9 个 wire_gen.go 重生成，无 diff
make api                 # Proto 生成，工作树 clean
make config              # 内部 Proto 生成
make test-unit           # 全部 PASS
cd web && npm test       # 72/72 通过
./scripts/check-architecture.sh  # 架构边界 0 违规
make wire-check          # wireinject 编译通过
gosec ./...              # 0 HIGH/CRITICAL/MEDIUM
go mod verify            # 模块验证通过
```

## 破坏性变更

- **Docker 构建路径变化**：各子服务从根 Dockerfile 统一构建改为 per-service Dockerfile 独立构建。CI 和部署脚本已更新，自定义构建流水线需同步调整。
- **配置文件路径变化**：原 `configs/<service>.yaml` 已删除，改为 `app/<service>/configs/config.yaml`。
- **Go import 路径变化**：234 处 import 路径更新。如果有外部代码引用本仓库的 internal 路径，需同步更新（但 internal 路径本身不保证外部兼容性）。

以上变更均不影响运行时 API 行为和数据库 schema。
