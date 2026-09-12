# 分组重设计 v2 · 阶段 F 完成记录

> 日期：2026-09-11
> 状态：本地实现与验证完成；未执行生产迁移、部署或开关切换。
> 方案：[group-redesign-v2.md](./group-redesign-v2.md) §5、§8.2、§10。

## 1. 有序候选组（Auto）

Auto 使用 `routing_mode=ordered` 和 Token 的显式有序组 ID 列表（`token_routing_group_orders` 规范化表，按 `position` 排序），不新增名为 auto 的虚拟组，也没有组互相指向的 fallback 图。identity 校验策略形状（非空、无 0、无重复、固定组 ID 必须为 0），admin 编排校验每个组存在且未归档、至少一个当前有资格（无资格组允许预列，授权后即生效），组内资格与状态由 relay 每次请求按当前事实解析。

Relay 在 `Plan()` 解析期间（首次发送前、预扣前）按用户顺序遍历候选组，且只在此阶段允许跨组递进：

- 组不存在、停用或归档：递进；
- 组在模型上无候选资源（只读 `HasRoutingCandidates` 探针，不推进加权调度状态）：递进；
- 无访问资格、结算资格不足（billing `CheckRoutingSettlement` 探针，RPC 失败按不可用终止）：终止，不自动切去更贵的组扣钱包；
- 列表耗尽：明确拒绝，永不回退全局组列表。

候选探针之前先做一次 sticky 扫描：会话键 `v2/u<uid>/t<tid>/g<gid>/<hash>` 携带组 ID，已绑定的会话总落在原组；原组失效（停用、撤权、移出列表）时提示新建会话，不把上游会话状态交给另一组。绑定后的 Responses/WS 会话与预发送重试通过 `BoundGroupID` 只重校验原组，recheck 永不改组；`BindRoutingAdmission` 仍对 GroupID 变化硬失败。每个 attempt 用 `AttemptOrdinal` + 冻结候选快照生成唯一上下文摘要，旧预扣幂等释放后才建立下一 attempt，重复消息不产生第二次扣费。

## 2. 用户专属组倍率

新增 `(user_id, routing_group_id, version)` 覆盖，**替换**（绝不与基础倍率相乘）组基础倍率；billing mode 仍取发布策略。heads + history 两表，Set 单事务内 head 版本 +1 并追加历史行（清除后重设从历史 MAX 起算保持单调），Clear 删 head 留历史。解析顺序：基础 GroupRatio → 发布策略 → 用户覆盖；版本串 `user_routing_price:<gid>:<uid>:<ver>` 进入冻结快照与报价 `source=user_routing_price_override`。预扣即冻结快照，覆盖变更不影响在途请求（重放同请求 ID + 同上下文保持幂等）。

## 3. 组内优先级与权重覆盖

`channel_routing_groups` / `account_routing_groups` 各加 nullable `priority_override`/`weight_override`（NULL = 继承）。生效链：关系覆盖 > mapping/ability 优先级 > 资源自身优先级；权重覆盖仅在 >0 时生效并沿用 `Weight>0 ? Weight : Priority : 1` 选择语义。全 NULL 时调度输入与 F 前逐字节一致（测试以基线 ability 列表相等证明）。写入与组成员变更同事务 bump revision + routing outbox。旧服务/未迁移部署惰性安全：列探测（gorm Migrator）缺席时按全 NULL 处理。顺带修正一处三方言隐患：`abilities.enabled`、`subscription_account_abilities.enabled` 为 INTEGER 列，查询参数原来传 Go `true`，MySQL/SQLite 隐式宽容但 pgx 二进制协议拒绝 bool→int4，统一改为字面 `1`。

## 4. 接口与界面

