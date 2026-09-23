# 订阅续费语义（阶段 2.2）

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 2.2：续费。
> 实现分支：`feat/subscription-productization-phase2`。

## 1. 续费行为

同一分组（`group_id`）的 active 订阅续费时，延长 `expires_at`：
- `AssignOrExtend` 在检测到用户已有同组 active 订阅时，把新有效期叠加到现有 `expires_at` 之后（`expires_at = active.expires_at + duration`）。
- 若新订单的 `expires_at` 落在当前 `expires_at` 之前，则按"叠加"语义处理，避免续费反而缩短有效期。

## 2. 已过期后的再次购买

已过期但未撤销（`status=expired`，非 `revoked`）的订阅，策略固定为：

**新建订阅行，旧行保留用于追溯。**（2026-09-23 按实际实现纠正。）

- `AssignOrExtend` 只延长当前仍有效的 active 订阅；无有效 active 时进入 `Assign` 新建，获得新的订阅 ID 和窗口。
- 到期行即使尚未被 expiry checker 标记，也不作为有效续费对象。历史 expired/revoked 行及其订单/预留/窗口账记录保持原身份。
- 唯一约束保证用户当前 active 唯一，不保证用户历史上只有一行订阅；过期后购买与 active 同组续费应区分。

购买快照与当前配置的权限/倍率边界见[用量接口契约](./subscription-usage-api.md#窗口倍率与购买快照)。本次未改变续费业务行为。

## 3. 支付回调幂等

续费的支付回调幂等由 `MarkOrderPaid` 的事务守卫保证：
- `MarkOrderPaid` 在事务内对订单加行锁（`SELECT ... FOR UPDATE`）。
- 若订单已是 `paid`，直接返回 `changed=false`，**不重新执行 issue 回调**（不重复调用 assigner）。
- 因此同一订单多次回调（重放、多副本、重复 notify）只产生一次续费效果，`expires_at` 只延长一次。

## 4. 订单 ↔ 订阅 ↔ 账本追溯

续费链路通过 metadata 实现三方追溯：
- 订阅 `metadata.payment_trade_no` = 订单 `trade_no`。
- 订阅 `metadata.plan_id` / `plan_name` = 套餐快照。
- 钱包账本（`type=subscription`）`reference_id` = `group_id`；冲正账本 `reference_id` = `trade_no`。

因此续费订单、订阅记录、账本可互相追踪。

## 5. 验收

- 同一订单多次回放只产生一次续费效果（`expires_at` 只延长一次）。✅ 见 `TestMarkOrderPaid_RenewalIsIdempotentAcrossReplays`
- 续费订单、订阅记录、billing ledger 可互相追踪。✅ 见 `TestRenewal_TraceabilityMetadataLinksOrderToSubscription`
- 同分组 active 订阅续费延长 `expires_at`。✅ 见 `TestRenewal_ExtendsExpiryForSameGroupActiveSubscription`
