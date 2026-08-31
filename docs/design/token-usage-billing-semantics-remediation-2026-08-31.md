# Token Usage 语义与计费可审计性修复方案（2026-08-31）

> 状态：Phase 1 + Phase 2 implemented（2026-08-31）
>
> 日期：2026-08-31
>
> 关联 ADR：[`token-usage-semantics.md`](./token-usage-semantics.md)
>
> 范围：relay-gateway、provider / apicompat、billing-service、log-service、
> admin-api，以及三种数据库驱动的 additive migration。

> ## 实施状态（2026-08-31 第一阶段）
>
> 已落地：§4.1 数据模型（`internal/biz/usage.go` UsageEnvelope/ReportedUsage/
> CanonicalUsage/ParseStatus）；§4.2 解析层语义判定（`internal/server/usage/extract.go`
> DecideEnvelope,raw/SSE 共用同一 helper,`cached>prompt` 与协议字段冲突进入
> ambiguous 而非 max() 静默）;§4.3 provider canonical-first + inclusive 投影
> （`domain/upstream/provider/anthropic.go`,GLM 对外 total 现含 cache 桶，扣费不变）;
> §5.1 纯计费函数（`calculateCanonicalCost(price, CanonicalBuckets, multiplier)`,不含
> promptExclusive/协议判断）+ `BILLING_CANONICAL_USAGE_MODE=legacy|observe|charge`;
> §5.2 ambiguous 较低候选结算 + 隔离控制面（channel-service
> `usage_semantic_source_blocks` 持久化、selector 过滤、subscription 复用
> SetTempUnschedulable、人工恢复 RPC);§5.3 双写契约 + relay producer gate
> （`RELAY_CANONICAL_USAGE_PRODUCER`,默认关）;§6.1 migration 085/086/087
> （三方言 + ownership);§6.2 proto(billing envelope、log ingest/response、
> channel 控制面 RPC、common LedgerEntry);§9 指标与告警规则；§9.1
> log/identity API 与 Web(Usage/Dashboard 五桶、legacy 显示"历史口径"、
> billable total 含 cache creation)。
>
> 验证：`make api`、`make test-unit`、`make migration-check`、
> `./scripts/check-architecture.sh`、`go test -race ./internal/server/...
> ./domain/upstream/provider/... ./app/billing/...` 全部通过。
>
> 未做（后续阶段）:088 定价快照（§6.3 第二阶段）;§8 历史审计/冲正脚本；
> apicompat 内部改为复用统一投影 helper（当前行为已正确，仅代码复用层面的
> 防漂移）;Admin 用量详情页的完整五桶+价格快照展示（底层字段已全部透出）;灰度
> 发布本身（§11 的 observe 48 小时窗口与 charge allowlist 属于运维动作）。

> ## 实施状态（2026-08-31 第二阶段）
>
> 已落地：
>
> - **统一投影 helper（§4.3 防漂移）**：新增纯工具包 `pkg/usage`
>   （`Buckets` 五桶 + `ProjectOpenAI` inclusive 投影 + `SplitInclusive` 逆投影）。
>   provider Chat 非流式/流式 terminal、apicompat Anthropic→Responses 非流式/
>   流式 terminal、Responses→Anthropic 反向拆分全部收敛到同一实现；行为与
>   第一阶段完全一致（现有投影矩阵测试全数通过，另补 pkg/usage 单测含
>   GLM 生产样本数值）。
> - **§6.3 088 定价快照**：migration `088_create_billing_pricing_snapshots`
>   （三方言 + ownership;`billing_pricing_snapshots` 表 `config_hash` 唯一 +
>   `billing_ledgers.pricing_config_hash`,ledger 列刻意不加索引——分区大表、
>   仅按行审计查询）。billing 在 `resolveUserCost` 冻结 EFFECTIVE 单价
>   （cache-read 的 InputPrice 兜底、未定价 creation 桶记 0）、group ratio 与
>   cache-creation mode 的 sha256；快照在 `commitQuotaDualTrack` 与 ledger 行
>   **同一事务**内 claim（同 hash 复用，镜像 078 dedupe claim 模式），legacy
>   路径用独立事务 claim。ratio 计价模型无五桶单价可冻结，hash 保持空（§8.2
>   口径，不伪造证据）。proto：`common.v1.LedgerEntry.pricing_config_hash`(46)
>   + `PricingSnapshot`(47,详情专用)+ list/detail 映射。
> - **§9 Admin 用量详情页**：`app/admin` `GetLedgerEntry`/`ListLedgerEntries`
>   透传 per-bucket 成本、reported/billable totals、semantics/protocol/
>   field_shape/parse_status/decision_reason、候选成本与 pricing hash；detail
>   额外内嵌快照单价。Web `components/admin/UsageAuditPanel.tsx`：五桶×
>   （token/快照单价/成本）表、计费总 Token（含全部缓存桶）、上游上报对照、
>   ambiguous 双候选说明、定价快照 hash/倍率/mode；legacy 行显示"历史口径"
>   警示且不伪造 uncached；列表新增 Token 用量摘要列（历史/存疑徽标）。
>
> 关于跳过 observe 验证直接实现 088 的决策：088 是纯增量审计能力（新表 +
> 新列 + 同事务 claim），不改变任何计费金额、不依赖 `BILLING_CANONICAL_-
> USAGE_MODE` 的档位——legacy/observe/charge 三种模式下快照都只是额外记录
> 证据，反而让 observe 窗口本身可审计。原方案"charge 稳定后再上 088"的排序
> 是优先级判断而非安全约束，故提前落地；风险仅为每笔 consume 多一次按唯一键
> 的 INSERT（冲突即复用）。
>
> 验证：`make api`、`make test-unit`、`make migration-check`、
> `./scripts/check-architecture.sh`、`go test -race ./internal/server/...
> ./domain/upstream/provider/... ./app/billing/... ./pkg/usage/...`、
> sqlite lifecycle（count 28 + 088 表/列断言）、web `tsc -b`/`eslint`/
> `vitest run`（153 tests）全部通过。
>
> 仍未做：§8 历史审计/冲正脚本；灰度发布运维动作（§11 的 observe 48 小时
> 窗口、charge allowlist、`usage_contract_version=0` 流量归零后移除
> PromptExclusive 依赖）。

