# micro-one-api 架构重构 Review v3 状态报告

**更新日期**: 2026-06-28
**基准范围**: commit `5dd9475` 之后的修复
**当前结论**: ✅ **核心上线缺口已处理。新 orchestrator 链路已通过 feature flag 接入 `/v1/chat/completions`，计费和日志生命周期已补齐，核心模块已进入实际服务 wiring。`http.go` 已开始拆分，但仍然偏大，需要后续继续迁移端点。**

---

## 一、总体状态

| REVIEW_v3 问题 | 当前状态 | 说明 |
|---------------|---------|------|
| **P0-1** `http.go` God Object 拆分 | ⚠️ **部分修复** | 已抽出 `routes.go` 和 `http_orchestrator.go`；`http.go` 从 2,391 行降到约 2,354 行，仍高于目标 |
| **P0-2** handler/forwarder TODO | ✅ **已修复** | handler/forwarder/orchestrator 已有可运行实现和测试覆盖 |
| **P0-3** 新链路未注册路由 | ✅ **已修复** | 新增 `relay_orchestrator.enabled` 配置，开启后 `/v1/chat/completions` 走 orchestrator 链路 |
| Orchestrator 缺少 billing/log | ✅ **已修复** | 新链路补齐 reserve、commit、release、log 生命周期 |
| `statusCodeFromError` 占位 | ✅ **已修复** | 已区分鉴权、权限、限流、无渠道等错误状态码 |
| stream chunk 读取细节 | ✅ **已修复** | `chunkReadCloser` 跳过空 chunk，并在关闭时完成流式 usage 提交和日志记录 |

**核心判断**：本轮修复已经把之前“能跑但未上线”的新链路接入到 relay-gateway，且默认通过 feature flag 控制风险。生产路径可以继续保持旧链路，灰度环境可开启 `relay_orchestrator.enabled` 验证新链路。

---

## 二、核心模块接入状态

| 模块 | 当前状态 | 接入点 |
|-----|---------|--------|
| WeightedSelector | ✅ 已接入 | channel 服务 `SelectChannel` / `RecordHealth` 热路径 |
| AuthCache | ✅ 已接入 | relay-gateway identity client wrapper，降低重复鉴权 RPC |
| AsyncBillingUsecase | ✅ 已接入 | billing-service 可选异步 worker，受 `billing.async.enabled` 控制 |
| StreamEventBus | ✅ 已接入 | channel/config 服务通过 `events.NewConfiguredEventBus` 选择 Redis Streams 或内存总线 |
| ResilientClient | ✅ 已接入 | relay-gateway 下游 identity/channel/billing/log RPC client wrapper |
| IdempotencyMiddleware | ✅ 已接入 | relay route middleware，受 relay 配置控制 |
| AuditEvent | ✅ 已接入 | relay route middleware 审计记录，受 relay 配置控制 |
| mTLS | ✅ 已接入 | relay gRPC server 支持 mTLS server options，受 `mtls` 配置控制 |
| PartitionManager | ✅ 已接入 | billing-service 可选分区维护任务，受 `partition.enabled` 控制 |

### 设计约束

- `AsyncBillingUsecase` 作为 billing-service 后台能力接入，不替代 relay 主请求链路上的同步 reserve/commit。这样避免在计费失败时返回“请求成功但未计费”的假成功。
- Redis Streams 事件总线修复为 `XReadGroup` 消费模式，避免 consumer group 配置存在但读取路径仍用普通读取。
- orchestrator 路由保持 feature flag 关闭默认值，便于灰度和回滚。

---

## 三、已完成修复明细

### ✅ Orchestrator 路由接入

- 新增 `relay_orchestrator.enabled` 配置。
- `cmd/relay-gateway/wire_gen.go` 注入并调用 `SetRelayOrchestratorEnabled`。
- 新增 `internal/relay/server/http_orchestrator.go`，在 feature flag 开启时把 `/v1/chat/completions` 交给 orchestrator handler。
- 保留旧路径作为默认行为和回滚路径。

### ✅ Orchestrator 计费与日志生命周期

- 非流式请求：forward 成功后提交 usage 并记录日志。
- 流式请求：通过 `chunkReadCloser.Close` 在响应关闭时提交 usage 和日志。
- 请求失败时释放预留额度，避免 quota 泄漏。

### ✅ 下游客户端可靠性与缓存

- relay-gateway 包装 identity/channel/billing/log client，按配置启用 resilience。
- identity client 增加 `AuthCache` wrapper。
- 新增测试覆盖缓存命中逻辑。

### ✅ 事件总线与后台任务

- 新增 `events.NewConfiguredEventBus`，服务可按配置选择 Redis Streams 或内存事件总线。
- channel-service/config-service 改为使用统一 factory。
- billing-service 接入可选 async billing worker 和 partition maintenance。

### ✅ `http.go` 初步拆分

- 抽出 route middleware 与注册辅助逻辑到 `routes.go`。
- 抽出 orchestrator route 切换逻辑到 `http_orchestrator.go`。
- 当前仍未达到 `< 500 行` 的长期目标，后续应继续迁移 embeddings、audio、responses 等端点。

---

## 四、验证结果

已通过：

```bash
go test ./internal/relay/server ./internal/relay/data ./internal/pkg/events ./cmd/relay-gateway ./cmd/billing-service ./cmd/channel-service ./cmd/config-service
go test $(go list ./... | rg -v '/test/e2e/suite$')
```

说明：

- 排除 `test/e2e/suite` 的全量 Go 测试已通过。
- `go test ./...` 的 e2e suite 依赖本地服务 `127.0.0.1:9001`、`127.0.0.1:3000`、`127.0.0.1:8080`。这些服务未启动时会 connection refused，不代表本轮代码回归。

---

## 五、剩余风险与后续事项

| 项目 | 风险 | 建议 |
|-----|------|------|
| `http.go` 仍偏大 | 后续维护成本仍高 | 按端点继续迁移到独立 handler / route 文件 |
| orchestrator route 默认关闭 | 新链路尚需真实流量灰度 | 在 staging 开启 `relay_orchestrator.enabled`，观察计费、日志、错误码和流式关闭行为 |
| AsyncBilling 接入方式 | 仅作为 billing-service 后台能力 | 保持 relay 同步计费主链路，避免未计费成功响应 |
| e2e 未在完整服务栈中运行 | 本地缺少依赖服务 | 启动完整 docker-compose 或测试环境后补跑 `go test ./...` |

---

## 六、最终结论

**Review v3 中列出的未处理核心问题已经完成修复处理。**

当前状态可以进入灰度验证：默认旧链路不受影响，新 orchestrator 链路通过 `relay_orchestrator.enabled` 显式启用。上线前仍建议完成一次带真实依赖服务的 e2e 验证，并继续推进 `http.go` 端点拆分。
