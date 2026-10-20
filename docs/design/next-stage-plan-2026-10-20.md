# 下一阶段计划：计时内存预算与供应商证据可读性

> **当前执行入口**。制定：2026-10-20（Asia/Shanghai），规划基线 `develop@10054011`，已合入验证后的 v0.36.2。接替 [10 月 18 日计划](next-stage-plan-2026-10-18.md)。N1/N2 实现及最大输入/前端隔离验收已交付，完整原生三库 SQL 容量 CI 已通过；见 [实施与证据](../runbooks/p1-memory-vendor-evidence-2026-10-20.md)。不预定新版本号，不因立项部署或扩面。

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
| N3 / 条件 P1 | 首个生产来源费用闭环；运营 / Billing owner | 已授权来源、完整已结束账期与真实报告 | 一个可重放的核验 run；按实际证据给结论 | 材料齐备后评估 |
| N4 / 条件项 | 一个上游契约候选的现场准入；运营 / Channel owner | 已审候选、独立签名权限、凭据与预算 | 一次候选到晋级或隔离/回滚的完整记录 | 不承诺采样等待时长 |

**执行建议：N1 与 N2 可并行，随后汇总验收与交付；N3/N4 单独判断条件。** 两项工程按一轮约 3–5 个工程日估算，不含现场等待；N1 若暴露需修改保留策略的问题，先记录失败与影响，再调整排期。

## 3. N1：最大合法摘要内存与恢复预算

### 当前缺口

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

### 当前缺口与范围

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

当前证据仍是 [DeepSeek 六次手工调用的完整日窗](../runbooks/deepseek-closed-day-2026-10-16.md)，不能代替生产来源绑定和最终水位。[费用 API](../../app/billing/internal/biz/vendor_cost_api.go)当前只接受 OpenAI/Anthropic 的 USD、UTC 整日 account_period 范围；不能套用于 DeepSeek 或捏造 API/CSV 对照。不具备供应商材料的部分继续记 unknown/INSUFFICIENT，不额外制造付费样本来填计划。

### N4：上游契约现场准入

只处理一个精确来源/模型/协议的已审候选，沿用 [现场关卡](../runbooks/billing-upstream-p3-operations-completion-2026-10-14.md#尚保留的现场关卡)：独立 reviewer/IAM、凭据/账号绑定、candidate/previous 预算、至少 30 分钟/100 完整样本/fresh proof、切换收敛与通知。未签、过期、样本不足或 usage 错误保持拒绝/等待；具备条件后才执行真实 canary、晋级或回滚，不重做一套契约平台。

## 6. 继续保留与交付方式

| 保留项 | 恢复条件 |
| --- | --- |
| executor 七天准入与扩面 | 按 [观察手册](v0.23-executor-observation.md)形成同入口/协议/时段可比样本并确认精确起点；不恢复暂停任务，不删除 legacy |
| O5 缓存优化、排队、多副本/真实 OAuth | 有代表性业务流量、延迟/成本证据或明确部署需求；保留既有暂缓决定 |
| Playground 原参数恢复/历史编辑 | 有具体使用需求后先恢复所选快照到待发送草稿，用户显式发送；不先做分支树和后端会话存储 |
| 对账容差、归档/分区、其他产品扩展 | 有明确业务/数据规模证据；不为清空历史 TODO 而立项 |

N1/N2 各自保存提交、测试、证据和剩余边界，验收通过再决定合并交付范围及版本号。若安排 v0.36.2 上线，先检查 N1 预算与独立生产验收结论，沿用 [升级顺序](../releases/release-v0.36.2.md#升级步骤)：139 迁移 → Log → Relay，保留兼容镜像回滚；生产变更另行承接。本阶段 Web 变更部署时须独立发布宿主机 `web/dist`。

本轮 N1/N2 实现已提交并推送独立验证分支 `codex/p1-bounded-timing-and-evidence`：前端 `1d945b81`、计时 `64fa1478`、Jaeger 配置 `5dc00da5`。完整原生三库容量 CI 已通过；未合并 develop/main、发版或部署，未修改原观察证据、价格、账本、权限或灰度。
