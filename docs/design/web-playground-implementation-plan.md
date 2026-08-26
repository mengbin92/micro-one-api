# 用户侧 Web Playground 完整实现方案

> 制定日期：2026-08-26
> 状态：设计完成，待实施
> 范围：`web/` 用户控制台、`relay-gateway` 浏览器访问边界、部署配置与测试
> 决策摘要：首版以 OpenAI Chat Completions 为唯一执行协议；浏览器直接调用
> `relay-gateway`，API Key 只保存在当前页面内存中，不经过 `admin-api`、不落本地存储；
> Relay 在实际生产路由上启用精确 Origin 的 CORS。无数据库迁移、无 proto 变更。

## 1. 背景与问题定义

当前用户控制台已经覆盖：

- 仪表盘、余额与用量概览；
- API 密钥创建、列表与删除；
- 使用记录；
- 模型价格、充值、订阅和订单；
- API 地址、cURL / SDK / CLI 接入指南。

但它仍然是一个“账户与消费控制台”，没有完成开发者首次调用的站内闭环：

```text
当前：创建密钥 -> 阅读静态示例 -> 离开站点运行 curl / SDK -> 回到站点查看记录

目标：创建密钥 -> 在线调试 -> 看到流式结果 / Token / 延迟 -> 查看使用记录
```

代码现状：

- `web/src/router.tsx` 没有 Playground 路由；
- `web/src/components/AppNavigation.tsx` 的核心导航只有仪表盘、密钥、使用记录和指南；
- `web/src/pages/ApiGuidePage.tsx` 已展示 `/v1/models` 与
  `/v1/chat/completions` 的静态示例，但不能执行；
- `web/src/pages/TokensPage.tsx` 遵守“完整 API Key 只显示一次”，列表仅返回掩码；
- `relay-gateway` 已支持真实的模型权限过滤、Chat Completions 流式 / 非流式调用、
  用量结算、限流、Request Body 限制和使用日志；
- 标准 Compose 部署中 `admin-api` 暴露 `3000`，`relay-gateway` 暴露 `8080`，
  浏览器调用通常跨 Origin；
- `platform/middleware/cors.go` 已有 CORS 实现，但实际启动路径
  `cmd/relay-gateway/relay_helpers.go -> HTTPServer.RegisterRoutes` 没有把它挂到
  `HTTPServer.routeMiddleware`；`internal/server/http_enhanced.go` 中的 CORS 链不是当前
  Relay 启动所使用的注册路径。仅设置 `CORS_ALLOWED_ORIGINS` 目前不足以让生产路由支持浏览器调用。

因此，本功能不是单独增加一个 React 页面。可交付范围必须同时包含：

1. 用户界面与交互状态；
2. API Key 的安全生命周期；
3. Relay 模型列表和 SSE 调用客户端；
4. 实际 Relay 路由的 CORS 接线；
5. 错误诊断、测试、部署和回滚。

## 2. 目标、非目标与成功标准

### 2.1 目标

1. 登录用户可以在控制台内完成一次真实、计费的 Chat Completions 请求。
2. 模型列表来自该 API Key 对 Relay `/v1/models` 的真实查询，不使用价格目录替代。
3. 默认支持流式输出，并可随时停止；非流式作为调试选项保留。
4. 页面展示请求状态、首字延迟、总耗时、finish reason 和服务端返回的 Token usage。
5. 常见鉴权、权限、余额、限流、渠道和跨域错误可被明确区分。
6. API Key 不进入 URL、日志、错误上报、React Query 缓存、`localStorage`、
   `sessionStorage`、IndexedDB 或服务端会话。
7. 桌面和移动端均可完成“输入 Key -> 加载模型 -> 发送 -> 停止 / 完成”的主流程。

### 2.2 非目标

首版明确不实现：

- Responses API、Anthropic Messages API 和协议切换；
- 图片、音频、embeddings、moderations 等端点；
- tools / function calling 的可视化执行器；
- 文件上传和多模态消息编辑；
- 多模型并排比较；
- Prompt 模板市场；
- 服务端保存会话、跨设备同步或分享链接；
- 根据价格表预估费用；
- 使用登录 JWT 代替 API Key 执行 Relay 请求；
- 新微服务、新数据表或新的公共 proto 契约。

上述能力只有在首版有真实使用量和明确需求后再单独立项，不能提前扩张 MVP。

### 2.3 成功标准

| 维度 | 验收标准 |
|------|----------|
| 首次调用 | 新建 Key 后可一键进入 Playground，并在不再次复制的情况下完成一次调用 |
| 既有 Key | 用户可手动粘贴原 Key；掩码 Key 不会被误当成可调用凭证 |
| 模型准确性 | 下拉框只展示该 Key 从 Relay `/v1/models` 获得的模型 |
| 流式 | SSE 任意分片边界、CRLF、多 `data:` 行和 `[DONE]` 均可正确处理 |
| 终止 | 点击停止或离开页面会中止网络请求，旧请求不能继续修改新会话 |
| 安全 | 构建产物、浏览器存储、地址栏、请求预览和错误信息中均无完整 Key |
| 错误 | 401、403、额度不足、429、5xx、网络失败、CORS 失败有不同提示和恢复动作 |
| 兼容性 | Chrome 桌面与项目现有 mobile-chrome Playwright 项目通过 |
| 回归 | `npm run test`、`npm run lint`、`npm run build` 和相关 Go 测试通过 |
| 部署 | 标准 Compose 的 `3000 -> 8080` 跨 Origin 场景完成真实流式 smoke |

## 3. 核心架构决策

### 3.1 采用浏览器直连 Relay

调用链：

```text
┌──────────────────────┐      GET /api/status       ┌──────────────────┐
│ Web 控制台 admin-api │ <------------------------> │ admin-api :8000  │
│ 浏览器 Origin :3000  │                            └──────────────────┘
│                      │
│ Playground fetch     │  Authorization: Bearer API_KEY
│                      │ --------------------------> ┌──────────────────┐
└──────────────────────┘  GET /v1/models             │ relay :8080      │
                          POST /v1/chat/completions   │ 鉴权/路由/计费   │
                          <----- JSON / SSE --------- └──────────────────┘
```

