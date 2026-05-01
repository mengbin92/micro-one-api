# One-API 基于 Kratos 的微服务落地方案

## 1. 结论

既然你计划用 `go-kratos` 做这次改造，整体方案应该收敛为：

1. 用 Kratos 承载服务化骨架。
2. 用 DDD 风格拆 `biz / data / service / server`。
3. 用 `protobuf + gRPC` 作为内部服务契约。
4. 用 HTTP 暴露管理端与兼容 OpenAI 的外部接口。
5. 用 `wire` 做依赖注入。
6. 用 `config`、`middleware`、`transport`、`log`、`registry` 这些 Kratos 标准能力统一全项目工程风格。

这次改造不建议做成“很多独立仓库”。更适合做成：

1. 单仓。
2. 多 Kratos 服务。
3. 共享 `api/`、`internal/pkg/`、`third_party/`。

这样既符合 `one-api` 当前体量，也能控制复杂度。

## 2. 为什么 Kratos 适合这个项目

`one-api` 的改造重点不是页面，而是：

1. 高并发 relay 链路。
2. 配额账务一致性。
3. 渠道选择与缓存。
4. 配置与后台管理。

Kratos 对这个场景比较合适，原因是：

1. 原生支持 `gRPC + HTTP` 双协议。
2. 服务分层固定，适合把当前混杂在 `controller/model/middleware` 里的逻辑重新归位。
3. `middleware` 体系适合做鉴权、限流、追踪、熔断、日志。
4. `wire` 适合把当前单体里到处散落的初始化逻辑收口。
5. `protobuf` 很适合作为服务间稳定契约。

## 3. Kratos 目标架构

## 3.1 服务划分

建议第一阶段落地这 5 个 Kratos 服务：

1. `relay-gateway`
2. `admin-api`
3. `identity-service`
4. `channel-service`
5. `billing-service`

第二阶段再补：

1. `config-service`
2. `log-service`
3. `monitor-worker`
4. `notify-worker`

## 3.2 职责分工

### `relay-gateway`

职责：

1. 对外提供 `/v1/*` OpenAI 兼容接口。
2. 接收客户端请求并解析模型、流式参数、媒体参数。
3. 调用 `identity-service` 获取鉴权快照。
4. 调用 `channel-service` 获取最佳渠道。
5. 调用 `billing-service` 完成预扣、提交、释放。
6. 执行上游模型适配与重试。

协议建议：

1. 对外 `HTTP`.
2. 对内调用 `gRPC`.

### `admin-api`

职责：

1. 对外提供后台管理接口。
2. 聚合用户、渠道、配置、日志查询。
3. 作为管理端 BFF。

协议建议：

1. 对外 `HTTP`.
2. 对内 `gRPC`.

### `identity-service`

职责：

1. 用户注册、登录、OAuth。
2. 用户状态、分组、Token 管理。
3. 输出给 Relay 的鉴权快照。

### `channel-service`

职责：

1. 渠道管理。
2. 模型能力矩阵管理。
3. 渠道路由与负载选择。
4. 渠道启停、优先级、模型映射。

### `billing-service`

职责：

1. 配额账户。
2. 预扣、结算、回补。
3. Token 配额和用户配额联动。
4. 充值、兑换码、邀请奖励。

## 4. 推荐仓库结构

建议直接按 Kratos 单仓多服务组织：

