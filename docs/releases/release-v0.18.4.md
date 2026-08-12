# Micro-One-API v0.18.4 发布：admin 用量排行维度修正与排序保证

> 2026-08-12 · 上一版：[v0.18.3](./release-v0.18.3.md)（2026-08-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.18.4)

v0.18.4 是 v0.18.3 之后的 **PATCH 修复版本**（1 个提交，`29e875d`），修复 admin 运营后台「高消耗渠道」排行把订阅账号流量误渲染为「已删除渠道」的问题，并为全部 Top-N 用量排行补上不依赖 SQL 返回顺序的确定性 quota 降序排序。前端概览页同步新增「高消耗订阅账号」排行卡片。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无配置变更**。受影响服务：admin-api（含管理前端 web/dist）。

## 1. fix(admin): correct usage ranking dimensions and ordering（29e875d）

- **根因（维度混淆）**：订阅账号流量为兼容存量 ledger 消费方，会携带一个等于账号 id 的合成 `channel_id`。`AggregateUsageTopN("channel")` 只按 `channel` 单维度聚合，订阅账号行混入渠道排行，由于该合成 id 查不到渠道名称，前端只能回退渲染为裸 `#<id>`，看起来像「已删除渠道」；同时这些行还会挤占 Top-N 名额，把真实渠道挤出榜单。
- **根因（排序不确定）**：billing 仅在 bucket 数超过请求 limit 时才在 SQL 层做 Top-N；bucket 数较少时直接返回，SQL 不保证行序，导致排行顺序不稳定。
- **修复**：渠道排行改为按 `["channel", "subscription_account"]` 双维度请求（limit=0 取全量），在 service 层先剔除 `subscription_account_id > 0` 的订阅账号行、再按 quota 降序排序并截取 Top-N——过滤先于截断，保证榜单只含真实渠道且数量不被挤占；所有维度（user / channel / model / token / subscription_account）统一在 service 层按 quota 降序重排，不再依赖 SQL 返回顺序。
- **前端**：`OverviewPage` 新增「高消耗订阅账号」排行卡片（`top_subscription_accounts`，cyan 配色，标签带平台标注），概览排行区从 4 列扩展为 5 列。
- **影响服务**：admin-api、web/dist。billing 侧 `AggregateUsage` 早已支持多维度 group_by，本次只是调用方改参，无 billing 代码变更。

## 兼容性说明

- **API / proto / 数据库 / 配置**：全部无破坏性变更（`AggregateUsageRequest.group_by` 为既有 repeated 字段，新增维度为 additive 用法）。
- **行为变化**：admin 概览「高消耗渠道」榜单不再包含订阅账号合成行，顺序为确定的 quota 降序；新增「高消耗订阅账号」榜单。
- **升级**：admin-api 镜像需重新构建部署；前端 web/dist 需重新构建并同步到 `/opt/web/dist`（按 README 部署流程，前端走 host 挂载而非镜像）。

## 升级步骤

```bash
git fetch --tags
git checkout v0.18.4
# 重新构建并部署 admin-api（镜像）与 web/dist（host 挂载），其余服务无需变更。
```

## 验证

- `go test ./app/admin/...`：通过（新增 `TestAggregateUsageTopNChannelsExcludesSubscriptionAccounts`、`TestAggregateUsageTopNSubscriptionAccountsSortsByQuota` 两个用例，覆盖双维度请求参数、limit=0、订阅账号行剔除与 quota 降序）。

## 完整变更日志

- fix(admin): correct usage ranking dimensions and ordering