原因：

- API Key 只发送给原本就应该接收它的 Relay，不扩大凭证经过的服务范围；
- 不需要在 `admin-api` 新增转发器、流式 flush、超时和断连传播逻辑；
- 调用与真实 SDK/cURL 走同一入口、同一模型权限、路由、计费与限流链路；
- Playground 可以真实验证部署公开的 `ServerAddress`，而不是验证内部代理。

### 3.2 不采用 admin-api Playground 代理

以下方案首版拒绝：

```text
browser -> /api/playground/chat -> admin-api -> relay-gateway
```

它看似能规避 CORS，但会引入：

- API Key 额外经过 `admin-api`，增加日志与内存暴露面；
- admin-api 必须正确代理 SSE、flush、取消、超时和客户端断连；
- 登录会话与 API Key 两套鉴权容易混用；
- 未来可能形成一条行为与公开 Relay 不一致的旁路。

若未来要实现“无需粘贴 API Key”，应另行设计 Relay 的受限短期凭证或“代表用户执行”契约，
而不是把长期 Key 放进请求 body 交给 admin-api。

### 3.3 控制面与执行面客户端必须分离

现有 `web/src/lib/api.ts` 的 `apiClient`：

- `baseURL` 为 `/api`；
- 自动从 `localStorage.token` 添加登录 JWT；
- 特定路径 401 会清除登录态并跳转登录页。

Playground 不能复用它。新增的 Relay 客户端必须：

- 使用原生 `fetch`，以便消费 `ReadableStream`；
- 由调用方显式传入 Relay base URL 和 API Key；
- 不读取登录 JWT；
- Relay 401 只标记 API Key 无效，绝不清除 Web 登录态；
- 不进入全局 React Query cache；
- 不在异常对象中附带 Authorization header。

## 4. 信息架构与页面设计

### 4.1 路由和入口

新增受保护路由：

```text
/playground
```

入口调整：

1. `AppNavigation.userLinks`：在“API 密钥”和“使用记录”之间加入“在线调试”；
2. `routeTitles`：加入 `/playground: 在线调试`；
3. 仪表盘快捷操作：加入“在线调试”，优先级高于“兑换充值码”；
4. API 指南页头：加入“在线调试”按钮；
5. API Key 创建成功弹窗：加入“在在线调试中使用”按钮；
6. 空密钥状态：Playground 提供“前往创建 API 密钥”链接。

导航图标使用 `lucide-react` 已有的 `FlaskConical` 或 `MessagesSquare`，不增加图标依赖。

### 4.2 桌面布局

页面使用控制台现有内容区域，不新增全屏壳层：

```text
┌──────────────────────────────────────────────────────────────────────┐
│ 在线调试   API 地址状态              [清空] [查看使用记录]           │
├──────────────────┬───────────────────────────────┬───────────────────┤
│ 配置             │ 对话                          │ 调试信息          │
│                  │                               │                   │
│ API Key          │ system / user / assistant     │ 概览              │
│ [••••••••]       │                               │ Request ID        │
│ [验证并加载模型] │                               │ TTFT / 总耗时     │
│                  │                               │ Token / finish    │
│ 模型             │                               │                   │
│ [model ▼]        │                               │ 请求 JSON         │
│                  │                               │ 原始响应事件      │
│ 高级参数         │ [输入消息……] [发送/停止]      │                   │
└──────────────────┴───────────────────────────────┴───────────────────┘
```

建议列宽：配置区 `280px`、对话区 `minmax(0, 1fr)`、调试区 `340px`。内容区低于
`1280px` 时，调试信息改为对话区下方的 Tabs；低于 `768px` 时，配置改为顶部折叠区，
输入框固定在页面内容底部，但不得覆盖主导航和系统浏览器安全区域。

### 4.3 页面状态

页面必须显式建模以下状态，而不是依靠多个互相推断的 boolean：

| 状态 | 页面行为 |
|------|----------|
| `needs_key` | 显示 Key 输入和创建链接，不请求模型 |
| `loading_models` | 禁用发送，显示模型加载状态 |
| `ready` | 可编辑消息与参数，可发送 |
| `submitting` | 请求已发出，尚未收到第一个内容片段 |
| `streaming` | 增量渲染，发送按钮变为停止 |
| `completed` | 展示 usage、finish reason、TTFT 和总耗时 |
| `stopped` | 保留已生成内容，明确标注“已由用户停止” |
| `failed` | 保留输入和已收到的部分内容，展示错误与恢复动作 |

模型加载失败和聊天失败应分别保存；重新加载模型不能清空当前对话。

### 4.4 主流程

#### 新 Key 首次调用

1. 用户在 API 密钥页创建 Key；
2. 创建弹窗显示完整 Key；
3. 点击“在在线调试中使用”；
4. Key 通过一次性内存交接进入 Playground；
5. Playground 自动执行 `/v1/models`；
6. 用户选择模型、输入消息并发送；
7. 完成后可跳转使用记录。

#### 既有 Key

1. 用户进入 Playground；
2. 粘贴自己保存的完整 Key；
3. 点击“验证并加载模型”；
4. 加载成功后 Key 输入默认折叠为掩码状态；
5. 换 Key 时先停止在飞请求、清空模型权限缓存，再重新验证。

掩码 Key 仅用于展示，永远不能从 Token 列表自动填入并发送。

## 5. API Key 与隐私设计

### 5.1 生命周期

完整 Key 只能存在于：

- Token 创建接口响应对象；
- 创建成功弹窗受控状态；
- 一次性模块内存交接槽；
- Playground 页面组件状态；
- 调用 Relay 时构造的 Authorization header。

当前 Token 创建使用 TanStack Query mutation；mutation state 可能在组件观察期或 mutation cache
生命周期内保留完整响应。实施时需要把创建请求改为不缓存 secret 的受控异步操作，或在把 Key
复制到弹窗状态后立即 reset 并验证 mutation cache 不再包含原值。更稳妥的 P0 方案是：创建请求
使用局部 pending/error 状态，成功后只把脱敏 Token 写入 `['tokens']` query cache，完整 Key 仅写入
弹窗状态。测试应扫描 query cache 与 mutation cache，确认不存在完整测试 Key。

