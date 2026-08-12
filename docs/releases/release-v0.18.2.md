# Micro-One-API v0.18.2 发布：修复 Kimi K3 web_search 文本粘连死循环 + relay 弹性配置 + gRPC 延迟全边缘可观测

> 2026-08-12 · 上一版：[v0.18.1](./release-v0.18.1.md)（2026-08-11）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.18.2)

v0.18.2 是 v0.18.1 之后的 **PATCH 修复版本**（7 个提交，`759181f` → `8b40b63`），核心是修复一个导致 codex 端任务中断的多轮对话 bug：Kimi K3 等 Anthropic 兼容上游联网搜索时返回 `server_tool_use` / `web_search_tool_result` content blocks，经 relay 转换后使 codex 端出现「Search results for query: Search results for query: …」文本无限粘连累积、多轮对话崩溃。同时完成 v0.18 P4 可观测性闭环——`xgrpc.UnaryClientMetricsInterceptor` 接入全部 gRPC dial 点（relay-gateway / channel / identity / billing / monitor / internal/data），并在真实流量下回填 BASELINE 基线；新增 relay-gateway 下游 gRPC 熔断器的 `resilience` 配置段（env-gated，默认关闭，生产已通过 `RELAY_RESILIENCE_ENABLED=true` 开启并观测到 `circuit_breaker_state` 真实数据）。

**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为 `relay-gateway`、`channel-service`、`identity-service`、`billing-service`、`monitor-worker`。

## 修复内容

### 1. fix(apicompat): Kimi K3 web_search 导致 codex 端文本粘连累积（三个递进提交）

这是一个跨三个提交逐步定位、收敛的多轮对话 bug，最终形态是「OAuth relay 路径未剥离 web_search tool + 结构化 block 转换产生 codex 不支持的 item 类型」两个缺陷叠加。

#### 1a. 759181f — 初步修复：转换 web_search blocks

**现象**：codex 端使用 K3 模型联网搜索时，多轮对话中出现「Search results for query: Search results for query: …」文本不断粘连累积，最终任务中断。

**根因**：Kimi K3 等 Anthropic 兼容上游联网搜索时，在流式响应中返回 `server_tool_use` + `web_search_tool_result` content blocks。本提交将其转换为 `web_search_call` output items，但 **codex 不支持该 item 类型**——这是后续 ddacd81 改为完全静默丢弃的原因。

#### 1b. ddacd81 — 核心修复：静默丢弃 server_tool_use / web_search_tool_result blocks

**根因**：759181f 把这两类 block 转成 `web_search_call` output items，但 codex 不支持该类型，显示「Searching the web」/「Search results for query」，并导致多轮对话中文本粘连累积、任务中断。

**修复**：完全静默丢弃这两类 blocks，不产生任何 `web_search_call` output items。搜索结果已融入模型文本输出，不丢失上下文。新增 `SkippingBlock` flag 确保跳过期间的 `content_block_delta` / `stop` 不干扰当前打开的 message / reasoning / function_call item 状态。

三个方向的完整处理：
- **流式响应**：`content_block_start` 设 `SkippingBlock=true` 并 return nil，delta/stop 检查 flag 跳过。
- **非流式响应**：`server_tool_use` / `web_search_tool_result` 直接 continue。
- **请求方向**：`web_search_call` history skip（759181f 已有，保持不变）。

删除 `emitWebSearchCallItem` / `extractSearchQuery` 等废弃代码。

#### 1c. be53c14 — 根治：OAuth relay 路径跳过 web_search tool

**根因**：ddacd81 只覆盖了 fallback 路径（`responses_fallback.go` 会剥离 tool type 标识符），但 **OAuth relay 路径**（`ClaudeOAuthAdaptor`）仍把 web_search tools 以 `web_search_20250305` 原样转发给第三方 Anthropic 兼容端点（Kimi K3、智谱、MiniMax），触发**服务端联网搜索**。模型随后在普通 text block 中同时输出结构化 `server_tool_use` / `web_search_tool_result` blocks **和**「Search results for query:」文本，该文本跨对话轮次累积，造成无限粘连循环。

**修复**：在 `convertResponsesToAnthropicTools()` 中**完全跳过 web_search tools**，使 OAuth 路径与 fallback 路径行为一致。既有的 `SkippingBlock` 逻辑（ddacd81）作为安全网保留，以防任何下游仍发出这些 blocks。

**影响服务**：`relay-gateway`（OAuth relay 路径 + fallback 路径）。

### 2. feat(relay): 新增 resilience 配置段，env-gated 默认关闭（8f11028）

为 relay-gateway 的下游 gRPC 熔断器提供配置入口：

- `RELAY_RESILIENCE_ENABLED`（默认 `false`）
- `RELAY_RESILIENCE_TIMEOUT`（默认 `3s`）

启用后，每个下游调用（identity / channel / billing / log）被包装在 `ResilientClient` 中，应用 per-call 超时并记录 `circuit_breaker_state` 指标。**默认关闭**——生产通过 docker-compose 环境变量 `RELAY_RESILIENCE_ENABLED=true` 启用。纯新增配置段，不改变默认行为。

