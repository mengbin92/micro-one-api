# Issue #4 SQLite / Postgres 轻量化部署落地说明

## 背景

Issue: <https://github.com/mengbin92/micro-one-api/issues/4>

用户诉求是降低 Docker 部署复杂度，建议默认采用 SQLite，减少 MySQL 参数和依赖。Issue 正文为空，评论里确认了两个信息：

- 维护者回复：可以支持，但当前在做其它功能，后续会支持。
- 用户补充：Docker 部署后端参数比较复杂，建议默认 SQLite 最简化配置。

本轮实现已完成 SQLite3 轻量部署支持，并在方案基础上额外增加了 Postgres 支持。当前项目运行时支持三种数据库方言：

- `mysql`：保留原有生产部署路径。
- `sqlite3` / `sqlite`：用于单机轻量部署。
- `postgres` / `postgresql`：用于偏生产化的开源数据库部署。

## 结论

SQLite3 可以作为单机、自托管、低维护成本部署的默认推荐路径；MySQL 和 Postgres 适合更长期、并发更高或需要外部数据库运维能力的部署。

落地方式不是把现有 MySQL 迁移脚本改造成跨库 SQL，而是引入明确的数据库方言层：

- 运行时通过 `data.database.driver` / `DATABASE_DRIVER` 选择数据库方言。
- `cmd/migrate` 支持 `MIGRATIONS_DRIVER` / `-driver`，并按方言执行迁移目录。
- 提供 SQLite3 专用 `docker-compose.lite.yml`。
- 提供 Postgres 专用 `docker-compose.postgres.yml`。
- MySQL 专用分区维护在非 MySQL 方言下自动 no-op。
- 对 MySQL 专用查询函数补充 SQLite3 / Postgres 分支。

## 当前实现

### 数据库打开器

新增统一入口：

```go
// internal/pkg/xdb/db.go
func Open(cfg DatabaseConfig) (*gorm.DB, error)
func OpenSQL(driver, dsn string) (*sql.DB, error)
```

支持规则：

- `mysql` 使用 `gorm.io/driver/mysql`。
- `sqlite` / `sqlite3` 使用 `gorm.io/driver/sqlite` 和 `github.com/mattn/go-sqlite3`。
- `postgres` / `postgresql` 使用 `gorm.io/driver/postgres` 和 `pgx`。
- driver 为空时从 DSN 推断：
  - `file:`、`:memory:`、`.db`、`.sqlite`、`.sqlite3` 推断为 SQLite3。
  - `postgres://`、`postgresql://`、`host=...` 推断为 Postgres。
  - 其他情况保持 MySQL，兼容既有部署。

SQLite3 默认连接策略：

- `MaxOpenConns=1`
- `MaxIdleConns=1`
- 默认 PRAGMA：
  - `busy_timeout = 5000`
  - `journal_mode = WAL`
  - `foreign_keys = ON`
  - `synchronous = NORMAL`

### 配置透传

所有服务配置中的数据库 driver 已改为环境变量驱动：

```yaml
data:
  database:
    driver: ${DATABASE_DRIVER:-mysql}
    source: ${DATABASE_DSN}
```

涉及服务：

- `identity-service`
- `channel-service`
- `billing-service`
- `config-service`
- `log-service`
- `monitor-worker`
- `notify-worker`
- `admin-api`

各服务 `wire_gen.go` 已将 `cfg.Data.Database.Driver` 传入数据层构造函数。`admin-api` 的 system options repo 改为通过 `xdb.OpenSQL` 打开连接，并按方言选择占位符和 upsert 语法。

### 迁移

迁移目录按方言拆分：

```text
migrations/             # 既有 MySQL 迁移
migrations/sqlite/      # SQLite3 baseline
migrations/postgres/    # Postgres baseline
```

新增 baseline：

- `migrations/sqlite/000_create_full_schema.sql`
- `migrations/postgres/000_create_full_schema.sql`

`cmd/migrate` 支持：

```text
MIGRATIONS_DRIVER=mysql|sqlite3|postgres
MIGRATIONS_DSN=...
go run ./cmd/migrate -driver sqlite3 -dir ./migrations/sqlite
go run ./cmd/migrate -driver postgres -dir ./migrations/postgres
```

迁移 runner 已补充方言差异：

- MySQL：继续使用 `information_schema.tables` + `DATABASE()`。
- SQLite3：使用 `sqlite_master`。
- Postgres：使用 `information_schema.tables` + `current_schema()`。
- Postgres 占位符从 `?` 转换为 `$1`、`$2`。

### Docker 部署

新增轻量 SQLite3 部署：

```sh
cd deployments/docker-compose
cp .env.lite.example .env
docker compose -f docker-compose.lite.yml --env-file .env up -d
```

新增 Postgres 部署：

```sh
cd deployments/docker-compose
cp .env.postgres.example .env
docker compose -f docker-compose.postgres.yml --env-file .env up -d
```

主服务镜像已从 `scratch` 调整为 `alpine:3.20`，并使用 `CGO_ENABLED=1` 构建，以支持 `go-sqlite3`。迁移镜像 `Dockerfile.migrate` 会根据 `MIGRATIONS_DRIVER` 自动选择迁移目录：

