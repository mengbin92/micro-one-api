# 管理员鉴权迁移到 per-user role 方案（方向 B）

> 背景：最近 `b52d5b4` / `acf78a8` 两次提交在 identity 引入了基于角色的管理员体系
> （`users.role` + `SetUserRole` + operator-vs-target 校验），但 admin-api 的认证层
> 仍是共享 `ADMIN_TOKEN`。本方案把 admin-api 的认证迁移到“用户会话 token + role ≥ admin”，
> 让管理员登录后无需再输入共享 token 即可使用管理后台。
>
> 方案先行，实施前对齐。

## 1. 现状分析

### 1.1 后端认证链路

- `internal/admin/server/http.go` 的 `AdminAuth(next)` 中间件包裹了约 30 个管理端点
  （`/api/user*`、`/v1/users*`、`/v1/channels*`、`/v1/system/options`、`/api/log*`、
  `/v1/redeem-codes*`、`/api/payment/orders*` 等）。
- `AdminAuth` 仅做一件事：比对 `Authorization: Bearer <token>` 与环境变量 `ADMIN_TOKEN`
  （constant-time compare）。`ADMIN_TOKEN` 未配置时**所有**受保护端点直接返回 403。
- 用户态端点（`/api/user/self`、`/api/user/login` 等）走 `identityProxy` 反向代理，
  由 identity-service 用用户会话 token 自行鉴权，不经过 `AdminAuth`。

### 1.2 token 模型

- 登录（`biz.Login`）成功后在 `tokens` 表创建一条记录并返回其 `key`，前端存入
  `localStorage.token`。这条会话 token 与 API access token 同表同构。
- `identity.ValidateToken(token)` → 校验 token 有效性，返回 `user_id`（不含 role）。
- `identity.GetUser(user_id)` → 返回 `UserInfo`，**已包含 `role`**（见
  `internal/identity/service/identity.go` GetUser，字段在 `acf78a8` 后补齐）。

### 1.3 admin-service 的依赖

- `service.AdminService` 已持有 `identityClient identityv1.IdentityServiceClient`
  （`internal/admin/service/admin.go`），可直接调用 `ValidateToken` / `GetUser`。

### 1.4 前端现状（第一步完成后）

- `adminApiClient` 用 `localStorage.adminToken` 作为 `Authorization`，并已透传
  `X-Operator-User-Id`（来自 `localStorage.userId`）。
- `AppNavigation` 通过探测 `GET /api/admin/access` 判断是否显示管理导航；
  入口按钮在 `adminToken` 存在/不存在间切换“退出管理 / 进入管理”。
- `AdminRoute` 在无 `adminToken` 时渲染一个共享 token 输入框。
- `canAccessAdmin({adminToken, snapshot})` = `Boolean(adminToken || snapshot.admin)`。

### 1.5 问题

纯前端把入口判定改成 role、并去掉 token 输入框，会导致 `adminApiClient` 不再携带可被
`AdminAuth` 接受的凭据 → 所有管理请求 401。**必须同时改造后端认证层。**

## 2. 设计目标

1. 管理员用**自己的会话 token**（登录态）即可访问 admin-api，无需共享 `ADMIN_TOKEN`。
2. 保留 `ADMIN_TOKEN` 作为兼容/运维后路（CI、`admin-reset` CLI、无浏览器场景）。
3. operator 身份由后端从 token 解析，**不信任**前端伪造的 `X-Operator-User-Id`。
4. 非管理员用户访问管理后台时得到明确的“无权限”提示，而非空白/报错。
5. 既有基于 `ADMIN_TOKEN` 的后端测试保持通过。

## 3. 后端方案

### 3.1 `AdminAuth` 升级为双凭据守卫

把包级 `AdminAuth(next)` 改为由 `NewHTTPServer` 内部构造的工厂闭包
`adminGuard := newAdminGuard(svc)`，签名仍为 `func(http.HandlerFunc) http.HandlerFunc`，
30 处调用点做机械替换 `AdminAuth(` → `adminGuard(`。

