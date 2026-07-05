# Micro-One-API v0.4.0 / v0.5.0 联合发布公告

> 2026-07-05 · 上一版: [v0.3.1](./release-v0.3.1.md) (2026-06-29)

v0.4.0 与 v0.5.0 是 micro-one-api 自 v0.3.1 以来围绕「**订阅账号实现、套餐购买闭环与额度治理**」连续交付的两个 MINOR 版本。考虑两版主线一致,本次合并发布:先在 v0.4.0 落地订阅套餐、用户订阅、OAuth 绑定、订阅优先扣费、账号池 failover 与 Codex 配额快照,再在 v0.5.0 补齐订阅套餐购买发放、订阅账号本地额度/RPM/会话窗口、额度事件分析、多副本并发控制与发布链路修复。

本次合并范围覆盖 v0.3.1..v0.5.0 共 54 次提交、250 个文件、+27k/-2.2k 行,包含数据库迁移 `039-056`。升级前必须备份数据库,并按顺序执行迁移后再滚动发布服务。

## 亮点

- **订阅套餐购买闭环**:新增 `subscription_groups`、`subscription_plans`、`user_subscriptions` 等数据层,支持套餐配置、用户购买、支付宝支付成功后自动发放订阅权益,并阻止缺少发放配置的订单被误标记为已发放。
- **订阅优先扣费**:billing-service 新增订阅额度优先吸收、余额兜底、预留/提交/释放状态机、账本成本来源与订阅用量回填,小额请求不再因为精度不足被四舍五入为 0。
- **订阅账号 OAuth 绑定**:channel-service 新增 Claude/Codex `auth-url` 与 `exchange` 端点,管理后台提供授权码绑定入口,交换成功后自动创建 `subscription_accounts` 记录。
- **Relay 订阅账号调度**:Responses 路径支持 `previous_response_id` route 与 `session_hash` sticky,Chat/Anthropic/Responses 订阅账号路径新增 AccountPool、RuntimeBlocker、429/5xx failover、同账号重试、529 cooldown 和并发控制。
- **订阅账号额度治理**:订阅账号新增本地额度、5h quota、RPM、会话窗口限制、额度重置配置和批量额度管理,管理后台可查看账号额度状态、用户 RPM 限制与成本分析维度。
- **Codex 配额快照与自动暂停**:relay-gateway 可解析 Codex 5h/7d quota snapshot,写入 `account_quota_snapshots`,并在阈值耗尽时自动暂停订阅账号。
- **多副本并发控制**:relay-gateway 新增 Redis-backed 订阅账号并发限制器,多副本部署时共享账号并发槽位;Redis 不可用时回退到内存 limiter。
- **发布与安全链路修复**:Dockerfile 新增自包含 `go-deps` stage,修复 GitHub Actions buildx matrix 构建失败;gosec、govulncheck、gitleaks 扫描收敛到全绿,并临时 replace Kratos 到包含 CVE-2026-6993 修复的提交。

## 变更内容

### Added

#### 订阅套餐与用户订阅

- `migrations/039_create_user_subscriptions.sql`:新增用户订阅表,记录订阅权益、用量窗口、冻结额度与状态。
- `migrations/040_create_subscription_groups.sql`、`042_add_subscription_group_pricing.sql`:新增订阅分组与价格配置。
- `migrations/050_create_subscription_plans.sql`:新增订阅套餐表,支持独立管理可购买套餐。
- `internal/subscription/`:新增 group、plan、subscription usecase/repo,覆盖订阅套餐列表、购买发放、过期检查和额度校验。
- `web/src/pages/SubscriptionsPage.tsx`、`PurchasablePlansSection.tsx`:新增用户侧订阅页和可购买套餐组件。
- 管理后台新增订阅组、订阅用户、订阅账号、OAuth 绑定与套餐购买相关页面和测试。

#### 订阅扣费与账本

- `api/billing/v1/billing.proto`:预留/提交/账本接口新增订阅吸收、余额扣费、订阅 ID、订阅账号 ID 与成本来源字段。
- `internal/billing/biz/subscription_assigner.go`:支付成功后自动发放订阅权益。
- `internal/billing/biz/dual_track_test.go`:覆盖订阅优先扣费、混合扣费和余额兜底路径。
- `migrations/044_add_reservation_subscription_fields.sql`、`045_add_ledger_cost_source_and_dedupe_key.sql`:补齐预留和账本上的订阅扣费字段。
- `migrations/046_create_account_receivables.sql`:新增应收账款记录,为后续订阅账务对账留出落点。

#### Relay 订阅账号调度

- `internal/relay/server/response_route_scheduler.go`:Responses 多轮 route sticky,优先复用 `previous_response_id` 绑定渠道。
- `internal/relay/server/response_scheduler.go`:支持 `session_hash` / `sessionHash` body 字段和 `X-Session-Hash` / `OpenAI-Session-Hash` header。
- `internal/relay/biz/account_pool.go`、`runtime_blocker.go`:订阅账号池跳过运行时熔断账号,上游网络错误、`429`、`5xx`、`529` 可短 TTL 冷却后切换账号。
- `internal/relay/biz/account_concurrency.go`、`account_rpm.go`:账号级并发和 RPM limiter,支持 Redis-backed 多副本共享并发槽位。
- `internal/relay/server/subscription_session_window.go`:订阅账号会话窗口限制,避免单会话持续消耗同一账号。