禁止进入：

- URL path、query、hash；
- React Router `location.state` / 浏览器 history state；
- `localStorage`、`sessionStorage`、IndexedDB、cookie；
- React Query key 或 query cache；
- toast、DOM 文本、错误详情、console；
- 请求 JSON / cURL 预览；
- analytics、Sentry breadcrumb 或服务端日志。

### 5.2 一次性内存交接

新增 `web/src/lib/playground-credential.ts`，保持最小 API：

```ts
setPlaygroundCredential(secret: string): void
takePlaygroundCredential(): string | null
clearPlaygroundCredential(): void
```

语义：

- Token 创建弹窗点击跳转前调用 `set`；
- Playground 首次 mount 调用 `take`，读取后立即清空槽；
- 不通过 Router state，以免 Key 保留在浏览器 session history；
- 页面刷新、标签关闭和重新打开都会丢失 Key，这是预期安全行为；
- 登出时调用 `clear`；
- 单元测试验证 `take` 的一次性语义。

### 5.3 页面展示

- Key 输入使用 `type="password"`；
- 提供按住或点击显示按钮，但失焦后恢复隐藏；
- 验证成功后只显示本地计算的 `前 4 位 + **** + 后 4 位`；
- 请求预览中的 Authorization 固定显示 `Bearer ••••<suffix>`，或完全省略 headers；
- “复制请求”默认只复制 JSON body，不复制带真实 Key 的 cURL；
- 切换 API 地址后必须清除已验证 Key 与模型，防止凭证误发到新地址。

### 5.4 XSS 边界

只驻留内存不能抵御同源 XSS，因此仍需：

- 不使用 `dangerouslySetInnerHTML` 渲染模型输出；
- 首版按纯文本渲染；后续 Markdown 必须使用安全 renderer 和严格 sanitizer；
- 不加载第三方 Playground 脚本；
- 不在页面注入远程字体、统计脚本或 Prompt 插件；
- 保留现有安全响应头，并在上线验收中检查 CSP / `X-Content-Type-Options` 等头。

## 6. Relay 地址与模型发现

### 6.1 地址解析

把 `ApiGuidePage` 内现有地址获取逻辑提取为：

```text
web/src/lib/server-address.ts
```

解析顺序：

1. `GET /api/status` 返回的 `data.server_address`；
2. 构建期 `VITE_RELAY_BASE_URL`；
3. `window.location.origin`，但必须标记为 `fallback` 并在 Playground 显示部署警告。

规范化规则：

- 仅允许 `http:` / `https:`；
- 禁止 URL 内嵌 username/password；
- 移除末尾 `/`，调用处统一拼接 `/v1/...`；
- 生产页面若最终使用 `http:` 且控制台为 `https:`，直接阻止并提示 Mixed Content；
- P0 页面只读展示平台配置地址，不提供任意目标地址编辑，避免诱导用户把 Key 发往恶意域名；
- 开发环境可通过 `VITE_RELAY_BASE_URL=http://127.0.0.1:8080` 配置。

若 `server_address` 未配置且同 Origin 未反代 `/v1/chat/completions`，页面应显示部署配置错误，
不能因为 admin-api 恰好实现了 `/v1/models` 就错误判断 Relay 可用。

### 6.2 模型列表

验证动作请求：

```http
GET {relayBaseUrl}/v1/models
Authorization: Bearer <api-key>
X-Request-ID: playground-<uuid>
```

使用 Relay 返回的 OpenAI 兼容形状：

```json
{
  "object": "list",
  "data": [
    {"id": "model-name", "object": "model", "owned_by": "organization"}
  ]
}
```

规则：

- 只接受非空字符串 `data[].id`；
- 去重并按 `localeCompare` 排序；
- 保留上一次选择仍可用时不改变选择，否则选择第一个；
- 空列表显示“当前密钥没有可用模型”，不允许自由输入绕过；
- 401 清除“已验证”状态但不清空输入，便于用户修正；
- 不使用 `/api/pricing`：价格目录不能表达 Token 的 `AllowedModels` 和实时可路由性；
- 不使用 admin-api 的 `/v1/models` 副本：真实 Relay 已经按用户组和 Key 的模型权限过滤，
  应作为唯一执行面事实源。

模型列表不进入全局 React Query cache，因为 cache key 若包含 Key 会泄漏，若不包含 Key又会串权限。
页面内仅缓存本次验证结果。

## 7. Chat Completions 请求设计

### 7.1 请求体

P0 请求形状：

```json
{
  "model": "selected-model",
  "messages": [
    {"role": "system", "content": "optional system prompt"},
    {"role": "user", "content": "hello"}
  ],
  "stream": true
}
```

P1 可选字段：

```json
{
  "temperature": 0.7,
  "max_tokens": 2048
}
```

参数规则：

- `model` 和至少一条非空 user message 必填；
- system prompt 为空时不发送 system message；
- `temperature` 默认“自动”，即不发送该字段；
- `max_tokens` 默认“自动”，即不发送该字段；
- 只有用户显式设置时才发送可选参数，避免对不接受固定采样参数的模型造成兼容问题；
- `temperature` 显式值限制为 `0..2`；
- `max_tokens` 显式值限制为正整数，UI 上限先设 `128000`，最终仍由上游校验；
- 单条输入在浏览器端限制为 100,000 字符，并显示计数；这不是 Token 估算；
- 最多保留 100 条消息，超过时阻止继续发送并提示清理会话；不在前端静默截断上下文。

### 7.2 请求快照与多轮语义

点击发送时必须建立不可变请求快照：

```ts
interface PlaygroundRequestSnapshot {
  requestId: string;
  endpoint: string;
  model: string;
  messages: PlaygroundAPIMessage[];
  stream: boolean;
  temperature?: number;
  maxTokens?: number;
  startedAt: number;
}
```

在飞请求期间：

- 配置区锁定，防止页面显示参数和实际请求不一致；
- 用户输入草稿可保留，但不能再次发送；
- assistant 占位消息记录 `requestId`；
- 只有当前 active request 可以更新该占位消息；
- 完成、失败或停止后再解锁配置。

