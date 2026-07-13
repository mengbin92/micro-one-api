# Micro-One-API v0.6.1 发布公告

> 2026-07-10 · 上一版: [v0.6.0](./release-v0.6.0.md) (2026-07-09)

v0.6.1 是 v0.6.0 之后的 PATCH 版本,聚焦品牌视觉资产落地与管理后台 logo 资源 404 修复。无 proto 变更,无数据库迁移,无破坏性 API 变更,升级时只需替换镜像 + 重建容器即可。

## 亮点

- **品牌视觉落地**:新增项目 logo 图标、横排 wordmark SVG 资产,刷新 README 头图与发版链接。
- **管理后台 logo 404 修复**:admin-api HTTP server 补齐 `/logo-icon.svg`、`/logo-wordmark.svg` 路由,前端 logo 引用不再 404。

## 变更内容

### Added

- `docs/assets/micro-one-api-logo-icon.svg`:项目 logo 图标 SVG。
- `docs/assets/micro-one-api-logo-wordmark.svg`:项目 logo 横排 wordmark SVG。
- `docs/logo-design.md`:logo 设计说明。
- `web/public/logo-icon.svg`、`web/public/logo-wordmark.svg`:前端静态 logo 资产。
- `web/public/favicon.svg`:刷新站点 favicon。

### Changed

- `README.md`:头图改为 logo wordmark,最新发版链接更新到 v0.6.0,功能概览补充订阅套餐与用量查询、订阅账号治理描述。
- `web/src/components/AppNavigation.tsx`:导航栏 logo 引用更新为新资产路径。

### Fixed

- `internal/admin/server/http.go`:admin HTTP server 新增 `/logo-icon.svg` 与 `/logo-wordmark.svg` handler 注册。此前 Vite 构建输出的 logo 资产未在 admin server 路由中暴露,前端引用返回 404。
- `internal/admin/server/http_test.go`:新增外部 web root 下 logo 资产服务回归测试。

## 配置变化

无新增配置项,无新增环境变量。

## 升级指南

1. 拉取 v0.6.1 镜像并按依赖顺序重启:`config-service` → `identity-service` → `channel-service` → `billing-service` → `log-service` → `relay-gateway` → `admin-api` → workers。
2. 验证 `/healthz` 与 `/metrics` 正常;访问管理后台确认导航栏 logo 正常显示(不再 404)。

### 兼容性

- HTTP 客户端协议:完全向后兼容,所有 `/v1/*` 接口行为不变。
- gRPC 客户端:proto 无变更。
- 数据库:无新增迁移。

### 回滚

- 代码:回滚到 v0.6.0 镜像。logo 资源 404 不影响功能正确性,仅为视觉缺失。

## Security

- 本版仅涉及静态资源、路由注册与文档,无业务逻辑变更,无新增依赖。

## 验证

本次发版前本地已执行:

```bash
make test-unit
cd web && npm run lint && npm test && npm run build
make build
```
