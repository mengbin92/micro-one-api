# 下一阶段计划：计时内存预算与供应商证据可读性

> **当前执行入口**。制定：2026-10-20（Asia/Shanghai），规划基线 `develop@10054011`，已合入验证后的 v0.36.2。接替 [10 月 18 日计划](next-stage-plan-2026-10-18.md)。N1/N2 实现及最大输入/前端隔离验收已交付，完整原生三库 SQL 容量 CI 已通过；见 [实施与证据](../runbooks/p1-memory-vendor-evidence-2026-10-20.md)。2026-10-20 已完成 [生产部署](../runbooks/p1-production-deployment-2026-10-20.md)并合入 develop，2026-10-21 已正式发布 [v0.36.3](../releases/release-v0.36.3.md)，完整 Release、CI 与安全检查通过。剩余 N3/N4 材料条件见 [发布核验与后续记录](../runbooks/post-v0.36.3-followups-2026-10-21.md)，不扩大灰度。

> **N3 更新：2026-10-23**。DeepSeek 渠道 11 已取得 10/21–22 官方闭窗报告；201 条生产调用已按真实请求时间区间分为忙时 95 / 闲时 106，加入两次忙时直连测试后，逐日/费率组的三类 token 与 **2.04700896 CNY** 均精确相符。原 owner 留存 **1 份报告 / 1 条 run，仍为 pending**。另一台电脑的定时任务未实际执行，不再作为补证依赖；剩余 owner 覆盖/最终水位等条件以本文 N3 为准。

## 1. 阶段目标与范围

本阶段先交付两项无需生产模型调用的工程：验证计时摘要在最大合法输入下的内存与恢复边界，让供应商核验页直接展示现有证据与补证依据。真实供应商账期、契约晋级和 executor 准入继续按条件办理，不阻塞两项工程交付。

**独立生产验收不并入本计划。** 原存储稳态窗口为 2026-10-18 10:10:20 至 2026-10-20 10:10:20 CST，现已结束，结论 **FAIL**，自动任务 **PAUSED**；维护者确认线上 Jaeger 已调大至 **768 MiB**，新实例的内存、TTL 与生产 WS 验收仍由独立安排承接，不拼接旧窗口。依据见 [48h 最终记录](../runbooks/post-v0.36.0-acceptance-2026-10-18.md)。本计划不重开该自动任务。

已交付内容不再立项：

- v0.36.1：普通 WS 第 33 轮误断开修复、Lite 独立服务身份与安装/空卷恢复验收。
- v0.36.2：SQL 分组持久化、PostgreSQL claim ID、授权范围分页与清理游标、139 索引迁移、Q3 身份/清理修复、双架构原生构建。见 [发布记录](../releases/release-v0.36.2.md)。隔离通过保留原负载与资源边界。
- 上游契约及费用证据 P0–P3 工程，包括有限规则、签名/CAS、隔离与回滚、费用 API、operator/worker 和管理入口；现场关卡尚未通过不等于工程尚未实现。
- 双语 README/Lite Quickstart、安全 Markdown、系统字体和关键分支门禁。

## 2. 顺序、依赖与交付

| ID / 优先级 | 工作与责任范围 | 依赖 | 最小交付 | 粗估工程量 |
| --- | --- | --- | --- | --- |
| N1 / P1 | 最大合法摘要内存预算；Relay / Log owner | 无生产资料依赖；先确定各进程测试预算 | 最大输入夹具、受限容器峰值/恢复证据、必要时最小修复 | 2–3 个工程日 |
| N2 / P1 | 供应商核验只读详情；Web / Billing owner | 现有报告响应与权限；不等 N1 | 完整性、水位、来源映射、差异与更正证据展示及回归 | 1–2 个工程日 |
| N3 / 条件 P1 | 首个生产来源费用闭环；运营 / Billing owner | DeepSeek 11 已取得闭窗报告及独立时间分组/CNY 重算，原 owner report/run 已留存 pending；仍缺 owner 覆盖/原币证据承载及最终水位 | 真实归档、首条核验 run 及事后原币比对已交付；后续补证再重放，不升级未知结论 | 按下列 N3 剩余补证项推进 |
| N4 / 条件项 | 一个上游契约候选的现场准入；运营 / Channel owner | 已审候选、独立签名权限、凭据与预算 | 一次候选到晋级或隔离/回滚的完整记录 | 不承诺采样等待时长 |