多轮请求发送当前页面全部 system/user/assistant 文本消息。工具调用和 reasoning 内容首版不回填到
下一轮，页面遇到这些字段时在调试区保留原始事件并提示“当前 UI 未执行工具调用”。

### 7.3 请求头

```http
Authorization: Bearer <api-key>
Content-Type: application/json
Accept: text/event-stream, application/json
X-Request-ID: playground-<crypto.randomUUID()>
```

不增加自定义 `X-Playground` header，避免扩大 CORS 与公共协议表面。`X-Request-ID` 已属于
现有中间件允许头，可用于页面与 Relay 边缘日志关联。当前各 handler 内部还会生成自己的执行 /
账务 request ID，因此首版不能承诺用浏览器 Request ID 精确跳转到某条 billing ledger；如需统一，
应单独梳理执行幂等与信任边界，不能直接用任意客户端 header 充当账务去重键。

## 8. 流式客户端设计

### 8.1 文件边界

新增：

```text
web/src/lib/relay-playground.ts       fetch、模型请求、聊天请求、错误归一
web/src/lib/sse.ts                    与 React 无关的增量 SSE parser
web/src/lib/playground-credential.ts  一次性 Key 交接
web/src/lib/server-address.ts         Relay 地址解析与规范化
```

`PlaygroundPage.tsx` 不直接实现字节流解析；它只消费 transport 暴露的回调或 async iterator。

### 8.2 SSE parser 契约

parser 输入为任意 `Uint8Array` 分片，不能假设：

- 一次 `read()` 对应一个 SSE event；
- JSON 不会跨分片；
- 每个 event 只有一个 `data:` 行；
- 换行一定是 `\n` 而不是 `\r\n`；
- `[DONE]` 后连接会立刻关闭。

算法：

1. 使用单个 `TextDecoder('utf-8')` 并传 `{stream: true}`；
2. 将文本追加到有界 buffer；
3. 按空行识别完整 event，兼容 `\n\n` 和 `\r\n\r\n`；
4. 忽略 `:` comment、空行以及无关字段；
5. 同一 event 的多个 `data:` 行按 SSE 规范用 `\n` 连接；
6. `data: [DONE]` 标记正常结束；
7. 其他 data 尝试 JSON parse；
8. 提取 `choices[].delta.content`、`reasoning_content`、`finish_reason` 和 `usage`；
9. 不认识的合法 JSON 仍进入有界 raw event 列表；
10. 连接关闭时 flush `TextDecoder` 和剩余 buffer；若没有 `[DONE]`，根据是否已有内容标记
    “流提前结束”，不能伪装成完整成功。

容错策略：

- 单个无法解析的 event 记录 protocol warning；
- 连续或累计 3 个无法解析事件后终止，防止把 HTML 错误页当 SSE 无限读取；
- 非 `text/event-stream` 响应先按 JSON / text 错误处理；
- raw event 调试数据最多保留 2 MiB 或 1,000 条，达到上限后标记 truncated；
- assistant 最终文本单次会话最多保留 4 MiB，超过后停止读取并提示响应过大。

### 8.3 非流式

当 `stream=false`：

- 要求响应为 JSON；
- 提取 `choices[0].message.content`、`finish_reason`、`usage`；
- 不假设 `usage` 一定存在；缺失时显示“上游未返回”，不能显示为 0；
- 如果响应是 2xx 但形状不符合契约，保存原始 JSON并显示协议错误。

### 8.4 停止与竞态

每次发送创建独立 `AbortController`。以下动作必须调用 `abort()`：

- 点击“停止生成”；
- 更换 Key；
- 切换 Relay 地址；
- 清空会话；
- 页面卸载或退出登录。

状态更新同时检查递增的 `requestSequence` / `requestId`。即使旧 promise 在 abort 后才返回，
也不能写入新请求的 assistant 消息或覆盖新的错误状态。

用户主动 abort：

- 状态为 `stopped`，不是 `failed`；
- 保留已经收到的文本；
- 不弹全局错误 toast；
- usage 若尚未收到则显示未知；
- 页面提示服务端可能已经产生部分用量，最终以“使用记录”为准。

### 8.5 时间指标

浏览器只计算交互指标，不替代服务端账务：

- `startedAt`：调用 `fetch` 前的 `performance.now()`；
- `headersAt`：收到 response headers；
- `firstContentAt`：收到第一段非空 assistant content；
- `completedAt`：正常 `[DONE]` / JSON 完成；
- TTFT = `firstContentAt - startedAt`；
- 总耗时 = `completedAt - startedAt`。

没有内容但正常结束时 TTFT 显示 `—`。停止和失败时总耗时显示“中止前 / 失败前耗时”。

## 9. 错误模型与恢复动作

新增本地错误类型：

```ts
type PlaygroundErrorKind =
  | 'invalid_key'
  | 'forbidden_model'
  | 'insufficient_quota'
  | 'rate_limited'
  | 'upstream_unavailable'
  | 'invalid_request'
  | 'cors_or_network'
  | 'mixed_content'
  | 'protocol_error'
  | 'aborted'
  | 'unknown';
```

映射优先级：HTTP status -> OpenAI 兼容 `error.type/error.code/error.message` -> 安全回退文案。

| 条件 | 用户提示 | 恢复动作 |
|------|----------|----------|
| 401 | API Key 无效、已删除或已过期 | 重新输入 / 创建 Key |
| 403 | Key 或用户无权调用该模型 | 重新加载模型 |
| 400/422 | 请求参数或模型不兼容 | 展开响应详情、调整参数 |
| 余额 / quota 错误 | 余额或订阅额度不足 | 前往充值 / 订阅 |
| 429 | 请求过于频繁或上游限流 | 保留输入，稍后重试 |
| 502/503/504 | 当前路由或上游暂不可用 | 重试或更换模型 |
| `TypeError: Failed to fetch` | 网络、CORS、DNS 或 TLS 问题 | 显示地址与部署检查说明 |
| HTTPS -> HTTP | 浏览器阻止 Mixed Content | 要求使用 HTTPS Relay |
| 2xx 非 JSON/SSE | 返回协议异常 | 显示 Content-Type 与截断预览 |

安全要求：

