# Web Frontend 设计与实现文档

> 文档版本: v1.0  
> 创建日期: 2026-05-20  
> 对应代码: Sprint 1+2 (commit d14e989)  
> 作者: Claude Opus 4.7 + mengbin92

## 1. 架构决策

### 1.1 Monorepo vs 独立仓库

**决策**: Monorepo（`web/` 子目录与 `cmd/`、`internal/` 同级）

**理由**:
- API 契约同步：改 `/api/*` 的 PR 立刻看到前端联动改动，避免跨仓版本错位
- OpenAPI 复用：已有 `openapi.yaml`，前端用 `openapi-typescript` 生成 types，零手工同步
- 部署简化：单二进制交付（go:embed），无需独立前端服务
- 团队规模：单人维护，跨仓只增加心智负担
- 上游参照：upstream one-api 也是 monorepo 结构

**不适用场景**（未来可能需要拆分）:
- 多团队协作，前后端独立迭代
- 前端要独立发布到 CDN 给 SaaS 多租户用
- 前端要做成独立 npm 包给第三方集成

### 1.2 技术栈选型

| 技术 | 选择 | 理由 |
|---|---|---|
| **框架** | React 18 | 生态成熟、shadcn/ui 一等支持、团队熟悉度高 |
| **构建工具** | Vite | 开发体验最佳、HMR 快、构建速度优于 webpack |
| **语言** | TypeScript | 类型安全、OpenAPI 生成 types 无缝对接 |
| **UI 组件库** | shadcn/ui + Tailwind | 代码进仓库可改、主题自由、现代 Console 风格、AI 友好 |
| **状态管理** | TanStack Query | 服务端状态管理最佳实践、缓存/重试/乐观更新开箱即用 |
| **路由** | React Router v6 | 标准方案、SPA fallback 支持好 |
| **HTTP 客户端** | axios | 拦截器支持好、错误处理统一 |
| **图表** | recharts | 声明式、与 React 集成好、bundle size 可控 |

**为什么不选 Ant Design**:
- 默认风格"国产后台"味重，与 LLM gateway 工具气质不匹配
- 包体积大（~2MB gzipped）
- shadcn/ui 的"组件代码在仓里"模式更灵活，不被库版本绑架

**为什么不选 Vue/Svelte**:
- shadcn/ui 生态主要在 React
- OpenAPI TypeScript 生成的类型与 React 组件结合更自然
- 团队已有 React 经验

### 1.3 部署方式

**决策**: go:embed 单二进制部署

**流程**:
```
make web-build:
  1. cd web && npm ci
  2. npm run build  → web/dist/
  3. cp -r web/dist/* internal/admin/server/static/web/

make build:
  1. 依赖 web-build
  2. go build  → admin-api 二进制（embed web/dist）
```

**embed 实现**:
```go
//go:embed all:static/web
var webFS embed.FS

func handleAdminPage(w http.ResponseWriter, r *http.Request) {
    distFS, _ := fs.Sub(webFS, "static/web")
    // SPA fallback: 无扩展名路径 → index.html
    if !strings.Contains(path, ".") {
        r2 := r.Clone(r.Context())
        r2.URL.Path = "/"
        http.FileServer(http.FS(distFS)).ServeHTTP(w, r2)
        return
    }
    http.FileServer(http.FS(distFS)).ServeHTTP(w, r)
}
```

**优势**:
- 单二进制交付，部署零依赖
- 与旧 `admin.html` 一致的交付模式，平滑迁移
- 无需 nginx/CDN，降低运维复杂度

**劣势**（未来可优化）:
- 前端改动需要重新编译 Go 二进制
- 无法利用 CDN 缓存（可通过 docker-compose + nginx 升级）

---

## 2. 目录结构

```
web/
├── .env.example          # 环境变量模板
├── .npmrc                # npm 配置（legacy-peer-deps=true）
├── package.json          # 依赖 + scripts
├── vite.config.ts        # Vite 配置（path alias）
├── tsconfig.json         # TS 配置（project references）
├── tsconfig.app.json     # App TS 配置（path alias）
├── components.json       # shadcn/ui 配置
├── index.html            # SPA 入口
├── public/
│   └── favicon.svg
├── src/
│   ├── main.tsx          # React 入口
│   ├── App.tsx           # RouterProvider + QueryClientProvider
│   ├── router.tsx        # 路由定义
│   ├── index.css         # Tailwind + shadcn 全局样式
│   ├── lib/
│   │   ├── api.ts        # axios 实例 + 拦截器
│   │   └── utils.ts      # shadcn cn() helper
│   ├── types/
│   │   └── api.ts        # OpenAPI 生成的 TS types
│   ├── components/
│   │   ├── ProtectedRoute.tsx  # 鉴权 + 导航布局
│   │   └── ui/           # shadcn 组件（button/card/table/...）
│   └── pages/
│       ├── LoginPage.tsx
│       ├── DashboardPage.tsx
│       ├── TokensPage.tsx
│       └── admin/
│           ├── UsersPage.tsx
│           ├── ChannelsPage.tsx
│           ├── LogsPage.tsx
│           └── RedemptionsPage.tsx
└── dist/                 # 构建产物（.gitignore）
```

