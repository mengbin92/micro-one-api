# Micro-One-API v0.23.2 发布：Responses / Anthropic 兼容与 executor 灰度修复

> 2026-08-27 · 上一版：[v0.23.1](./release-v0.23.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.23.2)

v0.23.2 是 v0.23.1 之后的 **PATCH 协议兼容与灰度可靠性版本**：修复 `/v1/responses`
经 orchestrator 选择 Anthropic API-key 渠道时在本地被误报为 502 的问题，补齐流式 executor
观察与失败边界，并发布受控的 Relay Playground。

本版本 **无数据库迁移、无公共 API / proto 破坏性变更**。运行时新增可选的
`RELAY_GRPC_ADDR` 覆盖项；生产升级需要重新部署 `relay-gateway`，如需使用 Playground
还需同步发布 `web/dist`。

## 1. 修复 Responses → Anthropic API-key 协议转换

**根因**：staged executor 将 `/v1/responses` 请求标记为 Responses 入站格式，但 Anthropic
API-key adaptor 当时只接受原生 Messages。选择 StepFun `step-explore` 后，请求尚未发往上游
就在本地转换阶段失败，并被统一映射为 HTTP 502。

**修复**：在 adaptor 边界增加 Responses → Anthropic Messages 请求转换、Anthropic
非流式响应 → Responses 转换，以及 Anthropic SSE → Responses SSE 转换；继续裁剪第三方
Messages 端点不普遍支持的 `thinking`、`output_config` 和 server-tool `type` 扩展。

**影响服务**：`relay-gateway`。生产 canary 已获得 14 个 StepFun / `step-explore` Responses
成功样本；旧的 25–70ms 本地 unsupported-format 502 未再复现。

## 2. 补齐流式 executor 灰度与可观测性

**根因**：v0.23.1 前的 staging executor 主要覆盖非流式 Chat Completions，而生产主流量集中在
流式 Responses 与 Anthropic Messages，缺少一致的流终止、quota 结算和新旧路径对照指标。

**修复**：新增 transport-neutral stream port、adaptor-backed 转发、终态事件识别、流式
retry / failover 和取消传播；增加按 endpoint / stream / execution path 分组的请求、延迟、quota
与 failover 指标，并固化 7 天观察与一键回滚手册。

**影响服务**：`relay-gateway`、Prometheus 面板与生产灰度流程。WebSocket 和旧 handler 继续保留，
关闭 `RELAY_ORCHESTRATOR_ENABLED` 或清空 allowlist 即可回退。

## 3. 收紧失败边界与运行时配置

**根因**：上游已成功后的结算错误可能继承网络重试语义，泛化的 405 被当作协议能力错误，
被放弃的流消费者可能阻塞 chunk 投递；relay gRPC 地址也缺少环境覆盖入口。

**修复**：将 post-forward 失败标记为终态且不污染渠道健康，只对 Responses 范围识别协议能力
retry，传播流取消，类型化模型权限错误，并让部署 CORS 默认 fail closed；新增可选
`RELAY_GRPC_ADDR`，统一 billing source-kind 常量。

**影响服务**：`relay-gateway` 与共享 billing 类型定义；无数据格式变化，无需数据库迁移。

## 4. 发布受控 Relay Playground

**根因**：用户控制台只能阅读 API 调用说明，缺少不持久化凭证、可取消且能正确解析 SSE 的
Relay 验证入口；浏览器访问 Relay 时也缺少与生产配置一致的 CORS 链路。

**修复**：新增内存态 token、模型发现、Chat Completions、SSE 解析、请求检查、取消与错误映射；
Relay 路由安装无凭证 CORS 和 request tracing，并修复上线后重复 CORS 响应头与 raw-request
路由问题。

**影响服务**：`relay-gateway`、`web/dist`。token 不写入 localStorage、sessionStorage 或 URL。

## 兼容性说明

- **API / proto**：无公共 API 或 proto 破坏性变更；旧 handler 与 WebSocket 路径保留。
- **数据库**：无新增迁移，无需执行 `make migrate`。
- **配置**：新增可选 `RELAY_GRPC_ADDR`；未配置时沿用原默认监听地址。CORS 生产默认 fail closed，
  使用 Playground 时应显式配置可信来源。
- **部署范围**：必须重新部署 `relay-gateway`；需要 Playground 时同步发布 `web/dist`。

## 升级步骤

```bash
git fetch --tags
git checkout v0.23.2
```

1. 备份当前 relay-gateway 镜像与 `web/dist`；确认 orchestrator flag 和 allowlist 摘要数量，
   不记录原始 token 或 hash。
2. 按既有跨平台流程构建并部署 `relay-gateway`；如启用 Playground，再构建并发布 `web/dist`。
3. 按 endpoint / stream 检查 executor success、5xx、P95、quota outcome 和 failover；重点确认
   Anthropic API-key Responses 请求不再出现本地 unsupported-format 502。
4. 如需回滚，恢复旧 relay-gateway 镜像并设置 `RELAY_ORCHESTRATOR_ENABLED=false`；本版本
   不涉及数据库回滚。

## 验证

- `go test ./internal/adaptor ./internal/server`：通过。
- `go test ./app/... ./cmd/... ./domain/... ./internal/... ./pkg/... ./platform/...`：通过。
- adaptor 回归覆盖 Responses 请求、非流式响应、SSE 终态以及 orchestrator forwarder 到
  `/v1/messages` 的完整转换路径。
- 生产 canary：Responses 累计 106 success / 2 error；StepFun Responses 14 success，单次
  StepFun 502 后连续约 47 分钟无复现；P95 收敛至约 59.7s，quota 生命周期闭合且无 failover。

## 完整变更日志

- feat(relay): add streaming executor staging
- fix(config): make relay gRPC address configurable
- fix: harden relay failure boundaries
- docs(relay): update executor observation window
- feat(web): add relay playground
- fix(relay): resolve playground issues after launch
- ci(security): allowlist test-only keys flagged by gitleaks
- docs(relay): record observation interruptions and redeploys
- fix(relay): bridge responses through anthropic api-key channels
- docs(relay): record responses canary evidence
- docs(relay): record stabilized responses canary
