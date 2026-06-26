# 混合中转网关技术方案：Adaptor 广度聚合 + apicompat/mimicry 订阅深度利用

> 分支：`feature/hybrid-relay-adaptor-apicompat`（基于 `develop`）
> 创建日期：2026-06-26
> 状态：已评审

## 一、背景与目标

micro-one-api 需要同时支持两类上游：

1. **大量 API Key 厂商中转**（DeepSeek、Kimi、通义、智谱、Groq、OpenRouter…）：
   这类上游本质是标准 OpenAI 兼容端点，核心需求是"广度聚合 + 协议格式互转 + 多渠道路由/重试/熔断"。
   **参考实现：new-api 的 Adaptor 模式。**

2. **少数订阅账号深度利用**（首批仅 Codex 订阅、Claude Code 订阅）：
   这类上游是订阅账号，不能简单当 API Key 透传，核心需求是"客户端身份伪装 + 协议链式转换 + token 刷新 + 配额窗口感知"。
   **参考实现：sub2api 的 apicompat + Identity/Fingerprint/Mimicry 体系。**

**核心思路**：用 new-api 的 Adaptor 抽象做"厂商广度聚合"的外层骨架，用 sub2api 的
apicompat + mimicry 做"订阅账号深度处理"的内部能力。二者通过统一的 Adaptor 接口组合。

---

## 二、现状分析（micro-one-api）

### 2.1 架构现状

micro-one-api 是 **Kratos 微服务** 架构，relay-gateway 是其中一个服务：

```
cmd/relay-gateway          网关入口
internal/relay/
  ├── biz/                 relay 编排（auth→model→channel→retry）
  ├── provider/            上游 Provider 接口 + 实现（OpenAI/Anthropic/Gemini/Azure/VoyageAI）
  ├── server/              HTTP 入口（/v1/chat/completions、/v1/responses、/v1/messages、WS）
  ├── service/             gRPC service
  └── data/                gRPC 客户端适配（identity/channel/billing）
```

### 2.2 现有转换链路（ChatCompletions 主干，Responses 旁路存在）

```
客户端协议          内部处理格式              上游 Provider
/v1/chat/completions → ChatCompletionsRequest → OpenAIProvider.ChatCompletions()
/v1/messages(Anthropic) → ChatCompletionsRequest → XXXProvider.ChatCompletions()
                         (anthropic_inbound.go 做转换)
/v1/responses        → OpenAIResponsesRequest → responses handler / fallback
```

**问题**：

1. **Provider 接口耦合"OpenAI ChatCompletions 中枢"**：`ChatCompletions(ctx, *ChatCompletionsRequest)`
   假设所有上游都能用 OpenAI 标准请求描述，但订阅账号（Codex/ChatGPT）只接受 Responses 格式，
   Claude OAuth 需要伪装后的 Messages 格式。当前 Responses/WS 是走 `RawRequest` 透传绕开的，
   缺乏统一的"协议感知"层。

2. **没有"订阅账号"概念**：`Channel` 只有 `Type(int32)` + `Key(string)` + `BaseURL`，
   无法表达 OAuth 订阅账号的 access_token/refresh_token/account_id/plan_type/配额窗口/指纹等元数据。

3. **没有身份伪装能力**：所有上游都按"API Key 透传"处理，无法满足 Claude OAuth 需要的
   system prompt 注入、metadata.user_id 重写、fingerprint 注入、anthropic-beta 计算。

4. **协议转换不完整**：Anthropic inbound 只转了简单的 text/tool，没有 thinking/cache_control/
   structured system/流式 SSE 事件级转换；没有 OpenAI Responses ⇄ Anthropic 的双向转换矩阵。

### 2.3 为什么不直接搬 new-api 或 sub2api

