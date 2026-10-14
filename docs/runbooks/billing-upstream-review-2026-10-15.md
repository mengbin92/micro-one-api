# 计费与上游契约方案实现复审

2026-10-15。审查基线：`05d6870c`。对照[计费准确性与上游成本证据方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游契约演进方案](../design/upstream-contract-evolution-plan-2026-10-13.md)，并核对 P0–P3 工程、运营补齐及部署记录。

本轮完成源码与工程差异修复，修复、回归测试与本记录一起提交。尚未构建发布制品或部署；没有执行真实供应商费用/推理调用，也没有修改收费名单、用户售价、已冻结快照或历史账本。现场关卡仍以真实资料和独立签名验收。

## 复审结果与修复

下列问题使用现有测试 seam 或最小回归先证明失败，再修复。表中测试名可直接通过 `go test <对应目录> -run <测试名> -count=1` 重放。诊断遵循仓库 [diagnosing-bugs](../../.agents/skills/diagnosing-bugs/SKILL.md)；本次请求没有预设故障症状，因此先跑既有基线测试，再为审查发现建立红/绿反馈环。

| 范围 / 严重性 | 根因与影响 | 修复与回归证据 |
|---|---|---|
| 原生状态 / P1 | `NativeUsageV2` 将已知缓存歧义改为 unsupported，丢失 reported cache 与两候选；断流又将 invalid/unsupported/ambiguous 一律改为 missing | 保存原始信任状态、原因、缓存值和候选；未完整执行不能得到 canonical。`internal/biz`: `TestNativeUsageV2PreservesAmbiguousCacheEvidence`、`TestNativeUsageV2PreservesFaultWhenExecutionIsIncomplete` |
| Chat SSE / P1 | 标准内容块 `usage:null` 触发持续 invalid；finish_reason 可在最终 usage / `[DONE]` 前使流被认作完整；早期无 usage 的供应商 ID 被丢弃 | 按 chunk 语义处理 null，实际 SSE 只由 `[DONE]` 完成，独立保留 ID。`domain/upstream/evidence`: `TestNativeChatStreamNullUsageAndTermination` |
| 终结与桶不变量 / P1 | Responses `status=incomplete` 仅因 HTTP EOF 被认作完成；reasoning 明细大于输出仍 verified；歧义路径绕过候选总量溢出检查 | 非成功终结保留 partial usage 采购证据，拒绝伪完成、subset 越界和溢出。`TestHTTPObservationIncompleteResponseIsNotCompletedAtEOF`、`TestNativeTokenDetailsAndAmbiguousCandidatesCannotOverflow` |
| canary 能力 / P1 | 空 message/delta/output/part 与仅工具名即被当作能力有效，损坏或不符合本地假工具 schema 的参数也通过 | 验证实际文本或合法空对象工具输入，按协议合并流式参数。`domain/execution`: `TestProbeOutputRequiresActualContentAndValidToolInput` |
| 执行身份 / P1 | durable Responses 拒绝后在同 intent 再发 Chat；HTTP 307/308 又可在 client 内部重新发送，外层准入与采购投影无法看到 | native-v2 持久 intent 只允许一次 HTTP 发送；发送前选择转换或独立准入新 attempt。该路径保留 redirect/拒绝证据，不跟随 redirect。旧非持久门禁行为仍由原测试固定。`internal/server`: `TestDurableResponsesNeverRetriesByChangingProtocol`、`TestDurableIntentCannotAuthorizeASecondSend`；evidence: `TestDurableHTTPIntentDoesNotFollowRedirects` |
| outbox 容量 / P1 | 只检查当前 pending，使新 checkpoint 挤掉已准入请求的后续证据；仅预留终结仍遗漏发送后必写的 header checkpoint | intent 持久预留 header/terminal，user 另预留 final；容量包括 SQLite/WAL 增长，预留跨 ACK/重启并按事件类别消费。`internal/data`: `TestExecutionOutboxFullCapacityPreservesRealHTTPHeadersTerminalAndSettlement` |
| outbox 恢复 / P1 | 旧库补预留可能超过总预算；批量到期清理又随新写容量拒绝回滚，导致永久阻塞 | 新准入失败关闭，旧 protected 事件按实际磁盘空间排空；到期维护独立分批提交，不丢迟到证据。`TestExecutionOutboxLegacyOversubscriptionKeepsCompletionLiveAndRejectsNewAdmission`、`TestExecutionOutboxExpiredLegacyReservationsAreClearedWithoutRollbackDeadlock` |
| ACK / P1 | tombstone 从创建时间保留，迟到 ACK 可立即丢失防重放保护；错误 digest/不存在 ID 的 ACK 返回成功 | 从首次 ACK 起保留七日，升级旧 schema 时启动保留时钟，明确拒绝错误 receipt。`TestExecutionOutboxACKRetentionStartsWhenReceived`、`TestExecutionOutboxRejectsUnknownOrMismatchedACK` |
| 发送后持久失败 / P1 | header 保存失败直接丢弃响应；已保存 incomplete terminal 后仍被旧 checkpoint 错误阻挡 final，且发送后的存储错误可被当作可重试 | 用 protected terminal 留下 incomplete 身份证据；持久错误标为 post-forward；成功终结保存后允许按冻结政策 release。`TestDurableHTTPHeaderFailureRetainsIncompleteTerminal`、`TestDurableTerminalCanReleaseAfterCheckpointFailure` |
| 已结算重放 / P1 | native-v2 deadline 检查先于 committed 幂等返回；过期后副作用重试与 settled receipt 失效 | 已完成的财务结果与 immutable receipt 可继续读取；未结算请求仍不能超期新扣费。billing: `TestNativeCommittedReplaySurvivesEvidenceDeadline`、`TestExecutionCommittedFinalReceiptSurvivesEvidenceDeadline` |
| 结算恢复竞争 / P1 | pending 候选 SELECT 检查 next_retry_at，claim UPDATE 不检查；旧候选可覆盖另一 worker 刚写的退避 | CAS 再检查同一 due 条件。`TestSettlementRecoveryRechecksRetryDeadlineAfterCandidateSelection` |
| 对账结果顺序 / P1 | 同秒补证按内容 SHA 排序，最新 matched 结果可能显示为旧 pending | 不可变 run 由报告行锁下单调序号排序；迁移 135 保留历史选择口径。`TestVendorLatestReplayFollowsIntakeOrderWithinOneSecond` |
| unknown 更正 / P1 | 空原报告金额集合求和为 0，将未知原值制造为已知更正差额 | 原或更正金额缺失时 `CorrectionDelta=nil`。`TestVendorCorrectionWithoutOriginalAmountStaysUnknown` |
| 有限规则冲突 / P1 | 同层覆写、父子 rename 目标、重复 JSON key 可通过；float64 舍入使越界整数通过 reject | 发布/执行前拒绝冲突，按原始 decimal 精确比较；限制长度、指数和有限范围，避免大指数资源耗尽。`domain/upstream/contract` 的 conflict/duplicate/number/exponent 回归 |
| repair 授权 / P1 | automatic_until=0 可使 reviewer 到期约束失效，旧零期限或撤销后的 gray 还可能恢复 | owner 固定签名期限，过期或撤销后拒绝恢复与准入。channel: `TestRepairPublicationRejectsExpiredAutomaticDeadline` 与既有 repair 授权负例 |
| 灰度样本 / P1 | recover / automatic_recover / rollback 继承旧候选的时间、完整样本与失败数 | 重启该 revision 的灰度观察窗和计数，不借旧版本 100 样本晋级。`TestGrayRecoveryAndRollbackRequireNewObservationWindow` |
| 候选与当前版本 / P1 | 失败候选 probe 可直接 quarantine 正在服务的另一个有效 revision | 只隔离实际失败的 current；失败候选不能 publish，已有 current 事故也不会被旧结果解除。`TestRollbackRequiresFreshProofAndRemovedOldProtocolStaysQuarantined` 及契约生命周期回归 |
| 探测周期 / P2 | jitter ticker 总在 completion+T 之前醒来，健康目标无排队仍约 2T 执行 | 按最早 target due 调度，保持有界 jitter 和失败退避。`TestSuccessfulProbeRepeatsWithinConfiguredPeriod` |
| 探测预算 / P1 | 晚到超 token 结果被 lease_owner 条件挡住；完成时未复核 frozen target digest | 超预算独立暂停 source，保留新租约，跨日不自动恢复；配置变动拒绝。`TestLateProbeOverrunPausesSourceWithoutClearingNewLease`、`TestProbeCompletionRemainsBoundToFrozenTarget` |
| 独立回放 / P1 | 候选用直接 ObserveFrame 绕过 live SSE 终结规则，可凭 finish_reason 宣称完整 | 离线 fixture 走实际 SSE Write seam，以实际 `[DONE]` 帧证明终止，同步独立 fixture/CLI/worker。`TestCandidateReplayRequiresActualChatStreamTerminator`、`TestBundledIndependentFixturesReplay` |
| 运营 PASS / P1 | reconciliation partial/failed、隐藏 issues、非法响应或全跳过检查仍被脚本当作零差异 | 只接受 completed、差异可见、有效 run_at/数组且无 error 的真实 owner 投影。`python3 scripts/test-reconcile.py`：七个离线脚本回归 |

