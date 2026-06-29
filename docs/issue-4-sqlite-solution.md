# Issue #4 SQLite 轻量化部署方案

## 背景

Issue: <https://github.com/mengbin92/micro-one-api/issues/4>

用户诉求是降低 Docker 部署复杂度，建议默认采用 SQLite，减少 MySQL 参数和依赖。Issue 正文为空，评论里确认了两个信息：

- 维护者回复：可以支持，但当前在做其它功能，后续会支持。
- 用户补充：Docker 部署后端参数比较复杂，建议默认 SQLite 最简化配置。

## 结论

可以支持 SQLite，并且适合作为单机、轻量、自托管部署的默认数据库；MySQL 仍应保留为生产、高并发、多实例部署的推荐数据库。

推荐方案不是把现有 MySQL 迁移脚本直接兼容 SQLite，而是引入明确的数据库方言层：

- 运行时通过 `data.database.driver` 或环境变量选择 `mysql` / `sqlite`。
- 迁移工具支持 `-driver` 和 `-dir`，分别执行 MySQL 与 SQLite 迁移集。
- 提供 `docker-compose.lite.yml`，默认使用 SQLite 文件卷，移除 MySQL 服务和 MySQL 必填参数。
- 分区、MySQL 专用索引、MySQL 专用 DDL 在 SQLite 模式下禁用或替换为 SQLite 兼容实现。

## 仓库现状

### 已具备的基础

`go.mod` 已经包含 SQLite 相关依赖：

- `github.com/mattn/go-sqlite3`
- `gorm.io/driver/sqlite`

多处单元测试也已经用 SQLite 作为内存数据库，例如：

- `internal/pkg/migrate/runner_test.go`
- `internal/pkg/db/partition_test.go`
- `internal/billing/data/*_test.go`
- `internal/channel/data/data_test.go`

这说明业务仓储中已有不少查询可以在 SQLite 上运行。

### 当前阻塞点

1. 生产数据库入口固定为 MySQL

   `internal/pkg/xdb/mysql.go` 只提供 `OpenMySQL`。`identity`、`channel`、`billing`、`log`、`monitor` 等数据层构造函数即使读取了配置文件中的 `data.database.source`，最终仍调用 `xdb.OpenMySQL(...)`。

2. 配置里有 `driver` 字段，但没有真正传递到数据层

   `configs/*-service.yaml` 已有：

   ```yaml
   data:
     database:
       driver: mysql
       source: ${DATABASE_DSN}
   ```

   但 `cmd/*/wire_gen.go` 多数只传 `cfg.Data.Database.Source`，没有传 `cfg.Data.Database.Driver`。

3. 迁移 CLI 固定 MySQL

   `cmd/migrate/main.go` 固定导入 MySQL driver，并执行：

   ```go
   sql.Open("mysql", dsn)
   ```

   还会自动追加 `multiStatements=true`，这是 MySQL 专用行为。

4. 现有迁移 SQL 大量使用 MySQL 语法

   当前 `migrations/` 下有 34 个 SQL 文件，包含多种 SQLite 不兼容语法：

   - `AUTO_INCREMENT`
   - `ENGINE=InnoDB`
   - `DEFAULT CHARSET`
   - `COMMENT`
   - `ALTER TABLE ... ADD INDEX`
   - `MODIFY COLUMN`
   - 前缀索引，如 `request_id(32)`
   - `ON UPDATE CURRENT_TIMESTAMP`
   - 分区脚本 `PARTITION BY RANGE`
   - `information_schema`

5. Docker Compose 强依赖 MySQL

   `deployments/docker-compose/docker-compose.yml` 默认启动 `mysql` 服务，所有核心服务通过 `depends_on.mysql` 等待 MySQL 健康检查，并要求 `DATABASE_DSN` 必填。

6. 分区维护逻辑是 MySQL 专用能力

   `internal/pkg/db/partition.go` 使用 `information_schema.PARTITIONS`、`ALTER TABLE ... REORGANIZE PARTITION`、`DROP PARTITION` 等 MySQL 语法。SQLite 模式下应默认关闭 `PARTITION_ENABLED`。

## 推荐架构

### 1. 统一数据库打开器

新增通用入口，例如：

```go
// internal/pkg/xdb/db.go
type DatabaseConfig struct {
    Driver string
    DSN    string
    Pool   *PoolConfig
}

func Open(cfg DatabaseConfig) (*gorm.DB, error)
func OpenSQL(driver, dsn string) (*sql.DB, error)
```

支持规则：