## 1. 结论摘要

本次生产核查没有发现模型单价被临时放大、重复 ledger 或 cache-creation 误扣：

- Kimi K3 当前按 OpenAI 子集语义结算，账本逐桶金额与配置价格一致；
- GLM-5.3 经 Z.AI Anthropic Messages 兼容端点执行，`input_tokens` 与
  `cache_read_input_tokens` 是互斥桶，当前逐桶扣费同样与配置价格一致；
- GLM 的对外 `total_tokens` 在 Anthropic → OpenAI 转换后没有包含 cache-read，
  导致日志 / UI 显示总 token 明显小于实际计费五桶之和，容易被误判为多扣；
- Kimi、GLM、StepFun 等历史记录中都出现过 `cache_read_tokens > prompt_tokens`。
  其中 StepFun 样本来自普通 OpenAI 类型 channel；这只能证明生产中已出现与
  exclusive 或异常协议转换一致的形态，不能仅凭 ledger 反推原始上游协议；
- Kimi 历史记录中出现过同一公开模型、同一渠道下两种不同的账本数值关系。
  现有 ledger 没有保存原始协议与字段来源，因此不能仅凭
  `total_tokens == prompt + output` 或 `total_tokens == prompt + cache + output`
  自动判断历史行语义，更不能据此直接退款或追扣。

真正需要修复的是：系统虽宣称使用“规范五桶”，但内部仍传递含义不稳定的
`PromptTokens + PromptExclusive bool`，并在较晚阶段依据渠道 / 账号类型猜测
prompt 与 cache 的关系。协议转换后，展示字段、计费字段和语义来源可能不再一致。

本方案固定以下目标：

1. **协议解析层即完成语义判定**，verified/estimated 请求产出五个互斥计费桶，
   ambiguous 请求保留 reported usage 与候选，不伪造 canonical；
2. **计费层只消费互斥桶**，不再做 `prompt - cache` 或读取
   `prompt_exclusive` 决策；
3. **原始上报值与规范计费值分开存储**，确保日志、账本和 UI 可解释；
4. **语义来源随最终执行 attempt 传递**，不能从最初 plan、模型名或渠道类型猜；
5. **历史修正只基于可证明证据**，原账本保持 append-only，修正使用幂等冲正。

## 2. 生产证据与问题边界

生产账本的权威 schema 是 `oneapi_billing.billing_ledgers`；
`oneapi.billing_ledgers` 是空表/镜像侧表，不是本次核查依据。`consume` ledger 的
`amount` 为负数，逐桶成本核对必须比较：

```text
abs(prompt_cost + completion_cost + cache_read_cost
    + cache_creation_5m_cost + cache_creation_1h_cost) == abs(amount)
```

### 2.1 核查范围与线上环境

本方案的生产判断来自 2026-08-31 的只读核查，证据范围包括：

1. relay usage 提取、orchestrator/final attempt、provider/apicompat 投影、billing
   计价与 ledger repo、log ingest，以及 Web 用量展示代码；
2. relay-gateway、billing-service、log-service、channel-service 运行状态；
3. relay-gateway 近 48 小时 usage / warning 日志；
4. 生产 MySQL 权威账本和各 service schema 的 migration 状态；
5. Prometheus usage anomaly / shadow cost 指标；
6. 当前 Web Dashboard / Usage 页面 token 推导逻辑。

核查时相关容器均在运行，关键配置为：

```text
BILLING_SCHEMA=oneapi_billing
BILLING_CACHE_CREATION_MODE=charge
BILLING_ASYNC_ENABLED=true
```

重点代码证据包括：

- `internal/biz/usage.go`
- `internal/server/usage/extract.go`
- `internal/server/http_raw_helpers.go`
- `internal/server/http_usage_log.go`
- `internal/server/http_billing.go`
- `internal/server/orchestrator.go`
- `internal/server/executor_stream.go`
- `domain/upstream/provider/anthropic.go`
- `internal/apicompat/anthropic_to_responses_response.go`
- `internal/apicompat/responses_to_chatcompletions.go`
- `app/billing/internal/biz/billing.go`

### 2.2 逐日账本核对

在 `oneapi_billing.billing_ledgers` 中核查 2026-08-29 以来的 consume 行，结果为：

| 日期 | 模型 | n | cache>prompt | 逐桶成本合计匹配 | reference 唯一 | dedupe key 唯一 |
|------|------|---:|-------------:|-----------------:|---------------:|----------------:|
| 2026-08-30 | `kimi-k3` | 571 | 0 | 571/571 | 571/571 | 571/571 |
| 2026-08-30 | `glm-5.3` | 28 | 0 | 28/28 | 28/28 | 28/28 |
| 2026-08-31 | `glm-5.3` | 158 | 45 | 158/158 | 158/158 | 158/158 |

抽样 GLM consume 行：

```text
prompt=130, cache_read=45056, output=9
prompt_cost=2, cache_read_cost=117, completion_cost=0
amount=-119
```

逐桶成本、`amount`、reference 和 dedupe 结果共同支持以下边界：未发现模型单价临时
放大、重复 ledger 或 cache-creation 误扣；GLM 样本与 Anthropic exclusive 语义一致，
不能从 `input_tokens` 中再次扣减 cache-read。

### 2.3 Kimi K3

2026-08-30（北京时间）生产账本中，571 笔 Kimi K3 请求均满足当前 OpenAI
子集形态：上游 `prompt_tokens` 包含 `cached_tokens`，用户成本按以下公式计算：

```text
uncached = max(prompt_tokens - cached_tokens, 0)
cost = uncached * input_price
     + cached_tokens * cache_read_price
     + output_tokens * output_price
```

逐桶成本之和与 ledger amount 一致，无重复 reference / dedupe key。费用上升主要由
请求量、上下文长度和非缓存输入增加造成。

