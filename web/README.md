# micro-one-api admin web

`web/` 是 micro-one-api 的管理后台前端，基于 React、TypeScript、Vite、React Router、TanStack Query 和 shadcn/base-ui 组件实现。

## 常用命令

```bash
# 安装依赖
npm ci

# 本地开发
npm run dev

# 类型检查并构建生产产物
npm run build

# 运行单元测试
npm run test

# 运行 ESLint
npm run lint
```

生产构建产物输出到 `web/dist`。Docker Compose 部署会把该目录挂载到 `admin-api` 容器的 `/web`，并通过 `ADMIN_WEB_ROOT=/web` 读取静态资源。

admin-api 在 SPA 页面响应中设置 CSP。Playground 优先使用 `/api/status` 的 `ServerAddress` 作为 Relay 地址，CSP 同步允许该地址的 origin；未设置时默认只允许同源。若前端构建使用跨域 `VITE_RELAY_BASE_URL` 作为回退地址，同时给 admin-api 设置相同 origin 的 `ADMIN_WEB_RELAY_ORIGIN`。更改 `ServerAddress` 后新加载的页面会获得更新后的 CSP。

已有生产构建和本地预览服务时，在 `web/` 下运行 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4174` 验证 CSP、跨域 Relay 请求、请求 ID 展示和 API Key 不落存储。脚本需要本机 Chrome，使用隔离用户和 mock API/Relay；验证真实 admin 响应头时传入实际页面地址、Relay origin 和 `--live-header`，例如 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4300 https://api.mengbin.top --live-header`。该模式仍拦截 API/Relay，不执行真实计费请求；取消与账务验收另见[运维手册](../docs/runbooks/routing-observability-runbook.md#流式取消验收)。

## API 类型

前端 API 类型由仓库根目录的 `openapi.yaml` 生成：

```bash
npm run generate:api
```

当后端 proto 或 OpenAPI 输出变化后，先在仓库根目录运行 `make proto`，再回到 `web/` 运行 `npm run generate:api`。

## 测试

前端单元测试使用 Vitest：

```bash
npm run test
```

端到端测试使用 Playwright：

```bash
npm run test:e2e
```

Playwright 测试需要可访问的前后端服务。完整后端发布验收优先使用仓库根目录的 Docker E2E 脚本：

```bash
make test-e2e
```
