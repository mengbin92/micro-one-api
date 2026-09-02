# Micro-One-API v0.26.4 发布：控制台页面切换延迟修复

> 2026-09-01 · 上一版：[v0.26.3](./release-v0.26.3.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.4)

v0.26.4 是 v0.26.3 之后的 **PATCH 控制台性能修复版本**：消除 `/dashboard`、`/usage`、`/admin/logs` 等页面每次打开约 1 秒的延时。诊断确认服务端本身很快（控制台 TTFB 约 0.8ms、`/api/log` 13–17ms、Dashboard 聚合 76+69ms），延迟来自前端资源加载与请求瀑布：懒加载页面先下载 JS 才发起 API、约 391KB 的 charts 公共包被非图表页面预加载、哈希资源无长期缓存且无压缩（单次 304 回源约 177ms）、导航与页面重复请求 `/user/self` 和 `/user/dashboard`。

本版本无公共 API / proto 变更；包含一个数据库迁移（089 联合索引）。需要更新 `admin-api`、同步 `web/dist` 并执行 089 迁移；`relay-gateway` 及其余服务无变更，无需重启（executor 观察窗口不受影响）。

## 1. 静态资源缓存与压缩

**根因**：Go 静态文件服务直接使用 `http.FileServer`，只给 HTML 设置了禁缓存策略；哈希命名的 `/assets/*` 每次都要回源校验（304 往返约 177ms），文本资源未启用压缩，首屏 11 个脚本、10 个中文字体分片总计约 1.84MB 全量传输。

**修复**：带哈希的 `/assets/*` 返回一年 `Cache-Control: public, max-age=31536000, immutable`；JS/CSS/HTML/SVG/JSON 等文本资源按 `Accept-Encoding` 协商 gzip。边界处理：尊重 `gzip;q=0` 与 `Range` 请求；WOFF2 字体本身已压缩、不再 gzip；不存在的 `/assets/*` 返回 404 且不加长缓存；空文件输出合法的空 gzip 数据流。HTML 保持 `no-cache, no-store, must-revalidate` 不变。

**影响服务**：`admin-api`。

## 2. 图表依赖分包隔离

**根因**：手工分包策略将 recharts/d3 归入 `charts` 公共包（约 391KB），登录页、Usage、Logs 等非图表页面也会加载它。

**修复**：移除 `charts` 与兜底 `vendor` 分包，图表代码只随 Dashboard 等图表页动态加载。生产构建实测非图表路由包不引用 recharts，登录首屏图表资源为 0。

**影响服务**：Web 前端构建产物。

## 3. 路由预取与账户查询复用

**根因**：所有页面 `React.lazy`，首次进入需先下载页面 JS、组件挂载后才开始请求 API，形成串行瀑布；导航组件与 Dashboard 页各自请求 `/user/self`、`/user/dashboard`，React Query 未设 `staleTime` 导致页面重新挂载即重复请求。

**修复**：侧边栏与导航链接在悬停/聚焦时预加载目标路由模块（`route-loaders.ts`，预取失败静默处理）；抽取共享账户查询（`account-queries.ts`），导航栏、Dashboard、个人资料、充值、兑换共用同一缓存（用户信息 5 分钟、账户概览 30 秒新鲜期）；登录成功与退出时清空整个 React Query 缓存，杜绝跨账号短暂显示上一用户数据。

同时修复 `AdminRoute` 刷新时信任 `localStorage.userRole` 导致旧角色闪现管理入口的问题：权限判断改为以共享的 `/user/self` 查询为唯一来源。

**影响服务**：Web 前端。

## 4. Dashboard 聚合索引（迁移 089）

**根因**：用户 Dashboard 按 `user_id + type` 过滤后按 `created_at` 分组聚合，现有 `(user_id, created_at, model_name)` 索引无法在扫描前收窄 consume 范围。

**修复**：新增 `(user_id, type, created_at)` 联合索引（MySQL / PostgreSQL / SQLite 三方言 + ownership 清单）。当前约 4 万行的表上优化器可能仍选择代价相近的既有索引，新索引随数据增长自然生效。

**影响服务**：数据库迁移（089），不要求重启任何服务。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：包含迁移 089（仅新增索引，无数据变更）；schema 拆分部署需按 ownership 归属应用到 `oneapi_billing`。
- **缓存行为**：哈希资源改为一年 immutable 后，回滚前端时浏览器可能继续使用缓存中的旧资源；回滚场景需让用户强制刷新或回滚后变更文件名（构建产物本身带内容哈希，正常升级不受影响）。
- **部署**：需更新 `admin-api`、同步 `web/dist`、执行 089 迁移；三者配套发布。`relay-gateway` 与其余服务无变更，**executor 观察窗口期间请勿重启 `relay-gateway`**。
- **回滚**：回滚 `admin-api` 镜像与 `web/dist` 即可；如需回滚索引，`ALTER TABLE billing_ledgers DROP KEY idx_billing_ledgers_user_type_created` 并删除 schema_migrations 的 089 记录（非必需，索引无副作用）。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.4
```

1. 从 v0.26.3 升级到 v0.26.4。
2. 应用迁移 089 到 billing 归属 schema（MySQL：`ALTER TABLE billing_ledgers ADD KEY idx_billing_ledgers_user_type_created (user_id, type, created_at);` 并登记 `schema_migrations`）。
3. 本地或 CI 交叉构建并更新 `admin-api`：`docker compose up -d --no-deps admin-api`。
4. 构建前端并同步 `web/dist` 到部署主机静态资源目录（主机挂载 `/opt/web/dist:/web:ro` 时无需重启容器）。
5. 验证：`curl -I /assets/<hashed>.js` 应返回 `immutable`；带 `Accept-Encoding: gzip` 应返回 `Content-Encoding: gzip`；HTML 仍为 `no-cache`。浏览器二次切换 `/dashboard`、`/usage`、`/admin/logs` 应为毫秒级。
6. 其余服务不因本版本重启。

## 验证

- `make verify`（unit、race、architecture、migration-check、前端 lint/test/build）
- 后端静态资源与 billing 数据层针对性测试；前端 46 个测试文件、166 个测试
- 浏览器实测登录首屏不加载图表资源；`/usage`、`/admin/logs` 构建产物不引用图表包
- 生产部署后逐项核对响应头（immutable / gzip / q=0 / 404 / woff2）与 089 索引登记

## 完整变更日志

- perf(web): eliminate page-switch latency from asset waterfalls
