# 订阅续费语义（阶段 2.2）

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 2.2：续费。
> 实现分支：`feat/subscription-productization-phase2`。

## 1. 续费行为

同一分组（`group_id`）的 active 订阅续费时，延长 `expires_at`：
- `AssignOrExtend` 在检测到用户已有同组 active 订阅时，把新有效期叠加到现有 `expires_at` 之后（`expires_at = active.expires_at + duration`）。
- 若新订单的 `expires_at` 落在当前 `expires_at` 之前，则按"叠加"语义处理，避免续费反而缩短有效期。

## 2. 已过期订阅的重新激活策略

已过期但未撤销（`status=expired`，非 `revoked`）的订阅，策略固定为：

**重新激活（reactivate）而非新建。**

- 续费时若 active 订阅不存在但存在同组 `expired` 订阅，则把该 expired 订阅重新置为 active 并延长 `expires_at`。
- 这保证同一用户在同一分组始终只有一条订阅记录（active 或可追溯的 expired→active），便于对账和用量追溯。
- `revoked` 订阅不参与重新激活，需新建。

> 注：当前 `AssignOrExtend` 通过 `GetActiveSubscriptionByUser` 判断；expired→active 的重新激活由 expiry_checker 将过期 active 标记为 expired 后，下一次 `Assign`（无 active 时）新建。两种路径都保证用户唯一 active 订阅。若未来需要严格复用 expired 行，可在 `Assign` 中增加 expired 查找分支。

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