- `driver=mysql` 使用 `gorm.io/driver/mysql`。
- `driver=sqlite` / `driver=sqlite3` 使用 `gorm.io/driver/sqlite`。
- `driver` 为空时：
  - DSN 以 `file:`、`.db`、`.sqlite`、`.sqlite3` 或 `:memory:` 开头/结尾时推断为 SQLite。
  - 其他情况保持 MySQL，兼容现有部署。

SQLite 建议默认参数：

```text
file:/data/micro-one-api.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on
```

连接池建议：

- SQLite: `MaxOpenConns=1` 或较小值，避免写锁冲突。
- MySQL: 保持现有默认池化配置。

### 2. 数据层构造函数接收 driver

把现有只传 DSN 的形式：

```go
data.NewRepositoryFromEnv(cfg.Data.Database.Source)
```

调整为显式配置：

```go
data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
```

或新增结构体参数，避免可变参数含义不清：

```go
data.NewRepository(data.DatabaseOptions{
    Driver: cfg.Data.Database.Driver,
    Source: cfg.Data.Database.Source,
})
```

涉及模块：

- `internal/identity/data`
- `internal/channel/data`
- `internal/billing/data`
- `internal/log/data`
- `internal/monitor/data`
- `internal/config/data`
- `internal/notify/data`
- `cmd/admin-api` 中直接使用 `database/sql` 的 system options repo

### 3. 迁移目录按方言拆分

保留现有 MySQL 迁移目录：

```text
migrations/mysql/
```

新增 SQLite 迁移目录：

```text
migrations/sqlite/
```

不建议自动把 MySQL SQL 文本转换成 SQLite SQL，原因是 `ALTER TABLE`、索引、默认值、分区和时间字段语义差异较大，自动转换容易遗漏边界。

SQLite 迁移集可以从当前最新 schema 生成首个 baseline：

```text
migrations/sqlite/000_create_core_tables.sql
migrations/sqlite/001_create_indexes.sql
migrations/sqlite/002_seed_defaults.sql
```

后续新功能要求同时提交：

- MySQL 迁移
- SQLite 迁移
- 迁移测试

### 4. 迁移 CLI 支持 driver

`cmd/migrate` 增加参数和环境变量：

```text
-driver mysql|sqlite
MIGRATIONS_DRIVER
DATABASE_DRIVER
SQL_DRIVER
```

DSN 优先级保持：

```text
MIGRATIONS_DSN > DATABASE_DSN > SQL_DSN
```

driver 优先级：

```text
-driver > MIGRATIONS_DRIVER > DATABASE_DRIVER > SQL_DRIVER > 从 DSN 推断 > mysql
```

目录默认规则：

- MySQL: `./migrations/mysql`
- SQLite: `./migrations/sqlite`

为了兼容当前仓库，短期内可以保留 `./migrations` 作为 MySQL legacy 目录，等迁移重排时再移动。

### 5. 提供轻量 Docker Compose

新增：

```text
deployments/docker-compose/docker-compose.lite.yml
```

核心变化：

- 删除 `mysql` 服务。
- 增加共享 SQLite 数据卷，例如 `sqlite_data:/data`。
- 所有需要数据库的服务挂载该卷。
- 设置：

  ```text
  DATABASE_DRIVER=sqlite
  DATABASE_DSN=file:/data/micro-one-api.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on
  PARTITION_ENABLED=false
  ```

- `depends_on` 只保留 Redis 和必要业务服务依赖。
- `migrate` 使用 SQLite driver 和 SQLite 迁移目录。

示例启动体验应收敛为：

```sh
docker compose -f deployments/docker-compose/docker-compose.lite.yml up -d
```

用户只需要填写最少安全参数：

- `JWT_SECRET_KEY`
- `SERVICE_TOKEN`
- `ADMIN_TOKEN`
- `REDIS_PASSWORD`
- 初始管理员账号密码

如果 Redis 后续也想轻量化，可以单独评估内存/单进程替代方案，但这不是 Issue #4 的核心范围。

## 分阶段实施计划

### 阶段 1：最小可运行 SQLite 支持

目标：本地和 Docker Lite 可以用 SQLite 启动核心链路。

任务：

1. 新增 `xdb.Open`，支持 MySQL / SQLite。
2. 修改各服务数据层构造函数，传递并消费 `driver`。
3. 修改 `cmd/migrate` 支持 `-driver sqlite`。
4. 新增 SQLite baseline 迁移。
5. 新增 `docker-compose.lite.yml`。
6. 在 SQLite 模式下强制跳过分区维护。
7. 更新 `docs/deployment.md`，增加 Lite 部署章节。

验收：

