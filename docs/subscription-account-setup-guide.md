# 上游订阅号配置与导入实操指南

> 分支：`feature/hybrid-relay-adaptor-apicompat`
> 适用版本：含 migration `034_create_subscription_accounts.sql` 及 hybrid adaptor 路径
> 本文档记录「订阅号配置示例 + 凭证导入脚本 + 本地测试环境实操」的完整过程。

---

## 一、背景

micro-one-api 的 hybrid relay 分支引入了与普通「API Key 渠道」并列的一等实体——
**上游订阅号（SubscriptionAccount）**，用于深度利用 Claude Code / Codex（ChatGPT）
这类 OAuth 订阅上游：客户端身份伪装、协议链式转换、token 刷新、配额感知。

订阅号与 API Key 渠道是两套独立实体，不互相冒充：

| 维度 | API Key 渠道 (Channel) | 上游订阅号 (SubscriptionAccount) |
|------|----------------------|-------------------------------|
| 表 | `channels` | `subscription_accounts` |
| 路由表 | `abilities` | `subscription_account_abilities` |
| 凭证 | `key`（明文 API Key） | `access_token` + `refresh_token` + `expires_at` |
| 选路 | `SelectChannel(group, model)` | `SelectSubscriptionAccount(group, model, platform)` |
| 平台标识 | `type` int32 | `platform` 字符串：`claude` / `codex` |
| 上游适配 | `OpenAICompatibleAdaptor` 等 | `ClaudeOAuthAdaptor` / `CodexOAuthAdaptor` |

选路顺序（`internal/relay/biz/relay.go` 的 `Plan`）：**先按客户端模型名找 API Key
渠道，找不到再回落到订阅号**（`selectSubscriptionChannel`）。模型名前缀决定平台：
`claude-*`→claude，`gpt-*/codex-*/o1/o3/o4`→codex，其它两者都试。

因此订阅号的 `models` 字段必须填**客户端暴露的模型名**，不是上游内部名。

---

## 二、字段对照（源码依据）

数据库表结构见 `migrations/034_create_subscription_accounts.sql`；proto 定义见
`api/common/v1/common.proto` 的 `SubscriptionAccountInfo`；admin API 见
`internal/admin/server/http.go` 的 `/v1/subscription-accounts`；消费逻辑见
`internal/relay/adaptor/{codex,claude}_oauth.go` 与
`internal/relay/credential/*`。

| 字段 | 必填 | 含义 / 取值依据 |
|------|------|----------------|
| `name` | ✅ | 任意名称，web 表单强制非空 |
| `platform` | ✅ | 只能 `claude` 或 `codex`（对应 `credential.PlatformCodex/PlatformClaude`） |
| `account_type` | ✅ | 当前固定 `oauth`；仅 oauth 类型会触发身份伪装 `ShouldMimic` |
| `group` | ✅ | 用户分组，默认 `default`，必须和用户 `group` 一致才会被选中 |
| `models` | ✅ | **逗号分隔的客户端暴露模型名**，会拆成多条 ability 行（`syncSubscriptionAccountAbilitiesTx`） |
| `priority` | ✅ | 越大越优先，同优先级随机挑（`SelectSubscriptionAccount` 按 priority 降序分 tier，tier 内 `rand.Int`） |
| `base_url` | ⬜ | 留空走默认：claude→`https://api.anthropic.com/v1/messages?beta=true`，codex→`https://chatgpt.com/backend-api/codex/responses`。自建反代时才填 |
| `access_token` | ✅ | OAuth access token。claude 是 `sk-ant-oat...`，codex 是 ChatGPT 的 JWT |
| `refresh_token` | ✅ | OAuth refresh token，到期后用它去刷新。web 表单把 access/refresh 都设为必填 |
| `expires_at` | ⬜ | access_token 的过期 **Unix 秒**。留 0 则被视为已过期，首次请求会立即触发刷新 |
| `account_id` | ⬜但建议填 | claude：账号 UUID，用于 `RewriteMetadataUserID` 重写 `metadata.user_id`；codex：`chatgpt-account-id` 头，**Codex 后端强制要求** |
| `fingerprint` | ⬜ | 留空即可，首次请求时 `IdentityService.GetOrCreateFingerprint` 按平台生成默认指纹并回写（`v2:` 前缀快照） |
| `metadata` | ⬜ | 预留 JSON，当前仅透传 |
| `quota_limit_usd` | ⬜ | 本地账号总额度上限，0 表示不限制；超限后该上游账号不再参与选路 |
| `quota_used_usd` | ⬜ | 本地账号总额度已用，成功计费后由 relay 按实际消费 USD 回写 |
| `quota_daily_limit_usd` | ⬜ | 本地滚动 24h 额度上限，0 表示不限制 |
| `quota_daily_used_usd` | ⬜ | 本地滚动 24h 已用；窗口过期后下一次回写会开启新窗口 |
| `quota_weekly_limit_usd` | ⬜ | 本地滚动 7d 额度上限，0 表示不限制 |
| `quota_weekly_used_usd` | ⬜ | 本地滚动 7d 已用；窗口过期后下一次回写会开启新窗口 |
| `rate_multiplier` | ⬜ | 账号级用量倍率，默认 1；记录本地账号用量时按 `cost_usd × rate_multiplier` 累计 |
| `status` | — | `1`=启用（`biz.ChannelStatusEnabled`），其它=禁用，禁用的账号在选路时被跳过 |

