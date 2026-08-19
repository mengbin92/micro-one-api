# 文档索引

本目录按职能分类组织，方便快速定位。新增文档时请放入对应子目录。

```
docs/
├── README.md            ← 本索引
├── TODO.md              ← 当前待办、优先级与验收标准
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
| 部署 / 升级服务 | [deployment.md](./deployment.md) |
| 查看下一阶段执行路线 | [design/v0.22-roadmap.md](./design/v0.22-roadmap.md) |
| 查看当前待办和历史完成记录 | [TODO.md](./TODO.md) |
| 查看产品界面预览 | [根 README 界面预览](../README.md#界面预览) |
| 查看某版本发布内容 | [releases/](./releases/) |
| 排查订阅系统生产故障 | [runbooks/subscription-production-runbook.md](./runbooks/subscription-production-runbook.md) |
| charge 后监控告警与 SQL 口径 | [runbooks/cache-creation-charge-monitoring.md](./runbooks/cache-creation-charge-monitoring.md) |
| 发布后强制失败验证（§9.2） | [runbooks/post-release-forced-failure-verification.md](./runbooks/post-release-forced-failure-verification.md) |
| 理解整体架构 | [design/ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md) |
| 了解订阅系统路线图 | [design/subscription-follow-up-roadmap.md](./design/subscription-follow-up-roadmap.md) |
| 查看 Kratos 大仓 / buf / v3 升级迁移方案 | [migration/](./migration/) |

> **路线图入口治理**：「当前执行路线图」只有一个事实源——`design/` 下最新版本路线图，其头部标注「状态：进行中，当前唯一执行入口」。新阶段立项时：新建 `design/vX.Y-roadmap.md` 作为唯一入口 → 旧路线图头部降级为「已归档」并指回新入口 → 同步本表「查看下一阶段执行路线」行、`design/` 表格与 [TODO.md](./TODO.md) 顶部。三处不一致即视为文档漂移。

当前 v0.22 状态：P0 启动安全与写入收紧已完成，存量凭证迁移待执行；P1 契约治理、前端 strict、模型映射批量化和 Admin 批量删除已完成；P2 请求体分级限额、前端错误兜底和 Relay 执行边界 ADR 已完成，executor 首切片按独立 PR 灰度。

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
- [v0.21.0](./releases/release-v0.21.0.md)（最新）

### runbooks/ — 运维操作手册

面向生产操作的标准流程（SOP），涵盖订阅系统的发布、配置、排障与压测。

| 文档 | 用途 |
|------|------|
| [subscription-production-runbook.md](./runbooks/subscription-production-runbook.md) | 订阅系统生产发布、回滚与排障总入口 |
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
| [v0.22-roadmap.md](./design/v0.22-roadmap.md) | v0.22 路线图：安全配置、契约治理与小范围可靠性修复（当前） |
| [v0.22-relay-execution-boundary-adr.md](./design/v0.22-relay-execution-boundary-adr.md) | v0.22 Relay executor / adaptor 执行边界 ADR |
| [ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md) | 整体架构重构方案 |
| [BASELINE.md](./design/BASELINE.md) | 性能基线 |
| [hybrid-relay-adaptor-apicompat-plan.md](./design/hybrid-relay-adaptor-apicompat-plan.md) | 混合中转网关技术方案 |
| [subscription-upgrade-plan.md](./design/subscription-upgrade-plan.md) | 订阅系统增强方案 |
| [subscription-priority-deduction-design.md](./design/subscription-priority-deduction-design.md) | 订阅优先扣减模型改造 |
| [subscription-renewal-semantics.md](./design/subscription-renewal-semantics.md) | 订阅续费语义 |
| [subscription-refund-reversal-semantics.md](./design/subscription-refund-reversal-semantics.md) | 订阅退款 / 冲正账务语义 |
| [subscription-usage-api.md](./design/subscription-usage-api.md) | 订阅套餐用量查询接口 |
| [subscription-follow-up-roadmap.md](./design/subscription-follow-up-roadmap.md) | 订阅系统后续规划路线图 |
| [subscription-follow-up-code-review.md](./design/subscription-follow-up-code-review.md) | 订阅系统后续规划 Code Review |
| [subscription-account-quota-follow-up.md](./design/subscription-account-quota-follow-up.md) | 上游账号额度后续工作 |
| [usage-billing-reconciliation-plan.md](./design/usage-billing-reconciliation-plan.md) | 用量统计 / 对账复盘 |
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
