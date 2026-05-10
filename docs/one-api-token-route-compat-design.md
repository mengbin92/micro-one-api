# One-API Token 路由兼容设计

## 背景

`one-api` 的用户 token 管理路由包括：

- `GET /api/token/`
- `GET /api/token/search`
- `GET /api/token/:id`
- `POST /api/token/`
- `PUT /api/token/`
- `DELETE /api/token/:id`

当前项目已有 token CRUD handler，但 HTTP 路由只注册了 `/api/token` 和 `/api/token/`，路径型 `/api/token/:id`、`/api/token/search` 兼容性不足。同时当前 PUT 要求路径 id，而 One API 的 PUT 使用 body 中的 `id`。

## 目标

补齐 token 路由兼容：

1. `GET /api/token/search?keyword=...`
2. `GET /api/token/:id`
3. `DELETE /api/token/:id`
4. `PUT /api/token/` 支持 body `id`

## 非目标

本期不做：

1. One API token 的 subnet、accessed_time 等完整字段。
2. token status_only 的全部边界规则。
3. 前端 token 管理页面。

## 架构

继续复用 `identity-service` 的 `handleTokens`：

- 增加 `srv.HandlePrefix("/api/token/", ...)`，让 `/api/token/:id` 和 `/api/token/search` 进入同一 handler。
- `parseTokenID` 识别 `search` 为非 token id。
- `GET /api/token/search` 使用 query keyword 调用 `ListAccessTokens`。
- `PUT /api/token/` 如果路径没有 id，则从 body `id` 读取。

## 响应格式

继续使用 One API 风格：

```json
{
  "success": true,
  "message": "",
  "data": {}
}
```

## 测试策略

1. `GET /api/token/search?keyword=...` 返回匹配 token。
2. `GET /api/token/:id` 返回指定 token。
3. `DELETE /api/token/:id` 删除指定 token。
4. `PUT /api/token/` body 带 `id` 可更新 token。

## 验收标准

1. `go test ./internal/identity/server -run 'TestIdentityHTTPToken' -count=1` 通过。
2. `go test ./...` 通过。
3. `go build ./...` 通过。
