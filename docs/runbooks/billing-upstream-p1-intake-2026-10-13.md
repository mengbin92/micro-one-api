# P1.1：billing 持久结算接收前置闭环

日期：2026-10-13。基线：`57153a1e`。状态：P1 工程已开始，本切片本地验证通过；尚未部署，不代表完整 P1 或收费门禁通过。

关联：[计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游方案](../design/upstream-contract-evolution-plan-2026-10-13.md)、[P0 生产基线](billing-upstream-p0-baseline-2026-10-13.md)。Neo 已指令开始下一阶段并继续实施；P0 完整窗口与独立费用验收仍保持原状态。

## 本切片的范围

先完成消费者的 final settlement 接收闭环，为 relay 发件箱提供可验证的持久 ACK。复用 `billing_settlement_tasks`，不新增消息队列、不新增表、不更改五桶计价或生产 allowlist。

- 持久任务在同一事务锁定 reservation，验证状态、读取已有任务并持久保存；收单与 commit/expiry 竞争同一行。
- 一个 reservation 的 final event 固定为 `<reservation_id>/final/0`。首次财务输入、首次时间戳和 worker 认领不可变。同键同内容重试返回原状态，同键不同财务输入返回 `SETTLEMENT_PAYLOAD_CONFLICT`，包括已 completed 的任务。
- 接收端重算 SHA-256，不相信客户端 digest。原任务 JSON 内保存接收端 digest，恢复时和 financial pipeline 前再次验证；损坏任务进入 invalid 状态，保留原因，不能进入扣费 worker。
- Data 支持持久接收时，原 async 入口也由 recovery worker 认领存储中的原始任务，不使用重试调用的对象或更新时间替换它。正常入队延迟最多包含现有 1 秒 recovery tick；仍为既有异步结算。
- payload conflict、非法 receipt、关闭的 reservation 不允许退回 sync 路径绕过校验。旧接口的暂时性存储故障保留原同步兜底；新 receipt 接口不能靠同步兜底制造持久 ACK。
- 过期释放在事务内重新读取状态/过期时间和待结算任务。候选选出后任务到达，会返回 expiry deferred；reconciliation 不将这种跳过计为已清理。
- release/expired 的 receipt 明确 `settlement_accepted=false`、`settled=false`，顶层 success=false；即使 committed_amount=0，也不能视为扣费成功。已提交且已有相同持久任务的重试返回 settled，已知 actual_cost=0 保留为 0。

这张表在本切片中提供**财务 final event inbox**，不冒充完整采购执行证据库。还没有到达 billing 的事件、失败 attempt 采购证据、intent/checkpoint、资金 lease 与最大补证期限均在后续切片处理。

## 协议与能力

新增字段由 `make api` 生成；生成文件遵循仓库既有忽略/生成规则，未手改 pb 或 Wire 文件。既有 RPC 名称和 IAM 权限没有增加或放宽。

| 位置 | 字段与行为 |
|---|---|
| GetRoutingCapabilities | `settlement_receipt_version=1` 表示仅 final 财务接收能力；不是完整 execution delivery 或 usage v2 能力 |
| CommitQuotaRequest | receipt version、event ID、payload SHA-256；version=0 保留旧 RPC 外形 |
| CommitQuotaResponse | receipt 的 received、settlement_accepted、settled 和状态；异步接受不等于账务完成 |
| 版本/状态 | 当前 receipt 只接受 usage contract 0/1；未知版本或 v1 envelope 错误拒绝，不借 legacy 强制收费 |

`BILLING_SETTLEMENT_RECEIPTS_V1` 默认 false，已接入三种 Compose 模板。启用还要求 async usecase、持久任务表与 reservation 表可用；能力不可用返回 0，receipt 请求失败关闭。现有 relay 尚未发送新 receipt 字段，生产开关未改。

同 ID/不同内容由标准化**有效财务输入**定义：reservation ID、actual tokens、success、subscription account override 和完整 LedgerUsage。共享 `domain/billing.SettlementPayload` 固定 JSON 外层和排序后的 usage；保持整数精度和 nil/presence。未被 settlement 使用的任务排队元数据及 Timestamp 不进 digest；其中首份 Timestamp 仍保存在原 payload，供副作用重放使用。ledger usage 内 elapsed time、reason、source/model 与 envelope 也受 digest 保护。此 SHA 不是调用方身份签名，认证仍由现有专用 service identity 和 exact RPC capability 承担。

## 验证

先运行新增 `TestSettlementIntake*`，原实现失败：pending/processing/failed/completed 可以接受不同 payload，重试撤销 processing 认领，terminal reservation 能收到新任务，过期候选能释放刚收到任务的 reservation。修复后相同回归通过。

新增/扩展测试覆盖：

- 同键不同内容和并发冲突只留下一个原始任务；重复时钟不改变 first clock/claim。
- 接收端 digest 错误、未来版本、错误 event key 在入库前拒绝。
- repo 重建可恢复 receipt，committed 的合法 0 不变成 reserve estimate。
- 释放/过期 receipt 不报告成功结算，已接收任务挡住过期候选。
- 存储 payload 被修改后不能进入 financial worker；拒绝事件不能走 sync fallback。
- capability 开关/持久存储不可用时失败关闭；received 与 settled 分开。
- JSON key order/空白不影响 digest，超过 float 精度的整数不丢失，非法/trailing usage JSON 拒绝。

相关 billing/domain 单元与 race 回归、全仓单元回归、API 生成和分层检查结果见 [本地验收索引](evidence/billing-upstream-p1-intake-2026-10-13.json)。全仓 RBAC source gate 同时发现既有 P0 观察代码，以及已经提交的 e2e 专用 caller 身份准备/registration 行号漂移；逐项复核后只更新 source facts 和测试说明，保留原 caller、operations、scopes 和 guard 分类，不自动生成授权。

MySQL/Postgres 的已有可选测试需要专用 test DSN；本次默认环境未提供这些 DSN，SQL dialect 真实并发门禁未宣称通过。

## 后续 P1 切片与放行边界

本次尚未交付或启用：relay SQLite/WAL 发件箱、before-forward 双方 execution intent、lease/最大补证期限、采购证据及可信 purpose/费率快照、usage v2 和 reserve 协商、nil/zero/ambiguous 传播修正、inventory/canary/预算与 L2 报告。

下一切片将基于本 ACK seam 完成 intent/lease 与 relay 发件箱，再补原生 usage 的受控路径及探测。不能只打开本 receipt 开关就宣称解决送达前丢失、主机损毁、未知供应商成本，或放行新收费。P0 窗口与独立费用门禁继续保留。
