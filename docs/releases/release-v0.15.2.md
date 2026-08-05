# Micro-One-API v0.15.2 发布：订阅流式中断补齐终止事件、渠道统计去噪修复回归

> 2026-08-05 · 上一版：[v0.15.1](./release-v0.15.1.md)（2026-08-05）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.15.2)

v0.15.2 是 v0.15.1 之后的 **PATCH 修复版本**（3 个提交），全部位于 relay-gateway 的流式转发链路与渠道用量统计：修复 v0.15.1 渠道统计去噪因赋值顺序反转而完全失效的回归；修复订阅来源 Anthropic 协议上游（如 kimi）流式响应在中断 / 提前关闭时不发终止事件、导致 codex 客户端报 "stream disconnected before completion" 的三个缺陷；并将 `response.completed` 之后的兜底 `response.failed` 守卫补齐到 adaptor 路径。

**无数据库迁移、无 API 破坏性变更、无新增配置项**。受影响的运行时服务为 `relay-gateway`。

## 变更内容

### 1. 渠道统计去噪回归：`applyPlanInputs` 赋值顺序反转

**根因**：v0.15.1 的渠道统计去噪修复（13ccb74）上线后，生产日志中订阅来源
流量（z.ai，合成 channel id）的 `failed to record channel usage ... channel not
found` 告警依然持续。排查发现 `applyPlanInputs` 把
`upstreamCostKeyInputsFromPlan` 的两个返回值按错误顺序接收：

```go
in.UpstreamModelID, in.SourceKind = upstreamCostKeyInputsFromPlan(plan)
```

helper 实际返回 `(sourceKind, upstreamModelID)`，导致 `SourceKind` 恒为空、
`UpstreamModelID` 被写成字面量 `"subscription"`。订阅请求因此永远无法命中
`recordChannelUsageFromDetail` 的跳过分支；被污染的 `UpstreamModelID` 同时破坏了
订阅流量的规范化计费 cost key 查找。

**修复**：按正确顺序接收返回值，订阅流量恢复跳过 channel 维度统计，cost key
恢复正常。

### 2. 上游流中断 / 提前关闭时补齐终止 SSE 事件

**根因**：订阅来源的 Anthropic 协议上游（如 kimi）在三种场景下会导致 codex
客户端报 "stream disconnected before completion" / "stream closed before
response.completed"：

1. **SSE data 行解析**：adaptor 路径的 `sseData` 只接受 `data: `（带空格），
   而 kimi 发送 `data:`（不带空格，符合 SSE 规范）。kimi 的每一行数据都被
   静默丢弃，转换器看到空流，从未发出 `response.created` / `response.completed`，
   客户端判定流已断开。
2. **scanner.Err() 分支**（上游中途断连）：发出了 `response.failed` 但缺少
   `[DONE]` 哨兵，客户端一直等待。
3. **上游在 `message_start` 之前关闭**：
   `FinalizeAnthropicResponsesStream` 因 `CreatedSent=false` 返回 nil，未发出
   任何终止事件，管道静默关闭。

**修复**：

- `sseData` 同时接受两种 `data:` 前缀形式。
- `scanner.Err()` 分支在终止事件后追加 `[DONE]`。
- 提前关闭时合成 `response.failed` 终止事件。
- 修复同时应用于 `pumpAnthropicToResponses`（adaptor 路径，所有 Anthropic 兼容
  订阅账号使用）与 `transformAnthropicStreamToResponses`（fallback 路径），并为
  两条路径补充无空格 SSE 格式、提前关闭、scanner 错误的回归测试。

### 3. adaptor 路径补齐 completed+failed 双终止守卫

**根因**：v0.15.1 之前为 `responses_fallback.go` 的 `scanner.Err()` 分支加过的
`CompletedSent` 守卫（550c1ad）没有同步到 adaptor 路径
`pumpAnthropicToResponses`。当上游 Anthropic 流正常到达终止状态
（`message_start` + `message_stop` ⇒ `response.completed`）**之后**连接才报错
时，adaptor 路径会在已发出的 `response.completed` 之上再无条件写一个
`response.failed`——客户端无法调和的矛盾双终止。

**修复**：镜像该守卫——仅当 `!state.CompletedSent` 才写
`writeResponsesStreamError`；已完成的流只追加 `[DONE]` 哨兵。回归测试
`TestPumpAnthropicToResponsesCompletedThenScannerError` 覆盖该边界。

## 兼容性说明

- **API**：无破坏性变更。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.15.2

# cross-build 并部署受影响的服务
./scripts/deploy-update.sh relay-gateway
```

## 验证

- 订阅来源流量（z.ai 等）不再刷 `failed to record channel usage ... channel not found` 告警。
- kimi 等不带空格的 `data:` SSE 行被正确解析，`response.created` / `response.completed` 正常发出。
- 上游中途断连：客户端收到 `response.failed` + `[DONE]`，不再悬挂。
- 上游提前关闭：合成 `response.failed` 终止事件。
- `response.completed` 之后的连接错误不再叠加 `response.failed`。
- `go build ./...`、`go vet`、69 个包单元测试通过。

## 完整变更日志

- fix(relay): applyPlanInputs reversed source-kind/upstream-model assignment
- fix(relay): ensure terminal SSE event on upstream stream interruption
- fix(relay): guard adaptor-path scanner error against completed+failed