- 对用户展示服务端安全化后的 message，但最大 1,000 字符；
- 不展示请求 header、Key、上游凭证或完整 HTML 错误页；
- 未知 body 最多预览 4 KiB 并进行纯文本渲染；
- Relay 401 不调用 `clearUserSession()`；
- 错误详情内展示 Request ID，方便与日志关联。

## 10. 前端组件与状态结构

### 10.1 建议文件

```text
web/src/pages/PlaygroundPage.tsx
web/src/pages/PlaygroundPage.test.tsx
web/src/components/playground/PlaygroundCredentials.tsx
web/src/components/playground/PlaygroundSettings.tsx
web/src/components/playground/PlaygroundConversation.tsx
web/src/components/playground/PlaygroundComposer.tsx
web/src/components/playground/PlaygroundInspector.tsx
web/src/components/playground/PlaygroundError.tsx
web/src/lib/relay-playground.ts
web/src/lib/relay-playground.test.ts
web/src/lib/sse.ts
web/src/lib/sse.test.ts
web/src/lib/playground-credential.ts
web/src/lib/playground-credential.test.ts
web/src/lib/server-address.ts
web/src/lib/server-address.test.ts
```

拆分原则：

- 页面拥有 orchestration state；
- 组件只负责一个可见区域；
- transport 和 parser 为纯 TypeScript，不依赖 React；
- 不为首版引入新的全局状态库；
- 不把短小的单次 UI 抽象成通用表单框架。

### 10.2 类型

不要直接依赖 `web/src/types/api.ts` 的生成类型完成流式实现。当前 OpenAPI 中的
Chat Completions 来自简化 proto，不能表达真实 raw HTTP 路由的完整 snake_case 请求、SSE delta
和 provider 扩展。Playground 在 `relay-playground.ts` 内声明最小兼容类型：

```ts
type PlaygroundRole = 'system' | 'user' | 'assistant';

interface PlaygroundMessage {
  id: string;
  role: PlaygroundRole;
  content: string;
  status?: 'pending' | 'streaming' | 'completed' | 'stopped' | 'failed';
  requestId?: string;
}

interface PlaygroundUsage {
  promptTokens?: number;
  completionTokens?: number;
  totalTokens?: number;
  cachedTokens?: number;
}
```

类型只覆盖页面真实消费的字段；原始未知字段保留为 `unknown`，不建立庞大 provider 类型体系。

### 10.3 状态管理

推荐页面使用 `useReducer`，因为发送、流式追加、停止、失败和重置存在明确状态转换。至少包含：

- credential 与 credential status；
- relay address 与 address source；
- models、selected model、model loading error；
- system prompt、参数和 stream 开关；
- messages 与 composer draft；
- active request ID、AbortController ref；
- last request snapshot、raw events、usage、timing、finish reason；
- transport error 与 protocol warnings。

AbortController、TextDecoder 和 Key 不放入 reducer action 日志或开发工具可序列化状态；
AbortController 用 `useRef`，Key 使用独立 `useState`，reducer 只保存掩码与验证状态。

### 10.4 可访问性与键盘

- 所有输入有可见 label；
- 流式正文容器使用节制的 `aria-live="polite"`，避免每个 token 重复朗读；
- `Ctrl/Cmd + Enter` 发送，`Escape` 仅在生成中停止；
- 发送、停止、显示 Key 等图标按钮均有中文 `aria-label`；
- focus 不因每个流片段重新定位；
- 错误摘要使用 `role="alert"`；
- 颜色不是状态的唯一表达方式；
- 移动端键盘弹出时输入区仍可见。

## 11. Relay CORS 与服务端改动

### 11.1 当前缺口

生产启动使用：

```text
cmd/relay-gateway/relay_helpers.go
  -> httpServer.RegisterRoutes(srv)
  -> internal/server/routes.go
```

实际 `routeMiddleware` 当前主要包含订阅额度、幂等、审计和 metrics。现有
`EnhancedHTTPServer.RegisterRoutesWithSecurity` 虽调用 `SimpleCORS()`，但未被生产 Wire 使用。
实施时禁止再注册第二套 `/v1/chat/completions` 路由；应把 CORS 接到当前唯一生产路由链。

### 11.2 接线方案

在 `cmd/relay-gateway/wire.go` 构造 route middleware 时，将以下通用中间件放在最外层：

1. Relay CORS；
2. Security headers；
3. Request ID；
4. 现有订阅额度 / 幂等 / 审计 / metrics 中间件。

因为 `HTTPServer.handleFunc` 逆序包装 middleware，数组中的 CORS 必须位于前面，使 OPTIONS
预检在鉴权、额度和 handler 方法校验之前返回。

同时更新 `wire_gen.go` 必须通过 Wire 生成流程完成，不能手改生成文件。

### 11.3 CORS 策略

Relay 使用 Bearer API Key，不需要 cookie：

```text
AllowedOrigins: CORS_ALLOWED_ORIGINS 的精确列表
AllowedMethods: GET, POST, OPTIONS
AllowedHeaders: Authorization, Content-Type, X-Request-ID
ExposedHeaders: Content-Type, X-Request-ID, X-RateLimit-Limit,
                X-RateLimit-Remaining, X-RateLimit-Reset
AllowCredentials: false
MaxAge: 86400
```

生产禁止 `*`。配置示例：

```dotenv
CORS_ALLOWED_ORIGINS=https://console.example.com
```

本地双端口开发：

```dotenv
CORS_ALLOWED_ORIGINS=http://127.0.0.1:5173,http://localhost:5173,http://127.0.0.1:3000
VITE_RELAY_BASE_URL=http://127.0.0.1:8080
```

配置列表要 trim、忽略空项并大小写不敏感匹配；不支持任意后缀通配，避免
`*.example.com` 被攻击者可控子域滥用。

### 11.4 CORS 测试

Go 测试至少覆盖：

1. 允许 Origin 的 OPTIONS 返回 204 和准确的 ACAO；
2. `Access-Control-Allow-Headers` 包含 Authorization；
3. `Allow-Credentials` 不存在；
4. 不允许 Origin 不返回 ACAO，且预检不能进入业务 handler；
5. 空配置 fail closed；
6. 多 Origin trim 正确；
7. 实际 `/v1/models` 和 `/v1/chat/completions` 注册链经过 CORS，而不只测试孤立 middleware；
8. 非浏览器、无 Origin 的 cURL/SDK 请求行为不变。

