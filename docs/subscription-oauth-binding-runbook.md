# 订阅账号 OAuth 绑定 Runbook

> 对应 `docs/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 适用版本：含 migration `034_create_subscription_accounts.sql` 及 hybrid adaptor 路径（v0.4.0+）。
> 相关文档：[订阅账号配置与导入实操指南](./subscription-account-setup-guide.md)、[生产发布 Runbook](./subscription-production-runbook.md)。

本 runbook 让新部署人员只按本文档即可完成 Claude / Codex（ChatGPT）订阅号的 OAuth 授权码绑定，并验证绑定结果可用于选路。绑定方式有两种：管理后台授权码流（推荐）和 Admin REST API / 脚本导入。两种方式最终都写入同一张 `subscription_accounts` 表。

## 一、前置条件

1. **服务已部署并健康**：relay-gateway、admin-api、channel-service、identity-service、billing-service 全部 `/healthz` 返回 `{"status":"ok"}`。
   ```bash
   curl -s http://127.0.0.1:8080/healthz   # relay-gateway
   curl -s http://127.0.0.1:3000/healthz   # admin-api（容器内 8000 → 宿主 3000）
   curl -s http://127.0.0.1:8002/healthz   # channel-service
   ```
2. **数据库迁移已执行**：`034_create_subscription_accounts.sql` 必须存在。订阅账号本地额度还需 `051-056`，详见 [生产发布 Runbook](./subscription-production-runbook.md) 的迁移清单。
   ```bash
   make migrate-status   # 确认 034 及之后的订阅账号迁移已 applied
   ```
3. **hybrid_adaptor 已开启**：relay-gateway 配置 `hybrid_adaptor.enabled: true`。不开则订阅号路径完全不启用，请求会回落到 provider-factory 直连并因 channel key 为空返回 502。详见 [生产发布 Runbook](./subscription-production-runbook.md)。
4. **管理员凭证**：`deployments/docker-compose/.env` 的 `ADMIN_TOKEN`（管理后台 Basic Auth / Bearer token）。
5. **上游订阅本身**：一个已登录的 Claude Code（`claude` 平台）或 ChatGPT Plus/Pro（`codex` 平台）账号，用于在浏览器里完成授权。

> 单副本部署可直接使用本 runbook。多副本部署（≥2 个 channel-service 实例）必须读 §六 的 session sticky 注意事项。

## 二、必填配置

OAuth 绑定本身不需要额外的环境变量——channel-service 的 OAuth 端点默认随服务启动。下列配置项影响绑定后的行为，确认它们符合预期：

| 配置 / 环境变量 | 位置 | 必填 | 说明 |
| --- | --- | --- | --- |
| `hybrid_adaptor.enabled: true` | `configs/relay-gateway.yaml` | ✅ | 订阅号路径总开关 |
| `hybrid_adaptor.identity_ttl` | 同上 | ⬜ | 指纹缓存 TTL，默认 24h |
| `hybrid_adaptor.refresh_interval` | 同上 | ⬜ | 后台 token 刷新扫描周期，默认 10m |
| `hybrid_adaptor.refresh_lookahead` | 同上 | ⬜ | 扫描 now+24h 内过期的账号预刷新 |
| `ADMIN_TOKEN` | `.env` | ✅ | 管理后台访问令牌 |
| `DATABASE_DSN` | `.env` | ✅ | channel-service 直连的订阅账号表 |

绑定产生的账号字段（由 `exchange` 自动填充，无需手动配）：

| 字段 | Claude 来源 | Codex 来源 |
| --- | --- | --- |
| `access_token` | token 响应 `access_token`（`sk-ant-oat...`） | token 响应 `access_token`（JWT） |
| `refresh_token` | token 响应 `refresh_token` | token 响应 `refresh_token` |
| `expires_at` | `now + expires_in` 秒 | 同上 |
| `account_id` | `claudeAccountID(token)`（账号 UUID） | id_token 的 `chatgpt_account_id`，回退 `accounts/check` 接口 |
| `fingerprint` | 空（首次请求时自动生成 `v2:` 快照并回写） | 同上 |
| `models` | 请求体 `models` 或平台默认 | 同上 |

## 三、绑定流程（管理后台授权码流，推荐）

端点实际注册在 channel-service HTTP 上，admin-api 会把 `/api/v1/admin/accounts/subscription/oauth/` 前缀代理到 channel-service（受 `adminAuth` 保护），所以管理后台或运维脚本统一从 admin-api 入口访问即可。

| Method | Path（经 admin-api 代理） | 直连 channel-service |
| --- | --- | --- |
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/auth-url` | 同路径 |
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/exchange` | 同路径 |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/auth-url` | 同路径 |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/exchange` | 同路径 |

### 3.1 申请授权链接

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/admin/accounts/subscription/oauth/claude/auth-url \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"redirect_uri":""}'   # 留空走平台默认回调
```

