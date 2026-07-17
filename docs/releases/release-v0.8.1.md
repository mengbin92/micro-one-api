# Micro-One-API v0.8.1 发布公告

> 2026-07-17 · 上一版：[v0.8.0](./release-v0.8.0.md)（2026-07-16）

v0.8.1 是 v0.8.0 之后的 **PATCH** 版本。本版聚焦 Anthropic 上游对 OpenAI Responses API（Codex）的兼容性修复，以及通知面板的前端体验优化。

本版**没有新增业务表迁移**，**没有 API 破坏性变更**，**没有部署配置变更**。从 v0.8.0 平滑升级即可。

## 亮点

- **Anthropic 上游支持 Codex 客户端**：OpenAI Responses API 请求在 `ChannelTypeAnthropic`（type=2）的 API-key 渠道上自动转换为 Anthropic Messages API (`/v1/messages`)，上游响应再转换回 Responses 格式，Codex / Claude Code 等客户端可直接使用 Anthropic 及兼容上游。
- **兼容 Anthropic-compatible 第三方上游**：针对 Kimi Coding 等第三方 Messages 端点，自动剔除 Anthropic 原生推理扩展（thinking/output_config），规范化工具类型和空输入 schema，并支持无空格 `data:` 的 SSE 行，提升跨厂商兼容性。
- **Anthropic 渠道健康探测更准确**：管理后台“测试渠道”对 Anthropic 渠道改为使用 `/v1/messages` 发起最小消息探测，而不是跳过或误用 `/models`。
- **通知面板结构化中文展示**：将后端存储的英文/技术性通知文本解析为分类、摘要、严重级别和详情字段，失败原因自动翻译为中文，便于运维人员快速定位问题。

## 变更内容

### Fixed

- `internal/apicompat/anthropic_to_responses_response.go` 在流式转换中累计文本、reasoning 和 function-call 参数，确保 `response.content_part.done` / `response.output_text.done` / `response.reasoning_summary_text.done` 等事件携带完整内容。
- `internal/apicompat/responses_to_anthropic_request.go` 将 reasoning 预算限制为 `max_tokens` 的一半（不低于 1024），避免 Anthropic 的 `budget_tokens < max_tokens` 校验失败；同时支持 `instructions` 作为 system prompt，并兼容 developer role 输入。
- `internal/server/responses_fallback.go` 的 Anthropic API-key 回退路径自动清理不兼容的 thinking/output_config，规范化工具类型和 input_schema；SSE 解析兼容 `data:` 后无空格的实现。
- `domain/upstream/provider/anthropic.go` 的 `Forward` / `ForwardStream` 实现真正的 Anthropic Messages API 透传，支持自定义 base URL 和 API 版本头。
- `app/admin/internal/service/admin.go` 的 `TestChannel` 对 Anthropic 渠道使用 `/v1/messages` 健康探测，并验证请求头与模型参数。

### Changed

- `web/src/components/NotificationPanel.tsx` 使用 `web/src/lib/notification-display.ts` 解析通知内容，按类别、严重级别、渠道类型展示，并翻译失败原因；新增展开/收起详情交互。
- `docs/TODO.md` 刷新架构重构 Phase 1 剩余项和可观测性基线计划。
- `docs/releases/release-blog-v0.6.1-v0.8.0.md` 新增 v0.6.1 到 v0.8.0 的合并发布博客。

## 数据库迁移

本版没有新增编号迁移文件，也没有 schema 变更。迁移执行方式与 v0.8.0 一致。

## 破坏性变更

API、数据库 schema 和部署配置均无破坏性变更。升级 v0.8.1 不需要修改环境变量或配置。

## 升级步骤

```bash
git fetch --tags
git checkout v0.8.1

# 检查并替换 deployments/docker-compose/.env 中的生产密钥
cd deployments/docker-compose
docker compose --env-file .env config --quiet

# 旧数据卷升级前先备份；全新环境直接启动
docker compose --env-file .env up -d --build
```

Kubernetes 部署应先运行迁移，再执行 `kubectl apply -k deployments/k8s`，并等待九个 Deployment rollout 完成。完整步骤和 Secret 清单见 [docs/deployment.md](../deployment.md)。

## 验证

发布前已执行：

```bash
go test ./app/admin/internal/service/... -run TestChannel
# 通过（Anthropic 渠道健康探测测试）

go test ./internal/server/... -run 'Anthropic|SSE|ResponsesRequest'
# 通过（Responses→Anthropic 转换、SSE 桥接、工具规范化）

go test ./internal/apicompat/... -run 'ReasoningBudget|Developer|ToolPairing'
# 通过（reasoning 预算、system prompt、工具配对）

cd web && npm test -- notification-display
# 通过（通知解析单元测试）
```

`develop` 头提交 `33e9ebd` 的 GitHub CI 和 Security Pipeline 均已通过，包括 Backend、Frontend、amd64/arm64 Docker Build、CodeQL、license scan 和 security scan。

## 完整变更日志

- 33e9ebd fix(relay): support Codex on Anthropic-compatible upstreams
- 682d8f6 feat(relay): support Anthropic upstream for Responses (Codex) via messages conversion
- e414739 feat(web): optimize notification panel with structured Chinese display
- 46b0a99 docs: refresh TODO roadmap with architecture refactor Phase 1 & observability baseline
- 47d947f docs: add consolidated release blog for v0.6.1 through v0.8.0
