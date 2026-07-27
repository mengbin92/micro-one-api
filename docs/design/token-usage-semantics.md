# ADR：统一 Token 计费桶语义（v0.11.0 Phase 0）

> 状态：Accepted（Phase 0 设计门，评审通过前不进入 Phase 1）
>
> 关联：`docs/design/v0.11.0-roadmap.md` Phase 0、`docs/TODO.md` 待办 — cache_creation 全链路支持
>
> 范围：raw relay 解析层（`internal/server/http_raw_helpers.go`）、provider 转换层
> （`domain/upstream/provider`）、计费层（`app/billing/internal/biz`）。本文档只
> 固定语义与测试向量，**不修改 proto、DO、PO、数据库或扣费行为**。

## 1. 背景与动机

当前账务把 `cache_read` 当作 `prompt` 的子集处理（见
`internal/server/http_usage_log.go` 的 `logUpstreamUsage` 与
`app/billing/internal/biz/billing.go` 的 `calculateModelPriceCost`），并且
`rawUsage`、provider `Usage`、日志、账本和 proto 均无 `cache_creation`
承载字段。Anthropic / GLM 等兼容上游返回的 `cache_creation_input_tokens`（含
`cache_creation.ephemeral_5m/1h_input_tokens` TTL 细分）被直接丢弃，导致：

- 缓存创建量丢失，无法统计也无法按缓存创建价计费；
- 直接给结构补字段仍可能重复扣减或漏扣，因为 OpenAI 与 Anthropic 的桶关系
  是不同模型（OpenAI 的 cached 是 prompt 子集；Anthropic 的 input /
  cache-read / cache-creation 是互斥桶）。

因此必须先定义一套不重叠的规范计费桶，并固定测试向量，再在 Phase 1 落字段。

## 2. 决策：规范计费桶

系统内部统一使用以下五个**互斥**计费桶，所有协议的 usage 在进入计费前必须
规范化为这五个桶：

| 规范桶 | 含义 | 单调性 |
|--------|------|--------|
| `uncached_input_tokens` | 不属于缓存读取或缓存创建的输入 token | ≥ 0 |
| `cache_read_tokens` | 从 prompt cache 读取（命中）的 token | ≥ 0 |
| `cache_creation_5m_tokens` | 写入 5 分钟 TTL cache 的 token | ≥ 0 |
| `cache_creation_1h_tokens` | 写入 1 小时 TTL cache 的 token | ≥ 0 |
| `output_tokens` | 输出 token | ≥ 0 |

五桶之和即“真实计费总 token”，对应参考实现 cc-switch 的
`realTotalTokens = input + output + cache_creation + cache_read`（所有
cache-normalized，见 `../cc-switch/src/types/usage.ts`），以及 sub2api 的
`UsageTokens` 五字段结构（`../sub2api/backend/internal/service/billing_service.go`
`InputTokens / OutputTokens / CacheReadTokens / CacheCreation5mTokens /
CacheCreation1hTokens`）。

### 2.1 与现有兼容字段的关系

- `prompt_tokens` 保留现有 API / 日志含义，**不直接改名或删除**。billing 使用
  规范桶计算，避免从一个兼容字段猜测供应商语义。
- `total_tokens` 同样保留作为对外兼容字段；当其与规范桶之和冲突时，计费以
  规范桶之和为准，`total_tokens` 只用于日志展示与 preflight 异常检测。

## 3. 兼容规则（按供应商协议）

### 3.1 OpenAI 兼容（`/v1/chat/completions`、`/v1/embeddings` 等）

OpenAI 的 `prompt_tokens_details.cached_tokens` 是 `prompt_tokens` 的**子集**，
按子集语义规范化：

```
cache_read_tokens         = cached_tokens
uncached_input_tokens     = max(prompt_tokens - cached_tokens, 0)
cache_creation_5m_tokens  = 0
cache_creation_1h_tokens  = 0
output_tokens             = completion_tokens
```

来源字段映射：

- `prompt_tokens`（兼容回退：`input_tokens`）
- `completion_tokens`（兼容回退：`output_tokens`）
- `prompt_tokens_details.cached_tokens` / `input_tokens_details.cached_tokens`
  / 顶层 `cache_read_tokens` / `cached_tokens`
- `total_tokens`

参考实现：`internal/server/http_raw_helpers.go` 的 `cacheReadTokensFromUsageMap`
已覆盖 OpenAI cached 的读取；`app/billing/internal/biz/billing.go` 的
`calculateModelPriceCost` 已按 `input - cacheRead` 计算 uncached input。

### 3.2 OpenAI Responses（`/v1/responses`）

`input_tokens_details.cached_tokens` 同样是 `input_tokens` 子集，沿用 3.1 子集
规则。若 Responses 路径同时出现 `cached_tokens` 与 `cache_creation_tokens`
（部分兼容上游会补这个字段），`cache_creation_tokens` 写入一个**临时聚合桶**
`cache_creation_total_tokens`，TTL 不明细分桶规则见 §4.2。