### 11.5 不需要的后端改动

P0 不新增：

- `/api/playground/*`；
- Playground 专属 Token；
- 数据库表或迁移；
- gRPC / proto 字段；
- 新计费分支；
- 新路由策略；
- 模型目录副本。

Playground 请求应完整走现有 Relay 计划、渠道选择、订阅/钱包结算和 usage log。

## 12. 观测与调试

### 12.1 页面展示

Inspector 展示：

- Request ID；
- endpoint 与 model；
- stream 状态；
- HTTP status；
- TTFT 与总耗时；
- finish reason；
- prompt / completion / total / cached tokens（仅服务端有返回时）；
- 脱敏后的请求 JSON；
- 有界原始 SSE event / JSON response；
- protocol warning。

### 12.2 服务端观测

不新增高基数 `playground=true` 指标标签。现有 Relay endpoint、stream、status、result、duration、
quota outcome、failover 指标和 usage log 已覆盖执行质量。响应头 Request ID 用于关联 Relay 边缘
请求日志；账务记录仍使用现有 handler / executor 生成的内部 Request ID。

浏览器指标只是用户体验信息，不能用于计费或对账。最终扣费、Token 数和请求归属以 billing
ledger 与使用记录为准。

### 12.3 日志红线

- 前端不 `console.log` request options、Key 或完整 fetch error config；
- Relay CORS debug 日志只记录 Origin、method、path 和 allowed，不记录 Authorization；
- Request ID 可记录；
- 原始 prompt / completion 不因 Playground 新增服务端日志；
- E2E fixture 使用明显的假 Key，测试 trace 中不得出现生产 Key。

## 13. 测试方案

### 13.1 纯单元测试

`sse.test.ts`：

- 一个 event 一个 chunk；
- JSON 横跨多个 chunk；
- 一个 chunk 包含多个 event；
- UTF-8 中文字符跨字节分片；
- CRLF；
- 多 `data:` 行；
- comment / event / id 字段；
- `[DONE]`；
- 无 `[DONE]` 提前关闭；
- 单个 malformed event 与累计阈值；
- raw event 和文本内存上限。

`relay-playground.test.ts`：

- 模型列表规范化、去重和空列表；
- stream / non-stream 请求体；
- 未设置参数时不发送 `temperature` / `max_tokens`；
- 401/403/429/5xx 和 OpenAI error body 映射；
- JSON 错误页、HTML 错误页、错误 Content-Type；
- abort 映射为 stopped；
- Authorization 不出现在错误字符串。

`server-address.test.ts`：

- status 配置地址优先；
- build env 和 fallback；
- trailing slash；
- 非 http(s)、内嵌凭证和 Mixed Content；
- 地址变化使 credential verification 失效。

`playground-credential.test.ts`：

- `take` 只返回一次；
- clear；
- 空白 Key 拒绝；
- 不与 Web 登录 token 混用。

### 13.2 页面测试

使用 Vitest、Testing Library 和 MSW：

1. 无 Key 时不请求 `/v1/models`；
2. 粘贴 Key 后只在点击验证时发请求；
3. 模型成功加载并选中；
4. 401 不清除 `localStorage.token`，且页面仍保持登录；
5. 发送后追加 user 和 assistant，占位转为 streaming/completed；
6. stop 调用 abort 并保留部分文本；
7. 清空会话前停止在飞请求；
8. 旧请求晚到事件不会污染新请求；
9. 请求预览无完整 Key；
10. 新 Key 一次性交接后自动加载模型；
11. mobile inspector 折叠和关键按钮可访问；
12. 键盘发送与停止行为。

若 JSDOM/MSW 对真实流分片支持不足，transport 用注入式 `fetch` 或 async iterable 测试，
不要把 SSE parser 逻辑 mock 掉。

### 13.3 Playwright E2E

在 `web/e2e/` 新增 `playground.spec.ts`：

- mock `/api/status` 返回 Relay URL；
- 启动独立的本地 Relay fixture server，提供跨 Origin `/v1/models`；
- fixture server 的 `/v1/chat/completions` 至少分 3 次 flush SSE chunk；
- 验证桌面和 mobile-chrome 主流程；
- 验证错误恢复与停止；
- 验证页面刷新后 Key 消失；
- 验证 URL 与 `localStorage/sessionStorage` 不包含假 Key；
- 验证 Relay 401 不跳转登录。

Playwright 的 `page.route().fulfill()` 只能一次性 fulfill body，不能证明增量渲染和停止传播。
因此新增 `web/e2e/playground-relay-server.ts`（或等价最小 Node fixture），并在
`playwright.config.ts` 的 `webServer` 数组中与 Vite 一起启动。fixture 只绑定 `127.0.0.1`、使用固定
假 Key、返回精确测试 Origin CORS；退出 Playwright 时自动终止，不引入生产依赖。

### 13.4 Go 与集成测试

- `platform/middleware/cors_test.go` 扩展无 cookie 的 Relay 策略；
- 为生产路由链增加 OPTIONS 集成测试；
- `make api` / `make config` 不应产生变更；
- 标准 Docker Compose 启动后：
  - 从允许 Origin 预检 `/v1/models`、`/v1/chat/completions`；
  - 用测试用户 Key 完成一次流式请求；
  - usage log 只落一次；
  - stop 场景账务与现有 Relay 语义一致；
  - 不允许 Origin 的预检无法发起 POST。

## 14. 文件级实施清单

### 14.1 Web