历史上曾出现 `cache_read_tokens > prompt_tokens`，且 reported total 更接近五桶之和
的记录。这些行是**语义异常候选**，但 ledger 未保存原始字段名、响应协议和最终
adapter，不能排除上游代理已做过协议转换。它们只能进入人工 / 供应商账单对账，
不能通过 SQL 关系直接定义为少扣或多扣。

### 2.4 GLM-5.3

GLM-5.3 由 `subscription_accounts.platform=zhipu` 执行，生产接入使用 Z.AI
Anthropic Messages 兼容端点。按 Anthropic 语义：

```text
uncached_input_tokens     = input_tokens
cache_read_tokens         = cache_read_input_tokens
cache_creation_*_tokens   = cache_creation_*_input_tokens
output_tokens             = output_tokens
```

这些桶互斥，计费时不得从 `input_tokens` 再减 cache-read。Anthropic 官方也明确要求
把普通输入、cache write、cache read 与输出分别相加计算请求成本。

当前 [`domain/upstream/provider/anthropic.go`](../../domain/upstream/provider/anthropic.go)
在转换成 OpenAI `Usage` 时使用：

```text
PromptTokens = Anthropic input_tokens            // 仍为互斥语义
CachedTokens = Anthropic cache_read_input_tokens
TotalTokens  = input_tokens + output_tokens       // 未包含 cache 桶
```

计费链路通过 `PromptExclusive=true` 保留了正确收费语义，但对外 `total_tokens` 和
用量日志少展示 cache-read / cache-creation。结果是“扣费正确、展示总量错误”。

### 2.5 为什么不能通过 total 自动推断语义

`total_tokens` 只适合作为异常检测信号，不能作为计费决策依据：

- 原生 OpenAI：通常 `total = prompt(inclusive) + output`；
- 原生 Anthropic：计费总量需把 input、cache read、cache creation、output 相加；
- 当前 Anthropic → OpenAI adapter：仍保留 exclusive prompt，却把
  `total` 写成 `input + output`，数值关系看起来像 OpenAI subset；
- 第三方兼容网关可能重命名字段、合并桶或省略 total；
- 流式响应可能在 message_start / message_delta / terminal event 分段上报 usage。

因此禁止实现以下启发式计费：

```text
if total == prompt + output:
    assume OpenAI subset
else if total == prompt + cache + output:
    assume Anthropic exclusive
```

该关系只能触发 anomaly 指标，不能改变用户余额。

### 2.6 普通 OpenAI channel 的异常候选

完整生产账本分布如下：

| 模型 | 总数 | cache>prompt |
|------|-----:|-------------:|
| `step-explore` | 1542 | 1150 |
| `glm-5.3` | 4670 | 640 |
| `k3` | 4623 | 1457 |
| `kimi-k3` | 2239 | 356 |
| `deepseek-v4-pro-0813` | 985 | 221 |
| `glm-5.3-flash` | 147 | 137 |

其中 `step-explore` 来自普通 channel 9，channel type 为 OpenAI 兼容类型，不在当前
`IsPromptExclusiveChannelType` 的 Anthropic-like 列表内；2026-08-30 有 23 笔、
2026-08-31 有 3 笔 `cache>prompt`。

这证明“根据 channel type 决定 prompt/cache 关系”在生产中不可靠，但 ledger 只记录
转换后的 bucket，不能证明这些请求原始响应一定是 Anthropic 协议。历史审计仍将其
归为 candidate，只有原始 usage、不可变审计事件或供应商账单才能升级为 verified。

### 2.7 线上 relay 日志与指标基线

relay 日志中的 GLM usage 样本：

```text
model=glm-5.3
total_tokens=55880
upstream_input_tokens=55619
input_tokens=55619
output_tokens=261
cache_read_tokens=7232
```

以及：

```text
model=glm-5.3
total_tokens=56032
upstream_input_tokens=55828
cache_read_tokens=55616
```

这些样本中 `total_tokens` 等于 input + output，却没有包含 cache-read，导致展示总量
明显小于五桶 billable total；形态与 provider Chat 及其流式 terminal usage 当前实现
一致。

核查时以下现有 Prometheus 指标均无样本：

- `micro_one_api_relay_token_usage_parse_anomaly_total`
- `micro_one_api_relay_token_usage_shadow_cost_sum`

结合当前 parser 只覆盖负数、TTL detail 等有限 reason，这组指标不能检测本次
`cache>prompt`、reported total mismatch 或语义来源冲突。§9 的 semantics / invariant
指标是本次修复的上线前置条件，不能把“当前无指标样本”解释为生产没有异常。

## 3. 根因

### 3.1 `CanonicalUsage` 并未真正 canonical

[`internal/biz/usage.go`](../../internal/biz/usage.go) 当前结构仍包含：

```go
PromptTokens    int64
CacheReadTokens int64
PromptExclusive bool
```

`PromptTokens` 根据路径可能表示：

- OpenAI inclusive prompt；
- Anthropic uncached input；
- 协议转换后的兼容字段；
- fallback 估算值。

计费层直到 [`calculateCanonicalCost`](../../app/billing/internal/biz/billing.go)
才根据布尔值决定是否相减，语义固定得太晚。

### 3.2 语义由路由身份推断，而非解析器证明

`IsPromptExclusiveChannel(plan)` / `IsPromptExclusiveChannelType(type)` 根据
channel type 或 subscription account platform 返回布尔值。这对原生协议通常成立，
但无法覆盖：

- 普通 OpenAI channel 背后又代理 Anthropic 响应；
- Anthropic provider 已转换为 OpenAI 响应；
- retry / failover 后最终 attempt 的协议与初始 plan 不同；
- 同一第三方 upstream 升级后改变 usage 字段形态。

### 3.3 展示 DTO 与计费 DO 混用

provider 为了返回 OpenAI 兼容响应而构造 `Usage`，relay 又从这个兼容响应反向解析
计费数据。协议投影字段与财务规范桶共用同一结构，使一次转换同时影响客户端展示、
日志和扣费。

### 3.4 历史账本缺少语义与价格版本证据

