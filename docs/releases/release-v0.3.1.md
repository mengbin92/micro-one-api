# Micro-One-API v0.3.1 发布公告

> 2026-06-29 · 上一版: [v0.3.0](./release-v0.3.0.md) (2026-06-29)

v0.3.1 是 micro-one-api 自 v0.3.0 以来的首个 PATCH 版本,落地 Issue #4 提出的「轻量化部署」诉求,把数据库方言从单一的 MySQL 扩展为 MySQL / SQLite3 / Postgres 三选一,同时修复了 SQLite/Postgres 路径上 4 个 P0/P1 缺陷。无 proto 变更,无破坏性 API 变更,升级时只需替换镜像 + 重建容器即可。

## 亮点

- **SQLite3 Lite 部署**:新增 docker-compose.lite.yml、.env.lite.example、migrations/sqlite/000_create_full_schema.sql,单机部署可不再启动 MySQL 容器。
- **Postgres 部署**:新增 docker-compose.postgres.yml、.env.postgres.example、migrations/postgres/000_create_full_schema.sql。
- **统一数据库打开器**:internal/pkg/xdb.Open / OpenSQL / WithPool 按 Driver 字段分派到 mysql / sqlite3 / postgres 三种方言,支持从 DSN 自动推断 driver。
- **migrate runner 升级**:cmd/migrate 新增 -driver flag 与 MIGRATIONS_DRIVER / SQL_DRIVER 环境变量;Postgres 路径下把 ? 占位符改写为 $N,复用现有 .sql 文件。
- **Docker 镜像修复**:主服务 Dockerfile 切换为 CGO-enabled Alpine 构建,带 sqlite-libs 运行时依赖,mattn/go-sqlite3 不再因为 CGO 缺失而无法在 Lite 部署中运行。
- **Postgres baseline 修正**:billing_ledgers / payment_orders 等表的 created_at / updated_at / expired_at 改为 TIMESTAMPTZ,payment_orders.user_id 改为 TEXT,与 GORM 模型对齐。

## 变更内容

### Added

#### SQLite3 Lite 部署
- deployments/docker-compose/docker-compose.lite.yml:单文件 data/micro-one-api.db 即可启动全套后端,带 migrate 一次性 init 容器。
- deployments/docker-compose/.env.lite.example:Lite 部署环境变量模板,只暴露 JWT_SECRET_KEY / SERVICE_TOKEN / ADMIN_TOKEN / REDIS_PASSWORD 四个必填项。
- migrations/sqlite/000_create_full_schema.sql:手写 SQLite3 baseline,覆盖 19 张表(用户、令牌、渠道、订阅账号、订单、账本、日志、系统选项等)。
- migrations/sqlite/README.md:解释 snapshot-vs-split 决策、db 版本与 MySQL 迁移的同步期望。

#### Postgres 部署
- deployments/docker-compose/docker-compose.postgres.yml:同 docker-compose.yml 结构,数据库换成 postgres:16-alpine。
- deployments/docker-compose/.env.postgres.example:Postgres 部署环境变量模板。
- migrations/postgres/000_create_full_schema.sql:手写 Postgres baseline,使用 BIGSERIAL / TEXT / BOOLEAN / TIMESTAMPTZ。
- migrations/postgres/README.md:Postgres 方言特有的索引、约束、序列说明。

#### 数据库抽象层
- internal/pkg/xdb/db.go:Open / OpenSQL / WithPool 三个入口,按 Driver 字段分派到 database/sql.Open。
- NormalizeDriver(raw string) string:归一化 mysql / mariadb / sqlite3 / sqlite / postgres / postgresql 等别名。
- InferDriver(dsn string) string:从 DSN 推断 driver。
- internal/pkg/xdb/db_test.go:9 个用例覆盖 NormalizeDriver / InferDriver / Open round-trip / 自定义 pragmas / 未知 driver 错误 / PostgresDriverName 常量 / pgx 注册。

#### Partition manager
- internal/pkg/db/partition.go:NewPartitionManager 检查 db.Dialector.Name(),非 MySQL 方言下 Supported=false。
- PartitionMaintenance / PartitionMaintenanceForTable:!Supported 时直接返回 nil,cron ticker 继续 tick 但不做实际工作。
- internal/pkg/db/partition_test.go:新增 TestPartitionManagerNonMySQLNoop。

#### Documentation
- docs/design/issue-4-sqlite-solution.md:Issue #4 落地说明。
- docs/deployment.md §5/§6:新增 Lite / Postgres 部署小节。

### Changed

- **服务配置**:7 个服务的 configs/<svc>.yaml 中 data.database.driver 字段成为一等公民,默认 mysql;cmd/<svc>/wire_gen.go 把 cfg.Data.Database.Driver 显式传入 data.NewRepositoryFromEnv。
- **cmd/migrate**:参数解析时新增 -driver flag,main.go 把 MIGRATIONS_DRIVER / SQL_DRIVER 注入到 migrate.NewWithDriver。
- **主服务 Dockerfile**:构建阶段从 CGO_ENABLED=0 + FROM scratch 改为 CGO_ENABLED=1 + FROM alpine:3.20,apk add build-base sqlite-libs。
- **MySQL 分区维护**:在 SQLite/Postgres 下自动 no-op,不影响 cron 心跳与 Prometheus 指标上报。

