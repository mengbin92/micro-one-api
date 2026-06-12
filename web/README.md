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
