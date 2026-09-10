# micro-one-api 协议转换：Chat ↔ Responses ↔ Messages

> 本文以 `internal/apicompat` + `internal/adaptor` + `internal/server` 的实际代码为例，
> 讲清楚三个主流 LLM 协议在网关中是如何双向转换的、什么时候触发哪条转换链、
> 以及流式场景下的状态机设计。

## 0. 三个协议一句话对比

| 协议 | 端点 | 会话模型 | 工具调用载体 | 思考能力 |
|---|---|---|---|---|
| **ChatCompletions**（OpenAI 经典） | `POST /v1/chat/completions` | `messages[]` 平面数组，role 驱动 | assistant 消息的 `tool_calls[]` + `role=tool` 回复 | `reasoning_effort` 参数 / `reasoning_content` 字段 |
| **Responses**（OpenAI 新一代） | `POST /v1/responses` | `instructions` + `input[]`（item 化：message / function_call / function_call_output / reasoning 都是独立 item） | `function_call` / `function_call_output` 独立 item | `reasoning: {effort, summary}`，reasoning 是独立 output item |
| **Anthropic Messages** | `POST /v1/messages` | `system`（顶层）+ `messages[]`（仅 user/assistant 交替），content 为 block 数组 | `tool_use` block（assistant）/ `tool_result` block（user） | `thinking: {budget_tokens}`，thinking 是独立 content block |

核心类型定义集中在 `internal/apicompat/types.go`：

- Anthropic 侧：`AnthropicRequest`（types.go:16）、`AnthropicContentBlock`（types.go:55）、`AnthropicStreamEvent`（types.go:150）
- Responses 侧：`ResponsesRequest`（types.go:202）、`ResponsesInputItem`（types.go:235）、`ResponsesStreamEvent`（types.go:395）
- Chat 侧：`ChatCompletionsRequest`（types.go:439）、`ChatMessage`（types.go:466）、`ChatCompletionsChunk`（types.go:565）

注意：**没有统一的中心 IR 结构体**。三套 DTO + 一组点对点转换函数（命名即方向，如 `ResponsesToAnthropicRequest`）。
但拓扑上实际是 **hub-and-spoke，hub 是 Responses 格式**（详见 §2）。

## 1. 总体架构：谁负责转换

```
客户端 (Chat / Responses / Anthropic 协议)
   │
   ▼
internal/server  ← 路由 + gate（legacy / orchestrator 二选一）+ 渠道类型分派
   │
   ▼
internal/adaptor ← Adaptor 接口：ConvertRequest / ConvertResponse / ConvertStreamResponse
   │                按 channel.Type 注册（registry.go），每个 adaptor 知道自己的上游协议
   ▼
internal/apicompat ← 纯函数转换层：三套 DTO + 点对点转换 + 流式状态机
   │
   ▼
上游 (OpenAI 兼容 / Anthropic / Codex OAuth / Claude OAuth / Gemini …)
```

### Adaptor 接口（internal/adaptor/adaptor.go:86-118）

```go
type Adaptor interface {
    Init(ctx *RelayContext)
    ConvertRequest(ctx *RelayContext, inbound Format, body []byte) (Format, []byte, error)
    GetUpstreamURL(ctx *RelayContext) (string, error)
    BuildUpstreamRequest(...) (*http.Request, error)
    ConvertResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error)
    ConvertStreamResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error)
    ...
}
```

`Format` 是四值枚举（adaptor.go:26）：`chat_completions` / `responses` / `anthropic_messages` / `gemini`。
注册表 `registry.go:11-35` 按渠道类型返回 lazy adaptor（`Init` 时才构造真实实例）。

### 各 Adaptor 的转换矩阵

| Adaptor | 上游协议 | 入站 Responses | 入站 Chat | 入站 Anthropic |
|---|---|---|---|---|
| `OpenAICompatibleAdaptor`（20+ 家 OpenAI 兼容渠道） | chat_completions | `ResponsesToChatCompletionsRequest` | 直通 | `AnthropicToChatCompletionsRequest` |
| `AnthropicAdaptor`（type=2 API-key） | anthropic_messages | `ResponsesToAnthropicRequest` + 剥离扩展字段 | 不支持 | 直通（仅改 model） |
| `CodexOAuthAdaptor` | responses | 直通 | `ChatCompletionsToResponses` | `AnthropicToResponses` |
| `ClaudeOAuthAdaptor`（含 Zhipu/MiniMax/Kimi 复用） | anthropic_messages | `ResponsesToAnthropicRequest` | **Chat→Responses→Anthropic 两跳** | 直通 |