守卫逻辑（伪代码）：

```
adminToken := os.Getenv("ADMIN_TOKEN")
return func(next) http.HandlerFunc {
  return func(w, r) {
    bearer := parseBearer(r)            // 缺失 → 401
    // 路径 A：共享 ADMIN_TOKEN（系统级，兼容）
    if adminToken != "" && constantTimeEq(bearer, adminToken) {
      next(w, r); return                // operator id 仍取前端 X-Operator-User-Id（可空=系统级）
    }
    // 路径 B：用户会话 token + role 校验
    if svc != nil {
      userID, role, err := svc.AuthorizeAdminToken(r.Context(), bearer)
      if err == nil && role >= roleAdmin /*=10*/ {
        // 用真实操作者覆盖前端传入值，杜绝伪造
        r.Header.Set("X-Operator-User-Id", strconv.FormatInt(userID, 10))
        next(w, r); return
      }
    }
    401 / 403
  }
}
```

关键点：
- `ADMIN_TOKEN` 未配置时**不再**无条件 403：路径 B 仍可工作。
- 路径 B 命中时强制重写 `X-Operator-User-Id` 为真实 userID，使
  `acf78a8` 的 operator-vs-target 校验真正生效且不可被前端绕过。
- `svc == nil`（部分仅测试静态资源的 `NewHTTPServer(":0", nil)`）时跳过路径 B，
  不 panic。

### 3.2 admin-service 新增授权方法

`internal/admin/service/admin.go`：

```go
const RoleAdmin int32 = 10 // 与 biz.RoleAdminUser 对齐；admin 包不依赖 identity/biz

// AuthorizeAdminToken 校验用户会话 token 并返回其 user id 与 role。
func (s *AdminService) AuthorizeAdminToken(ctx context.Context, token string) (int64, int32, error) {
    vr, err := s.identityClient.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: token})
    if err != nil || !vr.GetValid() {
        return 0, 0, errUnauthorized
    }
    ur, err := s.identityClient.GetUser(ctx, &identityv1.GetUserRequest{UserId: vr.GetUserId()})
    if err != nil {
        return 0, 0, err
    }
    return vr.GetUserId(), ur.GetUser().GetRole(), nil
}
```

> 两次 RPC（ValidateToken + GetUser）。相比扩展 `ValidateTokenReply.role`，
> 这样只在 admin 认证路径增加一次调用，不影响 relay 等 ValidateToken 的高频调用方。

### 3.3 `/api/admin/access` 回传 role

`handleAdminAccess` 当前硬编码 `{admin: true}`。改为反映真实角色，供前端缓存判定：

- 路径 A（ADMIN_TOKEN）：`{admin: true, role: 100}`（视为 root 等价）。
- 路径 B（user token）：`{admin: true, role: <真实 role>}`。

实现上守卫已知道走哪条路径与 role，可将 role 通过 request context 传给 handler，
或 handler 自行从（已被守卫重写的）`X-Operator-User-Id` + GetUser 读取。
倾向用 context 传递，避免重复 RPC。

## 4. 前端方案

### 4.1 凭据来源切换

`web/src/lib/api.ts`：`adminApiClient` 的 `Authorization` 改用 `localStorage.token`
（与 `apiClient` 同源的用户会话 token），不再用 `adminToken`。
保留写入 `X-Operator-User-Id`（后端会覆盖，留作 ADMIN_TOKEN 调试无害）。
401 处理：清 `token` / `userId` / `userRole` 并跳登录。

### 4.2 role 持久化

`AppNavigation`（已在第一步拉 `/user/self`）在拿到 `self.role` 时
`localStorage.setItem('userRole', String(self.role))`；`handleLogout` 与 401 拦截器
一并清除 `userRole`。

### 4.3 `canAccessAdmin` 基于 role