迁移 080 后 ledger 已保存五桶 token、per-bucket cost、`source_kind`、
`upstream_model_id` 和 `cost_audit_status`。但这些字段仍不足以还原 usage 语义，当前
部分普通 channel 请求的 source/upstream model 覆盖也不完整。ledger 仍未保存：

```text
GLM 样本：
source_kind=subscription
upstream_model_id=glm-5.3
cost_audit_status=priced
```

这说明执行来源并非完全缺失；真正缺少的是原始协议和字段语义。同时，channel 1 的
Kimi 样本中 `source_kind/upstream_model_id` 仍为空，必须作为 producer 覆盖缺口纳入
本次字段完整率指标和测试，而不能依赖 migration 080 的默认值。

- 原始响应协议 / usage 字段来源；
- 本次请求使用的 usage semantics；
- reported prompt / total 与 billable total 的区别；
- 定价配置版本或 hash。

动态 `system_options.ModelPrice` 会覆盖旧值，导致历史复算只能从 bucket cost 反推，
无法形成完整、不可抵赖的定价快照。

## 4. 目标数据模型

### 4.1 reported、canonical 与解析状态分层

`CanonicalUsage` 只表达已经确定的五个互斥桶。原始上报值、协议来源和解析状态放在
外层 envelope；`estimated` / `ambiguous` 是可信度状态，不是 bucket semantics：

```go
type UsageSemantics string

const (
    UsageSemanticsOpenAISubset       UsageSemantics = "openai_subset"
    UsageSemanticsAnthropicExclusive UsageSemantics = "anthropic_exclusive"
)

type UsageParseStatus string

const (
    UsageParseVerified  UsageParseStatus = "verified"
    UsageParseEstimated UsageParseStatus = "estimated"
    UsageParseAmbiguous UsageParseStatus = "ambiguous"
    UsageParseLegacy    UsageParseStatus = "legacy"
)

type ReportedUsage struct {
    PromptTokens          int64
    OutputTokens          int64
    CacheReadTokens       int64
    CacheCreation5mTokens int64
    CacheCreation1hTokens int64
    TotalTokens           int64
    SourceProtocol        string
    FieldShape            string
}

type CanonicalUsage struct {
    UncachedInputTokens   int64
    CacheReadTokens       int64
    CacheCreation5mTokens int64
    CacheCreation1hTokens int64
    OutputTokens          int64
}

func (u CanonicalUsage) BillableTotal() int64 {
    return u.UncachedInputTokens + u.CacheReadTokens +
        u.CacheCreation5mTokens + u.CacheCreation1hTokens + u.OutputTokens
}

type UsageEnvelope struct {
    ContractVersion   int32
    Reported          ReportedUsage
    Canonical         *CanonicalUsage
    Semantics         UsageSemantics
    ParseStatus       UsageParseStatus
    SubsetCandidate   *CanonicalUsage
    ExclusiveCandidate *CanonicalUsage
}
```

约束如下：

- `verified`：必须有且只有一个 `Canonical`；有 cache 时必须有明确 semantics；
- `estimated`：允许 estimator 生成无 cache 的 canonical bucket，但不得伪造 cache；
- `ambiguous`：不得伪造单一 `Canonical`，只保留 reported usage 和两个候选；
- `legacy`：仅表示旧 producer 没有发送新契约，不能用于新 producer 的解析失败；
- `PromptExclusive` 进入 deprecated 兼容期，新计费路径不得读取它。

### 4.2 解析与 invariant 判定顺序

parser 必须先识别字段来源，再校验 invariant，最后才规范化。不能先通过
`max(prompt-cache, 0)` 消除冲突后仍把结果标记为 verified：

| 输入来源 / 条件 | 状态 | 语义与规范化 |
|-----------------|------|----------------|
| OpenAI Chat 且 `cached <= prompt` | verified | subset；`uncached=prompt-cached` |
| Responses 且 `cached <= input` | verified | subset；`uncached=input-cached` |
| Anthropic `cache_read_input_tokens` | verified | exclusive；`uncached=input_tokens` |
| Anthropic cache creation 字段 | verified | exclusive；分别进入 5m / 1h 桶 |
| OpenAI/Responses 中 `cached > prompt/input` | ambiguous | 不产出单一 canonical；构造 subset/exclusive 候选 |
| 同一 payload 同时出现互相冲突的协议字段 | ambiguous | 保存 field shape，进入保守结算与隔离 |
| 无 usage，仅有本地 estimator | estimated | 只写 estimator 能证明的 bucket，不伪造 cache |
| 新契约声明 canonical v1 但 canonical 缺失 | ambiguous | 视为 producer/序列化错误，禁止 legacy fallback |

reported total 只用于展示和 invariant 检测，不参与语义决策。模型名、channel type、
subscription platform 仅用于 parser 选择 / preflight，不能覆盖 parser 已确认的结果。

### 4.3 对外协议投影矩阵

内部 canonical usage 与返回给客户端的兼容 usage 分离。OpenAI 投影统一使用：

```text
prompt/input_tokens = uncached + cache_read + cache_creation_5m + cache_creation_1h
cached_tokens       = cache_read
total_tokens        = prompt/input_tokens + output_tokens
```

| 输入 / 代码路径 | 当前状态 | 目标行为 |
|-----------------|----------|----------|
| Anthropic → provider Chat 非流式 | total 漏 cache | 使用 canonical 后投影 inclusive prompt/total |
| Anthropic → provider Chat 流式 | terminal usage total 漏 cache | message_start/delta 合并后使用相同投影 |
| Anthropic → apicompat Responses 非流式 | 已正确 inclusive | 保持行为，改为复用统一 canonical/projection helper |
| Anthropic → apicompat Responses 流式 | 已正确 inclusive | 保持行为，验证 terminal usage 与非流式一致 |
| Responses ↔ Chat bridge | 存在重复转换入口 | 保留 inclusive 语义和 cache detail，不二次加减 |
| 对外 Anthropic Messages | exclusive | 保持 input/cache-read/cache-creation 互斥字段 |