### Fixed

- **admin-api system options 跨方言兼容**:internal/admin/data/system_options.go 新增 driver-aware Set / Get,MySQL 走 ON DUPLICATE KEY UPDATE,SQLite/Postgres 走 ON CONFLICT ... DO UPDATE。
- **billing/log 聚合查询 MySQL 专用函数**:internal/billing/biz 与 internal/log/biz 中 DATE_FORMAT / FROM_UNIXTIME 等被替换为方言无关表达式。
- **Postgres baseline time.Time 类型不一致**:migrations/postgres/000_create_full_schema.sql 中 billing_ledgers / payment_orders 的 created_at / updated_at / expired_at 由 BIGINT 改为 TIMESTAMPTZ,payment_orders.user_id 由 BIGINT 改为 TEXT。

## 配置变化

### 新增配置项

所有服务的 data.database 块新增 driver 字段(默认 mysql,可被 DATABASE_DRIVER 覆盖):

| Driver | 适用场景 | 备注 |
| --- | --- | --- |
| mysql | 原生产路径 | 默认,行为不变 |
| sqlite3 | 单机自托管 / Lite 部署 | 需要 mattn/go-sqlite3 (CGO-enabled) |
| postgres | 偏生产化的开源数据库 | 需要 github.com/jackc/pgx/v5 |

### 新增环境变量

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| DATABASE_DRIVER | 否 | mysql / sqlite3 / postgres,默认 mysql |
| MIGRATIONS_DRIVER | migrate 容器必填 | 决定 migrate -dir 指向 migrations/{sqlite,postgres} 还是 migrations |
| SQL_DRIVER | 否 | migrate 进程的兼容别名 |

## 升级指南

### 现有 MySQL 部署

无需任何动作,本次升级完全向后兼容。DATABASE_DRIVER 不设置时仍走 MySQL 路径。

### 切换到 Lite 部署(新部署)

1. 准备目录:`mkdir -p data && cp deployments/docker-compose/.env.lite.example .env`,编辑四个必填密钥。
2. 启动:`cd deployments/docker-compose && docker compose -f docker-compose.lite.yml --env-file ../../.env up -d`。
3. `migrate` 容器会自动跑 migrations/sqlite/000_create_full_schema.sql 创建 19 张表。
4. 验证:访问 `http://<host>:3000` 进入管理后台,登录后查看「系统选项」是否正常显示。

### 切换到 Postgres 部署(新部署)

1. 准备目录:`cp deployments/docker-compose/.env.postgres.example .env`。
2. 启动:`docker compose -f docker-compose.postgres.yml --env-file ../../.env up -d`。
3. `migrate` 容器跑 migrations/postgres/000_create_full_schema.sql。
4. 验证:用 `psql` 连 Postgres,`\dt` 应能看到 19 张表。

### 升级镜像(任意部署)

1. 拉取 v0.3.1 镜像并按依赖顺序重启:`config-service` → `identity-service` → `channel-service` → `billing-service` → `log-service` → `relay-gateway` → `admin-api` → workers。
2. 验证 `/healthz` 与 `/metrics` 正常;重点观察 admin-api system options 是否能正常 Set/Get。
3. 主服务 Dockerfile 切到 CGO-enabled Alpine,镜像体积会比 v0.3.0 的 scratch 版本大约 20MB。

### 兼容性

- HTTP 客户端协议:完全向后兼容。`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、WebSocket、`/v1/embeddings`、`/v1/models` 行为不变。
- gRPC 客户端:proto 字段保持兼容,无新增/废弃 RPC。
- 数据库:MySQL baseline 不变;SQLite / Postgres 是新增路径,旧数据不迁移。
- 配置文件:`data.database.driver` 字段为可选,不填走 MySQL。

### 回滚

- 代码:回滚到 v0.3.0 镜像(纯 scratch + CGO_ENABLED=0)。
- 配置:删除 DATABASE_DRIVER 环境变量即回到 MySQL 默认。

## Security

- **gosec SAST**:本次新增代码 0 issues。
- **govulncheck SCA**:xdb / pgx/v5 / mattn/go-sqlite3 均无已知漏洞。
- **gitleaks 密钥扫描**:本次新增代码 0 leaks。

## 后续规划

- v0.3.2:`docs/migration/grpc-gateway-migration-todo.md` P0 服务(config/log/monitor/notify)迁 grpc-gateway runtime mux。
- v0.4.0:`ARCHITECTURE_REFACTOR.md` §3 目标架构剩余项 —— 事件总线由 MemoryEventBus 升级到 Redis Streams、按 schema 拆库、配置热更新(consul/etcd watch),多架构 Docker 镜像。
