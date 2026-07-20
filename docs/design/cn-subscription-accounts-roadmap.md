# 国内订阅账号接入实施路线图（Kimi / MiniMax / 智谱 GLM）

> 目标：让 relay-gateway 的订阅账号体系支持国内三家厂商的 Coding 订阅
> （Kimi For Coding、MiniMax Coding Plan、智谱 GLM Coding Plan），
> 与现有 Claude / Codex OAuth 订阅账号并列。

## 0. 背景与结论

现有订阅账号链路（Claude / Codex）：

```
channel (type=32/33) ──► lazyOAuthAdaptor (internal/adaptor/register_oauth.go)
   ──► TokenProvider (domain/upstream/credential/, baseTokenProvider 缓存+刷新)
   ──► identity.IdentityService (指纹/User-Agent 伪装)
   ──► apicompat (Anthropic ↔ Responses 转换)
   ──► subscription_accounts 表 (access/refresh token, expires_at, platform, fingerprint)
```

三家厂商调研结论：

| 厂商 | 订阅产品 | 鉴权方式 | 上游端点形态 | 接入模式 |
|------|----------|----------|--------------|----------|
| 智谱 GLM | GLM Coding Plan | 长期 API Key（无 refresh） | Anthropic 兼容 `https://open.bigmodel.cn/api/anthropic` | **静态 token**，复用 Claude OAuth adaptor 路径 |
| MiniMax | MiniMax Coding Plan | 长期 API Key（无 refresh） | Anthropic 兼容 `https://api.minimax.io/anthropic` | **静态 token**，同上 |
| Kimi | Kimi For Coding | OAuth PKCE（refresh token 续期） | Anthropic 兼容（Kimi CLI 端点） | **OAuth refresh**，复用 baseTokenProvider |

关键设计判断：

- 三家都提供 Anthropic 兼容端点，上游交互可整体复用 `ClaudeOAuthAdaptor` 的请求
  构造与 `apicompat` 转换，不新增协议转换代码。
- GLM / MiniMax 是"永不过期的静态 key"，Kimi 是 OAuth。引入第二种凭据类型
  `StaticTokenProvider`（Refresh 为 no-op），TokenProvider 工厂按 platform 分发。
- 官方对 Claude Code 类客户端的支持态度：GLM / MiniMax 官方文档公开支持，风控
  风险低，可直接透传 Claude Code 指纹；Kimi 限定 Kimi CLI，需要模拟其 UA。

实施总顺序：**P0 抽象重构 → P1 智谱 GLM（最小验证）→ P2 MiniMax（复制模式）
→ P3 Kimi（OAuth 全流程）→ P4 账号管理面与配额 → P5 联调与灰度**。

---

## P0 抽象重构（前置，不改行为）

目的：把 Claude 专属的硬编码抽成 per-platform 配置，为后续三家铺路。

1. **Platform 枚举扩展**
   - `internal/identity/fingerprint.go`：`Platform` 增加 `PlatformKimi`、
     `PlatformZhipu`、`PlatformMinimax`。
   - `domain/upstream/credential/token_provider.go`：镜像的 `Platform` 类型同步增加。
   - 两个包保持各自独立（现有约定：credential 不依赖 identity）。

2. **上游 URL 配置化**
   - 现状：`ClaudeOAuthAdaptor.GetUpstreamURL` 默认 `https://api.anthropic.com`，
     已支持 `ctx.Channel.BaseURL` 覆盖（claude_oauth.go:110）。
   - 改造：确认三家端点均通过 channel 的 `BaseURL` 下发即可，adaptor 无需改代码；
     在 channel-service 创建渠道时写入对应 BaseURL。若路径不同（`/v1/messages` vs
     其他），把路径也抽为 per-platform 常量表。

3. **StaticTokenProvider（新凭据类型）**
   - 新文件 `domain/upstream/credential/static_token_provider.go`：
     - `GetAccessToken`：直接返回 `AccountLookup` 里存的 access_token（即订阅 key）。
     - `Refresh`：no-op（或检测到 401 时置账号错误态，走现有
       `SetSubscriptionAccountError` 链路）。
     - `Invalidate`：no-op。
   - 单元测试：静态返回、401 错误上报路径。