### 3.3 Anthropic Messages（`/v1/messages`、`/v1/messages?beta=...`）

Anthropic 的 `input_tokens`、`cache_read_input_tokens`、
`cache_creation_input_tokens` 是**互不重叠**的桶，规范化时**不得相减**：

```
uncached_input_tokens     = input_tokens
cache_read_tokens         = cache_read_input_tokens
cache_creation_total      = cache_creation_input_tokens
cache_creation_5m_tokens  = cache_creation.ephemeral_5m_input_tokens
cache_creation_1h_tokens  = cache_creation.ephemeral_1h_input_tokens
output_tokens             = output_tokens
```

**重要语义**：`uncached_input_tokens` 在 Anthropic 下**就是** `input_tokens`
本身，不要做 `input_tokens - cache_read_input_tokens`，否则会把命中的缓存读
重复扣减一次。这与 OpenAI 的子集语义不同，是本 ADR 最容易出错的地方，fixture
与单元测试必须覆盖两条路径并断言差异。

参考实现：`../sub2api/backend/internal/service/gateway_anthropic_passthrough.go`
的 `parseSSEUsagePassthrough` 解析了 `input_tokens` /
`cache_creation_input_tokens` / `cache_read_input_tokens` 与嵌套
`cache_creation.ephemeral_5m/1h_input_tokens`，并明确注释“与通用解析一致：
message_start 允许覆盖 5m/1h 明细（包括 0）”。

### 3.4 Chat Completions 转换路径（Anthropic provider → OpenAI 响应）

`domain/upstream/provider/anthropic.go` 将 Anthropic 响应转成 OpenAI
`ChatCompletionsResponse`。转换时 `Usage` 必须承载与 §3.3 相同的五桶，**不得**
把 `cache_creation` 折叠进 `prompt_tokens` 或丢弃。具体：

- `PromptTokens` ← Anthropic `input_tokens`（**不含** cache 部分）
- `PromptTokensDetails.CacheReadTokens` ← `cache_read_input_tokens`
- 新增承载 `CacheCreation5mTokens` / `CacheCreation1hTokens` 字段（Phase 1 落地）
- `CompletionTokens` ← `output_tokens`

流式合并沿用 `message_start`（input 侧）+ `message_delta`（output 侧）的
back-fill 策略（见现有 `anthropic.go` `startUsage` 合并注释），但 cache 字段
也必须 back-fill：`message_delta` 若省略 cache 字段，则沿用 `message_start`
的 cache 值，最终 chunk 的五桶必须等于非流式结果。

## 4. 异常与边界规则

### 4.1 负数与溢出

- 任何桶出现负数 → 归零，记录 `token_usage_parse_anomaly` 指标（label：
  `reason=negative`），不抛错、不改变扣费路径。
- 单桶超过 `math.MaxInt64` 视为上游异常 → 记录指标
  （`reason=overflow`），该桶按 0 处理。

### 4.2 TTL 明细与总量冲突

供应商可能只返回 `cache_creation_input_tokens` 总量而无
`cache_creation.ephemeral_5m/1h_input_tokens` 明细。**固定策略**：

- **不依据模型名猜测 TTL**（roadmap 明确禁止）。当且仅当无任何 TTL 明细时，
  将总量全部写入 `cache_creation_5m_tokens`，并在计费层用 5m 单价计费。
  选择 5m 而非 1h 作为默认桶，因为 Anthropic 官方默认 cache TTL 为 5 分钟，
  且参考实现 sub2api 的 `applyCacheTTLOverride`（`gateway_upstream_response.go`）
  在“只有聚合字段但无 5m/1h 明细”时也把聚合字段归入 5m 默认类别。
- 若同时返回了总量与明细，且 `明细之和 > 总量`：记录
  `reason=ttl_detail_exceeds_total` 指标，**以明细为准**计费（明细更精确），
  不回退到总量；总量仅用于异常检测。
- 若同时返回了总量与明细，且 `明细之和 < 总量`：差额计入 `cache_creation_5m_tokens`
  （未分类部分按默认 TTL）。

### 4.3 缺失 usage

- 完全无 usage 块 → 沿用现有 `fallback` 估计（`estimateRawUsage`，
  `len(body)/4` 字符近似）。Phase 0 不改这条路径，Phase 1 评估是否保留。
- 流式请求缺少 `message_delta` usage → 不计费，记 `reason=stream_usage_missing`
  指标；Phase 1 的 charge 模式下按 fallback 估计并标记为估算。

## 5. 计费层（observe / charge）

Phase 0 只固定语义，不落扣费；为 Phase 1 预留接口：

