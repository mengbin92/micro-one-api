# DeepSeek 渠道 11：10/21–22 闭窗核验与 N3 下一步

日期：2026-10-23（Asia/Shanghai）。承接 [N3 计划](../design/next-stage-plan-2026-10-20.md#n3首个生产来源费用闭环)及 [10 月 21 日记录](deepseek-channel11-two-day-2026-10-21.md)。维护者澄清另一台电脑只安排了定时任务、没有实际执行，本窗不等待该任务产物。初次只读核对生产 owner 数据与官方 CSV，随后按明确授权发布唯一的报告导入权限、修复 owner 导入超时，已通过原 owner 接口留存 **1 份真实报告 / 1 条 run，状态 pending**。16:25 CST 补充实际调用时间分组与独立 CNY 重算，合计及逐日/费率组用量精确相符；未改价格、用户账本或追加推理调用，各阶段见下文。

## 固定范围与来源

- 时间窗：**`[2026-10-21T00:00:00+08:00, 2026-10-23T00:00:00+08:00)`**，Asia/Shanghai，48 小时。
- 生产 source：`channel/11`，provider type 6，`https://api.deepseek.com/`，当前 status 1 / healthy / authorization revision 4。
- 当前模型映射：`deepseek-flash` 启用，`deepseek-v4-pro` 未启用。
- 官方用量页：`https://platform.deepseek.com/usage`，登录后选定 **micro-one-api** Key，掩码与 10 月 21 日记录相同，日期选择 **10/21–10/22**。页面声明 GMT+8、可能延迟 5 分钟。CSV 账号散列与此前快照一致。
- 当前 revision/Key 选择与既有来源证据相符；本轮没有重新解密凭据，也没有独立证明完整两天的历史凭据生效区间。

生产聚合采样时间为 **08:17:06 CST**，官方 CSV 导出约 **08:16 CST**。SQL 使用 owner schema（`oneapi_channel`、`oneapi_billing`、`oneapi_log`）的一致性只读事务，每条 SQL 最长 10 秒；只保存聚合、模型名、来源状态和有限布尔开关，不收集用户文本、原始请求 ID 或凭据。

## 请求数与 token 合计

| 维度 | 生产消费账本 | 已归档直连 internal_test | 官方 CSV | 合计差额 |
| --- | ---: | ---: | ---: | ---: |
| 请求 / 消费条数 | 201 | 2 | 203 | 0 |
| 未缓存输入 token | 319,226 | 16 | 319,242 | 0 |
| 缓存读取 token | 30,420,736 | 0 | 30,420,736 | 0 |
| 输出 token | 92,726 | 2 | 92,728 | 0 |
| 总 token | 30,832,688 | 18 | 30,832,706 | 0 |

201 条账本均为 consume，canonical_present=1、usage_semantics=openai_subset、cost_audit_status=priced；10/21 为 185 条，10/22 为 16 条。官方相应为 187 / 16 次，差额是 10/21 的两次已归档直连测试。生产 consume 日志也为 201 条，另有 402 条 audit；不能将全部日志行当作请求数。

两次测试继续使用原始外部归档，不补写成 Relay 用户请求。账本按 created_at、日志按 created_at 归集；这证明所取记账窗的聚合相符，不能代替按可靠 upstream send 时间建立的执行覆盖或逐请求关联。

## 官方费用报告自洽

| 账期日（GMT+8） | 供应商报告金额（CNY） |
| --- | ---: |
| 2026-10-21 | 1.4846006400000000 |
| 2026-10-22 | 0.5624083200000000 |
| 合计 | **2.0470089600000000** |

费用 CSV 3 行，token CSV 11 行。使用 Decimal 计算费用合计与全部已提供价格行的 `amount × price`，差额精确为 **0**。官方费用按钱包拆分为 Granted **0.8109963200000000 CNY**、Paid **1.2360126400000000 CNY**；这里报告供应商费用，不推断余额快照变化、充值扣款凭证或税务发票。

页面显示 `2.04 CNY`，不能用其显示精度替代 CSV 的精确金额。CSV 具有不同 token 单价行，归集保留每行费率，不能以一个当前统一单价乘两天 token。现有 owner 采购估价为 USD，未与这份 CNY 金额做相减或汇率换算；费用报告内部自洽不代表两侧金额核验通过。

### 维护者澄清与线上价目核对

维护者随后明确要求使用现有 owner 执行，并说明线上采用 OpenRouter 中 DeepSeek 的计价，DeepSeek 官方另有忙闲时计费。本次保留这两个口径，不以价目来源不同认定计费错误，也不把改用官方价、换汇或开发分时定价作为保存真实 pending 报告的前提。

10 月 23 日 08:30:58 CST 只读核对实际配置：`ModelPrice.deepseek-flash`（客户售价）与 `UpstreamModelPrice.deepseek-flash`（来源采购估价）均为每百万 token **未缓存输入 0.30 / 缓存读取 0.06 / 输出 1.20 USD**。该窗口 201 条消费关联同一份客户价格快照，group_ratio=1；当前 exact `channel:11:deepseek-flash` 与 legacy `11:deepseek-flash` 采购价项均未配置。普通账本路径可以使用 bare-model fallback，durable execution 只冻结精确来源价，二者不能混同。见 [价格上下文](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/pricing-context.json)。价目来源按维护者说明记录，不声称已证明与 OpenRouter 当前全部时段/模型变体的报价完全相同。

依据官方 CSV 自带的历史逐行单价对照费率分组；CSV 本身没有逐请求发送时间或忙闲标签，下文另用真实调用时间独立核对：

| 官方价格组 | 未缓存输入 token | 缓存读取 token | 输出 token | 费用（CNY） |
| --- | ---: | ---: | ---: | ---: |
| 闲时 | 119,884 | 12,708,224 | 44,020 | 0.55012848 |
| 忙时 | 199,358 | 17,712,512 | 48,708 | 1.49688048 |
| 合计 | 319,242 | 30,420,736 | 92,728 | 2.04700896 |

[DeepSeek 官方价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)在本次读取时列出北京时间工作日（不含中国法定节假日）09:00–12:00、14:00–18:00 为忙时。历史金额仍取 CSV 中实际单价，不用当前规则反推缺失的请求发送时间。[OpenRouter 模型价格文档](https://openrouter.ai/docs/guides/overview/models)将报价单位定义为 USD，并提供分时 overrides 语义；现有静态配置值与官方 CNY 实报分别保留。

### 按真实调用时间区分忙闲并重算 CNY

维护者补充：**线上记录的价格统一采用忙时基准**，并要求按调用记录时间比较。这是既有配置口径，不表示所有实际调用均发生在忙时，也不要求先为当前计价增加自动忙闲切换。

**16:25:13 CST** 再次以一致性只读事务取得 201 条 consume、201 条对应请求计时摘要及 201 条 ledger。先按 root 关联计时，逐项验证唯一成功 attempt 的 request/reservation、`channel/11` 和 `deepseek-flash`，再按 reservation 对应 ledger；201 条三类用量完全一致，均为 legacy `/v1/responses` 成功 SSE 路径。私有源文件 SHA256、关联检查和聚合结果见 [时间重算摘要](evidence/deepseek-channel11-closed-window-2026-10-23/time-pricing-analysis.json)。公开 [203 条分组记录](evidence/deepseek-channel11-closed-window-2026-10-23/time-pricing-records.csv)只含序号、时间、purpose、三类 token 与 CNY 重算，不含账号、凭据或原始请求 ID；其中两条外部测试仍保持 `internal_test` 来源。

分类先使用独立的本地时间，再核对官方 CSV。请求入口至 handler 完成覆盖本次单 attempt 的上游交互；异步结算可以晚于 handler。保守比较区间为 `[request.started_at_ms, max(handler_finished_at_ms+1ms, ledger.created_at+1ms, consume.created_at+1s))`，覆盖毫秒/秒截断及记账延迟。检查整个区间内所有 09:00、12:00、14:00、18:00 切换点，**201 条均未跨计价边界**，因此可唯一确定忙闲组，无需捏造精确 upstream send 时间。尤其 13:59:59 的日志只给出 `<14:00:00` 的上界，不应被误算成发生在 14:00。

本窗两天为普通周三、周四；按 CST 半开区间 `[09:00,12:00)`、`[14:00,18:00)` 为忙时，其余闲时。采用与原始 CSV 一致的 CNY/百万 token 历史费率：未缓存输入 / 缓存读取 / 输出，忙时 `2 / 0.04 / 8`、闲时 `1 / 0.02 / 4`。以 Decimal 独立计算，无逐条金额舍入：

| 日期 / 时段 / 来源 | 调用数 | 未缓存输入 | 缓存读取 | 输出 | 重算 CNY |
| --- | ---: | ---: | ---: | ---: | ---: |
| 10/21 闲时生产 | 106 | 119,884 | 12,708,224 | 44,020 | 0.55012848 |
| 10/21 忙时生产 | 79 | 26,094 | 14,968,704 | 35,436 | 0.93442416 |
| 10/22 忙时生产 | 16 | 173,248 | 2,743,808 | 13,270 | 0.56240832 |
| 10/21 10:47 忙时外部测试 | 2 | 16 | 0 | 2 | 0.00004800 |
| 合计 | **203** | **319,242** | **30,420,736** | **92,728** | **2.04700896** |

生产 201 条合计 **2.04696096 CNY**；加入原有两次直连测试后，逐日、费率组、三类 token 及总 CNY 与官方 CSV 精确相符，差额 **0**。忙时 97（含测试 2）/ 闲时 106 是本地时间分类条数；官方 CSV 只提供每日总请求数，未单独提供分时请求数。独立复算没有使用官方 token 数量或金额反推本地分类。

新增结论为 **TIME_BAND_CNY_RECOMPUTATION_MATCHED**。这是事后、独立、同币种的采购费率重算，不是请求执行时已冻结的 CNY 估价，也不是 invoice/逐请求实扣证明。正式 owner 的 USD 加载逻辑和原 pending run 未改写，仍需评审这份证据的正式承载方式并补来源覆盖、未知探针及供应商最终性。

### 现有 owner 命令预览

已使用当前仓库原有 `vendor-reconcile` 命令完成输入校验及真实 owner 证据快照预览，没有新增核验算法。生产导出使用一致性只读事务，实际全表 intent 为 2,511 条；仅复制渠道 11 的 2,442 条 intent、7,316 条 event，原始 PK/payload/digest 保留在本机 0700/0600 私有 SQLite 副本。只读预览不传 `-local-attempts` 或 `-import`，snapshot 主库哈希在预览前后不变。

| 实际 owner 预览字段 | 结果 |
| --- | --- |
| report_id | `2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db` |
| status / verification_scope / cost_basis | pending / none / provider_reported |
| 比较项 | account_period / missing_local / amount_status=unknown |
| 供应商金额 | 2.0470089600000000 CNY |
| 本地估价 / 差额 | null / null |
| unknown_time_attempts / user_reservations | 2,442 / 0 |
| 命令退出码 | 1（已生成 pending，非输入或运行错误） |

这里的 2,442 是 owner 当前持久化的渠道 11 全历史探针意图，因没有可靠发送时间而保留 unresolved，不能全部归到本次 48 小时窗，也不能算作付费请求。原始 owner 输出包含关联 ID，仅私有保留；[脱敏预览摘要](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-preview-summary.json)与 [输入来源说明](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/input-provenance.json)可复核。

归一化 CSV 只填官方金额，五个 token 桶全部留空；原始报告明确提供的三个 token 类型继续作为旁证，不为填满五桶而制造缓存创建零值。账号使用包含所选 Key 范围的稳定散列别名，没有捏造供应商 project。mapping 仅引用已观察的 revision 4 绑定跨度，不声明完整账期覆盖；complete=false、双方水位未提供、coverage_evidence_id 为空。

正式持久化继续使用当前在线 Admin → Billing → IAM owner 的既有导入接口。管理页目前只有查询/导出，系统 Token 被 owner 拒绝为 user operator required；维护者随后在真实登录页执行同源脚本。**10:27 CST 回执确认当前会话缺少 `billing.vendor_report.import`，在 POST 前停止，未导入。** 10:28 CST 只读回查固定 report ID 的报告和 run 均为空、生产总数仍为 0/0。会话凭据不输出或归档，未通过 CLI/直接改表绕过拒绝。

10:31 CST 的 [权限现状](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-import-permission-readiness.json)进一步确认：import 权限 239 仍为 draft/revision 1，无任何角色 grant；read 权限 238 已 enabled，只有 builtin root（role 4、revision 3、当前一名成员）拥有读取 grant。binding 的实际可执行性由 owner 代码登记决定，持久化 unbound 元数据本身不能发布入口。

这与 [此前只读发布](evidence-management-pages-deploy-2026-10-16.md)一致：migration 136 只发布两项 read，未扩大历史 root 的有限写权限。root 角色在普通 IAM 角色编辑/用户分配入口受保护，不能把本次缺口当成会话过期，或尝试通过直接编辑 root 授权绕过。维护者随后明确回复“授权发布这一项权限并继续导入”，按 [一项权限发布方案](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/permission-publication-proposal.json)执行下述发布。

### 已授权的唯一 import 权限发布

新增 identity-owned migration **140_publish_vendor_report_import**，三方言均仅发布 `billing.vendor_report.import` 给既有 enabled builtin root；同步 fresh D0 的有限 root 清单，不新增用户绑定或其他操作。继承 136 的 policy 行锁、首次 guard、显式 deny/窄 allow 保留及固定审计 receipt。MySQL 使用 `DROP TEMPORARY TABLE`，保持权限修改与 schema_migrations 登记的同一事务。

SQLite、MySQL 8.4、PostgreSQL 18 的实际 runner 验收通过，覆盖仅一项权限/既有 root、首次停用/归档/owner 错配等完整回滚、receipt 重放不复活后续停用，以及审计失败/MySQL 最终迁移记账失败的原子回滚。IAM biz 全部 `TestIAM`、迁移治理、876 项 RBAC 契约和 diff 检查通过。见 [三库测试日志](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/migration-tests.txt)。

**10:45:31 CST 已完成生产权限发布**：复用 mysql 容器内现有静态 Linux/amd64 migrate 制品，隔离 staging 仅含 140 与 identity ownership，先 status 确认唯一 pending，再 apply。生产未构建镜像或重启服务。私有 Identity 权限表备份及前后状态保留在生产，具体路径/哈希见 [发布结果](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/permission-publication-result.json)。

| 字段 | 发布前 | 发布后 |
| --- | --- | --- |
| import 状态 / revision | draft / 1 | enabled / 2 |
| root import allow grant | 0 | 1 |
| root / policy / catalog revision | 3 / 8 / 3 | 4 / 9 / 4 |
| 140 migration / 审计 receipt | 0 / 0 | 1 / 1 |

其他 permission/grant、非 root 角色、用户绑定及会话的指纹前后一致。重复 runner 返回 `nothing to apply`，状态未变。后续导入仍通过已登录的原 Admin/Billing/IAM owner，不以权限迁移直接写报告表。

### Owner 导入 500 的诊断与修复

权限获准后的真实 POST 返回 500。生产 Admin 日志保留 **10:54:09 CST，Billing RPC DeadlineExceeded**。只读核对发现原 loader 对每个选中 intent 单独查 events；原真实快照需 **2,443 次 SELECT**。线上事件表没有 intent 定位索引，`WHERE intent_id=? ORDER BY id LIMIT 1001` 的计划沿 PRIMARY 扫描后过滤；Billing gRPC 期限为原有 **10 秒**。见 [诊断证据](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-query-diagnosis.json)和 [错误日志](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-import-error-logs.json)。

在真实 repo seam 建立回归：64 个合法 intent 的旧实现为 **65 次 SQL**，超过固定查询上限而红灯；原先的 digest/intent 绑定、native usage、冻结估价和只读事务校验仍通过，未把合法 pending 当成故障。修复以 **256 个 intent 一批、1,001 条 event 一页**读取，使用数据库 `(intent_id,id)` 后继游标；跨页累计每 intent 计数，第 1,001 条仍拒绝。保留无响应、unknown_time、late arrival、原 intent/event 顺序和全部既有证明校验，不按 received_at 或账期截掉旧证据。

SQLite、MySQL 8.4、PostgreSQL 18 的真实 repo 回归均通过：64 intent **65 → 2 次 SQL**，513 intent 跨批次 **4 次 SQL**，覆盖跨页缓存及冻结 USD 估价、篡改、多 send 冲突及只读事务。相关 Vendor/Execution 回归和 Vendor race 通过。私有真实生产快照的新旧完整报告逐字节一致，均为 2,506,917 字节，SHA256 同为 `ad1f9d74817f3afedaeccd1511a594670b0876a5a404c8eea8a72a83cd8c9e10`；见 [等价重放](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-batch-replay.json)及索引中的红/绿日志。

**11:13 CST 已发布** Billing-owned migration **141_add_execution_intent_event_index** 和 Billing 单服务修复。索引 `(intent_id,id)` 生效，真实 EXPLAIN 由 PRIMARY 扫描变为 `ref / idx_execution_intent_event / 约 3 行`，迁移重复运行无变更。MySQL 建索引会隐式提交，因此分别核对物理索引和迁移记录，不宣称 DDL 与元数据能一起回滚。见 [141 发布结果](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/execution-index-publication.json)。

服务按标准流程在本机跨构建 Linux/amd64、传输并加载，生产没有编译。Billing image 为 `sha256:498e818116057e3b94ddd4f30a6d22a25103dccc04b629ec7dcc1b0813bc7235`，源码摘要 `c4d9f13123c300e5c76d4a636c895afd556569215e1253a91c94a8c325bb069b`；回滚标签 `rollback-20261023-111149`。健康检查 HTTP 200、restart=0，门禁与 timeout 环境保持，仍使用原 10 秒 RPC 期限。见 [部署核验](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/billing-deploy-result.json)。

### 真实 owner 导入已完成

维护者重放原固定请求后，**11:17 CST** 同源脚本确认成功并在 owner 列表回读到同一报告。**11:18:04 CST** 独立只读核验：

| 实际落库字段 | 结果 |
| --- | --- |
| report_id | `2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db` |
| report / run 数量 | **1 / 1** |
| run sequence / actor | **1 / iam-user:1** |
| run ID（也是两份 payload SHA256） | `6b74a1c12409e0321f31688c18228d7e265a07a1ccc8987f5c5217a3445be45a` |
| status / cost_basis / verification_scope | **pending / provider_reported / none** |
| 原币费用 | **2.0470089600000000 CNY** |
| 比较项 / 金额状态 | missing_local / unknown |
| unknown_time_attempts / user_reservations | **2,476 / 0** |
| Billing owner 写审计 | billing.vendor_report.import / actor 1 / success，原因包含固定窗口 |

报告与 run payload 哈希一致，首条 run 序号不是 0；没有重复报告或伪造 user attempts。2,476 个 unresolved 是当前 source 全历史无 send 时间探针，比早先快照自然增加，不能当成本账期推理请求数。临时会话文件已删除，凭据未归档。

原 500 链路已通过同一真实 owner 请求回放验证成功，而非只据健康检查结案；见 [成功回执摘要](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-import-result.json)、[独立落库与审计核验](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-persistence-verification.json)及 [原故障回放结论](evidence/deepseek-channel11-closed-window-2026-10-23/owner-execution/owner-timeout-fix-verification.json)。N3 的真实 report/run 产物已形成，完整费用闭环的剩余材料继续保持 pending。

## 正式 owner 核验的状态与剩余缺口

1. **执行覆盖不足。** 查询所有当前持久化的渠道 11 事件，没有 record.started_at_unix_nano 落在固定窗口内的事件。按 received_at 另查到 598 个 health_probe intent、595 个 checkpoint、595 个 terminal，均缺 send 时间，后两类 endpoint 为 `/models`。它们不能替代付费模型请求。采样只覆盖查询时已持久化的数据，不保证不存在迟到或外部证据。
2. **投递开关不等于路径覆盖。** 当前 Relay/Billing 的 execution delivery 均为 true；08:21 CST 另只读确认当前 V2 allowlist 未配置，精确 `channel:11:deepseek-flash` 不在准入名单，仅保存成员布尔值。Relay 的 `RELAY_UPSTREAM_EXECUTION_TIME_V1` 未设置，契约开关为 false。此为当前运行快照，尚未建立驱动实际消费入口的故障复现，也未证明历史配置，不断言缺口根因或直接扩大准入。后续先定位实际入口、协议、准入和采集路径。
3. **独立原币重算已相符，owner 尚未承载。** 16:25 CST 补证已按实际请求时间区间确定忙闲组，本地三类 usage 乘历史 CNY 费率后与官方逐日/费率组及总额精确相符。现有 [owner loader](../../app/billing/internal/data/vendor_attempt_reader.go)仍仅从冻结采购估价构造 USD/currency，不能把本次事后重算伪称历史冻结 CNY 或直接改写原 run。账本 priced 和重算相符均不授予供应商实扣结论。
4. **最终性后续已按维护者确认登记。** 初次及后续重导出均没有供应商显式最终水位，规范化费用/用量行保持相符。维护者随后明确确认“不会再更正了”，本账期以 `maintainer_confirmation` 保存来源，并准备完整性/期末水位的新 version/supersedes；原 v1 不改写。见 [owner 补证收口](deepseek-owner-retrospective-2026-10-23.md)。这不补足历史执行覆盖，也不授予 request/invoice 结论。
5. **真实 run 已留存。** 初次只读采样时报告/run 为 0/0；后续通过真实 owner 已形成 1/1、sequence=1、status=pending。该进展不补造执行覆盖、最终水位或可比采购金额。

当前结论为 **CLOSED_WINDOW_USAGE_AGGREGATES_MATCHED**、**PROVIDER_COST_EXPORT_SELF_CONSISTENT**、**TIME_BAND_CNY_RECOMPUTATION_MATCHED**；**N3_NOT_ACCEPTED**。本次已完成唯一 import 权限发布、原 owner 报告留存及调用时间/CNY 补证；N4、收费扩面和新增付费采样继续按各自关卡办理。

## 后续执行顺序

具体责任与完成条件已写回 [N3.1–N3.5](../design/next-stage-plan-2026-10-20.md#n3首个生产来源费用闭环)。N3.2 预览与 N3.5 首次真实 owner 留存已完成；后续路径复现、事后补证/最终性来源模型、页面展示和继任候选见 [本轮收口](deepseek-owner-retrospective-2026-10-23.md)，已完成本机跨构建、Billing/前端发布及原 IAM owner 的继任导入；生产 2 reports / 2 runs、继任 sequence=1、actor iam-user:1、审计成功，原 payload/run 哈希不变。原报告的本地补证可追加不可变 run；维护者最终性确认引起 complete/vendor_watermark/确认身份变化，已准备新 version/supersedes，即使金额不变也不重写原报告。历史本地覆盖缺口保留。

现有 [vendor-reconcile](../../app/billing/cmd/vendor-reconcile/main.go)的 `-vendor-bill` 接收固定 13 列 normalized CSV，不能直接传官方 cost/amount CSV。固定 `account_period` 范围后归集一行，request_id 留空；五个 token 桶全部有据或全部留空，原始 token 另存旁证，不能为通过校验擅自补零。

只读预览使用专用 SELECT-only owner DSN，不加 `-import`。正式留存可用 CLI `-import` 或已有 IAM 入口，允许如实保存 pending；CLI 状态退出 1 不等于存储失败，需同时核对输出和 report/run。禁止将 `-local-attempts` 与 `-import` 混用；供应商报告字段变更需新 version/supersedes，本地补证可追加不可变 run。

## 归档与离线复核

[证据索引](evidence/deepseek-channel11-closed-window-2026-10-23/index.json)包含官方原始 ZIP/CSV 哈希、脱敏 CSV、线上只读统计、配置快照及聚合结果。原始 ZIP 保留在本机私有文件（权限 0600）；仓库 CSV 的 user_id 使用 SHA256、api_key 已移除明文值，其余费用/token 字段保留。原始下载不进入 Git。

```bash
python3 docs/runbooks/evidence/deepseek-channel11-closed-window-2026-10-23/check.py
```

此命令仅复核已保存文件哈希、固定窗口、来源、每日请求/token 合计、完整时间区间的忙闲分类与独立 CNY 重算，以及已归档的权限发布、修复重放、部署和真实 report/run/审计结果；仍保留 N3 待补证条件，不访问生产或供应商。
