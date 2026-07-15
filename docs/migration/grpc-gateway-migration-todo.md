# HTTP 转换机制决策：不引入 grpc-gateway

> 状态：已接受
> 决策日期：2026-07-15
> 原状态：grpc-gateway 渐进迁移 TODO

## 结论

本项目不引入 grpc-gateway runtime。

标准 unary HTTP API 继续使用 `google.api.http` 注解，并由 Kratos
`protoc-gen-go-http` 生成 handler，直接注册到现有 `khttp.Server`。以下需要特殊
HTTP 语义的入口继续保留自定义实现：

- OpenAI、Anthropic 等兼容协议及 SSE 流式响应；
- WebSocket 转发；
- 支付回调和其他入站 Webhook；
- OAuth 回调；
- One-API 兼容路由；
- admin BFF、静态资源和反向代理路由；
- `/metrics`、`/healthz` 等运维端点。

原 grpc-gateway 迁移计划不再推进。后续目标是减少标准 CRUD 的手写解析和响应
代码，而不是在 Kratos HTTP transport 之外再维护一套 HTTP 转码 runtime。

## 当前事实

仓库已经具备 Kratos 生成 HTTP handler 所需的完整链路：

- `Makefile` 的 `api` 目标已经执行
  `--go-http_out=paths=source_relative:.`；
- `config`、`log`、`monitor`、`notify`、`admin` 和 `relay-gateway` 已生成
  `*_http.pb.go`；
- 生成文件已经提供 `RegisterXxxHTTPServer`，但目前没有在手写 server 中注册；
- `github.com/grpc-ecosystem/grpc-gateway/v2` 虽然作为 indirect dependency 出现在
  `go.mod`，但来自 OpenTelemetry OTLP HTTP 依赖链，项目自身没有使用 gateway mux；
- 生产 Ingress 只对外暴露 `relay-gateway` 和 `admin-api`，其余服务为内部
  `ClusterIP`，当前没有独立统一 REST gateway 的部署需求。

因此，标准 CRUD 的重复代码可以直接通过现有 Kratos 生成 handler 收敛，无需增加
`protoc-gen-grpc-gateway`、`runtime.ServeMux`、gRPC endpoint 拨号和另一套 marshaler、
metadata、错误编码及中间件配置。

## 决策理由

### 1. 避免功能重复

Kratos `protoc-gen-go-http` 和 grpc-gateway 都能根据 `google.api.http` 注解生成
HTTP/JSON 转换层。当前服务已经使用 Kratos HTTP server，继续使用 Kratos 生成
handler 可以直接调用同一个 service 实现；引入 grpc-gateway 则会形成两套 HTTP
路由、序列化、错误映射和中间件机制。

### 2. 当前不需要独立 REST facade

grpc-gateway 更适合将一个独立 HTTP gateway 部署在多个 gRPC backend 前面。当前
项目的外部入口已经由 `relay-gateway` 和 `admin-api` 承担，内部服务调用主要使用
gRPC，部分兼容接口由 admin 反向代理。没有证据表明再增加一层统一 REST gateway
能够简化部署或调用链。

### 3. 自定义协议不能机械转码

relay 的流式接口不仅是通用服务端 streaming，还包含 SSE `data:` 帧、结束标记、
上游 header 和状态码透传，以及 OpenAI、Anthropic 的协议兼容语义。grpc-gateway
支持将通用服务端流映射为分块 JSON，但默认格式不能替代这些兼容协议。
WebSocket、Webhook 和 OAuth 回调也应继续由原生 HTTP handler 处理。

### 4. 迁移风险来自 HTTP 契约，而不是路由注册

生成 handler 和当前手写 handler 可能在以下方面产生行为差异：

- 成功状态码，例如手写创建接口可能返回 `201`，生成 handler 默认返回 `200`；
- proto JSON 对 `int64`、空字段和字段名的编码；
- 时间字段使用 Unix timestamp 还是 RFC 3339；
- 错误状态码和响应体格式；
- path/query/body 的绑定和校验顺序；
- 分页默认值；
- 鉴权失败的状态码和响应体；
- 404、405 和路由优先级。

所以后续切换必须以 HTTP 契约测试为前提，不能只验证路径相同。

## 各服务处理方式

