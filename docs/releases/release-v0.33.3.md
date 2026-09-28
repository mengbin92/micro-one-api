# Micro-One-API v0.33.3 发布：Claude Code auto mode 网关兼容修复

> 2026-09-28 · 上一版：[v0.33.2](./release-v0.33.2.md)（2026-09-27）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.3)

v0.33.3 是 v0.33.2 之后的 **PATCH 协议兼容与安全修复版本**：按 [Claude Code 网关兼容说明](https://code.claude.com/docs/en/auto-mode-classifier-billing)透传服务端分类器所需的请求头、`safeguards` 请求字段、`safeguard_results` 响应字段及流事件。修复审查中发现的压缩响应损坏和网关请求头泄露。

**无公共 API/proto 变更、无数据库迁移、无前端资源更新**。受影响的运行时服务只有 `relay-gateway`。

## 修复内容

### 1. Claude Code auto mode 功能头完整到达 Anthropic 上游

**根因**：订阅账户的 OAuth 适配器仅构造固定 `anthropic-beta` 值，丢弃客户端传来的第二个及后续 beta 值、`anthropic-version` 与其他功能头；Anthropic API-key 转发路径覆盖客户端的版本头。功能头与未知请求字段无法成对透传时，Claude Code 会退回本地计费的分类器请求。

**修复**：OAuth 与 API-key 路径保留客户端提供的版本头和所有 beta 值，仅在缺失版本头时使用现有默认值。原生 Messages 请求继续保留未知 JSON 字段，原生 JSON/SSE 响应继续逐字节传递，包括 `safeguard_results`、ping 事件和工具调用 ID。

**影响服务**：`relay-gateway`。新会话能否启用服务端检查仍取决于所选上游及账号是否提供该能力。

### 2. 阻断网关私有头泄露及压缩响应损坏

**根因**：审查发现新增的请求头复制同时转发了客户端 `Accept-Encoding`、Cookie 和 `Connection` 指定的逐跳头。显式复制压缩偏好后，Go 上游客户端不再自动解压 gzip 响应，导致非流式返回压缩字节、流式请求返回 502、用量无法正确解析；网关会话 Cookie 和私有头也到达上游。

**修复**：所有转发路径共用一处请求头复制规则：保留功能头，过滤入站鉴权、Cookie、逐跳头及客户端压缩偏好；由 Go 上游传输自行协商并解压响应。

**影响服务**：`relay-gateway`。不会改变上游授权凭据的注入方式。

## 兼容性说明

- 无公共 API/proto、数据库 schema 或前端资源变更；无需新配置项。
- 服务端分类器免计费需要真实 Anthropic 上游支持。其他不提供该能力的上游仍按 Claude Code 的回退行为处理，网关不会伪造分类器结果。
- 入站网关 Cookie、鉴权头和逐跳头不再发送至上游；上游响应的 gzip 解压由 Go HTTP 传输处理。

## 升级步骤

1. 在本机交叉构建 `linux/amd64` 的 `relay-gateway` 镜像，不在生产主机上构建。
2. 为当前线上镜像保存回滚标签，上传并加载新镜像，在生产 Compose 目录执行 `docker compose up -d --no-deps relay-gateway`。无需迁移数据库或更新前端。
3. 检查容器状态、重启次数与近期日志；使用 Claude Code 新会话的 `/status` 检查 `Auto mode server`，并确认所用上游支持服务端检查。

生产已完成镜像更新。当前镜像为 `sha256:3beac59a04951788906ad269f630b94f8b9d4f96bcf638af11b4bb0f6ad0c464`；此前镜像保留为 `docker-compose-relay-gateway:rollback-20260928-093420`（`sha256:80d9f90aade5290540fe330d50cbe3b07c112e026576a6f8f0a1e0e6c492fb85`）。若需回滚，将该标签重新标记为 `latest` 后仅重建 `relay-gateway`。回滚会重新引入本次修复的请求头缺陷。

## 验证

- 原生 Anthropic API-key 和 OAuth 两种渠道、JSON 和 SSE 两种响应均通过端到端回归：多个 beta 值、版本头、未知 `safeguards` 字段、`safeguard_results`、工具 ID 与 ping 事件完整透传。
- gzip 响应正常解压，流式终止及实际用量结算正确；网关 Cookie 和 `Connection` 指定的私有头不再到达上游。
- `go test -race ./internal/adaptor ./internal/server ./domain/upstream/provider` 通过。
- `make verify` 全部门禁通过，包含 Go 单测、race、架构、迁移治理、生成前端类型和前端 lint/test/build。
- 生产 `relay-gateway` 运行中，重启计数 `0`；`/healthz` 返回 `200`，未认证的 `/v1/messages` 返回预期 `401`。更新后真实流式请求完成并记录用量。Claude Code 新会话的服务端分类器状态尚未以具备对应上游资格的账号实测。

## 完整变更日志

- `af26b210` fix(relay): preserve Claude Code auto mode safeguard traffic
- docs(release): v0.33.3
