# 订阅套餐配置与购买发放 Runbook

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 适用版本：v0.5.0+（含 `subscription_groups`、`subscription_plans`、`user_subscriptions`、`payment_orders.plan_snapshot`）。
> 相关语义文档：[续费语义](./subscription-renewal-semantics.md)、[退款/冲正语义](./subscription-refund-reversal-semantics.md)。
> 相关 runbook：[生产发布 Runbook](./subscription-production-runbook.md)、[订阅账号 OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md)。

本 runbook 让新部署人员只按本文档即可完成订阅套餐的上架、测试购买、支付回调发放与下架，并验证已下架套餐不影响已下单订单。

## 一、前置条件

1. **服务健康**：admin-api、billing-service、identity-service、channel-service `/healthz` 正常。
2. **迁移已执行**：`039_create_user_subscriptions.sql`、`040_create_subscription_groups.sql`、`042_add_subscription_group_pricing.sql`、`043_add_group_id_to_payment_orders.sql`、`050_create_subscription_plans.sql`、`057_add_plan_snapshot_to_payment_orders.sql`。
   ```bash
   make migrate-status
   ```
3. **支付通道已配置**（购买需要在线支付时）：`configs/billing-service.yaml` 的 `payment.alipay.enabled: true` 且 `ALIPAY_APP_ID` / 密钥 / 证书齐备。沙箱可用 `ALIPAY_FORM_URL=https://openapi-sandbox.dl.alipaydev.com/gateway.do`。
4. **管理员凭证** `ADMIN_TOKEN`，以及一个测试用户 token（`${API_TOKEN}`，用于用户侧购买接口）。
5. **订阅分组就绪**：套餐必须挂在一个 `status=1` 的 `subscription_groups` 上；分组是「权益容器」，套餐是「可购买产品层」。

## 二、必填配置

### 2.1 分组字段（`subscription_groups`）

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `name` | ✅ | 机器名，唯一 |
| `display_name` | ✅ | 展示名 |
| `platform` | ✅ | `claude` / `codex`（与订阅账号平台对齐，影响选路） |
| `subscription_type` | ⬜ | 预留 |
| `daily_limit_usd` / `weekly_limit_usd` / `monthly_limit_usd` | ⬜ | 用户订阅的窗口额度（USD），留空=不限 |
| `rate_multiplier` | ⬜ | 用户侧订阅扣费倍率，默认 1 |
| `status` | ✅ | `1`=启用，`0`=禁用。禁用分组上的套餐不可购买 |
| `price_quota` | ✅（自购） | 自购价格（quota 单位）。`isPurchasableGroup` 要求 > 0 |
| `duration_days` | ✅（自购） | 有效期天数。`isPurchasableGroup` 要求 > 0 |

可购买判定（`internal/admin/service/subscription.go` 的 `isPurchasableGroup`）：`status==1 && price_quota>0 && duration_days>0`。

### 2.2 套餐字段（`subscription_plans`）

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `group_id` | ✅ | 挂载的分组 |
| `name` | ✅ | 套餐名 |
| `description` | ⬜ | 描述 |
| `price_quota` | ✅ | 价格（quota 单位） |
| `original_price` | ⬜ | 划线原价，留空不展示 |
| `validity_days` | ✅ | 有效期天数 |
| `validity_unit` | ⬜ | 单位展示，默认 `day` |
| `features` | ⬜ | 特性 JSON/文本 |
| `product_name` | ⬜ | 产品名（`name` 为空时回退） |
| `for_sale` | ✅ | `true`=在售，`false`=下架。默认 1 |
| `sort_order` | ⬜ | 排序，越大越靠前，默认 0 |

可购买判定（`isPurchasablePlan`）：`for_sale && price_quota>0 && validity_days>0 && group.status==1`。

## 三、上架流程

所有操作经 admin-api（管理后台图形界面或 REST API）。REST 端点（受 `adminAuth` 保护）：

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/subscription-groups` | 列分组 |
| POST | `/api/v1/admin/subscription-groups` | 建分组 |
| GET/PUT/DELETE | `/api/v1/admin/subscription-groups/{id}` | 增删改单个分组 |
| GET | `/api/v1/admin/subscription-plans?for_sale=true\|false` | 列套餐（过滤在售/下架/全部） |
| POST | `/api/v1/admin/subscription-plans` | 建套餐 |
| GET/PUT/DELETE | `/api/v1/admin/subscription-plans/{id}` | 增删改单个套餐 |
| POST | `/api/v1/admin/subscription-plans/{id}/for-sale` | 上下架切换（只翻 `for_sale`，body `{"for_sale":bool}`） |

### 3.1 建分组

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/admin/subscription-groups \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name":"claude-pro",
    "display_name":"Claude Pro 月度",
    "platform":"claude",
    "status":1,
    "price_quota":100,
    "duration_days":30,
    "daily_limit_usd":20,
    "monthly_limit_usd":200,
    "rate_multiplier":1
  }'
```

