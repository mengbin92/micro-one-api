# 订阅套餐用量查询接口

## 背景

为了方便在 `cc-switch` 这类工具中查询用户订阅套餐的使用情况（日/周/月限额、已用、剩余、下次刷新时间），新增一个 **API Key 鉴权** 的查询接口。它与 relay-gateway 上的 `/v1/usage`（钱包余额查询）并列，面向开通了订阅套餐的用户。

同时，web 端「我的订阅」页面现在也会展示日/周/月限额的 **下次刷新时间点**，对用户更友好。

## 接口

### `GET /v1/subscription/usage`

**鉴权**：与 `/v1/chat/completions` 相同的 Bearer Token（用户 API Key）。

```
GET /v1/subscription/usage
Authorization: Bearer sk-xxxxxxxx
```

**响应（有活跃订阅）**：

```json
{
  "success": true,
  "isValid": true,
  "is_active": true,
  "status": "active",
  "mode": "subscription",
  "planName": "Pro 套餐",
  "unit": "USD",
  "user_id": "42",
  "data": {
    "id": 7,
    "status": "active",
    "starts_at": 1700000000,
    "expires_at": 1800000000,
    "group_id": 3,
    "subscription_name": "Pro 套餐",
    "remaining_seconds": 864000,
    "daily_used": {
      "used": 5.2,
      "limit": 10,
      "remaining": 4.8,
      "next_refresh": 1700086400
    },
    "weekly_used": {
      "used": 12.3,
      "limit": 70,
      "remaining": 57.7,
      "next_refresh": 1700604800
    },
    "monthly_used": {
      "used": 45.6,
      "limit": 300,
      "remaining": 254.4,
      "next_refresh": 1702590400
    }
  }
}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否有活跃订阅 |
| `mode` | string | 固定为 `subscription`，用于与 `/v1/usage`（钱包）区分 |
| `planName` | string | 套餐名（取订阅组的 `display_name`，缺失时回退到 `name`） |
| `unit` | string | 固定 `USD` |
| `data.status` | string | `active` / `expired` / `revoked` |
| `data.remaining_seconds` | int | 距到期剩余秒数 |
| `data.*_used.used` | float | 当前窗口已用（USD） |
| `data.*_used.limit` | float\|null | 限额；`null` 表示无限制 |
| `data.*_used.remaining` | float | 剩余（limit - used） |
| `data.*_used.next_refresh` | int | **下次刷新的 Unix 时间戳**，即该窗口重置、用量归零的时间点 |

**响应（无活跃订阅 / 未开通订阅服务）**：

```json
{
  "success": false,
  "isValid": false,
  "is_active": false,
  "mode": "subscription",
  "message": "no active subscription"
}
```

> 无活跃订阅返回 HTTP 200 + `success:false`，而不是 4xx/5xx，便于 `cc-switch` 这类工具直接展示「无订阅」而非报错。

## 在 cc-switch 中接入

`cc-switch` 的「NewAPI 模板」目前请求 `{{baseUrl}}/api/user/self`，需要登录态 access token + user id。本接口与之互补：只需 API Key 即可查询订阅用量，无需额外登录态。

可在 `cc-switch` 的「Custom 模板」中配置：

```js
({
  request: {
    url: "{{baseUrl}}/v1/subscription/usage",
    method: "GET",
    headers: {
      "Authorization": "Bearer {{apiKey}}"
    }
  },
  extractor: function (response) {
    if (response.success && response.data) {
      var d = response.data;
      return {
        isValid: d.status === "active",
        planName: response.planName,
        remaining: d.daily_used.remaining,
        total: d.daily_used.limit,
        used: d.daily_used.used,
        unit: "USD"
      };
    }
    return {
      isValid: false,
      invalidMessage: response.message || "no active subscription"
    };
  }
})
```

如需展示「下次刷新时间」，可在 extractor 里读取 `d.daily_used.next_refresh` / `d.weekly_used.next_refresh` / `d.monthly_used.next_refresh`（Unix 秒），按本地时区格式化。

## 实现位置

- 路由注册：`internal/relay/server/routes.go`（`/v1/subscription/usage`）
- handler：`internal/relay/server/http.go`（`handleSubscriptionUsage`）
- 业务逻辑：`internal/subscription/biz/subscription_usecase.go`（`GetProgress`，已扩展 `next_refresh` 与 `subscription_name`/`group_id`）
- 实体：`internal/subscription/biz/entity.go`（`QuotaDimension.NextRefresh`、`SubscriptionProgress.GroupID`/`SubscriptionName`）
- web 展示：`web/src/components/SubscriptionProgress.tsx`（`QuotaBar` 渲染「Xh后刷新」）