返回：

```json
{
  "auth_url":   "https://claude.ai/oauth/authorize?client_id=...&state=...&code_challenge=...&redirect_uri=...",
  "session_id": "<32 hex>",
  "state":      "<64 hex>",
  "expires_at": 1780000000
}
```

- `auth_url`：在浏览器打开，用上游订阅账号登录并授权。
- `session_id` / `state`：保存好，`exchange` 时要回传。
- `expires_at`：session TTL 5 分钟（`defaultSessionTTL`），过期需重新申请。

默认回调地址（`redirect_uri` 留空时）：
- Claude → `https://platform.claude.com/oauth/code/callback`
- Codex → `http://localhost:1455/auth/callback`

授权完成后浏览器会跳到回调地址，URL 里带 `?code=...&state=...`。

### 3.2 交换 code 创建订阅账号

把回调 URL 里的 `code`（或整段回调 URL）连同 `session_id` / `state` 回传：

```bash
curl -s -X POST http://127.0.0.1:3000/api/v1/admin/accounts/subscription/oauth/claude/exchange \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "session_id": "<上一步 session_id>",
    "state":      "<上一步 state>",
    "code":       "https://platform.claude.com/oauth/code/callback?code=...&state=...",
    "name":       "claude-pro-1",
    "group":      "default",
    "models":     "claude-sonnet-4,claude-opus-4,claude-haiku-4",
    "priority":   100
  }'
```

`code` 字段支持三种写法（`parseOAuthCallbackInput` 会解析）：
1. 整段回调 URL（`https://...?code=xxx&state=yyy`）。
2. 纯 `code` 值（`xxx`）。
3. `code=xxx` 形式。

可选字段：`name`、`group`（默认 `default`）、`models`（留空走平台默认模型名）、`priority`、`base_url`、`metadata`。

成功返回：

```json
{
  "account_id": 2,
  "platform":   "claude",
  "metadata":   "{\"chatgpt_account_id\":\"...\",\"plan_type\":\"...\"}"
}
```

`account_id`（数字）是新建的 `subscription_accounts.id`，不是上游账号 UUID。上游 UUID 存在该行的 `account_id` 列。

Codex 流程完全对称，把 `claude` 换成 `codex` 即可。Codex 的 `exchange` 还会：
- 从 id_token 解析 `chatgpt_account_id` 与 `plan_type`，必要时回退 `chatgpt.com/backend-api/accounts/check/v4-2023-04-27`。
- 调 `chatgpt.com/backend-api/settings/user` 关闭训练（`training_allowed=false`）。

### 3.3 管理后台图形界面

`web/src/pages/admin/OAuthBindDialog.tsx` 封装了上述两步：选平台 → 申请 auth_url → 浏览器授权 → 粘贴回调 URL → 交换。入口在「订阅账号」管理页。无需手动 curl。

## 四、验证

### 4.1 库内确认

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e \
"SELECT id,name,platform,status,\`group\`,priority,account_id,expires_at,
        LEFT(access_token,12) AS at_preview
 FROM subscription_accounts ORDER BY id;"
```

期望：`status=1`（启用）、`expires_at` 在未来、`account_id` 非空（Codex 必填）。

### 4.2 abilities 同步

`exchange` 通过 `CreateSubscriptionAccount` 会自动调用 `syncSubscriptionAccountAbilitiesTx`，按 `models` 字段拆成多条 `subscription_account_abilities` 行。确认：

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e \
"SELECT subscription_account_id, model, \`group\`
 FROM subscription_account_abilities ORDER BY subscription_account_id;"
```

### 4.3 端到端调用

```bash
# 用 ChatCompletions 协议打 Claude 订阅号
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"stream":true}'

# 用 Anthropic Messages 协议打 Codex 订阅号
curl http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"max_tokens":1024}'
```

期望返回 200 + 正常流式响应。若返回 502 且日志里有「channel key 为空」，说明 `hybrid_adaptor.enabled` 没开。

