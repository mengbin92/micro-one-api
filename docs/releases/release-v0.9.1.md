# Micro-One-API v0.9.1 发布：Schema 隔离生产就绪

> 2026-07-19 · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.9.1)

v0.9.1 是 v0.9.0 的补丁版本，主要完成 Phase 2.4 Schema 隔离的生产环境启用，修复了若干配置问题，并同步了本地部署配置。

**关键特点**：所有改动向后兼容，schema 隔离默认关闭，升级即生效，无需改环境变量或迁移。

## 核心更新

### 1. Schema 隔离生产启用完成

Phase 2.4 的 per-service schema 隔离现已生产就绪，完成以下关键修复：

- **修复 generated column 问题**：`user_subscriptions` 表的 `active_user_id` 是 generated column，在 schema_split.sql 中改用显式列插入
- **填充 schema_migrations**：为每个 schema 创建并填充 migration 版本记录，避免重复执行已应用的迁移
- **跨 schema 视图创建**：在 `oneapi_billing` 中创建 `channels`、`logs`、`users` 视图，支持对账跨表读
- **本地配置同步**：docker-compose.yml 添加所有服务的 `<SVC>_SCHEMA` 环境变量

### 2. 配置修复

- **protobuf Duration 格式修复**：interval 值从 `5m`/`1h` 改为 `300s`/`3600s`，符合 proto Duration 规范
- **async billing 配置启用**：billing/configs/config.yaml 添加 `async.enabled` 配置段
- **batch log 配置启用**：log/configs/config.yaml 添加 `batch_enabled` 配置段

### 3. 生产验证

生产环境（43.133.65.212）已完成 schema 隔离切流：

```
oneapi_identity.users        → 5 rows
oneapi_channel.channels       → 4 rows
oneapi_billing.billing_ledgers → 12563 rows
oneapi_log.logs              → 2414 rows
```

跨 schema 视图验证：
```
oneapi_billing.channels (view) → 4 rows
oneapi_billing.logs (view)     → 2414 rows
oneapi_billing.users (view)    → 5 rows
```

所有服务健康检查通过，billing 对账自动完成，无 `table doesn't exist` 错误。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.9.1

# 检查并替换 deployments/docker-compose/.env 中的生产密钥
cd deployments/docker-compose
docker compose --env-file .env config --quiet

# 旧数据卷升级前先备份；全新环境直接启动
docker compose --env-file .env up -d --build
```

### 可选启用 Schema 隔离

按需启用以下环境变量（不设置时保持共享库模式）：

```bash
# 逐服务启用 schema 隔离
IDENTITY_SCHEMA=oneapi_identity
CHANNEL_SCHEMA=oneapi_channel
BILLING_SCHEMA=oneapi_billing
LOG_SCHEMA=oneapi_log
ADMIN_SCHEMA=oneapi_admin
CONFIG_SCHEMA=oneapi_config
NOTIFY_SCHEMA=oneapi_notify
MONITOR_SCHEMA=oneapi_monitor

# relay-gateway 需与 billing 同库
SUBSCRIPTION_SCHEMA=oneapi_billing
```

**启用前必须先执行** `migrations/schema_split.sql` 创建 per-service schema。

## 兼容性说明

- **API**：无破坏性变更
- **数据库**：schema 隔离相关文件是可选运维工件，不影响默认部署
- **配置**：所有新能力默认关闭，升级即生效

## 完整变更日志

- 05f4260 feat(phase2.4): complete schema isolation production enablement
- 4f21361 feat(conf): enable async billing and batch log features (v0.9.0)
- 953541a fix(conf): use protobuf Duration format (seconds) for interval values
- 06bd5a3 feat(conf): migrate all 9 services to proto-based configuration
- da97e45 feat(channel): migrate config to proto definition (conf.proto)

## 下一步

后续版本计划包括：

- 完善渠道健康检查和自动熔断
- 强化用量统计、成本分析和对账能力
- 增加更细粒度的用户、团队、分组和模型权限
- 完善前端运营体验和可观测性面板
- 加强生产部署文档、安全基线和高可用方案

欢迎关注、试用和参与改进：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