#### OAuth 绑定与配额快照

- `internal/channel/biz/oauth/`:新增 Claude/Codex OAuth 授权码交换、5 分钟 session store、Codex 账号信息解析。
- `internal/channel/service/subscription_model_probe.go`:新增订阅账号模型探测。
- `internal/relay/quota/codex.go`:解析 Codex 5h/7d quota snapshot。
- `migrations/041_create_account_quota_snapshots.sql`:新增配额快照落库。
- `migrations/052_create_subscription_account_quota_events.sql`:新增订阅账号额度事件表,支持幂等记录和聚合分析。

#### 管理后台与可观测性

- 管理后台订阅账号页新增本地额度、5h quota、RPM、会话窗口、重置配置、批量额度管理、额度事件分析和用户 RPM 展示。
- Usage、Profile、Dashboard、Cost Analysis 等页面补充订阅用量、余额语义、订阅账号额度状态和成本分摊。
- `internal/pkg/metrics/subscription.go`:新增订阅额度检查、订阅用量回写、subscription adaptor 请求、failover、runtime block、quota snapshot、auto-pause、Redis 并发 fallback 等指标。

### Changed

- 钱包字段和页面文案从 quota 语义收敛到 balance/amount 语义,避免把余额金额误显示为模型额度。
- docker-compose 默认启用 relay session sticky 与订阅额度 enforcement 所需配置。
- Dockerfile 新增自包含 `go-deps` stage,CI buildx matrix 可独立构建单服务镜像;`Dockerfile.deps` 仍保留给部署脚本预热依赖缓存。
- `scripts/deploy.sh` 增加依赖镜像 hash tag、`DEPLOY_BUILD_PARALLEL` 并行构建开关、远端目录参数、数据库备份和 compose 文件保护。
- `scripts/test-e2e-flow.sh` 保留 compose 实时日志,启动健康检查竞态时等待并重试一次,默认降低并行度以减少本地构建 OOM 风险。

### Fixed

- 订阅套餐支付宝支付成功后未发放用户订阅的问题。
- 订阅扣费小额请求因 `DECIMAL(12,4)` 精度不足被四舍五入为 0 的问题,用量字段提升到 `DECIMAL(18,8)`。
- 已写入 subscription ledger 但 `user_subscriptions` 累计用量未对齐的问题,新增迁移按 committed ledger 回填 active subscription usage。
- 空 `ledger_dedupe_key` 造成唯一索引冲突的问题。
- MySQL 聚合查询误用 SQLite `strftime` 的问题。
- `phase1_indexes.sql` 重复创建 `billing_ledgers.idx_created_at`,导致干净 MySQL 初始化失败的问题。
- `previous_response_id` 解析拒绝 `msg_` message id,避免把 message id 误当 Responses route id。
- 订阅账号上游 `401` / `403` / `429` / `cyber_policy` 错误改为透传状态码、body 与 `Retry-After`,不再统一包装成网关错误。
- 订阅账号额度状态、用户 RPM 限制和批量额度管理在管理后台展示不完整的问题。
- GitHub Actions Docker buildx matrix 因尝试拉取不存在的 `micro-one-api/go-deps:latest` 而失败的问题。
- gosec G115/G101/G705 告警:新增 `internal/pkg/safecast` 安全转换工具,标注公开 OAuth endpoint 非凭据,idempotency replay 为响应缓存原样回放。
- gitleaks 历史占位符误报:文档示例改用 `${API_TOKEN}` / `${ADMIN_TOKEN}`,并为已确认 fingerprint 建立 `.gitleaksignore`。

### Security

- gosec SAST:0 issues。
- govulncheck SCA:0 vulnerabilities。
- gitleaks secret scan:0 leaks。
- 临时 replace `github.com/go-kratos/kratos/v2` 到包含 CVE-2026-6993 修复的 pseudo-version,规避 Kratos `http.DefaultServeMux` confused deputy 告警。
- 订阅账号凭据继续沿用 v0.3.0 的 At-Rest 加密与安全导入策略,发布稿和测试文档不再包含形似真实密钥的示例。

## 配置变化

### 新增或重点配置

| 配置 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `relay.subscription.enabled` | bool | `false` | 是否启用订阅额度 enforcement |
| `relay.session_sticky.enabled` | bool | `false` | 是否启用 session_hash / previous_response_id 粘滞 |
| `relay.subscription.runtime_block.*` | object | 内置短 TTL | 订阅账号 429/5xx/529/unauthorized 运行时冷却时间 |
| `relay.subscription.account_concurrency.*` | object | 内存 fallback | 订阅账号并发控制,配置 Redis 后支持多副本共享槽位 |
| `subscription_plans.*` | object | 可选 | 订阅套餐购买与发放配置 |
| `subscription_accounts.rpm_limit` | int | `0` | 单账号 RPM 限制,0 表示不限制 |
| `subscription_accounts.session_window_limit_usd` | decimal | `0` | 单会话窗口成本限制,0 表示不限制 |
| `subscription_accounts.quota_reset_*` | object | 可选 | 订阅账号本地额度/5h 额度重置配置 |

