# Micro-One-API v0.25.0 发布：模型模态与注册表定价

> 2026-08-31 · 上一版：[v0.24.0](./release-v0.24.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.25.0)

v0.25.0 是 v0.24.0 之后的 **MINOR 模型能力与定价注册版本**：为模型管理和导入导出增加输入 / 输出模态与 cache-read 定价，统一注册表价格为每 1M tokens，并补齐 MySQL、PostgreSQL、SQLite 的迁移和旧 MySQL 默认值兼容。

本版本包含公共 channel API 的向后兼容字段新增，以及数据库迁移 `083`、`084`。它影响 `channel-service`、`admin-api` 和 `web/dist`；不包含 usage-semantics / billing 迁移 `085`–`088`，也不包含 Go 1.27 modernization。

## 1. 增加模型输入 / 输出模态和 cache-read 定价

**根因**：模型注册表只能表达通用 capabilities，无法区分输入与输出模态，也无法持久化 cache-read 价格，导致管理端、渠道 API 和 Web 定价页无法准确展示多模态模型。

**修复**：为模型详情、摘要、创建 / 更新请求和导入导出结构增加 `input_modalities`、`output_modalities` 和 `pricing_cache_read`；同步生成 channel API stubs、服务转换、仓储映射、管理端表单和 Web 模态图标。

**影响服务**：`channel-service`、`admin-api`、`web/dist`。

## 2. 统一注册表价格单位并兼容旧 MySQL 默认值

**根因**：模型注册表原有 `pricing_input` / `pricing_output` 以每 1K tokens 记录，而管理端和新模型元数据按每 1M tokens 展示；部分旧 MySQL 版本对带表达式或不兼容默认值的迁移处理不同。

**修复**：迁移将已有非零 `pricing_input` / `pricing_output` 数值乘以 1000，扩大价格列精度并统一按每 1M tokens 存储；新增 `pricing_cache_read`；为旧 MySQL 默认值路径提供兼容处理。新增 `083` 模态列和 `084` 定价列 / 数据转换迁移，覆盖三种数据库。

**影响服务**：数据库、`channel-service`、`admin-api`、Web 定价和模型管理页面。

## 3. 修复上游成本键格式展示

**根因**：上游成本数据的 provider key 可能使用不同大小写或分隔格式，管理端成本页面无法稳定匹配并展示已配置价格。

**修复**：统一 Web 成本页面的 key 归一化和展示匹配逻辑，并补齐相关测试。

**影响服务**：`web/dist`；不改变 relay executor 运行时行为。

## 兼容性说明

- **API / proto**：新增字段均为可选 / 非必填字段，旧客户端可继续工作；价格字段注释和语义统一为每 1M tokens。新增模态字段不改变既有模型路由。
- **数据库**：必须按 `083 → 084` 顺序执行。MySQL、PostgreSQL、SQLite 均提供对应迁移文件；`084` 会将旧输入 / 输出价格乘以 1000，重复执行或手工重复换算会导致价格错误。
- **迁移回滚**：`083` 的新增列可在备份基础上回滚；`084` 同时改变列精度、增加 cache-read 列并改写历史价格，不建议直接执行人工 down migration。回滚应用版本前应保留数据库备份，并根据备份恢复价格数据或继续使用兼容旧字段的应用版本。
- **配置 / 服务**：无新增必填环境变量。公共 API 生成文件和 channel/admin 服务必须整体升级，避免新旧服务对模型字段转换不一致。
- **Relay 观察**：本版本不改变 relay executor 运行时行为。观察期间不要构建、加载、重建或重启 `relay-gateway`，不要修改 `RELAY_ORCHESTRATOR_ENABLED` 或 allowlist。

## 升级步骤

```bash
git fetch --tags
git checkout v0.25.0
```

1. 备份数据库和当前 `channel-service`、`admin-api` 镜像；记录迁移版本和现有模型价格。
2. 先执行数据库迁移 `083`，确认输入 / 输出模态列存在，再执行 `084`。
3. 核对一条旧模型的输入 / 输出价格已从每 1K 正确转换为每 1M（数值乘以 1000），并确认 cache-read 默认值为 0。
4. 按顺序使用 `docker compose up -d --no-deps channel-service`、`docker compose up -d --no-deps admin-api` 部署后端，再同步发布 `web/dist`。
5. 验证模型列表、模型详情、创建 / 编辑、导入导出、模态图标、定价页和上游成本展示；不要使用宽泛的 `docker compose up -d`。

## 验证

- `go test ./app/channel/... ./app/admin/... ./platform/database/migrate/...`：验证模型字段、服务转换、仓储和迁移行为。
- Web lint、单元测试和生产构建：验证模型管理、定价、模态图标和成本 key 展示。
- MySQL、PostgreSQL、SQLite 分别验证 `083 → 084` 顺序、历史价格换算和旧 MySQL 默认值兼容。
- `./scripts/check-architecture.sh`、`./scripts/check-deployment-docs.sh` 和 Markdown 链接检查：通过。
- 生产观察期间仅部署数据库、`channel-service`、`admin-api` 和 `web/dist`；不触碰 `relay-gateway`。

## 完整变更日志

- fix(models): link pricing and multimodal metadata
- fix(migrations): support legacy mysql defaults
- feat(models): align modalities and registry pricing
- fix(web): update user pricing modalities
- fix(web): render upstream cost key formats correctly