- 新增纯计算函数 `calculateCanonicalCost(pricing, buckets)`，输入为五规范桶
  + 价格（`InputPrice / OutputPrice / CacheReadPrice /
  CacheCreation5mPrice / CacheCreation1hPrice`），输出 `int64`（以 `AmountScale`
  为单位的整数成本）。用户费用、上游成本、订阅用量和 reconciliation 都复用
  **同一个**纯函数，避免四处复制价格公式（roadmap §1.3 要求）。
- 未配置 `cache_creation_5m_price` 时保持 v0.10.2 行为（不收 cache_creation
  费用），并标记 `unpriced`，**不默认套用 input price**。
- 观察开关：`BILLING_CACHE_CREATION_MODE=observe|charge`，默认 `observe`；
  observe 模式写 token 与影子成本（shadow cost）但不改用户余额。

参考实现：sub2api 的 `computeCacheCreationCost`（`billing_service.go`）已给出
5m/1h 分桶计价的正确公式，可直接借鉴：
当 `SupportsCacheBreakdown` 且存在 5m/1h 价格时，
`cost = 5m_tokens*price5m + 1h_tokens*price1h`；只有聚合无明细时回退到
`total * price5m`。

## 6. 测试向量（fixture）

在 `internal/server/http_raw_helpers.go` 旁新增
`token_usage_fixture_test.go`，定义表驱动的 fixture，每条 fixture 同时供
raw relay、provider 转换和 billing 三层消费，断言相同的五桶结果。

覆盖矩阵（roadmap §6 最少要求）：

| ID | 协议 | 场景 | 预期五桶 |
|----|------|------|----------|
| F1 | OpenAI 非流式 | cached 子集 | uncached=200, read=100, create5m=0, create1h=0, output=50 |
| F2 | OpenAI 流式 | 首尾 usage 合并 | 同 F1 |
| F3 | Anthropic 非流式 | 5m 明细 | uncached=300, read=60, create5m=40, create1h=0, output=25 |
| F4 | Anthropic 非流式 | 1h 明细 | uncached=300, read=60, create5m=0, create1h=70, output=25 |
| F5 | Anthropic 非流式 | 5m+1h 混合明细 | uncached=300, read=60, create5m=40, create1h=70, output=25 |
| F6 | Anthropic | 总量无明细 | create5m=110(总量), create1h=0 |
| F7 | Anthropic | 明细之和 > 总量 | 以明细为准 |
| F8 | 通用 | 负数 | 归零 + 指标 |
| F9 | Anthropic 流式 | message_start + message_delta 合并 | 同 F5 |
| F10 | OpenAI Responses | input_tokens_details.cached | 同 F1 语义 |

每条 fixture 同时被以下三个消费者读取，断言五桶一致：

1. `extractRawUsage` → `rawUsage`（Phase 1 扩展后含五桶）；
2. provider `convertFromAnthropicResponse` / `convertFromOpenAIResponse` → `Usage`；
3. billing `calculateCanonicalCost`（Phase 1 落地，Phase 0 先以桩函数占位）。

Phase 0 阶段 billing 消费者先用桩函数占位（返回与 §3 规范化结果一致的五桶），
目的是验证 fixture 的跨层一致性，不引入真实价格逻辑。

## 7. 不做（非目标）

- 不修改 proto（`api/log/v1`、`api/billing/v1`）、DO、PO、数据库迁移。
- 不改扣费行为；observe/charge 开关 Phase 1 落地。
- 不删除 `prompt_tokens` / `total_tokens` 兼容字段。
- 不引入新的微服务、消息中间件或计费数据库。
- 不依据模型名猜测 TTL 明细。

## 8. 验收

- `docs/design/token-usage-semantics.md`（本文件）存在且评审通过。
- `internal/server/token_usage_fixture_test.go` 表驱动 fixture 至少覆盖 F1–F10。
- 同一 fixture 经 raw relay、provider 转换和 billing（桩）后得到相同的五桶结果。
- `make test-unit` 与 `./scripts/check-architecture.sh` 通过；本阶段无 proto / DB 变更，不需要 `make api`。
- 评审通过前不进入 Phase 1（proto / DO/PO / 三数据库迁移）。

## 9. 后续 Phase 1 衔接

Phase 1 在本 ADR 固定的语义上落字段：

- `rawUsage` 增加 `CacheCreation5mTokens` / `CacheCreation1hTokens`；
  `extractRawUsageValue` 解析 `cache_creation_input_tokens` 与嵌套
  `cache_creation.ephemeral_5m/1h_input_tokens`，沿用 §3 / §4 规则。
- provider `Usage` / `UsageTokenDetails` 增加 `CacheCreation5mTokens` /
  `CacheCreation1hTokens`；`anthropic.go` 转换与流式合并按 §3.4 back-fill。
- `usageLogInput` 增加 `CacheCreation5mTokens` / `CacheCreation1hTokens`。
- `app/log` biz/data 表结构与聚合增加两列。
- `app/billing` `LedgerUsage` / `ModelPrice` / `calculateCanonicalCost` 按五桶实现。
- proto 扩展后 `make api` 重新生成，不手改生成文件。
