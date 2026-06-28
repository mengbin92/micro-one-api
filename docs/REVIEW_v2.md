# micro-one-api 架构重构 Review v2 跟进报告

**Review 日期**: 2026-06-28 (第二轮)
**跟进修复日期**: 2026-06-28
**原 Review 范围**: commit `4187d4b` - "fix(refactor): resolve REVIEW_v1 defects"
**跟进结论**: ⚠️ **P1 质量问题保持通过；P0 已修复 handler/forwarder 空响应与 WeightedSelector 接入，但 http.go 拆分和更多新模块热路径接入仍未完成。**

---

## 一、总体状态

| REVIEW_v1 / REVIEW_v2 问题 | 当前状态 | 说明 |
|----------------------------|----------|------|
| **P0-1** http.go God Object 拆分 | ❌ **未修复** | `internal/relay/server/http.go` 仍是 2,391 行，本次未做大拆分 |
| **P0-2** handler/forwarder 关键 TODO | ✅ **已修复** | Chat/Completions handler 会写回上游响应；Stream/NonStream forwarder 已真实转发 |
| **P0-3** 新增模块零引用 | ⚠️ **部分修复** | `WeightedSelector` 已接入 `channel/biz`；orchestrator/forwarder 可执行并有测试；AuthCache/AsyncBilling/StreamEventBus 等仍未接入主热路径 |
| **P1-1 ~ P1-6** 模块质量问题 | ✅ **已修复** | REVIEW_v2 中确认通过，本次未回退 |

**核心判断**：本次跟进解决了 REVIEW_v2 中最危险的“新 handler 接入后会返回空响应”问题，并把 `WeightedSelector` 接入 channel 选路。系统已经不再是“forwarder 全 TODO”的状态，但还不能宣称架构重构完全落地，因为 `http.go` 仍未拆分，缓存、异步计费、Redis Stream 事件总线、gRPC 熔断等模块仍需要继续接入。

---

## 二、本次修复内容

### ✅ P0-2: handler/forwarder 不再空响应

已修复文件：

| 文件 | 修复内容 |
|------|----------|
| `internal/relay/server/handler/chat.go` | 将原始 body 传给 orchestrator；复制上游 status/header/body 到客户端 |
| `internal/relay/server/handler/completions.go` | 同上，修复 orchestrator 成功后直接 return 的空响应 |
| `internal/relay/server/forwarder/nonstream.go` | 通过 provider raw `Forward` 执行非流式请求，并提取 usage |
| `internal/relay/server/forwarder/stream.go` | 通过 provider raw `ForwardStream` 执行流式请求，并返回 chunk reader |
| `internal/relay/server/orchestrator.go` | 从只做 `Plan()` 改为执行 `Plan -> model rewrite -> forward -> RelayResult` |

验证：

```bash
$ rg -n "TODO: Forward response|TODO: Implement streaming forwarder|TODO: Implement chunk processing|TODO: Cleanup resources|TODO: Implement non-streaming forwarder|TODO: Stage" internal/relay/server -S
(empty)
```

新增测试：

| 测试文件 | 覆盖范围 |
|----------|----------|
| `internal/relay/server/handler/handler_test.go` | handler 必须写出 orchestrator 返回的响应 |
| `internal/relay/server/orchestrator_test.go` | orchestrator 使用真实 provider factory 转发到 httptest upstream，校验响应体、usage 和 upstream Authorization |

### ✅ P0-3 部分: WeightedSelector 已接入 channel 选路

已修复文件：

| 文件 | 修复内容 |
|------|----------|
| `internal/channel/biz/channel.go` | `ChannelUsecase` 持有 `WeightedSelector`，同优先级 tier 内使用健康感知加权选择 |
| `internal/channel/biz/selector.go` | selector 状态会刷新静态权重，并继承 DB 中的熔断时间 |

保留语义：

- 仍按 priority 分层，高优先级 tier 优先。
- `excludeFirstPriority` 仍跳过最高优先级 tier。
- 已禁用或处于熔断窗口的 channel 仍不会被选中。
- `RecordHealth` 会同步 selector 的运行时健康状态。

---

## 三、仍未完成的问题

### ❌ P0-1: http.go 仍未拆分

```bash
$ wc -l internal/relay/server/http.go
2391 internal/relay/server/http.go
```

本次修复没有拆 `http.go`。原因是该文件承载当前生产路由和大量已覆盖行为，直接大拆风险高。后续应在 handler/forwarder/orchestrator 行为稳定后，按端点分批迁移。

### ⚠️ P0-3: 仍有模块未接入主热路径

已接入：

- `WeightedSelector` -> `internal/channel/biz.ChannelUsecase.SelectChannel`
- `StreamForwarder` / `NonStreamForwarder` -> `relayOrchestrator.Execute`
- `ChatHandler` / `CompletionsHandler` -> 可写真实响应，但尚未注册到生产 `HTTPServer.RegisterRoutes`

仍需继续接入：

- `AuthCache` / `ChannelCache`
- `AsyncBillingUsecase`
- `StreamEventBus` 替换默认 `MemoryEventBus`
- `ResilientClient` / `CircuitBreakerManager`
- `IdempotencyMiddleware`
- `AuditEvent`
- graceful drain / mTLS / PartitionManager

---

## 四、验证结果

已通过：

```bash
go test ./internal/relay/server/... ./internal/channel/biz ./internal/relay/biz ./internal/relay/provider ./internal/pkg/cache ./internal/pkg/grpc ./internal/pkg/events ./internal/billing/biz
```

覆盖重点：

- handler 不再成功后空响应。
- orchestrator 可以真实转发 upstream raw request。
- forwarder 不再返回 nil placeholder。
- channel 选路使用 `WeightedSelector`，原有 channel 业务测试保持通过。
- REVIEW_v2 已确认通过的 P1 模块测试保持通过。

---

## 五、下一步建议

优先级建议：

1. 将 `ChatHandler` / `CompletionsHandler` 分阶段注册到 `HTTPServer.RegisterRoutes`，用现有 `http.go` 测试做行为对齐。
2. 在 relay-gateway 初始化 `AuthCache` / `ChannelCache`，替换直接 gRPC 查询路径。
3. 将 `AsyncBillingUsecase` 接入计费路径，保留失败时同步结算回退。
4. 用配置开关把 `StreamEventBus` 接入 channel 事件总线，默认可先保持 MemoryEventBus。
5. 完成上述接入后，再拆分 `http.go`，避免先拆文件后行为漂移。

---

**当前结论**: **P0 已部分落地，最危险的 handler/forwarder 空响应问题已修复；但架构重构仍未完全上线，后续重点是缓存、异步计费、事件总线和路由迁移。**