关键位置：`openai_compatible.go:50`、`anthropic.go:50-102`、`codex_oauth.go:58-93`、`claude_oauth.go:65-108`。

## 2. 转换拓扑：Responses 是事实上的 hub

```
                 ┌───────────────┐
   点对点直连 ──► │   Responses   │ ◄── hub
                 └──┬─────▲───┬──┘
          直连/组合 │     │   │ 直连
   ┌────────────────┘     │   └────────────────┐
   ▼                      │                    ▼
┌──────────┐              │             ┌──────────────┐
│ ChatComp │ ◄────────────┴───────────► │  Anthropic   │
└──────────┘   Chat↔Anthropic 不经直连，  └──────────────┘
                而是 Chat→Responses→Anthropic 两段组合
```

最典型例子是 `anthropic_chat_bridge.go:13-25`——Anthropic 请求转 Chat 请求并非独立实现，
而是**以 Responses 为枢纽的组合桥**：

```go
// internal/apicompat/anthropic_chat_bridge.go:18
responsesReq, err := AnthropicToResponses(req)              // 第一跳
chatReq, err := ResponsesToChatCompletionsRequest(responsesReq) // 第二跳
```

之后再补回 Responses 表达不了、但 Anthropic 客户端关心的字段（anthropic_chat_bridge.go:27-53）：
`max_tokens`（上限 64000，并清掉 `max_completion_tokens` 防止 DeepSeek 系端点拒绝）、
`stop_sequences → stop`、未显式声明 thinking 时清掉桥默认带的 `reasoning_effort`、
流式强制 `stream_options.include_usage=true`。

同样，Claude OAuth 渠道收到 Chat 入站时走 `Chat→Responses→Anthropic` 两跳（claude_oauth.go:86-104，
注释原文 "via the Responses hub"）。这样做的好处是 6 条转换方向只需维护 **4 组核心转换器 + 2 个组合桥**，
覆盖矩阵由 `compatibility_matrix_test.go` + `matrix_fixtures_test.go` 固化。

## 3. 什么时候触发哪条转换链

路由注册在 `internal/server/routes.go:14-71`。每个端点有 gate handler 按 feature flag
在 legacy / orchestrator 间二选一（`orchestrator_gate.go`），handler 内部先经
`RelayUsecase.Plan()`（internal/biz/relay.go:473-622）选出渠道，再按 `channel.Type` 分派：

### `/v1/chat/completions`（http_chat_handler.go:87-94）

```
Plan() 选渠道
 ├─ 订阅渠道 → handleChatCompletionsViaAdaptor
 │    （Codex: chat→responses；Claude 系: chat→responses→anthropic）
 └─ API-key 渠道 → 原生 ChatCompletions 直通（OpenAI 兼容，无转换）
```

### `/v1/messages`（anthropic_inbound.go:71-74）

```
Plan() 选渠道
 ├─ 订阅渠道 → handleAnthropicMessagesViaAdaptor（inbound=anthropic_messages）
 └─ API-key 渠道 → executeAnthropicChannelAttempt（anthropic_adaptor_attempt.go:23）
      · Anthropic 渠道 → 直通（只改写 model）
      · OpenAI 兼容渠道 → AnthropicToChatCompletionsRequest（经 Responses hub 组合）
```

### `/v1/responses`（http_responses_handler.go:46-101）—— 唯一带"降级链"的端点

```
handleResponsesCreateLike
 ├─ Upgrade: websocket → WS relay（不经转换）
 ├─ 订阅渠道 → ViaAdaptor（Codex 直通；Claude 系 responses→anthropic）
 ├─ Anthropic API-key 渠道 → 【链 A】确定性 responses→anthropic（先于原生尝试）
 └─ 原生 responses 透传
      └─ 失败(400/404/405/501/502/503) → 【链 B】responses→chat fallback
```

- **链 A（确定性）**：`responses_fallback.go:756`，请求走 `ResponsesToAnthropicRequest` 后剥离
  `Thinking`/`OutputConfig`、清空 server-tool Type、补空 `input_schema`（:808-820，
  第三方 Anthropic 兼容端点不支持这些扩展）。
- **链 B（错误触发）**：`shouldFallbackResponsesToChat`（responses_fallback.go:29-50），
  对同一渠道改发 `/chat/completions`，流式用 `responsesStreamFallbackState`（:396-673）
  把 chat SSE chunk 状态机转成 Responses 事件序列。
