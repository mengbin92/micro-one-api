# 文档索引

本目录按职能分类组织，方便快速定位。新增文档时请放入对应子目录。

```
docs/
├── README.md            ← 本索引
├── TODO.md              ← 当前计划入口与历史完成记录
├── deployment.md        ← 部署运维文档（最高频查阅，保留在根目录）
├── community-promotion-blog.md  ← 社区宣传博客
├── logo-design.md       ← Logo 设计说明
├── assets/              ← 图片资源（logo、社区配图和界面截图）
├── releases/            ← 版本发布公告
├── runbooks/            ← 运维操作手册（SOP）
├── design/              ← 架构设计、技术方案、复盘与路线图
└── migration/           ← Kratos 大仓 / grpc-gateway / log / buf / v3 升级方案
```

## 快速入口

| 我想... | 看这里 |
|---------|--------|
| 个人单机，创建首个渠道和 Token | [quickstart-lite.md](./quickstart-lite.md) |
| 部署 / 升级服务 | [deployment.md](./deployment.md) |
| 查看下一阶段执行路线与条件保留项 | [design/next-stage-plan-2026-10-07.md](./design/next-stage-plan-2026-10-07.md) |
| 复核 executor 有限灰度与第二批条件结论 | [观察手册](./design/v0.23-executor-observation.md) · [第二批记录](./runbooks/executor-second-batch-acceptance-2026-10-07.md) |
| 查看当前待办和历史完成记录 | [TODO.md](./TODO.md) |
| 查看产品界面预览 | [根 README 界面预览](../README.md#界面预览) |
| 查看某版本发布内容 | [releases/](./releases/) |
| 排查订阅系统生产故障 | [runbooks/subscription-production-runbook.md](./runbooks/subscription-production-runbook.md) |
| charge 后监控告警与 SQL 口径 | [runbooks/cache-creation-charge-monitoring.md](./runbooks/cache-creation-charge-monitoring.md) |
| 发布后强制失败验证（§9.2） | [runbooks/post-release-forced-failure-verification.md](./runbooks/post-release-forced-failure-verification.md) |
| 理解整体架构 | [design/ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md) |
| 了解大模型如何计费 | [design/llm-billing-explained.md](./design/llm-billing-explained.md) |
| 理解路由分组、价格倍率与订阅额度策略 | [design/group-concepts-and-implementation.md](./design/group-concepts-and-implementation.md) |
| 清点分组数据、检查模型与来源授权 | [design/group-audit-and-routing-authorization.md](./design/group-audit-and-routing-authorization.md) |
| 了解订阅系统历史交付 | [design/subscription-follow-up-roadmap.md](./design/subscription-follow-up-roadmap.md) |
| 查看 IAM 权限管理设计与正式交接 | [实施记录](./design/rbac-permission-management-implementation-plan.md) · [D1 生产交接](./design/rbac/d-iam-production-deployment.md) |
| 查看 Kratos 大仓 / buf / v3 升级迁移方案 | [migration/](./migration/) |

> **路线图入口治理**：「当前执行路线图」只有一个事实源——本表明确指向且头部标注「当前执行入口」的计划。未定发布版本时使用 `design/next-stage-plan-YYYY-MM-DD.md`，已定版本时使用 `design/vX.Y-roadmap.md`。新阶段立项时：新建计划 → 旧路线图标为「已归档」并指回新入口 → 同步本表、`design/` 表格与 [TODO.md](./TODO.md) 顶部。三处不一致即视为文档漂移。

最新发布：[v0.34.6](./releases/release-v0.34.6.md)（2026-10-07），恢复后台对账并强化完整 IAM 持续门禁。v0.34.0 已交付 IAM 正式生产交接，v0.34.1–v0.34.4 补齐本人用量/订阅、字段/公开价格、权限详情与安全修复。

**当前第二批状态（2026-10-07）**：第一批已上线发版，v0.34.6 CI 的固定契约/十三项快速门禁已实际成功，自然小时对账恢复已复核。E1 与 E2 条件评估完成，结论 **INSUFFICIENT**，正式七天起点和可比自然 cohort 仍不成立；只读取证工具及隔离长流方案已交付，线上同步运维资料，保留有限灰度与 legacy。见 [第二批记录](./runbooks/executor-second-batch-acceptance-2026-10-07.md)，运行事实仍以 [观察手册](./design/v0.23-executor-observation.md) 为唯一来源。下方规划/第一批记录保留当轮历史边界。

本轮核对（2026-10-07）：`develop@a37b39bf` 与 main 的 CI/安全流水线、v0.34.4 Release 已通过，开放 Code Scanning 告警为零。生产交接现状来自 [2026-10-05 记录](./design/rbac/d-iam-production-deployment.md)，本轮未重验生产。新计划按 S1 只读运行基线 → S2 关键链路覆盖 → S3 IAM 专项持续门禁推进；E1 核对 10 月 1 日重新开启的两枚 Token executor 灰度及有效七天起点，运行事实仍由 [观察手册](./design/v0.23-executor-observation.md) 管理。

历史可靠性阶段：R1–R4、O1–O4、O5 正确性及 Q1–Q3 既定实施/隔离验收已完成。O5 代表性流量复采与缓存优化延续 2026-09-28 的暂缓决定；真实 OAuth/多副本、支付平台重发、供应商侧取消及 D2–D7 按 [新计划](./design/next-stage-plan-2026-10-07.md) 条件恢复，原证据保留在 [历史阶段清单](./design/next-stage-todo-2026-09-26.md)。

第一批实施（2026-10-07）：S1 只读基线、S2 关键链路与 S3 门禁接线/本地验收完成，见 [验收记录](./runbooks/iam-first-batch-acceptance-2026-10-07.md)。生产定时对账断点已复现并本地修复，尚待发布部署；executor 对照条件未满足。上段未重验生产仅指规划整理时点。

第一批复审：[五项问题/差异已修复](./design/iam-first-batch-review-2026-10-07.md)，当前十项完整真实 IAM 浏览器及十二项快速门禁通过；验收文件与 SQL 证据范围已纠正，生产状态未改动。

上线更新（2026-10-07）：已更新 Billing 为 v0.34.5，启动轮 #365 completed/0 差异；[生产证据](./runbooks/evidence/patch-v0.34.5-production-2026-10-07.json)保留备份、回滚及 IAM/环境/实例一致性。此前“待部署/生产未改动”为实施和复审时点的历史记录。

---

## 目录详解

### releases/ — 版本发布公告

每个 tag 对应一份 `release-vX.Y.Z.md`，CI 在打 tag 时会校验并自动发布到 GitHub Release。

- [v0.2.1](./releases/release-v0.2.1.md) · [v0.2.2](./releases/release-v0.2.2.md) · [v0.2.3](./releases/release-v0.2.3.md) · [v0.2.4](./releases/release-v0.2.4.md) · [v0.2.5](./releases/release-v0.2.5.md) · [v0.2.6](./releases/release-v0.2.6.md) · [v0.2.7](./releases/release-v0.2.7.md) · [v0.2.8](./releases/release-v0.2.8.md) · [v0.2.9](./releases/release-v0.2.9.md)
- [v0.3.0](./releases/release-v0.3.0.md) · [v0.3.1](./releases/release-v0.3.1.md)
- [v0.4.0](./releases/release-v0.4.0.md) · [v0.4.0 / v0.5.0 联合公告](./releases/release-v0.4.0-v0.5.0.md) · [v0.5.0](./releases/release-v0.5.0.md)
- [v0.6.0](./releases/release-v0.6.0.md) · [v0.6.1](./releases/release-v0.6.1.md)
- [v0.7.0](./releases/release-v0.7.0.md) · [v0.7.1](./releases/release-v0.7.1.md) · [v0.7.2](./releases/release-v0.7.2.md) · [v0.8.0](./releases/release-v0.8.0.md)
- [v0.9.0](./releases/release-v0.9.0.md) · [v0.9.1](./releases/release-v0.9.1.md) · [v0.9.2](./releases/release-v0.9.2.md) · [v0.9.3](./releases/release-v0.9.3.md)
- [v0.10.0](./releases/release-v0.10.0.md) · [v0.10.1](./releases/release-v0.10.1.md) · [v0.10.2](./releases/release-v0.10.2.md)
- [v0.11.0](./releases/release-v0.11.0.md) · [v0.12.0](./releases/release-v0.12.0.md) · [v0.13.0](./releases/release-v0.13.0.md) · [v0.13.1](./releases/release-v0.13.1.md) · [v0.13.2](./releases/release-v0.13.2.md) · [v0.13.3](./releases/release-v0.13.3.md)
- [v0.14.0](./releases/release-v0.14.0.md) · [v0.15.0](./releases/release-v0.15.0.md) · [v0.15.1](./releases/release-v0.15.1.md) · [v0.15.2](./releases/release-v0.15.2.md) · [v0.15.3](./releases/release-v0.15.3.md) · [v0.16.0](./releases/release-v0.16.0.md)
- [v0.17.0](./releases/release-v0.17.0.md) · [v0.17.1](./releases/release-v0.17.1.md)
- [v0.18.0](./releases/release-v0.18.0.md) · [v0.18.1](./releases/release-v0.18.1.md) · [v0.18.2](./releases/release-v0.18.2.md) · [v0.18.3](./releases/release-v0.18.3.md) · [v0.18.4](./releases/release-v0.18.4.md)
- [v0.19.0](./releases/release-v0.19.0.md) · [v0.19.1](./releases/release-v0.19.1.md)
- [v0.20.0](./releases/release-v0.20.0.md) · [v0.20.1](./releases/release-v0.20.1.md) · [v0.20.2](./releases/release-v0.20.2.md) · [v0.20.3](./releases/release-v0.20.3.md) · [v0.20.4](./releases/release-v0.20.4.md) · [v0.20.5](./releases/release-v0.20.5.md)
- [v0.21.0](./releases/release-v0.21.0.md) · [v0.22.0](./releases/release-v0.22.0.md)
- [v0.23.0](./releases/release-v0.23.0.md) · [v0.23.1](./releases/release-v0.23.1.md) · [v0.23.2](./releases/release-v0.23.2.md) · [v0.23.3](./releases/release-v0.23.3.md)
- [v0.24.0](./releases/release-v0.24.0.md) · [v0.25.0](./releases/release-v0.25.0.md) · [v0.26.0](./releases/release-v0.26.0.md) · [v0.26.1](./releases/release-v0.26.1.md) · [v0.26.2](./releases/release-v0.26.2.md) · [v0.26.3](./releases/release-v0.26.3.md) · [v0.26.4](./releases/release-v0.26.4.md) · [v0.26.5](./releases/release-v0.26.5.md) · [v0.26.6](./releases/release-v0.26.6.md)
- [v0.27.0](./releases/release-v0.27.0.md) · [v0.28.0](./releases/release-v0.28.0.md) · [v0.28.1](./releases/release-v0.28.1.md) · [v0.29.0](./releases/release-v0.29.0.md)
- [v0.30.0](./releases/release-v0.30.0.md) · [v0.30.1](./releases/release-v0.30.1.md) · [v0.31.0](./releases/release-v0.31.0.md) · [v0.31.1](./releases/release-v0.31.1.md)
- [v0.32.0](./releases/release-v0.32.0.md) · [v0.32.1](./releases/release-v0.32.1.md) · [v0.32.2](./releases/release-v0.32.2.md)
- [v0.33.0](./releases/release-v0.33.0.md) · [v0.33.1](./releases/release-v0.33.1.md) · [v0.33.2](./releases/release-v0.33.2.md) · [v0.33.3](./releases/release-v0.33.3.md) · [v0.33.4](./releases/release-v0.33.4.md) · [v0.33.5](./releases/release-v0.33.5.md) · [v0.33.6](./releases/release-v0.33.6.md)
- [v0.34.0](./releases/release-v0.34.0.md) · [v0.34.1](./releases/release-v0.34.1.md) · [v0.34.2](./releases/release-v0.34.2.md) · [v0.34.3](./releases/release-v0.34.3.md) · [v0.34.4](./releases/release-v0.34.4.md) · [v0.34.5 中止候选](./releases/release-v0.34.5.md) · [v0.34.6](./releases/release-v0.34.6.md)（最新）

### runbooks/ — 运维操作手册

面向生产操作的标准流程（SOP），涵盖订阅系统的发布、配置、排障与压测。

| 文档 | 用途 |
|------|------|
| [subscription-production-runbook.md](./runbooks/subscription-production-runbook.md) | 订阅系统生产发布、回滚与排障总入口 |
| [routing-groups-runbook.md](./runbooks/routing-groups-runbook.md) | 路由分组 / 订阅合约 / 结算模式的迁移核对、开关启用、验证与回退 |
| [subscription-account-setup-guide.md](./runbooks/subscription-account-setup-guide.md) | 上游订阅号配置与导入实操 |
| [subscription-account-ops-runbook.md](./runbooks/subscription-account-ops-runbook.md) | 订阅账号治理（阶段 1） |
| [subscription-account-quota-governance-runbook.md](./runbooks/subscription-account-quota-governance-runbook.md) | 订阅账号额度治理 |
| [subscription-oauth-binding-runbook.md](./runbooks/subscription-oauth-binding-runbook.md) | 订阅账号 OAuth 绑定 |
| [subscription-plan-runbook.md](./runbooks/subscription-plan-runbook.md) | 订阅套餐配置与购买发放 |
| [subscription-redis-multi-replica-runbook.md](./runbooks/subscription-redis-multi-replica-runbook.md) | 订阅 Redis 多副本部署 |
| [relay-stress-runbook.md](./runbooks/relay-stress-runbook.md) | Relay 稳定性压测 |
| [cache-creation-charge-monitoring.md](./runbooks/cache-creation-charge-monitoring.md) | cache-creation charge 后监控告警与文档化 SQL 查询 |
| [post-release-forced-failure-verification.md](./runbooks/post-release-forced-failure-verification.md) | 发布后强制失败验证（v0.11.0 §9.2 补完） |

### design/ — 架构设计与技术方案

架构蓝图、专题设计、阶段复盘、后续路线图。设计类文档偏"为什么这么设计"，runbook 偏"怎么操作"。

| 文档 | 主题 |
|------|------|
| [v0.11.0-roadmap.md](./design/v0.11.0-roadmap.md) | v0.11 路线图：计费准确性、模型治理与路由运营闭环（已收尾） |
| [v0.16-roadmap.md](./design/v0.16-roadmap.md) | v0.16 路线图：上线收尾、契约加固、运营增强与工程卫生（已收尾） |
| [v0.17-roadmap.md](./design/v0.17-roadmap.md) | v0.17 路线图：工程收尾、运营闭环与按需增强（已收尾） |
| [v0.19-v0.20-execution-record.md](./design/v0.19-v0.20-execution-record.md) | v0.19 → v0.20 执行记录：兼容性契约、迁移治理、CI 分层与发布收口（已归档） |
| [v0.21-roadmap.md](./design/v0.21-roadmap.md) | v0.21 路线图：事件驱动对账、质量门禁补齐与触发式观察（已归档） |
| [v0.22-roadmap.md](./design/v0.22-roadmap.md) | v0.22 路线图：安全配置、契约治理与小范围可靠性修复（已归档） |
| [v0.22-relay-execution-boundary-adr.md](./design/v0.22-relay-execution-boundary-adr.md) | v0.22 Relay executor / adaptor 执行边界 ADR |
| [v0.23-roadmap.md](./design/v0.23-roadmap.md) | v0.23 路线图：上线观察与 Relay executor 首切片（已归档） |
| [v0.23-executor-observation.md](./design/v0.23-executor-observation.md) | executor 新旧路径 7 天生产观察与回滚事实源 |
| [v0.24-web-release-readiness.md](./design/v0.24-web-release-readiness.md) | v0.24 双语 Web、中国法律协议与发布隔离准备清单 |
| [next-stage-plan-2026-10-07.md](./design/next-stage-plan-2026-10-07.md) | 当前计划：IAM 上线稳定性、本人/公开关键链路、专项持续门禁与 executor 有限灰度收口 |
| [rbac-permission-management-implementation-plan.md](./design/rbac-permission-management-implementation-plan.md) | RBAC A–D 实施与交接记录，已纳入 v0.34.0；后续补丁见发布说明 |
| [next-stage-plan-2026-09-22.md](./design/next-stage-plan-2026-09-22.md) | 已归档：R/O/Q 可靠性与观测交付；外部证据和条件项由新计划接管 |
| [v0.30-roadmap.md](./design/v0.30-roadmap.md) | 已归档：分组 v2 配置、真实链路验收与稳定性收口 |
| [v0.27-roadmap.md](./design/v0.27-roadmap.md) | 已归档：发布完整性、双灰度闭环与轻量产品化 |
| [web-playground-implementation-plan.md](./design/web-playground-implementation-plan.md) | 已交付首版的原设计：交互、密钥安全、SSE、Relay CORS；历史消息重用已随 v0.33.6 上线 |
| [architecture-review-remediation-report-2026-08-25.md](./design/architecture-review-remediation-report-2026-08-25.md) | 系统架构审查复核、修复方案与执行状态（2026-08-25） |
| [systematic-code-review-remediation-2026-08-25.md](./design/systematic-code-review-remediation-2026-08-25.md) | 系统性代码审查方案复核、优化与修复状态（2026-08-25） |
| [ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md) | 整体架构重构方案 |
| [BASELINE.md](./design/BASELINE.md) | 性能基线 |
| [hybrid-relay-adaptor-apicompat-plan.md](./design/hybrid-relay-adaptor-apicompat-plan.md) | 混合中转网关技术方案 |
| [subscription-upgrade-plan.md](./design/subscription-upgrade-plan.md) | 订阅系统增强方案 |
| [subscription-priority-deduction-design.md](./design/subscription-priority-deduction-design.md) | 订阅优先扣减模型改造 |
| [subscription-renewal-semantics.md](./design/subscription-renewal-semantics.md) | 订阅续费语义 |
| [subscription-refund-reversal-semantics.md](./design/subscription-refund-reversal-semantics.md) | 订阅退款 / 冲正账务语义 |
| [subscription-usage-api.md](./design/subscription-usage-api.md) | 订阅套餐用量查询接口 |
| [subscription-follow-up-roadmap.md](./design/subscription-follow-up-roadmap.md) | 已归档：订阅系统历史规划与交付记录 |
| [subscription-follow-up-code-review.md](./design/subscription-follow-up-code-review.md) | 订阅系统后续规划 Code Review |
| [subscription-account-quota-follow-up.md](./design/subscription-account-quota-follow-up.md) | 上游账号额度后续工作 |
| [usage-billing-reconciliation-plan.md](./design/usage-billing-reconciliation-plan.md) | 用量统计 / 对账复盘 |
| [token-usage-billing-semantics-remediation-2026-08-31.md](./design/token-usage-billing-semantics-remediation-2026-08-31.md) | Token usage 协议语义、计费规范桶与历史审计修复方案 |
| [quota-removal-follow-up.md](./design/quota-removal-follow-up.md) | Quota 移除后续工作 |
| [sub2api-borrowable-ideas.md](./design/sub2api-borrowable-ideas.md) | sub2api 可借鉴内容清单 |
| [issue-4-sqlite-solution.md](./design/issue-4-sqlite-solution.md) | Issue #4 SQLite/Postgres 轻量化部署 |

### migration/ — 迁移方案

Kratos 大仓结构、grpc-gateway、log-service 降级、buf 工具链迁移、Kratos v3 升级等迁移类文档。

| 文档 | 主题 |
|------|------|
| [kratos-monorepo-migration-implementation-plan.md](./migration/kratos-monorepo-migration-implementation-plan.md) | Kratos 大仓迁移实施方案（落地用） |
| [kratos-monorepo-migration-plan-final.md](./migration/kratos-monorepo-migration-plan-final.md) | Kratos 大仓迁移方案（最终版） |
| [kratos-monorepo-migration-plan-v3-corrected.md](./migration/kratos-monorepo-migration-plan-v3-corrected.md) | Kratos 大仓迁移方案（v3 修正版） |
| [log-service-to-platform-logging.md](./migration/log-service-to-platform-logging.md) | log-service 降级为 platform/logging 组件 |
| [grpc-gateway-migration-todo.md](./migration/grpc-gateway-migration-todo.md) | grpc-gateway 迁移 TODO |
| [buf-migration-and-kratos-v3-upgrade-plan.md](./migration/buf-migration-and-kratos-v3-upgrade-plan.md) | buf 工具链迁移 + Kratos v3 升级综合方案（含两个未知项确认结论） |

发布恢复（2026-10-07）：v0.34.5 候选因 Linux bytecode 工作区检查失败中止；原 tag 保留。修复后正式版本为 v0.34.6，最终 Billing 镜像已更新，对账 #366 completed/0 差异，见 [最终证据](./runbooks/evidence/patch-v0.34.6-production-2026-10-07.json)。
