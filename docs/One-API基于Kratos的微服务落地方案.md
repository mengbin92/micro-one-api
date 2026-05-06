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

## 16. 迁移进度核查清单（截至 2026/05/06 更新）

### 16.1 骨架层 — ✅ 已完成

| 构件 | 状态 | 备注 |
|------|------|------|
| `cmd/` 9 个服务入口 | ✅ | 5 个一期服务 + 4 个二期服务（config / log / monitor / notify） |
| `internal/` DDD 目录 | ✅ | biz / data / service / server 四层均已创建 |
| `api/` proto 定义 | ✅ | 6 个 .proto 文件均已生成（identity / channel / billing / admin / relay / common） |
| `configs/` 配置文件 | ✅ | 9 个 .yaml 文件均已创建，支持环境变量覆盖 |
| `internal/pkg/` 共享包 | ✅ | auth / errors / events / grpc / middleware / model / logger / timeout / tls / validation / xdb / xgrpc / xhttp / xtrace（已集成 OpenTelemetry + Jaeger） |
| `third_party/` | ✅ | google / validate proto 依赖 |
| `migrations/billing/` | ✅ | 6 个 SQL 迁移文件 |
| `deployments/` | ✅ | Docker / K8s 部署文件（含二期服务） |
| 旧 one-api 目录清理 | ✅ | controller/ / model/ / middleware/ / relay/ / monitor/ / web/ 已移除 |

### 16.2 服务实现层 — ✅ 全部完成

#### 16.2.1 `identity-service` — ✅ 已完成

| 计划 RPC | 当前状态 |
|----------|----------|
| `ValidateToken` | ✅ 已实现 |
| `GetAuthSnapshot` | ✅ 已实现 |
| `Login` | ✅ 已实现 |
| `Register` | ✅ 已实现 |
| `CreateAccessToken` | ✅ 已实现 |
| `ListUsers` | ✅ 已实现 |
| `ManageUser` (CreateUser, UpdateUser, DeleteUser) | ✅ 已实现 |

#### 16.2.2 `channel-service` — ✅ 已完成

| 计划 RPC | 当前状态 |
|----------|----------|
| `SelectChannel` | ✅ 已实现 |
| `GetChannel` | ✅ 已实现 |
| `ListAvailableModels` | ✅ 已实现 |
| `ListChannels` | ✅ 已实现 |
| `CreateChannel` | ✅ 已实现 |
| `UpdateChannel` | ✅ 已实现 |
| `ChangeChannelStatus` | ✅ 已实现 |

#### 16.2.3 `billing-service` — ✅ 完整实现

- 14 个 RPC 均已实现（ReserveQuota / CommitQuota / ReleaseQuota / GetAccountSnapshot / TopUpQuota / CreateRedeemCode / CreateRedeemCodesBatch / RedeemCode / GetRedeemCode / ListRedeemCodes / SearchRedeemCodes / UpdateRedeemCode / DeleteRedeemCode / ListLedger）
- ✅ **proto 文件无重复定义**，编译正常，196 行清晰结构
- ✅ **对账定时调度** — `ReconciliationJob` 每小时自动执行（清理过期 reservation + 账户 quota 一致性检查）
- ✅ **对账任务** — `ReconciliationUsecase.RunReconciliation()` 已实现，包含过期清理和账户不一致检测

#### 16.2.4 `admin-api` — ✅ 已承担管理端 BFF 职责

| 计划职责 | 当前状态 |
|----------|----------|
| 聚合用户管理 | ✅ 已实现（ListUsers / CreateUser / UpdateUser / DeleteUser / ResetUserQuota） |
| 聚合渠道管理 | ✅ 已实现（ListChannels / CreateChannel / UpdateChannel / DeleteChannel / ChangeChannelStatus） |
| 聚合选项管理 | ✅ 已实现（GetSystemOptions / UpdateSystemOptions） |
| 账务管理 | ✅ 已实现（复用 billing-service 所有 RPC） |
| 日志查询 | ✅ 已实现（ListLogs 透传 billing ledger，支持 user_id / type / start_time / end_time 过滤） |
| 充值 / 兑换码 | ✅ 已实现（复用 billing.proto） |

