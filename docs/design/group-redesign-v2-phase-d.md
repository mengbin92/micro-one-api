# 分组重设计 v2 · 阶段 D 完成记录

> 日期：2026-09-11
> 状态：本地实现与验证完成；未执行生产迁移、回填、部署或开关切换。
> 方案：[group-redesign-v2.md](./group-redesign-v2.md) §6、§7、§8.2。

## 1. 交付范围

开放用户多组授权、可用组目录和 `fixed` Key。一个用户可以让不同 Key 选择不同组；Relay 按该 Key 的实际组选择资源、读取模型目录并预扣，billing 的同步/异步结算和账单审计继续使用预扣时冻结的组与价格。`inherit` Key 继续跟随用户默认组。

本阶段保留 `subscription_first` 和旧订阅 `legacy_all_authorized` 的费用覆盖语义；订阅不会额外授予路由访问资格。同一用户的不同组消耗同一有效订阅的现有额度窗口。套餐覆盖范围、订阅访问权益、仅钱包/仅订阅模式属于 E；Auto 和高级定价属于 F。

## 2. 授权、Key 与服务边界

identity 的访问命令按用户授权版本执行 CAS，并在事务内修改对应来源。管理员可授予带起止时间的 `admin` 来源，或撤销指定 `admin` / `migration` 来源；不会接受客户端伪造的订阅来源。撤销某一来源不影响其他有效来源。

新默认组命令仅更改默认 ID、旧 `users.group` 投影和授权版本，保留所有来源授权。`public_group_access=explicit_only` 保持迁移用户原有访问范围；管理员明确选择 `all` 后，用户才能通过公开组来源获得访问资格。公开改为专属后，公开来源立即失效，显式授权继续有效。

Key 的固定组与用户默认组独立保存。改组命令同时校验所属用户、Key ID 和预期版本，只修改路由字段；隐藏的登录凭证不作为可管理 API Key。旧 `/api/token` 写接口拒绝新增路由字段，防止绕过受控入口。新建密钥只在创建响应中返回一次，列表和授权影响展示使用非秘密的名称、ID、模式、组及版本。

admin 在 biz 声明领域对象和窄 repo 接口，data 的 `routingaccess` 适配包通过 identity/channel/billing RPC 组合事实，service 转换 DTO；不读取其他服务的表。跨服务创建引用前读取当前组、用户资格和 billing 能力，运行时再校验，避免把创建时资格当作永久许可。

| HTTP 接口 | 用途与权限 |
|---|---|
| `GET /api/v1/routing-groups/available` | 登录用户的可用组、资格来源/期限、模型、有效价格/来源/版本、旧订阅费用覆盖、默认组有效性 |
| `GET/PATCH /api/v1/routing-access` | 登录用户读取自身事实；只允许自助设置有资格的默认组 |
| `GET/PATCH /api/v1/admin/routing-access/{user}` | 管理员查看用户及关联 Key，管理默认组、公开访问偏好和指定来源授权 |
| `POST /api/v1/routing-tokens` | 按当前用户资格创建 inherit/fixed Key |
| `PATCH /api/v1/routing-tokens/{token}` | 当前用户按 Key 版本修改路由选择 |
| `PATCH /api/v1/admin/routing-groups/{id}` | 管理员按组版本启用/停用、切换公开/专属；不提供硬删除 |

请求主体从登录会话取得，忽略 body 中的用户 ID；管理入口保留管理员鉴权。列表使用现有分页、过滤和排序解析，前端遍历分页后展示可用组；默认组有效性单独检查，不受当前页影响。依赖不可用时返回失败，不伪造资格、模型或价格。

## 3. 准入、重试与会话

`ResolvedRoutingContext` 支持 `fixed/token_fixed` 和 `inherit/user_default` 两种合法组合。fixed 选择 Key 的组，inherit 选择用户默认组；两者均需当前有效访问来源和启用的组。实际组写回请求鉴权快照，并贯穿候选选择、模型目录和预扣。Key 自身模型白名单继续收窄组模型权限。

