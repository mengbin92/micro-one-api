# `/v1/messages` Tool Call 丢失分析与 A+ 实施方案

- 日期：2026-08-22
- 结论状态：A+ 已实施完成（adaptor 收敛并硬切换）；等待发布部署验证
- 关联会话：codex CLI 会话（rollout-2026-08-22T08-04-48-*.jsonl）曾给出「方案1：网关层 tool call 容错/修复」，本文验证后判定该方案无效

## 1. 背景

Claude Code 通过 `ANTHROPIC_BASE_URL=https://api.mengbin.top`（本网关 relay-gateway）调用
`deepseek-v4-pro-0813` 时报错：

```
The model's tool call could not be parsed (retry also failed).
```

Claude Code debug 日志（`~/.claude/debug/e9ec7010-1dbd-4be2-a8a0-e1eafecd55e7.txt`）关键行：

```
00:24:04.624 [API REQUEST] /v1/messages source=repl_main_thread
00:24:09.421 Stream started - received first chunk
00:24:12.459 [API REQUEST] /v1/messages source=repl_main_thread   ← CC 自动重试
00:24:15.496 Stream started - received first chunk
00:24:18.068 [engine] turn 1 end (turns=2 usage in=69466 out=900 cost=$0.3698
                      api=22463ms stop=stop_sequence resultLen=0)
00:24:18.069 [ERROR] [engine] turn ended in error: The model's tool call could not be parsed (retry also failed).
```

要点：请求成功发出、流式响应正常开始、模型生成了 ~900 output token，但 CC 侧
`resultLen=0`（没有任何可用内容），两次尝试均解析失败。

codex 会话的推断是「DeepSeek 模型输出的 tool call JSON 非法（官方文档承认非 strict mode
下可能出现），Claude 解析失败」，并推荐方案1（网关层修复 JSON / 缓冲拼装 / 降级文本）。

## 2. 根因验证

### 2.1 代码层面（决定性证据）

`/v1/messages` 走非订阅渠道（DeepSeek 渠道）的链路：

```
Claude Code → handleAnthropicMessages          internal/server/anthropic_inbound.go:376
  → convertAnthropicToChatCompletions          ✅ 请求方向 tool_use → tool_calls 已转换 (:165-206)
  → OpenAIProvider.ChatCompletionsStream       ✅ SSE 已解析，Delta.ToolCalls 字段可用
                                                (domain/upstream/provider/provider.go:174, :523-529)
  → handleAnthropicStreamingResponse           ❌ 流式回转 —— tool call 在这里被丢弃
                                                (internal/server/anthropic_inbound.go:546-791)
```

`handleAnthropicStreamingResponse` 的流式循环（anthropic_inbound.go:623-718）只处理三样东西：

- `choice.Delta.ReasoningContent` → thinking 块
- `choice.Delta.Content` → text 块
- `choice.FinishReason` → stop_reason 映射

**`choice.Delta.ToolCalls` 从头到尾没有被读取** —— 上游发来的 tool call 增量被静默丢弃。

对照：非流式路径（`convertChatCompletionsToAnthropic`，anthropic_inbound.go:322）是处理
`choice.Message.ToolCalls` → tool_use 块的。**这是纯流式缺口**。

### 2.2 生产日志时间轴对齐（43.133.65.212 / relay-gateway）

| Claude Code debug | relay-gateway 线上日志 |
|---|---|
| 00:24:04 请求发出 | 00:24:08 路由 channel 8（DeepSeek 官方）→ `sensitive_words` 拦截（policy_rejection）→ fallback channel 1，「成功」，out=**275** |
| 00:24:12 自动重试 | 00:24:15 路由 channel 1，「成功」，out=**180** |
| resultLen=0，out=900 | 275 + 180 + 445（标题生成请求）= 900 ✅ 模型确实生成了 tool call token |

