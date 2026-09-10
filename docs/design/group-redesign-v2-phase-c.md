# 分组重设计 v2 · 阶段 C 完成记录

> 日期：2026-09-10
> 状态：本地实现与验证完成；未执行生产迁移、回填、部署或运行路径切换。
> 方案：[group-redesign-v2.md](./group-redesign-v2.md) §6.2、§6.3、§8.2。

## 1. 交付范围

identity 提供默认路由组和显式授权事实；Relay 仅解析 `inherit`，将实际组 ID/键及主体版本带入预扣；billing 在预扣事务中保存 v2 请求快照，同步提交、异步消费及重试均读取该证据。请求建立后，用户换组、价格及额度消耗倍率改变，不重新定价已接受的请求。

本阶段保留 `subscription_first`、旧订阅 `legacy_all_authorized` 覆盖语义，以及当前金额单位、逐桶舍入、最低扣费和钱包兜底规则。固定组 Key、用户多组管理、套餐覆盖权益、新结算模式仍按 D/E 阶段开放。

## 2. identity 事实与迁移

迁移 `092_add_identity_routing_facts.sql` 分别提供 MySQL、PostgreSQL、SQLite 版本，所有权归 identity：

- 用户增加 `default_routing_group_id`、`routing_access_revision`、`public_group_access`。旧用户默认 `explicit_only`，不会因为其他组改为公开而扩大访问资格。
- Token 增加 `routing_mode`、`routing_group_id`、`routing_revision`。迁移默认 `inherit`、空固定组 ID、版本 1；本阶段运行时拒绝 fixed 或不一致的字段组合。
- `user_routing_group_grants` 按用户、组和来源唯一。默认组是偏好，访问必须另有有效授权或明确允许公开组。

`app/identity/cmd/routing-backfill` 经 channel RPC 读取稳定组 ID，按旧 `users.group` 精确匹配，在 identity 自己的 serializable 事务内设置默认组、初始版本及 `migration / legacy_group` 来源授权。演练写入后整体回滚，只有 `--apply` 提交；未知组或已有映射冲突使整批回滚。重复执行验证既有映射及迁移授权，不覆盖独立管理员授权，不通过组名猜订阅权益。

开启 identity v2 后，注册和 OAuth 新建用户使用服务端 `IDENTITY_DEFAULT_ROUTING_GROUP`（默认 `default`），客户端旧 `group` 参数不能产生授权。旧管理员改组操作同事务更新默认 ID、旧键、授权版本及 migration 来源授权，保留其他来源；版本过时返回冲突。

鉴权在一致性读事务中读取用户、Token 路由字段及授权，检查用户/Token 状态、到期时间和旧键一致性，再返回 `routing_context_version=2`。启动时检查新增 schema 和未映射用户；缺少能力或迁移未完成时返回错误。

## 3. 请求上下文与会话

`domain/routing.ResolvedRoutingContext` 是纯 DO，包含用户 ID、Token ID、实际组 ID/键、inherit 模式、Token/用户授权/组版本和选择来源。C 阶段没有订阅访问权益，`SubscriptionEntitlementVersion=0`。DTO 转换和 channel RPC 适配分别位于 `platform/routingdto`、`platform/routingclient`，业务层不读取其他服务的存储模型。

Relay 从 identity 事实和 channel 当前组状态解析默认组，核对 ID/键，验证有效授权；原 `CanRoute`、Key 模型权限和来源检查继续适用。v2 选择绕过旧 channel 选择缓存，选择/来源校验 RPC 携带数字组引用，channel 拒绝 ID 与键不一致的组合。

HTTP、gRPC、Chat/Anthropic/Responses、raw/adaptor/orchestrator 及会话恢复路径将解析后的上下文传入预扣。发往 billing 前检查 `GetRoutingCapabilities`，成功预扣后核对快照版本和上下文摘要；能力缺失直接拒绝，响应证据不匹配则补偿释放预扣，均不调用上游。

Responses WebSocket 第一轮沿用连接建立时的预扣，后续 `response.create` 在转发前重新鉴权、校验当前来源并预扣。前一轮未完成时拒绝重叠生成；默认组或模型改变要求新建连接。每轮独立 reservation，终止时释放未完成轮次；同组 failover 释放旧 attempt 后使用新的幂等键。已建立会话不自动跨组切换。

完整的授权 outbox、版本失效传播、固定组目录和撤权矩阵属于 D。C 的直接事实读取和缓存绕过不能当作 D 的完整实时失效机制；尚未开放用户自助多组选组。

