# One-API 微服务改造方案

## 1. 目标与结论

本方案基于 [one-api](https://github.com/songquanpeng/one-api) 的实际代码结构做分析，目标不是“把单体硬拆成很多进程”，而是把高并发路径、后台管理路径、配置路径和异步任务路径解耦，最终形成一套可灰度、可扩展、可观测的微服务架构。

先给结论：

1. `one-api` 当前是“强状态单体”而不是“纯转发网关”。
2. 直接一步拆成十几个服务，风险很高，尤其会破坏额度一致性、渠道缓存一致性和重试链路。
3. 最合理的路线是分三层推进：
   1. 先做模块化单体，抽公共领域层与事件层。
   2. 再优先拆高价值服务：`gateway/relay`、`identity`、`admin/config`、`billing`、`channel`、`log`。
   3. 最后拆通知、统计、渠道探测、报表等边缘服务。
4. 数据库要先按“逻辑库拆分”设计，再决定是否物理分库；不要一开始就上强分布式事务。

## 2. 现状分析

### 2.1 当前系统的单体特征

从 `../one-api/main.go` 可以看到，单个进程在启动时同时完成了这些职责：

1. 初始化主库和日志库。
2. 初始化 Redis。
3. 初始化配置项缓存和渠道缓存。
4. 启动定时同步任务。
5. 启动批量更新器。
6. 启动 HTTP API、Relay API、Dashboard、Web 静态站点。

也就是说，现在的一个二进制同时承担：

1. OpenAI 兼容 API 网关。
2. 用户认证与会话管理。
3. 后台管理接口。
4. 渠道管理与模型能力路由。
5. 配额预扣、结算、日志记录。
6. 渠道健康检查与自动熔断。
7. 前端托管。

### 2.2 代码层面的核心模块

按目录划分，当前单体大致可以拆成这些领域：

1. `router/`
   - HTTP 路由总装配。
   - `api`、`relay`、`dashboard`、`web` 全部耦合在一个进程。
2. `controller/`
   - 用户、令牌、渠道、日志、设置、OAuth、充值、管理 API。
3. `relay/`
   - 核心请求转发链路。
   - 适配几十种上游模型供应商。
4. `model/`
   - 用户、令牌、渠道、能力、配置、日志、兑换码。
   - 同时包含缓存逻辑、批量更新、额度扣减、业务规则。
5. `middleware/`
   - 鉴权、限流、渠道选择、压缩、日志、语言、异常恢复。
6. `monitor/`
   - 渠道成功率统计、自动禁用、通知。
7. `common/`
   - 配置、日志、Redis、消息、工具类。
8. `web/`
   - 管理与用户前端。

### 2.3 关键耦合点

#### 耦合点 A：Relay 链路直接依赖状态读写

`router/relay.go` 与 `middleware/distributor.go` 表明，一次 `/v1/...` 请求并不是简单代理，而是强依赖：

1. 用户身份。
2. 用户分组。
3. Token 状态与可用模型。
4. 用户额度。
5. 渠道可用性。
6. 渠道优先级与模型映射。
7. 重试时的替代渠道选择。

这意味着 `relay` 是系统核心，不是可以最后才考虑的边角模块。

#### 耦合点 B：渠道选择依赖内存缓存与数据库双路径

`model/cache.go` 中：

1. `InitChannelCache` 把 `group -> model -> channels` 放进进程内存。
2. `SyncChannelCache` 周期性从数据库重载。
3. `CacheGetRandomSatisfiedChannel` 优先从内存中挑选。

这类设计在单体里简单有效，但拆成多服务后会产生两个问题：

1. 多实例缓存不一致。
2. 管理端改渠道后，网关实例感知延迟取决于同步周期。

#### 耦合点 C：额度扣减是“预扣 + 回补 + 事后结算”

从 `relay/controller/helper.go`、`relay/controller/audio.go`、`model/token.go`、`relay/billing/billing.go` 可以看到当前额度模型：

1. 请求前先检查用户额度。
2. 先做 `PreConsume` 预扣。
3. 请求结束后再按真实 token 数量 `PostConsume`。
4. 失败时回补预扣额度。
5. 同时更新用户额度、Token 额度、渠道已用额度、日志。

这是一套完整的账务链路。拆分后如果没有统一账务服务，最容易出现：

1. 重复扣费。
2. 回补失败。
3. 用户额度和 Token 额度不一致。
4. 日志已写但账务未落，或者反过来。

#### 耦合点 D：配置不是纯静态配置，而是动态业务配置

`model/option.go` 说明当前配置项并不只是环境变量，还包含：

1. 登录方式开关。
2. 主题与系统展示项。
3. 配额阈值。
4. 模型倍率、分组倍率、补全倍率。
5. 通知渠道。
6. OAuth 参数。

这些配置被直接加载到进程内 `config.OptionMap`，并周期同步。拆分后必须有统一配置中心或配置服务。

#### 耦合点 E：管理 API 与网关共用同一数据模型

管理员修改渠道、模型、用户状态、选项时，当前就是直接改数据库。Relay 节点靠 Redis 或定时同步感知变更。这种模式适合“单体多实例”，不适合真正微服务。

### 2.4 当前数据模型分层

现在的核心表可以按领域重新理解：

1. 身份域
   - `users`
   - `tokens`
2. 渠道路由域
   - `channels`
   - `abilities`
3. 配置域
   - `options`
4. 账务与运营域
   - `redemptions`
   - 用户额度、Token 额度、渠道已用额度
5. 审计与观测域
   - `logs`

这个划分已经天然提示了未来微服务边界。

## 3. 微服务改造原则

### 3.1 先按领域拆，再按技术拆

不要拆成：

1. 一个认证服务。
2. 一个 Redis 服务包装层。
3. 一个数据库服务包装层。

应该拆成有明确业务边界的领域服务。

### 3.2 核心链路必须优先保证一致性和性能

最关键的不是“服务数量”，而是保证：

1. Relay 请求延迟不能明显恶化。
2. 额度扣减必须可审计。
3. 渠道状态更新要能快速生效。
4. 重试逻辑不能因为跨服务调用失控。

### 3.3 同步调用只放在强实时链路

适合同步的：

1. Token 鉴权。
2. 渠道选择。
3. 配额预扣授权。

适合异步的：

1. 消费日志。
2. 报表聚合。
3. 渠道健康统计。
4. 告警通知。
5. 审计事件归档。

### 3.4 优先避免分布式事务

建议采用：

1. 单服务内部本地事务。
2. 跨服务通过事件驱动和补偿。
3. 对账任务兜底。

不要在第一阶段引入复杂的 2PC / XA / Saga 编排平台。

## 4. 目标微服务架构

## 4.1 建议的服务拆分

### A. `api-gateway`

职责：

1. 统一外部入口。
2. 路由管理 API、用户 API、Relay API。
3. TLS、限流、黑白名单、请求追踪。
4. WebSocket/SSE 透传。

建议：

1. 可保留 Nginx/Envoy/Kong 作为北向网关。
2. 业务侧再保留一个 `relay-gateway` 负责 OpenAI 兼容协议处理。

### B. `relay-gateway`

职责：

1. 接收 `/v1/*` OpenAI 兼容请求。
2. 完成模型解析、上下文装配、重试编排。
3. 调用渠道路由服务获取上游渠道。
4. 调用账务服务做预扣与结算。
5. 调用 provider adaptor 执行真正转发。

说明：

1. 这是最高并发服务，必须无状态化。
2. 不能再直接读写业务表。

### C. `identity-service`

职责：

1. 用户注册、登录、密码重置、OAuth。
2. Session/JWT/管理访问令牌签发。
3. 用户状态校验。
4. Token 管理。

下沉数据：

1. `users`
2. `tokens`

### D. `channel-service`

职责：

1. 渠道 CRUD。
2. 能力矩阵维护。
3. 路由策略计算。
4. 渠道启停、优先级、模型映射。
5. 提供“按 group + model 选可用渠道”的接口。

下沉数据：

1. `channels`
2. `abilities`

### E. `billing-service`

职责：

1. 用户额度账户管理。
2. Token 额度账户管理。
3. 预扣、回补、结算。
4. 充值、兑换码、邀请奖励。
5. 对账、幂等、账单流水。

说明：

1. 这是最重要的新服务之一。
2. 当前 `users.quota`、`users.used_quota`、`tokens.remain_quota`、`tokens.used_quota` 的更新逻辑应统一收口到这里。

### F. `config-service`

职责：

1. 系统选项管理。
2. 动态业务配置发布。
3. 倍率配置管理。
4. 前端展示配置。

下沉数据：

1. `options`

### G. `log-service`

职责：

1. 消费日志写入。
2. 审计日志写入。
3. 查询聚合、统计报表。
4. 历史清理归档。

下沉数据：

1. `logs`

### H. `monitor-service`

职责：

1. 渠道成功率统计。
2. 自动禁用/恢复。
3. 告警通知。
4. 余额拉取、定时探测。

说明：

1. 这部分现在散落在 `monitor/` 与 `controller/channel*.go` 中。
2. 很适合拆成异步工作服务。

### I. `notification-service`

职责：

1. 邮件。
2. Message Pusher。
3. 告警消息模板。

这个服务可以后拆，不是第一阶段必需。

## 4.2 推荐部署拓扑

```text
Client
  |
[North Gateway: Nginx / Envoy / APISIX]
  |
  +-------------------------+
  |                         |
relay-gateway          console-bff / admin-api
  |                         |
  |                         +--> identity-service
  |                         +--> config-service
  |                         +--> channel-service
  |                         +--> billing-service
  |
  +--> identity-service
  +--> channel-service
  +--> billing-service
  +--> provider-runtime
  +--> event-bus

async workers:
  log-service
  monitor-service
  notification-service
```

## 4.3 推荐技术选型

### 服务通信

1. 外部 API：HTTP/JSON。
2. 内部高频调用：优先 gRPC。
3. 异步事件：Kafka、RabbitMQ 或 NATS JetStream。

建议：

1. 如果团队偏 Go 且追求简单，优先 `gRPC + NATS JetStream`。
2. 如果已有大数据消费链路，优先 `gRPC + Kafka`。

### 配置与注册

1. 配置中心：先用数据库 + Redis Pub/Sub 即可。
2. 服务发现：Kubernetes Service 即可，不必额外上注册中心。

### 存储

1. 主业务库：MySQL 或 PostgreSQL。
2. 缓存：Redis。
3. 日志检索：前期 MySQL 分表即可，后期可引入 ClickHouse/Elasticsearch。

## 5. 目标数据架构

### 5.1 建议的领域库拆分

#### `identity_db`

1. `users`
2. `tokens`
3. OAuth 绑定表，可从 `users` 中拆出

#### `channel_db`

1. `channels`
2. `abilities`
3. 渠道探测记录表

#### `billing_db`

新增核心表：

1. `accounts`
   - 用户主账户余额。
2. `token_accounts`
   - Token 维度余额。
3. `ledger_entries`
   - 账务流水。
4. `quota_reservations`
   - 预扣记录。
5. `billing_orders`
   - 充值/兑换/奖励单据。
6. `redemptions`

说明：

1. 不建议继续只靠 `users.quota` 和 `tokens.remain_quota` 表达完整账务。
2. 需要显式的流水与预占记录，支持幂等和对账。

#### `config_db`

1. `options`
2. 可新增 `config_revisions`

#### `log_db`

1. `logs`
2. `request_events`
3. `daily_stats`

### 5.2 必须新增的关键字段

为了支持微服务和幂等，建议新增：

1. `request_id`
2. `trace_id`
3. `idempotency_key`
4. `version`
5. `updated_at`
6. `deleted_at`

尤其是账务相关表必须有：

1. 唯一业务单号。
2. 状态机字段。
3. 幂等约束。

## 6. 核心服务设计

### 6.1 `relay-gateway` 设计

请求主链路建议改为：

1. 验证调用 Token。
2. 获取用户和 Token 的授权快照。
3. 校验模型权限、IP/Subnet、用户状态。
4. 向 `channel-service` 请求最佳渠道。
5. 向 `billing-service` 发起 `ReserveQuota` 预扣。
6. 调用 provider adaptor 发起上游请求。
7. 成功后向 `billing-service` 发起 `CommitUsage`。
8. 失败则发起 `ReleaseQuota`。
9. 异步投递 `UsageRecorded`、`RelayFailed`、`ChannelMetric` 事件。

#### Relay 内部建议保留的模块

1. OpenAI 兼容协议层。
2. 上游适配器层。
3. 重试编排器。
4. 流式响应处理。

#### Relay 内部不应继续保留的模块

1. 直接数据库访问。
2. 直接修改用户/Token 额度。
3. 直接维护渠道缓存。
4. 直接写运营日志。

### 6.2 `channel-service` 设计

核心接口建议：

1. `SelectChannel(group, model, constraints) -> channel_snapshot`
2. `GetAvailableModels(group) -> models`
3. `UpdateChannelStatus(channel_id, status)`
4. `RefreshRouteCache(channel_id|group|model)`

内部实现建议：

1. DB 为真源。
2. Redis 为热缓存。
3. 管理端更新渠道后发布 `ChannelChanged` 事件。
4. 所有 `relay-gateway` 实例订阅事件并本地刷新。

相比当前 `SYNC_FREQUENCY` 轮询模式，这会更实时。

### 6.3 `billing-service` 设计

这是改造成败关键，建议明确三类动作：

1. `ReserveQuota`
   - 按请求做预扣。
   - 生成 `reservation_id`。
2. `CommitQuota`
   - 用实际 token 用量结算。
   - 释放多扣部分或补扣差额。
3. `ReleaseQuota`
   - 请求失败后完全回补。

#### 幂等要求

1. `ReserveQuota(request_id)` 幂等。
2. `CommitQuota(request_id)` 幂等。
3. `ReleaseQuota(request_id)` 幂等。

#### 账务模型建议

1. 用户账户与 Token 账户分开维护。
2. 预扣记录必须可查询。
3. 结算后落流水。
4. 支持异步补偿与对账。

### 6.4 `identity-service` 设计

建议输出两类能力：

1. 面向网关的高频鉴权接口。
2. 面向管理后台的用户与 Token 管理接口。

高频鉴权建议返回授权快照：

1. `user_id`
2. `group`
3. `user_status`
4. `token_id`
5. `token_status`
6. `allowed_models`
7. `subnet_policy`

避免 Relay 为一次请求再拆很多下游调用。

### 6.5 `config-service` 设计

把现在 `OptionMap + 定时同步` 改为：

1. 配置存储。
2. 配置版本号。
3. 配置变更事件。
4. 订阅式本地缓存。

配置可分层：

1. 安全配置。
2. 业务倍率配置。
3. 前端展示配置。
4. 第三方 OAuth 配置。

### 6.6 `log-service` 与 `monitor-service`

建议事件化：

1. Relay 成功 -> `UsageCommitted`
2. Relay 失败 -> `RelayFailed`
3. Channel 请求结果 -> `ChannelObserved`
4. Admin 修改配置 -> `ConfigChanged`

由 `log-service` 和 `monitor-service` 各自消费，而不是在主链路里同步写库。

## 7. 服务边界与接口建议

### 7.1 同步接口

#### `identity-service`

1. `ValidateAccessToken`
2. `GetAuthSnapshot`
3. `CreateUser`
4. `Login`
5. `ManageToken`

#### `channel-service`

1. `SelectChannel`
2. `ListModelsByGroup`
3. `GetChannel`
4. `ChangeChannelStatus`

#### `billing-service`

1. `ReserveQuota`
2. `CommitQuota`
3. `ReleaseQuota`
4. `TopUp`
5. `Redeem`

#### `config-service`

1. `GetRuntimeConfig`
2. `GetPricingConfig`
3. `UpdateOption`

### 7.2 异步事件

建议定义主题：

1. `identity.user.changed`
2. `identity.token.changed`
3. `channel.changed`
4. `channel.status.changed`
5. `billing.quota.reserved`
6. `billing.quota.committed`
7. `billing.quota.released`
8. `relay.request.finished`
9. `relay.request.failed`
10. `config.changed`

## 8. 建议迁移路径

## 8.1 Phase 0：先做单体模块化

目标：

1. 不改业务行为。
2. 为后续拆服务做代码准备。

要做的事：

1. 从 `model/` 中剥离“领域模型”和“基础设施实现”。
2. 定义清晰接口：
   - `UserRepository`
   - `TokenRepository`
   - `ChannelRepository`
   - `BillingRepository`
   - `OptionRepository`
3. 把额度逻辑从 `model/token.go`、`model/user.go` 抽成独立 `billing` 领域模块。
4. 把渠道选择从 `middleware/distributor.go` 抽成 `routing` 领域服务。
5. 把当前日志写入改成事件接口，即使底层暂时仍写本地 DB。

产出：

1. 模块化单体。
2. 内部事件总线接口。
3. 领域服务接口层。

这是必须先做的一步，否则后续每拆一个服务都会重复重构。

## 8.2 Phase 1：先拆前后台与主链路

优先拆分顺序建议：

1. `relay-gateway`
2. `admin-api` / `console-bff`
3. `identity-service`
4. `channel-service`

原因：

1. 先把最重流量的 `/v1/*` 路径与后台管理流量分离。
2. 这样即使账务暂时仍共库，也已经获得了最直接的扩展收益。

这一阶段允许：

1. 多服务共用同一个 MySQL。
2. 通过代码契约隔离，而不是立刻物理分库。

## 8.3 Phase 2：拆账务服务

这是最难阶段。

要做的事：

1. 建立 `ledger_entries`、`quota_reservations` 等新表。
2. 把预扣、回补、结算统一收口。
3. 让 `relay-gateway` 不再直接动用户与 Token 额度字段。
4. 引入幂等键和对账任务。

验收标准：

1. 任意失败重试不会重复扣费。
2. 同一个 `request_id` 多次提交不会产生重复流水。
3. 可按日对账用户、Token、渠道消耗。

## 8.4 Phase 3：日志、监控、通知异步化

要做的事：

1. 把消费日志改为异步事件落库。
2. 把渠道健康统计与自动禁用独立出去。
3. 把邮件、站外通知抽成消息消费服务。

收益：

1. 主链路延迟下降。
2. 告警能力增强。
3. 可独立扩容报表与监控任务。

## 8.5 Phase 4：物理分库与平台化

可选动作：

1. `identity_db`、`channel_db`、`billing_db`、`log_db` 分库。
2. 上 K8s。
3. 引入 Service Mesh、OpenTelemetry、APM。

这一步必须在业务稳定后再做，不建议前置。

## 9. 代码仓库改造建议

推荐目标仓库结构：

```text
micro-one-api/
  api/
  cmd/
    relay-gateway/
    admin-api/
    identity-service/
    channel-service/
    billing-service/
  configs/
    relay-gateway.yaml
    admin-api.yaml
    identity-service.yaml
    channel-service.yaml
    billing-service.yaml
  internal/
    identity/
    channel/
    billing/
    config/
    relay/
    logsvc/
    pkg/
      auth/
      errors/
      events/
      xgrpc/
      xhttp/
      xtrace/
  third_party/
    google/
    validate/
  deployments/
    docker-compose/
    k8s/
  docs/
```

这也是当前仓库已经采用的方向：单仓、多服务、共享 `api/` 和 `internal/pkg/`，更符合当前代码体量和团队维护成本。

## 10. 技术实施清单

### 10.1 第一批必须补齐

1. 全链路 `trace_id` / `request_id`。
2. 统一错误码。
3. 统一幂等键。
4. 统一审计日志格式。
5. 统一配置版本号。
6. 健康检查、就绪检查。

### 10.2 第一批基础设施

1. MySQL
2. Redis
3. 消息总线
4. Prometheus
5. Grafana
6. Loki 或 ELK

### 10.3 第一批 SLO

1. `relay-gateway` P95 延迟。
2. 鉴权失败率。
3. 渠道选择失败率。
4. 预扣失败率。
5. 结算幂等冲突率。
6. 渠道自动禁用次数。

## 11. 风险与应对

### 风险 1：拆分后延迟升高

原因：

1. 同步 RPC 过多。

应对：

1. Relay 获取“授权快照”而不是多次调用。
2. 渠道与配置都做本地缓存。
3. 高并发接口优先 gRPC。

### 风险 2：额度不一致

原因：

1. 预扣、回补、结算跨服务。

应对：

1. 建立独立账务流水。
2. 所有账务接口幂等。
3. 每日自动对账。

### 风险 3：渠道状态变更传播慢

原因：

1. 仍然依赖轮询。

应对：

1. 改成事件广播 + 本地缓存失效。

### 风险 4：服务数量过多，运维复杂

应对：

1. 第一阶段只拆 4 到 6 个核心服务。
2. 边缘能力仍以 worker 方式运行。

## 12. 推荐的最小可行改造方案

如果你的目标是“尽快把项目做成微服务形态，同时控制风险”，我建议采用以下最小闭环：

### 第一步

把当前单体改成单仓多服务，先产出：

1. `relay-gateway`
2. `admin-api`
3. `identity-service`
4. `channel-service`
5. `billing-service`

### 第二步

先共用一个 MySQL 实例，但分表归属与代码边界严格隔离。

### 第三步

引入 Redis 和消息总线，把：

1. 配置变更
2. 渠道变更
3. 日志写入
4. 监控统计

从同步逻辑中拿出去。

### 第四步

账务落独立流水模型，完成幂等化后，再考虑物理分库。

## 13. 最终建议

这个项目最适合的目标形态不是“泛化的企业级微服务平台”，而是“以 Relay 为核心的高性能 AI API 中台”。

因此架构重点应该放在：

1. Relay 无状态化。
2. 渠道路由中心化。
3. 账务结算强一致化。
4. 日志监控异步化。
5. 后台与主链路彻底隔离。

不建议一开始就追求过度拆分。按当前 `one-api` 的实现特点，最稳妥也最现实的方案是：

1. 先模块化单体。
2. 再拆核心 5 个服务。
3. 最后再做分库和平台化。

---

## 附：建议优先重构的原始代码区域

如果马上要动手，优先从这些文件开始抽象：

1. `../one-api/main.go`
2. `../one-api/router/api.go`
3. `../one-api/router/relay.go`
4. `../one-api/middleware/distributor.go`
5. `../one-api/controller/relay.go`
6. `../one-api/relay/controller/helper.go`
7. `../one-api/model/cache.go`
8. `../one-api/model/token.go`
9. `../one-api/model/user.go`
10. `../one-api/model/channel.go`
11. `../one-api/model/option.go`
12. `../one-api/model/log.go`
13. `../one-api/monitor/channel.go`

这几块基本覆盖了：

1. 请求入口。
2. 路由分发。
3. 额度模型。
4. 缓存模型。
5. 配置模型。
6. 监控模型。

后续如果你需要，我可以继续直接在当前仓库里补下一份：

1. `微服务拆分后的目录脚手架设计`
2. `服务间 API / gRPC proto 草案`
3. `数据库拆分与表结构设计`
4. `第一阶段实施任务清单`