其中 `AnthropicToResponsesResponse` 当前已经执行
`totalInput=input+cacheRead+cacheCreation`、`total=totalInput+output`；它不是本次
total 漏 cache 的错误点。对该文件的改动只用于复用统一 helper 和防止后续路径漂移，
不能改变现有正确投影。

日志同时写 `reported_total_tokens` 与 `billable_total_tokens`，不得再用一个
`total_tokens` 同时承担协议兼容和财务总量。修复 provider Chat 及流式路径后，GLM
日志 / UI 的 OpenAI total 将包含 cache 桶，但用户扣费金额不应变化。

## 5. 计费层改造

### 5.1 单一纯函数

将计费函数收敛为：

```go
func calculateCanonicalCost(
    price ModelPrice,
    usage CanonicalUsage,
    multiplier float64,
) canonicalCostBreakdown
```

公式固定为：

```text
input_cost     = round(uncached_input_tokens * input_price * multiplier * AmountScale)
cache_cost     = round(cache_read_tokens * cache_read_price * multiplier * AmountScale)
create_5m_cost = round(cache_creation_5m_tokens * create_5m_price * multiplier * AmountScale)
create_1h_cost = round(cache_creation_1h_tokens * create_1h_price * multiplier * AmountScale)
output_cost    = round(output_tokens * output_price * multiplier * AmountScale)
total_cost     = sum(each bucket cost)
```

计费函数内不再出现：

- `promptExclusive` 参数；
- `prompt - cache`；
- channel / provider / model 语义判断；
- reported total 参与成本计算。

用户售价、上游成本、订阅吸收成本和 shadow cost 必须复用同一个 canonical usage。

### 5.2 ambiguous 的保守策略

当 `ParseStatus=ambiguous` 且存在 cache token 时，请求已经完成，不能事后拒绝响应，
也不能选择更高候选价静默扣款。normalizer 先基于 reported usage 构造 subset 和
exclusive 两个 `CanonicalUsage` 候选，再分别调用同一个纯计费函数：

当前 `SetTempUnschedulable` 主要面向 subscription account，普通 channel health /
circuit breaker 主要消费 upstream HTTP 结果；usage semantic error 则发生在成功响应
解析之后。因此不能直接复用现有 transport failure 调用链，必须新增独立控制面：

1. 用户侧按两个候选成本中的较低值结算；该安全规则独立于 observe/charge 模式；
2. ledger 标记 `usage_parse_status=ambiguous`，保存两个候选成本和决策原因；
3. 第一笔立即触发高优先级告警，但不因单个异常停掉整个多模型 channel；
4. 以 `(source_kind, source_id, upstream_model_id, adapter/protocol)` 为隔离键；默认
   5 分钟内连续 3 笔 ambiguous 时暂停该键 15 分钟，verified 结果重置连续计数，
   阈值必须可配置；
5. channel 与 subscription account 都按 source+upstream model 读取独立的
   usage-semantic block；不能复用会封禁整个 subscription account 的
   `SetTempUnschedulable`，也不能把成功 HTTP 结果伪装成 transport failure；
6. 恢复需要人工确认 adapter 已修复，或隔离期后连续两次探测得到 verified usage；
7. 未取得供应商原始 usage / 账单证据前，不自动追扣用户。

该策略优先避免不可逆的用户多扣。上游成本按供应商已经确认的协议或账单记录，
不能直接复用 ambiguous 用户侧的较低候选成本。

### 5.3 滚动升级兼容

`CommitQuotaRequest.prompt_exclusive` 和旧 token 字段暂时保留，并增加明确的
`usage_contract_version`。旧 producer 不发送该字段，proto 默认值 0 表示 legacy；
新 producer 必须发送 canonical v1：

| producer 输入 | 新 billing 行为 |
|---------------|------------------|
| `version=0` | 仅作为真实旧 producer，回退旧字段并记录 `legacy_producer` |
| `version=v1,status=verified/estimated,canonical!=nil` | 只消费 canonical buckets |
| `version=v1,status=ambiguous` | 执行 §5.2，不读取 `PromptExclusive` 决策 |
| `version=v1` 但 canonical/status 缺失或非法 | 标记 producer contract error，按 ambiguous 处理并告警 |

混跑期必须满足以下双写契约：

- 新 relay 继续按旧版本原有含义写 `prompt_tokens`、`cache_read_tokens` 和
  `prompt_exclusive`，同时 additive 写 canonical v1；
- 旧 billing 忽略新字段后仍得到与升级前一致的输入，禁止把 legacy
  `prompt_tokens` 提前改成 uncached 后让旧 billing 再减一次 cache；
- relay 的 canonical v1 producer 由 feature gate 控制，确认所有 billing 实例升级后
  才启用；滚动部署期间不得假设同一服务的所有 pod 已同时升级；
- 新 producer 声明 v1 后字段缺失不得回退 legacy，否则会绕过 ambiguous 策略；
- 至少跨两个发布版本并确认没有 `version=0` 流量后再删除旧分支。

部署顺序：migration → billing-service / log-service 全部消费者 → 验证消费者版本 →
relay-gateway producer feature gate → admin-api / web 展示。

## 6. 持久化与 API 变更

### 6.1 Additive migration

生产按 service schema 运行 ownership-filtered migration，核查时状态为：

| schema | 最新 migration |
|--------|------------------|
| `oneapi_channel` | `084` |
| `oneapi_billing` | `080` |
| `oneapi_log` | `082` |

这是 `migrations/ownership.yaml` 的预期结果，不是 billing/log 漏升 081–084。实现时
使用全局下一可用编号，并为每个 owner 拆分 migration；以下编号为当前主线预留，
若落地时已占用则整体顺延：

| migration | owner | 变更 |
|-----------|-------|------|
| `085_add_billing_usage_semantics` | billing | `billing_ledgers` canonical/reported/status 字段 |
| `086_add_log_usage_semantics` | log | `logs` canonical/reported/status 字段 |
| `087_create_usage_semantic_source_blocks` | channel | source+model 隔离窗口、计数与恢复状态 |
| `088_create_billing_pricing_snapshots` | billing | 第二阶段价格快照表及 ledger hash |

