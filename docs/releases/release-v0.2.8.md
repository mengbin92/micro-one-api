# Micro-One-API v0.2.8 发布公告

> 2026-06-25 · 上一版: [v0.2.7](./release-v0.2.6.md) (2026-06-24)

v0.2.8 新增 Anthropic Messages API 入站端点 `/v1/messages`，使 relay-gateway 能直接对接 Claude Code CLI 及原生 Anthropic SDK 客户端，无需中间协议转换代理。无数据库迁移，无破坏性 API 变更。

## 亮点

- **Claude Code CLI 支持**：relay-gateway 现可直接作为 Claude Code CLI 的 `ANTHROPIC_BASE_URL` 后端，用户只需配置 `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_API_KEY` 即可接入。
- **完整协议转换**：Anthropic Messages 格式 ↔ 内部 OpenAI 兼容选路/计费链路的双向转换，支持 string 和 array content blocks、system prompt、tool_use / tool_result 工具调用。
- **流式 SSE 适配**：将 OpenAI 兼容的 SSE 流式响应转换为 Anthropic 原生事件序列（`message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop`）。
- **Thinking 模式支持**：支持 DeepSeek-R1、GLM-5.x 等 thinking-mode 模型，将 `reasoning_content` 字段转换为 Anthropic `thinking` content block。
- **安全加固**：通过 gosec / govulncheck / gitleaks 全链路安全扫描，修复流式响应中途错误处理、错误格式一致性和 max_tokens 上限保护。

## 变更内容

### Added

- `internal/relay/server/anthropic_inbound.go`：新增 `POST /v1/messages` 入站 handler
  - 请求转换：`anthropicInboundRequest` → `ChatCompletionsRequest`，支持 system（string/array）、tool_use、tool_result、tool_choice 转换。
  - 响应转换：`ChatCompletionsResponse` → `anthropicMessagesResponse`，含 content blocks、stop_reason、usage。
  - 流式响应：OpenAI SSE → Anthropic SSE 事件序列，支持 thinking_delta + text_delta 混合输出。
  - 鉴权：支持 `x-api-key`（Anthropic 原生）和 `Authorization: Bearer` 两种方式。
  - 计费：复用现有 reserve / commit / release 配额链路，endpoint 记录为 `/v1/messages`。
- `internal/relay/server/http.go`：注册 `/v1/messages` 路由。
- `internal/relay/server/anthropic_inbound_test.go`：11 个单元测试，覆盖协议转换、鉴权、非流式、流式、错误处理。

### Fixed

- 流式 SSE 中途写入错误不再触发二次 `WriteHeader`（改为 `break`，与现有 chat/completions 流式处理一致）。
- `Plan()` 失败现在返回 Anthropic 错误信封格式（`{"type":"error","error":{...}}`），而非 OpenAI 格式。
- 新增 `max_tokens` 上限保护（64000），防止资源耗尽攻击。

### Security

- gosec SAST 扫描：0 issues。
- govulncheck SCA 扫描：0 vulnerabilities。
- gitleaks 密钥扫描：0 leaks（新代码）。

## 配置变化

无新增配置项。Claude Code CLI 客户端侧配置：

```bash
export ANTHROPIC_BASE_URL=http://<relay-gateway-host>:8080
export ANTHROPIC_API_KEY=<your-relay-token>
```

## 升级指南

1. 拉取最新镜像并重启 relay-gateway 服务。
2. 确保管理后台已配置目标模型（如 `GLM-5.2`、`claude-3-5-sonnet` 等）的可用渠道。
3. Claude Code CLI 用户设置上述环境变量即可接入。

无数据库迁移，无配置文件变更，可直接滚动更新。