| 维度 | new-api | sub2api | micro-one-api 现状 |
|------|---------|---------|-------------------|
| 架构 | 单体（Gin） | 单体（Gin） | Kratos 微服务 |
| 转换枢纽 | OpenAI ChatCompletions | OpenAI Responses | OpenAI ChatCompletions（不完整） |
| 订阅深度 | 弱（仅 codex adaptor） | 极深（apicompat+mimicry+identity） | 无 |
| 厂商广度 | 强（40+ adaptor） | 弱（仅 4 平台） | 中（factory switch 30+，但逻辑薄） |
| 账号体系 | Channel（int Type+Key） | Account（platform+type+credentials） | Channel（int32 Type+Key） |

**结论**：需要一套"混合"设计，取两者之长，并适配 Kratos 微服务风格。

---

## 三、总体架构设计

### 3.1 分层模型

```
┌─────────────────────────────────────────────────────────────────┐
│  入口层（server/）：协议感知的 inbound handler                    │
│  /v1/chat/completions  /v1/messages  /v1/responses  /v1/ws       │
├─────────────────────────────────────────────────────────────────┤
│  编排层（biz/）：Plan(auth→model→channel) + RetryExecutor        │
├─────────────────────────────────────────────────────────────────┤
│  ★ Adaptor 层（relay/adaptor/）：统一适配器接口（新增）           │
│   - ConvertXxxRequest / ConvertXxxResponse（协议转换）           │
│   - BuildUpstreamRequest（含身份伪装、URL/Header 构造）          │
│   - DoRequest / DoResponse                                       │
├─────────────────────────────────────────────────────────────────┤
│  ★ 转换内核（relay/apicompat/）：四格式转换矩阵（移植自 sub2api） │
│   Anthropic ⇄ Responses ⇄ ChatCompletions（含流式 SSE 状态机）   │
├─────────────────────────────────────────────────────────────────┤
│  ★ 身份层（relay/identity/）：订阅账号指纹与伪装（移植自 sub2api）│
│   Fingerprint + Mimicry + Metadata 重写                          │
├─────────────────────────────────────────────────────────────────┤
│  ★ 凭证层（relay/credential/）：OAuth token 管理与刷新           │
├─────────────────────────────────────────────────────────────────┤
│  数据层（data/）：channel/account repo + gRPC 客户端              │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 核心设计决策

**决策 1：订阅深度链路以 OpenAI Responses 为协议转换枢纽**

理由：
- 首批订阅账号（Codex / Claude Code）上游分别依赖 Responses / Anthropic Messages，单靠 ChatCompletions 无法覆盖
- Responses 是最"富"的格式（reasoning/thinking、tool、structured output 都有原生表达）
- sub2api 已验证"以 Responses 为枢纽"的链式转换可行（CC→Responses→Anthropic）
- ChatCompletions 保留为客户端入站/出站协议和 API Key 厂商兼容路径，不作为订阅深度链路的唯一中枢

**决策 2：Adaptor 接口吸收 new-api 的 ConvertXxx 方法 + sub2api 的身份处理**

不再用当前薄薄的 `Provider` 接口，而是引入"Adaptor"层，每个上游一个 adaptor 实现，
内部决定是走"API Key 直转"还是"OAuth 伪装转发"。

**决策 3：Channel 与 SubscriptionAccount 分层，不互相冒充**

- `Channel`（保留）：描述 API Key 厂商渠道（Type/BaseURL/Key/Models）
- `SubscriptionAccount`（新增）：描述订阅账号（Platform/AccountType/Credentials/Quota/Fingerprint）
- 选择器层分别处理 `Channel` 和 `SubscriptionAccount`，避免把订阅账号硬塞进 Channel 类型里

---

## 四、详细设计

### 4.1 Adaptor 接口设计（relay/adaptor/）

参考 new-api 的 `relay/channel/adapter.go`，但精简为 micro-one-api 的微服务风格：

```go
// relay/adaptor/adaptor.go
package adaptor

// Format 标识协议格式，作为转换矩阵的维度
type Format string

const (
    FormatOpenAIChatCompletions Format = "chat_completions"
    FormatOpenAIResponses       Format = "responses"
    FormatAnthropicMessages     Format = "anthropic_messages"
    FormatGemini                Format = "gemini"
)