| 入口 | 行为 |
|---|---|
| `POST/PATCH /api/v1/routing-tokens` | 接受 `routing_group_ids`；ordered 模式要求开关开启且校验候选列表 |
| `GET /api/v1/routing-groups/available` | 每组增加 `ordered_eligible`、`user_price_ratio`、`user_price_version` |
| `PUT/DELETE /api/v1/admin/routing-groups/{id}/user-price/{userID}` | 设置/清除用户专属倍率（有限正数校验，审计落库） |
| `PUT /api/v1/admin/routing-groups/{id}/resource-overrides` | 设置/清除资源关系优先级与权重覆盖（空=继承） |
| 组详情资源 | 返回 `priority_override`/`weight_override`（null=继承） |
| billing RPC | `SetUserRoutingPrice`/`ClearUserRoutingPrice`/`CheckRoutingSettlement`；能力位 `user_price_overrides`；报价回传用户覆盖字段 |
| channel RPC | `SetRoutingGroupResourceOverrides`（事务 revision+outbox）、`HasRoutingCandidates`（只读探针） |
| relay 开关 | `RELAY_ROUTING_ORDERED`（默认关）；`ADMIN_ROUTING_ORDERED_KEYS`（默认关） |

用户端 Token 页面支持 跟随默认/固定分组/有序候选组 三态，ordered 用多选 + 上下箭头排序，提示“各组价格可能不同”；列表显示“有序候选组（N 个）”徽章。管理端分组详情提供覆盖输入（空=继承），用户授权弹窗提供用户倍率设置/清除。

## 5. 迁移、启用与回退

新增三方言迁移：`098_identity_ordered_token_groups`（identity）、`099_billing_user_routing_price_overrides`（billing，heads+history）、`100_channel_routing_relation_overrides`（channel，4 个可空列），均已登记 ownership.yaml。全部纯增量，098/099 新表、100 可空列，旧版本服务安全忽略。

启用顺序：

1. 部署 identity/billing/channel（098/099/100），纯增量无需回填。
2. 开启 `ADMIN_ROUTING_ORDERED_KEYS`，管理员即可创建 ordered Key（relay 未开时该 Key 暂不能被服务，identity 对旧 relay fail-closed，绝不下钻为 inherit）。
3. 部署 relay F 并开启 `RELAY_ROUTING_ORDERED`；按能力位 `user_price_overrides` 逐步开放用户倍率。

回退：第 1–2 步可自由回退；一旦有 ordered Key 在跑，旧 relay 二进制无法服务（identity fail-closed），回退需先把 ordered Key 改回 inherit/fixed。用户倍率与关系覆盖数据对旧服务惰性安全；在途预扣按 C 的排空窗口处理。前端仍按仓库部署约定单独发布 `web/dist`。本次没有执行生产操作。

## 6. 验证记录

测试使用本机一次性 MySQL 8、PostgreSQL 16、临时 SQLite（MySQL/PostgreSQL 通过 opt-in DSN 环境变量），以及 fake RPC/HTTP/WS；未访问真实付费上游或生产数据库。

| 检查 | 结果 |
|---|---|
| 三方言 098/099/100 完整迁移及重复应用（无操作断言） | 通过 |
| 跨组授权：停用/归档/删除组递进，无资格/结算拒绝终止且不探测后续组，列表耗尽不回退全局，模型白名单全局拒绝 | 通过（`internal/biz/routing_ordered_test.go`） |
| 重试幂等：绑定组跨 recheck 不变、撤权硬失败、同 attempt 重放 digest 一致、不同 ordinal digest 唯一、sticky 失效硬失败 | 通过 |
| 会话语义：sticky 扫描先于候选探针且命中原组、WS/Responses 恢复按 BoundGroupID 只重校验原组、stored route 组必须在候选列表内 | 通过（含 `route_permission_test.go` 回归） |
| 价格：用户覆盖替换非相乘、版本串、清除后回落策略、在途冻结（改价/清除后重放不冲突）、报价 source/version | 通过；三方言 |
| 结算资格：三种模式（wallet_only 余额、subscription_only 覆盖+窗口额度、subscription_first 回退钱包）、RPC 缺席跳过、错误 fail-closed | 通过；三方言 |
| 组内覆盖：override > ability > source 生效链、全 NULL 分布不变式、detail 暴露、revision/outbox 同事务、未知关系/归档组拒绝 | 通过；三方言 |
| ordered 数据：创建/替换/清空列表、级联删除、三方言 CRUD 与 CAS | 通过（identity `TestOrderedTokenGroupOrders`） |
| 全仓构建、单元测试、竞态检查、迁移检查 | 通过 |
| 前端 lint、类型检查与生产构建 | 通过 |

本机日志位于 `/tmp/group-v2-f-*.log`。这些证据证明本地代码验收完成，不表示生产已上线。
