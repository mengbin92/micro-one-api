# 分组 v2 运行基线（2026-09-13）

> 生产只读核查；没有变更配置、重启服务、创建 Key、请求模型或修改账本。
>
> 原计划：[下一阶段工作建议](../design/next-stage-plan-2026-09-13.md)；执行入口：[v0.30 路线图](../design/v0.30-roadmap.md)。

## 核查结论

生产 B–F 所列运行时与创建开关均为 `true`，三个订阅合约开关一致。阶段 F 只读预检于 **2026-09-13 21:19:44 CST** 通过，证据见[预检 JSON](./evidence/routing-preflight-2026-09-13.json)。仓库模板默认值仍为 `false`；新部署必须完成迁移和回填后按 runbook 启用。

| 状态层次 | 核查结果 |
| --- | --- |
| 代码合入 | 审阅基线 `develop@9402bb5f` 已包含 A–F |
| 本地 tag / 发布文档 | `v0.29.0@d694bb6a`，对应发布文档存在；本次不重新发布或核验远程镜像 registry |
| 生产镜像 | 五个相关服务运行中，重启次数均为 0；容器 image ID 见下表，不能由可变 latest 名称反推源码提交 |
| 迁移 | channel / identity / billing 各自 ownership 的 091–100 已应用 |
| 回填 | channel 有 1 条回填完成记录；5 位用户均具备路由事实，未映射为 0 |
| 能力响应 | channel ListRoutingGroups、identity GetUserRoutingFacts 成功；billing snapshot version=2，fixed / contracts / user price capability 均为 true |
| 业务对象 | 12 个 inherit Key，fixed=0、ordered=0；4 份无合约快照的旧订阅，1 份 selected_groups 合约（统计含历史状态） |
| 生产业务验收 | 本次没有生产模型请求或支付测试；生产能力已开不能等同于 fixed / ordered 已有真实使用样本 |

## 镜像与启用状态

2026-09-14 07:33:23 CST 使用最终预检工具复核：43 项检查通过，新增 endpoint 检查无缺失；工具对存量合约给出“保留 v2 兼容读取”的回退提示。见[复核 JSON](./evidence/routing-preflight-2026-09-14.json)。下表仍保留 09-13 的镜像采样时间，不以复核时间覆盖历史事实。

下列时间来自容器 inspect（UTC）；image 为主机上的镜像内容 ID，不是多架构 manifest digest。

| 服务 | image ID | 启动时间 UTC | 开关 |
| --- | --- | --- | --- |
| identity-service | `sha256:d98e5fd746379a2a8bac950cf61544eda541ef659dfddeda4581185a060cdbb7` | 2026-09-12T14:30:16.879517637Z | `IDENTITY_ROUTING_V2=true`；`IDENTITY_DEFAULT_ROUTING_GROUP=default` |
| channel-service | `sha256:174a71fd6d5b05171e2b778e79e29d26f3d5895a65152d4c23f02df9447e2692` | 2026-09-12T15:11:05.250722292Z | `CHANNEL_ROUTING_GROUP_DUAL_WRITE=true` |
| billing-service | `sha256:ae152bfb3a6d7176b2f8193ca6e44dbf62d665fa464871a4e6d74519c35110d5` | 2026-09-13T01:46:14.182406964Z | `BILLING_REQUEST_SNAPSHOT_V2=true`；`SUBSCRIPTION_ENTITLEMENTS_V2=true` |
| admin-api | `sha256:358b18ac47447c9bc14740b269739f09316b48c1041bdbd3dbf87038a7b18745` | 2026-09-13T01:44:51.737527868Z | `ADMIN_ROUTING_FIXED_KEYS=true`；`ADMIN_ROUTING_ORDERED_KEYS=true`；`SUBSCRIPTION_ENTITLEMENTS_V2=true` |
| relay-gateway | `sha256:969aa817dd0f11231c4cd8f4afc7838cc0b898e679de983b1c96d782c8f931b9` | 2026-09-12T14:44:05.884041494Z | `RELAY_ROUTING_CONTEXT_V2=true`；`RELAY_ROUTING_ORDERED=true`；`SUBSCRIPTION_ENTITLEMENTS_V2=true` |

## 存储证据

| ownership | 已记录的路由相关迁移 |
| --- | --- |
| oneapi_identity | `094_add_identity_routing_facts`、`095_create_routing_change_outbox`、`098_identity_ordered_token_groups` |
| oneapi_channel | `091_create_model_health_states`、`092_create_routing_groups`、`095_create_routing_change_outbox`、`100_channel_routing_relation_overrides` |
| oneapi_billing | `093_add_request_snapshots`、`095_create_routing_change_outbox`、`096_add_subscription_contracts`、`097_create_routing_billing_policies`、`099_billing_user_routing_price_overrides` |

2 个组均启用；channel 成员关系 3 条、account 成员关系 3 条，migration 来源用户授权 5 条；显式账务策略 1 条，模式为 subscription_first。预检时未完成的 v2 reservation 为 0。以上均是带时间点的汇总，不能替代每次发布前的重新核查。

## 方法与回退边界

通过 SSH 获取容器 inspect 的指定开关、镜像 ID 和启动状态；在所属 schema 上执行 SELECT。通过临时 SSH 端口转发运行 `scripts/routing-preflight`，仅调用只读 RPC，敏感 DSN 和服务令牌只存在于进程内存。没有调用会初始化迁移表的 `migrate -status`，没有执行回填 dry-run。

关闭 admin 创建开关只限制新增对象。现有 selected_groups 合约仍要求 v2 合约兼容代码；当前 fixed / ordered 数量为零，也不能据此关闭合同读取或退回忽略请求快照的旧二进制。
