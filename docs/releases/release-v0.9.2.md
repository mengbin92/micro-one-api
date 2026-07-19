# Micro-One-API v0.9.2 发布：异步 Billing 扣费异常修复

> 2026-07-19 · 上一版：[v0.9.1](./release-v0.9.1.md)（2026-07-19）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.9.2)

v0.9.2 是 v0.9.1 的**补丁版本**,聚焦修复启用异步 Billing(Phase 2.1)与 Schema 隔离(Phase 2.4)后出现的 token 扣费异常。

本版**无新增业务表迁移**,**无 API 破坏性变更**,**无新增/删除端点**,所有修复均向后兼容,从 v0.9.1 平滑升级即可。

## 问题现象

在启用 schema 隔离(`BILLING_SCHEMA=oneapi_billing`)并开启异步 Billing(`async.enabled=true`)的生产环境中,billing-service 启动后无法正确加载定价配置,具体表现:

- `/v1/chat/completions` 等扣费链路返回 500 或扣费金额异常(0 / 错误比例)
- billing-service 日志出现 `Table 'oneapi_billing.system_options' doesn't exist`
- `PricingConfigRepo.GetPricingConfig` 查询失败,`pricingConfig()` 退回到硬编码的内存默认值,无法读取运维通过 admin 后台配置的 `ModelPrice` / `ModelRatio` / `GroupRatio` / `CompletionRatio` / `UpstreamModelPrice` / `AmountPerUnit`

## 根因

Phase 2.4 schema 隔离时,`migrations/schema_split.sql` 只把 `system_options` 表复制到了 `oneapi_admin` schema。而 billing-service 在 schema 隔离模式下连接的是 `oneapi_billing` schema,其 `app/billing/internal/data/pricing_config_repo.go` 通过 `Table("system_options")` 读定价配置——该表在 `oneapi_billing` 下不存在。

由于 `pricingConfig()` 在 `GetPricingConfig` 失败时**静默降级**到 usecase 内存中的硬编码默认值(`uc.groupRatios` / `uc.modelRatios` 等),不会直接报错退出,但实际扣费比例与运维在 admin 后台设置的值脱钩,表现为"扣费异常"。

异步 Billing 让问题更容易暴露:扣费从同步路径移到 worker goroutine,定价配置加载失败的影响被放大到所有异步计费请求。

## 修复内容

### 1. 在 `oneapi_billing` schema 暴露 `system_options` 视图

- **`migrations/schema_split.sql`**:新增 `oneapi_billing.system_options` 视图,指向 `oneapi_admin.system_options`(单一真相源),供 billing-service 加载定价配置。新部署走此路径一次性建好。
- **`migrations/061_add_billing_schema_system_options.sql`**(新增):幂等增量迁移,面向已按 v0.9.0/v0.9.1 部署 schema 隔离的环境。`DROP VIEW IF EXISTS` + `CREATE VIEW`,可安全重复执行。

视图指向 `oneapi_admin.system_options`,保证:
1. 定价配置单一真相源(admin schema 仍是权威写入点)
2. billing-service 通过视图透明读取,无需改业务代码
3. 不引入数据复制与同步问题

### 2. 迁移方案文档归档(非代码变更)

同步归档了一份 `docs/migration/buf-migration-and-kratos-v3-upgrade-plan.md`,评估后续从 protoc 切换到 buf 工具链、以及 kratos v2 → v3 升级的可行性与成本。该文档**不影响 v0.9.2 运行时行为**,仅为后续版本规划参考。

## 升级步骤

### 已启用 schema 隔离的环境(必做)

```bash
# 拉取版本
git fetch --tags
git checkout v0.9.2

# 应用增量迁移,为 billing schema 补齐 system_options 视图
make migrate                         # 自动按顺序应用 pending migrations
# 或显式指定:
# MIGRATIONS_DSN='user:pass@tcp(host:3306)/oneapi_billing' \
#   go run ./cmd/migrate -dir ./migrations

# 重启 billing-service 让其重新加载定价配置
docker compose restart billing-service

# 验证视图存在且数据可读
docker compose exec mysql mysql -uroot -p oneapi_billing \
  -e "SELECT option_key FROM system_options WHERE option_key='ModelRatio';"
```

**幂等性**:`061` 迁移使用 `DROP VIEW IF EXISTS` + `CREATE VIEW`,重复执行无副作用。已在 v0.9.0/v0.9.1 启用 schema 隔离的环境上验证。

### 未启用 schema 隔离的环境

无需任何动作。默认共享库模式下 `system_options` 仍是一张物理表,billing-service 直接读取,不受本次修复影响。schema_split.sql 的改动会在未来首次启用 schema 隔离时一并生效。

## 兼容性说明

- **API**:无破坏性变更
- **数据库**:`061` 是纯新增视图,不修改任何表结构或数据,向后兼容
- **配置**:无新增/删除环境变量
- **运行时**:未启用 schema 隔离的环境行为与 v0.9.1 完全一致

## 验证

发布前已确认:

- `app/billing/internal/data/pricing_config_repo.go` 的 `Table("system_options")` 查询在 `oneapi_billing` schema 下通过视图可正确返回数据
- `migrations/061_add_billing_schema_system_options.sql` 在已部署 v0.9.1 schema 隔离的环境上幂等执行通过
- `migrations/schema_split.sql` 新增视图与 `061` 增量迁移作用一致,新部署路径完整

升级后可执行以下检查确认定价配置加载正常:

```bash
# billing-service 日志不应再出现 system_options 不存在错误
docker compose logs billing-service | grep -i "system_options"
# (期望:无输出)

# 触发一次实际计费,确认扣费金额符合 admin 后台配置的比例
```

## 完整变更日志

- f7ccfb0 fix(billing): expose system_options to billing schema for pricing config
- 75f64d2 docs(migration): add buf migration + kratos v3 upgrade plan

## 下一步

后续版本计划:

- 按 `docs/migration/buf-migration-and-kratos-v3-upgrade-plan.md` 推进 buf 工具链迁移(独立 PR,预计 1.5–2 人日)
- 完善 schema 隔离下其他跨 schema 读取场景的覆盖测试
- 加强用量统计与对账的可观测性

欢迎反馈与参与:[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