- 协议能力错误（405、特定 400）由 `ProtocolCapabilityError`（internal/biz/retry.go:87,149）
  标记为"可换渠道重试"，让请求漂移到原生支持 Responses 的渠道。

## 4. ChatCompletions → Responses（请求方向）

入口：`ChatCompletionsToResponses`（chatcompletions_to_responses.go:20）。

### 顶层字段映射（:31-88）

```go
out := &ResponsesRequest{
    Model:        req.Model,
    Instructions: req.Instructions,
    Input:        inputJSON,   // messages 转成的 items 数组
    Stream:       req.Stream,  // 保留客户端流式偏好
    Include:      []string{"reasoning.encrypted_content"},
    Store:        ptr(false),  // 恒 false：每轮重发全量历史
}
```

- reasoning 模型（gpt-5.x，`isReasoningModel`）**不发送** `temperature/top_p`，否则透传（:42-45）
- `max_completion_tokens` 优先于 `max_tokens` → `max_output_tokens`，下限 128（:51-61）
- `reasoning_effort` → `reasoning: {effort, summary: "auto"}`（:64-69）
- legacy `functions[]` 与 `tools[]` 合并为 Responses 工具数组；legacy `function_call` → tool_choice（:456-474）

### messages → input items 的核心映射（`chatMessageToResponsesItems` :107）

| Chat 消息 | Responses item |
|---|---|
| `system` | `{role: "system", content}` |
| `user`（含多模态） | `text` part → `input_text`；`image_url` → `input_image`（跳过空 base64 data URI，:377） |
| `assistant`（纯文本） | `{role: "assistant", content: [output_text]}` |
| `assistant` + `tool_calls[]` | **拆成 1 条 assistant 消息 + N 个独立 `function_call` item**（:187-198） |
| `assistant.reasoning_content` | 包 `<thinking>...</thinking>` 标签前缀保留语义（:159-161） |
| `role=tool` | `{type: "function_call_output", call_id, output}`，空输出补 `"(empty)"`（:275-288） |
| `role=function`（legacy） | 同上，`call_id` 用 `m.Name`（:293-306） |

最值得注意的是 assistant 拆分：Chat 的"一条消息携带 tool_calls 数组"在 Responses 里
每个 tool call 都是**独立 item**：

```go
// chatcompletions_to_responses.go:187
items = append(items, ResponsesInputItem{
    Type:      "function_call",
    CallID:    tc.ID,
    Name:      tc.Function.Name,
    Arguments: args, // 空时补 "{}"
})
```

## 5. Responses → ChatCompletions（响应方向）

### 非流式：`ResponsesToChatCompletions`（responses_to_chatcompletions.go:19）

- `message` item 的 `output_text` → `content`
- `function_call` item → `tool_calls[]`
- `reasoning` item 的 `summary_text` → `reasoning_content`
- `web_search_call` 静默吞掉
- finish_reason：`incomplete + max_output_tokens → "length"`；`completed` 且有 tool_calls → `"tool_calls"`，否则 `"stop"`（`responsesStatusToChatFinishReason` :89）
- usage：input/output → prompt/completion，details 仅在非零时输出（:331-383），避免非 reasoning 响应出现空 details 对象

### 流式：`ResponsesEventToChatChunks`（responses_to_chatcompletions.go:137）+ 状态机

| Responses 事件 | 产出的 Chat chunk | 处理位置 |
|---|---|---|
| `response.created` | 首个 `role=assistant` chunk | :208 |
| `response.output_text.delta` | `delta.content` | :227 |
| `response.output_item.added`（function_call） | `delta.tool_calls[0]` 带 `{index, id, name}`，登记 `OutputIndexToToolIndex` | :236 |
| `response.function_call_arguments.delta` | `delta.tool_calls[0].function.arguments` **分片直转不聚合** | :260 |
| `response.reasoning_summary_text.delta` / `reasoning_text.delta` | `delta.reasoning_content` | :280 |
| `response.completed` / `done` / `incomplete` / `failed` | finish chunk（+可选 usage chunk） | :288 |
| 其他事件 | 忽略 | :161 |

要点：
- Chat 流式 tool_call 的 `index` 是客户端维度整数索引，Responses 用 `output_index` + `item_id`，
  靠 `OutputIndexToToolIndex` map 做翻译；找不到注册的 index 的分片**直接丢弃**，防止乱序孤儿 delta（:265-268）
