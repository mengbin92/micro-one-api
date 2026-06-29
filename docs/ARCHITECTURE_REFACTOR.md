# micro-one-api 架构重构方案

> 版本: v1.0 | 日期: 2026-06-28 | 作者: Backend Architect

---

## 目录

1. [现状分析](#1-现状分析)
2. [架构痛点诊断](#2-架构痛点诊断)
3. [目标架构设计](#3-目标架构设计)
4. [核心重构方案](#4-核心重构方案)
5. [数据库优化方案](#5-数据库优化方案)
6. [容错与降级策略](#6-容错与降级策略)
7. [缓存架构设计](#7-缓存架构设计)
8. [可观测性增强](#8-可观测性增强)
9. [安全加固方案](#9-安全加固方案)
10. [迁移路径与优先级](#10-迁移路径与优先级)
11. [预期收益](#11-预期收益)

---

## 1. 现状分析

### 1.1 项目概述

micro-one-api 是基于 one-api、new-api、sub2api 三个开源项目融合重构的 **AI API 网关与管理系统**，采用 Go + Kratos v2 微服务框架，已拆分为 9 个独立服务。

### 1.2 技术栈

| 层次 | 技术 | 版本 |
|------|------|------|
| 语言 | Go | 1.26 |
| 微服务框架 | go-kratos/kratos/v2 | v2.9.2 |
| 依赖注入 | google/wire | v0.7.0 |
| ORM | gorm (MySQL) | v1.30.0 |
| 缓存 | go-redis/v9 | v9.19.0 |
| 通信协议 | gRPC + HTTP REST | - |
| JSON 序列化 | bytedance/sonic | v1.15.1 |
| 日志 | zap | v1.28.0 |
| 链路追踪 | OpenTelemetry | v1.43.0 |
| 监控 | prometheus | v1.23.2 |
| 服务发现 | Consul (可选) | v1.34.2 |

### 1.3 服务拓扑

```
                    ┌──────────────────┐
                    │  Web 管理后台     │
                    │ (React/Vite/TS)  │
                    └────────┬─────────┘
                             │ HTTP
                    ┌────────▼─────────┐
                    │    admin-api     │ (BFF: 8000/9000)
                    └──┬───┬───┬───────┘
                       │   │   │ gRPC
          ┌────────────┘   │   └────────────┐
          ▼                ▼                ▼
   ┌─────────────┐ ┌─────────────┐ ┌──────────────┐
   │ identity-svc│ │ channel-svc │ │ billing-svc  │
   │   (9001)    │ │   (9002)    │ │   (9004)     │
   └──────┬──────┘ └──────┬──────┘ └──────┬───────┘
          │               │               │
          └───────┬───────┴───────────────┘
                  │ gRPC
          ┌───────▼───────────────────────┐
          │       relay-gateway           │ (OpenAI 兼容: 8080/9003)
          │  鉴权 → 选路 → 预扣 → 转发 → 结算  │
          └───────┬───────────────────────┘
                  │
     ┌────────────┼────────────┐
     ▼            ▼            ▼
  log-svc    config-svc    monitor/notify
  (9006)        (-)        worker (-)
```

### 1.4 核心业务流

**请求处理链 (relay-gateway)**:
```
HTTP Request → Auth(identity) → ModelMapping → ChannelSelect(channel)
  → ReserveQuota(billing) → Provider/Adaptor Forward → Stream/NonStream Response
  → CommitQuota(billing) → Log(log) → HTTP Response
```

---

## 2. 架构痛点诊断

### 2.1 P0 - 严重问题（影响可用性和可维护性）

#### P0-1: God Object — http.go (2,391 行 / 77KB)

**问题**: `internal/relay/server/http.go` 是一个巨型文件，单文件承载了：
- 路由注册
- 请求解析与验证
- 鉴权协调（调用 identity-service）
- 渠道选择协调（调用 channel-service）
- 计费协调（reserve/commit/release，调用 billing-service）
- 上游转发（流式 + 非流式）
- 响应转换
- 日志记录
- WebSocket 中继
- 错误处理
- 40+ 个方法

**影响**: 
- 任何修改都有回归风险
- 无法并行开发
- 测试覆盖困难（测试文件 http_raw_test.go 本身 72KB）
- 认知负担极高

#### P0-2: 服务间调用无熔断/降级

**问题**: relay-gateway 依赖 4 个 gRPC 下游服务（identity、channel、billing、log），但**没有任何熔断器或降级策略**。如果任一服务不可用：

| 下游服务故障 | 影响 |
|-------------|------|
| identity-service 宕机 | **所有请求失败**（无法鉴权） |
| channel-service 宕机 | **所有请求失败**（无法选路） |
| billing-service 宕机 | **所有请求失败**（无法预扣额度） |
| log-service 宕机 | 请求可继续，但日志丢失 |

**当前代码路径**: 每次请求同步发起 3+ 次 gRPC 调用，任一超时都会阻塞整个请求。

#### P0-3: 事件总线仅进程内（MemoryEventBus）

**问题**: `internal/pkg/events/events.go` 的 `MemoryEventBus` 只在单进程内传播事件：
- 服务重启时在途事件全部丢失
- 无法跨实例/跨服务传播
- `Publish` 是 fire-and-forget，无持久化保证
- 无法支撑水平扩展场景下的事件驱动

### 2.2 P1 - 高优先级问题（影响性能和扩展性）

#### P1-1: 无本地缓存，热路径全量 gRPC 调用

**问题**: 每个 relay 请求都需要：
1. gRPC 调用 identity-service 鉴权
2. gRPC 调用 channel-service 选路
3. gRPC 调用 billing-service 预扣
4. gRPC 调用 billing-service 结算
5. gRPC 调用 log-service 写日志

**在 1000 req/s 下**: 仅元数据查询就产生 5000+ gRPC 调用/s，且这些数据（token 信息、channel 列表、模型映射）变更频率极低。

**当前延迟预估**: 每次请求的 gRPC 往返开销约 3-8ms（同机房），5 次调用 = 15-40ms 纯网络开销。

#### P1-2: 共享单库 — 9 服务共用一个 MySQL

**问题**: 所有服务共享同一个 MySQL 数据库（`oneapi`），导致：
- 表级锁竞争（billing_ledgers 高频写入 vs 用户查询）
- 迁移文件混在一起（33 个迁移，无 namespace 隔离）
- 无法针对不同服务做独立的数据库优化
- 日志表（logs）和账务流水表（billing_ledgers）高频写入，影响其他服务查询

#### P1-3: 计费同步阻塞请求热路径

**问题**: `ReserveQuota` 在请求转发前同步执行，包含：
- 幂等检查（FindByRequestID）
- 获取账户快照（GetAccountSnapshot）
- 计算费用
- 扣减可用额度、增加冻结额度

如果 billing-service 响应慢（数据库锁等待等），**所有请求都会被拖慢**。

#### P1-4: 渠道选择算法粗糙

**问题**: `channel/biz/channel.go` 的 `SelectChannel` 在同优先级内使用**纯随机**选择：
- 不考虑渠道当前负载
- 不考虑响应时间
- 不考虑近期成功率
- 无加权轮询

**对比**: new-api 有 `channel_affinity_cache.go` 做亲和性缓存，sub2api 按 Group+Platform 做更精细路由。

### 2.3 P2 - 中优先级问题（影响运维和效率）

#### P2-1: 数据库索引缺失

| 表 | 缺失索引 | 查询场景 |
|----|---------|---------|
| `logs` | `idx_user_id` | 查询用户日志 |
| `logs` | `idx_user_id_created_at` | 用户日志分页 |
| `billing_ledgers` | `idx_user_id_created_at` | 用户消费记录 |
| `billing_ledgers` | `idx_channel_id_created_at` | 渠道用量统计 |
| `channels` | `idx_group_status` | 渠道筛选 |
| `tokens` | `idx_status_expired_time` | 有效 token 查询 |

#### P2-2: 无统一幂等中间件

只有 `ReserveQuota` 检查 `request_id` 幂等，支付订单、渠道创建等操作缺少幂等保证。

#### P2-3: 配置热更新缺失

`models.yaml`（模型映射）基于文件加载，修改后需重启服务。channel 配置变更需通过 admin-api 手动操作。

#### P2-4: WebSocket 扩展性受限

- 连接池是 per-instance 的，多实例间无法共享
- 虽然有 Redis sticky session（好的设计），但缺少全局连接协调
- 部署期间无优雅连接排空

---

## 3. 目标架构设计

### 3.1 架构原则

1. **渐进式重构** — 不推倒重来，分阶段优化，每阶段可独立部署
2. **降级优先** — 任何下游故障都不能导致完全不可用
3. **缓存为王** — 热路径上的元数据查询走本地缓存
4. **异步解耦** — 非关键路径异步化（日志、计费结算、通知）
5. **可观测先行** — 重构前先加监控，用数据驱动决策

### 3.2 目标架构总览

```
                           ┌──────────────────────────────────────┐
                           │           API Edge Layer             │
                           │  (Rate Limit / CORS / WAF / TLS)     │
                           └───────────────┬──────────────────────┘
                                           │
                    ┌──────────────────────▼──────────────────────┐
                    │              relay-gateway                  │
                    │  ┌─────────┐ ┌──────────┐ ┌──────────────┐ │
                    │  │ Router  │→│ Handlers │→│ Orchestrator │ │
                    │  │ (split) │ │ (split)  │ │   (new)      │ │
                    │  └─────────┘ └──────────┘ └──────┬───────┘ │
                    │  ┌───────────────────────────────▼───────┐ │
                    │  │         Resilience Layer              │ │
                    │  │  Circuit Breaker / Timeout / Retry    │ │
                    │  │  Fallback / Bulkhead                  │ │
                    │  └───┬───────┬───────┬───────┬───────────┘ │
                    └──────┼───────┼───────┼───────┼─────────────┘
                           │       │       │       │
                    ┌──────▼──┐ ┌──▼───┐ ┌─▼────┐ ┌▼────────┐
                    │identity │ │channel│ │billing│ │  log    │
                    │  + L1   │ │ + L1  │ │+async │ │+batch   │
                    │ Cache   │ │Cache  │ │ path  │ │ writer  │
                    └────┬────┘ └──┬───┘ └──┬────┘ └────┬────┘
                         │         │        │           │
                    ┌────▼─────────▼────────▼───────────▼────┐
                    │         Redis (L2 Cache + Streams)      │
                    │  ┌────────────┐  ┌───────────────────┐  │
                    │  │ Cache L2   │  │  Event Streams    │  │
                    │  │ (metadata) │  │ (cross-service)   │  │
                    │  └────────────┘  └───────────────────┘  │
                    └────────────────────┬────────────────────┘
                                         │
                    ┌────────────────────▼────────────────────┐
                    │           MySQL (Per-Service Schema)     │
                    │  ┌────────┐ ┌────────┐ ┌──────────────┐ │
                    │  │identity│ │channel │ │   billing    │ │
                    │  │ schema │ │ schema │ │   schema     │ │
                    │  └────────┘ └────────┘ └──────────────┘ │
                    │  ┌────────┐ ┌────────┐                  │
                    │  │  log   │ │  admin │                  │
                    │  │ schema │ │ schema │                  │
                    │  └────────┘ └────────┘                  │
                    └─────────────────────────────────────────┘
```

### 3.3 架构变更清单

| 变更项 | 类型 | 影响范围 | 优先级 |
|-------|------|---------|--------|
| http.go 拆分 | 代码重构 | relay-gateway | P0 |
| gRPC 熔断器 | 新增中间件 | relay-gateway | P0 |
| 本地缓存层 (L1) | 新增组件 | relay/channel/identity | P0 |
| 事件流 (Redis Streams) | 替换 EventBus | 全部服务 | P0 |
| 日志批量写入 | 重构 log-service | log-service | P1 |
| 异步计费路径 | 新增路径 | billing/relay | P1 |
| 渠道加权选择 | 算法升级 | channel-service | P1 |
| 数据库 Schema 隔离 | 数据层重构 | 全部服务 | P1 |
| 索引优化 | DDL | MySQL | P2 |
| 配置热更新 | 新增机制 | config/relay | P2 |

---

## 4. 核心重构方案

### 4.1 http.go 拆分（P0）

将 2,391 行的 `http.go` 按职责拆分为独立 handler 模块：

```
internal/relay/server/
├── router.go              // 路由注册（~150行）
├── middleware.go           // HTTP 中间件链
├── handler/
│   ├── chat.go            // /v1/chat/completions handler
│   ├── completions.go     // /v1/completions handler
│   ├── embeddings.go      // /v1/embeddings handler
│   ├── images.go          // /v1/images/generations handler
│   ├── audio.go           // /v1/audio/* handlers
│   ├── moderations.go     // /v1/moderations handler
│   ├── models.go          // /v1/models handler
│   ├── responses.go       // /v1/responses handler
│   ├── anthropic.go       // /v1/messages (Anthropic) handler
│   └── usage.go           // /v1/usage handler
├── orchestrator.go        // 请求编排器（鉴权→选路→预扣→转发→结算→日志）
├── forwarder/
│   ├── stream.go          // 流式转发逻辑
│   ├── nonstream.go       // 非流式转发逻辑
│   └── websocket.go       // WebSocket 转发逻辑
├── billing_coord.go       // 计费协调器（reserve/commit/release）
├── response_writer.go     // 统一响应写入
└── error_handler.go       // 统一错误处理
```

**拆分原则**:
- 每个 handler 只处理一种 API 端点的请求解析和响应格式化
- `Orchestrator` 统一编排请求生命周期，从 HTTP handler 中剥离
- `Forwarder` 封装上游调用细节（流式/非流式/WebSocket）
- `BillingCoord` 封装计费三阶段（Reserve→Commit/Release），含超时和降级

**Orchestrator 接口设计**:

```go
// Orchestrator coordinates the full relay request lifecycle.
type Orchestrator interface {
    // Execute runs the complete relay pipeline:
    // auth → model mapping → channel select → reserve → forward → commit → log
    Execute(ctx context.Context, req *RelayRequest) (*RelayResult, error)
}

// RelayRequest is the normalized input for orchestration.
type RelayRequest struct {
    Token       string
    Model       string
    Endpoint    APIEndpoint    // chat/completions, embeddings, images, etc.
    Body        io.Reader
    IsStream    bool
    Headers     http.Header
}

// RelayResult contains the response and metadata.
type RelayResult struct {
    Response    io.ReadCloser  // upstream response body
    Headers     http.Header
    StatusCode  int
    Usage       *Usage          // token usage for billing
    ChannelID   int64
    Latency     time.Duration
}
```

### 4.2 gRPC 熔断与降级（P0）

为所有 gRPC 客户端添加熔断器、超时和降级策略：

```go
// internal/pkg/grpc/resilience.go

// ResilientClient wraps a gRPC client with circuit breaker,
// timeout, and fallback capabilities.
type ResilientClient[T any] struct {
    client      T
    breaker     *CircuitBreaker
    timeout     time.Duration
    fallback    FallbackFunc[T]
    metrics     *ClientMetrics
}

// CircuitBreaker config per downstream service
type BreakerConfig struct {
    Name             string
    MaxRequests      uint32        // max requests allowed when half-open
    Interval         time.Duration // cyclic period of closed state
    Timeout          time.Duration // open → half-open wait time
    FailureThreshold uint32        // consecutive failures to trip
    OnTrip           func(name string)
    OnReset          func(name string)
}

// Fallback strategies per service
var FallbackStrategies = map[string]FallbackStrategy{
    "identity":  &AuthCacheFallback{},     // 用缓存的 auth snapshot
    "channel":   &ChannelCacheFallback{},   // 用缓存的 channel 列表
    "billing":   &AsyncBillingFallback{},   // 异步计费，允许先放行
    "log":       &NoOpFallback{},           // 丢弃日志，不影响请求
}
```

**降级矩阵**:

| 下游服务 | 熔断后行为 | 数据一致性影响 |
|---------|-----------|--------------|
| identity-service | 使用本地缓存的 Token 快照（TTL 30s） | 可能有已吊销的 token 仍在窗口内有效 |
| channel-service | 使用本地缓存的 channel 列表（TTL 60s） | 新渠道变更延迟生效 |
| billing-service | 切换异步计费模式，先放行后结算 | 短期内可能超扣，通过对账补偿 |
| log-service | 丢弃日志写入，记录到本地缓冲 | 日志可能丢失，需后续补偿 |

**熔断器配置建议**:

```yaml
# configs/resilience.yaml
circuit_breaker:
  identity:
    failure_threshold: 5        # 连续5次失败触发熔断
    timeout: 30s                # 熔断后30s进入半开
    max_half_open_requests: 3   # 半开状态最多3个探测请求
    fallback: cache             # 降级策略: cache / async / noop
  channel:
    failure_threshold: 5
    timeout: 30s
    max_half_open_requests: 3
    fallback: cache
  billing:
    failure_threshold: 10       # 计费容错更高，10次才熔断
    timeout: 60s
    max_half_open_requests: 5
    fallback: async
  log:
    failure_threshold: 20
    timeout: 10s
    max_half_open_requests: 10
    fallback: noop

timeout:
  identity: 2s
  channel: 2s
  billing: 3s
  log: 1s
  upstream: 300s                # AI 模型调用本身可能很长
```

### 4.3 事件流升级 — Redis Streams（P0）

将进程内 `MemoryEventBus` 升级为基于 Redis Streams 的跨进程事件总线：

```go
// internal/pkg/events/stream_bus.go

// StreamEventBus is a cross-process EventBus backed by Redis Streams.
// It guarantees at-least-once delivery with consumer groups.
type StreamEventBus struct {
    redis       *redis.Client
    consumerID  string                     // unique per instance
    handlers    map[string][]Handler
    maxlen      int64                      // stream max length (approximate)
    readTimeout time.Duration
}

// Publish sends an event to a Redis Stream with guaranteed persistence.
// Events survive process restarts.
func (b *StreamEventBus) Publish(ctx context.Context, topic string, payload interface{}) error {
    data, err := sonic.Marshal(payload)
    if err != nil {
        return fmt.Errorf("marshal event payload: %w", err)
    }
    return b.redis.XAdd(ctx, &redis.XAddArgs{
        Stream: topic,
        MaxLen: b.maxlen,       // trim old events to bound memory
        Approx: true,
        Values: map[string]interface{}{
            "payload":    data,
            "timestamp":  time.Now().UnixNano(),
            "producer":   b.consumerID,
        },
    }).Err()
}

// Subscribe joins a consumer group and processes events.
// Each event is ACKed only after the handler succeeds.
func (b *StreamEventBus) Subscribe(topic string, handler Handler) {
    b.ensureGroup(topic)
    go b.consumeLoop(topic, handler)
}
```

**事件拓扑升级**:

| 事件 Topic | 生产者 | 消费者 | 用途 |
|-----------|-------|-------|------|
| `channel.changed` | channel-svc | relay-gateway (缓存失效) | 渠道变更通知 |
| `config.changed` | config-svc | 全部服务 | 配置变更通知 |
| `billing.quota.reserved` | billing-svc | log-svc, monitor | 预扣记录 |
| `billing.quota.committed` | billing-svc | log-svc, monitor | 结算记录 |
| `billing.quota.released` | billing-svc | log-svc | 释放记录 |
| `relay.request.finished` | relay-gateway | log-svc, monitor | 请求完成 |
| `relay.request.failed` | relay-gateway | log-svc, notify-svc | 请求失败告警 |
| `channel.health.changed` | channel-svc | notify-svc, monitor | 渠道健康变更 |
| `payment.order.paid` | billing-svc | notify-svc | 支付成功通知 |

### 4.4 渠道加权选择算法（P1）

将纯随机选择升级为加权轮询 + 健康感知：

```go
// internal/channel/biz/selector.go

// WeightedSelector selects channels using a weighted round-robin
// algorithm that considers response time, success rate, and
// configured weight.
type WeightedSelector struct {
    mu       sync.Mutex
    channels map[int64]*channelState  // channelID → runtime state
}

type channelState struct {
    channel         *Channel
    weight          uint32            // configured weight
    currentWeight   int32             // smooth WRR current weight
    recentLatency   *SlidingWindow    // last 100 request latencies
    recentErrors    *SlidingCounter   // last 60s error count
    inflight        atomic.Int32      // current in-flight requests
}

// Select implements smooth weighted round-robin with health awareness.
// Algorithm: nginx-style smooth WRR + dynamic weight adjustment
func (s *WeightedSelector) Select(ctx context.Context, group, model string) (*Channel, error) {
    candidates := s.getCandidates(group, model)
    if len(candidates) == 0 {
        return nil, ErrChannelNotFound
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    var best *channelState
    var bestWeight int32 = math.MinInt32

    for _, cs := range candidates {
        // Skip circuit-opened channels
        if cs.channel.CircuitOpenedUntil > time.Now().Unix() {
            continue
        }

        // Skip overloaded channels (inflight > maxConcurrent)
        if cs.inflight.Load() > cs.maxConcurrent() {
            continue
        }

        // Dynamic weight = static weight × health factor × latency factor
        dynamicWeight := int32(cs.weight) * cs.healthFactor() * cs.latencyFactor()

        // Smooth WRR: current += dynamic, track max
        cs.currentWeight += dynamicWeight
        if cs.currentWeight > bestWeight {
            bestWeight = cs.currentWeight
            best = cs
        }
    }

    if best == nil {
        return nil, ErrChannelNotFound
    }

    // Decrement selected channel's current weight by total
    totalWeight := s.totalWeight(candidates)
    best.currentWeight -= totalWeight

    best.inflight.Add(1)
    return best.channel, nil
}

// healthFactor returns 0.1-1.0 based on recent error rate
func (cs *channelState) healthFactor() int32 {
    errorRate := cs.recentErrors.Rate()
    switch {
    case errorRate < 0.01:  return 100  // <1% error → full weight
    case errorRate < 0.05:  return 80   // <5% error → 80% weight
    case errorRate < 0.10:  return 50   // <10% error → 50% weight
    case errorRate < 0.30:  return 20   // <30% error → 20% weight
    default:                return 1    // >30% error → minimal weight
    }
}

// latencyFactor returns 50-100 based on p95 latency
func (cs *channelState) latencyFactor() int32 {
    p95 := cs.recentLatency.P95()
    switch {
    case p95 < 500*time.Millisecond:  return 100
    case p95 < 2*time.Second:         return 80
    case p95 < 5*time.Second:         return 50
    default:                          return 20
    }
}
```

### 4.5 异步计费路径（P1）

为信任用户和低风险场景提供异步计费选项：

```go
// internal/billing/biz/async_billing.go

// AsyncBillingUsecase provides a non-blocking billing path.
// It uses a local quota check + async settlement.
type AsyncBillingUsecase struct {
    localCache   *QuotaCache          // L1: in-memory quota snapshot
    redis        *redis.Client        // L2: distributed quota counter
    settleQueue  chan *SettleTask      // async settlement queue
    batchWriter  *BatchLedgerWriter    // batch ledger persistence
}

// PreCheck performs a fast local quota check without DB round-trip.
// This is the "fast path" — actual deduction happens asynchronously.
func (uc *AsyncBillingUsecase) PreCheck(ctx context.Context, userID, model string, estimatedTokens int64) error {
    // L1 check: local cache
    quota, ok := uc.localCache.Get(userID)
    if !ok {
        // L2 check: Redis atomic check-and-decrement
        quota, ok = uc.loadFromRedis(ctx, userID)
        if !ok {
            return ErrQuotaCheckUnavailable
        }
    }

    cost := uc.estimateCost(model, estimatedTokens)
    if quota.Available < cost {
        return ErrInsufficientQuota
    }

    // Optimistic deduction in Redis (atomic)
    _, err := uc.redis.Eval(ctx, luaCheckAndDeduct, 
        []string{fmt.Sprintf("quota:%s", userID)}, cost)
    return err
}

// Settle performs the actual billing asynchronously.
// Called after upstream response completes with real usage data.
func (uc *AsyncBillingUsecase) Settle(task *SettleTask) {
    select {
    case uc.settleQueue <- task:
        // queued successfully
    default:
        // queue full → fallback to synchronous settle
        uc.settleSync(context.Background(), task)
    }
}
```

**同步 vs 异步计费决策**:

| 用户类型 | 计费模式 | 理由 |
|---------|---------|------|
| 新用户 (< 100 requests) | 同步 | 需要严格额度控制 |
| 低余额用户 (< $1) | 同步 | 防止超扣 |
| 正常用户 | 异步 | 降低延迟 |
| 无限额度用户 | 异步 | 无额度风险 |
| billing-svc 降级时 | 异步 | 熔断降级 |

---

## 5. 数据库优化方案

### 5.1 Schema 隔离策略

将共享的 `oneapi` 数据库拆分为按服务隔离的 schema（同一 MySQL 实例内）：

```sql
-- Phase 1: Schema 隔离（同实例不同 schema）
CREATE DATABASE IF NOT EXISTS oneapi_identity;
CREATE DATABASE IF NOT EXISTS oneapi_channel;
CREATE DATABASE IF NOT EXISTS oneapi_billing;
CREATE DATABASE IF NOT EXISTS oneapi_log;
CREATE DATABASE IF NOT EXISTS oneapi_admin;

-- 迁移表到对应 schema
-- identity schema: users, tokens, user_oauth_identities
-- channel schema: channels, abilities, subscription_accounts
-- billing schema: billing_ledgers, billing_reservations, billing_redeem_codes, 
--                 billing_redeem_records, payment_orders, reconciliation_runs
-- log schema: logs
-- admin schema: configs, system_options, notifications, health_checks, alert_rules
```

**连接配置变更**:

```yaml
# configs/identity-service.yaml
database:
  dsn: ${DATABASE_DSN_BASE}/oneapi_identity
# configs/channel-service.yaml
database:
  dsn: ${DATABASE_DSN_BASE}/oneapi_channel
# configs/billing-service.yaml
database:
  dsn: ${DATABASE_DSN_BASE}/oneapi_billing
```

### 5.2 索引优化

```sql
-- ============ logs 表 ============
ALTER TABLE logs 
  ADD INDEX idx_user_id (user_id),
  ADD INDEX idx_user_id_created_at (user_id, created_at),
  ADD INDEX idx_request_id (request_id);

-- ============ billing_ledgers 表 ============
ALTER TABLE billing_ledgers
  ADD INDEX idx_user_id_created_at (user_id, created_at),
  ADD INDEX idx_channel_id_created_at (channel_id, created_at),
  ADD INDEX idx_model_created_at (model, created_at),
  ADD INDEX idx_created_at (created_at);

-- ============ channels 表 ============
ALTER TABLE channels
  ADD INDEX idx_group_status (status, `group`),
  ADD INDEX idx_group_status_priority (status, `group`, priority DESC);

-- ============ tokens 表 ============
ALTER TABLE tokens
  ADD INDEX idx_status_expired (status, expired_time);

-- ============ billing_reservations 表 ============
ALTER TABLE billing_reservations
  ADD INDEX idx_user_id_status (user_id, status),
  ADD INDEX idx_expires_at (expires_at);
```

### 5.3 日志表分区

`logs` 和 `billing_ledgers` 是高频写入表，按时间分区可以显著提升查询性能和数据清理效率：

```sql
-- logs 表按月分区
ALTER TABLE logs PARTITION BY RANGE (FROM_UNIXTIME(created_at)) (
    PARTITION p202606 VALUES LESS THAN ('2026-07-01'),
    PARTITION p202607 VALUES LESS THAN ('2026-08-01'),
    PARTITION p202608 VALUES LESS THAN ('2026-09-01'),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

-- billing_ledgers 表按月分区
ALTER TABLE billing_ledgers PARTITION BY RANGE (FROM_UNIXTIME(created_at)) (
    PARTITION p202606 VALUES LESS THAN ('2026-07-01'),
    PARTITION p202607 VALUES LESS THAN ('2026-08-01'),
    PARTITION pmax VALUES LESS THAN MAXVALUE
);

-- 自动清理旧分区（每月执行）
-- ALTER TABLE logs DROP PARTITION p202506;
```

### 5.4 连接池优化

```go
// internal/pkg/xdb/mysql.go

func NewMySQL(cfg DatabaseConfig) *gorm.DB {
    db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
    if err != nil {
        log.Fatal("failed to connect database")
    }

    sqlDB, _ := db.DB()
    
    // Per-service connection pool sizing
    // Formula: max_connections = (avg_query_time × peak_qps) / 1000 × safety_factor
    sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)     // default: 50
    sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)     // default: 10
    sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime) // default: 30m
    sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime) // default: 5m
    
    return db
}
```

**连接池配置建议**:

| 服务 | MaxOpen | MaxIdle | 理由 |
|------|---------|---------|------|
| relay-gateway | 20 | 5 | 主要走缓存，DB 访问少 |
| identity-service | 30 | 10 | 中等读写 |
| channel-service | 30 | 10 | 中等读写 |
| billing-service | 50 | 15 | 高频写入 |
| log-service | 40 | 10 | 批量写入 |
| admin-api | 20 | 5 | 低频管理查询 |

---

## 6. 容错与降级策略

### 6.1 多层容错架构

```
┌──────────────────────────────────────────────────────┐
│                    请求入口                           │
├──────────────────────────────────────────────────────┤
│  Layer 1: Edge Rate Limiting (Redis token bucket)    │
│  → 超限直接 429，保护后端                             │
├──────────────────────────────────────────────────────┤
│  Layer 2: Auth Cache (L1 local + L2 Redis)           │
│  → identity-svc 故障时，用缓存鉴权                    │
├──────────────────────────────────────────────────────┤
│  Layer 3: Channel Cache (L1 local + L2 Redis)        │
│  → channel-svc 故障时，用缓存选路                     │
├──────────────────────────────────────────────────────┤
│  Layer 4: Billing Fallback (async mode)              │
│  → billing-svc 故障时，异步计费+对账补偿               │
├──────────────────────────────────────────────────────┤
│  Layer 5: Log Buffer (in-memory ring buffer)         │
│  → log-svc 故障时，缓冲到内存，恢复后批量补写          │
├──────────────────────────────────────────────────────┤
│  Layer 6: Upstream Retry (channel failover)          │
│  → 上游失败时，自动切换到备用渠道                      │
└──────────────────────────────────────────────────────┘
```

### 6.2 降级决策引擎

```go
// internal/relay/orchestrator/degradation.go

type DegradationLevel int

const (
    DegradationNone     DegradationLevel = iota  // 全部正常
    DegradationCached                             // 使用缓存数据
    DegradationAsyncBilling                       // 异步计费
    DegradationMinimal                            // 最小可用（仅缓存鉴权+选路）
)

// assessDegradation evaluates the health of downstream services
// and determines the appropriate degradation level.
func (o *Orchestrator) assessDegradation(ctx context.Context) DegradationLevel {
    identityHealthy := o.identityBreaker.State() == BreakerClosed
    channelHealthy := o.channelBreaker.State() == BreakerClosed
    billingHealthy := o.billingBreaker.State() == BreakerClosed
    logHealthy := o.logBreaker.State() == BreakerClosed

    switch {
    case identityHealthy && channelHealthy && billingHealthy:
        return DegradationNone
    case !billingHealthy && identityHealthy && channelHealthy:
        return DegradationAsyncBilling
    case !identityHealthy || !channelHealthy:
        // Check if we have cached data
        if o.authCache.HasData() && o.channelCache.HasData() {
            return DegradationMinimal
        }
        return DegradationNone // can't degrade, must fail
    default:
        if logHealthy {
            return DegradationCached
        }
        return DegradationMinimal
    }
}
```

### 6.3 上游重试与故障转移

```go
// internal/relay/orchestrator/retry.go

type RetryConfig struct {
    MaxRetries         int               // default: 2
    RetryOnStatusCodes []int             // [429, 500, 502, 503, 504]
    RetryOnErrors      []error           // [context.DeadlineExceeded, ...]
    Backoff            BackoffStrategy   // exponential with jitter
    FailoverOnRetry    bool              // switch channel on retry
}

type BackoffStrategy struct {
    InitialDelay time.Duration  // 100ms
    MaxDelay     time.Duration  // 2s
    Multiplier   float64        // 2.0
    Jitter       float64        // 0.1 (±10%)
}

// ExecuteWithFailover tries the primary channel, and on retry,
// selects a different channel from the next priority tier.
func (o *Orchestrator) ExecuteWithFailover(ctx context.Context, plan *RelayPlan) (*RelayResult, error) {
    var lastErr error
    currentPlan := plan

    for attempt := 0; attempt <= o.retryConfig.MaxRetries; attempt++ {
        if attempt > 0 {
            // Exponential backoff with jitter
            delay := o.calculateBackoff(attempt)
            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return nil, ctx.Err()
            }

            // Failover: select a different channel
            if o.retryConfig.FailoverOnRetry {
                newPlan, err := o.replan(ctx, plan, attempt)
                if err != nil {
                    return nil, fmt.Errorf("failover replan failed: %w", err)
                }
                currentPlan = newPlan
            }
        }

        result, err := o.forward(ctx, currentPlan)
        if err == nil {
            return result, nil
        }

        // Check if error is retryable
        if !o.isRetryable(err) {
            return nil, err
        }

        lastErr = err
        
        // Record channel health for weighted selector
        o.recordHealth(currentPlan.Channel.ID, false, err)
    }

    return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}
```

---

## 7. 缓存架构设计

### 7.1 多级缓存策略

```
┌──────────────────────────────────────────────────────┐
│                 请求处理热路径                         │
│                                                       │
│  ┌──────────────┐    ┌──────────────┐               │
│  │  L1: Local   │───→│  L2: Redis   │───→ gRPC      │
│  │  (in-memory) │    │  (shared)    │    (source)   │
│  │  TTL: 10-30s │    │  TTL: 5-10m  │               │
│  └──────────────┘    └──────────────┘               │
│                                                       │
│  Cache invalidation:                                  │
│  - Event-driven (Redis Streams → channel.changed)    │
│  - TTL expiry (fallback)                             │
│  - Write-through (admin API updates → cache update)  │
└──────────────────────────────────────────────────────┘
```

### 7.2 缓存对象设计

```go
// internal/pkg/cache/multilevel.go

// MultiLevelCache provides L1 (local) + L2 (Redis) caching
// with event-driven invalidation.
type MultiLevelCache[T any] struct {
    l1         *ristretto.Cache[string, *entry[T]]  // local LRU
    l2         *redis.Client                         // shared Redis
    prefix     string                                // cache key prefix
    ttl        time.Duration                         // L1 TTL
    l2TTL      time.Duration                         // L2 TTL
    eventBus   *events.StreamEventBus                // invalidation events
    loader     CacheLoader[T]                        // cache miss → source
    metrics    *CacheMetrics
}

// Get retrieves from L1 → L2 → source, populating upstream caches.
func (c *MultiLevelCache[T]) Get(ctx context.Context, key string) (*T, error) {
    // L1 check
    if val, ok := c.l1.Get(c.prefix + key); ok {
        if !val.expired() {
            c.metrics.L1Hit()
            return val.data, nil
        }
    }

    // L2 check
    data, err := c.l2.Get(ctx, c.prefix+key).Bytes()
    if err == nil {
        c.metrics.L2Hit()
        var val T
        if err := sonic.Unmarshal(data, &val); err == nil {
            c.l1.Set(c.prefix+key, &entry[T]{data: &val, expiresAt: time.Now().Add(c.ttl)}, 1)
            return &val, nil
        }
    }

    // Cache miss → load from source
    c.metrics.Miss()
    val, err := c.loader.Load(ctx, key)
    if err != nil {
        return nil, err
    }

    // Write-through to L1 + L2
    c.populate(ctx, key, val)
    return val, nil
}

// Invalidate removes a key from both L1 and L2.
// Triggered by event-driven invalidation or explicit API.
func (c *MultiLevelCache[T]) Invalidate(ctx context.Context, key string) error {
    c.l1.Del(c.prefix + key)
    return c.l2.Del(ctx, c.prefix+key).Err()
}
```

### 7.3 缓存对象与 TTL

| 缓存对象 | L1 TTL | L2 TTL | 失效触发 | 大小预估 |
|---------|--------|--------|---------|---------|
| Auth Snapshot (token → user) | 30s | 5m | token 吊销/用户禁用 | ~1KB/entry |
| Channel List (group+model → channels) | 60s | 10m | channel.changed 事件 | ~5KB/entry |
| Model Mapping (client → upstream) | ∞ (启动加载) | ∞ | config.changed 事件 | ~100KB total |
| User Quota (user → balance) | 10s | 1m | billing 事件 | ~100B/entry |
| Channel Health (channel → stats) | 5s | 30s | 健康变更事件 | ~200B/entry |
| System Options | 5m | 30m | config.changed 事件 | ~10KB total |

### 7.4 缓存击穿保护

```go
// singleflight pattern to prevent cache stampede
func (c *MultiLevelCache[T]) Get(ctx context.Context, key string) (*T, error) {
    // ... L1/L2 checks ...

    // Cache miss: use singleflight to prevent thundering herd
    val, err, _ := c.sf.Do(key, func() (*T, error) {
        return c.loader.Load(ctx, key)
    })
    if err != nil {
        return nil, err
    }
    c.populate(ctx, key, val)
    return val, nil
}
```

---

## 8. 可观测性增强

### 8.1 指标体系

```go
// internal/pkg/metrics/metrics.go

// === Relay Gateway Metrics ===
var (
    RelayRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "relay_request_duration_seconds",
            Help:    "Relay request duration in seconds",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
        },
        []string{"endpoint", "model", "channel_type", "status"},
    )

    RelayUpstreamDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "relay_upstream_duration_seconds",
            Help:    "Time spent waiting for upstream provider",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
        },
        []string{"provider", "model", "stream"},
    )

    RelayRetryCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "relay_retry_total",
            Help: "Number of retry attempts",
        },
        []string{"endpoint", "reason"},
    )

    RelayFailoverCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "relay_failover_total",
            Help: "Number of channel failover events",
        },
        []string{"from_channel_type", "to_channel_type"},
    )
)

// === Resilience Metrics ===
var (
    CircuitBreakerState = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "circuit_breaker_state",
            Help: "Circuit breaker state: 0=closed, 1=half-open, 2=open",
        },
        []string{"service"},
    )

    CircuitBreakerTrips = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "circuit_breaker_trips_total",
            Help: "Number of circuit breaker trips",
        },
        []string{"service"},
    )

    DegradationLevel = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "degradation_level",
            Help: "Current degradation level: 0=none, 1=cached, 2=async, 3=minimal",
        },
        []string{"service"},
    )
)

// === Cache Metrics ===
var (
    CacheHits = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "cache_hits_total",
            Help: "Cache hit count",
        },
        []string{"cache", "level"},  // level: l1, l2
    )

    CacheMisses = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "cache_misses_total",
            Help: "Cache miss count",
        },
        []string{"cache"},
    )

    CacheLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "cache_operation_duration_seconds",
            Help:    "Cache operation latency",
            Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05},
        },
        []string{"cache", "operation", "level"},
    )
)

// === Billing Metrics ===
var (
    BillingReserveDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "billing_reserve_duration_seconds",
            Help:    "Quota reservation duration",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
        },
        []string{"mode"},  // sync, async
    )

    BillingSettlementLag = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "billing_settlement_lag_seconds",
            Help:    "Lag between async pre-check and settlement",
            Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60},
        },
    )

    QuotaCheckFallback = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "quota_check_fallback_total",
            Help: "Number of quota check fallbacks (sync→async or cache)",
        },
        []string{"reason"},
    )
)
```

### 8.2 告警规则

```yaml
# Prometheus Alert Rules
groups:
  - name: relay-gateway
    rules:
      - alert: HighErrorRate
        expr: |
          rate(relay_request_duration_seconds_count{status=~"5.."}[5m]) 
          / rate(relay_request_duration_seconds_count[5m]) > 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Relay error rate > 5%"

      - alert: CircuitBreakerOpen
        expr: circuit_breaker_state == 2
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Circuit breaker open for {{ $labels.service }}"

      - alert: HighUpstreamLatency
        expr: |
          histogram_quantile(0.95, 
            rate(relay_upstream_duration_seconds_bucket[5m])) > 30
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "P95 upstream latency > 30s"

      - alert: BillingSettlementLag
        expr: |
          histogram_quantile(0.99, 
            rate(billing_settlement_lag_seconds_bucket[5m])) > 60
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Billing settlement lag > 60s at P99"

      - alert: CacheHitRateLow
        expr: |
          rate(cache_hits_total{level="l1"}[5m]) 
          / (rate(cache_hits_total{level="l1"}[5m]) + rate(cache_misses_total[5m])) < 0.8
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "L1 cache hit rate < 80%"
```

---

## 9. 安全加固方案

### 9.1 当前安全基线评估

| 安全措施 | 状态 | 说明 |
|---------|------|------|
| AES 加密存储 | ✅ 已实现 | channel key、OAuth token 加密 |
| JWT 鉴权 | ✅ 已实现 | gRPC + HTTP 双协议 |
| SSRF 防护 | ✅ 已实现 | Provider 层 validateBaseURL |
| CORS 配置 | ✅ 已实现 | 可配置白名单 |
| 速率限制 | ✅ 已实现 | 令牌桶限流 |
| 安全头 | ✅ 已实现 | Helmet 风格中间件 |
| 容器安全 | ✅ 已实现 | read_only + cap_drop ALL |

### 9.2 需增强的安全措施

```go
// 1. gRPC 服务间认证 (mTLS or service token)
// internal/pkg/grpc/auth_interceptor.go

func ServiceAuthInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
        md, ok := metadata.FromIncomingContext(ctx)
        if !ok {
            return nil, status.Error(codes.Unauthenticated, "missing metadata")
        }
        tokens := md.Get("x-service-token")
        if len(tokens) == 0 || tokens[0] != serviceToken {
            return nil, status.Error(codes.Unauthenticated, "invalid service token")
        }
        return handler(ctx, req)
    }
}

// 2. 请求体大小限制 (防止 OOM)
func MaxBodySize(maxBytes int64) middleware.Middleware {
    return func(handler middleware.Handler) middleware.Handler {
        return func(ctx context.Context, req interface{}) (interface{}, error) {
            if r, ok := getHTTPRequest(ctx); ok {
                r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
            }
            return handler(ctx, req)
        }
    }
}

// 3. 敏感数据脱敏 (日志中不记录 API key / token)
func SanitizeLogFields(fields []zap.Field) []zap.Field {
    for i, f := range fields {
        if isSensitiveKey(f.Key) {
            fields[i] = zap.String(f.Key, "[REDACTED]")
        }
    }
    return fields
}

// 4. 审计日志 (管理操作)
type AuditLogger struct {
    log *zap.Logger
}

func (a *AuditLogger) Log(ctx context.Context, action string, resource string, resourceID string, userID int64, detail interface{}) {
    a.log.Info("audit",
        zap.String("action", action),
        zap.String("resource", resource),
        zap.String("resource_id", resourceID),
        zap.Int64("user_id", userID),
        zap.Any("detail", detail),
        zap.String("ip", getIPFromContext(ctx)),
        zap.Time("timestamp", time.Now()),
    )
}
```

---

## 10. 迁移路径与优先级

### 10.1 分阶段迁移计划

```
Phase 0: 可观测性先行 (1-2周)
├── 添加 Prometheus 指标埋点
├── 配置 Grafana Dashboard
├── 设置告警规则
└── 验证当前性能基线

Phase 1: P0 重构 (3-4周)
├── 1.1 http.go 拆分 (Handler + Orchestrator + Forwarder)
├── 1.2 gRPC 熔断器 + 降级策略
├── 1.3 本地缓存层 (L1) — auth + channel
├── 1.4 Redis Streams 事件总线
└── 1.5 数据库索引优化 (DDL，零风险)

Phase 2: P1 优化 (3-4周)
├── 2.1 异步计费路径
├── 2.2 渠道加权选择算法
├── 2.3 日志批量写入
├── 2.4 数据库 Schema 隔离
└── 2.5 配置热更新机制

Phase 3: P2 增强 (2-3周)
├── 3.1 日志表分区
├── 3.2 幂等中间件
├── 3.3 WebSocket 优雅排空
├── 3.4 审计日志
└── 3.5 gRPC mTLS (服务间认证)
```

### 10.2 详细任务分解

#### Phase 1.1: http.go 拆分

| 步骤 | 工作内容 | 验证方式 |
|------|---------|---------|
| 1 | 提取 `Orchestrator` 接口和实现 | 单元测试覆盖编排逻辑 |
| 2 | 提取 `Forwarder`（stream/nonstream/ws） | 复用现有 http_raw_test.go |
| 3 | 提取 `BillingCoord`（reserve/commit/release） | 单元测试 + 降级测试 |
| 4 | 按端点拆分 Handler 文件 | 各 Handler 独立测试 |
| 5 | 提取 `Router` 和 `Middleware` | 路由注册测试 |
| 6 | 验证所有端点 E2E 测试通过 | 集成测试 |

#### Phase 1.2: gRPC 熔断器

| 步骤 | 工作内容 | 验证方式 |
|------|---------|---------|
| 1 | 实现 `CircuitBreaker`（sony/gobreaker） | 单元测试 |
| 2 | 实现 `ResilientClient` wrapper | 单元测试 |
| 3 | 实现 4 种降级策略（cache/async/noop/identity） | 单元测试 |
| 4 | 集成到 relay-gateway 的 gRPC client | 故障注入测试 |
| 5 | 添加 Prometheus 指标 | 指标验证 |

#### Phase 1.3: 本地缓存层

| 步骤 | 工作内容 | 验证方式 |
|------|---------|---------|
| 1 | 实现 `MultiLevelCache` 泛型组件 | 单元测试 |
| 2 | 集成 singleflight 防击穿 | 并发测试 |
| 3 | 实现 Auth Cache（token → AuthSnapshot） | 缓存命中率验证 |
| 4 | 实现 Channel Cache（group+model → channels） | 缓存命中率验证 |
| 5 | 事件驱动缓存失效（channel.changed） | 集成测试 |
| 6 | 性能对比测试（加缓存前后） | 压测 |

### 10.3 风险控制

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| http.go 拆分引入回归 | 中 | 高 | 保留原文件备份，分步拆分，每步跑全量测试 |
| 缓存数据不一致 | 中 | 高 | 短 TTL + 事件失效 + 对账机制 |
| 异步计费超扣 | 低 | 中 | 仅对信任用户开放，对账补偿 |
| Schema 隔离迁移失败 | 低 | 高 | 先在 staging 环境验证，保留回滚脚本 |
| 熔断器误触发 | 中 | 中 | 合理配置阈值 + 半开探测 + 监控告警 |

---

## 11. 预期收益

### 11.1 性能提升

| 指标 | 现状 (预估) | 目标 | 提升幅度 |
|------|-----------|------|---------|
| P95 请求延迟 (不含上游) | 30-50ms | 5-10ms | ~80% |
| gRPC 调用/请求 | 5 次 | 0-1 次 (缓存命中) | ~90% |
| 单实例吞吐 (req/s) | ~500 | ~2000 | 4x |
| billing-svc 故障影响 | 全站不可用 | 延迟结算 | 0 downtime |
| identity-svc 故障影响 | 全站不可用 | 30s 缓存窗口 | 0 downtime |

### 11.2 可维护性提升

| 指标 | 现状 | 目标 |
|------|------|------|
| 最大文件行数 | 2,391 行 (http.go) | < 400 行 |
| 服务间耦合度 | 同步强依赖 | 熔断 + 降级 + 异步 |
| 事件可靠性 | 进程内 fire-and-forget | Redis Streams 至少一次 |
| 数据库隔离度 | 共享单库 | 按 schema 隔离 |
| 可观测性 | 基础指标 | 全链路 + 熔断 + 缓存 + 计费指标 |

### 11.3 可用性提升

| 场景 | 现状 | 重构后 |
|------|------|--------|
| 单服务故障 | 级联失败 | 自动降级，核心功能可用 |
| 数据库慢查询 | 全站变慢 | 隔离到单 schema，不影响其他服务 |
| 流量突增 | 限流保护 | 缓存吸收 + 异步计费 + 自动扩缩 |
| 部署期间 | 短暂中断 | 优雅排空 + 缓存兜底 |

---

## 附录

### A. 参考架构模式

- **Circuit Breaker**: Microsoft Azure Architecture Center — Circuit Breaker Pattern
- **Bulkhead**: Netflix Hystrix — Bulkhead Pattern
- **Saga**: Event-driven distributed transactions
- **CQRS**: Command Query Responsibility Segregation (billing read/write separation)
- **Sidecar**: Service mesh pattern for observability and security

### B. 推荐依赖库

| 库 | 用途 | 版本 |
|----|------|------|
| `github.com/sony/gobreaker` | 熔断器 | v1.0 |
| `github.com/dgraph-io/ristretto` | 高性能本地缓存 | v0.2.0 |
| `golang.org/x/sync/singleflight` | 缓存击穿保护 | stdlib |
| `github.com/redis/go-redis/v9` | Redis Streams | v9.19+ |
| `github.com/prometheus/client_golang` | 指标埋点 | v1.23+ |

### C. 关键文件索引

| 文件 | 行数 | 重构优先级 |
|------|------|-----------|
| `internal/relay/server/http.go` | 2,391 | P0-拆分 |
| `internal/relay/server/http_adaptor.go` | 342 | 随 P0 拆分 |
| `internal/relay/server/http_enhanced.go` | 448 | 随 P0 拆分 |
| `internal/relay/server/responses_fallback.go` | ~600 | 随 P0 拆分 |
| `internal/relay/server/anthropic_inbound.go` | ~800 | 随 P0 拆分 |
| `internal/relay/biz/relay.go` | 290 | P0-Orchestrator |
| `internal/billing/biz/billing.go` | 749 | P1-异步路径 |
| `internal/channel/biz/channel.go` | 517 | P1-加权选择 |
| `internal/pkg/events/events.go` | 70 | P0-Redis Streams |