即：模型每次都生成了约 200 token 的 tool call（走的是带 tools 的请求，input ~34k），
网关两次都「成功」，但 CC 收到的是：thinking 块 + 空文本块 + `stop_reason: tool_use`，
**没有任何 tool_use 内容块** —— 所以报 "tool call could not be parsed"。

### 2.3 附带澄清两个细节

- debug 日志里的 `stop=stop_sequence`：整个网关代码没有任何路径会输出
  `stop_sequence` 作为 stop_reason（`responses_to_anthropic.go` 只产生
  end_turn/tool_use/max_tokens；`anthropic_inbound.go` 的映射同理，见 :793-804）。
  该值是 Claude Code 解析失败路径自己合成的，可忽略。
- 渠道确认：两次路由日志 `provider_family: "deepseek"`，channel 1（自建）/ channel 8
  （官方）均为 OpenAI 兼容渠道 → 都走上述 `handleAnthropicStreamingResponse` 路径。

## 3. 对 codex 方案1 的判定：无效

方案1 前提是「DeepSeek 返回的 tool call JSON 非法，需要修复」。实际上 arguments JSON
**从未离开过网关**：

| 方案1 子项 | 判定 | 原因 |
|---|---|---|
| 修复非法 JSON（补括号、去尾逗号） | ❌ 无的放矢 | 网关没有转发 arguments，没有东西可修 |
| 缓冲拆散的 delta 拼完整再发 | ❌ 无从谈起 | 现在压根不发任何 tool call delta |
| 解析失败降级为文本 | ⚠️ 等价于现状 | 「降级成空文本」正是 bug 的表现而非解法 |

方案1 里唯一值得保留的想法是：在 `content_block_stop` 前校验累积后的完整 arguments。
但 `sanitizeAnthropicToolUseInput` 只清理 `Read.pages=""`，并不是通用 JSON 修复器；当前也
没有证据表明本次故障包含非法 JSON。因此本次不应加入补括号、删尾逗号等猜测式修复。
若最终 JSON 非法，应发送明确的 Anthropic stream error 并终止当前块，不能静默改成 `{}`
或文本，否则会把上游错误伪装成一次语义不同的工具调用。

## 4. 修复方案

### 4.1 评审结论：选 A，但原方案需要升级为 A+

项目尚未正式上线、用户只有两三个时，直接消除 legacy 路径比先做 B、再迁移 A 更合理：
B 会在 `internal/server` 再造一套 tool-call 状态机，而同类状态机已经存在于
`internal/apicompat`。上线后再迁移反而需要双栈、灰度和更大的兼容成本。

不过，原方案 A 仍不是最优落地方式，原因有四点：

1. **忽略了现成的统一抽象**：`internal/adaptor.Adaptor` 已明确承担「入站协议 → 上游协议 →
   客户端协议」的职责，并已注册 OpenAI-compatible、Anthropic、Gemini、Azure 和订阅渠道。
   在 `anthropic_inbound.go` 手工拼转换器会形成第三套编排逻辑。
2. **不需要重写路由和计费**：`Plan`、用户 RPM、`ExecuteWithCandidates`、每次重试的模型
   remap、reserve/release/commit、selection finalize 都应原样保留；只替换单次 channel
   attempt 内的协议适配和上游调用。
3. **Responses 不是所有上游都必须绕行的 wire format**：Anthropic API-key 渠道收到
   `/v1/messages` 时应原样走 `/v1/messages`，避免无意义的 Anthropic → Responses →
   Anthropic 往返；OpenAI-compatible 渠道才使用 Responses 作为内部跨协议中间模型。
4. **现有 apicompat 仍有收敛前置缺口**：cache creation 反向映射、并行 tool call 的流式
   生命周期、`stop_sequences`、stream usage 请求和非法 arguments 终止语义尚未全部闭合。
   直接接线会把「tool call 被丢弃」换成更隐蔽的协议差异。

因此采用 **方案 A+：完成 adaptor 收敛并硬切换，不实现 B，不保留运行时双栈**。