- `FinalizeResponsesChatStream`（:170）在上游异常断开时幂等补发 finish chunk
- 非流式但上游终止事件 output 为空时，`BufferedResponseAccumulator`（:435）从 delta 事件重建完整 output

## 6. Responses → Anthropic Messages

### 请求：`ResponsesToAnthropicRequest`（responses_to_anthropic_request.go:14）

| Responses | Anthropic | 说明 |
|---|---|---|
| `instructions` + input 中 system/developer 项 | `system`（`\n\n` 拼接） | :114-143 |
| `max_output_tokens` | `max_tokens`（缺省补 8192，Anthropic 必填） | :33-39 |
| `function_call` item | assistant `tool_use` block | :145-161 |
| `function_call_output` item | user `tool_result` block，空补 `"(empty)"` | :163-179 |
| `input_image`（data URI） | `image` block（base64 source） | `dataURIToAnthropicImageSource` :481 |
| `reasoning.effort` | `output_config.effort` + `thinking.budget_tokens` | 见下 |
| `web_search_call` 历史 item | **丢弃** | :201-209，防止 Kimi K3 等上游 "Search results for query:" 累积死循环 |
| `web_search` 工具定义 | **跳过** | :548-561，第三方 Anthropic 兼容端点不支持 |

**reasoning effort → thinking budget**（:56-94）：

```go
low→1024, medium→4096, high→10240, max(xhigh)→32768
// 两道钳制：budget ≤ max_tokens/2（保证可见回答空间）
//           budget ≥ 1024（Anthropic 最低要求）
```

**tool_use/tool_result 配对修复**（`normalizeAnthropicToolPairing` :267-347）——这是本方向最核心的防御性逻辑。
动机（注释 :251-256）：Codex（store:false）每轮重发全量历史，且常在 function_call 与其 output 之间
插入 developer 审批通知，朴素逐条转换必然破坏 Anthropic 的三条不变式：

1. 每个 `tool_result` 必须紧跟含对应 `tool_use` 的 assistant 消息
2. 每个 `tool_use` 必须被下一条 user 消息中的 `tool_result` 应答（未应答会被上游 400）
3. user/assistant 必须交替

算法：先按 `tool_use_id` 索引所有 tool_result（last wins）；重组时 assistant 只保留有应答的 tool_use，
孤立调用整体删除；有应答时紧接着按调用顺序输出配对 tool_result 的 user 消息；孤儿 result 丢弃。
前后各跑一遍 `mergeConsecutiveMessages`（:227-229）合并同角色相邻消息。

**call_id 双向稳定**：`fc_toolu_xxx` 剥 `fc_` 前缀还原（`fromResponsesCallIDToAnthropic` :466），
因为 Claude Code 会把 `tool_result.tool_use_id` 原样回传，ID 必须可逆。

### 响应（Anthropic → Responses）：`anthropic_to_responses_response.go`

非流式 `AnthropicToResponsesResponse`（:21）：
- `thinking` block → `reasoning` output item（summary_text 包裹）
- `text` → message item 的 `output_text`
- `tool_use` → `function_call` item（call_id 原样保留）
- `server_tool_use` / `web_search_tool_result` → 跳过
- stop_reason：`max_tokens → incomplete(+incomplete_details)`；`end_turn/tool_use/stop_sequence → completed`（:146-155）
- usage 经共享投影 `pkg/usage.ProjectOpenAI`（:125）：Anthropic **互斥桶**（input_tokens 不含缓存）
  投影为 OpenAI **包容桶**（input_tokens 含缓存），cache_creation 聚合值归 5m 桶

### 反向（Responses → Anthropic 响应）：`responses_to_anthropic.go`

给"客户端是 Claude Code、上游是 Responses 协议（Codex）"的场景用：
- `reasoning` item → `thinking` block；`function_call` → `tool_use`（剥 `fc_` 前缀）
- `web_search_call` → **合成** `server_tool_use` + `web_search_tool_result` 块对（:61-79），
  工具 ID 固定 `"srvtoolu_" + item.ID`，让 Claude Code 能计数搜索次数
- status → stop_reason：`incomplete+max_output_tokens → max_tokens`；completed 含 tool_use → `tool_use`

## 7. 流式转换：两个方向的状态机对照

流式是协议转换最难的部分——两种协议的流事件粒度完全不同：

- **Anthropic**：`message_start → content_block_start → content_block_delta* → content_block_stop → message_delta → message_stop`，按**顺序 block index** 引用
- **Responses**：`response.created → output_item.added → content_part.added → *.delta* → *.done → output_item.done → response.completed`，按 **output_index + item_id** 引用

