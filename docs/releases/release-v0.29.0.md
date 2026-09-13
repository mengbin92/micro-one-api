# Micro-One-API v0.29.0 发布：路由分组重设计 v2（分组实体、订阅合约与有序候选组）

> 2026-09-13 · 上一版：[v0.28.1](./release-v0.28.1.md)（2026-09-11）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.29.0)

v0.29.0 是 v0.28.1 之后的 **MINOR 路由分组重设计版本**，交付分组重设计 v2 的全部
实施阶段（A–F）：把「分组」从散落在用户、渠道、账号和倍率配置里的字符串，升级为有
稳定 ID、生命周期、资源成员和使用权限的**路由分组实体**，并在此之上交付订阅合约
（contracts / entitlements）、按组结算模式、有序候选组（ordered/Auto）和用户专属
倍率。配套修复一轮跨服务代码审查发现的问题。

**兼容性基调：新代码 + 旧行为。** 全部能力由分阶段开关控制、默认关闭；不打开任何
开关时，系统行为与重设计前完全一致（所有用户走服务端默认组、legacy 订阅合同、
inherit 模式）。启用路径见
[路由分组运维 runbook](../runbooks/routing-groups-runbook.md)，逐阶段、可灰度、
可回退，不要在同一窗口内跳阶段全开。

**proto additive**（`common.v1` 新增 `ResolvedRoutingContext` / `RoutingSubjectFacts` /
`UserRoutingGroupGrant`，`identity.v1` 新增路由事实字段与错误原因，`channel.v1`
新增错误原因，`LedgerEntry` 新增请求快照证据字段）；MySQL / SQLite / PostgreSQL
三方言新增迁移 `092`–`100`（全部 additive：新表 / 加列 / 可空列，无回填型改写）；
新增配置均为可选开关，默认关闭。

## 1. 路由分组实体与授权（阶段 A–B）

**根因**：旧模型没有组的事实源——用户、渠道、账号、模型映射和倍率配置各自携带组名
字符串；删除倍率覆盖不会撤销组访问，页面无法准确表达组是否存在、可用或被引用；
用户切组会影响其全部 Key。

**交付**：

- `routing_groups` 实体（稳定 ID、`key` 精确唯一、状态、资格模式、revision）及
  channel / account 成员关系表，随既有 CSV 配置双写回填（迁移 `092`）。
- 用户资格与 Key 选组分离：用户有默认组 + 零到多个显式授权组；授权记录携带来源
  （migration / grant / subscription）与有效期。
- 只读跨 schema 清点工具与审计基线，修复清点中发现的跨服务 schema 数据问题。
- 管理台「分组」页面：列表（名称/键、状态、资格、结算方式、倍率、模型数、资源数、
  有效套餐数）与**显式新建分组**（`POST /api/v1/admin/routing-groups`；新建组固定
  停用、资格模式可选，避免半成品组立即可路由）。

**影响服务**：`channel-service`、`identity-service`、`admin-api`、web。

## 2. 请求快照与 identity 路由事实（阶段 C，默认关）

**根因**：`ReserveQuotaRequest` 没有实际路由组，预扣与结算按 `account.Group` 取价；
Key 选组前必须补齐上下文，否则错组计费；请求中途改价/换组也没有快照语义。

**交付**：

- billing 请求快照（迁移 `093`）：reservation 携带冻结的价格与路由上下文，在途
  改价/换组不影响已预扣请求；`LedgerEntry` 记录 `routing_group_id/key` 与
  `request_snapshot_hash/json` 证据。
- identity 路由事实（迁移 `094`）：用户与 Token 携带默认组、授权、revision，鉴权
  返回 `routing_context_version=2`；`common.v1` 新增 `ResolvedRoutingContext` /
  `RoutingSubjectFacts` / `UserRoutingGroupGrant`。

**影响服务**：`billing-service`、`identity-service`。

## 3. fixed 组 Key、多组授权与 outbox（阶段 D，默认关）

- Token 支持 `inherit`（跟随默认组）与 `fixed`（固定组）模式，每次调用重新确认资格。
- 授权与生命周期变更经 `routing_change_outbox` 投递（identity / channel / billing
  三个 schema 各一份，迁移 `095`）。
- 用户可用组目录 API 与前端选组入口。

**影响服务**：`identity-service`、`channel-service`、`billing-service`、web。

## 4. 订阅合约、覆盖组与结算模式（阶段 E，默认关）

**根因**：旧订阅按用户查唯一有效订阅，额度策略的 `Platform` 只是描述字段，无法表达
「只覆盖 Claude 组」；购买权益没有快照。

**交付**：

- 订阅合约（迁移 `096`）：套餐声明覆盖组与访问权益，用户订阅冻结购买时的合同快照；
  权益带版本号，到期按来源撤销访问，不改写默认组与其他授权。
- 按组计费策略（迁移 `097`）：`routing_billing_policies` 版本化价格/模式，支持
  `wallet_only` / `subscription_first` / `subscription_only` 结算模式，与合同引用
  互斥。
- 订阅购买全流程事务化（单连接 SQLite 不再自等待），含幂等回执。

**影响服务**：`billing-service`、`domain/subscription`、`admin-api`、web。

## 5. 有序候选组、用户倍率与关系覆盖（阶段 F，默认关）

- Token 可携带显式有序候选组列表（迁移 `098`）：按顺序尝试，仅在前置组无可用资源时
  切换到下一组；绑定组失效（掉出候选、失去资格、被停用）时要求客户端重新发起，
  不静默漂移。