**关键文件说明**:

- **`package.json` scripts**:
  - `dev`: 开发服务器（Vite HMR）
  - `build`: 生产构建（tsc + vite build）
  - `generate:api`: 从 `../openapi.yaml` 生成 `src/types/api.ts`

- **`.npmrc`**: `legacy-peer-deps=true` 解决 TypeScript 6 与 openapi-typescript 的 peer deps 冲突

- **`tsconfig.json`**: project references 模式，`baseUrl: "."` + `paths: {"@/*": ["./src/*"]}`

- **`vite.config.ts`**: path alias `@` → `./src`，与 tsconfig 对齐

---

## 3. 鉴权设计

### 3.1 用户鉴权（普通用户）

**流程**:
1. 用户在 `/login` 输入 username/password
2. 调用 `POST /api/user/login`，返回 JWT token
3. 存 `localStorage.token`
4. 后续请求通过 `apiClient` 拦截器自动带 `Authorization: Bearer`
5. 401 响应 → 清除 token，重定向 `/login`

**实现**:
```typescript
// src/lib/api.ts
export const apiClient = axios.create({ baseURL: '/api' });

apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('token');
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
});

apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('token');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);
```

### 3.2 管理员鉴权（Admin Token）

**背景**: 当前项目无 `users.role` 字段，管理端通过 `ADMIN_TOKEN` 环境变量鉴权。

**设计决策**: 前端通过 localStorage 存储 Admin Token，用户手动输入后显示管理端导航。

**流程**:
1. 用户点击导航栏 "Admin" 按钮
2. 弹窗输入 `ADMIN_TOKEN`（与后端环境变量一致）
3. 存 `localStorage.adminToken`
4. 显示管理端菜单（Users/Channels/Logs/Redemptions）
5. 管理端 API 调用通过 `adminApiClient` 带 `Authorization: Bearer`
6. 401 响应 → 清除 adminToken，alert 提示重新输入

**实现**:
```typescript
// src/lib/api.ts
export const adminApiClient = axios.create({ baseURL: '/api' });

adminApiClient.interceptors.request.use((config) => {
  const adminToken = localStorage.getItem('adminToken');
  if (adminToken) config.headers.Authorization = `Bearer ${adminToken}`;
  return config;
});

adminApiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('adminToken');
      alert('Admin token invalid or expired. Please re-enter.');
      window.location.reload();
    }
    return Promise.reject(error);
  }
);
```

**为什么不基于 role 字段**:
- 数据库 schema 无 `users.role`
- 避免 schema 变更（migration 风险）
- 上游 one-api 也是 ADMIN_TOKEN 环境变量模式

**未来优化方向**:
- 添加 `users.role` 字段（需 migration）
- `/api/user/self` 返回 role，前端自动判断
- 移除手动输入 Admin Token 的步骤

---

## 4. 路由设计

### 4.1 路由表

```typescript
// src/router.tsx
export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage /> },
  {
    path: '/',
    element: <ProtectedRoute />,  // 鉴权 + 布局
    children: [
      { index: true, element: <Navigate to="/dashboard" /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'tokens', element: <TokensPage /> },
      { path: 'admin/users', element: <AdminUsersPage /> },
      { path: 'admin/channels', element: <AdminChannelsPage /> },
      { path: 'admin/logs', element: <AdminLogsPage /> },
      { path: 'admin/redemptions', element: <AdminRedemptionsPage /> },
    ],
  },
]);
```

### 4.2 SPA Fallback

**问题**: 用户直接访问 `/dashboard` 或刷新页面时，服务端返回 404。

**解决**: admin-api 注册所有 SPA 路由到 `handleAdminPage`，非资源路径返回 `index.html`。

```go
// internal/admin/server/http.go
srv.HandleFunc("/", handleAdminPage)
srv.HandleFunc("/login", handleAdminPage)
srv.HandleFunc("/dashboard", handleAdminPage)
srv.HandleFunc("/tokens", handleAdminPage)
srv.HandleFunc("/admin/users", handleAdminPage)
srv.HandleFunc("/admin/channels", handleAdminPage)
srv.HandleFunc("/admin/logs", handleAdminPage)
srv.HandleFunc("/admin/redemptions", handleAdminPage)
srv.HandleFunc("/assets/", handleAdminPage)  // Vite 构建产物
```