---

## 三、创建订阅号配置示例

### 3.0 OAuth 授权码绑定（channel-service）

§7 已在 channel-service 暴露授权码绑定端点，用于 admin-web 或运维脚本生成授权链接、完成 code exchange 并创建 `subscription_accounts` 记录：

| Method | Path |
|---|---|
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/auth-url` |
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/exchange` |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/auth-url` |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/exchange` |

`auth-url` 返回 `auth_url` / `session_id` / `state` / `expires_at`；`exchange` 请求体传 `session_id`、`state`、`code`，可选传 `name`、`group`、`models`、`priority`、`base_url`、`metadata`。session 存在 channel-service 进程内，TTL 5 分钟，多副本部署时 exchange 必须回到生成 auth-url 的同一副本。

### 3.1 经 Admin REST API（推荐，对应 `/v1/subscription-accounts`）

**① Claude Code 订阅号（platform=claude）**

```bash
curl -X POST http://127.0.0.1:3000/v1/subscription-accounts \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "claude-pro-1",
    "platform": "claude",
    "account_type": "oauth",
    "group": "default",
    "models": "claude-sonnet-4,claude-opus-4,claude-haiku-4",
    "priority": 100,
    "base_url": "",
    "access_token": "sk-ant-oat01-xxxxxxxxxxxxxxxxxxxxxxxx",
    "refresh_token": "sk-ant-or1-xxxxxxxxxxxxxxxxxxxxxxxx",
    "expires_at": 1781234567,
    "account_id": "f9b3c2a1-1d2e-3f4a-5b6c-7d8e9f0a1b2c",
    "fingerprint": "",
    "metadata": "",
    "quota_daily_limit_usd": 20,
    "quota_weekly_limit_usd": 100,
    "rate_multiplier": 1
  }'
```

**② ChatGPT / Codex 订阅号（platform=codex）**

```bash
curl -X POST http://127.0.0.1:3000/v1/subscription-accounts \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "chatgpt-pro-1",
    "platform": "codex",
    "account_type": "oauth",
    "group": "default",
    "models": "gpt-5,gpt-5-codex,codex-mini-latest,o4-mini",
    "priority": 90,
    "base_url": "",
    "access_token": "eyJhbGciOiJSUzI1NiIs...<JWT access token>",
    "refresh_token": "v1.xxx.zzz-long-refresh-token",
    "expires_at": 1781234567,
    "account_id": "org-AbCdEf...<chatgpt-account-id>",
    "fingerprint": "",
    "metadata": "",
    "quota_daily_limit_usd": 20,
    "quota_weekly_limit_usd": 100,
    "rate_multiplier": 1
  }'
```

> 说明：本地 docker-compose 部署下 admin-api 容器内部端口 8000 映射到宿主 3000，
> 所以从宿主访问用 `http://127.0.0.1:3000`；若直连容器/非 compose 部署则用 8000。

### 3.2 等价的 SQL（直接写库，跳过 admin API）

