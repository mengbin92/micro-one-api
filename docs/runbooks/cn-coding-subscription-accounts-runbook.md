# 国内 Coding 订阅账号运维手册（智谱 GLM / MiniMax / Kimi）

> 适用范围：relay-gateway 的国内三家 Coding 订阅账号接入。
> 设计文档：`docs/design/cn-subscription-accounts-roadmap.md`。

## 平台与凭据对照

| 平台    | ChannelType | platform 值 | account_type | 凭据形态                 | 上游 BaseURL                                |
|---------|-------------|-------------|--------------|--------------------------|---------------------------------------------|
| 智谱 GLM | 34          | `zhipu`     | `static_key` | 长期 API Key（无刷新）   | `https://open.bigmodel.cn/api/anthropic`    |
| MiniMax | 35          | `minimax`   | `static_key` | 长期 API Key（无刷新）   | `https://api.minimax.io/anthropic`          |
| Kimi    | 36          | `kimi`      | `oauth`      | OAuth refresh token      | Kimi CLI 的 Anthropic 兼容端点（config 覆盖）|

三家上游均提供 Anthropic 兼容端点，复用 `ClaudeOAuthAdaptor` 的请求构造与
`apicompat` 转换，不新增协议转换代码。

## 录入

### GLM / MiniMax（静态 Key）

1. 在「订阅账号管理」页面点「新建订阅账号」。
2. 平台选择 `智谱 GLM` 或 `MiniMax`，账号类型自动切换为 `静态 Key`。
3. 填写 Coding Plan Key（即 Access Token），Refresh Token 留空、过期时间留空
   （语义：永不过期，`expires_at=0`）。
4. Base URL 填上表中的上游地址。
5. 模型按厂商填写（GLM：`glm-4.x` 系列；MiniMax：`MiniMax-M2` 系列）。

静态账号不会被加入后台 `RefreshTask`，因为没有刷新流程；Key 失效时由上游
401/403 触发现有的 runtime-blocker → `SetSubscriptionAccountError` 链路熔断，
恢复时由运营更换 Key。

### Kimi（OAuth）

设备码 OAuth 需上游为本应用批准的客户端注册。Channel 与 Relay 使用同一 `KIMI_OAUTH_CLIENT_ID`，见[订阅账号 OAuth 绑定手册](./subscription-oauth-binding-runbook.md#34-kimi-设备码授权)。
绑定后由 `RefreshTask` 在到期前自动刷新。已设置 `bootstrap.hybrid_adaptor.kimi` 覆盖项的部署需核对注册一致性。
refresh token 失效后重新授权。

## 指纹与伪装

| 平台    | 策略                                                                |
|---------|---------------------------------------------------------------------|
| GLM     | 官方支持 Claude Code 客户端，沿用 Claude Code UA/指纹透传           |
| MiniMax | 同 GLM                                                              |
| Kimi    | 保留真实调用方 User-Agent 和请求正文 |

`ShouldMimic` 对 GLM/MiniMax 复用 `IsClaudeCodeClient` 检测。

## 配额与熔断

三家 Coding Plan 均有用量窗口限制。复用现有链路：

- `RecordSubscriptionAccountQuotaUsage`：按账号记录用量，更新本地
  5h/24h/7d 滚动窗口。
- `SetTempUnschedulable` / `AutoPauseAccount`：上游 429/529/401 触发冷却或
  熔断；冷却时长由 `runtime_block` 配置（429=5s、401=2m、5xx=2m、529=30s）。
- `AccountRecoverySweeper`：按 `recovery_policy`（auto/quota/codex）自动恢复。

`passthrough.Classify` 已将 429 映射为 `KindRetryableForward`（跨账号失败
转移），529 映射为 `KindOverloaded`，401/403 映射为 `KindPassthrough`，无需
为三家新增错误映射。

## 排障

| 现象                         | 排查                                                                 |
|------------------------------|----------------------------------------------------------------------|
| GLM/MiniMax 请求 401         | Coding Plan Key 失效，更换 Key；账号被 `SetSubscriptionAccountError` 标记，需 `ClearSubscriptionAccountError` |
| Kimi 刷新失败                | 查看 `RefreshTask` 日志；refresh_token 失效需重新授权录入            |
| Kimi 授权 403                | 核对凭据适用的接口和上游批准的客户端登记。对第三方工具使用控制台签发的 API Key |
| 上游 429 频发                | 降低账号优先级/权重；检查本地 RPM/窗口配额；必要时调大 `runtime_block.rate_limited_duration` |
| 模型不支持                   | 确认 channel `models` 与上游实际支持的模型一致                       |

## 不接入项

- MiniMax 消费者订阅（App 会员，无公开鉴权体系）。
- 三家的非 Coding 产品线（网页版会员）。
- 中转转售存在 ToS 风险，需运营侧评估。