```sh
go test ./internal/pkg/xdb ./internal/pkg/migrate ./internal/identity/data ./internal/channel/data ./internal/billing/data ./internal/log/data ./internal/monitor/data
docker compose -f deployments/docker-compose/docker-compose.lite.yml up -d
```

核心功能手工验收：

- 管理员初始化
- 登录
- 创建渠道
- 创建 token
- relay 请求写入日志
- billing ledger 写入
- 管理后台日志、用量、订单页面可读

### 阶段 2：双数据库迁移治理

目标：避免后续功能只改 MySQL 导致 SQLite 漂移。

任务：

1. 迁移目录标准化为 `migrations/mysql` 和 `migrations/sqlite`。
2. CI 增加 SQLite migration apply 测试。
3. CI 增加 SQLite 仓储集成测试。
4. 增加文档规则：凡涉及 schema 变更，必须同时更新两套迁移。
5. 增加脚本：

   ```sh
   make migrate-sqlite
   make test-sqlite
   ```

验收：

```sh
MIGRATIONS_DRIVER=sqlite MIGRATIONS_DSN='file:/tmp/micro-one-api-test.db?_foreign_keys=on' go run ./cmd/migrate -dir ./migrations/sqlite
```

### 阶段 3：部署体验优化

目标：让 SQLite 成为默认轻量部署路径，MySQL 作为进阶部署路径。

任务：

1. README 首屏增加 Lite 部署命令。
2. `.env.example` 拆分为：
   - `.env.lite.example`
   - `.env.mysql.example`
3. 发布说明中明确两种模式：
   - SQLite: 单机自托管、低维护成本。
   - MySQL: 多实例、较高并发、长期生产。
4. 增加从 SQLite 迁移到 MySQL 的导出/导入说明。

## 关键设计取舍

### 为什么不直接把 MySQL 迁移改成跨数据库 SQL

现有迁移已经包含较多 MySQL 专用能力。为了让单个 SQL 文件兼容两种数据库，需要牺牲 MySQL 能力，或者在迁移 runner 中实现复杂条件语法。相比之下，双迁移目录更直接，长期维护成本也更可控。

### 为什么 SQLite 适合默认轻量部署

SQLite 可以显著降低首启门槛：

- 少一个 MySQL 容器。
- 少一组数据库用户名、密码、root 密码、健康检查和初始化参数。
- 数据文件可直接通过 Docker volume 持久化。
- 适合个人、低并发、单机部署。

### 为什么仍保留 MySQL

当前项目是多服务架构，包含日志、账务、渠道、监控等写入场景。SQLite 是单文件数据库，写并发能力和多实例共享能力不适合所有生产环境。MySQL 仍应作为高并发和多副本部署的推荐选项。

## 风险与处理

| 风险 | 影响 | 处理 |
| --- | --- | --- |
| SQLite 写锁竞争 | 日志和账务写入可能阻塞 | WAL、busy timeout、较小连接池、批量写入控制 |
| 迁移漂移 | MySQL 与 SQLite schema 不一致 | CI 同时跑两套迁移 |
| MySQL 专用 SQL 泄漏 | SQLite 运行时报错 | 仓储测试覆盖 SQLite；代码中按 dialector 分支 |
| 分区维护误启 | SQLite 启动后报错 | SQLite 模式强制禁用或启动时报明确错误 |
| 多服务共享单 SQLite 文件 | 并发写入压力增大 | Lite 模式定位单机低并发；文档明确边界 |
| CGO 依赖 | `go-sqlite3` 需要 CGO | Docker 构建镜像内安装构建依赖，或后续评估纯 Go SQLite driver |

## 建议的 Issue 回复

可以回复：

> 可以支持。当前代码已经引入了 SQLite 依赖，并且部分测试已经用 SQLite 跑通；但生产入口、迁移工具和 Docker Compose 仍按 MySQL 固定实现。建议分三步做：先增加 `DATABASE_DRIVER=sqlite` 和 SQLite baseline 迁移，让单机 Docker Lite 部署跑起来；再把迁移目录拆成 MySQL/SQLite 双轨并加入 CI；最后把 README 和 `.env.example` 调整成 SQLite 轻量部署优先、MySQL 生产部署可选。

## 最小改动清单

首个 PR 建议只包含：

1. `internal/pkg/xdb` 增加统一 `Open`。
2. `cmd/migrate` 增加 driver 支持。
3. 核心服务数据层消费 driver。
4. 新增 `migrations/sqlite` baseline。
5. 新增 `docker-compose.lite.yml`。
6. 新增 SQLite 模式测试。
7. 更新部署文档。

这样可以把 Issue #4 直接落成可验证的轻量部署能力，同时不影响现有 MySQL 部署。