### Anthropic 流 → Responses 流（`AnthropicEventToResponsesEvents`，anthropic_to_responses_response.go:214）

| Anthropic 事件 | Responses 事件 |
|---|---|
| `message_start` | `response.created`（in_progress 骨架） |
| `content_block_start` thinking | `output_item.added`(reasoning) + `reasoning_summary_part.added` |
| `content_block_start` text | `output_item.added`(message) + `content_part.added` |
| `content_block_start` tool_use | `output_item.added`(function_call, in_progress) |
| `content_block_start` server_tool_use 等 | 无输出，置 `SkippingBlock` 吞掉后续 delta/stop（:369-383） |
| delta `text_delta` / `thinking_delta` / `input_json_delta` | `output_text.delta` / `reasoning_summary_text.delta` / `function_call_arguments.delta` |
| delta `signature_delta` | **丢弃**（无 Responses 等价物，:431-433） |
| `content_block_stop` | 对应的 `*.done` + `output_item.done` |
| `message_delta` | 无独立事件，仅累计 usage/stop_reason |
| `message_stop` | `response.completed`（含最终 usage 投影） |

### Responses 流 → Anthropic 流（`ResponsesEventToAnthropicEvents`，responses_to_anthropic.go:235）

两个特有的难点：

1. **output_index → block index 映射**（`OutputIndexToBlockIdx` :194）：Responses 事件按 output_index
   引用 item，Anthropic 按顺序 block index，状态机负责翻译。
2. **缓冲模式**（`NewBufferedResponsesEventToAnthropicState` :226）：Chat 系上游会**交错**发送并行
   工具调用的参数 delta，而 Anthropic 要求 content block 严格有序生命周期（start→delta*→stop）。
   缓冲模式把参数累积在 `PendingTools`，完成时由 `flushPendingAnthropicTools`（:725）按 output_index
   排序后整段发出 start+delta+stop；参数非合法 JSON 则发 error 事件。

### Chat 流 → Responses 流（`ChatCompletionsToResponsesStreamState`，chatcompletions_responses_bridge.go:624）

- reasoning delta 到达前必须先开 reasoning item（`output_item.added` + `reasoning_summary_part.added`，
  `ensureChatReasoningItem` :878——注释 :875 说明 Codex 客户端没有完整生命周期会丢弃 delta）
- 首个正文 delta 会先关闭 reasoning item 再开 message item（:718-731）
- **arguments 有状态聚合**（:772）：`function_call_arguments.done` 必须携带完整 arguments，
  所以状态机累积分片，创建 item 时清空首片防重复计数（:741-744）
- 终止时 `FinalizeChatCompletionsResponsesStream`（:792）补发全部 done 事件——
  注释 :991 明确说不做这步 Codex 会话会卡死
- 只有 reasoning 没有正文时，`synthesizeChatReasoningFallbackMessage`（:933）把 reasoning 文本合成为正文

### 断流兜底

每条流路径都有 `Finalize*` 函数（responses_to_anthropic.go:273、
anthropic_to_responses_response.go:238、responses_to_chatcompletions.go:170），
上游未发终止事件就断开时合成 `response.completed` / `message_stop`，usage 取状态机已累计值，**幂等**。

### wire 序列化的字段存在性（responses_stream_event_wire.go）

`ResponsesStreamEvent.MarshalJSON`（:24）对每种事件类型显式构造 wire JSON，强制保留
Go `omitempty` 会丢掉的零值字段（`output_index: 0`、空 `content: []`、空 `arguments: ""`）——
Codex CLI 这类严格客户端**拒绝缺字段的 item/delta**。这是 Chat→Responses 和 Anthropic→Responses
两条转换链共用的"字段存在性唯一事实源"。

## 8. 横切细节

### usage 双向不漂移

Anthropic 互斥桶 ↔ OpenAI 包容桶的换算统一走 `pkg/usage` 的正投影 `ProjectOpenAI`
与逆投影 `SplitInclusive`（anthropic_to_responses_response.go:125、responses_to_anthropic.go:100），
保证两个方向的 cache token 口径一致。`ResponsesUsage.UnmarshalJSON`（types.go:342）还兼容
部分上游混用 Chat 风格 `prompt_tokens` 字段名的情况。

### schema 规范化

