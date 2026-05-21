# 修复设计:channel-abilities 同步 + 迁移管理 runner

**日期**: 2026-05-21
**分支**: `test/anthropic-api`
**背景**: e2e 扣费链路验证过程中暴露的两处运行时缺陷。

---

## 问题陈述

### 问题 1:Admin API 新建的 channel 不可用

`internal/channel/data/data.go` 的 `createChannelDB` / `updateChannelDB` / `deleteChannelDB` / `changeStatusDB` 仅维护 `channels` 表,不写 `abilities` 路由表。`ChannelUsecase.SelectChannel` 通过 `abilities` 表选择渠道,因此通过 admin HTTP API 新建的渠道始终选不到,relay 调用返回 `internal server error`(`upstream service error` 之前的阶段就已失败)。

复现:本次端到端测试时手工 `INSERT INTO abilities ...` 后 relay 才走通。

### 问题 2:增量迁移不会被应用

`deployments/docker-compose/docker-compose.yml` 通过 `docker-entrypoint-initdb.d` 挂载 `migrations/` 目录。MySQL 这个机制只在**数据卷为空**时执行一次目录里的 SQL 文件,且无版本追踪。

后果:`mysql_data` 持久化卷一旦存在,新增的 migration(本仓库目前为 015/018/019/020/021)永远不被应用,造成代码引用的列在生产/测试库中缺失,服务以"未知列"错误失败。复现于本次 e2e:用户注册报 `failed to generate unique aff code`(实际是 `users.aff_code` 列不存在),`CreateChannel` 报 `Unknown column 'weight'`(`channels.weight` 列不存在)。

---

## 修复方案

### Part 1:abilities 事务同步

**范围**:`internal/channel/data/data.go` 内 DB 路径的 4 个方法。Memory 路径仅测试使用,不变。

**实现要点**:

新增私有方法:

```go
// syncAbilitiesTx rewrites abilities rows for one channel.
// Caller MUST run inside an active gorm transaction.
func (r *Repository) syncAbilitiesTx(tx *gorm.DB, channel *biz.Channel) error
```

逻辑:
1. `DELETE FROM abilities WHERE channel_id = ?`(全删,后重建,简化 diff 逻辑)
2. 针对 `SplitCSV(channel.Group)` × `channel.Models` 的笛卡尔积构造 `abilityModel` 切片;跳过空 group / 空 model
3. `tx.Create(rows)` 批量插入(零行时跳过)

字段映射:

| ability 列 | 取值来源 |
|---|---|
| `group` | `SplitCSV(channel.Group)` 中每个非空字符串 |
| `model` | `channel.Models` 中每个非空字符串 |
| `channel_id` | `channel.ID` |
| `enabled` | `channel.Status == ChannelStatusEnabled` (即 `== 1`) |
| `priority` | `&channel.Priority`(0 也存为 0,不存 NULL) |

各 DB 方法改造:

| 方法 | 事务内顺序 |
|---|---|
| `createChannelDB` | `tx.Create(channelModel)` → `syncAbilitiesTx(tx, channel)` |
| `updateChannelDB` | `tx.Model(...).Updates(...)` → `syncAbilitiesTx(tx, channel)` |
| `deleteChannelDB` | `tx.Where("id=?",id).Delete(channelModel{})` → `tx.Where("channel_id=?",id).Delete(abilityModel{})` |
| `changeStatusDB` | `tx.Model(channelModel{}).Update("status",s)` → `tx.Model(abilityModel{}).Where("channel_id=?",id).Update("enabled", s==1)` |

所有方法用 `r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { ... })` 包裹。

**为什么 DELETE-then-INSERT 而非 diff**:channel 的 group 和 models 列表是 CSV 字符串,需要解析后比对集合差异。DELETE+INSERT 对几行到几十行的 abilities 没有性能问题(单 channel 通常不会有上百个 group×model 组合),实现简单。

**为什么不在 biz 层做**:`channels` 和 `abilities` 是同一个聚合的两个物化视图,事务化才能保证一致性。biz 层无法表达事务边界,改用方案 A 把数据存储的内部一致性约束放在 repo 层,对调用方完全透明。

**测试覆盖**(新增 `internal/channel/data/data_test.go`):

用 `sqlite::memory:` + GORM(项目已使用 GORM,sqlite driver 仅测试时引入)断言:
- Create:abilities 行数 == groups × models
- Update 把 models 从 `[a,b]` 改为 `[c]` 后,abilities 旧两行删除、新一行写入
- Delete:abilities 全部清理
- ChangeStatus(1→2):abilities.enabled 全部变 false
- 任一步骤失败时,channels 与 abilities 都回滚

如果 sqlite GORM 与 MySQL DDL 不兼容(`backtick` 标识符等),改为接受外部 `*gorm.DB`,在 `e2e-mysql` 启动时跑一遍。优先 sqlite;不行降级。

---

### Part 2:迁移 runner

**新增文件**:

1. **`migrations/022_create_schema_migrations.sql`** - 追踪表
   ```sql
   CREATE TABLE IF NOT EXISTS schema_migrations (
     version VARCHAR(255) NOT NULL PRIMARY KEY,
     applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
   );
   ```

