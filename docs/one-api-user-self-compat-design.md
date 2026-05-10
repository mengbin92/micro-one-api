# One-API 用户 Self 更新与删除兼容设计

## 背景

`one-api` 在 `/api/user/self` 下提供用户自助资料管理：

- `GET /api/user/self`：当前项目已实现。
- `PUT /api/user/self`：更新当前用户的用户名、显示名和密码。
- `DELETE /api/user/self`：删除当前用户。

当前 `micro-one-api` 只实现了 `GET /api/user/self`，管理侧 `UpdateUser` 也主要面向管理员修改用户展示名、邮箱、分组和状态，不适合直接复用为用户自助资料更新。

## 目标

补齐 One API 兼容用户自助资料接口：

1. `PUT /api/user/self`
2. `DELETE /api/user/self`

响应继续使用 One API 风格：

```json
{
  "success": true,
  "message": ""
}
```

## 非目标

本期不做：

1. root 用户删除保护的角色体系迁移，当前 identity 用户模型没有 One API 的 root role 字段。
2. 邮箱绑定 `/api/oauth/email/bind`，单独作为 OAuth/email 兼容项处理。
3. 用户头像、主题、偏好设置等前端体验字段。
4. session/cookie 鉴权，继续使用 Bearer token。

## 架构

新增 identity usecase 方法，专门表达用户自助更新语义：

```go
func (uc *IdentityUsecase) UpdateSelf(ctx context.Context, userID int64, username, displayName, password string) error
```

语义：

- `username` 非空时更新用户名，并校验不能与其他用户重复。
- `display_name` 非空时更新展示名。
- `password` 非空时更新密码 hash，最短 8 位，与注册/重置密码一致。
- 保持用户 group、status、email、quota、aff 字段不变。

HTTP server 在 `handleSelf` 中按方法分发：

- `GET`：保留现有行为。
- `PUT`：调用 `UpdateSelf`。
- `DELETE`：调用现有 `DeleteUser` 删除当前用户。

## API 设计

### PUT /api/user/self

鉴权：

- `Authorization: Bearer <token>`

请求：

```json
{
  "username": "alice2",
  "display_name": "Alice",
  "password": "newpass123"
}
```

成功响应：

```json
{
  "success": true,
  "message": ""
}
```

失败响应：

```json
{
  "success": false,
  "message": "..."
}
```

### DELETE /api/user/self

鉴权：

- `Authorization: Bearer <token>`

成功响应：

```json
{
  "success": true,
  "message": ""
}
```

## 错误处理

- 未授权：HTTP 401，`success=false`。
- 非法 JSON：HTTP 400，`success=false`。
- 用户名重复：HTTP 200，`success=false`。
- 密码过短：HTTP 200，`success=false`。
- 删除失败：HTTP 200，`success=false`。

## 测试策略

1. Usecase 测试：
   - `UpdateSelf` 可更新 username/display name。
   - `UpdateSelf` 拒绝重复 username。
   - `UpdateSelf` 可更新 password 并允许新密码登录。

2. HTTP server 测试：
   - `PUT /api/user/self` 未授权返回 401。
   - `PUT /api/user/self` 成功更新当前用户。
   - `DELETE /api/user/self` 未授权返回 401。
   - `DELETE /api/user/self` 删除当前用户后 token 不再可用于 `GET /api/user/self`。

## 文档更新

实现完成后更新：

- `docs/one-api-full-gap-analysis-20260509.md`
- 保留 `/api/oauth/email/bind` 等未实现项为后续缺口。

## 验收标准

1. `go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelf' -count=1` 通过。
2. `go test ./internal/identity/server -run 'TestIdentityHTTPSelf' -count=1` 通过。
3. `go test ./...` 通过。
4. `go build ./...` 通过。