双向都对工具 parameters/input_schema 兜底为 `{"type":"object","properties":{}}`
（anthropic_to_responses.go:460、responses_to_anthropic_request.go:586）——
OpenAI 端缺 `properties` 会报错，Anthropic 兼容端点拒绝 null schema（422）。

### cache_control 与 metadata

`AnthropicContentBlock.CacheControl`（types.go:58）定义在 wire 类型上，但 Responses 无从表达，
两条转换路径都不携带；`AnthropicRequest.Metadata`（types.go:28-33 有长注释）则必须原样透传——
OAuth/Claude-Code 路径依赖 `metadata.user_id` 参与上游"是否官方 Claude Code 请求"的判定。

### 降级链总结

```
/v1/responses 请求
 ├─ 渠道 = Anthropic API-key → 直接走 Responses→Anthropic（不试原生）
 ├─ 渠道 = Claude OAuth 订阅 → Responses→Anthropic（保留 thinking 等扩展）
 ├─ 渠道 = Codex OAuth      → 直通
 └─ 其他渠道 → 原生透传
      ├─ 成功 → 结束
      ├─ 400/404/405/501/502/503 → Responses→Chat fallback（同渠道改发 /chat/completions）
      └─ 协议能力错误 → 标记 ProtocolCapabilityError，换渠道重试
```

## 9. 关键文件速查

| 职责 | 文件 |
|---|---|
| 三套协议 DTO | `internal/apicompat/types.go` |
| Chat 请求 → Responses 请求 | `internal/apicompat/chatcompletions_to_responses.go` |
| Responses 响应/流 → Chat 响应/chunk | `internal/apicompat/responses_to_chatcompletions.go` |
| Responses ↔ Chat 反向桥（请求+响应+流） | `internal/apicompat/chatcompletions_responses_bridge.go` |
| Responses SSE wire 序列化（字段存在性） | `internal/apicompat/responses_stream_event_wire.go` |
| Responses 请求 → Anthropic 请求（含配对修复） | `internal/apicompat/responses_to_anthropic_request.go` |
| Anthropic 请求 → Responses 请求 | `internal/apicompat/anthropic_to_responses.go` |
| Anthropic 响应/流 → Responses 响应/流 | `internal/apicompat/anthropic_to_responses_response.go` |
| Responses 响应/流 → Anthropic 响应/流 | `internal/apicompat/responses_to_anthropic.go` |
| Anthropic ↔ Chat 组合桥（经 Responses hub） | `internal/apicompat/anthropic_chat_bridge.go` |
| Adaptor 接口 / Format / RelayContext | `internal/adaptor/adaptor.go:26,58,86` |
| 渠道类型 → Adaptor 注册 | `internal/adaptor/registry.go`、`register.go:56-128`、`register_oauth.go:45-68` |
| 各 adaptor 转换矩阵 | `openai_compatible.go`、`anthropic.go`、`codex_oauth.go`、`claude_oauth.go`、`chat_compat.go` |
| 路由注册与 gate | `internal/server/routes.go:14-71`、`orchestrator_gate.go` |
| /v1/responses 降级链 A/B | `internal/server/responses_fallback.go:756（A）, 29（B）` |
| usage 桶投影 | `pkg/usage`（ProjectOpenAI / SplitInclusive） |
| 覆盖矩阵测试 | `internal/apicompat/compatibility_matrix_test.go`、`matrix_fixtures_test.go` |

## 10. 设计经验小结

1. **hub-and-spoke 优于全互联**：3 个协议全互联需 6 组转换器；以 Responses 为 hub 后，
   Anthropic↔Chat 复用组合桥，新增第 4 个协议（如 Gemini）只需实现到 hub 的两条边。
2. **转换层是纯函数 + 显式状态机**：非流式是无状态纯函数；流式把"item 生命周期、index 映射、
   参数聚合"收敛到显式 State 结构体，配合幂等的 `Finalize*` 兜底断流。
3. **防御性规范化是生产必需品**：tool_use/tool_result 配对修复、空参数补 `"{}"`、
   schema 兜底、空输出补 `"(empty)"`——每一条都对应某个真实上游的 400/422。
4. **字段存在性 ≠ 字段值**：严格客户端（Codex CLI）要求零值字段也必须出现，
   Go 的 `omitempty` 语义会成为 bug 源，需要在 wire 层显式序列化。
5. **降级链分确定性与错误触发两类**：渠道类型可判定的（Anthropic API-key）在原生尝试前转换，
   不可判定的（端点是否支持 Responses）靠状态码触发 fallback + 协议能力错误换渠道。
