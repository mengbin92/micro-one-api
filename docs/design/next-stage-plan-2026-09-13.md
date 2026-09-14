# 下一阶段工作建议：分组 v2 启用与稳定性收口

> 日期：2026-09-13
> 状态：P0 已采纳并于 2026-09-14 完成；P1 / P2 保留规划。当前执行状态以 [v0.30 路线图](./v0.30-roadmap.md) 为准。
> 审阅基线：本地 `develop@9402bb5f`，工作区审阅开始时干净；本地 `v0.29.0` 指向 `d694bb6a`。
> 初稿核查范围：仓库文档、代码、测试与 CI 配置，以及 MySQL / Lite Compose 的实际配置渲染。以下“当前事实”保留 09-13 规划时的问题快照；后续生产只读复核、配置修复与真实服务验收已完成，证据见路线图，不应再将初稿问题视为当前缺口。远程 CI 和 registry 制品未核验。

下一阶段的主目标是让分组 v2 形成可重复部署、可验收、可解释的使用闭环。A–F 已交付核心实现，优先把已有能力稳妥启用，再根据实际使用反馈扩展产品范围。

## 1. 当前事实与规划依据

| 事项 | 核查结果 | 对排期的影响 |
| --- | --- | --- |
| 分组重设计 | [v0.29.0 发布说明](../releases/release-v0.29.0.md)已列出 A–F；代码已有 fixed / ordered 路由、冻结快照、订阅合约、三种结算模式、用户倍率与关系覆盖 | A–F 不重复立项；下一步验收跨服务接线和运行行为 |
| 执行入口 | [TODO](../TODO.md)与[文档索引](../README.md)仍指向 v0.27；[v0.27 路线图](./v0.27-roadmap.md)还标注“当前唯一执行入口”和等待发布 | 先归档完成项，建立与 v0.29 代码一致的状态表 |
| 生产证据 | [runbook](../runbooks/routing-groups-runbook.md)记载 09-12 已部署代码与迁移、B 双写已启用、C–F 关闭；阶段完成记录仍写未部署，发布说明仍把生产验证列为待确认 | 无法仅凭这些文档断言当前线上已启用或尚未部署；先做只读核对 |
| 部署开关 | [MySQL Compose](../../deployments/docker-compose/docker-compose.yml)与[Lite Compose](../../deployments/docker-compose/docker-compose.lite.yml)渲染后，五个相关服务都没有 `ROUTING_*`、`BILLING_REQUEST_SNAPSHOT_V2`、`SUBSCRIPTION_ENTITLEMENTS_V2` 环境变量；模板没有通过 `env_file` 整体注入 | runbook 的 `.env` 启用路径尚未在仓库模板中接通，是首批工程任务 |
| 测试覆盖 | [ordered 单测](../../internal/biz/routing_ordered_test.go)、[账务合同测试](../../app/billing/internal/data/subscription_entitlements_test.go)等覆盖很多边界；[分组 Playwright](../../web/e2e/routing-groups.spec.ts)通过 `page.route` mock API；现有 Compose E2E 未发现 v2 完整业务流程 | 补真实服务与存储下的纵向验收，沿用现有测试基础 |
| 运维观察 | [outbox](../../platform/routingoutbox/outbox.go)有持久化、重试和去重；未发现其积压/最老消息年龄/投递失败专用指标与告警；[订阅 outbox](../../domain/subscription/data/contract.go)传入的错误报告回调为 `nil` | 分组灰度前补可观测性，验证故障可被发现和恢复 |
| 管理台性能 | [汇总 handler](../../app/admin/internal/server/http.go)仍串行调用约 20 次 RPC；近期修复增加了 60 秒 context 并调整日志等级，部分失败数据仍退化为零或空集 | 对延迟、部分失败展示和超时链路单独收口 |
| 存量库升级 | runbook §2.2 记录旧 `schema_migrations.applied_at` 无默认值；[runner](../../platform/database/migrate/runner.go)仍在 DDL 后仅插入 `version` | 补 DDL 前的迁移元数据预检，降低再次出现半完成迁移的概率 |