### 4.2 A+ 的目标结构

```text
POST /v1/messages
  ├─ parse + validate: apicompat.AnthropicRequest
  ├─ Plan + user RPM + session stickiness
  ├─ subscription channel
  │    └─ 现有 handleAnthropicMessagesViaAdaptor（账户级 failover 保持不变）
  └─ API-key channel
       └─ 现有 ExecuteWithCandidates（渠道级 retry/fallback 保持不变）
            └─ executeChannelAttemptViaAdaptor
                 ├─ reserve quota + per-channel model remap
                 ├─ Adaptor.ConvertRequest
                 ├─ Adaptor.BuildUpstreamRequest + HTTP call
                 ├─ Adaptor.ConvertResponse / ConvertStreamResponse
                 └─ commit/release quota + usage log
```

Adaptor 按上游原生协议处理：

| 上游类型 | 请求/响应策略 |
|---|---|
| OpenAI-compatible（含 DeepSeek） | Anthropic → Responses → Chat；Chat → Responses → Anthropic |
| Anthropic API-key | 重写 model 后原生 Messages 透传；不经过 Chat |
| Codex / Claude / 国内 coding plan 订阅 | 保留现有 OAuth adaptor 与账户 failover |
| Gemini / Azure | 切换前补齐并通过各自兼容矩阵；不能静默落回 legacy handler |

`executeChannelAttemptViaAdaptor` 只负责 **API-key 单次尝试**，不能直接复用
`executeSubscriptionAccountViaAdaptor`：后者包含账户并发、账户 RPM、session window、凭据
解析和账户熔断。两者可以共享一个很小的「build request → HTTP → convert response」内部
函数，但重试和计费所有权仍分别留在各自调用方，避免把两种故障域混在一起。

### 4.3 接线前必须补齐的 apicompat / adaptor 不变量

1. **请求字段完整性**
   - `/v1/messages` 直接反序列化为 `apicompat.AnthropicRequest`，删除 server 私有重复 DTO；
   - 组合转换必须覆盖 system string/blocks、text/image、tool_use/tool_result、tool_choice、
     `max_tokens`、`temperature`、`top_p` 和 `stop_sequences`；
   - `AnthropicToResponses` 当前默认生成 `reasoning.effort=medium`。转 Chat 时不得给不支持
     `reasoning_effort` 的模型无条件注入该字段；只在请求明确开启 thinking/output_config
     或渠道能力确认支持时发送；
   - streaming Chat 请求强制 `stream_options.include_usage=true`，否则真实 token/cache
     用量经常拿不到。
2. **tool call 生命周期**
   - 按 OpenAI `tool_call.index` 累积 id/name/arguments；
   - 单调用、多个并行调用、多个 chunk 交错到达都必须生成严格配对且单调递增的 Anthropic
     block index；不能在 tool 0 尚未收全时因 tool 1 到达就永久关闭 tool 0；
   - text → thinking → tool_use，以及 reasoning-only / tool-only 均必须闭合；
   - 完整 arguments 在 `content_block_stop` 前用 `jsonx.Valid` 校验。非法时发 Anthropic
     `error` 事件并结束 stream，不猜测修复、不伪造 `{}`。
3. **usage 语义**
   - `ChatUsageToResponsesUsage` 同时映射 cached、cache_creation_5m、cache_creation_1h；
   - `anthropicUsageFromResponsesUsage` 按 Anthropic 语义输出：`input_tokens` 排除
     cache-read 和 cache-creation，`cache_creation_input_tokens` 为两个 creation bucket 之和；
   - 网关计费始终取上游原始 usage，保留 5m/1h 分桶，不能从已转换给客户端的 Anthropic
     usage 反推；无 usage 时才用估算值。
4. **stream 失败语义**
   - 上游在首字节前失败：允许 `ExecuteWithCandidates` fallback，并 release 当前 reservation；
   - 已向客户端写出 `message_start` 后失败：禁止切换渠道或再写 JSON HTTP error，发送一次
     Anthropic stream error 后结束；
   - 正常完成、提前 EOF、scanner error、客户端取消都只能发生一次 quota commit/release。

