# Micro-One-API v0.15.0 发布：订阅账号负载反馈闭环、审计归因链路与前端安全补丁

> 2026-08-04 · 上一版：[v0.14.0](./release-v0.14.0.md)（2026-08-04）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.15.0)

v0.15.0 是 **MINOR 功能版本**，包含 6 个提交（2 feat + 1 fix + 1 test + 1 fix(web) + 1 fix(relay)），完成两件运行时增强：闭合订阅账号 weight 反馈回路（channel selector 的 inflight 计数首次接入 relay 实际占用），以及打通审计平台的 actor / request-id 提取（relay-gateway 与 admin / identity 的敏感操作审计记录从此可归因），并修复前端 4 个 npm 依赖漏洞。

**无数据库迁移、无新增配置项**。新增 1 个 additive proto 字段与 1 个内部 gRPC 方法（`RecordSubscriptionAccountSlot`）。受影响的运行时服务为 `relay-gateway`、`channel-service`、`admin-api`、`identity-service`。

## 变更内容

### 1. 订阅账号负载反馈闭环 —— slot feedback（weight loop）

**根因**：channel 的账号选择器维护一个 per-process inflight 计数器来计算
`loadFactor`（用于 weight 降权），但 relay 的 `Acquire`/`Release` 接缝在生产中
从未被喂入数据——选择器的内存限制器与 Redis fallback 窗口始终拿到零负载反馈，
`loadFactor` 仅靠跨副本 `LoadOracle`（Redis `ZCOUNT`）单维度降权，导致选择可能
持续堆叠在一个忙账号上。

**修复**：

- **channel**：proto 新增 `RecordSubscriptionAccountSlot(account_id, acquired)`；
  `ChannelUsecase.RecordSubscriptionAccountSlot` 转发到选择器的 `Acquire`/`Release`
  （per-process inflight）。`loadFactor` 取 `max(local, crossReplica)`，Redis 与本地
  视图不再重复计数。
- **relay**：新增可选 `SubscriptionAccountSlotReporter` 接口（通过类型断言探测，
  现有 fake 不受影响）；`ChannelAdapter` 实现该接口；
  `executeSubscriptionAccountViaAdaptor` 在 slot 授予时上报 acquire、在每次 slot
  释放时上报 release，**fire-and-forget**，hot path 永不阻塞。
- **测试**：channel usecase 的 slot→loadFactor 降权/恢复；relay adaptor 断言每个
  请求转发 `[acquire, release]`。

**影响服务**：`relay-gateway`、`channel-service`。

### 2. Slot feedback 健壮性 —— 真正的 fire-and-forget + 幂等对

**根因**：首轮实现声称 fire-and-forget 但实际在 relay hot path 上同步发起 gRPC
调用，且 `releaseSlotWithReport` 的限制器释放与 channel 上报不在同一个
`sync.Once` 下，defer 与 transferred result.write 两条控制流可能重复释放。

**修复**：

- `reportSubscriptionAccountSlot` 改为后台 goroutine 派发，带 200ms 专用超时，
  使用 `context.Background()` 以免被取消的请求上下文吞掉 acquire。
- `releaseSlotWithReport` 用单个 `sync.Once` 同时守卫限制器释放与 channel 上报，
  使 (acquire, release) 对在 defer vs transferred 两条控制流间完全幂等。
- 文档化接受的顺序边界（独立 goroutine；只有在调度停顿远超上报超时时 release
  才可能跑在 acquire 前，实际不可能）。
- 测试改为 mutex 守卫的 mock + poll-until-2 超时，因为上报现在是异步到达。

**影响服务**：`relay-gateway`。

### 3. 审计归因链路 —— actor + request-id 提取，接线 admin 与 login 审计

**根因**：审计平台的上下文提取器是 TODO 桩：relay-gateway 的审计中间件给每条
请求都记录了**空 actor 和空 request_id**，使审计日志无法做归因与链路追踪；
admin 退款与 identity 登录这类敏感操作也完全没有审计覆盖。

**修复**：

- **platform/audit**：`extractRequestID` 改读平台 request-ID 中间件上下文
  （`GetRequestID`）；新增标准 actor 上下文键与导出的 `WithActor`/`ActorFrom`，
  作为鉴权层盖戳已校验操作者的唯一入口。
- **mutable actorHolder**：审计中间件注入一个可变 `*actorHolder`，鉴权层在 handler
  链执行期间盖戳、中间件在 `next.ServeHTTP` 返回后读取。Go 的 `*http.Request` 是
 不可变的——handler 里的 `r.WithContext(...)` 对中间件持有的原始 request 不可见，
  纯 context value 方案会让 actor 始终为空。