**原执行估算：N1 与 N2 可并行，随后汇总验收与交付；N3/N4 单独判断条件。** 两项工程按一轮约 3–5 个工程日估算，不含现场等待。N1/N2 现已发布；N3 已取得 DeepSeek 渠道 11 的 10/21–22 官方闭窗 CSV，请求数/token 合计可由 201 条生产消费和两次已归档测试解释，正式 owner 核验仍待补证。当前下一步见下文 N3 及 [10 月 23 日核验记录](../runbooks/deepseek-channel11-closed-window-2026-10-23.md)。N4 仍待 [reviewer/候选/预算](../runbooks/post-v0.36.3-followups-2026-10-21.md#下一步所需材料与通过条件)。

## 3. N1：最大合法摘要内存与恢复预算

### 实施前缺口（已交付）

[exporter](../../domain/requesttrace/exporter.go) 固定 4,096 项队列和 256 个 worker，最多保留 4,352 项；[Log 分组](../../app/log/internal/data/request_timing_batch.go) 每仓库最多持有 128 份私有快照。[摘要校验](../../domain/requesttrace/timing.go) 已限制 128 attempts、22 个白名单 stages、32 missing reasons 及字段长度，不能描述为输入无界；目前没有独立的单摘要序列化字节上限。

[容量记录](../runbooks/request-timing-capacity-improvement-2026-10-18.md)中的约 19.2 MB 堆分配来自小摘要夹具；最大批次测试每条也只有两个 attempts。它们尚未覆盖最大合法输入、序列化/快照复制及整个进程峰值。既有 4 CPU / 1 GiB 是隔离测量条件，不能当作生产服务已经确定的内存预算。

### 最小改动范围

1. 先登记 Relay、Log 各自的可用内存预算与留给正常业务的余量，再测量；保留空载基线、源码版本、输入字节和容器限额，不在看到结果后放宽门槛。
2. 在既有测试旁构造最大合法摘要：集合与字符串填满上限、覆盖合法 JSON 转义，各摘要使用独立字符串；记录 JSON/proto 字节，补越界一位的拒绝用例。
3. 复用 [exporter 饱和测试](../../domain/requesttrace/recorder_test.go)及 [暂停探针](../../internal/server/request_timing_checkpoint_test.go)，覆盖满队列、慢 sink、恢复和有期限关闭；记录 HeapAlloc、进程 RSS 与 cgroup 峰值，不能只以“未 OOM”判定通过。
4. 扩展 [最大批次测试](../../app/log/internal/data/request_timing_test.go)：128 条摘要 × 128 attempts，验证日志、claim、16,384 条 refs 及重放幂等。复用 [真实 RPC/SQL harness](../../internal/server/request_timing_sql_test.go)，覆盖转换、序列化与存储快照的实际复制成本。
5. 先测后改。只有超过预算才选择共享校验或保留策略的最小修复；保持既有合法调用兼容，不能默默丢掉 attempt 关联来凑预算。没有需求证据时不增加可调队列框架、持久消息队列或新依赖。

### 验收与停止条件

- [x] Linux/amd64 隔离环境记录最大输入、空载/饱和/排空峰值，Relay 与 Log 分别低于预先登记预算且无 OOM；少量重复周期排空后无持续保留增长。
- [x] 确定性填满队列与在途项；超预算提交快速拒绝且有计数，已接收项恢复后导出或按原失败语义明确记账，关闭遵守期限。
- [x] 三库最大批次的日志/claim/refs 数量一致，重复送达不新增行，失败不留下部分关联。
- [x] 测量或修复未通过时保留 FAIL 与边界，不据小夹具、自然低流量或单次无 OOM 认定容量准入。
- [x] 若只补验收，不重复已通过的整套吞吐长测；若改队列、worker、分组或持久化策略，重跑原 SQL 容量/P95 门禁与相关 race。生产整体容量仍独立验收。

## 4. N2：供应商核验页展示现有证据

### 实施前缺口与范围（已交付）

[核验页](../../web/src/pages/admin/VendorReportsPage.tsx)的“查看证据”目前仅显示报告 ID、版本、证据引用和更正前序。[既有报告模型](../../app/billing/internal/biz/vendor_reconciliation.go)已经包含完整性、分页、双方水位、来源映射、覆盖证据、unknown/unmapped attempts、purpose、差异归因及更正差额；[Admin HTTP](../../app/admin/internal/server/vendor_report.go)保留原报告 JSON，无需先增加接口或存储。

只扩展现有只读详情，使用已有 UI 组件、`billing.vendor_report.read` 和查询/导出链路：

- 展示固定账期/时区、币种/单位、complete/pages_complete、双方水位、覆盖证据及生效 source mapping。
- 展示未知与未映射数量、purpose 分布、供应商金额/本地估价/差额/未解释差额，关联归因证据及更正前序/差额。
- 保留后端状态和 `next_action`，将已有补证信息翻译为可读说明；缺字段显示未知或未提供，前端不自行授予 matched/request 结论。
- 保持十进制金额字符串，不经浮点重算；明确区分 `"0"`、null/缺失，以及账期核验与逐请求实扣。

当前查询仅返回更正链末端的最新报告；本轮展示 `supersedes` 前序引用与 `correction_delta`，不增加完整历史版本浏览接口。保持只读权限和现有导入/归因边界。

### 验收

- [x] 扩展 [现有页面测试](../../web/src/pages/admin/VendorReportsPage.test.tsx)：pending、attention、matched、缺字段、零金额、高精度金额、差异归因与更正前序引用/差额；已有 JSON 导出保留完整报告。
- [x] 无权限不查询；撤权后清掉已选报告和受保护数据，不能通过旧详情或导出读取缓存。
- [x] 桌面与手机、明暗主题可读；长证据 ID/映射列表不撑破页面，使用隔离 fixture 验证，不发送供应商请求。
- [x] 适用前端测试、lint、类型检查、构建与原资产预算通过；不为只读展示变更重跑无关 SQL 长测。

## 5. 条件启动项

### N3：首个生产来源费用闭环

先选择一个已有授权且能提供真实材料的来源，固定账号/项目、source 生效映射、模型范围、币种、已结束账期、分页完整性和报告摘要，复用既有导入与重放能力。首份 run 必须能够解释双方缺行、内部调用、unknown 与差额；重复导入不重复生效，不修改用户账本。

制定本计划时证据为 [DeepSeek 六次手工调用的完整日窗](../runbooks/deepseek-closed-day-2026-10-16.md)，不能代替生产来源绑定和最终水位。[费用 API](../../app/billing/internal/biz/vendor_cost_api.go)当前只接受 OpenAI/Anthropic 的 USD、UTC 整日 account_period 范围；不能套用于 DeepSeek 或捏造 API/CSV 对照。不具备供应商材料的部分继续记 unknown/INSUFFICIENT，付费样本须有单独授权。

**2026-10-23 当前状态：闭窗用量及按调用时间区分忙闲的 CNY 重算相符，N3 尚未通过。** 固定窗口为 **`[2026-10-21 00:00, 2026-10-23 00:00)` Asia/Shanghai**。维护者澄清另一台电脑只是安排定时任务、没有实际执行，因此没有可合并的任务产物，不再等待该来源。补证使用已有生产记录及官方导出。

渠道 11 当前仍为官方地址、启用 healthy、revision 4，启用模型为 `deepseek-flash`。官方页面筛选 `micro-one-api` Key、10/21–10/22，导出的报告为 **203 请求、30,832,706 token、2.0470089600000000 CNY**。线上 201 条消费账本加上 [10 月 21 日两次直连 `internal_test`](../runbooks/deepseek-channel11-two-day-2026-10-21.md)，请求数、未缓存输入、缓存读取和输出合计均精确相符；费用 CSV 合计与 token CSV 的十进制单价延展差额为 0。这里的费用自洽不等于本地采购估价与供应商金额已匹配，页面显示的 `2.04` 也不替代 CSV 精度。原始报告、脱敏归档和复核命令见 [闭窗记录](../runbooks/deepseek-channel11-closed-window-2026-10-23.md)。

正式 owner 闭环仍有三个缺口：owner 中没有带可靠 upstream send 时间的渠道 11 窗口事件，接收时间窗仅查到 `/models` 健康探针；现有 [owner 估价加载](../../app/billing/internal/data/vendor_attempt_reader.go)只承载冻结 USD，尚未承载本次独立 CNY 重算；官方报告没有显式最终水位。16:25 CST 的补证已另找到 201 条请求计时摘要，足以确定本窗调用费率组，但没有补造 owner execution 事件或历史冻结 CNY 估价。CSV 没有逐请求 ID，因此保持 `account_period` 粒度，缺 ID 本身不阻塞该粒度核验。08:17 初次采样时生产 report/run 均为 0。

**维护者明确使用现有 owner，并说明线上采用 OpenRouter 的 DeepSeek 计价、记录价统一按忙时基准。** 已查实该窗口 201 条客户价格快照为每百万未缓存输入 / 缓存读取 / 输出 `0.30 / 0.06 / 1.20 USD`，当前 `ModelPrice` 与 bare-model `UpstreamModelPrice` 相同；固定价目不表示实际调用都在忙时。按请求入口至处理完成及记账上界的完整时间区间独立分类，本地为忙时 95 条 / `1.49683248 CNY`、闲时 106 条 / `0.55012848 CNY`；加上两次外部忙时测试 `0.000048 CNY`，与官方逐日、费率组、三类 token 及总额精确相符，无跨忙闲边界的歧义记录。这是事后原币重算，不修改既有价目或声称已有历史冻结 CNY。按明确授权通过 140 仅发布 import 给既有 root，随后以批量读取及 141 索引修复 owner 导入超时，保留原 RPC 期限和全部校验。**11:17 已通过原 owner 导入**，report ID 为 `2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db`；独立核对 **1 report / 1 run、sequence 1、actor iam-user:1、pending / missing_local / amount unknown** 及成功审计。详见 [完整核验记录](../runbooks/deepseek-channel11-closed-window-2026-10-23.md)。

**N3.2 预览与 N3.5 真实留存已完成；N3.4 已取得独立的按时段 CNY 重算证据。** 下一步使用已取得的生产计时材料梳理 owner 覆盖/重放承载方式，并补官方最终性；不等待未执行的定时任务，也不先要求现有计价增加自动忙闲切换。

| 顺序 | 工作 / owner | 完成条件 |
| --- | --- | --- |
| N3.1 | 关联现有生产与官方材料；运营 / Billing | 另一台电脑任务未执行，无产物依赖。将本窗计时摘要、来源映射生效区间、覆盖与迟到更正证据关联到原始 CSV 哈希；缺失项保留未知。导出时间、CSV 桶 end_time 不充当供应商最终水位。 |
| N3.2 | 整理可导入材料并做只读预览；Billing | **已完成**固定 13 列 cost-only `account_period` 归集和现有 owner 命令的真实快照预览；CNY、Asia/Shanghai、request_id 留空，五桶留空并保留三个已提供 token 类型旁证。实际结果 pending / missing_local / amount unknown，未传合成 attempts 或执行 import。 |
| N3.3 | 核对真实消费与 owner 执行证据的覆盖条件；Relay / Billing | 已一一关联 201 consume、201 计时摘要及 201 ledger，均为 legacy `/v1/responses`、单次成功 attempt；完整时间区间可确定费率组。该材料仍不等于 owner execution inbox 已覆盖。当前 delivery 为 true，但精确来源不在当前 V2 allowlist、发送时间开关未设置；不据当前快照推断历史根因或扩大准入，不用账本/received_at 补写 send 时间。 |
| N3.4 | 核对既有价目与差异口径；Billing | **独立 CNY 时间分组重算已完成，差额为 0。** 线上固定忙时基准价的说明与实际调用时段分开记录。历史 CNY 费率与官方 CSV 单价一致；本地 usage 先按真实时间区间分组，再与官方比较，没有反推分组。正式 owner 仍只加载冻结 USD，后续评审如何承载这份事后原币证据；不修改客户价、不换汇、不伪造历史冻结价，不先要求自动分时计价引擎。 |
| N3.5 | 通过既有 owner 入口留存真实 report/run；运营 / Billing | **已完成**唯一 import 权限发布、原 owner 超时修复、同一真实请求导入及独立落库/审计核对：1 report / 1 run、sequence=1、pending。后续本地补证追加 run，供应商更正使用新 version/supersedes，继续保持用户账本、价格与灰度不变。 |

N3 的完成标准仍是一个能重放、解释内部调用/缺行/unknown/差额、满足完整性与覆盖条件的真实生产核验 run。当前 CSV 只支持账户账期级 `provider_reported` 证据；不授予逐请求实扣或 invoice 结论。现阶段无需新增模型调用来重复证明已有的 203 次合计，也不将 N3 补证变成 N4 契约晋级。

### N4：上游契约现场准入

只处理一个精确来源/模型/协议的已审候选，沿用 [现场关卡](../runbooks/billing-upstream-p3-operations-completion-2026-10-14.md#尚保留的现场关卡)：独立 reviewer/IAM、凭据/账号绑定、candidate/previous 预算、至少 30 分钟/100 完整样本/fresh proof、切换收敛与通知。未签、过期、样本不足或 usage 错误保持拒绝/等待；具备条件后才执行真实 canary、晋级或回滚，不重做一套契约平台。

## 6. 继续保留与交付方式

| 保留项 | 恢复条件 |
| --- | --- |
| executor 七天准入与扩面 | 按 [观察手册](v0.23-executor-observation.md)形成同入口/协议/时段可比样本并确认精确起点；不恢复暂停任务，不删除 legacy |
| O5 缓存优化、排队、多副本/真实 OAuth | 有代表性业务流量、延迟/成本证据或明确部署需求；保留既有暂缓决定 |
| Playground 原参数恢复/历史编辑 | 有具体使用需求后先恢复所选快照到待发送草稿，用户显式发送；不先做分支树和后端会话存储 |
| 对账容差、归档/分区、其他产品扩展 | 有明确业务/数据规模证据；不为清空历史 TODO 而立项 |

N1/N2 已保存提交、测试、证据和剩余边界，生产按 139 迁移 → Log → Relay → 宿主机 `web/dist` 完成上线并保留回滚点。后续其他环境按 [v0.36.3 升级步骤](../releases/release-v0.36.3.md#升级步骤)办理，不因发布重复重启已上线实例。

本轮 N1/N2 实现已提交：前端 `1d945b81`、计时 `64fa1478`、Jaeger 配置 `5dc00da5`。完整原生三库容量 CI 已通过；2026-10-20 先完成生产部署并合入 develop，2026-10-21 按后续授权合入 main 并发布 v0.36.3，tag 为 `946f25a0`。发布未重复部署；本轮未纳入原观察任务未提交文件，也未修改价格、账本、权限或灰度。N3/N4 后续只读核对及所缺现场材料见 [交接记录](../runbooks/post-v0.36.3-followups-2026-10-21.md)。