**测试覆盖**:
```go
// internal/admin/server/http_test.go
func TestAdminHTTPPageSPARouteFallback(t *testing.T) {
    for _, path := range []string{"/", "/login", "/dashboard", "/tokens"} {
        // 验证返回 index.html
    }
}
```

---

## 5. 状态管理

### 5.1 服务端状态（TanStack Query）

**所有 API 数据通过 TanStack Query 管理**，不使用 Redux/Zustand。

**示例**:
```typescript
// src/pages/TokensPage.tsx
const { data: tokens, isLoading } = useQuery({
  queryKey: ['tokens'],
  queryFn: async () => {
    const res = await apiClient.get('/token');
    return res.data.data as Token[];
  },
});

const deleteMutation = useMutation({
  mutationFn: async (id: number) => {
    await apiClient.delete(`/token/${id}`);
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['tokens'] });
  },
});
```

**优势**:
- 自动缓存、重试、重新验证
- 乐观更新、后台刷新
- 无需手写 loading/error 状态管理

### 5.2 客户端状态（React useState）

**仅用于 UI 状态**（弹窗开关、表单输入、分页）。

**示例**:
```typescript
const [page, setPage] = useState(1);
const [isCreateOpen, setIsCreateOpen] = useState(false);
```

**不使用全局状态库的理由**:
- 当前无跨页面共享的复杂 UI 状态
- localStorage 已满足 token/adminToken 持久化需求
- 过度设计会增加复杂度

---

## 6. API 对接

### 6.1 OpenAPI Type Generation

**流程**:
```bash
npm run generate:api
# → openapi-typescript ../openapi.yaml -o src/types/api.ts
```

**生成的类型**:
```typescript
// src/types/api.ts (自动生成，1274 行)
export interface paths {
  "/api/user/login": {
    post: {
      requestBody: { content: { "application/json": { username: string; password: string } } };
      responses: { 200: { content: { "application/json": { success: boolean; data: string } } } };
    };
  };
  // ...
}
```

**使用方式**（当前未严格使用，未来可优化）:
```typescript
import type { paths } from '@/types/api';
type LoginRequest = paths['/api/user/login']['post']['requestBody']['content']['application/json'];
```

### 6.2 API 端点映射

| 页面 | 端点 | 方法 | 说明 |
|---|---|---|---|
| **Login** | `/api/user/login` | POST | 返回 JWT token |
| **Dashboard** | `/api/user/self` | GET | 用户信息（quota/used_quota） |
| | `/api/user/dashboard` | GET | 使用统计（按日聚合） |
| **Tokens** | `/api/token` | GET | Token 列表 |
| | `/api/token` | POST | 创建 Token |
| | `/api/token/{id}` | DELETE | 删除 Token |
| **Admin Users** | `/api/user` | GET | 用户列表（分页/搜索） |
| | `/api/user/{id}` | PUT | 更新用户（status） |
| **Admin Channels** | `/api/channel` | GET | 渠道列表 |
| | `/api/channel/{id}` | PUT | 更新渠道（status） |
| | `/api/channel/update_balance/{id}` | GET | 刷新余额 |
| **Admin Logs** | `/api/log` | GET | 账务流水（billing ledger） |
| **Admin Redemptions** | `/api/redemption` | GET | 兑换码列表 |
| | `/api/redemption` | POST | 创建兑换码 |
| | `/api/redemption/{code}` | DELETE | 删除兑换码 |

### 6.3 响应格式统一处理

**后端统一响应格式**:
```json
{
  "success": true,
  "message": "",
  "data": { ... }
}
```

**前端解包**:
```typescript
const res = await apiClient.get('/token');
return res.data.data;  // 直接取 data 字段
```

**错误处理**（当前简化，Sprint 3 优化）:
```typescript
try {
  await apiClient.post('/token', { name });
} catch (err: any) {
  // 当前: 静默失败或 console.error
  // Sprint 3: toast 提示 err.response?.data?.message
}
```

---

## 7. UI/UX 设计

### 7.1 设计风格

**参照**: 现代 Console 风格（Vercel / Supabase / Linear）

**特点**:
- 默认暗色 + 可切浅色（shadcn 原生支持）
- 高信息密度（表格紧凑、字号 13-14px）
- 克制的留白、无过度动画
- 单色调为主（紫色 accent）