- 用户专属组倍率（迁移 `099`）：heads + history 版本化，替换组基础倍率。
- 组内成员关系 priority / weight 覆盖（迁移 `100`，可空列）。

**影响服务**：`identity-service`、`billing-service`、`channel-service`、`relay-gateway`、web。

## 6. 审查修复（随本版一并发布）

- **relay**：有序路由把非 NotFound 的组读取错误当作「组不存在」静默跳到下一候选；
  现在真实读取错误会中止本次尝试，仅 NotFound 穿透。
- **channel**：`HasRoutingCandidates` 在注册表读取失败时落入无限制回退路径、并在
  探测资源时吞掉非 NotFound 错误；注册表拒绝不再放宽访问，意外错误向上传播。
- **billing**：订阅覆盖判定使用未滚动窗口与最宽松语义，与 reserve 不一致——统一为
  滚动窗口 + 最严格限额；无订阅的钱包用户查价不再误报 `ErrSubscriptionNotFound`；
  订阅购买事务内的 plan / group / billing policy 读取全部留在事务连接上，修复
  单连接 SQLite 死锁。
- **identity**：v2 默认组命令会改写 migration 授权，把临时/订阅访问变成永久访问；
  现在只改偏好，迁移授权的改写保留在 legacy 路径。
- **web**：有序组选择器丢弃不可用候选 ID 导致序号静默重排；管理台覆盖解析接受
  小数优先级。两者均已修复。
- **deploy**：`deploy-update.sh` 在校验参数前就开始部署列表中靠前的服务；现在任何
  构建或远程调用之前先校验完整服务列表，附 `scripts/test-deploy-update.py` 回归。

## 兼容性说明

- **API / proto**：additive。新增 `common.v1` 路由上下文消息、`identity.v1` 路由事实
  与错误原因、`channel.v1` 错误原因、`LedgerEntry` 快照证据字段；无破坏性变更。
- **数据库**：迁移 `092`–`100`（三方言），全部 additive。生产 per-service schema 须
  **逐 schema 带 `-ownership` 执行**；`phase1_indexes.sql` / `phase3_partitioning.sql`
  / `schema_split.sql` 是参考 DDL，不进自动迁移。执行前备份全部服务 schema，先
  `-status` 核对 pending。已知坑与补录步骤见 runbook §2.2。
- **配置**：新增能力开关均默认关闭；不开关则行为与 v0.28.1 完全一致。
- **运行时**：默认路径的路由、计费与重试决策不变；新能力的启用须按 runbook 逐阶段
  灰度。
- **回滚**：镜像回滚前为线上现有镜像打 `rollback-<ts>` 标签即可；已应用的 additive
  迁移无需回退（新表/新列不被旧代码引用）。已打开开关的环境须先按 runbook 反向
  关闭开关再回滚镜像。

## 升级步骤

```bash
git fetch --tags
git checkout v0.29.0
```

1. **备份全部服务 schema**，然后按 runbook §2.1 逐 schema 执行迁移 `092`–`100`
   （本地交叉构建静态 `migrate` 二进制上传，经 `docker run` 执行；先 `-status`
   核对 pending 只含本次新增编号）。
2. 本地交叉构建 `linux/amd64` 镜像（**不在资源受限服务器上构建**）：本次涉及
   `identity-service`、`channel-service`、`billing-service`、`admin-api`、
   `relay-gateway`，可用 `scripts/deploy-update.sh identity-service channel-service
   billing-service admin-api relay-gateway` 一次完成（脚本会先校验完整服务列表）。
3. 重新构建并同步前端 `web/dist`（前端经 `/opt/web/dist` 挂载提供，重建镜像不会
   更新前端）。
4. 验证默认行为不变后，如需启用新能力，严格按
   [routing-groups runbook](../runbooks/routing-groups-runbook.md) 逐阶段打开开关。

## 验证

**已完成（发布前）**

- 后端相关包单元测试与 `-race` 检测通过（relay / billing / channel / identity /
  domain/subscription）。
- 前端 184 个测试与 lint 通过；`tsc --noEmit` 无错误。
- 仓库门禁通过：gofmt、分层（architecture）检查、migration-check、文档链接检查、
  commit-body 检查。
- `scripts/test-deploy-update.py`：拼错的服务名在任何构建/远程调用前被拒绝，且
  不会部分部署靠前的服务。

**待确认（发布后）**

- 生产迁移 `092`–`100` 按 `-ownership` 逐 schema 应用完成，`-status` 无 pending。
- 各服务重建后默认行为回归（不打开任何开关）：鉴权、路由、预扣/结算、管理台分组
  页只读展示正常。
- 新能力按 runbook 逐阶段灰度验证（每阶段有独立的验证与回退步骤）。

## 完整变更日志

- refactor(groups): separate routing membership from subscription quota policies
- refactor(groups): unify routing authorization and add read-only audit
- fix(groups): audit routing data across service schemas
- docs(groups): record verified read-only audit baseline
- feat(groups): establish routing group foundation for redesign v2
- feat(groups): freeze routing and billing context for redesign phase C
- feat(groups): deliver subscription contracts, entitlements, and settlement modes for redesign phase E
- fix(groups): close code-review findings and enforce a gofmt quality gate
- fix(docs): reject out-of-repo Markdown links and drop broken source links
- fix(deploy): honor service arguments in deploy-update.sh
- docs(runbooks): add routing-groups operations runbook
- feat(routing): add explicit routing group creation
- docs(routing): correct the new-group wiring order in the create form
- fix(routing): close review findings across relay, billing, channel, identity, and web