v2 请求绕过旧 identity/候选选择缓存，以当前 identity 授权事实和 channel 状态判定准入。每次重试、adaptor failover、WS failover 重新鉴权和校验来源；撤权、期限届满、Key/用户或组停用后不能通过旧计划继续调用上游。依赖查询失败也不能用缓存恢复访问。

重检只判断是否仍允许执行，不覆盖已经接受的请求快照。Key 改到另一个组或 inherit 的默认组改变时，旧计划要求新建请求；不会拿旧组预扣执行新组资源。已发送请求的最终结算仍使用原快照。

Responses 恢复记录校验用户、Key 和组；跨 Key/组复用 response ID 被拒绝。共享 WS 和订阅会话粘性键按用户、Key、组隔离。v2 恢复未识别的旧 response ID 时要求新建会话，不把该 ID 发送给另一组/来源。WS 后续每轮继续执行 C 阶段的重新鉴权、当前来源校验和独立预扣。

fixed 预扣需要 billing 显式返回 `fixed_routing` 能力，仅理解 C 阶段版本号的实例不能通过此检查。预扣响应仍需通过版本和上下文摘要校验。

## 4. 事务 outbox 与失效传播

新增 `093_create_routing_change_outbox.sql`，提供 MySQL、PostgreSQL、SQLite 三个方言，ownership 同时列入 identity 和 channel。在拆分 schema 时分别落在所有者库；共享 schema 由迁移版本记录保证只应用一次。

用户授权/默认组/公开访问偏好、Key 改组和组状态命令在修改事实与递增 revision 的同一事务中插入事件。identity 旧管理员用户更新也递增版本并记事件；channel 既有组成员及带组模型映射的事务双写同时记录组版本事件，改映射时包含原组和目标组。数据库写入或 outbox 插入失败均整体回滚。

事件只保存所有者、实体类型、ID、revision 和时间，不包含密钥、授权凭证或可替代权威事实的完整快照。每个所有者按批读取待投递记录，发送至 `routing.changed` Redis Stream，成功后才标记已投递；Redis 故障保留待投递行，后续轮次重试。进程在发布后、确认前退出可能造成重复投递。

投递器直接使用 Redis Streams，不受通用事件总线的 memory 模式影响。未配置可用 Redis 时记录保留在数据库，服务重启并连接 Redis 后继续投递。Relay 为每个进程建立独立消费组，使各副本都能淘汰本地缓存；按实体单调 revision 忽略重复/乱序事件，淘汰失败保留重试机会。事件只做缓存淘汰，不能用旧事件覆盖较新事实。

即时撤权依靠权威准入读取，事件用于提前淘汰缓存，不能以 outbox 投递成功推断准入允许。订阅权益的 outbox 与版本推进随 E 阶段生命周期命令落地。

## 5. 目录、定价与审计界面

Tokens 页面展示各组的模型、资格来源/到期、有效倍率、价格来源和版本、订阅费用覆盖。新建和改组表单支持 fixed/inherit；默认组失效明确提示，不能保存无效选择。用户可以从可用目录设定默认组。

Users 管理页增加分组授权弹窗，展示各来源与关联 Key；可授予/撤销指定来源、设置到期、修改默认和公开访问偏好。分组详情增加启停与公开/专属控制，并说明对新调用及在途结算的影响。关键管理操作写入现有审计日志，不记录密钥。

billing 的组价格查询使用与 reserve 一致的有效配置投影；`GroupRatio` 缺省时明确显示 billing 默认来源。查询失败不会用展示值代替实际价格。实际结算价格仍以 reservation 快照为准。

账单列表新增实际组 ID/键及快照摘要，详情展示冻结证据。列表通过批量 reservation 查询读取证据，避免逐条读取；历史记录缺少快照时标为未知，不使用当前用户默认组补造历史。真实 PostgreSQL 回归同时修复了 Token `unlimited_quota` 整数字段写入，以及 ledger 文本用户 ID 与 users 数值 ID 的 join 类型不一致问题。

## 6. 启用与回退

以下为待上线步骤，本次未操作生产：