### 7.2 组件使用规范

**shadcn/ui 组件清单**:
- `Button`: 主操作（variant: default/outline/destructive）
- `Card`: 容器（Dashboard 卡片）
- `Table`: 列表展示（用户/渠道/日志）
- `Dialog`: 弹窗（创建 Token/Redemption、输入 Admin Token）
- `Input`: 表单输入
- `Label`: 表单标签

**自定义组件**:
- `ProtectedRoute`: 鉴权 + 导航布局

### 7.3 响应式设计

**当前状态**: 基础响应式（Tailwind breakpoints）

**断点**:
- `sm`: 640px（移动端竖屏）
- `md`: 768px（平板）
- `lg`: 1024px（桌面）

**已适配**:
- Dashboard 卡片: `grid-cols-1 sm:grid-cols-2 lg:grid-cols-4`
- 表格: 横向滚动（`overflow-x-auto`）

**未适配**（Sprint 3 可优化）:
- 移动端导航（汉堡菜单）
- 表格列隐藏（小屏只显示关键列）

### 7.4 暗色模式

**实现**: shadcn/ui 默认支持，通过 CSS 变量切换。

**当前状态**: 跟随系统（`prefers-color-scheme`）

**未来优化**: 添加手动切换按钮（localStorage 持久化）

---

## 8. 性能优化

### 8.1 Bundle Size

**当前**:
- `index.js`: ~820 KB (gzipped ~252 KB)
- `index.css`: ~43 KB (gzipped ~9 KB)

**警告**: Vite 提示 chunk > 500 KB，建议 code splitting。

**优化方向**（Sprint 3+）:
1. **动态导入**: 管理端页面按需加载
   ```typescript
   const AdminUsersPage = lazy(() => import('@/pages/admin/UsersPage'));
   ```
2. **recharts tree-shaking**: 只导入用到的图表组件
3. **shadcn 组件按需**: 已是按需（组件代码在仓里）

### 8.2 请求优化

**TanStack Query 缓存**:
- 默认 5 分钟 stale time
- 窗口聚焦自动 refetch（可配置关闭）

**分页**:
- 当前: 简单分页（page/page_size）
- 未来: 虚拟滚动（表格数据 > 1000 行时）

### 8.3 渲染优化

**当前**: 无明显性能瓶颈（列表 < 100 项）

**未来**:
- `React.memo` 包裹表格行组件
- `useMemo` 缓存过滤/排序结果

---

## 9. 测试策略

### 9.1 后端集成测试

**覆盖**:
- SPA fallback（`TestAdminHTTPPageSPARouteFallback`）
- SPA shell 加载（`TestAdminHTTPPageIsServed`）

**示例**:
```go
func TestAdminHTTPPageSPARouteFallback(t *testing.T) {
    srv := NewHTTPServer(":0", nil)
    for _, path := range []string{"/", "/login", "/dashboard", "/tokens"} {
        req := httptest.NewRequest(http.MethodGet, path, nil)
        rec := httptest.NewRecorder()
        srv.ServeHTTP(rec, req)
        if rec.Code != http.StatusOK {
            t.Fatalf("path %s status = %d, want 200", path, rec.Code)
        }
        if !strings.Contains(rec.Body.String(), `<div id="root">`) {
            t.Fatalf("path %s did not fall back to SPA shell", path)
        }
    }
}
```

### 9.2 前端测试（当前缺失）

**未来补充**:
1. **单元测试**: Vitest + React Testing Library
   - 组件渲染
   - 用户交互（点击/输入）
   - API mock（MSW）

2. **E2E 测试**: Playwright
   - 登录流程
   - Token CRUD
   - 管理端操作

**优先级**: Sprint 4+（当前功能稳定后再补）

---

## 10. 已知限制与未来优化

### 10.1 当前限制

| 限制 | 影响 | 优先级 |
|---|---|---|
| **错误处理简陋** | API 失败无 toast 提示，用户无感知 | P0（Sprint 3） |
| **加载状态粗糙** | 只有 "Loading..." 文本，无 skeleton | P1（Sprint 3） |
| **无表格排序/筛选** | 大数据量时体验差 | P2 |
| **无导出功能** | 无法导出用户/日志列表 | P3 |
| **移动端适配不足** | 小屏表格难用 | P2 |
| **无暗色模式切换** | 只能跟随系统 | P2 |
| **Bundle size 大** | 首屏加载慢（~820 KB） | P1（code splitting） |
| **无前端测试** | 重构风险高 | P2 |

### 10.2 Sprint 3 计划

**目标**: 提升用户体验，补齐基础交互

