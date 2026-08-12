# Micro-One-API v0.18.3 发布：Responses↔Anthropic web_search 兼容性回归加固（纯测试收口）

> 2026-08-12 · 上一版：[v0.18.2](./release-v0.18.2.md)（2026-08-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.18.3)

v0.18.3 是 v0.18.2 之后的 **PATCH 测试收口版本**（2 个提交，`a21970a` + 本版新增），**不含任何生产代码变更，无运行时行为变化**。本版只做一件事：把 v0.18.2 web_search 修复链路的验证集合收口进 release tag，并补齐 fallback 路径的 web_search 兼容性最小矩阵，避免「tag 对应的测试语义」与「develop 当前认知」不一致。

**无 API 变更、无数据库迁移、无 proto 变更、无配置变更**。受影响服务：无（仅 `internal/server` 测试文件变化）。

## 背景：为什么需要一个纯测试的 PATCH

v0.18.2 发布后，`develop` 上新增了一个测试提交 `a21970a test(server): align Anthropic fallback tool expectations`。web_search 修复跨 Responses→Anthropic OAuth、fallback、流式/非流式多个路径，发布后才出现的测试调整说明兼容契约仍在收敛。如果把这个测试长期留在 develop 而不进入 tag，就会出现：

- release tag 对应的验证集合落后于当前认知；
- 后续以 tag 为基线的回归验证可能复现已被修正的测试预期，产生误报。

按规划（P0 — v0.18.3 收口），先审查该提交、补齐矩阵，再以补丁版收口。

## 内容

### 1. 审查结论：`a21970a` 是纯测试对齐，不掩盖实现偏差

`a21970a` 只改 `internal/server/responses_anthropic_fallback_test.go`，把 `TestResponsesRequestToAnthropicBodyNormalizesCodexTools` 的预期 tools 数从 3 改为 2，并新增显式断言：`exec_command` / `multi_agent_v1` 被保留、`web_search` 被跳过。

审查确认这是**与既有实现语义对齐**，而非放松断言去适配一个错误实现：

- 该测试走 fallback 路径 `responsesRequestToAnthropicBody()`（`internal/server/responses_fallback.go`），它调用的是 `apicompat.ResponsesToAnthropicRequest()`——**与 OAuth 路径共用同一个** `convertResponsesToAnthropicTools()`（be53c14 在此函数中跳过 web_search）。因此 fallback 路径在 `responses_fallback.go` 把 tool type 置空之前，web_search 就已经被 apicompat 层丢弃，「tools=2」是正确预期。
- 新断言方向是**收紧**而非放松：显式断言「client tools 保留 + web_search 不得保留」，比原来的「恰好 3 个」更能暴露回归。

### 2. 补齐 fallback 路径 web_search 兼容性最小矩阵（本版新增）

v0.18.2 的 web_search 三方向（请求 / 流式响应 / 非流式响应）覆盖都在 `internal/apicompat`（OAuth 转换层）；fallback 路径虽然复用同一批转换器，但经过的是 `responses_fallback.go` 的 raw-JSON / SSE-pipe 边界，此前只有「请求方向 tools 过滤」一个用例。本版在该边界补齐最小矩阵，与 apicompat 侧一一对应：

| 矩阵格 | apicompat 侧（OAuth 层，v0.18.2 已有） | fallback 侧（本版新增） |
|---|---|---|
| 请求方向：tools 过滤 | `TestResponsesToAnthropicRequest_AllToolsGetObjectInputSchema` | `TestResponsesRequestToAnthropicBodyNormalizesCodexTools`（a21970a 对齐） |
| 请求方向：history `web_search_call` 丢弃 | `TestResponsesToAnthropicRequest_WebSearchCallSkipped` | `TestResponsesRequestToAnthropicBodySkipsWebSearchCallHistory` |
| 非流式响应：blocks 静默丢弃 | `TestNonStreaming_ServerToolUseDropped` | `TestAnthropicResponseToResponsesDropsServerToolBlocks` |
| 流式响应：blocks 静默丢弃 + 文本/终止事件不受干扰 | `TestAnthropicEventToResponses_ServerToolUseDropped` | `TestTransformAnthropicStreamDropsServerToolBlocks` |

三个新用例的断言要点：

- 请求体 / 响应体 / 响应流中**不得出现** `server_tool_use` / `web_search_tool_result` / `web_search_call` 任何字样；
- 被丢弃 block 前后的文本内容（deltas、非流式 text）必须完整保留；
- 流式路径必须仍以 `response.completed` + `[DONE]` 正常收尾（跳过 block 不得干扰终止事件）。

## 兼容性说明

- **API / proto / 数据库 / 配置**：全部无变更。
- **运行时行为**：无变更——本版只修改 `internal/server/responses_anthropic_fallback_test.go`（测试文件），不触碰任何非测试代码。
- **升级必要性**：本版不修复任何运行时缺陷；仅当你需要「tag 基线 = 当前验证集合」时才需要同步。生产运行 v0.18.2 的用户**无需**为本版重新部署。

## 升级步骤

```bash
git fetch --tags
git checkout v0.18.3
# 无生产代码变更，无需重新构建或部署任何服务。
```

## 验证

- `make test-unit`：通过（含 3 个新增 fallback 矩阵用例）。
- `make test-race`：通过。
- `./scripts/check-architecture.sh`：通过。
- 前端 `npm run lint` / `npm test -- --run`（29 files / 100 tests）/ `npm run build`：全部通过。

## 完整变更日志

- test(server): align Anthropic fallback tool expectations
- test(server): fallback web_search minimal compatibility matrix (request history / non-stream / stream block drops)
