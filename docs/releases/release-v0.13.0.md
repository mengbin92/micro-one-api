# Micro-One-API v0.13.0 发布：v0.12.0 生产加固 + 身份/计费安全 + API 增量

> 2026-08-01 · 上一版：[v0.12.0](./release-v0.12.0.md)（2026-07-30）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.13.0)

v0.13.0 是 v0.12.0 发版后的**生产加固版本**。自 v0.12.0 以来共 18 个提交（17 个 `fix`、1 个 `chore`），覆盖身份鉴权、Token 存储安全、计费原子性、订阅有效期、SSE/流式稳定性、SSRF、gRPC 认证、错误信息收敛和数据库迁移兼容性。

本版新增一个公开 gRPC RPC（`identity.v1.ConsumeTokenQuota`）与若干 additive proto 字段；**无端点删除、无字段重命名、无 API 破坏性变更**。数据库迁移新增 `072`–`076`（additive），并修复 `070` 在 MySQL 下的幂等性。

## 变更内容

### 1. 身份与 Token 安全

**Token 明文不再落盘（L6）**

- `tokens` 表新增 `key_hash` 列（迁移 `076`），创建/校验 API Key 改为 HMAC-SHA256 哈希查找，数据库泄露后无法直接使用明文 Key 认证。
- 明文 `key` 列降级为短展示前缀（前 8 + 后 4 字符），UI 搜索不再对 key 做 `LIKE` 匹配。
- 启动时自动回填存量明文 Token：逐行哈希并截断 key，幂等可重入。

**会话撤销（M6）**

- `users` 表新增 `password_changed_at`（迁移 `075`），JWT 内嵌 `pwd_epoch`，改密/重置/登出会撤销旧会话。

**认证链路加固**

- `ValidateToken` / `GetAuthSnapshot` 新增 `client_ip` 字段，支持 Token 子网 CIDR 限制。
- 验证码/重置码改为 out-of-band 投递，不再回显；48-bit、10 分钟 TTL、一次性、尝试次数受限。
- identity 与 billing gRPC 服务挂载 `SERVICE_TOKEN` bearer interceptor，fail-closed。
- `SetUserRole` 操作者身份改从认证上下文解析，不再信任请求字段。
- mTLS `AllowedSubjects` / `AllowedServices` 改为精确匹配，修复 `admin` 误匹配 `not-admin` 的 confused-deputy 问题。
- 登录限流改为 `username@IP`，未知用户走 dummy bcrypt 比较，关闭时序侧信道。

### 2. 计费、账务与订阅

**资金原子性与幂等**

- 所有非订阅生产扣费路径统一走 dual-track CAS 事务，消除 retry 下重复退款窗口。
- `billing_reservations` 新增 `(user_id, request_id)` 唯一索引（迁移 `072`），幂等重入前在事务内重新校验。
- 新增 `actual_cost` 列（迁移 `074`），幂等重入返回真实结算成本，不再返回预扣估算值。
- `MarkOrderRefunded` 改用独立 `refund_reason` 列（迁移 `073`），不再覆盖原始 `provider_payload`。
- redeem code 扣减改为条件 `UPDATE`，消除 count=1 并发超卖。
- Alipay 回调增加 `total_amount` / `app_id` / `seller_id` 交叉校验。

**分层约束与订阅生命周期**

- 修复 `*gorm.DB` 泄漏进 biz 的架构违规：biz 声明不透明 `Tx` / `TxRunner`，data 独占存储驱动实现，18 个 billing + 6 个 domain/subscription 的 `*InTx` 接口全部迁移。
- `SubscriptionExpiryChecker` 正式接入 billing 后台；active 订阅读取增加 `expires_at > now` 防御性过滤。
- 订阅状态/有效期更新改为窄字段写入，不再覆盖并发 `AddUsage` 的用量/窗口增量。
- OAuth refresh token 解析出 `invalid_grant` 后返回类型化错误，停止对已撤销 refresh token 的无效重试。

### 3. Relay、平台与流式稳定性

- 所有上游响应/错误体读取增加上限（成功 128MB、错误 1MB），修复恶意/异常上游 OOM 风险。
- 流式请求改用无 `Client.Timeout` 的专用 HTTP client，SSE 长连接不再被 30s 整体超时中断。
- 熔断器改为真实错误比例、`minRequests=10` 才跳闸，并实现 half-open 探测恢复。
- 非重试客户端错误不再把 identity/channel/billing breaker 打成 open；breaker 只对网络/超时/不可用等 retryable 失败累积。
- 4 个生产 ResilientClient 补齐类型化 fallback，调用方可 `errors.Is` 判断 `ErrCircuitBreakerOpen`。
- 上游错误统一收敛为 `UpstreamHTTPError`，HTTP 5xx 不再向客户端泄漏原始错误详情。
- 审计中间件补齐 `http.Flusher` / `Unwrap`，SSE/streaming 在 audit 开启时仍可正常 flush。
- StreamEventBus 使用 per-service consumer group，每个服务拿到独立事件副本；handler 失败时不再无条件 XACK。
- 全局 logger 改为 atomic pointer，修复并发读写 race；OTLP exporter shutdown 增加 5s 超时。

### 4. Admin、运维与 CI