**范围**:
1. **错误处理增强**（P0）
   - 安装 `sonner` toast 库
   - API 失败统一 toast 提示
   - 表单验证错误提示

2. **加载状态优化**（P0）
   - Skeleton 组件（表格/卡片）
   - 空状态插图（无数据时）

3. **Options 配置页**（P1）
   - `/api/option` GET/PUT
   - 系统选项 CRUD（注册开关/邀请奖励/默认 quota）

4. **暗色模式切换**（P1）
   - 导航栏加切换按钮
   - localStorage 持久化

5. **Code splitting**（P1）
   - 管理端页面动态导入
   - recharts 按需加载

### 10.3 Sprint 4+ 展望

- 表格高级交互（排序/筛选/导出）
- 移动端优化（汉堡菜单/列隐藏）
- 前端单元测试 + E2E 测试
- 国际化（i18n）
- 用户偏好设置（语言/时区/分页大小）

---

## 11. 开发指南

### 11.1 本地开发

```bash
# 1. 安装依赖
cd web
npm install

# 2. 生成 API types
npm run generate:api

# 3. 启动开发服务器（HMR）
npm run dev
# → http://localhost:5173

# 4. 后端 API 代理（vite.config.ts 配置）
# 开发时前端调 /api/* 会代理到 http://localhost:8000
```

**环境变量**:
```bash
# web/.env
VITE_API_BASE_URL=/api  # 生产环境用相对路径
```

### 11.2 生产构建

```bash
# 方式 1: Makefile（推荐）
make web-build  # npm ci + build + copy to static/web
make build      # go build with embed

# 方式 2: 手动
cd web
npm ci
npm run build
cd ..
go build ./cmd/admin-api
```

### 11.3 添加新页面

1. **创建页面组件**:
   ```typescript
   // web/src/pages/NewPage.tsx
   export function NewPage() {
     returnNew Page;
   }
   ```

2. **注册路由**:
   ```typescript
   // web/src/router.tsx
   import { NewPage } from '@/pages/NewPage';
   
   children: [
     // ...
     { path: 'new', element: <NewPage /> },
   ]
   ```

3. **注册 SPA fallback**:
   ```go
   // internal/admin/server/http.go
   srv.HandleFunc("/new", handleAdminPage)
   ```

4. **添加导航链接**:
   ```typescript
   // web/src/components/ProtectedRoute.tsx
   <a href="/new">New
   ```

### 11.4 添加 shadcn 组件

```bash
cd web
npx shadcn@latest add <component-name>
# 例如: npx shadcn@latest add toast
```

组件代码会写入 `src/components/ui/<component-name>.tsx`，可直接修改。

---

## 12. 部署检查清单

- [ ] `make web-build` 成功
- [ ] `make build` 成功
- [ ] `go test ./...` 通过
- [ ] 浏览器访问 `http://localhost:8000/` 能看到登录页
- [ ] 登录后能访问 `/dashboard`、`/tokens`
- [ ] 输入 Admin Token 后能访问 `/admin/*` 页面
- [ ] 刷新页面不 404（SPA fallback 生效）
- [ ] `/assets/*` 静态资源加载正常
- [ ] 暗色模式跟随系统

---

## 13. 参考资料

- [shadcn/ui 文档](https://ui.shadcn.com/)
- [TanStack Query 文档](https://tanstack.com/query/latest)
- [Vite 文档](https://vitejs.dev/)
- [React Router 文档](https://reactrouter.com/)
- [openapi-typescript](https://github.com/drwpow/openapi-typescript)
- [Tailwind CSS 文档](https://tailwindcss.com/)
- [recharts 文档](https://recharts.org/)

---

## 附录 A: 技术债务追踪

| 债务 | 引入时间 | 影响 | 计划清理 |
|---|---|---|---|
| OpenAPI types 未严格使用 | Sprint 1 | 类型安全不足 | Sprint 4 |
| 无前端测试 | Sprint 1 | 重构风险 | Sprint 4 |
| Bundle size 大 | Sprint 1 | 首屏慢 | Sprint 3 |
| 错误处理简陋 | Sprint 1 | 用户体验差 | Sprint 3 |
| Admin Token 手动输入 | Sprint 2 | 操作繁琐 | 等 role 字段 |
| 移动端适配不足 | Sprint 1 | 小屏难用 | Sprint 4 |

---

## 附录 B: 变更日志

### v1.0 (2026-05-20)
- 初始版本，覆盖 Sprint 1+2 设计与实现
- 架构决策、技术栈、鉴权、路由、API 对接、UI/UX、性能、测试
- 已知限制与 Sprint 3 计划