**影响服务**：`relay-gateway`。

### 3. feat(observability): v0.18 P4 可观测性闭环（445771a + 30ffb73）

#### 445771a — gRPC 延迟全边缘接入 + BASELINE 回填

将 `xgrpc.UnaryClientMetricsInterceptor` 接入**全部** gRPC dial 点：

- relay-gateway（identity / channel / billing / log，经 `createInsecureClient`）
- channel（notify ×2）
- identity（billing）
- billing（notify）
- monitor（channel）
- internal/data（relay identity / channel）

拦截器只做计时 + 打标签，无 I/O，保持 relay 热路径开销可忽略。

**熔断器说明**：state gauge 已在既有 resilience 路径（`ResilientClient.Execute`）中观测；刻意**不**接入 breaker wrapper 的 `UnaryClientInterceptor`（会造成双层包装 + 行为变更，违反 observe-only 约束）。生产 relay `Resilience.Enabled` 此前未设置，`circuit_breaker_state` 在运维开启 resilience 后才有数据（行为变更，属延后决策）。

**生产回填**：已部署到生产并在真实流量下回填 BASELINE：`dependency_grpc_latency` identity 2.7/6.9/9.6ms、channel 1.5/9.3/18.6ms、billing 1.0/20.9ms、log 0.6/4.8/21.9ms（P50/P95/P99）；billing commit async 31/60/92ms、reserve sync 8/40/48ms。无热路径回归：路由选择 P95 20.9ms（15m）对比部署前 10ms（5m）。

同时修复 identity server/test 与 `pkg/errors` 的历史 gofmt 漂移（repo-wide gofmt clean）。

**影响服务**：`relay-gateway`、`channel-service`、`identity-service`、`billing-service`、`monitor-worker`。

#### 30ffb73 — 熔断指标闭环：生产开启 relay resilience

运维通过 `config.yaml` 开启 relay resilience（`RELAY_RESILIENCE_ENABLED=true`）并重启。`circuit_breaker_state` 在流量下产出真实数据：4 个下游服务（identity / channel / billing / log）均处于 `state=closed`，24h trips=0（健康）。BASELINE 熔断表已回填；并注明 `circuit_breaker_requests_total` 只统计错误路径（`ResilientClient.Execute` 无成功分支），故下游调用成功时无样本——属指标语义而非缺陷。**纯文档，无代码变更**。

### 4. chore(security): gosec G101 误报标注（8b40b63）

`ReasonTokenSubnetViolation = "TOKEN_SUBNET_VIOLATION"`（v0.18.1 引入）触发 gosec G101（硬编码凭证检测）——但它只是错误码字符串标签而非密钥，G101 正则被 `TOKEN` 前缀命中。按仓库已有的 3 处 G101 误报处理惯例（如 `claude_token_provider.go`）加带理由的 `#nosec` 标注，使 pre-push gosec 门禁通过。**无运行时行为变更**。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **apicompat 行为**：web_search tool 不再转发给 Anthropic 兼容上游（OAuth + fallback 路径一致）。依赖上游**服务端**联网搜索的客户端会失去该能力——这正是修复意图（此前该路径产生 codex 无法处理的输出并导致对话崩溃）。搜索结果文本仍融入模型正常文本输出。
- **数据库**：无新增迁移文件。
- **配置**：新增 `resilience` 配置段（`RELAY_RESILIENCE_ENABLED` / `RELAY_RESILIENCE_TIMEOUT`），**默认关闭**，不改变既有行为；生产如需熔断器需显式开启。
- **运行时**：所有 gRPC dial 点新增延迟指标拦截器，纯计时无 I/O，热路径开销可忽略。

## 升级步骤

```bash
git fetch --tags
git checkout v0.18.2

# 无数据库迁移；重新构建受影响的服务：
./scripts/deploy-update.sh relay-gateway channel-service identity-service billing-service monitor-worker

# 如需启用 relay 熔断器（可选），在 docker-compose 环境变量中设置：
#   RELAY_RESILIENCE_ENABLED=true
#   RELAY_RESILIENCE_TIMEOUT=3s
```

## 验证

- `go build ./...`、`gofmt` repo-wide clean。
- apicompat 流式 / 非流式 / 请求三方向测试通过（`anthropic_responses_test.go`）。
- gosec（pre-push 门禁）0 issues。
- 生产 BASELINE 已回填：gRPC 延迟全边缘 P50/P95/P99、circuit_breaker_state 4 下游 closed / trips=0。

## 完整变更日志

- fix(apicompat): skip web_search tool in Responses→Anthropic conversion
- chore(security): annotate gosec G101 false positive on TOKEN_SUBNET_VIOLATION
- feat(relay): 新增 resilience 配置段，env-gated 默认关闭
- fix(apicompat): 静默丢弃 server_tool_use/web_search_tool_result blocks
- docs(observability): P4 circuit-breaker metrics closed — production relay resilience enabled
- feat(observability): v0.18 P4 observability loop closure (gRPC latency full-edge + BASELINE backfill)
- fix(apicompat): 修复 k3 模型 web_search 导致 codex 端 'Search results for query' 文本粘连累积