文档中的两类旧任务应直接归入历史：Lite 首次聊天 smoke 已在 v0.27 收口；buf 与 Kratos v3 已完成迁移，`go.mod` 已使用 Kratos v3。旧方案正文可以保留，但需明确其适用版本。

## 2. 执行顺序与交付物

以下工作量是单人有效工程时间的粗估，不包含生产观察窗口；实际排期随复现结果调整。

| 顺序 | 优先级 | 任务 | 交付物与退出条件 | 粗估 |
| --- | --- | --- | --- | --- |
| 1 | P0 | 对齐版本与运行基线 | 新阶段唯一入口、归档 v0.27、同步 TODO / 索引；生产镜像/迁移/开关/回填的脱敏状态记录 | 0.5–1 人日 |
| 2 | P0 | 接通部署与启用配置 | MySQL / Lite / PostgreSQL Compose 与示例环境变量一致；增加配置渲染断言和启用前检查 | 1–2 人日 |
| 3 | P0 | 建立分组 v2 真实链路验收 | 独立环境中完成创建组到扣费、撤权、回退的流程；纳入共享 E2E workflow | 3–5 人日 |
| 4 | P1，灰度前置 | 补齐路由与 outbox 观察 | 指标、告警、看板和故障演练记录；积压、投递失败、能力不匹配可定位 | 1–2 人日 |
| 5 | P1 | 管理台汇总性能与降级 | 受限并发、明确预算、部分数据不可用标识；有修复前后同负载延迟对照 | 1–2 人日 |
| 6 | P1 | 升级迁移预检 | 兼容旧迁移记录表的诊断与升级测试；不兼容时在执行业务 DDL 前停止 | 1–2 人日 |
| 7 | P2 | 完善分组操作路径 | 新建组后的待办引导、启用条件诊断、授权/价格/账单解释；在验收基础上按反馈选取 | 1–2 人日 |

建议先把 1–4 做成第一批交付，5–6 分别提交独立修复。第 7 项根据真实试用中的阻碍选择范围。正式版本号由最终交付内容决定：部署与可靠性修复可以独立 PATCH，新的产品交互可以进入后续 MINOR。

## 3. 第一批：让分组 v2 可以可靠启用

### 3.1 基线与文档收口

建立一张运行状态表，分别记录代码已合入、tag 已发布、镜像已部署、迁移已应用、数据已回填、能力已启用、业务已验收。每条附核查时间和证据位置。

生产只读核对包括：各服务镜像摘要和启动时间、逐 ownership 的迁移状态、B 成员回填与 identity 路由事实回填、相关开关、各服务能力响应、现有 fixed / ordered Key 与 selected_groups 合同数量。记录仅保存脱敏汇总。

随后归档 v0.27 当前入口，把已完成的 charge 回滚演练、范围决策和 Lite smoke 从“下一步”移入完成记录；新路线图、TODO 和文档索引同步。阶段 A–F 的历史记录补一条指向最新运行状态记录的链接。

### 3.2 配置接线与启用前检查

为以下变量在所属服务增加明确透传，并同步示例文件与 runbook：

- channel：`CHANNEL_ROUTING_GROUP_DUAL_WRITE`。
- billing：`BILLING_REQUEST_SNAPSHOT_V2`。
- identity：`IDENTITY_ROUTING_V2`、`IDENTITY_DEFAULT_ROUTING_GROUP`。
- relay：`RELAY_ROUTING_CONTEXT_V2`、`RELAY_ROUTING_ORDERED`。
- admin：`ADMIN_ROUTING_FIXED_KEYS`、`ADMIN_ROUTING_ORDERED_KEYS`。
- admin / billing / relay：`SUBSCRIPTION_ENTITLEMENTS_V2`。

保持当前模板默认行为；双写只能在其 schema 与回填前置满足后启用。Kubernetes 模板若继续作为受支持交付面，也应提供等价配置或明确当前能力边界。

