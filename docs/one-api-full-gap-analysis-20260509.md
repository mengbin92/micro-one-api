# one-api 完整功能对比与补齐方案

> 对比对象：同级目录 `../one-api`。分析日期：2026-05-09。

## 结论

当前项目没有完全实现 `one-api` 的全部产品功能。

已有实现覆盖了核心微服务骨架、OpenAI 兼容主链路、鉴权、渠道选择、账务预扣/提交、管理服务 gRPC 边界、配置/日志/监控/通知等基础能力。但与 `one-api` 的完整单体产品相比，仍缺少完整 Web 管理端、全部管理 HTTP API、全部登录注册/OAuth 体验、令牌自助管理、用户自助面板、公告/关于/首页内容接口、部分渠道专用适配器能力，以及若干 One API 原生响应格式。

## 对比范围

本次按 `../one-api` 的以下入口对比：

1. `router/api.go`：管理端、用户端、OAuth、日志、兑换码、渠道、系统配置 API。
2. `router/relay.go`：OpenAI 兼容转发、模型列表、模型详情、代理转发等 relay API。
3. `README.md`：产品功能清单，包括多模型、渠道、令牌、兑换码、邀请、公告、自定义主题、Turnstile、OAuth、多机部署等。

## 已基本覆盖

| one-api 功能 | 当前项目状态 |
| --- | --- |
| OpenAI Chat Completions | 已实现，含流式与非流式 |
| `/v1/completions`、embeddings、images、audio、moderations 原样转发 | 已实现 |
| `/v1/oneapi/proxy/:channelid/*target` | 已实现 |
| `/v1/models` | 已实现 |
| 用户鉴权与模型白名单 | 已实现 |
| 渠道选择、优先级、分组模型 | 已实现 |
| 账务预扣、提交、释放、流水、兑换码 | 已实现 |
| 管理服务 gRPC 边界 | 已实现 |
| 系统配置、日志、监控、通知服务 | 已实现 |
| Prometheus 指标、健康检查、Redis 限流、注册发现 | 已实现 |

## 本分支已补齐

| 缺口 | 补齐内容 |
| --- | --- |
| `/v1/models/:model` 缺失 | relay-gateway 新增 OpenAI 兼容模型详情响应 |
| `/api/status` 缺失 | relay-gateway 与 admin-api 新增 One API 风格状态响应 |
| admin-api HTTP 只支持查询 | `/v1/users`、`/v1/channels`、`/v1/redeem-codes` 增加 POST/PUT/DELETE 分发 |
| 路径型管理操作缺失 | 新增 `/v1/users/{id}`、`/v1/channels/{id}`、`/v1/channels/{id}/status`、`/v1/redeem-codes/{code}` |
| One API 管理充值兼容入口缺失 | 新增 `/api/topup`，同时保留 `/v1/topup` |
| 路由行为缺少测试 | 新增 admin HTTP 测试和 relay 兼容端点测试 |
| 用户邀请缺失 | 新增 aff code 设计和实现：用户邀请码、注册绑定邀请关系、`/api/user/aff`、可配置邀请奖励 |
| 用户账务自助入口缺失 | 新增 `/api/user/dashboard` 账户快照和 `/api/user/topup` 兑换码充值兼容入口 |
| 用户自助资料管理缺失 | 新增 `PUT /api/user/self` 更新用户名、展示名、密码，以及 `DELETE /api/user/self` 删除当前用户 |
| 用户自助日志缺失 | log-service 新增 `/api/log/self`、`/api/log/self/search`、`/api/log/self/stat`，按 Bearer 用户过滤日志 |
| 公告/内容兼容入口缺失 | config-service 已暴露 `/api/notice`、`/api/about`、`/api/home_page_content` 基础兼容入口 |
| 分组查询兼容入口缺失 | relay-gateway 已暴露 `/api/group` 基础兼容入口 |
| 渠道测试与余额兼容入口缺失 | admin-api 已暴露 `/api/channel/test`、`/api/channel/test/{id}`、`/api/channel/update_balance`、`/api/channel/update_balance/{id}` 基础兼容入口 |

## 仍未完全实现

