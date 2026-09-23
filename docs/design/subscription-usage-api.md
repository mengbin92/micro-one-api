# 订阅套餐用量查询接口

> 2026-09-23：第二批 O3 已实现并通过本地验收，未部署。Relay 与 admin 的 progress 接口均优先读取 billing 权威快照。

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
    "rate_multiplier": 2,
    "usage_source": "billing",
    "observed_at": 1700003600,
    "daily_used": {
      "used": 5.2,
      "settled": 5.2,
      "frozen": 1.3,
      "available": 3.5,
      "limit": 10,
      "remaining": 4.8,
      "unlimited": false,
      "over_limit": false,
      "window_start": 1700000000,
      "next_refresh": 1700086400
    },
    "weekly_used": {
      "used": 12.3,
      "settled": 12.3,
      "frozen": 1.3,
      "available": 56.4,
      "limit": 70,
      "remaining": 57.7,
      "unlimited": false,
      "over_limit": false,
      "window_start": 1700000000,
      "next_refresh": 1700604800
    },
    "monthly_used": {
      "used": 45.6,
      "settled": 45.6,
      "frozen": 1.3,
      "available": 253.1,
      "limit": 300,
      "remaining": 254.4,
      "unlimited": false,
      "over_limit": false,
      "window_start": 1700000000,
      "next_refresh": 1702590400
    }
  }
}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否有活跃订阅 |
| `isValid` / `is_active` | bool | 同一活跃状态的兼容别名；无订阅/过期时均为 false |
| `mode` | string | 固定为 `subscription`，用于与 `/v1/usage`（钱包）区分 |
| `planName` | string | 套餐名（取订阅组的 `display_name`，缺失时回退到 `name`） |
| `unit` | string | 固定 `USD`，指订阅会计美元，已应用适用倍率，不等于上游原始成本 |
| `data.status` | string | `active` / `expired` / `revoked` |
| `data.remaining_seconds` | int | 距到期剩余秒数 |
| `data.rate_multiplier` | float | 当前订阅合同/legacy 配置倍率；历史预留各自冻结的倍率不随此字段改变 |
| `data.usage_source` | string | `billing` 为权威冻结/结算快照；仅未配置 billing 的兼容路径为 `settled_only` |
| `data.observed_at` | int | 读取时的 Unix 秒；不是不断更新的实时可用额保证 |
| `data.*_used.used` / `settled` | float | 当前窗口已结算（旧 `used` 保留） |
| `data.*_used.frozen` | float\|null | 仍 active 的预留中归属本订阅及本窗口的订阅部分；未知为 null，不含钱包冻结 |
| `data.*_used.available` | float\|null | 有限额度为 `max(0, limit - settled - frozen)`；无限或未知为 null，结合 unlimited/usage_source 解释 |
| `data.*_used.limit` | float\|null | 限额；`null` 表示无限制 |
| `data.*_used.remaining` | float | 旧客户端兼容：`max(0, limit - used)`，不扣冻结；无限时仍为历史值 0。新客户端使用 available |
| `data.*_used.unlimited` | bool | 仅 limit=null 为 true；limit=0 是有限的零额度 |
| `data.*_used.over_limit` | bool | 有限时 settled+frozen 严格大于 limit；额度恰好耗尽仅 available=0 |
| `data.*_used.window_start` | int | 当前统计窗口起点 Unix 秒 |
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

已配置 billing 但 RPC/事务读取失败时返回 HTTP 502，不回退到看似完整的本地用量。admin `GET /api/v1/subscriptions/progress` 使用相同 DO 和单位。UI 的冻结/可用额未知时显示“未知”，不会把旧响应缺少字段解释为零。

## 窗口、倍率与购买快照

billing 在与预留/结算相同的订阅行锁下读取已结算计数和 active 预留聚合，不把查询时刻分散到不同事务。日、周、月分别沿用既有 24 小时、7 天、30 天窗口，月不是自然月。

每条 V2 预留保存自己的订阅窗口与倍率，冻结为该条 `subscription_reserved_quota / quota_per_usd × frozen_multiplier`。没有冻结倍率的 legacy 记录沿用既有当前策略回退，不猜测历史倍率。倍率切换时，不可用总冻结量乘最新倍率替代逐条求和。

跨窗例：昨日按倍率 2 预留会计 $0.2，今天尚未结束时日冻结为 0，同一周的周冻结仍为 $0.2。今天结算后，**沿用既有 late settlement 规则计入当前日已结算 $0.2**，原预留窗口保留在窗口记账记录中；取消则释放冻结且不增加 settled。此接口解释现有结算语义，没有更改跨窗收费策略。

购买与运行时配置的边界：

| 数据 | 权威来源与兼容规则 |
| --- | --- |
| 订单价格、名称、时长、分组 | 创建订单时的 `PlanSnapshot`；付款履行使用快照，套餐下架/改价不改该订单 |
| V2 覆盖范围、限额、倍率 | version=2 的 `SubscriptionContract` 及 digest；订阅履行/用量使用该合同，不套用管理员后续编辑的组配置 |
| legacy 合同缺失 | version 缺省为 0、contract=nil；限额/倍率保持读取当前组配置，覆盖不构造新增授权；历史未知不回填 |
| 空快照老订单 | 沿用既有 live plan/group 兼容路径；损坏 JSON、未知版本或 V2 合同缺失均报错，不按 legacy 静默解释 |
| 新增履行字段 | 必须同时加入快照捕获、解码验证及履行映射；定义旧版本的明确默认值，不能由当前配置推测已售合同。影响合同语义的字段需版本化并测试旧订单 |

`frozen=0` 表示已知无冻结；`limit=null/available=null/unlimited=true` 表示无限；`limit=0/available=0/unlimited=false` 表示无可用额度。已过期再次购买新建订阅行，不复活旧行，见[续费语义](./subscription-renewal-semantics.md)。

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
        remaining: d.daily_used.available != null ? d.daily_used.available : d.daily_used.remaining,
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

示例中的 remaining 回退仅供旧客户端兼容；支持无限额度的工具应先检查 `unlimited`，支持未知冻结的工具应检查 `usage_source`。如需展示下次刷新时间，读取 `next_refresh`（Unix 秒）并按本地时区格式化。

## 实现位置

- Relay handler：[http_status_handler.go](../../internal/server/http_status_handler.go)
- 权威读取：[subscription_usage.go](../../app/billing/internal/biz/subscription_usage.go)，RPC：[billing.proto](../../api/billing/v1/billing.proto)
- 窗口与实体：[subscription_usecase.go](../../domain/subscription/biz/subscription_usecase.go)、[entity.go](../../domain/subscription/biz/entity.go)
- Web：[SubscriptionProgress.tsx](../../web/src/components/SubscriptionProgress.tsx)
- 三方言证据：[subscription_usage_test.go](../../app/billing/internal/data/subscription_usage_test.go)
