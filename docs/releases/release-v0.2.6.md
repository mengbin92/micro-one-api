# Micro-One-API v0.2.6 发布公告

> 2026-06-20 · 上一版: [v0.2.5](./release-v0.2.5.md) (2026-06-19)

v0.2.6 是一个管理后台体验增强与通知面板修复版本，重点补齐通知历史查看、渠道健康分析和成本分析的前端入口，并修复管理端通知 API 的代理与路由问题。无数据库迁移，无破坏性 API 变更。

## 亮点

- **通知面板**：管理后台顶部新增通知入口，可查看通知历史、按 `pending` / `sent` / `failed` 状态筛选，并显示待发送数量徽标。
- **渠道健康分析页面**：新增 `/admin/channel-health` 页面和健康图表组件，便于集中查看渠道健康状态与趋势。
- **成本分析页面**：新增 `/admin/cost-analysis` 页面和成本图表组件，支持在后台查看收入、成本、毛利等运营指标。
- **通知 API 代理修复**：前端通知请求统一走 `admin-api` 的 `/api/admin/notifications` 代理，由服务端转发到 `notify-worker`，降低浏览器端部署耦合。
- **后台 SPA 路由补齐**：`admin-api` 补齐新增后台页面路由，修复刷新或直接访问新增页面时无法回到前端应用的问题。

## 变更内容

### Added

- `web/src/components/NotificationPanel.tsx` 新增通知面板。
- `web/src/pages/admin/ChannelHealthPage.tsx` 与 `web/src/components/admin/HealthCharts.tsx` 新增渠道健康分析视图。
- `web/src/pages/admin/CostAnalysisPage.tsx` 与 `web/src/components/admin/CostCharts.tsx` 新增成本分析视图。
- `web/src/router.tsx` 与 `AppNavigation` 接入新增后台页面入口。

### Fixed

- `internal/admin/server/http.go` 增加 `notify-worker` 反向代理，通知查询不再需要前端直连 worker。
- 通知面板兼容 `notify-worker` 直接返回 `{items,total}` 的响应格式。
- 修复通知面板路由与刷新路径，补齐新增后台 SPA route。

## 配置变化

- 无新增环境变量。
- 如需使用通知面板，部署环境需要保持 `admin-api` 能访问 `notify-worker`，沿用已有 `NOTIFY_GRPC_ENDPOINT` / worker 部署配置即可。

## 升级说明

- 无数据库迁移。
- 无破坏性 API 变更。
- 从 v0.2.5 升级时，重新构建并发布 `admin-api` 与管理前端即可。
- 若使用外部 `ADMIN_WEB_ROOT` 托管前端，请同步发布新的 `web/dist`。

## 验证

本次发版前执行：

```bash
make test
cd web && npm run lint && npm test && npm run build
```

重点覆盖：

- `admin-api` 通知代理路由与后台 SPA route 单元测试。
- 前端通知面板响应格式兼容。
- 新增后台页面和图表组件的 TypeScript 构建校验。