```text
micro-one-api/
  api/
    admin/v1/
    identity/v1/
    channel/v1/
    billing/v1/
    relay/v1/
  cmd/
    admin-api/
    billing-service/
    channel-service/
    identity-service/
    relay-gateway/
  configs/
    admin-api.yaml
    billing-service.yaml
    channel-service.yaml
    identity-service.yaml
    relay-gateway.yaml
  internal/
    admin/
      data/
      server/
      service/
    billing/
      biz/
      data/
      server/
      service/
    channel/
      biz/
      data/
      server/
      service/
    identity/
      biz/
      data/
      server/
      service/
    relay/
      biz/
      data/
      server/
      service/
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

这里的关键点是：

1. `api/` 放所有 proto。
2. `cmd/` 放各服务入口。
3. `internal/` 放服务实现与共享内部组件。
4. `internal/pkg/` 放共享组件，但只放真正稳定、无业务归属的公共能力。

## 5. Kratos 分层映射建议

`one-api` 当前目录和 Kratos 分层大致这样映射：

### 当前 `controller/`

迁移到：

1. `internal/service/`
   - 处理 transport 入参与出参。
2. 少量编排逻辑进入 `internal/biz/`

### 当前 `model/`

拆成两部分：

1. `internal/biz/`
   - 领域实体、领域规则、用例。
2. `internal/data/`
   - GORM/Redis/消息队列实现。

这是最关键的重构动作。

### 当前 `middleware/`

迁移到：

1. `server/http.go`
2. `server/grpc.go`
3. `internal/pkg/xhttp`
4. `internal/pkg/xgrpc`

### 当前 `relay/`

迁移到：

1. `relay-gateway/internal/biz/relay`
2. `relay-gateway/internal/data/provider`
3. `relay-gateway/internal/service`

### 当前 `monitor/`

迁移到：

1. `monitor-worker/internal/biz`
2. `monitor-worker/internal/data`

## 6. 每个 Kratos 服务内部结构建议

以 `billing-service` 为例：

```text
cmd/billing-service/
  main.go
  wire.go
  wire_gen.go
configs/
  billing-service.yaml
internal/billing/
  biz/
    account.go
    reservation.go
    ledger.go
    billing.go
    repo.go
  data/
    data.go
    account_repo.go
    reservation_repo.go
    ledger_repo.go
    redis.go
    gorm.go
  service/
    billing.go
  server/
    grpc.go
    http.go
```

建议约束：

1. `service` 不写业务规则。
2. `biz` 不直接依赖 transport。
3. `data` 不泄漏数据库模型到 `service` 层。

## 7. Proto 设计建议

Kratos 场景下，proto 要先行。

推荐 API 目录：

```text
api/
  identity/v1/identity.proto
  channel/v1/channel.proto
  billing/v1/billing.proto
  admin/v1/admin.proto
```

### `identity.proto`

至少包含：

1. `Login`
2. `Register`
3. `ValidateToken`
4. `GetAuthSnapshot`
5. `CreateAccessToken`
6. `ListUsers`
7. `ManageUser`

### `channel.proto`

至少包含：

1. `SelectChannel`
2. `GetChannel`
3. `ListChannels`
4. `CreateChannel`
5. `UpdateChannel`
6. `ChangeChannelStatus`
7. `ListAvailableModels`

### `billing.proto`

至少包含：

1. `ReserveQuota`
2. `CommitQuota`
3. `ReleaseQuota`
4. `TopUpQuota`
5. `RedeemCode`
6. `GetAccountSnapshot`

## 8. Relay 在 Kratos 中怎么实现

这里要特别说明：`relay-gateway` 不适合完全套 CRUD 风格。

它更像“协议网关 + 业务编排器”。

建议结构：

```text
internal/relay/
  biz/
    relay/
      usecase.go
      auth.go
      route.go
      billing.go
      retry.go
      stream.go
      model_mapping.go
  data/
    provider/
      openai/
      anthropic/
      gemini/
      ...
    client/
      identity.go
      channel.go
      billing.go
  service/
    openai_service.go
  server/
    http.go