### 3.2 建套餐并上架

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/admin/subscription-plans \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "group_id":1,
    "name":"Claude Pro 月度套餐",
    "price_quota":100,
    "original_price":150,
    "validity_days":30,
    "validity_unit":"day",
    "features":"{\"daily\":20,\"monthly\":200}",
    "product_name":"Claude Pro",
    "for_sale":true,
    "sort_order":100
  }'
```

建好默认 `for_sale=true`（迁移默认值 1）。

### 3.3 用户侧展示口径

用户侧只展示 `for_sale=true` 且关联 group `status=1` 的套餐：
- `GET /api/v1/subscriptions/plans` → `ListPlansForSale`。
- `GET /api/v1/subscriptions/groups` → 可购分组。

管理后台审计可用 `?for_sale=false` 看下架套餐，或不带过滤看全部。

## 四、测试购买与发放

### 4.1 创建支付订单

用户侧（bearer 是用户 token，非 admin）：

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/subscriptions/purchase/payment \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"plan_id":1,"channel":"alipay","currency":"CNY"}'
```

- `plan_id` 或 `group_id` 至少传一个。传 `plan_id` 走套餐流（推荐）。
- `money_cents` 留空时 = `price_quota * 100`（`quota_per_unit` 默认 500000，价格以 quota 为单位）。
- `channel` 留空默认 `alipay`。

返回两种情况：
1. 余额足够 → 直接发放，返回 `{"subscription":{...},"payment":null}`。
2. 余额不足 → 创建支付订单，返回 `{"subscription":null,"payment":{"trade_no":...,"pay_url":...}}`。

### 4.2 订单快照

下单时 `CreatePaymentOrder` 会调 `PlanSnapshotter.CapturePlanSnapshot(plan_id)`，把套餐的 `plan_id` / `group_id` / `name` / `price` / `validity_days` 冻结到 `payment_orders.plan_snapshot`（JSON）。**这是已下单订单不受之后上下架/改价影响的关键**。

### 4.3 完成支付与发放

支付回调由支付宝异步通知触发：

- 支付宝 → `POST /api/v1/user/payments/alipay/notify`（admin-api 代理到 billing-service）。
- `HandleAlipayNotify` → `MarkOrderPaid(tradeNo, providerTradeNo)`。
- `MarkOrderPaid` 在事务内对订单加行锁（`SELECT ... FOR UPDATE`）：若订单已是 `paid` 直接返回 `changed=false`，**不重新执行发放回调**（幂等）。
- 首次 `paid` 时执行 issue 回调：`AssignSubscriptionAfterPayment(order)` →
  - 有 `plan_snapshot` → `assignFromSnapshot`（只读快照，不查 live plan）。
  - 无快照但 `plan_id>0` → `assignPlan`（读 live plan）。
  - 只有 `group_id` → `assignGroup`。
- `AssignOrExtend`：同分组已有 active 订阅则延长 `expires_at`（续费语义）；否则新建。

用户也可主动调完成接口（支付后）：

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/subscriptions/purchase/complete \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"trade_no":"<上一步 trade_no>"}'
```

它查订单状态，`paid` 且 `asset_issue_status != issued` 时补发放。

### 4.4 管理后台手动发放

`POST /api/v1/admin/subscriptions/assign`（admin）可直接给用户发放订阅（跳过支付），用于运营补偿或测试。

## 五、验证

### 5.1 用户侧订阅

```bash
curl -s http://127.0.0.1:3000/api/v1/subscriptions/progress \
  -H "Authorization: Bearer ${API_TOKEN}" | jq
```

返回当前 active 订阅、用量窗口、额度剩余。

### 5.2 库内三方追溯

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e "
SELECT id,user_id,group_id,status,starts_at,expires_at,metadata FROM user_subscriptions WHERE user_id=<uid>;
SELECT trade_no,status,asset_type,asset_issue_status,plan_id,LEFT(plan_snapshot,40) AS snap FROM payment_orders WHERE user_id=<uid>;
SELECT id,type,reference_id,subscription_id,cost_source FROM billing_ledgers WHERE user_id=<uid> ORDER BY id DESC LIMIT 10;"
```