每个 migration 都必须同步 MySQL root、`migrations/postgres/`、`migrations/sqlite/`，
并更新 `migrations/ownership.yaml` 和 `migrations/dialect-manifest.yaml`。billing、log、
channel 分别只应用其 owner 条目，不能把 085/086 usage 列迁入 channel schema。

`billing_ledgers`：

- `uncached_input_tokens BIGINT NOT NULL DEFAULT 0`
- `reported_prompt_tokens BIGINT NOT NULL DEFAULT 0`
- `reported_total_tokens BIGINT NOT NULL DEFAULT 0`
- `billable_total_tokens BIGINT NOT NULL DEFAULT 0`
- `usage_semantics VARCHAR(32) NOT NULL DEFAULT ''`
- `usage_protocol VARCHAR(32) NOT NULL DEFAULT ''`
- `usage_field_shape VARCHAR(64) NOT NULL DEFAULT ''`
- `usage_parse_status VARCHAR(16) NOT NULL DEFAULT 'legacy'`
- `usage_contract_version INT NOT NULL DEFAULT 0`
- `canonical_present BOOLEAN NOT NULL DEFAULT FALSE`
- `usage_decision_reason VARCHAR(64) NOT NULL DEFAULT ''`
- `subset_candidate_cost BIGINT NOT NULL DEFAULT 0`
- `exclusive_candidate_cost BIGINT NOT NULL DEFAULT 0`

`logs` 通过独立的 086 增加同名 usage 字段，但不保存候选成本和价格 hash。

`usage_semantic_source_blocks` 由 087 在 channel schema 创建，至少包含：

- `source_kind`、`source_id`、`upstream_model_id`、`adapter_protocol` 组成唯一隔离键；
- `window_started_at`、`consecutive_ambiguous`、`blocked_until`；
- `reason`、`status`、`last_verified_at`、`updated_at`；
- `(status, blocked_until)` 调度查询索引。

selector 可缓存状态，但数据库是跨实例和重启后的权威来源；恢复操作必须清除持久化
block 并广播缓存失效。

存量行保持 `usage_semantics=''`、`usage_parse_status='legacy'`；**禁止**根据 token
数值关系猜测并回填 subset / exclusive。UI 可把这一组合显示为 `legacy_unknown`，
但不能把 legacy 混入 semantics 枚举。部署时分别检查 `oneapi_billing.schema_migrations` 和
`oneapi_log.schema_migrations`，不能用共享 schema 的状态代替。

### 6.2 Proto

对 `api/billing/v1/billing.proto` 的 `CommitQuotaRequest` 和 ledger DTO、
`api/log/v1/log.proto` 的 ingest / response DTO 增加相应字段。wire DTO 优先使用带
presence 的嵌套 `UsageEnvelope` message，并包含 `usage_contract_version`；不要只靠
proto3 scalar 的零值判断 canonical 是否存在。旧字段按 §5.3 保持双写。

`api/channel/v1/channel.proto` 增加 usage-semantic unsafe/verified 事件与人工恢复 RPC，
由 channel-service 持久化隔离状态。该事件是 usage 控制面信号，不复用 HTTP health
failure RPC。

只改 `.proto` 后执行 `make api`，不得手改生成文件。

### 6.3 定价快照（第二阶段）

完整定价快照提高长期复算能力，但不作为修复 usage 语义和错误展示的首轮上线
阻断项。第一阶段继续以 ledger 已保存的 per-bucket token/cost、source 和 upstream
model 形成计费闭环；完成 canonical charge 稳定验证后再上线 088。

`pricing_config_hash` 由本次实际使用的标准化 ModelPrice、group ratio、模型规范 ID
和 cache-creation mode 计算。hash 对应的完整快照写入独立、去重的
`billing_pricing_snapshots` 表，避免给 ledger 增加多列 decimal：

- `config_hash` 唯一；
- `model_name`、各桶单价、group ratio、mode；
- `created_at`。

`config_hash` 建唯一索引。ledger 写入与 snapshot claim 必须在同一事务；同 hash
复用既有 snapshot。这样历史复算不依赖已被覆盖的 `system_options`。088 同时给
`billing_ledgers` 增加 `pricing_config_hash`；在 088 上线前该字段不进入第一阶段契约。

## 7. Relay / Provider 实施位置

| 位置 | 修改 |
|------|------|
| `internal/biz/usage.go` | 分离 ReportedUsage、CanonicalUsage、Semantics、ParseStatus 与 envelope |
| `internal/server/usage/extract.go` | 先识别 field shape 和 invariant，再规范化；不接收 route-derived bool |
| `internal/server/http_raw_helpers.go` | raw / SSE 与通用 parser 使用同一 normalization helper |
| `domain/upstream/provider/anthropic.go` | 先生成 canonical usage，再独立投影 OpenAI response；修正 total |
| `internal/apicompat/anthropic_to_responses_response.go` | 当前 inclusive 行为保持不变，改为复用 helper 并补齐 bridge/stream 测试 |
| `internal/server/orchestrator.go` | 使用最终 attempt 返回的 semantics，禁止用初始 plan 覆盖 |
| `internal/server/http_usage_log.go` | 同时记录 reported / billable total 与 semantics |
| `internal/server/http_billing.go` | 传递 uncached bucket，不再只传 PromptExclusive |
| `internal/data` / channel client | 上报 usage-semantic unsafe/verified 事件和执行人工恢复 |
| `app/channel/internal/biz` / `data` / `service` | 持久化按 source+model 的 unsafe 事件、隔离与恢复，并使 selector 过滤 blocked key |

retry / failover 必须由最终成功 attempt 返回 canonical usage。`orchestrator` 不得在
finalize 阶段再次用 `IsPromptExclusiveChannel(finalPlan)` 覆盖 parser 已证明的语义。

## 8. 历史审计与补偿

### 8.1 禁止直接修改原账本

`billing_ledgers` 是 append-only 财务审计轨迹。任何修正都不得 UPDATE / DELETE
原 consume 行。

### 8.2 历史行分级