// RelayContext 携带一次转发的完整上下文（替代散落的参数）
type RelayContext struct {
    InboundFormat  Format      // 客户端入站协议
    ClientModel    string      // 客户端请求的模型名
    ResolvedModel  string      // 映射后的上游模型名
    Channel        *ChannelRef // 选中的 API Key 渠道
    Account        *AccountRef // 选中的订阅账号
    IsStream       bool
    UserID         int64
    RequestID      string
    RawBody        []byte      // 客户端原始请求体（供透传/mimicry）
}

// Adaptor 是每个上游的统一适配器接口。
// 设计要点：
//   - ConvertRequest/ConvertResponse 负责"入站协议 → 上游协议"和反向转换
//   - BuildUpstreamRequest 负责 URL/Header/body 的最终构造（含身份伪装）
//   - 上游协议由 adaptor 自己决定（可能是 responses/messages/chat_completions/raw）
type Adaptor interface {
    // Init 用 RelayContext 初始化 adaptor（类比 new-api adaptor.Init）
    Init(ctx *RelayContext)

    // ConvertRequest 将客户端请求转换为上游请求体。
    // inbound 为客户端原始格式，返回上游格式 + 转换后的 body。
    ConvertRequest(ctx *RelayContext, inbound Format, body []byte) (upstream Format, []byte, error)

    // GetUpstreamURL 返回上游目标 URL
    GetUpstreamURL(ctx *RelayContext) (string, error)

    // BuildUpstreamRequest 构造发往上游的 http.Request（含身份伪装、签名等）
    BuildUpstreamRequest(ctx *RelayContext, upstream Format, body []byte) (*http.Request, error)

    // ConvertResponse 将上游响应转换为客户端期望的出站格式。
    // 支持流式（reader）和非流式（body）。
    ConvertResponse(ctx *RelayContext, upstream Format, resp *http.Response) (outbound Format, []byte, error)
    ConvertStreamResponse(ctx *RelayContext, upstream Format, resp *http.Response) (outbound Format, io.Reader, error)

    // ModelList 返回该上游支持的模型
    ModelList() []string
    Name() string
}
```

**Adaptor 注册表**（类比 new-api `GetAdaptor`）：

```go
// relay/adaptor/registry.go
var registry = map[int32]func() Adaptor{}

func Register(channelType int32, factory func() Adaptor) { registry[channelType] = factory }
func GetAdaptor(channelType int32) (Adaptor, bool) { ... }

func init() {
    Register(ChannelTypeOpenAI, func() Adaptor { return &OpenAIAdaptor{} })
    Register(ChannelTypeAnthropic, func() Adaptor { return &AnthropicAdaptor{} })
    Register(ChannelTypeDeepSeek, func() Adaptor { return &OpenAICompatibleAdaptor{...} })
    // ...
    Register(SubscriptionPlatformCodex, func() Adaptor { return &CodexOAuthAdaptor{} })   // 订阅
    Register(SubscriptionPlatformClaude, func() Adaptor { return &ClaudeOAuthAdaptor{} }) // 订阅
}
```

### 4.2 Adaptor 实现分层

```
relay/adaptor/
├── adaptor.go              接口定义
├── registry.go             注册表 + GetAdaptor
├── base.go                 BaseAdaptor（共享 HTTP 客户端、header 工具）
├── openai_compatible.go    OpenAICompatibleAdaptor（覆盖 30+ 厂商，API Key 模式）
├── anthropic.go            AnthropicAdaptor（Anthropic API Key）
├── gemini.go               GeminiAdaptor
├── azure.go                AzureAdaptor
├── codex_oauth.go          ★ CodexOAuthAdaptor（ChatGPT 订阅，Responses 透传+伪装）
├── claude_oauth.go         ★ ClaudeOAuthAdaptor（Claude 订阅，Messages+伪装）
└── codex_oauth_test.go
```

**层次关系**：

```
OpenAICompatibleAdaptor（API Key 厂商，广度）
   ├── 内部直接调 apicompat（如需协议转换）或直接透传
   └── 不做身份伪装