### 4.4 token 刷新健康度

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e \
"SELECT id,name,expires_at,UNIX_TIMESTAMP() AS now_ts,
        CASE WHEN expires_at=0 THEN 'expired(0)' WHEN expires_at<UNIX_TIMESTAMP() THEN 'expired' ELSE 'ok' END AS token_state
 FROM subscription_accounts WHERE status=1;"
```

后台 `RefreshTask` 每 10min 扫描 `expires_at <= now+24h` 的账号预刷新；请求时 token 在 `RefreshSkew`（3min）内过期会同步刷新。`expires_at=0` 会导致每次请求都同步刷新，应尽快补刷一次。

## 五、常见故障与恢复

### 5.1 exchange 返回 `invalid oauth session`

**原因**：`session_id` 不存在或已过期（TTL 5min），或 `state` 不匹配。

**恢复**：重新调 `auth-url` 拿新的 `session_id` / `state`，5 分钟内完成授权并 `exchange`。

### 5.2 exchange 返回 `oauth code exchange failed: status=400`

**原因**：`code` 已被用过（一次性）、`code_verifier` 与 `code_challenge` 不匹配，或授权超时。

**恢复**：重新走一遍 `auth-url` → 浏览器授权 → `exchange`，确保用新 `session_id` 对应的 `code`。

### 5.3 绑定成功但请求 502 / 不走订阅号

**原因**：`hybrid_adaptor.enabled=false`，或 relay-gateway 没读到该配置。

**恢复**：
1. 确认 `configs/relay-gateway.yaml` 里 `hybrid_adaptor.enabled: true`。
2. 确认 relay-gateway 能连 channel-service gRPC（`CHANNEL_GRPC_ENDPOINT`）。
3. 重启 relay-gateway。

### 5.4 Codex 账号请求被上游拒（缺 `chatgpt-account-id`）

**原因**：`exchange` 没拿到 `chatgpt_account_id`（id_token 缺字段且回退接口失败）。

**恢复**：
1. 在订阅账号管理页编辑该账号，把 `account_id` 补成真实的 `chatgpt-account-id`（UUID 形式）。
2. 或重新 `exchange`（确保授权时选了正确的工作区/账号）。

### 5.5 token 长期过期、刷新失败

**原因**：上游 `refresh_token` 失效，或刷新端点不可达。

**行为**：后台刷新失败只记日志，不会自动禁用账号；请求时刷新失败返回 `ErrRefreshFailed`，路由层把该账号当不可用继续 failover 到下一个。

**恢复**：重新走 OAuth 绑定拿新的 `access_token` / `refresh_token`，更新到该 `subscription_accounts` 行（管理后台编辑或 `PUT /v1/subscription-accounts/{id}`）。

### 5.6 fingerprint 格式错

**原因**：手填了不符合 `v2:` 前缀 JSON 快照的指纹。

**恢复**：把 `fingerprint` 清空，首次请求时 `IdentityService.GetOrCreateFingerprint` 会自动生成并回写。

## 六、多副本部署注意事项

OAuth session 存在 channel-service **进程内**（`SessionStore`，TTL 5min），不落 Redis。因此多副本部署时：

- `exchange` **必须回到生成 `auth-url` 的同一 channel-service 副本**。否则 session 不存在，返回 `invalid oauth session`。
- 实现方式：
  - 单副本绑定后再扩容；或
  - 在 admin-api 代理层做 session 亲和（按 `session_id` hash 路由到固定副本）；或
  - 每个副本独立完成绑定（不推荐，会产生多条 `subscription_accounts` 行）。

绑定完成后，`subscription_accounts` 行在数据库里，所有副本共享，后续请求不再依赖 session store。详见 [Redis 多副本部署 Runbook](./subscription-redis-multi-replica-runbook.md)。

## 七、安全注意

- `access_token` / `refresh_token` 是明文存储在 `subscription_accounts` 表，确保数据库访问受控。
- 脚本 `scripts/import-subscription-creds.py` 生成的 payload 文件 `subscription-account.*.payload.json` 含明文 token，已加入 `.gitignore`，确认 `git check-ignore` 命中后再操作。
- `exchange` 对 Codex 会调上游 `settings/user` 关闭训练，属于账号级副作用，知情后使用。
- OAuth `client_id` 是公开值（Claude Code / codex_cli_rs 发布的 client_id），不是密钥。