| 文件 | 改动 |
|------|------|
| `web/src/router.tsx` | lazy load `PlaygroundPage`，注册 `/playground` |
| `web/src/components/AppNavigation.tsx` | 核心导航与标题加入在线调试 |
| `web/src/pages/DashboardPage.tsx` | 增加在线调试快捷操作 |
| `web/src/pages/TokensPage.tsx` | 创建成功后增加一次性 Key 交接入口 |
| `web/src/pages/ApiGuidePage.tsx` | 复用 server address helper，增加 Playground CTA |
| `web/src/pages/PlaygroundPage.tsx` | 页面 orchestration 与状态机 |
| `web/src/components/playground/*` | 配置、对话、输入、Inspector 和错误组件 |
| `web/src/lib/relay-playground.ts` | 独立 Relay fetch client |
| `web/src/lib/sse.ts` | 纯 SSE parser |
| `web/src/lib/playground-credential.ts` | 一次性内存凭证交接 |
| `web/src/lib/server-address.ts` | 地址发现、规范化和安全校验 |
| 对应 `*.test.ts(x)` | 单元与页面测试 |
| `web/e2e/playground.spec.ts` | 桌面和移动端主流程 |
| `web/e2e/playground-relay-server.ts` | 可真实 flush SSE 的本地跨 Origin Relay fixture |
| `web/e2e/fixtures.ts` | 补充 Playground mock；不得包含真实 Key |
| `web/playwright.config.ts` | 同时启动 Vite 与本地 Relay fixture |
| `web/README.md` | 增加本地 Relay 地址与 CORS 开发配置 |

### 14.2 Relay / 配置

| 文件 | 改动 |
|------|------|
| `cmd/relay-gateway/wire.go` | 将 Relay CORS、security headers、request ID 接入唯一生产 route chain |
| `cmd/relay-gateway/wire_gen.go` | 通过 Wire 重新生成 |
| `platform/middleware/cors.go` | 若需要，增加无 credentials 的 Relay 配置构造；保持通用 CORS 行为可测 |
| `platform/middleware/cors_test.go` | 精确 Origin、预检、headers、fail-closed 测试 |
| `internal/server/*_test.go` | 实际路由链的浏览器预检集成测试 |
| `deployments/docker-compose/.env.example` | 文档化精确 `CORS_ALLOWED_ORIGINS` |
| `deployments/docker-compose/.env.lite.example` | 同步 Lite 配置说明 |
| `deployments/docker-compose/.env.postgres.example` | 同步 PostgreSQL 配置说明 |
| `deployments/docker-compose/docker-compose*.yml` | 确认透传现有 CORS 环境项；不新增 secret |
| `docs/deployment.md` | 增加控制台与 Relay Origin 配置及验证命令 |

只修改仓库跟踪的三个 example 文件，不覆盖用户或生产 `.env`。同时在部署文档说明管理员需在
系统设置中配置 `ServerAddress` 为浏览器可访问的 Relay 公网地址；它不能填写容器内部地址
`http://relay-gateway:8080`。

## 15. 分阶段交付与提交边界

### Phase 0：契约测试先行

交付：

- SSE parser 测试向量；
- Relay error normalization 测试；
- Key 一次性交接测试；
- CORS 生产路由集成测试先失败。

验收：测试明确锁定分片、abort、Key 不持久化和预检语义。

### Phase 1：Relay 浏览器边界

交付：

- 实际 route chain 启用无 cookie CORS；
- Request ID 和安全 headers 接线；
- Compose example 与 deployment 文档；
- Go 测试通过。

验收：从控制台 Origin 可直接调用 Relay `/v1/models` 和流式 Chat；不允许 Origin 被浏览器阻止；
cURL/SDK 无回归。

建议提交：

```text
feat(relay): enable browser access for playground

The production relay route chain did not install the existing CORS
middleware, so browser clients could not call the public endpoints even
when CORS_ALLOWED_ORIGINS was configured. Wire a credential-free CORS
policy and request diagnostics into the actual route chain.
```

### Phase 2：无 UI 的前端执行内核

交付：

- `server-address.ts`；
- `playground-credential.ts`；
- `sse.ts`；
- `relay-playground.ts`；
- 全部纯单元测试。

验收：可以用测试 harness 加载模型、流式拼接、停止并归一错误；任何失败对象不包含 Key。

### Phase 3：页面与站内闭环

交付：

- Playground 页面和子组件；
- 路由、导航、仪表盘、指南和 Key 创建弹窗入口；
- 页面测试；
- 响应式与可访问性验收。

验收：新 Key 和既有 Key 两条主路径都能完成调用。

建议提交：

```text
feat(web): add chat completions playground

Users had to leave the console to verify a newly created API key, which
left the first-call flow incomplete and made model, quota, and routing
errors hard to diagnose. Add a memory-only, streaming playground backed
by the public relay endpoints.
```

### Phase 4：E2E、上线与观察

交付：

- Playwright desktop/mobile；
- Docker 双 Origin smoke；
- 部署与回滚检查；
- 线上 24 小时基础观察。

验收：见 §16。

## 16. 发布、验证与回滚

### 16.1 部署顺序

必须先后端、后前端：

1. 配置生产控制台精确 Origin；
2. 构建并部署 `relay-gateway`；
3. 使用 OPTIONS 和测试 Key 验证 CORS / models / stream；
4. 构建 `web/dist`；
5. 按仓库标准流程上传 `/opt/web/dist`；
6. 无需重启 admin-api（静态挂载实时生效）；
7. 桌面和移动端各完成一次真实 smoke；
8. 检查 usage log、账务和 Relay metrics。

若先发布 Web，用户会看到 Playground 但被 CORS 阻止，因此不允许交换顺序。

### 16.2 预检验证

示例命令中的域名必须替换为生产实际值，Key 不写入 shell history 或文档：

```bash
curl -i -X OPTIONS "$RELAY_URL/v1/chat/completions" \
  -H "Origin: $CONSOLE_ORIGIN" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: authorization,content-type,x-request-id"
```

确认：

- HTTP 204；
- ACAO 等于精确控制台 Origin，不是 `*`；
- Allow-Headers 包含 Authorization；
- 没有 `Access-Control-Allow-Credentials: true`。

### 16.3 功能验收清单

