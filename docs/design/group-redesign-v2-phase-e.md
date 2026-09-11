# 分组重设计 v2 · 阶段 E 完成记录

> 日期：2026-09-11
> 状态：本地实现与验证完成；未执行生产迁移、部署或开关切换。
> 方案：[group-redesign-v2.md](./group-redesign-v2.md) §5、§8.2、§9。

## 1. 合同与访问权益

启用 `SUBSCRIPTION_ENTITLEMENTS_V2=true` 后，新套餐和管理员直接分配必须选择非空路由组范围。每组独立设置 `grants_access`：费用覆盖不自动授予访问权；有访问权益时，在订阅生效期间增加 `subscription/{subscription_id}` 来源，不改用户默认组或 identity 的永久授权。

新合同冻结覆盖组 ID/Key、日/周/月额度、额度消耗倍率和策略版本，并计算内容摘要。套餐编辑只影响后续购买；上下架不重写冻结额度策略。已售合同继续按快照履约，停用额度策略阻止新销售，已有订单仍按购买快照履约。旧套餐、旧订阅和旧订单保留 `legacy_all_authorized` 费用覆盖及原有额度策略语义，不自动获得访问权。

共享订阅领域仍由 admin、billing、Relay 嵌入，不新增微服务。admin/Relay 每次解析资格读取当前有效订阅，按状态、起止时间合并权益；重试再次读取，依赖失败则拒绝。重新解析同一对象会替换旧订阅来源。identity 的授权版本与订阅 ID/权益版本分别进入请求上下文。

## 2. 购买、续费、更换与退款

- 钱包购买和显式更换交由 billing 协调，订阅、钱包、账单、幂等回执和权益 outbox 在同一数据库事务提交。`Idempotency-Key` 必填；同键同请求返回原结果，同键不同请求拒绝，失败回滚后可用原键重试。
- 续费只接受相同合同，保持同一个 subscription ID 和共享窗口，剩余期限累加。不同合同需显式更换；立即更换保留已用额度和窗口，升级差价由服务端保存的购买价格计算。下一周期更换保存目标合同，在用户续费该目标合同时生效。
- 新支付订单由 billing 读取可售套餐并冻结合同及价格，忽略客户端提供的购买金额。用户与幂等键生成稳定商户订单号；重复创建复用原订单，套餐停售/删除后也可查询重放。外部支付提供方使用同一商户订单号。
- 支付履约、重复回调与退款沿用订单行锁事务。退款撤销/缩短准确的原订阅并发布权益变更，重复退款不重复返还余额。退款金额仍按原实付订单计算；本阶段没有改动既有退款政策。
- 用户自助更换从登录凭证取得用户 ID，不接受客户端伪造价格；管理员仍可指定用户。旧的裸额度策略自助购买入口在 E 开启后关闭，新购买使用套餐。

## 3. 路由价格与结算模式

| 模式 | 费用归属 |
|---|---|
| `wallet_only` | 完全由钱包支付，不占用订阅额度 |
| `subscription_first` | 仅当有效订阅覆盖实际组时使用订阅，额度不足部分由钱包支付 |
| `subscription_only` | 必须有覆盖实际组的有效订阅；按费用上界冻结全部额度，不能转扣钱包 |

组价格和结算方式作为不可变版本发布，CAS 更新当前版本。请求预扣冻结该版本、实际组、合同、额度消耗倍率和窗口；后续改价、更换套餐不重定价已接受请求。尚未发布新策略的组保留旧 GroupRatio 投影及 `subscription_first`。E 开启后旧 `/api/group` 和 `GroupRatio` 写入口拒绝修改，应使用新策略入口。

切换结算模式前检查有效订阅、在售套餐和待履约订单。legacy 引用按可能覆盖全部授权组保守处理。套餐/合同/订单的新引用与模式发布共用事务互斥行，写入时重新检查结算方式，避免检查后新增覆盖引用。仅改价不要求清空合同。

### 仅订阅硬额度边界

首期只开放 OpenAI/Azure 文本 `/embeddings`，上游模型严格限制为 `text-embedding-3-small`、`text-embedding-3-large`、`text-embedding-ada-002`。输入只接受非空字符串或字符串数组；未知参数、token 数组、多模态、未知模型、聊天、Responses、流式和工具调用都在预扣时拒绝，因此不会进入上游调用。