### 4.4 实施顺序（同一个变更完成，不拆成“以后再做”）

1. 在 `internal/apicompat` 增加端到端组合测试，先锁定 Anthropic ↔ Chat 的非流式和流式
   事件序列；特别加入 DeepSeek reasoning + tool call fixture 与两个并行 tool call 交错 fixture。
2. 补齐上述请求字段、tool lifecycle、usage 和 stream error 不变量。
3. 完成 `OpenAICompatibleAdaptor` / `AnthropicAdaptor` 对
   `FormatAnthropicMessages` 的 request/response/stream 转换；对当前已启用的 Azure/Gemini
   渠道同步补齐，未支持的类型必须在切流前显式报错并从可选矩阵排除。
4. 在现有 `handleAnthropicMessages` 的 `ExecuteWithCandidates` 回调内接入
   `executeChannelAttemptViaAdaptor`，保留原有 Plan、RPM、model remap、quota、usage log 和
   selection finalize 代码。
5. 兼容矩阵全部通过后，删除 `convertAnthropicToChatCompletions`、
   `convertChatCompletionsToAnthropic`、`handleAnthropicStreamingResponse` 及 server 私有重复
   Anthropic DTO；不加 feature flag，不保留 B 作为 fallback。回滚依赖 Git/上一版镜像。

### 4.5 必须通过的验收矩阵

| 维度 | 最低验收 |
|---|---|
| 客户端 | Claude Code、CC Switch、普通 Anthropic SDK；stream / non-stream |
| 内容 | text、thinking+text、tool-only、text+tool、image、tool_result |
| 工具 | 单调用、并行调用、arguments 分片/交错、空对象、非法最终 JSON |
| 终止 | end_turn、tool_use、max_tokens、正常 EOF、提前 EOF、scanner error、取消 |
| usage | 无 cache、cache_read、creation 5m、creation 1h、上游不返回 usage |
| 路由 | DeepSeek/OpenAI fallback、每渠道 model remap、reserve/release/commit 恰好一次 |
| 原生协议 | Anthropic API-key 原样 Messages；订阅 adaptor 行为不回归 |

完成标准：

- DeepSeek fixture 中每个 tool call 都收到且仅收到一组
  `content_block_start → input_json_delta* → content_block_stop`；
- 最终 `message_delta.stop_reason=tool_use`，之后恰好一个 `message_stop`；
- 并行 tool call 的 block index 唯一、递增、无关闭后继续 delta；
- 非流式与流式输出在 content blocks、stop reason 和 usage 语义上等价；
- `go test ./internal/apicompat ./internal/adaptor ./internal/server`、`go test -race`（相关包）
  和 `./scripts/check-architecture.sh` 全部通过；
- 仓库中不再存在 `/v1/messages` 的第二套手写 Chat → Anthropic 流式状态机。

### 方案 B 的最终定位

B 仅适合已经大规模上线、必须数小时内止血且无法承担行为切换时使用。本项目当前不满足
这个前提，因此 **不实施 B**。这也意味着不为 B 写生产代码或运行时开关，避免形成必删债务。

### 附带运维建议

日志窗口内 3 次请求全部是 channel 8 被 `sensitive_words` 拦截 → fallback channel 1，
每次白烧 5-7 秒和一遍 input token（~34k）。建议把 `deepseek-v4-pro-0813` 直接钉到
channel 1，或对带 tools 的流量降权 channel 8。

## 5. 关键文件索引