- admin SPA fallback 改为 NotFoundHandler 统一返回 index.html，删除 34 项手工路由镜像；新增前端路由无需再同步后端。
- 约 19 处 `err.Error()` 泄漏改为 `sanitizeAdminError`，内部表名/列名/路径不再暴露给前端。
- `safefile` 增加 `allowedRoots` 沙箱边界与 `..` 穿越防护，支付私钥读取路径加固。
- 新增 pre-push gosec gate，代码扫描问题在推送前本地拦截。
- 修复 gitleaks 对公开 Claude OAuth client ID 的误报，并保留来源说明。

## API 变更

本版**新增端点**：

- `identity.v1.IdentityService.ConsumeTokenQuota`

本版**新增 additive 字段**：

- `identity.v1.ValidateTokenRequest.client_ip`
- `identity.v1.GetAuthSnapshotRequest.client_ip`

无删除、无重命名、无类型变化，旧客户端可平滑升级。

## 数据库迁移

新增迁移：

| 迁移 | 归属 | 内容 |
|---|------|------|
| `072_add_unique_request_id_billing_reservations` | billing | `(user_id, request_id)` 唯一索引 |
| `073_add_refund_reason_to_payment_orders` | billing | `payment_orders.refund_reason` |
| `074_add_actual_cost_to_billing_reservations` | billing | `billing_reservations.actual_cost` |
| `075_add_password_changed_at_to_users` | identity | `users.password_changed_at` |
| `076_add_key_hash_to_tokens` | identity | `tokens.key_hash` + 索引 |

同时将 `070_clean_orphan_model_mappings` 改为 MySQL 幂等写法：先检查 `information_schema`，FK 已存在时不再重复执行 `ADD CONSTRAINT`。

以上均为 additive 变更，可按 per-service schema 在 MySQL 容器内直接执行；部署前建议先备份数据库。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.13.0

# 开发者环境：重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：先备份数据库，再应用迁移、重建镜像并滚动重启
make migrate
docker compose build
docker compose up -d
```

## 兼容性说明

- **API**：无破坏性变更。新增 `ConsumeTokenQuota` 与 `client_ip` 字段，旧客户端不受影响。
- **数据库**：新增迁移 `072`–`076` 均为 additive；`070` 修复为幂等，已存在约束的环境可直接重放。
- **配置**：identity Token 哈希使用 `TOKEN_HASH_KEY`，未配置时回退 `JWT_SECRET_KEY`；轮换哈希密钥会使存量 Token 失效，这是预期 break-glass 行为。
- **安全语义**：Token 明文回填完成后，DB dump 不再能直接认证；密码修改/重置/登出会使旧会话失效。

## 验证

发布前已确认：

- `go build` / `go vet` / `gofmt` 通过
- billing + domain/subscription 事务与幂等测试全绿
- identity Token 哈希、回填、会话撤销、子网限制测试全绿
- platform/relay breaker、SSE、审计、日志 race 修复测试全绿
- gosec 非生成代码 0 issue；gitleaks 提交范围扫描 0 finding
- 迁移 `070` / `072` 在 MySQL scratch schema 中连续执行两次通过

## 完整变更日志

- 27e29ab fix(security): allow public Claude OAuth client id in gitleaks
- 490ac01 fix: resolve gosec findings and MySQL migration compatibility
- ced1d4a fix: resolve code review findings
- cfde9da fix(domain,billing): replace *gorm.DB in biz interfaces with Tx abstraction (domain-L1/billing-M6)
- 896df29 fix(identity): hash access-token keys (HMAC-SHA256) so a DB leak cannot authenticate as any user (L6)
- 1515948 fix(pkg): safefile allowedRoots sandbox enforcement + payment secret traversal guard (M1/L1)
- 8f8e4e7 fix(admin): SPA fallback-to-index eliminates route mirror, sanitize internal errors (H1/M1)
- 7253d2f fix(channel): true-ratio circuit breaker with minRequests + half-open, OAuth doc, deobfuscated client_id, UTC now() seam (H1/M1/M2/L1)
- f8151c3 fix(identity): token subnet enforcement, session revocation, scoped idempotency, ratelimit race/window, mTLS hardening (M1/M2/M3/M4/M6/L2/L3/L4/L5/L7)
- 17e1e02 fix(platform): breaker client-error isolation, audit SSE flush, streams broadcast/ACK, atomic logger (H1/H2/M1-M6/L1-L11)
- bfc18e9 fix(relay): cap upstream bodies, typed breaker fallback, sanitize errors, untrack binary (C1/H1/H2/M1/M2/L1/L2)
- f664df9 fix(domain): SSRF coverage, token persistence, typed upstream errors, stream hardening (M1-M5/L2-L6)
- 792f638 fix(domain): narrow subscription writes, surface invalid_grant, split stream client (H1/H2/H3)
- a7005d6 fix(domain): wire expiry checker + expires_at guard on active-subscription reads (C1)
- 809596e fix(billing): money-atomicity + idempotency hardening for reservations, orders, and reconciliation (C1/C2, H1/H2/H3, M1/M2/M3/M5, L1-L6)
- 5256d5f fix(identity): harden auth — C1/C2 code delivery, gRPC auth, quota, pagination, registration group, current-password gate
- 0ca3480 chore(ci): add pre-push gosec gate
- 86b8f6e fix(admin,channel): clear gosec code-scanning alerts (G115 x2, G118)

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