```sql
INSERT INTO subscription_accounts
  (name, platform, account_type, status, `group`, models, priority,
   base_url, access_token, refresh_token, expires_at, account_id,
   fingerprint, metadata, created_at, updated_at)
VALUES
  ('claude-pro-1', 'claude', 'oauth', 1, 'default',
   'claude-sonnet-4,claude-opus-4,claude-haiku-4', 100,
   NULL, 'sk-ant-oat01-xxx', 'sk-ant-or1-xxx', 1781234567,
   'f9b3c2a1-1d2e-3f4a-5b6c-7d8e9f0a1b2c',
   '', '', UNIX_TIMESTAMP(), UNIX_TIMESTAMP());
-- abilities 行由 CreateSubscriptionAccount 自动同步生成，无需手写

INSERT INTO subscription_accounts
  (name, platform, account_type, status, `group`, models, priority,
   base_url, access_token, refresh_token, expires_at, account_id,
   fingerprint, metadata, created_at, updated_at)
VALUES
  ('chatgpt-pro-1', 'codex', 'oauth', 1, 'default',
   'gpt-5,gpt-5-codex,codex-mini-latest,o4-mini', 90,
   NULL, 'eyJhbGci...', 'v1.xxx.zzz', 1781234567,
   'org-AbCdEf...', '', '', UNIX_TIMESTAMP(), UNIX_TIMESTAMP());
```

---

## 四、从本地 CLI 凭证自动导入（脚本）

手动填 token 容易出错且会泄露到 shell 历史。仓库提供了导入脚本
`scripts/import-subscription-creds.py`，从本机 Claude Code / Codex CLI 的 OAuth
凭证文件里抽取 `access_token` / `refresh_token` / `account_id` / `expires_at`，
组装成可直接 POST 的 payload。

### 4.1 凭证来源

| 平台 | 来源（按优先级尝试，命中即止） |
|------|------------------------------|
| Claude (macOS) | Keychain service `Claude Code-credentials`（JSON blob） |
| Claude (Linux) | `~/.claude/.credentials.json` |
| Codex (通用) | `~/.codex/auth.json` 或 `~/.config/codex/auth.json` |

Codex 的 `~/.codex/auth.json` 结构（ChatGPT 订阅登录态）：

```json
{
  "OPENAI_API_KEY": "",
  "auth_mode": "chatgpt",
  "last_refresh": "2026-06-27T10:30:45.792633Z",
  "tokens": {
    "access_token": "<JWT, ~2000 字符>",
    "refresh_token": "<~200 字符>",
    "id_token": "<JWT>",
    "account_id": "<36 字符 UUID>"
  }
}
```

### 4.2 脚本用法

```bash
# Codex
python3 scripts/import-subscription-creds.py codex \
  --name chatgpt-pro-1 --group default \
  --models "gpt-5,gpt-5-codex,codex-mini-latest,o4-mini" --priority 90

# Claude（需先在本机用 Claude Code 登录订阅）
python3 scripts/import-subscription-creds.py claude \
  --name claude-pro-1 --group default \
  --models "claude-sonnet-4,claude-opus-4" --priority 100
```

脚本行为：
1. **读凭证**：Codex 读 `~/.codex/auth.json` 的 `tokens.*`；Claude 优先 Keychain，回退文件。
2. **算过期**：优先用凭证里的显式 `expires_at`，没有就解 JWT 的 `exp` 字段。
3. **写文件**：完整明文 payload 写到 `subscription-account.<platform>.payload.json`
   （避免 secret 出现在命令行历史/进程列表里）。该文件已被 `.gitignore` 忽略。
4. **打印脱敏摘要 + curl 命令**。
5. 可选 `--apply`：带 `--admin-token` 直接 POST 到
   `--admin-url/v1/subscription-accounts`。

### 4.3 安全

- 脚本仅读取本机凭证用于导入到自建网关，不会外发任何数据。
- 生成的 payload 文件含明文 OAuth token，已加入 `.gitignore`：
  ```
  # Subscription account import payloads (contain plaintext OAuth tokens)
  subscription-account.*.payload.json
  ```

---

## 五、relay-gateway 侧开关

光建账号还不够，**必须打开 hybrid_adaptor 开关**，订阅号路径才生效
（`cmd/relay-gateway/wire_gen.go` 里 `cfg.HybridAdaptor.GetHybridAdaptorEnabled()`
为 true 才会启动 `RefreshTask` 并走 adaptor 新路径；为 false 时回退到老的
provider-factory 直连，订阅号不会被使用）：

```yaml
# configs/relay-gateway.yaml 末尾追加
hybrid_adaptor:
  enabled: true                 # 总开关，不开则订阅号路径完全不启用
  identity_ttl: 24h             # 指纹缓存 TTL，过期后从 fingerprint 列重建
  refresh_interval: 10m         # 后台刷新任务扫描周期
  refresh_lookahead: 24h        # 扫描「now+24h 内过期」的账号预刷新
```

