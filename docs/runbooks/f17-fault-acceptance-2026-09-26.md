# F17 支付故障子项隔离验收

> 日期：2026-09-26
> 代码：develop `8a1abde9`（repo 修复）+ `9790d2c2`（故障注入测试）
> 依据：桌面待办第 4 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 4 节
> 证据：本文 + [f17-fault-acceptance-2026-09-26.json](evidence/f17-fault-acceptance-2026-09-26.json)
>
> 更新：2026-09-28 补齐支付补偿失败指标及告警的本地验收，随后上线 billing-service 与 Prometheus 规则并只读核对接线；下方“后续观测与处置”记录本轮证据，原 JSON 仍只代表 2026-09-26 四项故障验收。

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
| 发放失败恢复 | `TestPaymentBalanceGrantFailureRollsBackAndRecovers`、`TestPaymentSubscriptionGrantFailureRollsBackAndRecovers`、`TestPaymentRepo_MarkOrderPaidIssueFailureRollsBack` | ✅ usecase 故障重试及 repo 层真实 SQLite 事务回滚：订单保持 pending+asset_issue_status=pending；notify 回 `fail` 或下一轮查单重放后收敛成功 |

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
- 后续 review 补充 `TestPaymentRepo_MarkOrderPaidIssueFailureRollsBack`，验证
  发放回调在同一事务写入后失败时，写入与订单状态一同回滚，重放可成功。
- 全部样本为隔离注入（sqlite :memory: + fake），与 2026-09-26 生产沙箱
  正常往返证据分属不同层，互不替代。

## 剩余边界

1. **查单重试仍无退避、无次数上限**：保持每 1 分钟、每轮最多 100 单的
   重试。2026-09-28 已补失败指标及持续失败告警的本地验收，上线后完成
   指标抓取和规则健康核对；退避、扫描公平性与重试治理另按证据启动，
   不以重试上限自动关闭可能已付款的订单。
2. **支付宝侧真实重发行为未验**：HTTP 层重复回调用 stub 验签器模拟；
   支付宝实际重发间隔/次数由平台控制，沙箱无法编程验证。
3. **发放失败的"卡死"终态**：admin 手工补单路径若补偿本身失败会留下
   `paid+issued+subscription_id=0` 卡死订单，现有机制是
   `ReconciliationDiscrepanciesTotal{stuck_issuance}` 对账上报、不自动修
   复（`reconciliation_stuck_test.go` 已覆盖检测）。自动修复未立项。
4. 正常支付路径的浏览器 `return_url` 与异步 `notify_url` 边界沿用沙箱
   证据的结论：同步返回不能代替服务端回执。

## 后续观测与处置（2026-09-28，Q2/F17）

基线提交 `426b0363`；实现见 [PaymentReconcileJob](../../app/billing/internal/biz/payment_reconcile_job.go)。用户因生产单用户低流量
暂缓 O5 后，本轮补齐已知的支付补偿可见性缺口，没有执行生产付款或故障注入。

### 指标与告警语义

- `micro_one_api_billing_payment_reconcile_runs_total{result}` 每个后台完成轮次
  只增加一次。`success` 表示本轮没有刷新错误，包含空扫描及尚未付款；
  `partial` 表示至少一单刷新失败；`error` 表示订单列表读取等扫描级失败。
  任务自身上下文取消/到期时跳过该轮观测并保留上一轮失败状态；供应商请求
  独立超时仍计 `partial`，不会被当作任务退出而吞掉。
- `micro_one_api_billing_payment_reconcile_failed` 为最近完成轮次的失败状态：
  后两类为 1，无错误为 0。`PaymentReconciliationFailing` 在此值连续为 1
  达 5 分钟后 warning；一次正常轮次即可清除条件。单笔反复失败也适用，
  不需要高 QPS；短暂失败后下一轮恢复不触发。
- 既有 `QueryFailures` 包含供应商查单、支付状态落库和资产发放失败，故新
  告警使用“补偿/刷新失败”而非断言“支付宝不可用”。指标仅包含固定结果
  标签，不携带用户、订单、付款金额、供应商回包或密钥。
- 沿用 Alertmanager → notify-worker 接线及 `send_resolved: true`。规则已上线
  并处于 inactive；本次只读验收不代表实际 firing/resolved 邮件已送达。

### 处置步骤

