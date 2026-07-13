# Micro-One-API v0.4.0 发布公告

> 2026-07-04 · 上一版: [v0.3.1](./release-v0.3.1.md) (2026-06-29)

v0.4.0 是订阅系统与 Relay 订阅账号调度增强版本,重点补齐订阅套餐购买/发放/扣费闭环、订阅账号 OAuth 绑定、Responses sticky 调度、账号池运行时熔断与 Codex 配额快照。包含数据库迁移 `039-049`,需要先备份数据库并按顺序执行迁移后再滚动发布服务。

## 亮点

- **订阅套餐闭环**:新增订阅套餐、用户订阅、支付宝支付后自动发放订阅,并支持订阅额度优先扣减、余额兜底和账本对账。
- **Relay 订阅账号调度**:Responses 路径支持 `previous_response_id` 和 `session_hash` sticky,订阅账号池支持运行时熔断、429/5xx failover、同账号重试与并发控制。
- **订阅账号 OAuth 绑定**:管理端新增 Claude/Codex 授权码绑定入口,channel-service 负责生成授权链接并交换凭据。
- **Codex 配额快照**:relay-gateway 可解析 Codex 5h/7d quota snapshot,写入 `account_quota_snapshots`,并在阈值耗尽时自动暂停订阅账号。
- **订阅可观测性**:新增订阅额度检查、订阅用量回写、subscription adaptor、failover、runtime block 和上游错误透传相关 Prometheus 指标。

## 变更内容

### Added

- `user_subscriptions`、`subscription_groups`、`account_quota_snapshots`、`account_receivables` 等订阅相关表与数据层。
- 管理后台新增订阅套餐、用户订阅、订阅账号 OAuth 绑定与用户侧订阅页。
- relay-gateway 新增 Responses route scheduler、session sticky scheduler、subscription middleware、account pool、runtime blocker 和 Codex quota parser。
- channel-service 新增订阅账号 OAuth `auth-url` / `exchange` 端点与订阅模型探测。
- billing-service 新增订阅优先扣费、预留/提交/释放 CAS 状态机、订阅用量回填和应收账款记录。

### Changed

- 钱包字段和界面文案从 quota 语义收敛到 balance/amount 语义,避免把余额金额误显示为额度。
- docker-compose 默认开启 relay session sticky 与订阅额度 enforcement 所需配置。
- Usage 页面和订阅进度组件展示订阅用量、账号额度状态与更高精度的订阅成本。
- 部署脚本增加远端目录参数、数据库备份和服务 compose 文件保护。

### Fixed

- 订阅套餐支付宝支付成功后未发放用户订阅的问题。
- 订阅扣费中小额请求因为 `DECIMAL(12,4)` 精度不足被四舍五入为 0 的问题。
- 订阅用量账本已写入但订阅累计值未对齐的问题,新增迁移按 committed ledger 回填 active subscription usage。
- 空 `ledger_dedupe_key` 造成唯一索引冲突的问题。
- MySQL 聚合查询误用 SQLite `strftime` 的问题。
- 订阅账号额度状态在管理后台展示不完整的问题。
- `phase1_indexes.sql` 重复创建 `billing_ledgers.idx_created_at` 导致干净 MySQL 初始化失败的问题。

## 数据库迁移

从 v0.3.1 升级到 v0.4.0 需要执行 MySQL 迁移:

```bash
go run ./cmd/migrate -dir ./migrations
```

关键迁移:

- `039-046`:订阅套餐、用户订阅、账号 quota snapshot、订阅预留字段、账本成本来源和应收账款。
- `047`:回填空 `billing_ledgers.ledger_dedupe_key`,并修复历史 consume ledger 的成本来源。
- `048`:把 `user_subscriptions` 用量字段提升到 `DECIMAL(18,8)`。
- `049`:从 committed subscription ledgers 回填 active subscription usage。

SQLite/Postgres 当前以 baseline 文件支持新部署;本次增量迁移主要覆盖默认 MySQL 部署。使用 SQLite/Postgres 存量部署时,请先在预发环境验证对应 schema 变更。

## 升级指南

1. 备份 MySQL 数据库,确认 `billing_ledgers`、`billing_reservations`、`user_subscriptions` 可回滚。
2. 发布前先执行 `go run ./cmd/migrate -dir ./migrations -status` 查看待执行迁移。
3. 按依赖顺序滚动重启:`config-service` → `identity-service` → `channel-service` → `billing-service` → `relay-gateway` → `admin-api` → workers。
4. 发布后验证 `/healthz`、`/metrics`、订阅套餐购买、订阅账号 OAuth 绑定、Responses sticky 多轮会话和账单用量展示。

## 兼容性

- HTTP API 保持向后兼容,新增订阅与 OAuth 管理接口不影响现有 chat/messages/responses 路径。
- gRPC proto 新增字段保持 optional/默认值兼容,旧客户端可继续工作。
- 数据库包含新增表和新增字段,回滚代码前需评估是否保留 v0.4.0 期间写入的订阅账单数据。

## 验证

本次发版前建议执行:

```bash
make test
cd web && npm run lint && npm test && npm run build
git diff --check
./scripts/test-e2e-flow.sh --suite
```

compose E2E 依赖本机 Docker 环境和干净测试 volume;生产发版前建议至少在预发环境完整跑一轮。