| 分类 | one-api 能力 | 当前缺口 |
| --- | --- | --- |
| Web 前端 | `web/default`、`web/air`、`web/berry` 三套主题与页面 | 当前仓库没有完整前端应用 |
| 用户自助 API | `/api/user/*` 邮箱绑定等完整体验 | 已补注册、登录、self 查询/更新/删除、dashboard 账户快照、token、aff、topup、available_models；仍缺邮箱绑定等完整体验 |
| 用户 dashboard 图表 | One API `dashboard` 返回按天/模型日志统计 | 当前先返回 billing account snapshot，仍缺 usage log 的按天/模型聚合图表 |
| Token 管理 | `/api/token/*` 列表、搜索、创建、更新、删除、状态 | 缺完整 token 管理 HTTP API 和数据模型对齐 |
| OAuth/SSO | GitHub、OIDC、飞书、微信、绑定邮箱 | 当前只具备部分 OAuth 基础能力，未对齐 One API 的完整路由和前端流程 |
| 日志 API | `/api/log/*` 完整日志字段、管理日志搜索、删除历史日志 | 已补用户 self 查询/搜索/统计；仍缺 One API 完整日志字段和部分管理日志操作 |
| 公告/内容 | `/api/notice`、`/api/about`、`/api/home_page_content` | 已有基础兼容入口；仍缺完整前端内容管理体验 |
| 渠道测试与余额 | `/api/channel/test`、`/update_balance` | 已有基础兼容入口；仍缺真实渠道余额查询实现 |
| 分组管理 | `/api/group` | 已有基础查询入口；仍缺完整分组配置管理 API |
| 全量 provider 适配 | one-api 支持数十种渠道专用适配器 | 当前以 OpenAI-compatible 原样转发为主，仅补了 Anthropic/Gemini 等有限适配 |
| 图片编辑/变体、文件、微调、Assistants、Threads | one-api 中多数也返回 NotImplemented | 当前也未实现，保持未支持状态 |
| Cloudflare Turnstile | 注册/重置等风控 | 未完整实现 |
| 自定义主题和静态资源服务 | 前端主题切换 | 未实现 |

## 后续实施方案

### Phase 1：HTTP 兼容层补齐

目标：让已有微服务能力通过 One API 风格 HTTP 路由可用。

1. 补邮箱绑定等 `/api/user/*` 和 `/api/oauth/*` 剩余用户体验路由。
2. 补 dashboard 按天/模型 usage log 聚合图表。
3. 补 One API 完整日志字段、历史日志删除和管理搜索。
4. 补内容、分组、渠道余额的完整管理体验。

### Phase 2：管理端能力对齐

目标：覆盖 One API 管理后台常用操作。

1. 渠道测试、批量测试、渠道余额刷新。
2. 管理日志搜索、历史日志删除和完整统计。
3. 兑换码批量创建和导出。
4. 用户额度重置语义修正：当前 `ResetUserQuota` 通过充值近似实现，后续应改成绝对设置或账务调账。

### Phase 3：登录与风控体验

目标：对齐 One API 用户登录注册体验。

1. OAuth/OIDC/飞书/微信路由兼容。
2. 邮箱验证和密码重置。
3. Cloudflare Turnstile 中间件。
4. 注册邮箱域名白名单。

### Phase 4：Provider 专用适配器

目标：逐步从“OpenAI-compatible 转发”扩展到渠道原生协议。

优先顺序建议：

1. OpenAI/Azure/OpenAI-compatible：模型映射、API version、代理配置细化。
2. Anthropic/Gemini：完善流式与 usage 转换。
3. DeepSeek、Moonshot、Groq、Mistral、Cohere、Ollama：多数可走 OpenAI-compatible，但需要默认 base URL 与模型清单。
4. 百度、阿里、讯飞、腾讯、智谱、火山等：需要专用鉴权和请求转换。

### Phase 5：Web 管理端

目标：恢复完整产品可用性。

建议不要直接搬三套 React 主题，先做一个最小管理端：

1. 登录、用户、渠道、令牌、兑换码、日志、设置。
2. 与新的 `/api/*` 兼容层对齐。
3. 后续再评估是否迁移 `one-api/web/default` 或改造为独立前端。

## 验收标准

1. `go test ./...` 通过。
2. One API 常用 HTTP 路由有兼容测试。
3. 每个新增 HTTP 路由都有鉴权边界测试。
4. 管理写操作不能绕过下游 service 的业务校验。
5. Provider 适配器新增时必须覆盖非流式、流式、错误映射和 usage 统计。
