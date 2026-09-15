# 2026-09-15 P1-3 旧迁移记录表升级预检验收记录

代码基线：`develop@77cd528d`，加本次 P1-3 工作区改动。未新增公共 API、业务表迁移或运行时配置；没有生产部署、生产数据库变更或版本发布。所有数据库验证均使用本机隔离容器 / 临时 SQLite 文件。

## 交付

- `Runner.Apply` 在 brownfield 标记和首个业务 DDL 之前，用事务执行 `INSERT INTO schema_migrations (version) ...` 探针并回滚，直接验证旧表是否支持 runner 的记录写法。
- 旧表 `applied_at NOT NULL` 且无默认值时，返回可操作的 `preflight schema_migrations` 错误；错误同时提示历史半完成迁移需先核对对象、修复元数据并手工补录版本。
- `Runner.Status` 改为严格只读：`schema_migrations` 不存在时全部报告 pending，不再创建元数据表。
- 保留显式恢复原则：runner 不会仅因同名业务对象存在就自动补录迁移完成。
- MySQL / PostgreSQL migration smoke 接入同一集成用例；SQLite 单元测试覆盖阻断、修复后升级、重复执行与半完成恢复。

## 验证结果

| 验证 | 结果 |
| --- | --- |
| `go test ./platform/database/migrate` | 通过：覆盖 SQLite 生命周期、元数据预检、状态只读及既有迁移行为 |
| `make migration-check` | 通过：迁移前缀、ownership 与方言镜像治理检查 |
| `bash -n scripts/test-migration-smoke.sh` | 通过 |
| `./scripts/test-migration-smoke.sh mysql` | 通过：fresh 94 个迁移、repeat no-op、状态审计、失败注入不记账、旧表预检阻断 / 修复后升级 / 重复执行 / 半完成显式恢复 |
| `./scripts/test-migration-smoke.sh postgres` | 通过：fresh 40 个迁移、repeat no-op、状态审计、失败注入不记账、旧表预检阻断 / 修复后升级 / 重复执行 / 半完成显式恢复 |
| `make verify` | 通过：format、Go 测试、race 门禁、分层、迁移治理、前端 lint / test / build |

## 关键行为

1. **DDL 前阻断**：不兼容元数据表存在时，业务探针表不存在，`schema_migrations` 也没有 probe 版本；探针事务回滚。
2. **修复后升级**：为 `applied_at` 补默认值后，pending 迁移正常执行并记账；再次执行输出 nothing to apply。
3. **半完成恢复**：模拟“业务对象已存在但版本未记账”后，去掉默认值重跑仍先被预检阻断；恢复默认值并显式补录版本后，重跑为 no-op。
4. **状态只读**：空库执行 `Status` 只返回 pending，不创建 `schema_migrations`。

## 边界

- 预检证明元数据表接受 runner 的最小插入语句，不校验历史 `applied_at` 数值的业务语义。
- 历史半完成迁移仍需要人工核对对象与版本后补录；本次没有引入自动修复工具。
- 真实 MySQL / PostgreSQL 验证使用 MySQL 8.4 与 PostgreSQL 16；生产版本差异仍按部署手册在升级窗口核查。