4. **TokenProvider 工厂泛化**
   - `cmd/relay-gateway/wire.go` 的 `tokenFactory`：switch 由 2 个 case 扩为按
     platform → provider 构造表驱动，便于后续增量加 case。
   - `RefreshTask` 的 platform map 同步改为表驱动；静态 platform 不加入 refresh map
     （无需周期刷新）。

验收：`go test ./domain/upstream/credential/... ./internal/adaptor/...` 全绿，
现有 Claude/Codex 链路行为不变（回归靠现有 adaptor 测试）。

---

## P1 智谱 GLM Coding Plan（第一家，工作量最小，验证整个模式）

1. **ChannelType 定义**
   - `domain/upstream/provider/factory.go`：新增 `ChannelTypeZhipuPlan int32 = 34`
     （接续现有 32/33 编号），注释 "Zhipu GLM Coding Plan (Anthropic Messages API)"。
   - channel 类型注册表、admin 前端的渠道类型下拉（web/ 下 channel type 映射）同步加。

2. **Adaptor 注册**
   - `internal/adaptor/register_oauth.go` `init()`：
     `Register(provider.ChannelTypeZhipuPlan, ...)` 注册 lazyOAuthAdaptor（platform=
     `identity.PlatformZhipu`）。
   - `lazyOAuthAdaptor.build` 增加 case：复用 `NewClaudeOAuthAdaptor`（Anthropic
     兼容请求构造完全适用），或在其基础上包一层仅改 kind/name。

3. **TokenProvider 接线**
   - `cmd/relay-gateway/wire.go`：构造 `NewStaticTokenProvider(accountLookup)`，
     tokenFactory 增加 `case relayidentity.PlatformZhipu: return staticProvider`。
   - 不加入 RefreshTask map。

4. **Identity / 指纹**
   - GLM 官方支持 Claude Code 客户端：`ShouldMimic` 对 `PlatformZhipu` 沿用
     Claude Code UA 透传策略即可；确认 `x-stainless-*` headers 与 anthropic-beta
     计算逻辑（`identity.ComputeAnthropicBeta`）对 GLM 端点无副作用，必要时按
     platform 裁剪 beta flags。

5. **渠道配置约定**
   - 创建 channel 时：`type=34`，`BaseURL=https://open.bigmodel.cn/api/anthropic`，
     `models=glm-4.x 系列`。
   - 账号录入：channel-service 的 `CreateSubscriptionAccount` 写入
     `access_token=<coding plan key>`，`expires_at=0`（语义：不过期），
     `platform=zhipu`。
   - 确认 `internal/data/subscription_accounts.go` 的 Lookup/Store 对 expires_at=0
     不做过期剔除（如有时基过滤需放行静态账号）。

验收：
- 单测：adaptor 注册、static provider、wire 工厂新 case。
- 集成：用真实 GLM Coding Plan key 走 `/v1/messages`（Anthropic 入站）与
  `/v1/chat/completions`（OpenAI 入站经 apicompat）各一条非流式+一条流式请求。
- 账号 key 失效时错误态落库并可恢复。

---

## P2 MiniMax Coding Plan（复制 P1 模式）

与 P1 完全同构，增量很小：

1. `ChannelTypeMinimaxPlan int32 = 35`，platform `minimax`。
2. `register_oauth.go` 注册 + `build` case（复用 Claude adaptor）。
3. wire.go tokenFactory 复用同一个 `StaticTokenProvider` 实例增加 case。
4. BaseURL 约定 `https://api.minimax.io/anthropic`，models 为 MiniMax-M2 系列。
5. 指纹策略同 GLM（官方支持 Claude Code）。
6. 差异点排查：MiniMax 端点对 `anthropic-beta` / system 字段 / max_tokens 的
   兼容性，在 adaptor 层做 per-platform 微调（如需）。

验收：同 P1 的三条请求链路 + 失效处理。

---

## P3 Kimi For Coding（OAuth 全流程，工作量最大）

1. **ChannelType**：`ChannelTypeKimiOAuth int32 = 36`，platform `kimi`。