## 4. 冻结计费与窗口归属

迁移 `091_add_request_snapshots.sql` 的三个方言版本归 billing 所有。reservation 新增 nullable `request_snapshot`、`request_snapshot_hash`、`subscription_accounting_usd`；共享订阅领域通过同一结算事务写 `subscription_window_charges`，记录原订阅/策略/三个窗口及实际 accounting USD。

请求快照冻结以下内容：

| 内容 | 预扣时保存的证据 |
|---|---|
| 选择 | 路由上下文、实际组键、计费模型、`subscription_first` |
| 价格 | ModelPrice 五桶价格及扩展输入，或 ModelRatio/CompletionRatio；组倍率、cache creation/canonical 计费模式和灰度规则 |
| 价格版本 | `legacy_projection:` 加捕获价格输入的 SHA-256；新策略版本管理留给后续阶段 |
| 订阅 | 具体 subscription ID、额度策略 ID/内容摘要、原合同摘要、legacy 覆盖方式、Q、日/周/月限额与原窗口标识 |
| 冻结量 | reservation 原订阅 USD、钱包定点金额，以及该行订阅 USD × 自身 Q |

价格配置只捕获一次；读取失败拒绝建立 v2 预扣。快照拥有独立数据副本，读取验证版本、摘要、模型、用户和订阅/窗口绑定。账单详情 RPC 提供组 ID/键、快照摘要和 JSON；列表页和 UI 的审计展示属于 D。原 `PricingSnapshot` v1 保持可读；历史 reservation 没有 v2 证据时保留空值，不用当前价格补造历史。

相同 `(user_id, request_id)` 的重试必须保持模型和路由上下文摘要一致；不同上下文返回冲突。已释放的幂等键不能创建新预扣，已提交的请求返回原实际金额，不重复扣费。

同步和异步结算使用 reservation 价格，不使用当时的账户组重新计价。异步 worker 停止接收后以独立、最长 30 秒的单笔结算 context 处理已排队工作；服务关闭先排空队列，再关闭数据库。它仍是现有进程内队列，不等同于持久化消息队列。

并发冻结按每条 v2 预扣自己的 accounting USD 求和；仅缺少 v2 证据的历史行保留旧 Q 解释。实际订阅费用先按原金额规则舍入，再乘快照 Q 写窗口用量，避免浮点分账和记账不一致。

跨日/周/月、到期、撤销或更换订阅后，已接受费用仍归原 subscription ID 和原窗口。原窗口不再是当前窗口时保留独立历史用量，不增加新窗口或新订阅计数。只有当前仍为同一订阅、同一额度策略且三个窗口全部一致，才允许在冻结限额内追加吸收；否则最多使用原预扣订阅额度，剩余按既有钱包规则结算。事务失败同时回滚状态、钱包、流水及窗口记录。

真实方言回归还发现两处存储兼容问题：SQLite 历史 INTEGER epoch 与 GORM 时间文本混存，现在统一读取及过期比较；PostgreSQL `is_stream` 为 INTEGER，现在使用整数 PO 转换，避免布尔写入失败。未重写历史数据。

## 5. 发布、启用与回退

三个 C 阶段开关默认关闭，只有显式 `true` 开启：

| 服务 | 开关及前提 |
|---|---|
| billing | `BILLING_REQUEST_SNAPSHOT_V2=true`；091 schema；`CHANNEL_GRPC_ENDPOINT` 和既有 `SERVICE_TOKEN` |
| identity | `IDENTITY_ROUTING_V2=true`；092 schema 和用户回填完成；channel 连接；服务端默认组有效 |
| relay | `RELAY_ROUTING_CONTEXT_V2=true`；identity、channel、billing 均理解本阶段契约 |

以下是待上线时执行的顺序，本阶段未执行生产操作：

1. 完成 B 的组回填，并保证所有 channel 写实例持续双写。安装 091/092 additive schema。
2. 先将所有 billing 实例和异步消费者升级到能读取 v2 快照的代码，再开启 billing 开关。不可留下会忽略新快照的旧提交消费者。此时旧 Relay 的新预扣也能保存 legacy 组的价格快照。
3. 在暂停用户注册、管理员改组等 identity 写入的窗口，通过 channel RPC 读取组映射，演练并提交用户回填。所有 identity 写实例开启 v2 后恢复写入，防止旧实例创建未映射用户或只更新旧组字段。
4. 验证 identity 能返回版本 2、billing 能返回快照能力，再启用 Relay v2。混合版本缺少契约会拒绝新请求，不允许静默忽略实际组。
5. 用原组请求验证金额、订阅/钱包分账、Key 额度及会话；再进入 D 的固定组选组实现。