扩展[部署检查脚本](../../scripts/check-deployment-docs.sh)：使用测试值渲染每种 Compose，逐服务断言变量存在、值正确、没有注入无关服务；再验证未设置时的默认行为。只检查 YAML 能被解析不足以覆盖此问题。

启用前检查复用现有能力 RPC，直接 SELECT 已有迁移记录，至少输出缺失迁移/回填、能力版本不足、三服务合同开关不一致、双写未开启、回退受现有 Key/合同限制等诊断。现有 migration status 会初始化元数据，不用于只读路径；identity 回填的默认演练含事务写入与锁，作为单独步骤标明。

### 3.3 真实端到端验收

建立受控 MySQL Compose 场景，使用本地 mock 上游和测试数据，通过真实 admin / identity / channel / relay / billing / Redis 完成流程。关键场景另跑 SQLite，数据边界继续使用现有三方言测试。

| 场景 | 必须验证的行为 |
| --- | --- |
| 默认兼容 | 新版本所有运行时功能关闭时，原 inherit Key、旧合同、聊天与账务继续通过 |
| 新组到首次调用 | 新建 disabled 组 → 加资源 → 配置价格 → 启用 → 授权用户 → fixed Key → models → 请求 → 账单；实际组与快照一致 |
| 冻结计价 | 预扣后改组倍率或用户覆盖，在途请求按旧快照；下一请求用新版本；同请求重试只有一次有效扣费 |
| 三种结算模式 | wallet_only 仅钱包；subscription_only 额度不足拒绝且钱包不扣；subscription_first 按覆盖与剩余额度拆分，账务守恒 |
| 合同生命周期 | 购买、续期、合同变更、退款/撤销、到期后资格和覆盖按来源变化；保留其他独立授权 |
| ordered | 无资源组允许递进；无访问权、结算失败和权威 RPC 异常按设计终止；候选耗尽不落入全局回退 |
| 会话与撤权 | fixed / ordered 的 Responses、SSE、WS 绑定与恢复维持原组；撤权和停组后按契约拒绝，不把会话转移到其他组 |
| 事件与故障 | Redis 断开时 outbox 保留，恢复后投递；重复和乱序事件不恢复旧权限；多 relay 副本均失效缓存 |
| 混部与回退 | 缺能力时先拒绝、无上游调用与错误扣费；关闭创建开关后仍能服务既有对象；在途 reservation 排空后再回退兼容版本 |

第一条自动化用例从“新建组到 fixed 请求及账单”开始，随后添加结算与撤权，最后增加 ordered / 会话和故障矩阵。接入[共享 E2E workflow](../../.github/workflows/e2e.yml)，让 nightly 与 release 使用同一入口。

已有单元测试继续承担组合边界；跨服务测试验证接线和资金/权限不变式，避免重复穷举同一实现。

### 3.4 观察与分阶段启用

新增或补齐：outbox pending、最老未投递事件年龄、投递失败计数、最后成功时间；权威 RPC 时延和错误、v2 准入拒绝原因、快照校验失败。指标按 owner / operation / reason 等有限枚举聚合，用户、Key 和请求 ID 留在结构化日志中。

订阅 outbox 补错误报告；沿用已有路由指标与账务看板。一次 Redis 断连演练应同时证明告警出现、恢复后积压消退、授权保持正确。

生产按 B 前置核对 → C → D → E → F 分窗口推进，每阶段留小额请求、账单与回退证据。先确保 relay 具备服务 ordered 的能力，再开放普通用户创建入口，避免产生暂时不可用的 Key。

建议的停止条件：越权请求成功、错误组/错误模式扣费、重复结算、快照不一致任一出现即停止扩面；延迟与错误率采用启用前可比基线确定门槛。每阶段都需要业务样本和观察证据，不能只用“运行了若干天”替代验收。

## 4. 第二批：控制台和升级可靠性

### 4.1 汇总接口

当前 `handleAdminSummary` 仍串行聚合多个服务，部分错误被替换为空集合和零值；单纯延长 context 或降低日志等级无法证明延迟已改善。`context.WithTimeout(parent, 60s)` 也不能延长更早到期的父 context，需要核对入口、客户端、RPC 的完整预算。