1. 保持 C 阶段的回填与请求快照前提，安装 093；identity/channel 启动就绪检查现在包含 outbox 表。已有 090/091/092 不做破坏性重写。
2. 先升级所有 billing 实例和提交消费者，确认版本 2 请求快照及 fixed 能力；升级所有 channel、identity、Relay 和 admin 实例。确保各服务 Redis 配置指向需要共享的事件集群。
3. 保持 `CHANNEL_ROUTING_GROUP_DUAL_WRITE=true`、`IDENTITY_ROUTING_V2=true`、`BILLING_REQUEST_SNAPSHOT_V2=true`、`RELAY_ROUTING_CONTEXT_V2=true`。跨服务 channel endpoint、服务鉴权和 schema 要求沿用 C。
4. 更新前端静态文件。完成当前资格、模型目录、实际价格和一个小额 fixed 请求的路由/预扣/账单核对，再设置 admin 的 `ADMIN_ROUTING_FIXED_KEYS=true` 开放新 fixed Key、fixed 改组与新增管理员授权。该开关默认关闭。
5. 上线监控待投递记录、事件投递错误、权威 RPC 延迟与失败率，并执行实际负载验证；本地测试不替代生产容量验收。

关闭 `ADMIN_ROUTING_FIXED_KEYS` 只停止新增 fixed 配置和授予操作，已有 fixed Key 的运行时鉴权、撤权、账单及结算仍工作；inherit 创建和符合资格的默认组维护继续可用。不要在仍有 fixed Key 或 v2 预扣时关闭运行时能力或回滚到不理解它们的旧实例。

回退顺序为：关闭创建开关，停止建立需要回退的配置，盘点 fixed Key 引用并明确迁移，排空/确认在途预扣及异步结算，再按 C 的混部限制回退服务。保留 additive schema、历史快照和事件记录；不得把 fixed Key 静默解释为用户默认组。仅需停止某组的新调用时优先停用组或撤销相应来源。

## 7. 验证证据

测试使用本机隔离 MySQL 8、PostgreSQL 16、临时 SQLite，以及模拟 RPC/HTTP/WS；未访问真实付费上游或生产数据库。

| 检查 | 结果 |
|---|---|
| 三方言完整迁移及重复应用，含 093 | 通过 |
| 多来源授权、按来源撤销、默认变更保留授权、CAS 冲突/回滚、fixed 持久化与所属用户校验 | 通过；三方言 |
| 组启停/公开模式 CAS，outbox 写失败时事实回滚 | 通过；三方言 |
| outbox 事务回滚、投递失败保留、所有者隔离、成功确认后不重投 | 通过；三方言 |
| 失效事件乱序/重复、淘汰失败后重试 | 通过 |
| 两个 fixed Key 的候选组隔离；撤权、停用及改组选路前重检 | 通过 |
| fixed 能力在 reserve 前检查，response ID 跨 Key/组拒绝，既有 WS/恢复回归 | 通过 |
| 两个组不同倍率、reserve 后改默认/改价、同步/异步 commit 与批量账单快照一致 | 通过；三方言 |
| 会话主体不可伪造、自助不能授予权限、管理员入口、分页输入验证 | 通过 |
| `make all`、`make test-unit`、`scripts/check-architecture.sh`、`make migration-check` | 通过 |
| `make test-race` 和 identity/channel/cache/outbox 补充竞态检查 | 通过 |
| 前端 lint、48 个文件 / 174 项测试、生产构建 | 通过 |

迁移检查只报告既有 allowlist 中 MySQL 057、SQLite 009 的历史重复编号。本机日志位于 `/tmp/group-v2-d-*.log`，包括 `generation`、`unit`、`three-dialects`、`member-dialects`、`outbox-tests`、`architecture`、`migration`、`race`、`race-extra`、`web-lint`、`web-all`、`web-build`、`web-token`。这些证据证明本地代码验收完成，不表示生产已上线。

下一阶段为 E：套餐覆盖组、订阅访问权益与合同版本、生命周期命令及新的结算模式。