| 服务 | 决策 |
|---|---|
| `config` | 4 个标准 RPC 使用 Kratos 生成 handler；`/api/notice`、`/api/about`、`/api/home_page_content` 保留自定义实现。 |
| `log` | `GetLog`、`ListLogs`、`IngestLog` 使用 Kratos 生成 handler；`ServiceAuth` 改为 Kratos transport middleware；One-API 用户日志路由保留自定义实现。批量删除需先补正式 RPC，或暂时保留手写 `DELETE /v1/logs`。 |
| `monitor` | 8 个标准 RPC 使用 Kratos 生成 handler；切换时明确启用当前只存在于 proto/OpenAPI 中的 `GET /v1/health-checks/latest`。 |
| `notify` | 4 个标准 RPC 使用 Kratos 生成 handler；同步补通 admin 对 `PUT /v1/notifications/{id}/status` 的代理，移除当前 `501` 占位行为。 |
| `admin` | 暂不整体替换。按资源逐组评估生成 handler，并保留 admin auth、BFF、静态资源、One-API 兼容和 reverse proxy 路由。 |
| `identity` | 暂不处理。当前大量 HTTP handler 直接调用 usecase，应先统一 transport/service 边界，再评估哪些 RPC 适合生成 HTTP。 |
| `billing` | 标准 unary RPC 可按实际 HTTP 消费需求使用 Kratos 生成 handler；支付宝回调、对账触发等特殊入口保留自定义实现。 |
| `relay-gateway` | 保留自定义 HTTP、SSE、WebSocket 和协议兼容实现，不使用通用 HTTP 转码替代。 |
| `channel` | OAuth 授权和交换路由保留自定义 HTTP；普通内部能力继续通过 gRPC 暴露。 |

## 后续收敛顺序

每次只迁移一个资源或一组行为一致的 RPC：

1. 为现有手写 HTTP 行为增加契约测试，覆盖成功、参数错误、业务错误、鉴权和
   404/405。
2. 注册对应的 `RegisterXxxHTTPServer`。
3. 对比并修正状态码、JSON、错误编码和 middleware 行为。
4. 保留该服务的自定义兼容路由、Webhook、健康检查和 metrics。
5. 删除已经被生成 handler 完整替代的手写 CRUD handler。
6. 执行架构检查、单元测试、前端测试和 lint。

建议优先顺序：

1. `config`；
2. `monitor`；
3. `notify`；
4. `log`；
5. `admin` 中边界清晰的单个资源。

`log` 排在后面是因为存在 `ServiceAuth`、批量删除和 One-API 用户日志接口；
`admin` 排在最后是因为它同时承担鉴权、BFF、静态站点和反向代理职责。

## 重新评估 grpc-gateway 的触发条件

只有出现以下至少一项明确需求时，才重新打开 grpc-gateway 评估：

- 需要独立部署、独立扩缩容的统一 REST gateway；
- 出现大量非 Go gRPC 服务，需要统一生成公共 HTTP facade；
- 计划移除各业务服务自己的 Kratos HTTP listener；
- 多个外部客户端明确依赖 grpc-gateway 的 metadata 或 marshaler 扩展；
- 有量化结果证明统一 gateway 能降低部署、鉴权或 API 治理成本。

重新评估时必须给出目标拓扑、调用链、认证模型、错误契约、性能基线和回滚方案，
不能仅以“统一技术栈”作为迁移理由。

## 验收标准

- [x] grpc-gateway 迁移已有明确的“不推进”决策。
- [x] 标准 CRUD 和自定义 HTTP 的边界已经定义。
- [x] 原计划中与当前代码不一致的路由和 streaming 描述已经修正。
- [x] 后续收敛工作以 Kratos 生成 handler 和 HTTP 契约测试为主。
- [x] 定义了重新评估 grpc-gateway 的具体触发条件。

## 参考

- [Kratos API Definition](https://go-kratos.dev/en/docs/component/api/)
- [grpc-gateway Introduction](https://grpc-ecosystem.github.io/grpc-gateway/docs/tutorials/introduction/)
- [grpc-gateway Customizing your gateway](https://grpc-ecosystem.github.io/grpc-gateway/docs/mapping/customizing_your_gateway/)