CodexOAuthAdaptor / ClaudeOAuthAdaptor（订阅深度）
   ├── 内部调 apicompat（Responses/CC → Anthropic）
   ├── 内部调 identity.Fingerprint + Mimicry
   └── 内部调 credential.TokenProvider 拿 access_token
```

### 4.3 转换内核（relay/apicompat/）—— 移植自 sub2api

**直接移植 sub2api 的 `internal/pkg/apicompat/` 包**（约 9400 行，已验证成熟）：

```
relay/apicompat/
├── types.go                          四种格式的类型定义
├── anthropic_to_responses.go         Anthropic → Responses
├── responses_to_anthropic_request.go Responses → Anthropic 请求
├── responses_to_anthropic.go         Responses → Anthropic 响应（含流式状态机）
├── anthropic_to_responses_response.go Anthropic 响应 → Responses（含流式）
├── chatcompletions_to_responses.go   ChatCompletions → Responses
├── responses_to_chatcompletions.go   Responses → ChatCompletions
└── chatcompletions_responses_bridge.go 桥接
```

**适配要点**：
- sub2api 用 `tidwall/gjson`+`sjson` 做 JSON 操作，micro-one-api 用 `bytedance/sonic`，
  移植时统一为 sonic（性能更好且已是项目依赖）
- 流式 SSE 事件转换状态机（`AnthropicEventToResponsesState` 等）整体保留，
  适配 micro-one-api 的 `StreamChunk` / SSE writer
- 首批只落地 Codex / Claude Code 所需的请求与流式转换，其余协议支路后续再补

**转换矩阵**（订阅深度链路的枢纽 = Responses）：

```
                ┌─────────────────┐
                │  OpenAI Responses │ ← 枢纽
                └──┬───────┬───────┘
         ┌─────────┘       └─────────┐
         ▼                           ▼
  Anthropic Messages          ChatCompletions
  (thinking/cache_control/    (tools/usage/
   tool_use/流式SSE)          reasoning_content)