刷新机制（双保险）：
- **后台 `RefreshTask`**：每 10min 扫描 `expires_at <= now+24h` 的账号
  （`ChannelSubscriptionAccountStore.ExpiringSoon`），调对应平台 `TokenProvider.Refresh`。
- **请求时按需刷新**：token 在 `RefreshSkew`(3min) 内过期时，
  `baseTokenProvider.GetAccessToken` 同步刷新。
- 刷新端点：claude→`https://console.anthropic.com/v1/oauth/token`，
  codex→`https://auth.openai.com/oauth/token`，client_id 已硬编码
  （`ClaudeOAuthClientID`/`CodexOAuthClientID`）。

---

## 六、本地测试环境实操记录

### 6.1 环境

- 部署方式：`deployments/docker-compose/docker-compose.yml`
- admin-api：容器内 8000 → 宿主 3000（`docker-compose.yml` 中 `ports: "3000:8000"`）
- relay-gateway：容器内 8080 → 宿主 8080
- MySQL：容器 `mysql`，库 `oneapi`，root 密码见 `deployments/docker-compose/.env`
- 管理员 token：`deployments/docker-compose/.env` 的 `ADMIN_TOKEN`

### 6.2 操作步骤

**1. 生成 Codex 订阅号 payload**

```bash
cd /Users/neo/vscode/mengbin/micro-one-api
python3 scripts/import-subscription-creds.py codex \
  --name chatgpt-pro-1 --group default \
  --models "gpt-5,gpt-5-codex,codex-mini-latest,o4-mini" --priority 90
```

输出（脱敏）：

```
✓ 凭证来源: /Users/neo/.codex/auth.json
✓ 完整 payload 已写入: subscription-account.codex.payload.json
──────────────────────────────────────────────────
脱敏摘要:
{
  "name": "chatgpt-pro-1",
  "platform": "codex",
  "account_type": "oauth",
  "group": "default",
  "models": "gpt-5,gpt-5-codex,codex-mini-latest,o4-mini",
  "priority": 90,
  "base_url": "",
  "access_token": "eyJhbG…5Htk",
  "refresh_token": "rt.1.A…-cOU",
  "expires_at": "2026-07-07 18:30:46",
  "account_id": "6eab8a…0476",
  "fingerprint": "",
  "metadata": ""
}
```

字段来源解析：
- `access_token`：`~/.codex/auth.json` 的 `tokens.access_token`（JWT，2088 字符）
- `refresh_token`：`tokens.refresh_token`（196 字符）
- `account_id`：`tokens.account_id`（36 字符 UUID，作为 `chatgpt-account-id` 头）
- `expires_at`：解 JWT payload 的 `exp` → Unix 秒 → 约 10 天后（`2026-07-07 18:30:46`）

**2. 经 admin API 导入**

```bash
curl -s -X POST http://127.0.0.1:3000/v1/subscription-accounts \
  -H "Authorization: Bearer test-admin-token-123456789" \
  -H "Content-Type: application/json" \
  -d @subscription-account.codex.payload.json \
  -w "\nHTTP %{http_code}\n"
```

返回：

```
{"success":true,"message":"ok","account_id":2}

HTTP 200
```

**3. 库内确认**

```bash
docker exec mysql mysql -uroot -prootpassword123 oneapi -e \
"SELECT id,name,platform,status,\`group\`,priority,account_id,expires_at,LEFT(access_token,12) AS at_preview FROM subscription_accounts ORDER BY id;"
```

导入后库内现状：

| id | name | platform | status | group | priority | account_id | expires_at | 备注 |
|----|------|----------|--------|-------|----------|------------|------------|------|
| 1 | claude-pro-1 | claude | 2(禁用) | default | 0 | acct-123 | 0 | 早期遗留假测试数据（access_token=`sk-ant-test-...`） |
| 2 | chatgpt-pro-1 | codex | 1(启用) | default | 90 | `<36 字符 UUID>` | `<约 10 天后>` | ✅ 本次用 `~/.codex/auth.json` 真实导入 |

### 6.3 说明

