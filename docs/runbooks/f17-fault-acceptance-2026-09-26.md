# F17 支付故障子项隔离验收

> 日期：2026-09-26
> 代码：develop `8a1abde9`（repo 修复）+ `9790d2c2`（故障注入测试）
> 依据：桌面待办第 4 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 4 节
> 证据：本文 + [f17-fault-acceptance-2026-09-26.json](evidence/f17-fault-acceptance-2026-09-26.json)

## 范围与方法

支付宝沙箱正常支付往返已于 2026-09-26 完成（
[f17-alipay-sandbox-roundtrip-2026-09-26.json](evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)）。
本文关闭其声明的四个故障子项。全部用隔离故障注入验证，不依赖沙箱可编程
性：biz 层用内存 fake repo/provider/issuer/assigner，service 层用 stub
验签器 + 真实 usecase，repo 层用 sqlite :memory: 真实事务。注入点是
`PaymentProvider` / `PaymentStatusQuerier` / `PaymentNotifyVerifier` /
`PaymentAssetIssuer` / `SubscriptionAssigner` 既有接口，未改生产接线。

## 四个子项的结论

| 子项 | 测试 | 结论 |
| --- | --- | --- |
| 丢回调后的查单收敛 | `TestPaymentReconcileLostNotifyConverges` | ✅ 1 分钟 reconcile 任务经 `alipay.trade.query` 把 pending 订单收敛为 paid，余额恰好发放一次 |
| 查单暂时失败重试 | `TestPaymentReconcileQueryFailureStaysPendingAndRetriesNextRound` | ✅ 查询失败计入 `QueryFailures`、订单保持 pending 不发放；下一轮自然重试成功。生产语义为固定 1 分钟间隔无限重试，无退避/上限/死信（见剩余边界） |
| 重复签名回调幂等 | `TestPaymentDuplicatePaidNotificationIssuesOnce`（biz）、`TestHandleAlipayNotifyDuplicateSignedCallbackIdempotent`（HTTP）、`TestPaymentRepo_MarkOrderPaidReplayDoesNotReissue`（repo 事务） | ✅ 三道闸门各自验证：状态短路（发放回调至多一次）、账本 dedupe claim `topup:{user}:payment:{trade_no}`（ledger_repo 既有测试）、HTTP 层重复投递回 `success` 不引发重发风暴 |
| 发放失败恢复 | `TestPaymentBalanceGrantFailureRollsBackAndRecovers`、`TestPaymentSubscriptionGrantFailureRollsBackAndRecovers` | ✅ 钱包/订阅发放失败使整事务回滚，订单保持 pending+asset_issue_status=pending；notify 回 `fail` 或下一轮查单重放后收敛成功，资产恰好发放一次 |

配套负向证据（同一 HTTP 边界）：验签失败回 `fail`（
`TestHandleAlipayNotifyVerifyFailureRespondsFail`）、非成功状态吞掉回
`success`（`...NonSuccessIsSwallowed`）、金额不符与 app_id 不符的签名通过
回调被 billing-L5 交叉校验拒绝（`...AmountMismatch...`、
`...AppIDMismatch...`）。

## 修复项

故障注入发现一个真实（非资损）缺陷：`paymentRepo.MarkOrderPaid` 的幂等
重放路径在短路前用入参覆盖返回内存副本的 `ProviderTradeNo`，重放通知携带
不同 provider 单号时会谎报订单引用（DB 行不受影响）。修复（`8a1abde9`）
把赋值移到 paid 短路之后，repo 回归测试锁定。

## 执行记录

- `go test ./app/billing/... -count=1` 全绿；`go test -race ./app/billing/internal/biz ./app/billing/internal/data` 通过；gosec 无新增。
- 全部样本为隔离注入（sqlite :memory: + fake），与 2026-09-26 生产沙箱
  正常往返证据分属不同层，互不替代。

## 剩余边界

1. **查单重试无退避、无次数上限、无失败告警**：单轮查询失败只打 warn
   日志，靠每 1 分钟固定间隔无限重试收敛。支付宝侧长期故障时会持续
   打日志但不产生告警指标；是否加退避/上限/告警属 D3/运维手册范围，
   不在本次故障验收内改动。
2. **支付宝侧真实重发行为未验**：HTTP 层重复回调用 stub 验签器模拟；
   支付宝实际重发间隔/次数由平台控制，沙箱无法编程验证。
3. **发放失败的"卡死"终态**：admin 手工补单路径若补偿本身失败会留下
   `paid+issued+subscription_id=0` 卡死订单，现有机制是
   `ReconciliationDiscrepanciesTotal{stuck_issuance}` 对账上报、不自动修
   复（`reconciliation_stuck_test.go` 已覆盖检测）。自动修复未立项。
4. 正常支付路径的浏览器 `return_url` 与异步 `notify_url` 边界沿用沙箱
   证据的结论：同步返回不能代替服务端回执。