```

### 4.4 身份层（relay/identity/）—— 移植自 sub2api

```
relay/identity/
├── fingerprint.go          Fingerprint 结构 + 默认值
├── identity_service.go     IdentityService（指纹缓存、metadata 重写）
├── mimicry.go              ClaudeOAuthMimicry（system prompt 注入、字段补齐）
└── claude_code_detector.go 判定是否真实 Claude Code 客户端
```

**核心能力（移植自 sub2api）**：

1. **Fingerprint**：每个订阅账号维护一套客户端指纹
   ```go
   type Fingerprint struct {
       ClientID, UserAgent                   string
       StainlessLang, StainlessPackageVersion string
       StainlessOS, StainlessArch            string
       StainlessRuntime, StainlessRuntimeVersion string
   }
   ```
   缓存到 Redis（key: `fp:{accountID}`），TTL 续期。

2. **Claude Code Mimicry**（`ClaudeOAuthAdaptor.BuildUpstreamRequest` 内调用）：
   - 判定 `shouldMimic = account.IsOAuth && !isClaudeCodeClient`
   - **system prompt 重写**：注入官方 CC system prompt + billing attribution block
   - **metadata.user_id 重写**：用 account UUID + ClientID 构造合法 user_id
   - **字段补齐**：补 `temperature=1`、`max_tokens=128000`、`tools=[]`、`context_management`
   - **anthropic-beta 计算**：`computeFinalAnthropicBeta` + body 能力对称 sanitize

3. **Codex Mimicry**（`CodexOAuthAdaptor.BuildUpstreamRequest` 内）：
   - 注入 `chatgpt-account-id`、`originator: codex_cli_rs`、`OpenAI-Beta: responses=experimental`
   - User-Agent 对齐 `codex_cli_rs/{version} ({OS}; {arch}) {terminal}`

### 4.5 凭证层（relay/credential/）—— OAuth token 管理

```
relay/credential/
├── token_provider.go       TokenProvider 接口
├── claude_token_provider.go Claude OAuth 刷新
├── openai_token_provider.go Codex/ChatGPT OAuth 刷新
└── refresh_task.go         后台刷新任务
```

参考 sub2api 的 `token_refresh_service.go` + new-api 的 `codex_credential_refresh_task.go`：

```go
type TokenProvider interface {
    // GetAccessToken 返回有效 access_token，必要时触发刷新
    GetAccessToken(ctx context.Context, accountID int64) (string, error)
    // Refresh 强制刷新
    Refresh(ctx context.Context, accountID int64) error
}
```

- **缓存**：Redis `token:{platform}:{accountID}`，提前 3min 续期（skew）
- **后台任务**：定时（10min）扫描即将过期（24h 内）的 token 批量刷新
- **失败处理**：刷新失败 N 次临时标记不可调度

### 4.6 数据模型扩展

#### 4.6.1 新增 SubscriptionAccount 表（订阅账号）

```sql
-- migrations/034_create_subscription_accounts.sql
CREATE TABLE `subscription_accounts` (
  `id`              BIGINT NOT NULL AUTO_INCREMENT,
  `name`            VARCHAR(128) NOT NULL,
  `platform`        VARCHAR(32) NOT NULL,            -- codex/claude
  `account_type`    VARCHAR(32) NOT NULL,            -- oauth/setup_token
  `credentials`     TEXT NOT NULL,                   -- 加密存储 access_token/refresh_token/account_id
  `extra`           TEXT,                            -- plan_type/quota_window/fingerprint_base 等
  `group_id`        VARCHAR(64) NOT NULL DEFAULT 'default',
  `concurrency`     INT NOT NULL DEFAULT 1,
  `priority`        INT NOT NULL DEFAULT 0,
  `status`          VARCHAR(16) NOT NULL DEFAULT 'active',  -- active/disabled/rate_limited/expired
  `expires_at`      BIGINT NOT NULL DEFAULT 0,       -- access_token 过期时间
  `rate_limited_until` BIGINT NOT NULL DEFAULT 0,
  `quota_used_percent` FLOAT DEFAULT 0,             -- 订阅配额使用百分比
  `quota_reset_at`  BIGINT NOT NULL DEFAULT 0,
  `last_used_at`    BIGINT NOT NULL DEFAULT 0,
  `fingerprint`     TEXT,                            -- 缓存的指纹快照
  `created_at`      BIGINT NOT NULL,
  `updated_at`      BIGINT NOT NULL,
  PRIMARY KEY (`id`),
  INDEX `idx_platform_status` (`platform`, `status`),
  INDEX `idx_group` (`group_id`),
  INDEX `idx_expires` (`expires_at`)
);
```

#### 4.6.2 不把订阅账号包装成特殊 Channel

首批实现里，`Channel` 只保留给 API Key 厂商；订阅账号用 `SubscriptionAccount` 作为一等实体，不再伪装成特殊 `channels.type`。

这样做的好处是：

- 路由边界清晰，选择器可以分别处理渠道和订阅账号
- 订阅账号状态、指纹、token 过期时间不用塞进 Channel 的通用字段
- 后续扩展新的订阅平台时，不需要重新定义 Channel 类型

#### 4.6.3 proto 扩展

```protobuf
// api/common/v1/common.proto 新增
message SubscriptionAccount {
  int64 id = 1;
  string name = 2;
  string platform = 3;
  string account_type = 4;
  string group_id = 5;
  int32 concurrency = 6;
  int32 priority = 7;
  string status = 8;
  int64 expires_at = 9;
  int64 rate_limited_until = 10;
  float quota_used_percent = 11;
  int64 quota_reset_at = 12;
}
```

channel-service 新增 RPC：

```protobuf
service ChannelService {
  // 已有 RPC...
  rpc ListSubscriptionAccounts(ListSubscriptionAccountsRequest) returns (ListSubscriptionAccountsReply);
  rpc GetSubscriptionAccount(GetSubscriptionAccountRequest) returns (GetSubscriptionAccountReply);
  rpc SelectSubscriptionAccount(SelectSubscriptionAccountRequest) returns (SelectSubscriptionAccountReply);
}
```

---

## 五、转发流程（以 Claude OAuth 订阅为例）

### 场景：客户端用 OpenAI ChatCompletions 协议调用 Claude Code 订阅账号

```
客户端 POST /v1/chat/completions  {model:"claude-sonnet-4", messages:[...], stream:true}