实施时先按仓库 diagnosing-bugs 技能收集复现和调用耗时，再做以下最小改动：将聚合编排放入应用/业务层，HTTP handler 保留输入输出适配；独立查询采用有上限的并发，ID enrichment 保留依赖顺序；统一总预算和子查询预算，客户端取消能向下传播；将非关键区块的不可用状态返回前端，避免显示为真实零值。

验收使用同数据量与同并发的前后对照，记录总耗时、各 RPC 耗时、超时率和数据完整性。注入单个慢依赖后，其余区块应可用且退化可见；具体 P95 目标在基线取得后确定。

### 4.2 存量库升级

将 runbook 已记录的 `schema_migrations.applied_at` 兼容性问题纳入迁移前检查：检测已有表结构是否支持 runner 的记录写法，不兼容时在首个业务 DDL 前返回可操作的错误。明确迁移 status 路径是否会初始化元数据，避免把有写入的动作称为只读。

使用隔离 MySQL 构造旧版 BIGINT 无默认值迁移表，测试预检阻断、修复后升级、重复执行，以及 DDL 已提交但版本未记账的恢复诊断。继续跑 SQLite / PostgreSQL 生命周期测试。恢复工具不得仅因同名表存在就自动补录迁移完成。

## 5. 后续产品方向与进入条件

分组 v2 验收后，优先改善日常配置成本：为新组显示“尚缺资源/尚未启用/尚未授权”等实际状态与下一步入口；展示有效价格、来源、版本和订阅覆盖；在停组、撤权前给出受影响 Key 的汇总。创建、授权、账务仍遵守 channel / identity / billing 的所有权，admin 编排展示。

| 后续方向 | 当前建议与进入条件 |
| --- | --- |
| Executor 再观察 | 保留为独立工作流。[09-02 成对实验](./v0.23-executor-observation.md)显示短固定输出下 P95 无基础回归，尚缺真实长流证据；先补长流、工具调用、相同模型/来源/payload 的对照，再建立新的 7 天窗口 |
| Canonical charge 扩面 | 保留 K3 范围的既有书面决策。新来源按收益和证据单独立项，复用修订后的 SQL 与独立观察窗口；与分组结算模式切换分开 |
| 多有效订阅叠加 | 等用户确有同时购买多个套餐的需求，再设计扣减顺序、续费、退款和并发约束 |
| 单二进制 / 更轻部署 | 先收集现有 Lite Quickstart 的实际失败与资源占用，再决定是否改变九服务部署形态 |
| legacy handler 删除 / Relay 目录迁移 | 达到 executor 默认开启、跨版本稳定与无回滚依赖后评估；按已有退出条件执行 |
| 历史自动冲正 | 证据达到 verified，并具备显式审批、幂等 reversal 设计和 dry-run 后独立执行 |

## 6. 第一轮完成标准

- 当前路线图、TODO、文档索引和最新运行基线一致。
- 仓库提供的部署模板能够显式启用各阶段，默认兼容路径仍通过。
- 真实 fixed / 结算 / 撤权 / ordered 核心流程进入可重复自动化测试。
- outbox 与能力失败具有可用的观察、告警和恢复证据。
- 生产启用范围有明确记录；运行时开关、部署和回填分别留证。
- 代码变更通过 `make verify`；额外执行相关 E2E、三方言升级测试与部署文档检查。`make verify` 本身不包含完整 E2E。
- 发布时按仓库要求同步 release note、CHANGELOG、README，完成 develop → main → tag 工作流。

执行结果（2026-09-14）：P0 三项已完成，文档统一到 v0.30 入口；三种 Compose 与 Kubernetes 接线、只读预检、MySQL / SQLite 全链路验收已交付并通过验证。生产复核确认 B–F 已启用，未修改生产。完整命令、缺陷修复与证据见 [v0.30 路线图](./v0.30-roadmap.md)。本节“第一轮”还包含 P1 的指标 / 告警和后续生产观察，P0 完成不代表这些规划项已完成，也不代表发布了新版本。