- `sqlite3` -> `/migrations/sqlite`
- `postgres` / `postgresql` -> `/migrations/postgres`
- 其他 -> `/migrations`

### 查询兼容

已处理的数据库方言差异：

- `billing_ledgers.created_at` 按方言格式化：
  - SQLite3: `strftime(...)`
  - Postgres: `to_char(...)`
  - MySQL: `DATE_FORMAT(...)`
- `logs.created_at` 保存 Unix epoch 秒，按方言转换日期：
  - SQLite3: `strftime(..., 'unixepoch')`
  - Postgres: `to_char(to_timestamp(...))`
  - MySQL: `FROM_UNIXTIME(...)`
- MySQL 分区维护在 SQLite3 / Postgres 下 no-op。
- `system_options` upsert：
  - MySQL: `ON DUPLICATE KEY UPDATE`
  - SQLite3/Postgres: `ON CONFLICT ... DO UPDATE`

## 使用建议

### SQLite3

适合：

- 个人部署
- 单机自托管
- 低并发团队内部使用
- 希望减少 MySQL 配置和运维成本的场景

默认 DSN：

```text
file:/data/micro-one-api.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on
```

边界：

- 不适合多实例共享同一个数据库文件。
- 写并发能力弱于 MySQL/Postgres。
- 依赖 CGO，镜像必须使用 CGO-enabled build。

### Postgres

适合：

- 希望使用开源关系数据库但不想依赖 MySQL 的部署。
- 需要更强并发和外部数据库运维能力的部署。

默认 DSN 示例：

```text
host=postgres user=micro_one_api password=change-me dbname=micro_one_api port=5432 sslmode=disable
```

边界：

- 当前 Postgres 迁移是手写 baseline，后续 schema 变更需要同步维护。
- MySQL 分区能力不会在 Postgres 下启用。

### MySQL

适合：

- 既有部署
- 高并发生产环境
- 需要当前 MySQL 分区脚本的场景

MySQL 部署路径保持兼容，默认 `DATABASE_DRIVER` 为空时仍按 MySQL 处理。

## 验证

已执行的验证项：

```sh
go test ./internal/billing/data ./internal/log/data ./internal/pkg/migrate ./internal/pkg/xdb ./internal/admin/data ./internal/pkg/db
go test -tags manual_smoke ./internal/billing/data ./internal/log/data
CGO_ENABLED=1 go test ./internal/pkg/xdb -run TestOpenSQLite3InMemory -count=1
MIGRATIONS_DRIVER=sqlite3 MIGRATIONS_DSN='file:/tmp/micro-one-api-review-sqlite.db?_busy_timeout=5000&_foreign_keys=on' go run ./cmd/migrate -dir ./migrations/sqlite
```

新增手动 Postgres smoke 测试：

- `internal/billing/data/postgres_smoke_test.go`
- `internal/log/data/postgres_smoke_test.go`

运行方式：

```sh
PG_SMOKE=1 go test -tags manual_smoke ./internal/billing/data ./internal/log/data
```

这些测试默认跳过，需要本地有可用 Postgres 测试库。

## 后续维护规则

涉及 schema 变更时必须同步维护：

- `migrations/` 下的 MySQL 迁移。
- `migrations/sqlite/` 下的 SQLite3 迁移。
- `migrations/postgres/` 下的 Postgres 迁移。

涉及 SQL 查询时必须检查：

- 是否使用了 MySQL 专用函数，如 `DATE_FORMAT`、`FROM_UNIXTIME`、`ON DUPLICATE KEY UPDATE`。
- 是否需要 Postgres `$N` 占位符。
- 是否需要 SQLite3 专用日期函数或 PRAGMA。
- 是否依赖 MySQL 分区能力。

## 风险与处理

| 风险 | 影响 | 处理 |
| --- | --- | --- |
| SQLite3 写锁竞争 | 日志和账务写入可能阻塞 | WAL、busy timeout、单连接池、批量写入控制 |
| 迁移漂移 | 三种数据库 schema 不一致 | 方言目录并行维护，新增迁移时同步提交 |
| MySQL 专用 SQL 泄漏 | SQLite3/Postgres 运行时报错 | 查询层按 `Dialector.Name()` 分支 |
| 分区维护误启 | 非 MySQL 部署报错 | 非 MySQL 方言下 partition manager no-op |
| CGO 依赖 | SQLite3 在 `CGO_ENABLED=0` 下不可用 | 主镜像使用 Alpine + CGO build |
| Postgres 时间类型差异 | `time.Time` 扫描/写入失败 | Postgres baseline 中对应字段使用 `TIMESTAMPTZ` |

## Issue 回复建议

可以回复：

> 已支持。当前实现新增了 SQLite3 Lite 部署，同时额外支持 Postgres。SQLite3 部署可通过 `deployments/docker-compose/docker-compose.lite.yml` 启动，不再需要 MySQL 容器；Postgres 可通过 `docker-compose.postgres.yml` 启动。运行时通过 `DATABASE_DRIVER` 选择 `mysql`、`sqlite3` 或 `postgres`，迁移目录也已按方言拆分。