1. server/handleChatCompletions
   - auth → Plan → SelectSubscriptionAccount(选中 Claude Code 订阅账号)

2. adaptor.GetSubscriptionAdaptor(ClaudeCode) → ClaudeOAuthAdaptor

3. adaptor.ConvertRequest(ctx, FormatOpenAIChatCompletions, body)
   - 调 apicompat.ChatCompletionsToResponses(body)
   - 调 apicompat.ResponsesToAnthropicRequest(responsesReq)
   - 返回 (FormatAnthropicMessages, anthropicBody)

4. adaptor.GetUpstreamURL(ctx)
   → "https://api.anthropic.com/v1/messages?beta=true"

5. adaptor.BuildUpstreamRequest(ctx, FormatAnthropicMessages, anthropicBody)
   - identity.GetOrCreateFingerprint(accountID) → Fingerprint
   - mimicry.RewriteSystemPrompt(anthropicBody) → 注入 CC system prompt
   - mimicry.RewriteMetadataUserID(body, accountUUID, clientID)
   - mimicry.NormalizeBody(body) → 补 temperature/max_tokens/tools
   - credential.GetAccessToken(accountID) → access_token
   - 构造 http.Request（Authorization: Bearer + anthropic-beta header）

6. 发送请求 → 上游返回 Anthropic 流式 SSE

7. adaptor.ConvertStreamResponse(ctx, FormatAnthropicMessages, resp)
   - apicompat.AnthropicEventToResponsesEvents(SSE 事件流)
   - apicompat.ResponsesToChatCompletionsStream(Responses 事件)
   - 返回 ChatCompletions SSE 流给客户端

8. 计费：从上游 usage 提取 token 数 → commit quota
```

### 场景：客户端用 Anthropic Messages 协议调用 Codex 订阅账号

```
客户端 POST /v1/messages  {model:"gpt-5", ...}  （Claude Code CLI 风格）

1. server/handleAnthropicMessages
   - auth → Plan → SelectSubscriptionAccount(选中 Codex 订阅账号)

2. adaptor.GetSubscriptionAdaptor(Codex) → CodexOAuthAdaptor

3. adaptor.ConvertRequest(ctx, FormatAnthropicMessages, body)
   - apicompat.AnthropicToResponses(body)
   - 返回 (FormatOpenAIResponses, responsesBody)

4. adaptor.GetUpstreamURL → "https://chatgpt.com/backend-api/codex/responses"

5. adaptor.BuildUpstreamRequest
   - credential.GetAccessToken → access_token
   - 注入 chatgpt-account-id / originator / OpenAI-Beta
   - UA 对齐 codex_cli_rs

6. 发送 → 上游返回 Responses 流式 SSE

7. adaptor.ConvertStreamResponse(ctx, FormatOpenAIResponses, resp)
   - apicompat.ResponsesToAnthropic(SSE 事件)
   - 返回 Anthropic SSE 给客户端