| 级别 | 证据 | 动作 |
|------|------|------|
| verified | 原始上游 usage、供应商账单或不可变审计事件可证明语义 | 可计算差额并冲正 |
| candidate | 只有 token 数值关系、渠道类型或当前配置 | 只进入报告，不动余额 |
| unknown | usage 缺失、协议转换路径无法还原 | 保留原账，不自动处理 |

现有 Kimi / GLM / StepFun 历史 ledger 大多只能归入 candidate：GLM 的 adapter 已证明
`total=input+output` 也可能对应 exclusive billing；StepFun 的 channel type 与数值关系
冲突也不能反向证明原始协议，故不能把所有类似行批量退款。

### 8.3 幂等冲正

对 verified 的用户多扣行：

- 新增正向 `refund` / `reversal` ledger，`reference_id` 指向原 ledger；
- dedupe key 固定为 `usage-semantics-reversal:<ledger_id>:v1`；
- 钱包支付部分退回钱包；
- 订阅吸收部分按原 `subscription_id` 和窗口语义冲正，不得一律退钱包；
- 差额、证据来源、操作者、计算版本写入 remark / audit 表；
- 原请求的 upstream cost 不随用户售价冲正，除非供应商账单也被更正。

对 verified 的历史少扣行默认不追扣用户；如业务决定追扣，必须单独审批并走
receivable 流程，不能复用 refund。

## 9. 可观测性与告警

新增指标：

```text
token_usage_semantics_total{protocol,semantics,source_kind}
token_usage_invariant_mismatch_total{reason,protocol,source_kind}
billing_usage_semantics_cost_delta{mode,model,source_kind}
billing_usage_ambiguous_total{model,source_kind}
usage_semantic_source_isolation_total{source_kind,reason}
```

异常 reason 至少包括：

- `cached_exceeds_reported_prompt`
- `reported_total_mismatch`
- `protocol_field_conflict`
- `final_attempt_semantics_missing`
- `negative_bucket`
- `overflow`
- `stream_usage_missing`

Admin 用量详情同时显示：

- 上游 reported prompt / total；
- 五个规范计费桶；
- billable total；
- semantics / protocol / parse status；
- 各桶单价、成本与 pricing snapshot hash。

禁止只展示 `total_tokens` 后再用账本金额反推单价。

### 9.1 日志、API 与 Web 展示契约

当前 Usage 页面仍使用旧推导：

```text
nonCachedInputTokens = max(prompt_tokens - cache_read_tokens, 0)
displayTotal          = quota || nonCachedInput + output + cacheRead
```

Dashboard 也从旧字段聚合 total，且未计入 cache creation。迁移后如果只加 DB/proto
字段而不修改 log service、API 和 Web，旧展示仍会继续制造“计费变贵”的错觉。

- `logs.quota` 在兼容期保持旧的 reported total 含义，不改成财务总量；
- log/admin API 新增显式 `reported_total_tokens` 和 `billable_total_tokens`，新 Web
  优先使用后者，禁止继续用 `quota || prompt-cache+output+cache` 推导；
- Dashboard 与用量详情都必须展示五桶，total 必须包含 cache creation；
- `usage_parse_status=legacy` 的存量行显示“历史口径未知”，可以展示原始字段，但不得
  假装其为 subset/exclusive，也不得计算伪精确的 uncached input；
- `ambiguous` 行同时展示 reported usage、实际采用的较低候选成本和隔离状态；
- reported、billable、wallet amount 三个概念分别命名，不再都显示为“用量/额度”。

## 10. 测试方案

扩展现有 [`internal/server/token_usage_fixture_test.go`](../../internal/server/token_usage_fixture_test.go)
跨层 fixture：

| ID | 场景 | 关键断言 |
|----|------|----------|
| F11 | OpenAI cached subset | uncached=prompt-cached；billing 无二次相减 |
| F12 | Anthropic exclusive | uncached=input；billable total 包含所有 cache 桶 |
| F13 | Anthropic → OpenAI 非流式 | 内部桶不变；对外 prompt / total 转为 inclusive |
| F14 | Anthropic → OpenAI 流式 | message_start + delta 合并与非流式一致 |
| F15 | 同一 channel 前后出现两种原始协议 | semantics 来自 parser，不来自 channel type |
| F16 | retry 后切换 execution source | 使用最终成功 attempt 的 semantics |
| F17 | total 关系伪装成 subset | 不改变已由 Anthropic parser 确认的 exclusive |
| F18 | ambiguous + cache | 无单一 canonical；两个候选调用同一计费函数；用户取低值 |
| F19 | fallback estimate | status=estimated，不伪造 cached bucket |
| F20 | 旧 producer → 新 billing | version=0 才允许 legacy fallback，并记录原因 |
| F21 | 新 producer canonical 缺失 | v1 contract error 进入 ambiguous，不回退 PromptExclusive |
| F22 | StepFun/OpenAI 形态 cache>prompt | invariant 先失败，不被 max() 静默归一化为 subset |
| F23 | 新 relay → 旧 billing | legacy 字段含义不变，不发生 cache 二次相减 |
| F24 | Responses/Chat/stream 投影矩阵 | 当前正确路径不回归，所有 OpenAI total 都 inclusive |
| F25 | Web legacy/ambiguous/canonical | 不伪造 legacy uncached；billable total 包含 cache creation |
| F26 | source+model 隔离 | 达阈值后持久化 block；不污染 transport health；重启后仍生效 |

计费测试必须直接断言每个 bucket cost 和整数舍入，不能只断言总价大于 0。

端到端测试至少覆盖：

1. OpenAI channel 的 Chat Completions / Responses；
2. Z.AI / Kimi / MiniMax subscription account 的 Anthropic Messages；
3. Anthropic provider 转 OpenAI 响应；
4. 流式与非流式同 fixture 同价；
5. 普通 OpenAI channel 返回 cache>prompt 的异常 fixture；
6. subscription / balance 双轨 ledger 和幂等重试；
7. 新旧 relay/billing 镜像的双向兼容组合。

验证命令：

