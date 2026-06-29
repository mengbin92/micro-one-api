# grpc-gateway 迁移 TODO

> 状态：待办 | 记录时间：2026-06-28
>
> 背景：仓库目前所有服务的 HTTP 入口（`internal/*/server/http.go`）都使用 Kratos
> `khttp.Server.HandleFunc` 手写挂路由，`grpc-gateway/v2` 仅作为 indirect 依赖、
> 未真正启用。本文档记录后续可逐步推进的迁移点。

## 已完成

- [x] `api/admin/v1/admin.proto` 中 5 个 `SubscriptionAccount` RPC
  补齐 `google.api.http` 注解（GET/POST/PUT/DELETE/PUT），`make api` + `make api-check`
  通过，`openapi.yaml` 已同步。

## 待办（按推荐优先级）

### P0 — 干净能迁：标准 CRUD 服务

目标：proto 注解已齐 + HTTP 路径与手写路由 1:1 对应，迁完收益明确。

- [x] **config** 服务：`api/config/v1/config.proto` 4 个 RPC 注解齐全，迁移
  `internal/config/server/http.go` → grpc-gateway runtime mux。
- [x] **log** 服务：`api/log/v1/log.proto` 3 个 RPC 注解齐全，迁移
  `internal/log/server/http.go`。注意 `ServiceAuth` 中间件（`SERVICE_TOKEN`）
  需要在 gateway 之外作为 HTTP middleware 保留。
- [x] **monitor** 服务：`api/monitor/v1/monitor.proto` 8 个 RPC 注解齐全，迁移
  `internal/monitor/server/http.go`。
- [x] **notify** 服务：`api/notify/v1/notify.proto` 4 个 RPC 注解齐全，迁移
  `internal/notify/server/http.go`。

### P1 — 半迁：admin 服务

- [x] admin 服务中已配 `google.api.http` 注解的 RPC（当前 13 个，含本次新增的
  5 个 `SubscriptionAccount`）可走 gateway；其余 `/api/...` oneAPI 兼容路径保留
  手写，避免破坏前端兼容层。

### P2 — 工具链与依赖

- [x] 引入 `protoc-gen-grpc-gateway`，把 `github.com/grpc-ecosystem/grpc-gateway/v2`
  从 indirect 提升为 direct 依赖。
- [x] 在 `Makefile` 的 `api` 目标加上 `--grpc-gateway_out=paths=source_relative:.`。
- [x] 为每个目标服务写一个 `gateway.go`（`runtime.NewServeMux` +
  `RegisterXxxHandlerFromEndpoint`），在 `cmd/<service>-api/wire.go` 中接入。
- [x] 保留 `/metrics`、`/healthz` 路由（不走 gateway）。

### P3 — 不迁 / 单独评估

| 服务 | 原因 |
|---|---|
| `relay` | `/v1/chat/completions` 流式响应、anthropic 兼容、WebSocket forwarder、`/v1/responses` fallback 均为自定义 HTTP 语义，grpc-gateway 不支持服务端 streaming。 |
| `identity` | 当前 `internal/identity/server/http.go` 直接调 `biz.IdentityUsecase`，**不经过** `IdentityService` 这个 gRPC service；`Login/Register/CreateAccessToken` 等 11 个 RPC 一旦迁 gateway 需要先补完 gRPC server 实现，需独立评估。 |
| `billing` | `/v1/reconciliation`、支付宝回调是**入站 webhook**，grpc-gateway 假定"你 → gRPC server"，方向相反。其余 RPC 已被 `relay`/`admin`/`identity` 用 gRPC 客户端直调，迁 gateway 收益有限。 |
| `channel` | 内部服务，`internal/channel/server/http.go` 无业务 handler，无需迁。 |

## 验证清单（每次迁移一个服务后跑）

- [x] `make api` 通过
- [x] `go build ./...` 通过
- [x] `go test ./internal/<service>/...` 通过
- [ ] 启动该服务后 `curl` 验证一条 GET / POST / PUT / DELETE 路径行为与手写版本一致
- [ ] 确认 `/metrics`、`/healthz` 仍可访问

## 参考

- 现有 `relay.proto` 中 `option (google.api.http) = { post: "/v1/chat/completions" }` 是注解样例
- `docs/ARCHITECTURE_REFACTOR.md` 中关于传输层统一的相关章节