| 文件 | 作用 |
|---|---|
| `internal/server/anthropic_inbound.go` | `/v1/messages` 入口、Plan/RPM/retry/finalize；不再包含协议转换 |
| `internal/server/anthropic_adaptor_attempt.go` | API-key 单渠道 adaptor 调用、quota 与原始 usage 采集 |
| `domain/upstream/provider/provider.go:480-544` | OpenAI 兼容流式读取（ToolCalls 可用） |
| `internal/adaptor/adaptor.go` | 统一协议/上游适配接口，A+ 的收敛边界 |
| `internal/adaptor/chat_compat.go` | Anthropic/Responses ↔ Chat 的 adaptor 公共编排 |
| `internal/adaptor/openai_compatible.go` | OpenAI-compatible（含 DeepSeek）Anthropic 入站适配 |
| `internal/adaptor/anthropic.go` | Anthropic API-key 原生 Messages 透传 |
| `internal/adaptor/gemini.go` | Gemini 原生请求/响应/流式桥接（含图片与工具调用） |
| `internal/adaptor/register.go` | API-key channel type → adaptor 注册矩阵 |
| `internal/server/http_adaptor.go` | 现有订阅 adaptor 执行/计费参考；账户逻辑不得照搬到 API-key |
| `internal/apicompat/anthropic_to_responses.go` | Anthropic 请求 → Responses 内部模型 |
| `internal/apicompat/anthropic_chat_bridge.go` | Anthropic ↔ Chat 端到端组合转换 |
| `internal/apicompat/responses_to_anthropic.go` | Responses → Anthropic usage/并行工具/非法 JSON 终止语义 |
| `internal/apicompat/chatcompletions_responses_bridge.go` | OpenAI SSE → Responses 事件桥 |
| `internal/apicompat/*_test.go` | `TestStream_ToolCallLifecycleComplete` 等成套流式 tool call 测试 |

## 6. 实施结果（2026-08-22）

A+ 已按“同一个变更完成、无运行时双栈”的原则落地：

1. `/v1/messages` 直接解析 `apicompat.AnthropicRequest`；server 私有 Anthropic DTO、
   `convertAnthropicToChatCompletions`、`convertChatCompletionsToAnthropic` 和
   `handleAnthropicStreamingResponse` 已全部删除。
2. API-key 单次尝试统一经过 `Adaptor.ConvertRequest → BuildUpstreamRequest →
   ConvertResponse/ConvertStreamResponse`；原 `Plan`、RPM、session stickiness、
   `ExecuteWithCandidates`、每渠道 model remap、reserve/release/commit 和 selection finalize
   保持在原所有权边界内。
3. OpenAI-compatible/DeepSeek 与 Azure 使用 Anthropic → Responses → Chat 及反向桥接；
   Anthropic API-key 保留未知扩展字段，仅重写路由后的 model，并原生透传 Messages；
   Gemini 已补齐 system、text/image、tool_use/tool_result、tool choice、stream 和 usage 转换，
   没有静默回落 legacy handler。
4. Chat 流式工具调用按 index 缓冲，支持多个并行调用交错到达，再按唯一递增的 Anthropic
   block index 顺序输出；最终 arguments 必须是 JSON object，非法或提前 EOF 时发送一次
   Anthropic `error`，不伪造成功终止事件。
5. 客户端 Anthropic usage 与网关计费 usage 分离：前者按 Anthropic 语义扣除 cache-read/
   cache-creation，后者始终观察上游原始响应并保留 5m/1h 分桶；同时补齐 Gemini
   `usageMetadata` 解析。

验证结果：

- `go test ./internal/apicompat ./internal/adaptor ./internal/server`：通过；
- `go test -race ./internal/apicompat ./internal/adaptor ./internal/server`：通过；
- `./scripts/check-architecture.sh`：通过；
- `go test ./...`：除需要预先启动 identity/admin/relay/billing 服务的
  `test/e2e/suite` 外，其余包均通过；E2E 失败原因为本机 `127.0.0.1:9001/9004/3000/8080`
  未启动，不是本次代码断言失败。

发布前只剩真实 Claude Code / CC Switch 对 DeepSeek 渠道的部署后 smoke test；代码层不保留
方案 B、feature flag 或 legacy fallback。