2. **KimiTokenProvider**
   - 新文件 `domain/upstream/credential/kimi_token_provider.go`，复用
     `baseTokenProvider`（与 Claude/OpenAI provider 同构，~30 行）：
     - 常量：`KimiOAuthClientID`、`KimiTokenRefreshURL`（Kimi CLI 的 token
       endpoint，需从 Kimi CLI 实际流量/公开资料确认；做成 config 可覆盖，
       因为这类端点不稳定）。
   - `http_refresher.go` 的 refresh 请求体若与标准 OAuth refresh grant 有差异，
     按 Kimi 实际格式扩展（保持 refresher 可注入）。

3. **OAuth 授权录入链路**
   - 现状 Claude/Codex 的授权码换 token 录入流程（channel-service 侧
     `CreateSubscriptionAccount` 上游的管理端逻辑）需要支持 Kimi 的 PKCE 授权：
     authorize URL 生成、callback 换 token、写入 access/refresh/expires。
   - 若 Kimi CLI 采用 device code 或本地 callback 模式，管理端提供"粘贴
     refresh token"的手工录入兜底（MVP 可先只支持手工录入）。

4. **Adaptor 注册**：同 P1，`build` 增加 `PlatformKimi` case，复用 Claude
   adaptor；BaseURL 指向 Kimi 的 Anthropic 兼容端点。

5. **Identity / 指纹**
   - `ShouldMimic` 对 `PlatformKimi` 采用 Kimi CLI 的 User-Agent 伪装
     （Kimi 限定自家 CLI，透传 Claude Code UA 有被风控风险）。
   - 新增 Kimi 指纹快照样例，参考 `claude_code_detector.go` 的实现方式；
     `RestoreFromSnapshot` 支持 `PlatformKimi`。

6. **RefreshTask**：wire.go 的 refresh map 增加
   `relaycredential.PlatformKimi: kimiTokenProvider`，走周期刷新。

验收：
- 单测：KimiTokenProvider 刷新成功/失败/并发去重（仿照现有 provider 测试）。
- 集成：授权录入 → 到期自动刷新 → 请求成功；refresh token 失效时账号置错误态。

---

## P4 账号管理面与配额

1. **channel-service / admin**
   - `api/channel/v1/channel.proto`：`platform` 字段新值的校验白名单（如存在枚举
     校验）加 `kimi` / `zhipu` / `minimax`，然后 `make api`。
   - admin web：订阅账号录入表单按 platform 区分（GLM/MiniMax 只填 key；Kimi 走
     授权或粘贴 refresh token）；渠道类型下拉加三项。
2. **配额/风控**
   - 三家 Coding Plan 均有用量窗口限制（5 小时窗口 / 周限额形态）。复用现有
     `RecordSubscriptionAccountQuotaUsage` / 账号错误熔断链路；窗口参数做成
     per-platform 配置。
   - 429/限流错误在 `internal/adaptor/errors.go` 映射为可熔断的账号级错误。
3. **文档**
   - 更新 `domain/subscription/README.md` Consumers/平台表；
   - docs/runbooks 增加三家账号录入与排障手册。

---

## P5 联调与灰度

1. 回归：`make all`，`go test ./...`，重点跑
   `./internal/adaptor/... ./domain/upstream/credential/... ./internal/apicompat/...`。
2. e2e：三家各一条 Anthropic 入站 + 一条 OpenAI 入站请求（流式/非流式），
   覆盖 token 刷新（Kimi）、静态 key 失效熔断（GLM/MiniMax）。
3. 灰度：先单账号小流量，观察上游风控响应（401/403 频率），再放量。
4. 监控：按 platform 维度的刷新失败率、熔断次数、上游 4xx 率告警。

---

## 风险与备注

- **端点与风控易变**：三家 Coding 订阅的端点、beta 头、UA 校验都可能随版本
  调整，所有 URL/UA/client_id 必须 config 可覆盖，禁止硬编码为唯一来源。
- **ToS 风险**：Kimi 限定 Kimi CLI、GLM/MiniMax 官方支持 Claude Code。中转
  转售均存在账号封禁风险，需在运营侧评估。
- **不做项**：MiniMax 消费者订阅（App 会员，无公开鉴权体系）不接入；
  三家的非 Coding 产品线（网页版会员）不接入。
- **分层约束**：所有新代码遵守 AGENTS.md 分层——platform 枚举与 TokenProvider
  在 `domain/upstream/credential`（biz 层语义），adaptor 在 `internal/adaptor`
  （service 层），凭据存取走 channel-service 既有 RPC，不新增跨层 import。
