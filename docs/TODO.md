# 项目 TODO

> 最后更新：2026-07-18
>
> 当前阶段重点：Phase 2.2（渠道加权选择运行时验证）已完成。relay-gateway → channel-service 的 `SelectChannel` 现在有一份端到端集成测试（`TestRelaySelectChannel_WeightedDistribution`）证明选路实际走到 `WeightedSelector`（同优先级不同 Weight 的两渠道，高权重命中严格更多；`WeightedSelector` 的 Weight / CurrentWeight / Inflight / P95Latency 被真实填充）；channel-service HTTP 新增 `GET /api/v1/admin/channels/selector/stats`（`ADMIN_TOKEN` 守卫）供运行时观察 selector 状态。下一步推进 2.4 数据库 Schema 隔离 / 2.5 配置热更新。
>
> 历史进度：Phase 1 的 `http.go` God Object 拆分（`9e40559`，2470→472 行）、gRPC 熔断器、本地缓存层 L1、Redis Streams 事件总线均已落地；Phase 0 可观测性基线已填充，原始结果归档在 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`；Phase 2.1 异步计费、Phase 2.3 批量日志、Phase 3.3 WebSocket 优雅排空、Phase 2.2 渠道加权选择运行时验证均已接入。
>
> 架构重构总方案见 [design/ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md)，性能基线表见 [design/BASELINE.md](./design/BASELINE.md)。

## P0 — Phase 2/3 现状核对（已完成）

> git 历史中存在 Phase 2（`d63fb72`）与 Phase 3（`810097f`）的脚手架提交，但此前 TODO 仍将 Phase 2 列为待启动。需先核对这些代码是否真正接入生产路径，避免重复造轮子或漏掉半成品。

### [x] 核对 Phase 2/3 脚手架接入状态

关联提交：

- `920fb3c feat(phase1): implement P0 reliability fixes - circuit breaker, cache, streams, indexes`
- `d63fb72 feat(phase2): implement P1 performance optimizations - async billing, weighted selector, batch logs, schema migration`
- `810097f feat(phase3): implement P2 enhancements - partitioning, idempotency, drain, audit, mtls`

核对清单（Phase 2，对应 ARCHITECTURE_REFACTOR.md §10.1 Phase 2）：

- [x] **2.1 异步计费路径** — **半接入（死代码）**：`app/billing/internal/biz/async_billing.go` 存在；`wire_gen.go:100-102` 构建了 `asyncBilling *biz.AsyncBillingUsecase`（受 `cfg.Billing.Async.Enabled` 开关控制），但该变量**仅用于 `asyncBilling.Close()`**（`wire_gen.go:190`），`BillingService.CommitQuota`（`app/billing/internal/service/billing.go:79`）仍走同步路径 `s.uc.CommitQuotaWithUsageAndSplit`，**从未调用 `asyncBilling.Settle`**。relay-gateway 的 `http_billing.go` 也只做同步 gRPC `CommitQuota`。结论：异步计费代码已迁移到新结构但未真正接入结算热路径，属半成品。
- [x] **2.2 渠道加权选择算法** — **已接入**：`app/channel/internal/biz/selector.go` 实现 `WeightedSelector`，被 `ChannelUsecase` 持有（`channel.go:269,282`）并在 `Select`（`channel.go:333`）与 `RecordHealth`（`channel.go:567-568`）中调用。channel-service 内部已启用。
- [x] **2.3 日志批量写入** — **仅脚手架（死代码）**：`app/log/internal/biz/batch_writer.go` 存在并实现完整，但 `LogUsecase.IngestLog`（`app/log/internal/biz/log.go:103`）直接走 `uc.repo.Create` 同步写库，wire 未构建 `BatchLogWriter`，service 层未引用。属未接入的死代码。
- [x] **2.4 数据库 Schema 隔离** — **未实现**：各服务 `config.yaml` 无独立 schema 配置，9 服务仍共享同一 MySQL 库。
- [x] **2.5 配置热更新机制** — **未实现**：`platform/config/` 无 hotreload / fsnotify / watch 机制。

核对清单（Phase 3，对应 §10.1 Phase 3）：

- [x] **3.1 日志表分区** — **已接入（但 DDL 被排除出自动迁移）**：`migrations/phase3_partitioning.sql` 存在；`app/log/cmd/log/partition.go` 实现 `startPartitionMaintenance`，并在 `wire.go:60-74` 接入运行时维护（受 `cfg.Partition` 开关控制）。CHANGELOG 注明该 SQL 已排除出自动 migrate，需手动应用。
- [x] **3.2 幂等中间件** — **已接入（条件启用）**：`platform/middleware/idempotency.go` 存在；`cmd/relay-gateway/wire.go:301` / `wire_gen.go:315` 以 `NewIdempotencyMiddleware(redisClient, ...)` 接入路由中间件链。
- [x] **3.3 WebSocket 优雅排空** — **仅脚手架（死代码）**：`platform/websocket/graceful.go` 实现了 `ConnectionTracker` / `DrainConfig`，但**无任何非 test 文件 import `platform/websocket`**；`internal/server/openai_ws_*` 用自己的 `graceful bool` 字段（`openai_ws_relay.go:58,336,359`），未使用 `ConnectionTracker`。属未接入的死代码。
- [x] **3.4 审计日志** — **已接入（条件启用）**：`platform/audit/audit.go` 存在；`cmd/relay-gateway/wire.go:307-308` / `wire_gen.go:321-322` 在 `cfg.Audit.Enabled` 时以 `audit.NewMiddleware(audit.NewAuditor(true)).Handler` 接入路由中间件链。
- [x] **3.5 gRPC mTLS** — **已接入（条件启用）**：`platform/grpc/mtls.go` 存在；`cmd/relay-gateway/wire.go:334-339` / `wire_gen.go:346-351` 在 `cfg.MTLS.Enabled` 时调用 `MTLSServerOptions` 注入 relay gRPC server 选项。

产出：见上方核对清单，每项已标注真实状态。汇总：

| 项 | 状态 | 结论 |
|----|------|------|
| 2.1 异步计费 | 半接入 | wire 构建但未接入结算热路径，需把 `CommitQuota` 改走 `asyncBilling.Settle` |
| 2.2 加权选路 | 已接入 | channel-service 内部已用 |
| 2.3 批量日志 | 仅脚手架 | `BatchLogWriter` 未接入 `LogUsecase` |
| 2.4 Schema 隔离 | 未实现 | 需新建 |
| 2.5 配置热更新 | 未实现 | 需新建 |
| 3.1 日志分区 | 已接入 | 运行时维护已接，DDL 需手动应用 |
| 3.2 幂等中间件 | 已接入 | relay-gateway 路由已用 |
| 3.3 WS 优雅排空 | 仅脚手架 | `ConnectionTracker` 未被 `openai_ws_*` 使用 |
| 3.4 审计日志 | 已接入 | relay-gateway 路由已用（开关） |
| 3.5 gRPC mTLS | 已接入 | relay-gateway gRPC server 已用（开关） |

验收标准：

- [x] 每个项明确标注：已接入生产、部分接入、仅脚手架、或不存在。
- [x] 引用具体文件 / wire_gen.go 行号作为证据。
- [x] 据核对结果重排 Phase 2 任务优先级（见下方"下一步推进顺序"）。

### Phase 2 推进顺序（据核对结果重排）

基于核对结果，按"激活半接入 → 接入死代码 → 新建缺失项"的顺序推进：

1. ~~**2.1 异步计费接入结算热路径**~~ ✅ 已完成。
2. ~~**2.3 日志批量写入接入 `LogUsecase`**~~ ✅ 已完成（见上方"已完成 — Phase 2.3"小节）。
3. ~~**3.3 WebSocket 优雅排空接入 `openai_ws_*`**~~ ✅ 已完成（见下方"已完成 — Phase 3.3"小节）。
4. ~~**2.2 渠道加权选择**~~ ✅ 已完成（见下方"已完成 — Phase 2.2"小节）。
5. **2.4 数据库 Schema 隔离 / 2.5 配置热更新**：结构性新建项，风险较高，放最后。

## 已完成 — Phase 2.3 日志批量写入接入 LogUsecase

### [x] 2.3 日志批量写入接入 LogUsecase

关联设计：[架构重构方案 §10.1 Phase 2 / §10.2](./design/ARCHITECTURE_REFACTOR.md)

本次完成：

- `LogRepo` 接口旁新增可选能力接口 `LogRepoBatch`（`CreateBatch(ctx, []*LogEntry)`），`BatchLogWriter` 探测并 fallback 到逐条 `Create`。
- `LogUsecase` 新增 `batchWriter *BatchLogWriter` 字段 + `SetBatchWriter` setter；`IngestLog` 在 `batchWriter != nil` 时入队（非阻塞，队列满返回错误），否则走原同步 `repo.Create`。
- `batch_writer.go` 的 `createBatch` 改为接收 `ctx` 并调用带 ctx 的 `CreateBatch`；删除文件末尾重复的 `LogRepoBatch` 定义和注释掉的实现 stub（接口现归 `log.go` 权威定义）。
- `data.Repository` 实现 `CreateBatch`：gorm `CreateInBatches` 单次 INSERT 多行，回写生成 ID 到源 entry；内存模式按 `seq` 逐条追加。
- `conf.LogSVCConfig` 新增 `BatchEnabled`（默认 false）+ `BatchFlushInterval`（默认 1s）配置项。
- `wire.go` / `wire_gen.go`（经 `wire` 重生成）在 `newApp` 中按 `cfg.Log.BatchEnabled` 构建 `BatchLogWriter`，`Start`/`Stop` 生命周期挂到 app cleanup，并 `uc.SetBatchWriter`。
- 新增 4 个测试：`IngestLogSyncFallback`、`IngestLogBatchRouting`、`IngestLogNilBatchWriterIsSafe`、`BatchLogWriter_QueueFullReturnsError`。

改动文件：

- `app/log/internal/biz/log.go`（`LogRepoBatch` 接口 + `LogUsecase.batchWriter` 字段 + `IngestLog` 分支）
- `app/log/internal/biz/batch_writer.go`（`createBatch` 接收 ctx；去重 `LogRepoBatch`）
- `app/log/internal/biz/log_test.go`（4 个批量路由/队列满测试）
- `app/log/internal/data/data.go`（`Repository.CreateBatch`）
- `app/log/internal/conf/config.go`（`BatchEnabled` + `BatchFlushInterval`）
- `app/log/cmd/log/wire.go` / `wire_gen.go`（构建 + 生命周期 + 注入 + `parseLogFlushInterval`）

验收标准：

- [x] `cfg.Log.BatchEnabled=false`（默认）时 `IngestLog` 行为与改动前一致（同步 `repo.Create`，返回 ID）。
- [x] `cfg.Log.BatchEnabled=true` 时 `IngestLog` 入队返回，不阻塞；队列满返回错误而非静默丢弃；`Flush` 后 `CreateBatch` 被调用并回写 ID。
- [x] `go build ./...` 通过；`go vet ./app/log/... ./app/billing/... ./cmd/relay-gateway/...` 通过。
- [x] `go test ./app/log/...` 全部通过（biz / data / integration / server）。

## 已完成 — Phase 2.2 渠道加权选择运行时验证

### [x] 2.2 渠道加权选择运行时验证

关联设计：[架构重构方案 §4.4 渠道加权选择算法（P1）](./design/ARCHITECTURE_REFACTOR.md)

核对结论回顾：`WeightedSelector` 本体早已接入（`ChannelUsecase` 持有，`SelectChannel` 调 `selector.Select`、`RecordHealth` 调 `selector.RecordHealth`），但缺少"relay-gateway 经 channel-service 选路实际走到 WeightedSelector"的端到端证据，也没有 selector 状态的运行时观察面。本次补齐这两项。

本次完成：

- `ChannelUsecase` 新增 `SelectorStats() map[int64]ChannelStats` 访问器，nil-safe 转发到 `WeightedSelector.GetStats()`，作为 selector 运行时状态的对外观察接缝。
- `app/channel/testutil/testutil.go` re-export `ChannelStats` 类型别名 + `SelectorStats(uc)` 自由函数，使跨服务集成测试与 admin 工具可观察 selector 状态。
- channel-service HTTP 新增 `GET /api/v1/admin/channels/selector/stats` 端点，`ADMIN_TOKEN` 常量时间比较守卫（未设置时 fail-closed 返回 401），返回 `{"channels": {<channelID>: {weight, current_weight, inflight, p95_latency, error_rate, is_circuit_open}}}` JSON。运维和验证脚本可直接 `curl` 观察选路是否实际落到 `WeightedSelector`。
- 新增 `internal/integration/weighted_distribution_test.go::TestRelaySelectChannel_WeightedDistribution`：两个同 Priority（10）但不同 Weight（100 / 1）的 OpenAI 兼容渠道 + 共享 mock 上游，连续发 40 次 `/v1/chat/completions`，断言四条不变式：
  1. 上游命中总数 == 请求数（无重复转发）。
  2. 高权重渠道命中严格 > 低权重（区分加权选路与均匀随机的强证据：均匀随机下 40 次全高的概率仅 `(0.5)^40 ≈ 9e-13`）。
  3. `SelectorStats` 两条渠道都登记、Weight=100/1、CurrentWeight 被改写（证明 `Select` 被调用而非旁路）、Inflight 归零（证明 `RecordHealth` 回路把 inflight 减回）。
  4. 被选渠道的 P95Latency > 0（证明 relay-gateway → `RetryExecutor.recordHealth` → gRPC `RecordChannelHealth` → `ChannelUsecase.RecordHealth` → `WeightedSelector.RecordHealth` 的健康反馈链路端到端打通）。
- 新增 `app/channel/internal/server/http_test.go`：`authorizeAdmin` 五种取值（含 fail-closed）+ `selectorStatsPayload` shape + 端点鉴权/方法/成功路径三组子测试。

改动文件：

- `app/channel/internal/biz/channel.go`（`SelectorStats()` 访问器）
- `app/channel/testutil/testutil.go`（`ChannelStats` 别名 + `SelectorStats` re-export）
- `app/channel/internal/server/http.go`（`registerSelectorStatsRoute` + `authorizeAdmin` + `selectorStatsPayload` + `crypto/subtle` / `os` / `strings` import）
- `app/channel/internal/server/http_test.go`（新增 selector stats 端点 + 鉴权单测）
- `internal/integration/weighted_distribution_test.go`（新增 Phase 2.2 端到端验证测试）

验收标准：

- [x] `WeightedSelector` 仍是 `ChannelUsecase.SelectChannel` / `RecordHealth` 的实际选路与反馈路径（核对结论不变）。
- [x] `TestRelaySelectChannel_WeightedDistribution` 通过：高权重 40/40、低权重 0，`SelectorStats` 两渠道 Weight/CurrentWeight/Inflight/P95Latency 全部符合预期。
- [x] channel-service `GET /api/v1/admin/channels/selector/stats` 在 `ADMIN_TOKEN` 未设置时 401、正确 token 时返回 `{"channels": {...}}` JSON。
- [x] `go build ./...` 通过；`go vet ./app/channel/... ./internal/integration/...` 通过。
- [x] `./scripts/check-architecture.sh` 通过；`make test-unit` 全绿（含新增 2 个 selector stats / 鉴权测试 + 1 个加权分布集成测试 + 既有 channel / integration / server 测试）。

## 已完成 — Phase 3.3 WebSocket 优雅排空接入 openai_ws_*

### [x] 3.3 WebSocket 优雅排空接入 `openai_ws_*`

关联设计：[架构重构方案 §10.1 Phase 3 / §11.3](./design/ARCHITECTURE_REFACTOR.md)

本次完成（激活 `platform/websocket/graceful.go` 死代码脚手架）：

- `HTTPServer` 新增 `wsConnTracker *appws.ConnectionTracker` + `wsDrainCfg appws.DrainConfig` 字段；`SetOpenAIWSConnPool` 构建 tracker（幂等），新增 `SetOpenAIWSDrainConfig` / `drainConfig` / `IsWSDraining` / `DrainWSConnections(ctx)` nil-safe 访问器（未 wire 时行为与改动前一致）。
- `handleResponsesWebSocket` 入口加排空门：`IsWSDraining()` 为真时直接返回 503 + `Retry-After`，不再 `coderws.Accept` 新连接；已升级连接在 relay 前 `NewConnection` 注册、defer `Unregister`，关闭回调走 `wsConn.CloseNow()`。
- `handleHealth`（`/healthz`）在排空期间返回 503 + `{"status":"draining","drain":"true"}` + `Retry-After: 30`，供 LB 摘流；稳态仍 200 `ok`。
- `internal/conf.OpenAIWSConfig` 新增 `DrainTimeout`（默认 30s）+ `GetOpenAIWSDrainTimeout()`；`configs/config.yaml` 补 `openai_ws.drain_timeout`（`OPENAI_WS_DRAIN_TIMEOUT`）。
- `cmd/relay-gateway/wire.go` / `wire_gen.go`（经 `wire` 重生成）在启动时 `SetOpenAIWSDrainConfig(appwsDrainConfig(wsDrain))`；`kratosOpts` 注入 `kratos.StopTimeout(drainTimeout+10s)` + `kratos.BeforeStop` 调 `httpServer.DrainWSConnections(drainCtx)`，实现 SIGTERM → 摘流 → 排空 → 强制关闭的完整链路。
- `platform/websocket/graceful.go` 新增 `SetDrainingForTest` 以便单测直接翻转 CAS 标志。

改动文件：

- `internal/server/http.go`（`wsConnTracker`/`wsDrainCfg` 字段 + `SetOpenAIWSConnPool`/`SetOpenAIWSDrainConfig`/`drainConfig`/`IsWSDraining`/`DrainWSConnections` + `appws`/`applogger`/`zap` import）
- `internal/server/http_status_handler.go`（`handleHealth` 排空分支）
- `internal/server/openai_ws_forwarder.go`（入口排空门 + relay 前 tracker 注册/Unregister + `appws` import）
- `internal/conf/config.go`（`DrainTimeout` + `GetOpenAIWSDrainTimeout`）
- `internal/conf/config_test.go`（`TestOpenAIWSDrainTimeoutDefault`）
- `cmd/relay-gateway/relay_helpers.go`（`appwsDrainConfig` + `appws` import）
- `cmd/relay-gateway/wire.go` / `wire_gen.go`（`SetOpenAIWSDrainConfig` + `BeforeStop(DrainWSConnections)` + `StopTimeout`）
- `configs/config.yaml`（`openai_ws.drain_timeout`）
- `platform/websocket/graceful.go`（`SetDrainingForTest`）
- `internal/server/openai_ws_drain_test.go`（新增 4 个测试：healthz 翻 503、排空期拒绝新升级、tracker 关闭活跃连接、HTTPServer 级 drain 关闭已注册连接）

验收标准：

- [x] 稳态（未排空）`/healthz` 仍返回 200 `{"status":"ok"}`；`handleResponsesWebSocket` 正常 accept + relay，行为与改动前一致。
- [x] `DrainWSConnections` 启动后 `IsWSDraining()` 为真，`/healthz` 返回 503 + `drain=true` + `Retry-After`，新 WebSocket 升级被 503 拒绝。
- [x] 已注册的 tracked 连接在 `drain_timeout` 内被关闭（优雅完成或强制关闭），`ActiveCount` 归零。
- [x] 未 wire tracker（`SetOpenAIWSConnPool` 未调）时 `IsWSDraining()` 恒假、`DrainWSConnections` no-op，不影响测试与默认禁用路径。
- [x] `go build ./...` 通过；`go vet ./internal/server/... ./cmd/relay-gateway/... ./internal/conf/... ./platform/websocket/...` 通过。
- [x] `go test ./internal/server/ ./platform/websocket/... ./internal/conf/...` 全部通过（含新增 4 个 drain 测试与既有 ws/pool/relay/stress/e2e 测试）。

## 已完成 — Phase 2.1 异步计费接入结算热路径

### [x] 2.1 异步计费接入结算热路径

关联设计：[架构重构方案 §10.1 Phase 2 / §10.2](./design/ARCHITECTURE_REFACTOR.md)

本次完成：

- `SettleTask` 扩展 `ReservationID` / `Success` / `Usage` 字段，使后台 worker 携带完整 commit 输入。
- `AsyncBillingUsecase.processSettlement` / `settleSync` 改为调用 `runCommitPipeline`，后者运行权威的 `BillingUsecase.CommitQuotaWithUsageAndSplit`（预约状态机 + 钱包结算 + 账本 + 订阅用量），不再走原先绕过预约状态机的 `BatchLedgerWriter.Add` 裸账本写入。
- `BillingService` 新增 `asyncUc` 字段 + `SetAsyncBillingUsecase` setter；`CommitQuota` 在 `asyncUc != nil && req.Success` 时入队 `Settle` 并立即返回临时成功响应（`CommittedAmount=0`，权威金额由 worker 写入），否则走原同步路径。`Success=false`（release）始终同步，保证调用方观察到已释放的预约。
- `wire.go` / `wire_gen.go`（经 `wire` 重生成）在 `svc.SetSubscriptionReportUsecase` 之后接入 `svc.SetAsyncBillingUsecase(asyncBilling)`。
- 新增 3 个契约测试：`SettleEnqueuesTask`、`SettleFallsBackWhenQueueFull`、`SettleNilTaskIsSafe`；`go test ./app/billing/...` 全部通过。

改动文件：

- `app/billing/internal/biz/async_billing.go`（`SettleTask` 扩展 + `Settle`/`settleSync`/`processSettlement`/`runCommitPipeline` 重写）
- `app/billing/internal/biz/async_billing_test.go`（新增 3 个契约测试 + `metrics`/`testutil` import）
- `app/billing/internal/service/billing.go`（`asyncUc` 字段 + setter + `CommitQuota` 分支）
- `app/billing/cmd/billing/wire.go` / `wire_gen.go`（接入 `SetAsyncBillingUsecase`）

验收标准：

- [x] `cfg.Billing.Async.Enabled=true` 时 `CommitQuota` 非阻塞返回，后台 worker 运行完整 commit 管线。
- [x] `cfg.Billing.Async.Enabled=false`（默认）行为与改动前完全一致（同步 `CommitQuotaWithUsageAndSplit`）。
- [x] `Success=false` 的 release 始终走同步路径，不会把失败请求的释放异步化。
- [x] `go build ./app/billing/... ./cmd/relay-gateway/... ./internal/server/...` 通过；`go vet ./app/billing/...` 通过。
- [x] `go test ./app/billing/... ./internal/server/...` 全部通过。

## 已完成 — 架构重构 Phase 1（原 P0）

> 依据 `docs/design/ARCHITECTURE_REFACTOR.md` §10.2。Phase 1 的其余 P0 项（gRPC 熔断器、本地缓存层 L1、Redis Streams 事件总线）已落地并在 `cmd/relay-gateway/wire.go` 中接入；`http.go` 拆分已于本次（`9e40559`）完成。

### [x] 拆分 `internal/server/http.go`

关联设计：[架构重构方案 §4.1 / §10.2](./design/ARCHITECTURE_REFACTOR.md)

本次完成（提交 `9e40559`）：

- 将 2470 行的 God Object `http.go` 拆分为 13 个聚焦文件，主体降至 472 行。
- 原始行为零变更：无新增/删除端点、无路由变更、无响应格式调整。
- `internal/server` 全量单元测试通过；生产环境（relay-gateway，linux/amd64）经 Kimi-K3、GLM-5.2 真实聊天转发验证正常。

拆分文件清单：

- 步骤 2（Forwarder）：`http_forwarder.go`（42 行，stream / nonstream raw 转发逻辑）。
- 步骤 3（BillingCoord）：`http_billing.go`（220 行，配额 reserve / commit / release 协调与超时降级）。
- 步骤 4（Handler 按端点拆分）：
  - `http_chat_handler.go`（251 行，`/v1/chat/completions`）
  - `http_responses_handler.go`（671 行，`/v1/responses`）
  - `http_raw_handler.go`（140 行，One-API 兼容 raw 透传）
  - `http_status_handler.go`（332 行，`/api/status`、`/api/models`、`/api/group`、`/healthz`、`/metrics`）
  - `http_oneapi_handler.go`（133 行，One-API 代理）
  - `http_unsupported_handler.go`（19 行，不支持端点的统一 501 响应）
- 步骤 5（Router / Middleware）：`routes.go`（83 行，`RegisterRoutes`）此前已提取，本次复用。
- 配套：`http_response.go`、`http_response_route.go`、`http_usage_log.go`、`http_helpers.go`、`http_config.go`。

任务（按风险从低到高顺序）：

- [x] 步骤 2：提取 `Forwarder`（stream / nonstream / ws 转发逻辑）到独立文件，复用现有 `http_raw_test.go` 做回归。
- [x] 步骤 3：提取 `BillingCoord`（reserve / commit / release 计费协调，含超时与降级），补单元测试 + 降级测试。
- [x] 步骤 4：按端点拆分 Handler 文件，使各 Handler 可独立测试。
- [x] 步骤 5：提取 `Router` 和 `Middleware`，补路由注册测试。
- [x] 步骤 6：验证所有端点测试通过（`internal/server` 单元测试 PASS + 生产环境真实流量验证）。

验收标准：

- [x] `internal/server/http.go` 行数大幅下降（2470 → 472）；剩余 472 行为 `HTTPServer` 结构体定义与运行时 Setter 配置方法，接近 <400 目标，后续可按需进一步抽离配置。
- [x] 每一步拆分后 `http_raw_test.go` 与 `make test-unit` 全部通过（`internal/server` 包测试 PASS）。
- [x] 拆分行为零变更：无新增/删除端点、无路由变更、无响应格式调整。
- [x] 生产环境真实流量验证：Kimi-K3（channel 4）、GLM-5.2（channel 1/3）聊天转发与 usage 上报正常。

## P1 — Phase 0 可观测性基线

> 依据 `docs/design/BASELINE.md`。当前基线表有 16 处 TBD，需先建立量化基线，为后续优化提供对比依据。

### [x] 填充性能基线数据

关联基线表：[design/BASELINE.md](./design/BASELINE.md)

现状：

- `docs/design/BASELINE.md` 中 P50/P95/P99 延迟、错误率、吞吐量、gRPC 服务调用延迟、缓存命中率、熔断器状态均为 TBD（共 16 处）。
- 压测脚本 `scripts/benchmark/k6-baseline.js` 已存在但未运行记录。

任务：

- [x] 在本地或预发环境按 `BASELINE.md` 的「How to Run」章节运行 `k6-baseline.js`。
- [x] 记录 `/healthz`、`/v1/models`、`/v1/chat/completions` 的 P50/P95/P99 与错误率。
- [x] 记录 identity / channel / billing / log 四个 gRPC 服务的调用延迟。
- [x] 记录 auth / channel 缓存的 L1/L2 命中率与 miss 率。
- [x] 记录各下游服务的熔断器状态与 24h trip 次数。
- [x] 将结果填入 `BASELINE.md` 的基线表，并写入 History 表首行。

验收标准：

- `BASELINE.md` 中不再有 TBD 占位项。
- 原始 `results.json` 保存归档，可在后续 Phase 对比。
- 记录测试环境的 CPU / 内存 / Go 版本 / Kratos 版本。

## 已完成

### [x] v0.8.0 发布

- API 指南页、CC Switch 一键导入、admin 前端改由 `ADMIN_WEB_ROOT` 提供。

### [x] 合并 OAuth 回调路由修复

- 等待 `develop` 提交 `2cb0a23` 的完整 CI 通过。
- 合并到 `main`。
- 评估并发布 `v0.7.2`：已于 2026-07-15 正式发布。

验收标准：

- GitHub CI 和 Security Pipeline 全部通过。
- OAuth 回调路由的相关单元测试通过。
- `main` 包含 `2cb0a23` 的修复。

### [x] 同步部署方式与部署文档

关联 Issue：[部署方式是否同步更新 #5](https://github.com/mengbin92/micro-one-api/issues/5)

- 统一部署文档与 K8s 清单中的数据库 Secret 名称：使用 `db-credentials`。
- 在文档中补充 `admin-tls-secret` 的创建步骤。
- 为 K8s `billing-service` 和 `log-service` 注入 `SERVICE_TOKEN`。
- 移除生产必需 Secret 上不合理的 `optional: true`。
- 核对 `config-service` 是否确实需要 `SERVICE_TOKEN`：代码不读取该变量，已从 Compose/K8s 移除。
- 文档说明如何替换 `your-registry/<service>:v0.7.2`，生产示例使用固定版本而非浮动 `latest`。
- 核对全部 ConfigMap、Secret、Service、Ingress 名称和端口引用。
- 验证全新 Docker Compose 部署。
- 使用 kind、k3d 或测试集群执行一次 K8s smoke test。

进度与完成记录：

- `2cb0a23` 的 GitHub CI 和 Security Pipeline 均已通过，`develop` 后续头提交的两条流水线也通过。
- kind v1.33.1 smoke 中，九个应用及 MySQL/Redis 均达到 `1/1 Running`；Admin Pod 可访问 billing/log 内部接口，共享令牌鉴权成功，Relay `/healthz` 成功。
- 全新 MySQL 已一次完成 55 项自动迁移；修复了 SQL 字符串内分号解析、`phase1_indexes.sql` 错误列名，并将可选 `phase3_partitioning.sql` 排除出自动迁移。
- 2026-07-15 使用独立 Compose project 和全新 MySQL/Redis 数据卷完成最终 smoke：一次性 `migrate` 成功退出，九个应用容器均正常运行，七个内部健康端点、log-service 共享令牌鉴权、Relay `/healthz` 和 `/v1/models` 未授权响应全部符合预期，共 23 项通过、0 项失败；测试结束后容器、网络和数据卷均已清理。
- `origin/main` 的 `942b58c` 已包含 OAuth 修复提交 `2cb0a23`；该提交对应的 main CI、Security Pipeline 和 v0.7.2 Release workflow 均已成功完成。

验收标准：

- 新环境可以仅按照 `docs/deployment.md` 完成部署。
- 所有 Pod 正常 Ready，Admin、Relay、billing/log 内部接口可访问。
- 文档中的 Secret 名称、环境变量和清单完全一致。

### [x] 增加软件界面图和用户向文档

关联 Issue：[希望能有软件界面图和文档 #6](https://github.com/mengbin92/micro-one-api/issues/6)

- 增加用户 Dashboard 截图。
- 增加 Token 管理和用量统计截图。
- 增加渠道管理或渠道健康截图。
- 增加成本分析截图。
- 增加订阅套餐或订阅账号截图。
- 增加日志详情或对账页面截图。
- 在 README 中新增“界面预览”章节。
- 增加简化架构图，以及“适合谁 / 不适合谁”说明。
- 补充从空环境部署到创建首个渠道和 Token 的最短流程。
- 修复当前文档重组后遗留的失效相对链接。
- 将 `docs/README.md` 的最新版本从 `v0.7.0` 更新为 `v0.7.1` 或当前最新版本。

建议截图目录：

```text
docs/assets/screenshots/
```

验收标准：

- 新用户在 README 中能快速了解项目界面、服务组成和主要能力。
- README 和 `docs/**/*.md` 的本地文件链接检查无错误。
- 截图不包含真实密钥、邮箱、用户数据或上游账号凭据。

### [x] 增加部署与文档漂移检查

- CI 执行 `docker compose config`。
- 使用 `kubeconform` 或同类工具校验 `deployments/k8s/*.yaml`。
- 增加 Markdown 本地链接检查。
- 对部署清单中的必需 Secret/ConfigMap 引用增加静态检查或 smoke test。

验收标准：

- Secret 名称、失效文档链接或非法 K8s 清单能够在 PR 阶段阻断 CI。

### [x] 重新评估 grpc-gateway 迁移计划

关联决策：[HTTP 转换机制决策](./migration/grpc-gateway-migration-todo.md)

- 标准 unary CRUD 继续使用 Kratos `protoc-gen-go-http` 生成的 HTTP handler。
- 评估 grpc-gateway runtime mux：当前部署和调用链没有足以抵消双运行时维护成本的明确收益，决定不引入。
- 流式响应、WebSocket、Webhook、OAuth 回调和 One-API 兼容路由继续使用自定义 HTTP 实现。
- 将原迁移 TODO 改为正式技术决策记录，grpc-gateway 迁移标记为不再推进。

评审结论（2026-07-15）：

- 标准 HTTP API 从手写 CRUD handler 逐步收敛到 Kratos 生成 handler，而不是迁移到 grpc-gateway。
- `config`、`log`、`monitor`、`notify` 在切换前先补 HTTP 契约测试，核对状态码、JSON 编码、错误格式、鉴权和分页行为。
- `log` 的批量删除、`monitor` 的 latest health check、`notify` 的状态更新存在 proto、生成路由与当前手写路由不一致，按资源单独决策和修复，不作为无行为变化的机械替换。
- 只有在需要独立统一 REST 网关、多语言 gRPC 后端或集中式 HTTP 转码层时，才重新评估 grpc-gateway。

验收标准：

- 路线图不再保留已经过期但没有明确决策的版本承诺。
- 明确只维护 Kratos 生成 HTTP 与必要的自定义 HTTP 两类机制，不增加重复的 grpc-gateway runtime。

## 基线检查

每个任务完成前至少执行：

```bash
./scripts/check-architecture.sh
make test-unit
cd web && npm test && npm run lint
```

涉及部署时追加：

```bash
cd deployments/docker-compose
docker compose config
```

涉及 Relay 行为的分支追加：

```bash
go test ./internal/relay/... ./internal/channel/...
make test-e2e-suite
```