2. **`internal/pkg/migrate/runner.go`** - 核心逻辑(无外部依赖,只用 `database/sql`)
   ```go
   type Runner struct {
       db *sql.DB
       dir string
   }
   func New(db *sql.DB, dir string) *Runner
   func (r *Runner) Apply(ctx context.Context) (applied []string, err error)
   func (r *Runner) Status(ctx context.Context) ([]MigrationStatus, error)
   ```

3. **`internal/pkg/migrate/runner_test.go`** - 单元测试

4. **`cmd/migrate/main.go`** - CLI 入口
   - 环境变量 `MIGRATIONS_DSN` 优先,fallback 到 `SQL_DSN`
   - flag `-dir`(默认 `./migrations`)
   - flag `-status`(只打印,不执行)
   - 退出码:`0` 成功 / `1` 任意失败

5. **`Makefile`** 加 target:
   ```makefile
   migrate:
     go run ./cmd/migrate -dir ./migrations
   migrate-status:
     go run ./cmd/migrate -dir ./migrations -status
   ```

**runner 核心算法**:

```
1. 确保 schema_migrations 表存在(执行 CREATE TABLE IF NOT EXISTS)
2. SELECT version FROM schema_migrations → applied_set
3. 扫描 dir,过滤出 *.sql,按文件名字典序排序 → all_files
4. 若 applied_set 为空 且 (users 表 OR channels 表存在):
     // brownfield baseline:把 ≤ 022 的全部标记为已应用,但不执行
     foreach f in all_files where filename <= "022_xxxxx.sql":
       INSERT INTO schema_migrations(version) VALUES (basename_without_ext(f))
5. foreach f in all_files where basename not in applied_set:
     BEGIN
       执行文件内 SQL(多语句:按 ";\n" 简单分割或用 multiStatements 选项)
       INSERT INTO schema_migrations(version) VALUES (basename)
     COMMIT
   失败 → ROLLBACK,返回 (已应用的列表, err)
```

**Brownfield baseline 触发判定**:
- `schema_migrations` 表当前为空(0 行)
- 且 `information_schema.tables` 中 `users` 或 `channels` 存在
- 同时满足则视为现存部署,需要 baseline。否则视为全新库,从头跑。

**版本号约定**:
- `version` 列存"文件名去掉 `.sql` 后缀",例如 `000_create_core_tables`、`022_create_schema_migrations`
- 按这个字符串字典序排序,所以三位数前缀保证顺序

**多语句执行**:
- DSN 启用 `multiStatements=true`(在 runner 内部 strings.Contains 检查 DSN,缺失时追加)
- 这样一个 .sql 文件里多条 ALTER 能一次执行

**与 `docker-entrypoint-initdb.d` 的关系**:
- 保留挂载,因为它在 mysql 容器**首启**时跑全部 SQL,对开发体验友好(空环境一行命令起来)
- runner 在那之后跑 brownfield baseline,会把所有 ≤ 022 的全部登记进 schema_migrations(不再实际执行)
- 增量 migration(023+)只能通过 runner 应用

**README 增补**(`README.md` 末尾加一节):

> ### 数据库迁移
>
> 首次启动会通过 `docker-entrypoint-initdb.d` 自动初始化所有 SQL。后续新增 migration 文件后:
>
> ```sh
> make migrate          # 应用所有未执行的迁移
> make migrate-status   # 仅查看状态
> ```
>
> 已有部署首次运行 `make migrate` 时会自动标记现存 migration 为已应用(brownfield baseline)。

**测试覆盖**(`internal/pkg/migrate/runner_test.go`,sqlite-mem):
- 空库:全部按顺序应用,schema_migrations 行数 == 文件数
- 已有 users/channels 表 + schema_migrations 不存在 → baseline 触发,标记所有现有文件,不实际执行
- 增量:已有 022,目录追加 023 文件,只跑 023
- 单个文件 SQL 失败:事务回滚,schema_migrations 不记录该 version
- 文件按字典序排序(`010` < `100`,`020` < `1abc`)

---

## 兼容性与回滚

- **Part 1** 是纯加法:不删除现有方法,只在事务里追加 abilities 操作。已部署的 channel 数据不受影响(只是 abilities 表缺记录的 channel 现在能补回来 —— 不,update 路径才会补;create 路径仅作用于新建)。

  > **副作用提示**:存量 channel 若已有缺失的 abilities(如本次 e2e 中手工插入的 channel 2),不会自动补齐。需要管理员触发一次 `UpdateChannel` 或写一个一次性 backfill 脚本。本 spec **不**包含 backfill,作为下一步可选项。

- **Part 2** 引入一张新表 `schema_migrations`。回滚只需 `DROP TABLE schema_migrations`。不修改任何现有迁移文件内容。

---

## 不做的事(YAGNI)

- 不实现 down migration(现有 SQL 全是单向 up,加 down 是大改造,且团队尚未有需求)
- 不引入 golang-migrate / goose 依赖
- 不让服务启动时自动跑迁移(隐式行为风险高)
- 不写存量 abilities backfill 脚本(可单独追加)
- 不写迁移文件校验(MD5 等)— 后续可加

---

## 验证

1. 单元测试通过
2. 手工 e2e:重启 docker compose,在已有数据库(本次测试遗留的环境)上跑 `make migrate`,确认 baseline 行为 + `schema_migrations` 表填充正确
3. 通过 admin API 新建一个 channel(任意 type),不手工插 abilities,直接发 relay 请求,应能命中
4. `DELETE channel` 后 abilities 行数为 0