```

---

## 六、实施阶段划分

### Phase 1：Adaptor 抽象层重构（广度，不破坏存量）

**目标**：引入 Adaptor 接口，将现有 Provider 实现包装为 Adaptor，存量行为不变。

- [ ] 新建 `internal/relay/adaptor/` 包，定义接口 + 注册表
- [ ] 实现 `OpenAICompatibleAdaptor`（包装现有 `OpenAIProvider`）
- [ ] 实现 `AnthropicAdaptor`（包装现有 `AnthropicProvider`）
- [ ] 实现 `GeminiAdaptor`、`AzureAdaptor`
- [ ] server 层改为 `GetAdaptor(ch.Type)` 调用，删除 `providerFactory.CreateProvider` 直接调用
- [ ] 全量回归测试，确保存量 API Key 渠道行为不变

**交付物**：Adaptor 层 + 存量功能回归通过

### Phase 2：apicompat 转换内核移植

**目标**：引入四格式转换矩阵，替换现有简陋的 anthropic_inbound 转换。

- [ ] 移植 sub2api `pkg/apicompat/` → `internal/relay/apicompat/`（gjson→sonic）
- [ ] 流式 SSE 状态机适配 micro-one-api 的 stream writer
- [ ] 改造 `anthropic_inbound.go` 使用 apicompat 替代手写转换
- [ ] 新增 `/v1/messages` → Codex/OpenAI 上游的转换路径
- [ ] 单元测试：四种格式两两转换 + 流式事件序列

**交付物**：apicompat 包 + 转换测试通过

### Phase 3：订阅账号数据模型与选择器

**目标**：支持存储和管理 OAuth 订阅账号。

- [ ] migration 创建 `subscription_accounts` 表
- [ ] channel-service 新增 SubscriptionAccount CRUD RPC
- [ ] admin-service 新增订阅账号管理 API
- [ ] relay/biz 新增 `SelectSubscriptionAccount`
- [ ] relay/biz 将订阅账号选择与 API Key 渠道选择分离

**交付物**：订阅账号可创建、存储、选择

### Phase 4：身份伪装 + 凭证层（订阅深度）

**目标**：Claude Code / Codex 订阅账号可正常调用，含伪装和刷新。

- [ ] 移植 `identity/`（Fingerprint + Mimicry + Metadata 重写）
- [ ] 移植 `credential/`（TokenProvider + 后台刷新任务）
- [ ] 实现 `ClaudeOAuthAdaptor`（集成 apicompat + identity + credential）
- [ ] 实现 `CodexOAuthAdaptor`（集成 apicompat + credential）
- [ ] 端到端测试：真实订阅账号调用

**交付物**：首批两类订阅账号可深度利用

### Phase 5：生产化

- [ ] 配额窗口感知（从上游响应头提取 used% / reset_after）
- [ ] 限流感知（429/overload cooldown）
- [ ] 粘性会话（session hash → account）
- [ ] 可观测性：per-adaptor / per-platform metrics
- [ ] 配置项：relay-gateway.yaml 增加 identity/mimicry 开关

---

## 七、风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| apicompat 移植量大（9400 行） | 工期 | 分批移植，Phase 2 先只移植 Anthropic⇄Responses，CC⇄Responses 后补 |
| sub2api 用 gjson/sjson，项目用 sonic | 语义偏差 | 移植时统一 JSON 库，补充 round-trip 测试 |
| 订阅账号被上游风控/封禁 | 业务风险 | 指纹随机化、metadata 合规、提供"合规使用"免责声明 |
| OAuth token 刷新失败导致不可用 | 可用性 | 后台刷新 + 请求时按需刷新双保险 + 临时不可调度降级 |
| Adaptor 重构破坏存量 | 回归 | Phase 1 纯包装，不改变存量逻辑；用 feature flag 控制新路径 |
| 订阅账号并发超限 | 429 风暴 | 并发槽位（Redis 信号量）+ 上游 429 cooldown |

---

## 八、与 new-api / sub2api 的对应关系

| 能力 | 来源 | 落地位置 |
|------|------|---------|
| Adaptor 接口（ConvertXxx/BuildRequest） | new-api `relay/channel/adapter.go` | `relay/adaptor/adaptor.go` |
| GetAdaptor 注册表 | new-api `relay/relay_adaptor.go` | `relay/adaptor/registry.go` |
| 四格式转换矩阵 | sub2api `pkg/apicompat/` | `relay/apicompat/` |
| Fingerprint + Mimicry | sub2api `identity_service.go` + `normalizeClaudeOAuthRequestBody` | `relay/identity/` |
| metadata.user_id 重写 | sub2api `RewriteUserIDWithMasking` | `relay/identity/identity_service.go` |
| OAuth token 刷新 | sub2api `token_refresh_service.go` + new-api `codex_credential_refresh_task.go` | `relay/credential/` |
| Channel 选择 + Retry | micro-one-api 现有 `relay/biz`（保留增强） | `relay/biz/`（不变） |
| Responses ⇄ ChatCompletions 回退 | micro-one-api 现有 `responses_fallback.go`（重构到 apicompat） | `relay/apicompat/` |

---

## 九、附录：目录结构总览（目标态）

```
internal/relay/
├── adaptor/                    ★ 新增：Adaptor 抽象层
│   ├── adaptor.go              接口定义（ConvertRequest/Response、BuildUpstreamRequest）
│   ├── registry.go             注册表（channelType → Adaptor 工厂）
│   ├── base.go                 BaseAdaptor（共享 HTTP/header 工具）
│   ├── openai_compatible.go    30+ API Key 厂商 adaptor
│   ├── anthropic.go            Anthropic API Key adaptor
│   ├── codex_oauth.go          ★ ChatGPT 订阅 adaptor（含伪装）
│   ├── claude_oauth.go         ★ Claude 订阅 adaptor（含伪装）
├── apicompat/                  ★ 新增：转换内核（移植自 sub2api）
│   ├── types.go
│   ├── anthropic_to_responses.go
│   ├── responses_to_anthropic.go
│   ├── chatcompletions_to_responses.go
│   ├── responses_to_chatcompletions.go
│   └── *_stream.go             流式 SSE 事件状态机
├── identity/                   ★ 新增：身份伪装层（移植自 sub2api）
│   ├── fingerprint.go
│   ├── mimicry.go
│   ├── claude_code_detector.go
│   └── identity_service.go
├── credential/                 ★ 新增：OAuth 凭证层
│   ├── token_provider.go
│   ├── claude_token_provider.go
│   ├── openai_token_provider.go
│   └── refresh_task.go
├── biz/                        编排层（保留增强）
│   ├── relay.go                Plan + RetryExecutor（不变）
│   ├── retry.go                （不变）
│   └── model_mapping.go        （不变）
├── server/                     入口层（改造为调用 adaptor）
│   ├── http.go                 /v1/chat/completions（改用 adaptor）
│   ├── anthropic_inbound.go    /v1/messages（改用 apicompat）
│   ├── responses_fallback.go   （重构进 apicompat）
│   └── openai_ws_*.go          （保留，WS 透传）
├── service/                    gRPC service（不变）
└── data/                       gRPC 客户端（扩展 SubscriptionAccount）
```

---

## 十、建议的 MVP 切入点

优先做一个可验证、可回滚的最小闭环，而不是一次性铺开全部能力：

1. 先保留现有 `provider` / `channel` 逻辑不变，只新增 `adaptor` 外层包装。
2. 先移植 `apicompat` 的 `ChatCompletions ⇄ Responses` 和 `Responses ⇄ Anthropic`。
3. 先支持两类上游：
   - 大量 API Key 厂商：继续走 adaptor 直转
   - 少数订阅账号：走 `apicompat + credential + identity`
4. 先只做两个深度场景：
   - Claude OAuth
   - Codex / ChatGPT OAuth
5. 先把身份伪装限定为“请求头 + metadata + TLS 指纹”三层，不扩展到更复杂的行为模拟。

这样可以在不推翻现有 relay 结构的前提下，先验证：

- 广度聚合是否仍然稳定
- Responses 中枢是否足够承接订阅协议
- 伪装层是否真的能提高订阅账号可用性
