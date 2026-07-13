# 订阅退款 / 冲正账务语义（阶段 2.3）

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 2.3：退款/冲正。
> 实现分支：`feat/subscription-productization-phase2`。

本文档固定退款/冲正的账务语义，确保 dashboard、usage、orders、cost analysis 口径一致，并保证重复回调不会重复返钱或重复冲正。

## 1. 退款订单状态

`payment_orders.status` 新增终态 `refunded`，与 `paid`、`closed` 并列：

| 状态 | 含义 | 是否终态 |
| --- | --- | --- |
| `pending` | 待支付 | 否 |
| `paid` | 已支付并已发放权益 | 是 |
| `closed` | 已关闭（未支付） | 是 |
| `refunded` | 已退款/冲正 | 是 |

- 只有 `paid` 订单可以退款。`pending` 订单应走关闭（`closed`），`closed` 订单没有钱包变动可冲正。
- 退款是终态：退款后的订单不能再次支付、关闭或退款。

## 2. 冲正账本类型

账本（`billing_ledgers`）新增冲正维度：

| 字段 | 值 | 说明 |
| --- | --- | --- |
| `type` | `refund` | 复用既有退款类型 |
| `cost_source` | `reversal` | 新增冲正维度，与 `balance`（消费退款）区分 |
| `ledger_dedupe_key` | `{trade_no}:refund:reversal` | 幂等键，DB 唯一约束保证一笔订单只冲正一次 |

退款时：
- 钱包 `UpdateBalance(+priceQuota, "refund")` 退还购买金额。
- 写入一条 `type=refund, cost_source=reversal` 的账本，金额为退还的 quota，`reference_id` 为 `trade_no`。

## 3. 订阅权益处理策略

退款后订阅权益按 `policy` 处理，固定为以下三种之一：

| policy | 行为 | 适用场景 |
| --- | --- | --- |
| `revoke` | 撤销订阅（`status=revoked`） | 退款前无明显消费 |
| `shorten` | 缩短 `expires_at`（减去套餐有效期对应的秒数，clamp 到 now） | 已部分消费、按剩余时间折算 |
| `keep` | 订阅不动 | 善意退款，不回收权益 |

- 默认 policy 为 `revoke`。
- `revoke`/`shorten` 需要订阅 id（通过订单的 traceability metadata `subscription_id` 关联）。
- `shorten` 不会把 `expires_at` 推到未来，只会回拉到 now 或更早。

## 4. 幂等保证

退款幂等由两层保证：
1. **订单状态守卫**：`MarkOrderRefunded` 在事务内对订单加行锁，若已是 `refunded` 则直接返回 `changed=false`，不执行 revert 回调。
2. **账本 dedupe key**：`{trade_no}:refund:reversal` 的 DB 唯一约束保证即使 revert 回调被执行，重复的账本写入也会失败。

因此重复退款回调（重放、多副本）不会：
- 重复退还钱包余额
- 重复撤销/缩短订阅
- 重复写冲正账本

## 5. 对账覆盖

退款/冲正纳入对账：
- 退款订单的 `money_cents` 计入 `refunded` 收入口径（运营报表的 `refunded_quota`）。
- 冲正账本（`cost_source=reversal`）在对账任务中与钱包余额变动核对。
- dashboard / orders / cost analysis 的退款口径统一来自 `payment_orders.status='refunded'` 和 `billing_ledgers.cost_source='reversal'`，不依赖前端内存抽样。

## 6. 追溯链接

订单 → 订阅：退款通过订单 `ProviderPayload` 中的 `subscription_id` 找到订阅行。
订阅 → 订单：订阅 `metadata` 中的 `payment_trade_no` 指回订单。
账本 → 订单：冲正账本 `reference_id = trade_no`。