当前 `admin-api` 已经完整承担了管理端 BFF 的职责，集成了 identity、channel、billing 三个服务的客户端。

#### 16.2.5 `relay-gateway` — ✅ 核心功能完整

| 计划内容 | 当前状态 |
|----------|----------|
| `RelayService` proto 契约 | ✅ 已实现 — `internal/relay/service/relay.go` 实现 ChatCompletion + ListModels，`server/grpc.go` 注册服务 |
| OpenAI 兼容 HTTP 接口 | ✅ `service/openai.go` 完整实现 |
| 渠道选择调用 `channel-service` | ✅ 已集成（SelectChannel / ListAvailableModels） |
| 账务预扣/提交/释放调用 `billing-service` | ✅ 已集成（ReserveQuota / CommitQuota / ReleaseQuota） |
| 鉴权调用 `identity-service` | ✅ 已集成（GetAuthSnapshot） |
| `data/client/` 服务间调用封装 | ✅ 已存在（`internal/relay/data/` 包含 gRPC client + 适配器） |
| `data/provider/` 上游模型适配 | ✅ 已存在（internal/relay/provider/ 包含 factory.go / provider.go / anthropic.go / stream_test.go） |
| Anthropic Provider | ✅ 已实现 — 支持 Claude API 格式转换（请求/响应/流式），x-api-key 认证 |
| 重试策略 `biz/retry.go` | ✅ 已实现 — `RetryPolicy` + `RetryExecutor`，指数退避 + channel fallback |
| 流式处理 `biz/stream.go` | ✅ 已存在（在 provider 中实现） |
| 模型映射 `biz/model_mapping.go` | ✅ 已实现 — 支持 YAML/JSON 格式，已接入 `RelayUsecase.Plan()` |
| Biz 层编排 `biz/relay.go` | ✅ 已增强 — `RelayUsecase.Plan()` 编排模型映射→鉴权→权限校验→渠道选择 |
| gRPC 适配器 `data/adapters.go` | ✅ 已实现 — `IdentityAdapter` / `ChannelAdapter` 桥接 gRPC 客户端与 biz 接口 |

### 16.3 基础设施层 — ✅ 已完成

| 项目 | 计划 | 当前状态 |
|------|------|----------|
| 依赖注入 | wire | ✅ 9 个服务均有 `wire.go` + `wire_gen.go`，签名一致，`//go:build` 标签正确 |
| 配置加载 | Kratos `config` 包加载 YAML | ✅ 所有服务均使用 Kratos config 包，支持环境变量覆盖 |
| 服务健康检查 | Kratos health | ✅ 已实现（所有服务均有 `/healthz` 端点） |
| TLS 配置 | Kratos TLS | ✅ 已实现（internal/pkg/tls/） |
| 服务认证 | JWT / mTLS | ✅ 已实现（internal/pkg/auth/） |
| 第二阶段服务 | config-service / log-service / monitor-worker / notify-worker | ✅ 已创建并实现 HTTP API |
| 链路追踪 | OpenTelemetry + Jaeger | ✅ 已集成 — xtrace 包支持 OTLP HTTP exporter，可配置采样率 |
| 对账调度 | 定时任务 | ✅ 已实现 — billing-service 内置 ReconciliationJob（每小时） |

### 16.4 配置一致性 — ✅ 已修复

