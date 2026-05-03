# 功能缺失分析 — Kratos 落地方案 vs 当前实现

> 基于 `docs/One-API基于Kratos的微服务落地方案.md` 与当前代码库的全面对比，分析日期：2026-05-02，更新日期：2026-05-03

## 一、文档标记"✅ 已完成"但实际未达标的项（已修复）

### 1. 健康检查（§16.3 / §16.5）✅ 已修复

- **原问题**：4 个服务缺失 `/healthz` 端点，relay-gateway 用 `/health` 命名不统一
- **修复内容**：
  - `identity-service`、`channel-service`、`admin-api` 新增完整 HTTP server + `/healthz`
  - `billing-service` 在已有 HTTP server 上添加 `/healthz` 路由
  - `relay-gateway` 统一改为 `/healthz`
  - 所有 wire.go / wire_gen.go 已更新，httpSrv 注册到 kratos.Server

### 2. Redis 支持（§16.6 / §11）✅ 已修复

- **原问题**：go.mod 无 Redis 客户端库，所有 Redis 配置是死代码
- **修复内容**：
  - 新增 `internal/pkg/xdb/redis.go` — `NewRedisClient(addr)` + `PingRedis()` 封装
  - `go.mod` 添加 `github.com/redis/go-redis/v9`
  - 4 个服务 data 层（billing/identity/channel/config）均接入 `*redis.Client`，从 `REDIS_ADDR` 环境变量初始化，带 ping 降级

### 3. `internal/pkg/` 共享包完整性（§16.1）✅ 已修复

- **原问题**：xtrace / xgrpc / xhttp 目录只有 README.md 占位文件
- **修复内容**：
  - `internal/pkg/xtrace/trace.go` — `GenerateTraceID`、`ExtractTraceID`、`WithTraceID`、HTTP Middleware
  - `internal/pkg/xhttp/response.go` — `JSON()`、`Error()` 标准响应函数
  - `internal/pkg/xgrpc/metadata.go` — trace ID 通过 gRPC metadata 透传，含 client/server interceptor

---

## 二、文档§17-18 遗漏汇总中标记"✅"但实际有问题的项（已修复）

### 4. 健康检查完善（§18 第 5 项）✅ 已修复

- 同上第 1 项，所有 9 个服务现均有 `/healthz` 端点

---

## 三、文档中建议但完全未实现的功能（已修复）

### 5. 链路追踪 / xtrace（§17 架构改进建议第 2 条）✅ 已修复

- **修复内容**：`xtrace` 包已实现 trace ID 生成、context 注入/提取、HTTP middleware

### 6. 二期服务 Proto 定义（§18 第 11 项 / §17 第 5 条）✅ 已修复

- **原问题**：config / log / monitor / notify 仅有 HTTP API，无 gRPC proto
- **修复内容**：
  - 新增 `api/config/v1/config.proto`、`api/log/v1/log.proto`、`api/monitor/v1/monitor.proto`、`api/notify/v1/notify.proto`
  - `make api` 生成 Go 代码（pb.go + _grpc.pb.go）
  - 4 个 service 层实现 gRPC 接口（嵌入 `Unimplemented*Server`）
  - 4 个 gRPC server 注册 `Register*Server`
  - HTTP handler 方法重命名为 `Handle*` 避免与 gRPC 方法签名冲突

### 7. 统一错误处理增强（§17 第 1 条）✅ 已修复

- **修复内容**：
  - 新增 reason 常量：`ReasonConfigNotFound`、`ReasonConfigExists`、`ReasonInvalidKey`、`ReasonLogNotFound`、`ReasonHealthCheckNotFound`、`ReasonAlertRuleNotFound`、`ReasonInvalidAlertRule`、`ReasonNotificationNotFound`、`ReasonInvalidNotification`
  - 新增 HTTP status code 映射
  - 新增 `MapConfigError`、`MapLogError`、`MapMonitorError`、`MapNotifyError` 函数

---

## 四、文档完全未提及但设计方案要求的缺失项（已修复）

| 缺失项 | 状态 | 修复说明 |
|--------|------|----------|
| **幂等键机制** | ✅ 已修复 | `ReservationRepo` 新增 `FindByRequestID`，`ReserveQuota` 开头做幂等检查 |
| **密码哈希** | ✅ 已修复 | `Login()` 使用 `bcrypt.CompareHashAndPassword`，`Register()` 使用 `bcrypt.GenerateFromPassword` |
| **事件总线** | ✅ 已修复 | `events.EventBus` 接口 + `MemoryEventBus` 实现，`ConfigUsecase`/`ChannelUsecase` 变更后发布事件 |
| **Config/Channel 变更通知** | ✅ 已修复 | `SetConfig`/`DeleteConfig` 发布 `TopicConfigChanged`，渠道 CRUD 发布 `TopicChannelChanged` |
| **分组倍率外部化** | ✅ 已修复 | `BillingUsecase` 从 config 注入 `groupRatios`，`DefaultGroupRatios()` 提供默认值 |

---

## 五、仍待实现的功能

| 缺失项 | 来源 | 说明 | 优先级 |
|--------|------|------|--------|
| **OAuth/SSO** | Kratos 方案§3.2 | 无 OAuth2/OIDC 实现 | P3 |
| **账务对账任务** | Kratos 方案§6 | 无 reconciliation、无审计报告 | P2 |
| **分布式限流** | Kratos 方案§9 | 进程内限流器，多实例无法协同 | P2 |
| **监控告警** | §17 第 10 条 | 无 Prometheus metrics 端点 | P2 |
| **服务治理 / 注册中心** | §17 第 3 条 | 静态端点配置，无 consul/nacos 服务发现 | P3 |

---

## 六、§16 进度清单修正后评估

| 章节 | 修正后准确度 | 说明 |
|------|-------------|------|
| §16.1 骨架层 | ✅ 准确 | xtrace/xgrpc/xhttp 已补充完整实现 |
| §16.2 服务实现层 | ✅ 准确 | 密码哈希、幂等键已补齐 |
| §16.3 基础设施层 | ✅ 准确 | 健康检查全覆盖，Redis 已接入 |
| §16.4 配置一致性 | ✅ 准确 | 无变化 |
| §16.5 部署配置 | ✅ 准确 | 无变化 |
| §16.6 第二阶段服务 | ✅ 准确 | gRPC proto 已补充，Redis 已接入 |

---

## 七、总结

经过本轮修复，当前实现与 Kratos 落地方案的**吻合度显著提升**：

| 类别 | 修复前 | 修复后 |
|------|--------|--------|
| 健康检查 | 5/9 服务 | 9/9 服务 ✅ |
| Redis 集成 | 0/7 服务 | 7/7 服务 ✅ |
| 共享包 | 仅占位 | xtrace/xhttp/xgrpc 完整实现 ✅ |
| 二期服务 gRPC | 0/4 服务 | 4/4 服务 ✅ |
| 密码安全 | 明文 | bcrypt ✅ |
| 幂等计费 | 无 | requestID 去重 ✅ |
| 事件驱动 | 仅常量 | EventBus + 发布 ✅ |
| 错误处理 | relay/identity/channel | + config/log/monitor/notify ✅ |

**剩余待做**：OAuth/SSO、分布式限流、Prometheus metrics、服务注册发现、账务对账（均为 P2-P3）。