```bash
make api
make test-unit
go test -race ./internal/server/... ./domain/upstream/provider/... ./app/billing/...
go test ./platform/database/migrate/...
make migration-check
./scripts/check-architecture.sh
```

## 11. 灰度、发布与回滚

新增 `BILLING_CANONICAL_USAGE_MODE=legacy|observe|charge`：

1. **legacy**：只用于紧急回滚；维持旧扣费，仍双写新审计字段；
2. **observe**：实际扣旧成本，同时计算 canonical shadow cost 和 delta；
3. **charge**：使用 canonical bucket 成本；旧算法只保留 shadow 对比。

`ambiguous` 是安全例外：除紧急 legacy 回滚模式外，observe/charge 均按 §5.2 的较低
候选结算并触发隔离，避免观察期继续产生潜在用户多扣。

发布步骤：

1. 应用 billing-owned 085、log-owned 086、channel-owned 087，并分别验证三个 schema；
2. 上线全部 billing/log consumer，模式 `observe`，canonical producer feature gate 关闭；
3. 确认所有 consumer 实例支持 v1，再上线 relay/provider，继续双写 legacy 字段；
4. 小流量开启 canonical producer，至少观察 48 小时；
5. 验证 priced 请求 `ambiguous=0`、无 unexplained delta、日志/UI 总量契约正确；
6. 先对 Kimi / Z.AI / MiniMax allowlist 切 `charge`，再全量；
7. 完成供应商账单对账后关闭 allowlist；
8. canonical charge 稳定后再决定是否上线第二阶段 088 定价快照；
9. 跨两个版本且 `usage_contract_version=0` 流量归零后移除 `PromptExclusive` 依赖。

回滚只切回 `observe` / `legacy` 并回滚服务镜像；additive 字段和 migration 不回滚、
不删列。回滚 relay 时关闭 canonical producer feature gate，但继续保留兼容字段。已经
写入的 canonical ledger 保持可读。

### 11.1 生产验证查询

发布前后分别在各 owner schema 查询 migration，确认 085/086/087 只进入目标 schema：

```sql
SELECT version, applied_at
FROM oneapi_billing.schema_migrations
ORDER BY applied_at DESC LIMIT 5;

SELECT version, applied_at
FROM oneapi_log.schema_migrations
ORDER BY applied_at DESC LIMIT 5;

SELECT version, applied_at
FROM oneapi_channel.schema_migrations
ORDER BY applied_at DESC LIMIT 5;
```

账本金额、契约覆盖和 ambiguous 基线至少检查：

```sql
SELECT COUNT(*) AS cost_mismatch
FROM oneapi_billing.billing_ledgers
WHERE type = 'consume'
  AND ABS(prompt_cost + completion_cost + cache_read_cost
          + cache_creation_5m_cost + cache_creation_1h_cost) <> ABS(amount);

SELECT usage_contract_version, usage_parse_status, usage_semantics,
       canonical_present, usage_decision_reason, COUNT(*) AS n
FROM oneapi_billing.billing_ledgers
WHERE type = 'consume' AND created_at >= :observe_start
GROUP BY usage_contract_version, usage_parse_status, usage_semantics,
         canonical_present, usage_decision_reason;

SELECT source_kind, upstream_model_id, COUNT(*) AS missing_source
FROM oneapi_billing.billing_ledgers
WHERE type = 'consume' AND created_at >= :observe_start
  AND (source_kind = '' OR upstream_model_id = '')
GROUP BY source_kind, upstream_model_id;
```

切 charge 前要求：新 producer 行不存在 `version=v1` 且非法/缺失 canonical 的静默
fallback；`ambiguous=0`；`cost_mismatch=0`；普通 channel 和 subscription 的来源字段
覆盖率达到 100%。查询参数使用发布记录中的 observe 起始时间，不对全量历史
legacy 行套用新契约。

## 12. 验收标准

- canonical v1 的 priced usage 100% 带合法 parse status；cache usage 在 charge 前必须
  为 verified 且带明确 semantics，或明确进入 ambiguous 安全路径；
- `calculateCanonicalCost` 不含 `promptExclusive` 或协议判断；
- ambiguous 不产生伪 canonical，subset/exclusive 候选调用同一个纯计费函数；
- 同一 canonical fixture 经 Chat / Responses / Anthropic 投影后，用户成本一致；
- GLM Anthropic → OpenAI 的客户端投影与日志 billable total 符合 inclusive 规则；
  Anthropic 未上报 total 时，审计字段 `reported_total_tokens` 保持 0，不伪造上游值；
- ledger 同时可解释 reported total、billable total 和五桶成本；启用 088 后还能还原
  完整价格版本；
- 48 小时 observe 窗口内 `ambiguous=0`，unexplained cost delta=0；
- 新旧 producer/consumer 四种组合均满足双写契约，v1 缺字段不会 legacy fallback；
- billing/log migration 只进入各自 schema，三种数据库 mirror/ownership 检查通过；
- Web 不再从 legacy token 字段推导 billable total，Dashboard total 包含 cache creation；
- 重试 / 异步结算不会产生重复 ledger；
- 历史审计脚本默认只出报告，必须显式审批才生成冲正；
- MySQL、Postgres、SQLite migration 与生命周期测试全部通过。

## 13. 非目标

- 本方案不修改已配置的模型售价；价格是否合理仍由模型价格治理流程决定；
- 不根据模型名猜 usage 协议；
- 不以 `total_tokens` 数值关系自动重算全部历史账单；
- 不原地改写历史 consume ledger；
- 不在本方案中引入新的计费微服务或消息队列。

## 14. 外部协议依据

- Z.AI 为 Claude Code 提供 Anthropic Messages 兼容端点：
  <https://docs.z.ai/scenario-example/develop-tools/claude>
- Anthropic 说明 input、cache write、cache read、output token 分桶相加计费：
  <https://docs.anthropic.com/en/docs/about-claude/pricing>
- Z.AI OpenAI 兼容缓存字段 `prompt_tokens_details.cached_tokens` 的示例与计费说明：
  <https://docs.z.ai/guides/capabilities/cache>
