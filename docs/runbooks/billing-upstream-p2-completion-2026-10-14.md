# P2：契约发布、协议事故恢复与外部核验工程

日期：2026-10-14。接续 `6071a995` 与 [P2.1](billing-upstream-p2-external-2026-10-13.md)。本次补齐 P2 工程接缝；真实代表性供应商账期、费用证据和生产灰度仍为独立验收，不能以工程测试替代。没有部署、没有调用真实付费 canary、没有启用收费名单或发布生产契约。

验收索引：[P2 工程证据](evidence/billing-upstream-p2-completion-2026-10-14.json)。关联：[计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[契约方案](../design/upstream-contract-evolution-plan-2026-10-13.md)。

## 交付

| 接缝 | 行为 |
|---|---|
| 不可变版本 | channel biz 声明契约 DO/repo port，shared contract 声明有限请求快照；data 保存内容 SHA、版本、binding、probe、result 和 audit。三种数据库 migration 133，channel ownership |
| CAS 发布 | expected binding revision 无损检查，冲突拒绝；candidate → 1–5% gray → active。至少 30 分钟、100 个完整 verified attempt、零失败，以及当前 revision 的五分钟内独立 canary proof 才可晋级；比建议错误增幅门槛更严格 |
| 有限规则 | provider/source/exact model 三层显式 replaces；只允许 rename/default/reject。重命名链/环、多目标写、重叠路径、未知操作、未安装 build/parser 拒绝。null/0 不当缺失，未知普通 JSON 保留；拒绝不 clamp、不剥离重放 |
| 保护边界 | model、鉴权、URL、用户身份、stream、工具、结构化输出、thinking/cache 与输出预算不允许 rename/default；reject 可拒绝不支持的类型/范围。endpoint/API version 仅核对现有 adapter 实际构建结果，不通过配置改变目的地 |
| 两种 mapping | API-key 与 subscription mapping 增加只读 contract_refs；同事务更新匹配精确 upstream ID 或默认 model ID 的引用。实际运行以 source/model/protocol 的权威 binding 为准，不能用普通 mapping 编辑伪造生效版本 |
| 数据面快照 | channel ClaimUpstreamExecution 返回固定快照、blocked protocols 与版本指纹；HTTP JSON/SSE 和 WS 在发送前执行，证据保存 scope/revision/build/绑定与事故 revision、命中 rule ID、改变路径，不保存参数值 |
| 兼容握手 | 有生效绑定的来源必须使用安装的 finite runtime build；旧 relay 或关闭 gate 的 relay/channel 不能借回退绕过隔离。没有生效绑定的来源保留原行为 |
| 受控 canary | ProbeTarget 可固定 exact contract revision，仍先经过 P1 monitor 一次性预算许可；channel 发一次性、并发受限、到期 lease。actual source revision 与上游协议匹配才调用；原生 usage 由 channel 再解析，200/missing 不产生晋级 proof |
| 事故持久化 | binding.status=quarantined 与事故 revision 持久保存；普通 cooldown/TTL 到期不会开放用户流量，只允许探测。老 binding/事故 revision、已换凭据、过期或重放 probe 不能解锁当前事故 |
| 有界恢复与回滚 | 手工 recover/rollback 要求当前事故的 fresh proof；原协议被移除则保持隔离。publish 可具体授权同一 revision 的 automatic_until（最多 24 小时）；成功 canary 才恢复，最多每小时三次；恢复保留此前已批准的 gray/active 百分比，不会把未晋级灰度扩大为全量。不会自动产生新候选、改价格、放宽收费范围 |
| usage 隔离 | 对已配置生效契约的源/model/protocol，原语义隔离 TTL 不再自动解除；普通 verified 或旧 manual resolve 不代替契约 proof。恢复成功同事务解除相同的 usage block |
| 审计与权限 | 独立 channel.upstream_contract.read/create/publish/quarantine，platform-only 范围；IAM owner 与事务再次核对。内容 hash、动作、理由、actor、绑定/事故 revision、证据引用持久记录，既有 IAM write audit 同事务追加；不自动给旧角色新增权力 |
| 供应商报告 API | billing.vendor_report.read/import/attribute 独立权限；导入 expected_report_id 防止审查后换内容，actor 在 owner 重建。服务从持久原生 attempt 核验，不能传入离线 local-attempts；只追加报告与更正，不写钱包/消费账本 |
| 日报覆盖 | 固定、最长 31 天的受控报告读取，选择更正链当前叶节点和最新 run，超限拒绝；输出 pending/attention/unknown 与去重内部 attempt 数。按报告账期结束日展示覆盖，不把月费和重叠范围相加为伪造的逐日金额 |
| 制品 | 本地 buildx、linux/amd64、CGO 构建 vendor-reconcile 工具镜像，嵌入 Go tzdata 并通过容器内六个时区/状态样例，解决 P2.1 无 CGO 交叉编译的缺口；没有在生产机编译 |

运行时仍只选择已安装的静态原生 usage parser；参数契约与 usage 契约/算法版本分开留证，不通过有限配置改变计费单位、包含关系或六桶计量。第一版只允许精确来源/模型，不实现 wildcard；规则有独立依据/fixture 后由具备 publish 权限的操作者审查发布，未验证的新 agent patch 属于 P3。

真实流量结果在原生证据持久捕获后向 channel 报告；失败或漏报不会增加晋级完整样本，原事件中的契约 trace 留在 P1 evidence/outbox，canary 继续提供周期覆盖。这个控制报告不重打供应商、不以用户费用变更重试实现修复。

## 操作入口

| Admin HTTP | Owner RPC/权限 |
|---|---|
| GET /api/admin/upstream-contracts?source_kind=channel&source_id=…&upstream_model_id=…&protocol=… | ListUpstreamContracts / read |
| POST /api/admin/upstream-contracts | CreateUpstreamContract / create |
| POST /api/admin/upstream-contracts/transition | TransitionUpstreamContract / publish 或 quarantine |
| POST /api/admin/vendor-reports | ImportVendorReport / import；提供归因另需 attribute |
| GET /api/admin/vendor-reports?start=…&end=…&limit=20 | ListVendorReports / read；返回报告与 daily_coverage |

这些入口复用已验证 admin session → owner → IAM 链。费用导入 bytes 字段使用 protobuf JSON/base64 格式，定义在 api/billing/v1/vendor_report.proto；契约 typed DTO 定义在 api/channel/v1/upstream_contract.proto。系统 AcquireContractProbe/RecordContractResult 仅 dedicated relay 可调用，不暴露管理 HTTP 包装。权限注册不会自动扩展历史 frozen IAM 角色，运维需要分别启用并授予这些能力。

契约 document 最少包含 scope（channel/subscription、真实 ID、精确模型、上游协议）、provider、adapter_build=`finite-contract-runtime-2026-10-14.1`、parser_revision=`native-shadow-2026-10-13.1`、usage_revision=`native-static-2026-10-13.1`、inbound_protocols、fixture_evidence_id、basis_revision、rule_sets。生成不可变 revision 后，将它加入受控 ProbeTarget 的 contract_revision，并让一次真实有预算的推理探测核对协议/能力/usage。Transition action 支持 publish/promote/quarantine/recover/rollback，必须给出 expected_revision 和 reason。automatic_until 只用于精确已批准 revision，不构成全新规则授权。

CSV/manifest 原币、账期、报告摘要、月费/余额/unknown 口径继续沿用 [P2.1 规范](billing-upstream-p2-external-2026-10-13.md)。报告 API 的 expected_report_id 是 VendorReportID 的内容身份；不能用一个任意证据字符串自称已经鉴真发票。

## 上线顺序与回退

1. billing migration 132、channel migration 133；更新 billing/channel/identity/admin 的消费者和权限目录，再更新 relay/monitor 的生产者。源码使用 make api/make all 生成，没有手改 pb/Wire。
2. `UPSTREAM_CONTRACTS_V1_ENABLED=false` 是三种 Compose 的默认 gate；先核验 dedicated system capability、权限与消费者版本。增加带签名的 ParameterContract/实际时间证据要求新的 billing 消费者，旧消费者会拒绝新 digest。
3. 试点 source+exact model+protocol 完成独立 fixture 与固定预算 canary，先发布小范围灰度；运行计数/时间/失败门槛满足后重新获取当前 revision 的 fresh proof，再 promote。
4. source 或凭据变化使旧 proof 失效。quarantine 后 TTL 仅准入 canary；不能关闭 gate 再用旧 relay 放开已绑定来源。关闭时停止新准入，保留新消费者、表与 P1 outbox 排空在途证据。
5. 回滚目标必须仍被实际供应商支持；没有 fresh proof 就保持隔离与已授权备用路由。所有账本、用户价、供应商旧快照和原报告保留。

当前本地镜像：`micro-one-api/vendor-reconcile:p2-20261014`，linux/amd64。它是可评审工具制品，不等于更新线上服务。新来源收费/parser/cache charge、模型公开和供应商发票确认仍各有独立门禁。

## 验收边界

本地 fixture 验证有限规则的 null/0/未知字段、冲突/预算/不安全操作、发送前拒绝且调用数不增加、固定版本留证、CAS、三库迁移、1–5% 灰度/30 分钟/100 完整样本、200 但 usage 错误、旧结果与 TTL、原协议失效时回滚拒绝、重启保持隔离、fresh proof 恢复、两类 namespace/权限与更正链。完整单元、竞态、架构、生成、制品检查以验收索引为准。

**仍待真实资料/现场窗口**：token 收费、固定月费/额度、仅余额或完全 unknown 的代表性账号/完整账期与凭据；真实差异、舍入/优惠口径、专用账号水位；生产灰度至少 30 分钟/100 个完整目标协议 attempt，以及实际 5 分钟发布/回滚收敛。后续已收到 DeepSeek 官方测试 key 并完成 [受控实测](deepseek-official-pilot-2026-10-14.md)，并已完成[扩大余额核验与真实费用导出核对](deepseek-balance-and-channel1-2026-10-14.md)：六次总额精确相符，余额区间在显示精度内相符；完整日账期尚未结束，其他代表性来源与生产窗口仍缺；未伪造这些验收，也未宣称 P2 整体出阶段。工程范围没有用 P3 的费用 API/候选修复代替这道门。