- **admin**：guard 在两条鉴权路径上都盖戳审计 actor——共享 `ADMIN_TOKEN` 记为
  system/admin-token 哨兵（`ServiceName=admin`），session-token admin 记为数字
  user id + role name；退款 handler 显式记录审计成功/失败事件
  （`payment_order` 资源），携带真实操作者；auditor 经 providerSet + `SetAdminAuditor`
  接线（带 lazy fallback）。
- **identity**：auditor 经 `NewIdentityUsecase(repo, auditor)` 注入（非 lazy 包级
  单例）；`Login` 在成功与失败分支（限流、未知用户、禁用、密码错误）都记录
  `LogUserLogin`，并传入真实 client IP（`x-forwarded-for` / `x-real-ip`，gRPC peer
  兜底）。
- **relay**：`getAuthSnapshot`（`HTTPServer` 与 `EnhancedHTTPServer`）盖戳审计 actor，
  relay 审计记录从此携带真实用户而非 anonymous。
- **SessionID** 为 8 字符展示前缀，永不是完整 token 凭证，避免长效审计日志泄露可用
  会话 token。
- **测试**：audit 提取（actor + request-id 穿过真实中间件）、admin guard 两条鉴权
  路径、identity login 审计。

**影响服务**：`relay-gateway`、`admin-api`、`identity-service`。

### 4. 前端依赖安全补丁 + tsconfig 清理

修复 Dependabot / Trivy 代码扫描告警（均在 `web/package-lock.json`）：

- `hono` 4.12.31 → 4.13.0（CVE-2026-69207，CORS 中间件 ReDoS）。
- `undici` 7.28.0 → 7.29.0（6 条告警：CRLF 注入、缓存泄露、cookie 注入、响应
  desync、解析期崩溃）。
- `fast-uri` 3.1.4 → 3.1.5（CVE-2026-18446，反斜杠 host 混淆）。
- `brace-expansion` 5.0.8 → 5.0.9（CVE-2026-69152，DoS 绕过）。

`hono` 与 `brace-expansion` 为直接依赖（dependencies + overrides 更新）；
`undici` 与 `fast-uri` 为传递依赖（overrides 补条目）。

同时移除已弃用的 TypeScript `baseUrl` 选项（`tsconfig.json` /
`tsconfig.app.json`）。自 TS 5.0 起，`paths` 无需 `baseUrl` 即可相对 tsconfig
位置解析；`tsconfig.app.json` 中的 `ignoreDeprecations: 6.0` hack 一并移除。
`tsc --noEmit` 现在干净通过。

**影响服务**：`admin-api`（前端构建产物）。

### 5. 测试稳定性 —— slot feedback 断言去抖

slot 上报由独立 fire-and-forget goroutine 发出；在负载 CI runner 上 release 可能
先于 acquire 到达，且 2s poll 超时可能在两者刷完前过期。改为断言上报的**集合**
（一个 acquire + 一个 release，任意顺序）并将 poll 超时延长到 5s，固化异步契约。

## 兼容性说明

- **API**：无破坏性变更。新增 additive proto 字段与内部 gRPC 方法
  `RecordSubscriptionAccountSlot`。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.15.0

# cross-build 并部署受影响的四个服务
./scripts/deploy-update.sh relay-gateway channel-service admin-api identity-service
```

## 验证

- 高并发下订阅账号选择不再持续堆叠在单个忙账号（`loadFactor` 由本地 inflight 与
  Redis 双视图取 max 驱动）。
- relay hot path 在 slot 上报时不再阻塞（fire-and-forget 后台 goroutine + 200ms
  超时）。
- relay-gateway / admin-api / identity-service 的审计日志携带真实 actor 与
  request_id，不再为空。
- admin 退款与 identity 登录（成功 / 失败分支）均有审计记录。
- `web/package-lock.json` 的 4 条漏洞告警在 Dependabot / Trivy 中关闭。
- `go build ./...`、`go vet`、`check-architecture.sh`、全部单元测试通过。

## 完整变更日志

- feat(relay,channel): close the subscription-account weight loop (slot feedback)
- fix(relay): make slot feedback truly fire-and-forget + idempotent pair
- feat(audit): resolve actor + request-id extraction, wire admin & login audit
- feat(audit): mutable actor holder, injected auditors, relay actor, session prefix
- test(relay): de-flake slot-feedback assertions for slow CI runners
- fix(web): patch npm vulns (hono, undici, fast-uri, brace-expansion) + remove deprecated tsconfig baseUrl
