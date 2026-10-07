# micro-one-api admin web

`web/` 是 micro-one-api 的管理后台前端，基于 React、TypeScript、Vite、React Router、TanStack Query 和 shadcn/base-ui 组件实现。

组件代码由本仓库维护；shadcn 4.7.0 的样式变体保存在 `src/styles/shadcn.css`，包含原 MIT 许可。构建使用这份本地样式，避免仅为 CSS 安装包含 MCP、Express 和文件匹配库的组件生成 CLI 工具链。

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

# 资金、授权/CAS、取消路径的覆盖率与交互时序门禁
npm run test:critical

# 生产构建上的桌面/手机 Markdown、CSP 和字体回归
npm run build
npm run test:e2e:d7

# 运行 ESLint
npm run lint
```

生产构建产物输出到 `web/dist`。Docker Compose 部署会把该目录挂载到 `admin-api` 容器的 `/web`，并通过 `ADMIN_WEB_ROOT=/web` 读取静态资源。

admin-api 在 SPA 页面响应中设置 CSP。Playground 优先使用 `/api/status` 的 `ServerAddress` 作为 Relay 地址，CSP 同步允许该地址的 origin；未设置时默认只允许同源。若前端构建使用跨域 `VITE_RELAY_BASE_URL` 作为回退地址，同时给 admin-api 设置相同 origin 的 `ADMIN_WEB_RELAY_ORIGIN`。更改 `ServerAddress` 后新加载的页面会获得更新后的 CSP。

已有生产构建和本地预览服务时，在 `web/` 下运行 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4174` 验证 CSP、跨域 Relay 请求、请求 ID 展示和 API Key 不落存储。脚本需要本机 Chrome，使用隔离用户和 mock API/Relay；验证真实 admin 响应头时传入实际页面地址、Relay origin 和 `--live-header`，例如 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4300 https://api.mengbin.top --live-header`。该模式仍拦截 API/Relay，不执行真实计费请求；取消与账务验收另见[运维手册](../docs/runbooks/routing-observability-runbook.md#流式取消验收)。

## Playground 内容与字体

助手回复在完成、停止或失败后渲染安全 Markdown，生成中显示原文；原始 HTML 被忽略，图片只显示说明，链接仅允许显式 HTTP(S) 外链，代码块仅支持手动复制。超过 65,536 个 UTF-16 code units 的回复保留纯文本。用户消息、历史重用、请求快照及取消规则保持既有行为；渲染器按需加载，无语法执行或高亮器。

界面使用本机中英文字体与等宽字体，不下载字体文件；字号/字形会随系统变化。`performance-budget.json` 保留 Q3 JS/CSS 上限并将字体预算收紧为 0。专项覆盖率、前后构建数字和本地验收边界见 [D7 记录](../docs/runbooks/d7-experience-acceptance-2026-10-07.md)。

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