### 环境变量

- `DATABASE_DRIVER`:仍支持 `mysql`、`sqlite3`、`postgres`,默认 MySQL。
- `RELAY_SESSION_STICKY_ENABLED`:开启 relay session sticky。
- `RELAY_SUBSCRIPTION_ENABLED`:开启订阅额度 enforcement。
- `REDIS_ADDR`:启用 Redis sticky store、runtime blocker 和多副本账号并发控制时需要配置。
- `DEPLOY_BUILD_PARALLEL`:部署脚本并行构建开关。

完整字段以 `configs/relay-gateway.yaml`、`deployments/docker-compose/.env.example` 与管理后台配置页为准。

## 数据库迁移

从 v0.3.1 升级到 v0.5.0 需要执行 MySQL 迁移 `039-056`:

```bash
go run ./cmd/migrate -dir ./migrations
```

建议发布前先检查迁移状态:

```bash
go run ./cmd/migrate -dir ./migrations -status
```

关键迁移:

- `039-046`:订阅套餐/分组、用户订阅、账号 quota snapshot、订阅预留字段、账本成本来源、应收账款。
- `047`:回填空 `billing_ledgers.ledger_dedupe_key`,并修复历史 consume ledger 的成本来源。
- `048`:把 `user_subscriptions` 用量字段提升到 `DECIMAL(18,8)`。
- `049`:从 committed subscription ledgers 回填 active subscription usage。
- `050`:新增 `subscription_plans` 套餐表。
- `051-056`:新增订阅账号本地额度、quota events、5h quota、RPM、会话窗口限制和额度重置配置。

SQLite/Postgres baseline 已同步新 schema;存量非 MySQL 部署升级前请先在预发环境验证迁移路径。

## 升级指南

1. 备份数据库,重点关注 `billing_ledgers`、`billing_reservations`、`user_subscriptions`、`subscription_accounts`、`subscription_plans` 和 `subscription_account_quota_events`。
2. 执行 `go run ./cmd/migrate -dir ./migrations -status`,确认待执行迁移为 `039-056`。
3. 执行迁移: `go run ./cmd/migrate -dir ./migrations`。
4. 按依赖顺序滚动重启: `config-service` → `identity-service` → `channel-service` → `billing-service` → `relay-gateway` → `admin-api` → workers。
5. 发布后验证 `/healthz`、`/metrics`、订阅套餐购买、支付宝支付发放、订阅账号 OAuth 绑定、Responses sticky 多轮会话、账号 failover、账号额度/RPM 限制和账单用量展示。

### 兼容性

- HTTP 客户端协议保持向后兼容。`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、WebSocket、`/v1/embeddings`、`/v1/models` 原有行为不变。
- gRPC proto 新增字段保持默认值/optional 语义,旧客户端可继续工作。
- 数据库包含新增表和新增字段,回滚代码前需评估 v0.4.0/v0.5.0 期间写入的订阅订单、用户订阅、账本、quota events 和账号 quota 配置。
- Dockerfile 不再强依赖本地 `micro-one-api/go-deps:latest`;已有使用 `Dockerfile.deps` 的部署脚本仍可继续用其预热依赖缓存。

### 回滚

- 代码可回滚到 `v0.3.1` 镜像,但不建议直接删除 `039-056` 写入的数据。
- 如需回滚数据库,先导出 v0.4.0/v0.5.0 期间新增的订阅订单、用户订阅、账本和额度事件,再按业务影响评估是否保留新增表/字段。
- 若已启用订阅套餐购买,回滚前建议暂停支付回调和订阅账号自动发放,避免旧版本无法识别新订单状态。

## 验证

本次发版前执行或建议执行:

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

真实 provider e2e 依赖 `PROVIDER_API_KEY`;未配置时相关用例会跳过。compose E2E 依赖本机 Docker 环境和干净测试 volume,生产发版前建议至少在预发环境完整跑一轮订阅套餐购买、支付回调、订阅扣费与订阅账号 failover。

## 后续规划

详细路线图见 [订阅系统后续规划路线图](./subscription-follow-up-roadmap.md)。后续按独立分支推进:

- 订阅账号治理:完善账号 quota reset 自动化、异常账号自动恢复策略和额度事件告警。
- 订阅产品化:补齐套餐上下架、续费、退款/冲正、订阅变更和运营报表。
- Relay 稳定性:压测多副本 Redis 并发控制、session sticky 与上游 failover 组合场景。
- 文档:完善订阅账号 OAuth 绑定、套餐配置、额度治理和生产发布 runbook。