1. 在 billing-service 的 `/metrics` 核对上述指标、Prometheus 抓取和容器状态。
   按实例查看结果增量，分清 `error` 和 `partial`，避免把未付款的 pending
   订单或空扫描当作故障。`success` 表示扫描无错误，不等于订单已付款。
2. `error` 先检查 billing 数据库连接及扫描日志；`partial` 核对支付宝
   网关/配置和订单状态、发放相关存储。使用管理端的授权查询核实 provider
   交易状态及对应充值账本/订阅，不在公开日志或告警中复制完整回包和凭证。
3. 恢复故障依赖后，等待现有后台任务下一轮收敛，再核对 pending → paid/issued
   或 closed、账本/订阅恰好一次。不要手改 paid/issued 标志、删除订单或清空
   幂等 claim 来消警；金额/身份不一致以及 stuck issuance 按既有人工核对处理。
4. 确认失败状态归零、success 增加、规则恢复；通知还需核对 notify-worker
   送达状态和收件端。生产验收只读核对接线，不为验证告警制造真实支付故障。

### 本地验证

`TestPaymentReconcileJobFailureAndRecoveryMetrics` 在接线前对 scan/query/issuance
三类故障均失败（计数为 0）；接线后验证失败分类、状态不提前推进、恢复清零
和余额只发放一次。`TestPaymentReconcileJobHealthyPendingAndEmptyScan` 验证
未付款/无订单不误报。沿用 F17 和 data 事务回归覆盖幂等及回滚。

本轮 review 先通过 `TestPaymentReconcileJobInterruptedScanPreservesMetrics`
复现四个中断场景污染成功/失败计数及错误清除上一轮失败状态，再在扫描前后
检查任务上下文修复；额外覆盖供应商独立超时仍计失败。CI 原来只在 routing
E2E 中执行 `routing.test.yml`，本轮将三个规则测试文件接入 CI 的
`Deployment and docs drift` job，防止本地通过后无人持续执行。

```bash
go test ./app/billing/... ./platform/metrics -count=1
go test -race ./app/billing/internal/biz ./app/billing/internal/data ./app/billing/internal/service ./platform/metrics -count=1
docker run --rm --entrypoint /bin/promtool \
  -v "$PWD/deploy/prometheus/alerts:/rules:ro" -w /rules \
  prom/prometheus:v3.6.0 test rules operations.test.yml credential.test.yml routing.test.yml
./scripts/check-architecture.sh
git diff --check
```

规则测试覆盖单笔持续失败在 5 分钟前不触发、达到时触发、恢复消警以及短暂
失败不触发。此次不改 DTO、存储或账务行为，无迁移或生成代码变更。

边界：指标仅观察后台扫描，不覆盖用户主动查单和回调路径；最近轮次无错误
不证明全量积压清空。当前扫描有 100 单上限、供应商逐单超时，轮次可能超过
一分钟；此告警不是任务心跳或积压年龄监控，也不能证明单个订单持续失败。
启动时失败状态初值为 0，需结合轮次计数判断任务是否执行过；进程重启会重置
指标，服务停机/抓取中断不由此规则证明恢复。新告警的生产
firing/resolved、实际平台重发和独立沙箱异步 HTTP 回执仍待相应证据。

### 生产上线与只读核对（2026-09-28）

- 本机 `scripts/deploy-update.sh billing-service` 交叉构建 linux/amd64 后上传；
  新容器镜像 `sha256:e35fbb3289dfe251c3986c71dcaf72620c3940a56d496349706024b69a0cda1c`，
  原镜像保留为 `rollback-20260928-151327`。容器健康接口返回 200，重启次数 0。
- 生产原规则先备份至
  `/opt/micro-one-api/monitoring/prometheus/alerts.yml.backup.20260928-152519`；
  原规则经 promtool 检查为 46 条。新文件写入现有挂载文件并通过 promtool
  检查为 47 条，Prometheus 热加载后 `PaymentReconciliationFailing` 为
  `health=ok/state=inactive`。
- billing-service `/metrics` 导出 `failed=0`、`runs_total{success}=1`、
  `partial/error=0`（部署后首轮样本）；Prometheus 查询
  `instance=billing-service:8004` 的失败状态为 0。此为规则接线与正常扫描
  的生产只读证据，未触发真实支付故障，不证明 firing/resolved 通知送达。