追溯链（续费语义文档 §4）：
- 订阅 `metadata.payment_trade_no` → 订单 `trade_no`。
- 订阅 `metadata.plan_id` / `plan_name` → 套餐快照。
- 账本 `type=subscription` 的 `reference_id` = `group_id`；冲正账本 `reference_id` = `trade_no`。

### 5.3 下架不影响已下单

1. 下架：`POST /api/v1/admin/subscription-plans/{id}/for-sale -d '{"for_sale":false}'`。
2. 确认用户侧 `/api/v1/subscriptions/plans` 不再返回该套餐。
3. 已创建但未支付订单仍能完成：`/api/v1/subscriptions/purchase/complete` 用快照发放（`completeFromPlanSnapshot` 不查 live plan 的 `for_sale`）。
4. 新下单被拒：`/api/v1/subscriptions/purchase/payment` 传该 `plan_id` 返回 `ErrSubscriptionPlanNotSaleable`（`isPurchasablePlan` 失败）。

### 5.4 续费幂等

回放同一 `trade_no` 的支付通知两次：

```bash
# 模拟重复回调
curl -s -X POST http://127.0.0.1:3000/api/v1/user/payments/alipay/notify -d "trade_no=<trade_no>&..."
```

期望：`expires_at` 只延长一次（第二次 `MarkOrderPaid` 返回 `changed=false`，不调 assigner）。对应测试 `TestMarkOrderPaid_RenewalIsIdempotentAcrossReplays`。

## 六、常见故障与恢复

### 6.1 购买返回 `subscription plan is not saleable`

**原因**：套餐 `for_sale=false`，或挂载分组 `status=0`，或 `price_quota<=0` / `validity_days<=0`。

**恢复**：在管理后台把套餐上架（`for_sale=true`）并确认分组启用、价格/有效期 > 0。

### 6.2 支付回调后订阅未发放

**原因**：`MarkOrderPaid` 的 issue 回调失败（assigner 报错），或订单 `asset_type != subscription`。

**排查**：
1. 看 billing-service 日志 `assign subscription after payment` 错误。
2. 确认 `payment_orders.asset_type='subscription'` 且 `plan_id` 或 `group_id` 有效。
3. 若 issue 回调失败，订单仍是 `paid` 但 `asset_issue_status='pending'`；修复后用 `/api/v1/subscriptions/purchase/complete` 或 `/api/v1/admin/subscriptions/assign` 补发放。

### 6.3 续费反而缩短了有效期

**原因**：理论上不会发生——`AssignOrExtend` 在 `req.ExpiresAt <= active.ExpiresAt` 时改用 `active.ExpiresAt + duration` 叠加。若观察到缩短，说明绕过了 `AssignOrExtend` 直接改库。

**恢复**：用 `metadata.payment_trade_no` 找到订单，按 `duration_days` 重算 `expires_at = 原 expires_at + duration` 并修正。

### 6.4 下架后老订单无法完成

**原因**：订单没有 `plan_snapshot`（旧数据，迁移 `057` 之前创建），且 live plan 已下架。

**恢复**：`completeFromPlanSnapshot` 缺快照时回退到 live plan 路径，会因 `isPurchasablePlan` 失败而拒绝。需临时上架该套餐完成发放，或用 `/api/v1/admin/subscriptions/assign` 手动发放等价订阅。

### 6.5 支付宝回调验签失败

**原因**：`ALIPAY_PUBLIC_KEY` / 证书路径错，或 `ALIPAY_NOTIFY_URL` 未指向 admin-api 的 `/api/v1/user/payments/alipay/notify`。

**恢复**：核对 `configs/billing-service.yaml` 的 `payment.alipay` 配置与证书路径；确认回调 URL 公网可达。

## 七、运营报表

billing-service 暴露 gRPC `SubscriptionOperationReport`（`internal/billing/service/billing.go`）：

- 维度：按 `plan_id` / `group_id` / `user_id` / 时间区间筛选。
- 指标：新购数、续费数、退款数、收入 quota、退款 quota、active/expired/revoked 订阅数、订阅用量 quota、余额兜底 quota。
- 数据来源：`payment_orders` + `billing_ledgers` + `user_subscriptions` 聚合，不依赖前端内存抽样。
- 退款口径统一来自 `payment_orders.status='refunded'` 与 `billing_ledgers.cost_source='reversal'`，与成本分析页一致。

成本分析页（`web/src/pages/admin/CostAnalysisPage.tsx`）展示订阅账号 ledger 成本与账号本地额度事件 TOP 5 对照。