Chat 的 null usage 与最终 usage 块语义按 [OpenAI 官方用量说明](https://help.openai.com/en/articles/10478918-reviewing-api-usage-and-costs)核对。五桶含义保持原静态契约，未安装或未获收费批准的 DeepSeek 候选仍只离线回放；parser 规则未升级为新收费能力。原生错误证据不会经 projection 隐去。

## 两份方案的差异闭合

| 方案关卡 | 本轮源码复核范围 | 剩余验收边界 |
|---|---|---|
| P0 观测 | 转换前原生 JSON/SSE/WS 证据、影子 parser、零值/缺失/错误分类及版本记录 | 生产两周基线与新的固定窗口由独立现场材料确认 |
| P1 证据与送达 | reserve intent、relay SQLite/WAL、inbox digest、checkpoint/final、lease/deadline、迟到释放与持久重试 | 真正主机损毁不由单机 SQLite 保证；缺历史证据保持 unknown |
| P2 外部费用与契约 | 固定账号/账期/原币 CSV/API、attempt 采购去重、不可变报告/更正；CAS、有限规则、事故、受控恢复与灰度 | 当前工程测试不能替代真实完整账期、供应商粒度和收费批准；历史多 HTTP 共用 intent 保持明确核验失败，不伪造采购身份 |
| P3 运营与候选修复 | 受限 worker、独立回放/reviewer/CI 接缝、预算期限、IAM/报表、告警与脚本判定 | reviewer 生产密钥、IAM 配置、真实通知、30 分钟/100 完整样本、5 分钟实例收敛与真实费用 CSV/API 一致性仍需独立验收 |

## 迁移与行为影响

发布 billing 新查询前应用 `135_vendor_reconciliation_run_sequence.sql`，MySQL/Postgres/SQLite mirror 与 ownership 同步。SQLite 已用真实新装/升级/backfill/重复应用测试验证；本机未提供 MySQL/Postgres 临时测试 DSN，相关实库测试明确 skip，不能据此宣称这两个方言实库已验。

relay 本地 outbox schema 自动升级。新请求为完成事件预留容量，因此可能更早背压；已有已持久 intent 的完成与 ACK/清理继续可推进。已 ACK 且旧 schema 已清空 payload 的 intent 无法重建缺失信息，保留有界迟到证据路径，不补造 purpose/deadline。

native-v2 拒绝响应后的协议 fallback 与 redirect 不再共用同一采购 intent。新制品仍需正常的灰度发布与费用差异门禁；修复测试通过不自动扩大收费范围。

## 验证记录

原始输出保存在 [本轮证据目录](evidence/billing-upstream-review-2026-10-15/)。验证范围包括完整默认单元测试集、受影响模块 race、五个服务内集成包、架构检查、863 行 IAM 入口矩阵、迁移治理、格式和运营 shell 回归；结果与摘要见该目录 `index.json`。

没有新增 API/proto 或 Wire 注入变化，无需重新生成，未手改 generated files。没有运行应用镜像构建、Docker 部署或生产数据迁移。
