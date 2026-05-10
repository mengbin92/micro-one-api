# One-API 邮箱绑定兼容设计

## 背景

`one-api` 提供 `GET /api/oauth/email/bind` 让已登录用户使用邮箱验证码绑定或更新邮箱。当前项目已有：

- `GET /api/verification?email=...`：生成并保存邮箱验证码。
- Bearer token 用户鉴权能力。
- 用户资料更新能力。

但还没有 One API 兼容的邮箱绑定入口。

## 目标

补齐：

- `GET /api/oauth/email/bind?email=<email>&code=<code>`

鉴权继续使用：

- `Authorization: Bearer <token>`

成功响应：

```json
{
  "success": true,
  "message": ""
}
```

## 非目标

本期不做：

1. 真实邮件发送。
2. 邮箱域名白名单。
3. Turnstile 风控。
4. OAuth/OIDC/飞书/微信完整登录流程。

## 架构

新增 identity usecase 方法：

```go
func (uc *IdentityUsecase) UpdateSelfEmail(ctx context.Context, userID int64, email string) error
```

该方法只更新当前用户邮箱，不改变 username、display name、group、status、password 等字段。

HTTP 入口复用当前 `verificationStore` 中 `v:<email>` 的验证码：

1. Bearer token 获取当前用户。
2. 校验 `email` 和 `code` 非空。
3. 校验 `verificationStore["v:"+email]` 存在且 code 匹配。
4. 调用 `UpdateSelfEmail`。

## 错误处理

- 未授权：HTTP 401，`success=false`。
- email/code 缺失：HTTP 200，`success=false`。
- 验证码错误或不存在：HTTP 200，`success=false`。
- 用户不存在或更新失败：HTTP 200，`success=false`。

## 测试策略

1. Usecase：
   - `UpdateSelfEmail` 更新 email。
   - `UpdateSelfEmail` 保持 group/status 等字段不变。

2. HTTP：
   - 未授权返回 401。
   - 错误验证码返回 `success=false`。
   - 通过 `/api/verification` 获取验证码后绑定成功。
   - 绑定成功后 `GET /api/user/self` 返回新邮箱。

## 验收标准

1. `go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelfEmail' -count=1` 通过。
2. `go test ./internal/identity/server -run 'TestIdentityHTTPEmailBind' -count=1` 通过。
3. `go test ./...` 通过。
4. `go build ./...` 通过。