- [ ] 导航、直接访问和刷新 `/playground` 正常；
- [ ] 无 Key 时没有 Relay 请求；
- [ ] 新建 Key 可一次性跳转并自动加载模型；
- [ ] 页面刷新后完整 Key 消失；
- [ ] 模型列表符合 Key 权限；
- [ ] 流式中文、多段输出正确，无乱码和重复；
- [ ] 停止后无后续 UI 写入；
- [ ] 切换 Key 后旧模型和旧请求状态失效；
- [ ] 401 不退出控制台登录；
- [ ] 429 / 5xx / 网络 / CORS 文案可区分；
- [ ] 请求和响应预览没有完整 Key；
- [ ] usage 缺失显示未知，不显示假 0；
- [ ] 使用记录出现一次对应消费；
- [ ] dark mode、桌面和 mobile-chrome 可用；
- [ ] 前端 test/lint/build 和相关 Go tests 全绿。

### 16.4 上线观察

发布后 24 小时检查：

- `/v1/models` 和 `/v1/chat/completions` 401 / 403 / 429 / 5xx 分布没有异常跃升；
- stream_error 与 quota_error 没有新增稳定模式；
- 没有 OPTIONS 请求进入账务、限流或 usage log；
- 浏览器报告中没有集中出现 CORS / Mixed Content；
- admin-api 登录态 401 跳转行为无回归；
- Relay P95 无因中间件接线产生可测回归。

### 16.5 回滚

本功能无数据库和公共契约变更，可独立回滚：

1. 回滚 `web/dist` 到部署前备份，立即移除入口和页面；
2. 若 CORS 配置错误，先移除错误 Origin 或回滚 Relay；
3. CORS 中间件为执行前边界，不改变无 Origin 的 cURL/SDK 请求，可在 Web 回滚后保留；
4. 回滚不删除 Token、不改 ledger、不需要数据库操作；
5. 若发现 Key 泄露风险，立即下线 Web、撤销受影响 Key，并按安全事件流程检查前端日志与 trace。

## 17. 风险与控制

| 风险 | 影响 | 控制 |
|------|------|------|
| CORS 只存在于未使用的 Enhanced server | Web 完全不可调用 | 接到唯一生产 route chain，并做路由级集成测试 |
| `CORS_ALLOWED_ORIGINS=*` | 任意站点可诱导用户发送 Key | 生产精确 Origin、无 credentials、发布前 curl 验证 |
| Key 被写入浏览器存储 | 长期凭证泄露 | 一次性内存槽、刷新丢失、测试扫描 storage |
| 复用 `apiClient` | JWT/API Key 混用，401 导致退出 | 独立 fetch client，不使用全局 interceptor |
| 使用价格目录作为模型列表 | 展示不可用或无权限模型 | 只使用带 Key 的 Relay `/v1/models` |
| SSE 分片假设错误 | 中文乱码、漏字、重复或卡死 | 纯 parser、跨字节/跨 event 测试向量 |
| abort 后旧回调继续写入 | 会话串线 | AbortController + request ID/sequence 双重门禁 |
| 默认发送采样参数 | 部分模型拒绝请求 | 默认“自动/不发送”，显式设置才加入 body |
| 2xx 返回 HTML/SPA | 被误判为成功 | 校验 Content-Type 和响应契约 |
| 流式调试数据无界增长 | 浏览器内存膨胀 | 文本、event、buffer 三层硬上限 |
| Markdown XSS | Key 和会话内容泄露 | P0 纯文本；后续安全 renderer 单独评审 |
| UI stop 被理解为不计费 | 用户争议 | 明示服务端可能已产生用量，以 ledger 为准 |

## 18. 后续触发项

只有满足触发条件才启动后续能力：

| 能力 | 触发条件 |
|------|----------|
| 无 Key 登录态调用 | 粘贴 Key 成为明确的主要流失点，且能定义短期、可撤销、可计费的凭证契约 |
| Responses API | Chat Playground 稳定，且真实用户需要 reasoning/tools/多模态输入 |
| 工具调用 UI | 有明确工具执行沙箱、安全模型和审计需求 |
| Markdown | 纯文本体验不足，且 sanitizer/CSP 验收就绪 |
| 会话本地草稿 | 用户明确需要刷新保留，并完成敏感 prompt 存储告知 |
| 服务端会话历史 | 有跨设备需求，且数据保留、删除、隐私和容量策略完成评审 |
| 多模型比较 | 有真实选型场景，且能接受一次操作产生多次计费 |
| Playground 使用分析 | 产品需要转化漏斗，并能以低基数、无 prompt/Key 的方式采集 |

## 19. 最终决策清单

- [x] 首版协议：OpenAI Chat Completions；
- [x] 执行路径：浏览器直连公开 Relay；
- [x] 鉴权：用户 API Key，不复用登录 JWT；
- [x] Key 保存：当前页面内存，一次性跨页交接，不持久化；
- [x] 模型事实源：带 Key 的 Relay `/v1/models`；
- [x] 默认响应：流式；
- [x] 参数默认：自动，不发送 temperature / max_tokens；
- [x] CORS：接入实际生产路由、精确 Origin、无 cookie credentials；
- [x] UI 渲染：首版纯文本；
- [x] 账务：完全复用现有 Relay 链路；
- [x] 数据库 / proto：无变更；
- [x] 发布顺序：Relay 先于 Web；
- [x] 回滚：静态 Web 与 Relay 边界可分别回滚。

## 20. 首版实施记录（2026-08-26）

已完成首版交付：

- Relay 生产 route chain 已接入无 cookie credentials 的 CORS、Security Headers 和 Request ID；Compose 环境模板补充精确 `CORS_ALLOWED_ORIGINS` 示例。
- Web 新增 `/playground`，支持一次性 Token 交接、Relay 地址解析、Key 验证与模型加载、Chat Completions 流式/非流式请求、停止/清空、错误分类、Request ID、TTFT/耗时/usage 和原始 SSE 事件查看。
- 导航、仪表盘和 Token 创建完成页均提供 Playground 入口；创建接口改为直接请求，避免完整新 Key 进入 React Query mutation cache。
- 新增 SSE、Relay client、地址校验、一次性交接和页面集成测试；已通过 `npm test`（40 个测试文件、138 个测试）、`npm run lint`、`npm run build`、`go test ./platform/middleware ./cmd/relay-gateway`。

上线前仍需按 §16 使用真实部署 Origin、浏览器和真实 API Key 完成手工/Playwright 验收，尤其是 mixed-content、移动端布局和实际账务记录。
