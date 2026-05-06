# Proto 补全与 gRPC 服务对齐方案

> 设计文档 `One-API基于Kratos的微服务落地方案.md` Section 17/18 标记的遗留项。
> 方案先行，实施前对齐。

## 1. 差距分析

### 1.1 relay-gateway gRPC server 空壳

**现状**：
- `api/relay/v1/relay.proto` 已定义 `ChatCompletion` + `ListModels` RPC
- `internal/relay/biz/relay.go` 有完整 `RelayUsecase`（Plan + Execute）
- `internal/relay/server/grpc.go` 的 `NewGRPCServer()` 是空函数
- relay-gateway 仅通过 HTTP 提供服务

**影响**：其他服务无法通过 gRPC 调用 relay（如 admin-api 代理 chat 请求）

### 1.2 monitor proto 缺 RPC

**现状**：
- `api/monitor/v1/monitor.proto` 定义了 5 个 RPC：SaveHealthCheck / ListHealthChecks / GetLatestHealthCheck / CreateAlertRule / ListAlertRules
- `internal/monitor/biz/monitor.go` 额外有：GetAlertRule / UpdateAlertRule / DeleteAlertRule
- proto 和 service 层均未暴露 Get/Update/Delete AlertRule

**影响**：告警规则只能创建和列表，无法查询单个、更新、删除

### 1.3 monitor HTTP 缺路由

**现状**：
- `internal/monitor/server/http.go` 有：HandleRecordHealthCheck / HandleListHealthChecks / HandleListAlertRules / HandleCreateAlertRule
- 缺少：HandleGetAlertRule / HandleUpdateAlertRule / HandleDeleteAlertRule

**影响**：HTTP API 告警规则管理不完整

## 2. 实施方案

### 2.1 relay-gateway gRPC server

**修改文件**：
1. `internal/relay/service/relay.go` — 新增 `RelayGrpcService` 实现 proto 接口
2. `internal/relay/server/grpc.go` — 实现 `NewGRPCServer` 注册服务
3. `cmd/relay-gateway/wire_gen.go` — 更新依赖注入

**实现设计**：
```go
// internal/relay/service/relay.go
type RelayGrpcService struct {
    relayv1.UnimplementedRelayServiceServer
    uc *biz.RelayUsecase
}

func (s *RelayGrpcService) ChatCompletion(ctx context.Context, req *relayv1.ChatCompletionRequest) (*relayv1.ChatCompletionResponse, error)
func (s *RelayGrpcService) ListModels(ctx context.Context, req *relayv1.ListModelsRequest) (*relayv1.ListModelsResponse, error)
```

**注意**：
- gRPC 的 ChatCompletion 不支持流式（stream），仅支持同步请求/响应
- 流式场景仍走 HTTP SSE
- 鉴权通过 gRPC metadata 传递 token

### 2.2 monitor proto 补全

**修改文件**：
1. `api/monitor/v1/monitor.proto` — 添加 3 个 RPC + message
2. 重新生成 `*.pb.go` 和 `*_grpc.pb.go`
3. `internal/monitor/service/monitor.go` — 实现新增的 3 个 RPC
4. `internal/monitor/server/http.go` — 添加 3 个 HTTP 路由

**新增 proto 定义**：
```protobuf
rpc GetAlertRule(GetAlertRuleRequest) returns (GetAlertRuleResponse);
rpc UpdateAlertRule(UpdateAlertRuleRequest) returns (UpdateAlertRuleResponse);
rpc DeleteAlertRule(DeleteAlertRuleRequest) returns (DeleteAlertRuleResponse);

message GetAlertRuleRequest {
  int64 id = 1;
}
message GetAlertRuleResponse {
  AlertRuleItem rule = 1;
}
message UpdateAlertRuleRequest {
  int64 id = 1;
  string name = 2;
  string service_name = 3;
  string metric = 4;
  double threshold = 5;
  string operator = 6;
  int32 duration = 7;
  bool enabled = 8;
}
message UpdateAlertRuleResponse {
  bool success = 1;
}
message DeleteAlertRuleRequest {
  int64 id = 1;
}
message DeleteAlertRuleResponse {
  bool success = 1;
}
```

### 2.3 设计文档更新

**修改文件**：
- `docs/One-API基于Kratos的微服务落地方案.md` — 更新 Section 16/17/18 状态

## 3. 实施顺序

```
Phase 1: monitor proto 补全
  ├── proto 新增 3 个 RPC
  ├── 生成 Go 代码
  ├── service 层实现
  └── HTTP 路由补全

Phase 2: relay gRPC server
  ├── 新增 RelayGrpcService
  ├── server/grpc.go 实现
  └── wire 更新

Phase 3: 文档更新 + 验证
  ├── 设计文档状态更新
  ├── go build ./...
  └── go test ./...
```

## 4. 验证标准

- [x] `go build ./...` 通过
- [x] `go test ./...` 全部通过
- [x] monitor 告警规则完整 CRUD（gRPC + HTTP）
- [x] relay-gateway gRPC server 可接收 ChatCompletion 请求
- [x] 设计文档状态与实际实现一致

## 5. 实施完成状态

| # | 任务 | 状态 | 修改文件 |
|---|------|------|----------|
| 1 | monitor proto 补全 | ✅ 完成 | `api/monitor/v1/monitor.proto`, `internal/monitor/service/monitor.go`, `internal/monitor/server/http.go` |
| 2 | relay gRPC server | ✅ 完成 | `internal/relay/service/relay.go`, `internal/relay/server/grpc.go`, `cmd/relay-gateway/wire_gen.go`, `internal/relay/config/config.go`, `configs/relay-gateway.yaml` |
| 3 | 文档更新 | ✅ 完成 | `docs/One-API基于Kratos的微服务落地方案.md` |