这些文本模型使用 byte-level BPE，输入 UTF-8 字节数加每项 16 个边界余量作为保守 token 上界，最多 2048 项、200 万个上界 token。计算原理参考 [OpenAI tiktoken 实现](https://github.com/openai/tiktoken/blob/main/tiktoken/_educational.py)。该上界只由可信 Relay 协议适配器产生，公共请求不能直接指定。billing 按冻结价格换算费用，锁住同一订阅行并汇总已有冻结额度后准入。提交释放未使用部分，绝不把差额转为钱包扣款；异常上游用量超过上界时结算返回 `SUBSCRIPTION_BOUND_EXCEEDED`，拒绝超额度结算。

其他端点后续必须逐项证明输入、输出、缓存和工具的总费用上界，并增加验收测试，才能扩大仅订阅开放范围。

## 4. 接口与界面

| 入口 | 行为 |
|---|---|
| `GET/PATCH /api/v1/admin/routing-groups/{id}/billing` | 管理员查询和按版本发布结算策略 |
| 套餐创建/更新、管理员分配 | `coverage` 数组配置路由组及访问权，返回冻结 `contract` 和版本 |
| 套餐/订阅 DTO | 保留输入 `group_id`，增加只读 `quota_policy_id` 别名，避免与路由组 ID 混淆 |
| `POST /api/v1/subscriptions/purchase` | 基于 `plan_id` 的钱包购买/续费 |
| `POST /api/v1/subscriptions/purchase/payment` | 基于 `plan_id` 与幂等键创建外部支付订单 |
| `POST /api/v1/subscriptions/change` | 用户显式更换；所属用户由会话确定 |
| `GET /api/v1/routing-groups/available` | 资格来源叠加有效订阅权益，并返回实际结算模式和覆盖结果 |

管理界面提供覆盖组选择与访问权开关、结算策略版本编辑；用户售卖卡片和订阅进度显示冻结策略、消耗倍率、共享窗口和访问范围。相同合同可续费，不同合同需明确更换。旧合同明确标记，不把旧费用覆盖显示成访问授权。授权管理中订阅来源只能通过订阅生命周期撤销。

## 5. 迁移、启用与回退

新增三方言迁移 `094_add_subscription_contracts`、`095_create_routing_billing_policies`。前者增加合同、覆盖关系、权益版本、购买价格与幂等回执；后者增加价格/模式版本及合同引用互斥行。订阅生命周期复用 093 outbox；billing 拥有 093–095，admin 与 billing 启动订阅 outbox 发布器，Relay 处理 subscription 事件。无 Redis 时事件留在数据库，资格仍从订阅事实源实时校验。

启用顺序：

1. 备份并在 billing 的共享订阅数据库应用 093–095；admin、billing、Relay 必须使用同一订阅数据库，不能把合同表分放在独立 admin schema。
2. 部署包含 D/E 契约的 channel、identity、billing，再部署 admin/Relay 和前端。先满足 D 的回填与开关条件，保留现有请求的快照排空能力。
3. billing 开启 `BILLING_REQUEST_SNAPSHOT_V2`，Relay 开启 `RELAY_ROUTING_CONTEXT_V2`，admin/billing/Relay 一致开启 `SUBSCRIPTION_ENTITLEMENTS_V2`。检查 billing 能力 `request_snapshot_version=2`、`fixed_routing=true`、`subscription_contracts=true` 后创建新合同。
4. 先验证受控测试套餐的购买、撤权和实际组账单，再开放销售和策略修改。新模式启用后 billing 拒绝缺少 v2 路由上下文的预扣，避免旧 Relay 静默走钱包。

E 开关默认关闭。产生 selected_groups 合同或仅订阅策略后，不得关闭 E 并回退旧二进制；回退应先停止销售/新分配，保留理解现有合同、模式和在途快照的兼容版本。前端仍按仓库部署约定单独发布 `web/dist`。本次没有执行生产操作。

## 6. 验证记录

测试使用本机一次性 MySQL 8、PostgreSQL 16、临时 SQLite，以及模拟 RPC/HTTP/WS；未访问真实付费上游或生产数据库。

| 检查 | 结果 |
|---|---|
| 三方言完整迁移及重复应用，含 093–095 | 通过 |
| 钱包购买/续费/更换事务原子性、幂等回执重放、失败回滚后原键重试；冻结合同覆盖组不受套餐后续编辑影响 | 通过；三方言 |
| 三种结算模式准入与分账：wallet_only 不占订阅、subscription_first 未覆盖组按钱包结算、reserve 后改价不改已接受请求 | 通过；三方言 |
| subscription_only 硬额度边界：超上界拒绝、提交超上界返回 `SUBSCRIPTION_BOUND_EXCEEDED`、过期订阅返回 `SUBSCRIPTION_COVERAGE_REQUIRED`、覆盖组之间并发冻结共享同一窗口额度 | 通过；三方言 |
| 支付订单创建/重放、套餐删除后仍可重放、履约重复回调、退款撤销订阅权益与 outbox、重复退款不重复返还 | 通过；三方言 |
| 合同冻结价格购买忽略客户端金额、外部支付履约创建订阅、退款按原实付订单计算 | 通过；三方言 |
| 自助/管理员更换合同、下一周期更换保存目标合同、更换后保留窗口用量 | 通过；三方言 |
| 全仓单元测试、关键服务竞态检查、三方言生命周期测试 | 通过 |
| 前端 lint、测试与生产构建 | 通过 |

本机日志位于 `/tmp/group-v2-e-*.log`。这些证据证明本地代码验收完成，不表示生产已上线。