- 本次导入的是 **Codex/ChatGPT 订阅**（platform=`codex`），不是 Claude Code。
  Claude 平台的真实订阅账号未导入：本机 Keychain 无 `Claude Code-credentials` 条目、
  `~/.claude/.credentials.json` 不存在，无法自动抽取 Claude OAuth 凭证。
- 遗留的 `claude-pro-1`（id=1）是假测试数据（禁用状态），不影响路由——选路会跳过
  `status≠1` 的账号。如需清理可执行：
  ```bash
  curl -X DELETE http://127.0.0.1:3000/v1/subscription-accounts/1 \
    -H "Authorization: Bearer $ADMIN_TOKEN"
  ```

---

## 6.4 配额快照与错误透传

开启 `hybrid_adaptor.enabled` 后，订阅账号路径会按 §7 规则处理上游错误和 Codex 配额窗口：

- Codex 响应体里出现 5h / 7d quota window 字段时，relay-gateway 会解析 `used_percent` / `reset_after_seconds` / `window_minutes`，并写回订阅账号 metadata；数据库迁移 `041_create_account_quota_snapshots.sql` 提供了后续直写快照表的落点。
- 本地上游账号额度由 `quota_limit_usd`、`quota_daily_limit_usd`、`quota_weekly_limit_usd` 控制，单位 USD，0 表示不限。选路时 `SelectSubscriptionAccount` 会跳过本地总额、24h 或 7d 窗口已耗尽的账号。
- 成功提交计费后，relay-gateway 会用 billing 返回的 `committed_amount` 折算 USD，并通过 `RecordSubscriptionAccountQuotaUsage` 回写账号本地用量；`rate_multiplier` 会在 channel-service 内用于累计账号实际消耗。
- 管理端可调用 `POST /v1/subscription-accounts/{account_id}/reset-quota` 重置本地用量，`scope` 支持 `total` / `daily` / `weekly` / `all`。
- 当 Codex primary quota 使用率达到 95% 或 secondary quota 达到 100% 时，账号会被自动暂停，避免继续被选路。
- 上游 `401` / `403` / `429` / `cyber_policy` 会原样透传状态码、body 和 `Retry-After` 给客户端；网络错误和 `5xx` 才会触发跨账号 failover。

排查时可先看订阅账号列表里的本地额度已用值、metadata 中的 `quota_snapshot` 与 `last_error`，再确认账号 `status` 是否已被自动改为禁用。

---

## 七、客户端调用示例

配好后，下游客户端照常用 micro-one-api 的标准协议入口即可，路由层自动落到订阅号：

```bash
# 用 OpenAI ChatCompletions 协议打 Claude 订阅号
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer <micro-one-api 用户 token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"stream":true}'

# 用 Anthropic Messages 协议打 Codex 订阅号（Claude Code CLI 风格）
curl http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer <micro-one-api 用户 token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"max_tokens":1024}'
```

协议转换由 `internal/relay/apicompat` 完成：`ChatCompletions⇄Responses`、
`Anthropic⇄Responses`，枢纽是 Responses。身份伪装（system prompt 注入、
metadata.user_id 重写、anthropic-beta 计算、codex_cli_rs/claude-cli UA 头）由
`internal/relay/identity` 在 `BuildUpstreamRequest` 里自动做，无需在订阅号配置里
写任何伪装参数。

---

## 八、踩坑提示

1. **`models` 写客户端模型名，不是上游名**：写 `claude-sonnet-4`，别写上游快照名——
   选路是按客户端请求里的 `model` 字段匹配 ability 的。
2. **`group` 要和用户分组对齐**：`default` 组的用户只能用 `group=default` 的订阅号。
3. **Codex 必须填 `account_id`**：是 `chatgpt-account-id` 头，不填会被上游拒。
   Claude 的 `account_id` 用于 metadata 重写，不填伪装不完整。
4. **`expires_at` 建议填真实过期时间**：填 0 会导致每次请求都触发同步刷新，增加延迟。
5. **`fingerprint` 留空**：让它自动生成并回写，手填容易格式错（必须是 `v2:` 前缀 JSON 快照）。
6. **token 刷新失败**：后台刷新失败只记日志，不自动禁用账号；请求时刷新失败会返回
   `ErrRefreshFailed`，路由层把该账号当不可用继续重试下一个。建议监控盯 `expires_at`
   长期过期的账号。
7. **payload 文件勿提交**：`subscription-account.*.payload.json` 含明文 token，已加入
   `.gitignore`，确认 `git check-ignore` 命中后再操作。
