# Micro-One-API v0.5.0 发布公告

> 2026-07-05 · 上一版: [v0.4.0](./release-v0.4.0.md) (2026-07-04)

v0.5.0 是订阅套餐与订阅账号额度治理增强版本,在 v0.4.0 的订阅系统基础上补齐套餐购买发放、账号本地额度/RPM/会话窗口控制、额度事件分析与多副本并发控制。同时修复 GitHub Actions Docker buildx matrix 构建失败,并将 gosec / govulncheck / gitleaks 安全扫描收敛到全绿。包含数据库迁移 `050-056`,升级前请先备份数据库并按顺序执行迁移。

## 亮点

- **订阅套餐购买闭环**:新增 `subscription_plans` 数据层与套餐购买流,支付成功后可自动分配订阅权益。
- **订阅账号额度治理**:订阅账号支持本地额度、5h quota、RPM、会话窗口、额度重置配置和批量额度管理。
- **额度事件分析**:额度事件写入具备幂等性,管理后台可查看订阅账号额度事件和聚合状态。
- **跨副本并发控制**:relay-gateway 新增 Redis-backed account concurrency limiter,多副本部署时共享订阅账号并发槽位。
- **发布链路修复**:Dockerfile 内置 `go-deps` stage,CI 不再依赖预先存在的本地依赖镜像;E2E 脚本对 compose 健康检查竞态和本地资源压力更稳。
- **安全扫描全绿**:修复 gosec 窄化转换、硬编码凭据误报和 idempotency replay 告警;处理 gitleaks 历史占位符命中。
- **依赖安全修复**:临时 replace Kratos 到包含 CVE-2026-6993 修复的提交,规避 `http.DefaultServeMux` confused deputy 告警。

## 变更内容

### Added

- `subscription_plans` 表与订阅套餐 repo/usecase,支持套餐列表、购买和支付后分配。
- 订阅账号新增本地额度、5h 额度、RPM 限制、会话窗口限制和 quota reset 配置字段。
- 订阅账号 quota events 表与幂等事件记录,支持额度事件聚合分析。
- relay-gateway 新增 Redis 订阅账号并发限制器,并保留内存 fallback。
- 管理后台订阅账号页新增额度批量管理、RPM/窗口/重置配置、事件分析和用户 RPM 展示。
- Prometheus 新增订阅账号并发 fallback 相关指标。

### Changed

- Dockerfile 新增自包含 `go-deps` stage,buildx 单服务 matrix 可独立构建;`Dockerfile.deps` 仍可由部署脚本用于预热依赖缓存。
- `scripts/deploy.sh` 增加依赖镜像 hash tag、`DEPLOY_BUILD_PARALLEL` 并行构建开关和更稳的构建顺序。
- `scripts/test-e2e-flow.sh` 改为实时输出 compose 日志,启动失败后短暂等待并重试一次;默认限制 compose 并行度以降低本地 CGO 静态编译 OOM 风险。
- 管理后台成本分析与订阅账号页面补充账号额度、RPM 和状态展示。

### Fixed

- 修复 GitHub Actions Docker Build matrix 因 `COPY --from=micro-one-api/go-deps:latest` 触发 Docker Hub 拉取而失败的问题。
- 修复 gosec G115 窄化转换告警,新增 `internal/pkg/safecast` 安全转换/饱和转换工具并应用到 selector、cache、batch writer、websocket、Redis concurrency 等路径。
- 修复 gosec G101/G705 告警:公开 OAuth endpoint 明确标注非凭据,Claude public client id 避免被误判为密钥,idempotency middleware 明确为缓存响应原样回放。
- 修复 gitleaks 对文档 `Bearer YOUR_TOKEN` / 测试 token 示例和公开 OAuth client id 的历史误报:当前文档示例改用 `${API_TOKEN}` / `${ADMIN_TOKEN}`,并对已确认历史 fingerprint 建立 `.gitleaksignore`。
- 修复订阅账号额度状态、用户 RPM 限制和批量额度管理在管理后台展示不完整的问题。
- 修复 Dependabot GHSA-jj45-xvq5-rhh9 / CVE-2026-6993 告警:在 Kratos 官方新 tag 发布前,通过 `replace` 使用包含修复补丁的 Kratos pseudo-version。

## 数据库迁移

从 v0.4.0 升级到 v0.5.0 需要执行 MySQL 迁移:

```bash
go run ./cmd/migrate -dir ./migrations
```

关键迁移:

- `050_create_subscription_plans.sql`:新增订阅套餐表。
- `051_add_subscription_account_local_quota.sql`:新增订阅账号本地额度字段。
- `052_create_subscription_account_quota_events.sql`:新增订阅账号额度事件表。
- `053_add_subscription_account_5h_quota.sql`:新增 5h quota 字段。
- `054_add_subscription_account_rpm_limit.sql`:新增账号 RPM 限制字段。
- `055_add_subscription_account_session_window_limit.sql`:新增会话窗口限制字段。
- `056_add_subscription_account_quota_reset_config.sql`:新增额度重置配置字段。

SQLite/Postgres baseline 已同步新 schema;存量非 MySQL 部署升级前请先在预发环境验证迁移路径。

## 升级指南

1. 备份数据库,重点关注 `subscription_accounts`、`subscription_plans`、`subscription_account_quota_events`、`user_subscriptions` 和账本相关表。
2. 执行 `go run ./cmd/migrate -dir ./migrations -status` 检查待执行迁移。
3. 执行迁移后滚动重启服务:`config-service` → `identity-service` → `channel-service` → `billing-service` → `relay-gateway` → `admin-api` → workers。
4. 发布后验证订阅套餐购买、订阅账号批量额度管理、RPM 限制、Responses/Chat relay、账本扣费和管理后台订阅账号页。

## 兼容性

- HTTP/gRPC API 保持向后兼容,新增字段均有默认值或 optional 语义。
- 数据库包含新增表和新增字段,回滚代码前需评估 v0.5.0 期间写入的套餐、额度事件和账号 quota 配置。
- Dockerfile 不再强依赖本地 `micro-one-api/go-deps:latest`;已有使用 `Dockerfile.deps` 的部署脚本仍可继续用其预热依赖缓存。

## 验证

本次发版前已执行:

```bash
make test-unit
cd web && npm test && npm run lint
make build
/Users/neo/go/bin/gosec -exclude-generated -exclude=G104 -exclude-dir=web/node_modules ./...
/Users/neo/go/bin/govulncheck ./...
/Users/neo/go/bin/gitleaks detect --source .
docker build --build-arg SERVICE_NAME=relay-gateway -f deployments/docker/Dockerfile -t micro-one-api/relay-gateway:security-fix .
docker buildx build --build-arg SERVICE_NAME=relay-gateway --file deployments/docker/Dockerfile --platform linux/amd64 --tag micro-one-api/relay-gateway:ci .
make test-e2e-suite
git diff --check
```

真实 provider e2e 仍依赖 `PROVIDER_API_KEY`;未配置时相关用例会跳过。
