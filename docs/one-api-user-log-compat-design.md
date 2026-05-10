# One-API 用户日志兼容设计

## 背景

`one-api` 用户侧提供自助日志接口：

- `GET /api/log/self`：查看当前用户日志。
- `GET /api/log/self/search`：按关键词搜索当前用户日志。
- `GET /api/log/self/stat`：查看当前用户日志统计。

当前 `micro-one-api` 有独立 `log-service`，但 HTTP 侧主要暴露 service-token 保护的 `/v1/logs`，缺少 One API 风格用户自助日志入口。`admin-api` 中已有 `/api/log/self/stat` 路由，但它使用 admin 鉴权且要求显式 `user_id`，不符合 One API 的用户 Bearer token 语义。

## 目标

补齐用户自助日志兼容入口：

1. `GET /api/log/self`
2. `GET /api/log/self/search`
3. `GET /api/log/self/stat`

响应使用 One API 风格：

```json
{
  "success": true,
  "message": "",
  "data": []
}
```

## 非目标

本期不做：

1. 完整 One API 日志字段迁移，例如 token name、model name、channel id、quota 等专用字段。
2. dashboard 按天/模型图表聚合。
3. 改造 admin-api 的日志查询架构。
4. 生成新的 protobuf 字段或破坏现有 gRPC 客户端。

## 架构

在 `log-service` HTTP server 中新增可选 identity client：

```go
func NewHTTPServer(addr string, svc *service.LogService, identityClients ...identityv1.IdentityServiceClient) *khttp.Server
```

用户自助日志接口通过 Bearer token 调用 `identity.GetAuthSnapshot` 获取当前用户 ID。未配置 identity client 时返回 503，避免生产 wiring 尚未接入时误开放接口。

log usecase/repo 新增用户过滤方法：

```go
func (uc *LogUsecase) ListUserLogs(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*LogEntry, int64, error)
```

仓储层扩展查询条件时保持现有 `ListLogs` 行为不变，避免影响 `/v1/logs` 和 gRPC `ListLogs`。

## API 设计

### GET /api/log/self

鉴权：

- `Authorization: Bearer <token>`

查询参数：

- `p`：One API 页码，从 0 开始。
- `page`：兼容页码，从 1 开始。
- `page_size`：默认 20。
- `type`：映射到当前日志 `level`。

响应：

```json
{
  "success": true,
  "message": "",
  "data": [
    {
      "id": 1,
      "type": "info",
      "message": "request completed",
      "source": "relay-gateway",
      "request_id": "req-1",
      "user_id": 2,
      "created_at": 1710000000
    }
  ]
}
```

### GET /api/log/self/search

查询参数：

- `keyword`：匹配 message。

其他鉴权和响应格式与 `/api/log/self` 一致。

### GET /api/log/self/stat`

返回当前用户过滤后的轻量统计：

```json
{
  "success": true,
  "message": "",
  "data": {
    "total": 2,
    "sampled_count": 2,
    "count_by_type": {
      "info": 1,
      "error": 1
    }
  }
}
```

## 错误处理

- 缺少或非法 Bearer token：HTTP 401，`success=false`。
- identity client 未配置：HTTP 503，`success=false`。
- identity gRPC error：HTTP 401，`success=false`。
- log 查询失败：HTTP 200，`success=false`。
- 非 GET 方法：HTTP 405，`success=false`。

## 测试策略

1. log data 测试：
   - 用户过滤只返回指定 `user_id` 的日志。
   - 用户过滤可叠加 keyword。

2. log service/server HTTP 测试：
   - `/api/log/self` 未授权返回 401。
   - 未配置 identity client 返回 503。
   - `/api/log/self` 只返回当前用户日志。
   - `/api/log/self/search` 只搜索当前用户日志。
   - `/api/log/self/stat` 返回当前用户统计。

## 文档更新

实现完成后更新：

- `docs/one-api-full-gap-analysis-20260509.md`

并修正文档里已经实现但仍列为缺失的内容/分组/渠道测试类条目。

## 验收标准

1. `go test ./internal/log/... -count=1` 通过。
2. `go test ./...` 通过。
3. `go build ./...` 通过。