```

这里的原则是：

1. `service` 只负责 HTTP 协议兼容。
2. 具体业务编排放 `biz/relay`.
3. 调其他服务统一经过 `data/client`.
4. 调上游大模型统一经过 `data/provider`.

## 9. 鉴权与中间件设计

Kratos 下建议统一两套中间件：

### HTTP 中间件

用于：

1. `request_id`
2. `trace_id`
3. 访问日志
4. CORS
5. panic recover
6. 统一错误转换

### gRPC 中间件

用于：

1. tracing
2. logging
3. metadata 透传
4. timeout
5. recovery

### 业务鉴权

不要把复杂业务鉴权全堆进 middleware。

建议：

1. 基础 token 解析放 middleware。
2. 用户状态、模型权限、额度权限在 `biz` 层处理。

否则会重演当前单体里 middleware 过重的问题。

## 10. 配置方案

Kratos 自带配置能力可以用，但要区分两类配置：

### 静态配置

适合放本地配置文件或环境变量：

1. 服务端口。
2. 数据库 DSN。
3. Redis 地址。
4. 注册中心地址。
5. 日志级别。

### 动态业务配置

不要直接塞进本地 yaml。

应该由 `config-service` 或数据库统一管理：

1. 模型倍率。
2. 分组倍率。
3. OAuth 开关。
4. 主题与公告。
5. 风控阈值。

建议做法：

1. Kratos 启动时加载静态配置。
2. 动态业务配置通过 `config-service` 或 Redis 缓存获取。
3. 配置变更通过事件通知各服务刷新本地缓存。

## 11. 数据访问层建议

Kratos 不限制 ORM，但结合当前项目，建议：

1. 短期继续用 `GORM`，降低迁移成本。
2. Redis 独立封装到 `data/redis.go`。
3. 各 repo 只返回领域对象，不直接向上层暴露 GORM model。

示例：

1. `UserRepo`
2. `TokenRepo`
3. `ChannelRepo`
4. `AbilityRepo`
5. `ReservationRepo`
6. `LedgerRepo`

## 12. 服务发现与部署建议

如果你准备上 K8s：

1. 服务发现直接走 K8s Service。
2. Kratos registry 可先不强依赖。

如果你先用 Docker Compose 做开发：

1. 内部服务地址先走静态配置。
2. 后续上 K8s 再切服务发现。

也就是说，Kratos 可以先只用它的：

1. transport
2. middleware
3. config
4. wire
5. log

不一定第一天就把 registry 全引进来。

## 13. 一阶段落地顺序

### 第一阶段：工程骨架

先在当前仓库落：

1. `api/` proto 目录。
2. `cmd/*` 服务入口目录。
3. `internal/identity`
4. `internal/channel`
5. `internal/billing`
6. `internal/admin`
7. `internal/relay`
8. `internal/pkg` 公共内部包。

### 第二阶段：先迁用户与渠道

先迁：

1. 用户鉴权。
2. Token 校验。
3. 渠道选择。

因为这是 Relay 拆出去的前置条件。

### 第三阶段：迁账务

再迁：

1. 预扣。
2. 回补。
3. 结算。
4. 消费流水。

### 第四阶段：迁后台 API

最后把：

1. 管理用户。
2. 管理渠道。
3. 管理选项。
4. 查日志。

从单体管理 API 迁进 `admin-api`。

## 14. 不建议这样做

### 不建议 1

直接把当前 `controller/model/common` 原封不动复制到 Kratos 目录里。

原因：

1. 这只是“换皮”，没有完成边界重构。

### 不建议 2

一开始就每个服务一个独立仓库。

原因：

1. 当前还处在强耦合重构期，拆仓只会放大协作成本。

### 不建议 3

把所有业务逻辑都写在 `service` 层。

原因：

1. 后面会再次变成新的单体式泥团。

### 不建议 4

把账务仍然分散在 `identity/channel/relay` 各服务里。

原因：

1. 这会直接导致配额一致性失控。

## 15. 针对你的项目的最终建议

如果你确定用 `go-kratos`，那最合适的路线就是：

1. 用 Kratos 重建项目骨架。
2. 先做单仓多服务。
3. 以 proto 契约优先。
4. 先拆 `identity/channel/billing/relay/admin` 五个核心服务。
5. 保持 `GORM + Redis`，不要同时引入过多新基础设施。

这条路线的优势是：

1. 工程结构会很快稳定。
2. 后续服务边界清晰。
3. 适合逐步把 `one-api` 旧逻辑搬迁过来。

---

## 附：建议下一步立即补的文档或产物

如果后面继续推进，最值得马上补的是：

1. `Kratos 单仓目录脚手架`
2. `proto 草案`
3. `billing-service 表结构设计`
4. `relay-gateway 调用链时序图`