| 服务 | configs/*.yaml 端口 | 实际运行端口 | 状态 |
|------|---------------------|---------------|------|
| relay-gateway | http=:3000, grpc=:9003 | http=:3000, grpc=:9003 | ✅ 一致 |
| identity-service | http=:8001, grpc=:9001 | http=:8001, grpc=:9001 | ✅ 一致 |
| channel-service | http=:8002, grpc=:9002 | http=:8002, grpc=:9002 | ✅ 一致 |
| billing-service | http=:8004, grpc=:9004 | http=:8004, grpc=:9004 | ✅ 一致 |
| admin-api | http=:8000, grpc=:9000 | http=:8000, grpc=:9000 | ✅ 一致 |
| config-service | http=:8005, grpc=:9005 | http=:8005, grpc=:9005 | ✅ 一致 |
| log-service | http=:8006, grpc=:9006 | http=:8006, grpc=:9006 | ✅ 一致 |
| monitor-worker | http=:8007, grpc=:9007 | http=:8007, grpc=:9007 | ✅ 一致 |
| notify-worker | http=:8008, grpc=:9008 | http=:8008, grpc=:9008 | ✅ 一致 |

所有服务配置文件与实际运行端口完全一致，且支持环境变量覆盖（如 `${DATABASE_DSN:-default}` 语法）。

### 16.5 部署配置 — ✅ 已完成

| 项目 | 当前状态 |
|------|----------|
| Dockerfile | ✅ 已创建（deployments/docker/Dockerfile），通用 Dockerfile 支持构建任意服务 |
| docker-compose.yml | ✅ 已完成 — 9 服务 + MySQL + Redis，包含一期和二期所有服务 |
| K8s deployment | ✅ 已完成 — 一期 5 服务独立 manifest + 二期 4 服务 `phase2-services.yaml` |
| K8s Ingress | ✅ 已完成 — relay-gateway + admin-api（`deployments/k8s/ingress.yaml`） |
| 服务发现 | ✅ gRPC 直连，配置文件指定端点 |
| 网络隔离 | ✅ docker-compose 中 backend 网络配置 |

### 16.6 第二阶段服务 — ✅ 已创建并实现

| 服务 | HTTP API | 当前状态 |
|------|----------|----------|
| `config-service` | GET/PUT/DELETE `/v1/configs/{namespace}/{key}` | ✅ 已实现（动态业务配置管理） |
| `log-service` | GET/POST `/v1/logs`，GET `/v1/logs/{id}` | ✅ 已实现（日志聚合服务） |
| `monitor-worker` | GET/POST `/v1/health-checks`，GET/POST `/v1/alert-rules` | ✅ 已实现（监控与告警） |
| `notify-worker` | GET/POST `/v1/notifications`，GET `/v1/notifications/{id}` | ✅ 已实现（通知 worker） |

所有二期服务均遵循 Kratos DDD 分层架构（biz / data / service / server / config），支持 MySQL + Redis，具备完整的 HTTP API 端点。

---

## 17. 遗漏汇总与优先级建议

### 高优先级（P0） - 阻塞生产可用性 — ✅ 已全部完成

1. ~~**docker-compose 完善**~~ — ✅ 已完成 — 9 服务 + MySQL + Redis
2. ~~**relay 重试策略**~~ — ✅ 已完成 — biz 层 `RetryPolicy` + `RetryExecutor`，指数退避 + channel fallback
3. ~~**模型映射配置**~~ — ✅ 已完成 — `configs/models.yaml`（YAML 格式）+ `internal/relay/biz/model_mapping.go`

### 中优先级（P1） - 提升可维护性 — ✅ 已全部完成

4. ~~**真正接入 wire 依赖注入**~~ — ✅ 已完成 — 9 个服务均有 `wire.go` + `wire_gen.go`，签名一致
5. ~~**K8s 部署完善**~~ — ✅ 已完成 — 一期 5 服务 + 二期 4 服务 K8s 资源 + Ingress
6. ~~**日志查询功能**~~ — ✅ 已完成 — admin-api `ListLogs` 支持 user_id / type / start_time / end_time 过滤
7. ~~**系统配置持久化**~~ — ✅ 已完成 — `system_options` 表 + DB 持久化

### 低优先级（P2） - 扩展功能

8. ~~**第二阶段服务**~~ — ✅ 已完成 — 4 个服务已创建并实现 HTTP API
9. **性能优化** — 缓存策略、连接池配置、限流细化
10. ~~**监控告警**~~ — ✅ 已完成 — 9/9 服务 `/metrics` 端点，Prometheus 指标（HTTP/gRPC 请求计数、延迟、活跃连接）

### 架构改进建议（可选）

1. **统一错误处理** — 建议在 internal/pkg/errors/ 中进一步完善错误码体系
2. ~~**链路追踪**~~ — ✅ 已完成 — OpenTelemetry + Jaeger OTLP HTTP 集成，支持采样率配置
3. **服务治理** — 可考虑接入 consul/nacos 等注册中心，替代静态端点配置
4. **API 文档** — 可考虑接入 swagger/OpenAPI 自动生成 API 文档
5. ~~**gRPC Proto 定义**~~ — ✅ 已完成 — 所有服务均有完整 proto 定义 + gRPC server 注册（含 relay-gateway）

### Proto 补全与 gRPC 对齐 — ✅ 已完成

- ~~relay-gateway gRPC server 空壳~~ — ✅ 已实现 `RelayGrpcService`（ChatCompletion + ListModels）
- ~~monitor proto 缺 RPC~~ — ✅ 已补全 GetAlertRule / UpdateAlertRule / DeleteAlertRule
- ~~monitor HTTP 缺路由~~ — ✅ 已补全 GET/PUT/DELETE `/v1/alert-rules/{id}`

### 安全修复（P0）— ✅ 已完成

- ~~Token 生成使用 `time.Now().UnixNano()`~~ — ✅ 已修复为 `crypto/rand`，熵从 ~10bit 提升到 ~190bit
- ~~Admin-api gRPC 服务器空壳~~ — ✅ 已实现 `NewGRPCServer()` 注册 AdminServiceServer
- ~~Admin-api HTTP 路由缺失~~ — ✅ 已注册 /v1/users, /v1/channels, /v1/logs 等路由
- ~~Event Bus 仅内存实现~~ — ✅ 已添加 Redis Pub/Sub 实现（`internal/pkg/events/redis.go`）

### 多模型支持（P2）— ✅ 已完成

- ~~Anthropic Provider~~ — ✅ 已实现（`internal/relay/provider/anthropic.go`），支持 Claude API 格式转换
- ~~对账定时调度~~ — ✅ 已实现（`internal/billing/biz/reconciliation_job.go`），每小时自动执行
- ~~二期服务集成测试~~ — ✅ 已实现（`test/integration/phase2_test.go`），覆盖 config/log/monitor/notify 四个服务

---

## 18. 建议下一步立即补的文档或产物

### 生产就绪相关 — ✅ 已全部完成

1. ~~**docker-compose 完善**~~ — ✅ 已完成
2. ~~**relay 重试策略**~~ — ✅ 已完成
3. ~~**模型映射配置**~~ — ✅ 已完成

### 部署运维相关

4. ~~**K8s 部署完善**~~ — ✅ 已完成
5. ~~**健康检查完善**~~ — ✅ 已完成 — 所有 9 个服务均有健康检查端点
6. ~~**部署文档**~~ — ✅ 已完成 — `docs/deployment.md` 含 Docker Compose / K8s / 环境变量 / 健康检查 / 故障排查

### 功能完善相关 — ✅ 已全部完成

7. ~~**日志查询功能**~~ — ✅ 已完成
8. ~~**系统配置持久化**~~ — ✅ 已完成
9. ~~**真正接入 wire**~~ — ✅ 已完成
10. ~~**第二阶段服务**~~ — ✅ 已完成

### 后续迭代建议

11. ~~**二期服务 Proto 定义**~~ — ✅ 已完成 — config/log/monitor/notify/relay 均有完整 proto 定义 + gRPC server 注册
12. ~~**二期服务单元测试**~~ — ✅ 已完成 — 4 个服务 biz + data 层单元测试
13. ~~**集成测试增强**~~ — ✅ 已完成 — `test/integration/phase2_test.go` 覆盖 config/log/monitor/notify 四个服务端到端测试
14. **性能优化** — 缓存策略、连接池配置、限流细化
15. ~~**监控告警**~~ — ✅ 已完成 — Prometheus metrics + Grafana 指标
16. ~~**链路追踪集成**~~ — ✅ 已完成 — OpenTelemetry + Jaeger（`internal/pkg/xtrace/trace.go`）
17. ~~**对账定时调度**~~ — ✅ 已完成 — ReconciliationJob 每小时自动执行（`internal/billing/biz/reconciliation_job.go`）
18. ~~**Anthropic Provider**~~ — ✅ 已完成 — Claude API 格式转换（`internal/relay/provider/anthropic.go`）