命令模板使用环境变量承载 DSN；执行前按服务设置 `MIGRATIONS_DSN`，以下示例为 MySQL：

```sh
go run ./cmd/migrate -driver=mysql -dir=./migrations -ownership=billing
go run ./cmd/migrate -driver=mysql -dir=./migrations -ownership=identity

# IDENTITY_SQL_DSN 指向 identity 所有者数据库。
# CHANNEL_GRPC_ENDPOINT 与 SERVICE_TOKEN 使用既有内部服务配置。
go run ./app/identity/cmd/routing-backfill --driver=mysql
go run ./app/identity/cmd/routing-backfill --driver=mysql --apply
```

独立 schema 使用 `--schema`；PostgreSQL 使用 `--driver=postgres` 和 `migrations/postgres`，SQLite 使用 `--driver=sqlite3` 和 `migrations/sqlite`。回填演练包含事务写入与锁，不是只读操作；不是进程启动的一部分。三方言测试覆盖存储边界的演练/提交/重放，未以生产 CLI 执行替代这些测试。

回退先停止新 v2 准入并排空 v2 reservation，再关闭新建快照开关或回退旧 billing。新代码对已有 v2 reservation 的提交始终读取快照，即使开关关闭；但旧二进制不理解该证据，不能用它结算在途 v2 请求。恢复旧 identity 写路径后，再启用前重新验证映射和授权一致性。保留 additive 字段与历史窗口证据，不删除表来回滚。

## 6. 验证记录

测试使用本机一次性 MySQL 8、PostgreSQL 16、临时 SQLite，以及本地模拟 RPC/HTTP/WS。外部方言回归只有显式提供测试 DSN 才启用，测试工具限制 loopback 并创建独立数据库/schema，结束后删除。

| 检查 | 结果 |
|---|---|
| 三方言全量空库安装与重复迁移，包含 091/092 | 通过 |
| identity 演练回滚、提交/重放、未知键整批回滚、大小写精确映射 | 通过 |
| 默认组不等于授权、授权期限边界、公开模式、停用组、fixed 拒绝 | 通过 |
| 服务端注册默认组、管理员改组来源隔离、版本冲突和事务故障回滚 | 通过 |
| 实际组与账户组不同；reserve 后改组、改 ModelPrice/Ratio；同步/异步结算 | 通过；三方言 |
| 五桶单价、可选价格指针、计费模式和灰度规则冻结；旧 usage/v1 envelope | 通过 |
| 同幂等键不同模型/上下文、重复 commit、释放后重用、损坏快照拒绝 | 通过 |
| Q 从 2 改为 4 的逐行冻结、分账与释放 | 通过；三方言 |
| 日/周/月切换、到期、撤销后新订阅、更换额度策略，保留原窗口归属 | 通过；三方言 |
| 注入账单写入失败后整体回滚，重试只记账一次 | 通过；三方言 |
| 能力探测在 reserve 之前、响应摘要不匹配补偿、ID/键不一致拒绝 | 通过 |
| WS 每轮转发前预扣、重叠轮次拒绝、准入失败不转发及现有会话回归 | 通过 |
| SQLite 历史时间混存及未来预扣不误过期、identity typed reason 经 gRPC 保留 | 通过 |
| `make all`、`make test-unit`、架构检查、`make migration-check` | 通过 |
| `make test-race`，identity/channel/routing 补充竞态检查 | 通过 |

迁移检查仅报告既有 allowlist 内的历史重复编号（MySQL 057、SQLite 009），没有新增违规。全仓回归包括现有 Key 额度、分账、Responses/WS 与恢复路径测试；未执行真实付费上游或生产压测。本阶段无前端改动。

本机日志为 `/tmp/group-v2-c-{generation,unit,three-dialects,race,identity-race}-final.log`、`/tmp/group-v2-c-architecture.log`、`/tmp/group-v2-c-migration-check.log`；它们是本地验证证据，不表示生产已上线。

## 7. 下一阶段

阶段 D 在此上下文和冻结计费基础上开放多组授权、available API 与固定组 Key，补齐目录、审计以及撤权/停用的版本失效传播。D 验收必须证明实际路由组、预扣组、账单组一致，并覆盖重试和已绑定会话；E 再处理套餐范围、购买合同版本和新结算模式。