`web/src/lib/admin-access.ts`：

```ts
export const ROLE_ADMIN = 10;
export function isAdminRole(role?: number | null) {
  return typeof role === 'number' && role >= ROLE_ADMIN;
}
export function canAccessAdmin({ role, snapshot }) {
  return isAdminRole(role) || isAdminRole(snapshot?.role) || Boolean(snapshot?.admin);
}
```

### 4.4 `AppNavigation` 入口判定

- 读 `localStorage.userRole`（或从已拉取的 `self.role`）判 `canAccessAdmin`。
- 移除“进入管理 / 退出管理”的共享 token 流与 `/api/admin/access` 探测
  （或保留探测作为 role 缺失时的兜底，二选一；倾向直接用 role）。
- role ≥ 10 才渲染“管理后台”导航分组。

### 4.5 `AdminRoute` 改为无权限提示

- role ≥ 10 → `<Outlet />`。
- 否则渲染“需要管理员权限，请联系超级管理员”卡片，移除 token 输入框。

## 5. 安全考量

- **operator 不可伪造**：路径 B 强制用 token 解析出的 userID 覆盖 `X-Operator-User-Id`。
- **越权防护**：实际的 promote/demote 越权仍由 identity 的 operator-vs-target 校验兜底
  （admin 不能动 root、不能授予 ≥ 自身等级）。
- **token 复用面**：会话 token 与 API access token 同表，admin 用户的任一 access token
  也能过 admin 认证。属现有设计局限，本方案不收敛；如需区分需引入 token 类型，列为未来工作。
- **ADMIN_TOKEN 仍是 root 级**：路径 A 等价 root，需继续作为机密管理。

## 6. 兼容性与迁移

- `ADMIN_TOKEN` 路径完全保留，CI / `admin-reset` / 现有后端测试不受影响。
- 已登录的普通浏览器用户：清掉 `adminToken` 概念后，是否能进管理后台完全由其 role 决定。
- 首个管理员仍由 identity 的 env bootstrap（`INITIAL_ADMIN_*`）产生。

## 7. 测试计划

### 后端

- 新增 mock `ValidateToken` 到 `adminHTTPIdentityClient`，并让 `GetUser` 可返回指定 role。
- 用例：
  - user token + role=10 → 受保护端点 200，且下游收到的 `X-Operator-User-Id` 为真实 userID。
  - user token + role=1 → 403。
  - 无 `ADMIN_TOKEN` 配置 + user token role=10 → 200（验证不再无条件 403）。
  - 共享 `ADMIN_TOKEN` 路径 → 保持 200（回归）。
  - 前端伪造 `X-Operator-User-Id` 在路径 B 下被覆盖。

### 前端

- `admin-access.test.ts`：role≥10 通过、role<10 拒绝、snapshot.role 与 admin 兜底。
- `AppNavigation.test.tsx`：role≥10 显示管理导航；role<10 不显示且无 token 输入。
- `AdminRoute`（如有测试）：role<10 显示无权限提示。

## 8. 实施顺序

1. 后端：proto 无需改动（GetUser 已含 role）。
2. 后端：`AdminService.AuthorizeAdminToken` + `newAdminGuard` + 30 处替换 + `/api/admin/access` 回传 role。
3. 后端：补 `http_test.go` 用例，`go build ./... && go test ./internal/admin/...`。
4. 前端：`api.ts` 凭据切换、`admin-access.ts`、`AppNavigation`、`AdminRoute`、role 持久化。
5. 前端：更新测试，`tsc -b && npm test && npm run lint`（注意 RechargePage 既有 lint 错与本次无关）。
6. 提交。

## 9. 未来工作（不在本次范围）

- 区分会话 token 与 API access token（token 类型/scope），收敛 admin 认证面。
- `AdminAuth` 路径 A 逐步下线，`ADMIN_TOKEN` 仅保留给 break-glass。
- 管理操作审计日志记录真实 operator。
