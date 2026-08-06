# Changelog

All notable changes to `micro-one-api` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.16.0] - 2026-08-06

v0.15.3 之后的 MINOR 功能版本（7 个提交），标志着 v0.11.0 路线图收尾阶段（P0–P3）全部完成：
routing-ops 双源指标降级（Prometheus → relay-gateway 直采）作为唯一面向用户的新功能，
P1 契约加固（同优先级精确回退 + 并发 active 唯一约束）补齐确定性回归测试，cache-creation
计费从 observe 切换为 charge 的生产闭环，以及 6 个服务 conf 包测试等工程卫生工作。
无 API 破坏性变更、无数据库迁移、无 proto 变更。详见 [release-v0.16.0.md](docs/releases/release-v0.16.0.md)。

### Added

- **feat(admin): P2.3 routing-ops dual-source metrics — relay-gateway scrape fallback**：当 Prometheus 不可用时，admin-api 自动降级为直接 scrape relay-gateway 的 `/metrics` 端点（`expfmt` 解析 exposition format，聚合 cumulative counter），保持 routing-ops 视图 `partial=false`。`RoutingRates` 新增 `Source` 字段，JSON 新增 `source`/`cumulative`；docker-compose 内置 `RELAY_METRICS_ENDPOINT` 默认值；`prometheus/common` 从 indirect 提升为 direct。10 条回归测试覆盖双源优先级、降级、全故障路径。影响 admin-api。

### Changed

- **chore(p3): clean up billing_model TODO + update TODO.md status**：`internal/biz/billing_model.go` 的 `channel_mapped ≡ upstream` 限制从 TODO 转为永久 NOTE（结构性变更超出当前计费模型）；`docs/TODO.md` 回写 P0–P3 全部完成状态。

### Fixed

- **docs(v0.16): consolidate v0.16 roadmap + fix mock race + dedup comment**：新增 `docs/design/v0.16-roadmap.md` 作为 P0–P3 收尾文档，替换各文档中的 `.workbuddy` 临时链接；修复 `mockConcurrentCreateRepo` 竞态（补 `GetActiveSubscriptionByUser` 加锁）；删除 `routing_rates.go` 重复注释行。

## [0.15.3] - 2026-08-06

v0.15.2 之后的 PATCH 版本（3 个提交），内容为内部重构与代码规范，无对外行为变更：将全仓库
JSON 序列化统一收敛到 `pkg/jsonx` 单一封装层（底层 sonic `ConfigStd`，保持 `encoding/json`
语义——HTML 转义、map key 排序、字符串拷贝），第一步迁移 52 个非测试文件的
`encoding/json`→`jsonx`（含升级 sonic 至 v1.15.2，唯一保留 `bodylimit.go` 因依赖 sonic 未暴露
的类型断言），第二步替换 53 个热点文件的直接 `sonic.*`/`sonic.ConfigStd.*` 调用、补齐
`jsonx.Get` 与 `AGENTS.md` JSON 策略章节，第三步一次纯机械 `gofmt -w` 收尾 50 个不规范文件。
无 proto 变更、无数据库迁移、无新增配置项。详见 [release-v0.15.3.md](docs/releases/release-v0.15.3.md)。

### Changed

- **refactor(json): replace encoding/json with sonic via pkg/jsonx wrapper**：扩展 `pkg/jsonx` 为 `encoding/json` 的 drop-in 替代（`sonic.ConfigStd`），迁移 `app/*`、`internal/*`、`domain/*`、`platform/*` 共 52 个非测试文件；升级 `github.com/bytedance/sonic` 至 v1.15.2；`platform/middleware/bodylimit.go` 保留 `encoding/json`（依赖 sonic 未暴露的类型断言）。影响全部后端服务。
- **refactor(jsonx): route all sonic calls through pkg/jsonx wrapper**：将 `internal/server`、`internal/apicompat`、`internal/adaptor`、`internal/biz`、`internal/identity`、`domain/upstream/provider/*`、`app/{channel,config,log,monitor,notify}`、`platform/{events,middleware/idempotency}` 共 53 个文件的直接 `sonic.*`/`sonic.ConfigStd.*` 替换为 `jsonx.*`；新增 `jsonx.Get`（封装 `sonic.Get`）与 `pkg/jsonx/json_test.go`（与 `encoding/json` 一致性 + 基准）；`AGENTS.md` 新增 JSON 序列化策略章节。影响全部后端服务。
- **style(gofmt): format 50 noncompliant Go files across the repo**：纯机械 `gofmt -w` 扫描——import 分组排序、结构体字面量字段对齐、闭包体重新缩进、单行函数展开、多余空行清理。无语义改动。`gofmt -l` 全仓库 clean。

## [0.15.2] - 2026-08-05

v0.15.1 的 PATCH 修复版本（3 个提交），全部位于 relay-gateway：修复 v0.15.1
渠道统计去噪因 `applyPlanInputs` 赋值顺序反转而完全失效的回归（`SourceKind`
恒空、`UpstreamModelID` 被写成字面量 "subscription"）；修复订阅来源 Anthropic
协议上游（如 kimi）流式响应的三个缺陷——`data:` 无空格行被静默丢弃、上游中途
断连缺 `[DONE]` 哨兵、`message_start` 前关闭无终止事件——导致 codex 报
"stream disconnected before completion"；并将 `response.completed` 之后的
`response.failed` 守卫补齐到 adaptor 路径，消除矛盾双终止。无 API 破坏性变更、
无数据库迁移。详见 [release-v0.15.2.md](docs/releases/release-v0.15.2.md)。

### Fixed

- **fix(relay): applyPlanInputs reversed source-kind/upstream-model assignment**：按正确顺序接收 `upstreamCostKeyInputsFromPlan` 的 `(sourceKind, upstreamModelID)` 返回值；订阅流量恢复跳过 channel 维度统计，规范化计费 cost key 恢复正常。影响 `relay-gateway`。
- **fix(relay): ensure terminal SSE event on upstream stream interruption**：`sseData` 同时接受 `data:` / `data: ` 两种形式；`scanner.Err()` 分支在终止事件后追加 `[DONE]`；上游在 `message_start` 前关闭时合成 `response.failed`。修复同时应用于 adaptor 路径与 fallback 路径，含回归测试。影响 `relay-gateway`。
- **fix(relay): guard adaptor-path scanner error against completed+failed**：`pumpAnthropicToResponses` 镜像 `CompletedSent` 守卫，已完成的流只追加 `[DONE]`，不再叠加 `response.failed`。影响 `relay-gateway`。

## [0.15.1] - 2026-08-05

v0.15.0 的 PATCH 修复版本（2 个提交）：收尾订阅变更链路的 M6 缺陷——换组用量窗口
仅在真正跨组时重置（同组改套餐保留已跑用量，避免丢数据与免费刷新配额），并为
`ChangeSubscription` 增加行锁串行化（`SELECT ... FOR UPDATE`）防止并发变更互相覆盖
写回；同时让 relay-gateway 跳过订阅来源流量的 channel 维度用量统计（合成 ChannelID
导致 channel-service 刷 "channel not found" 告警噪声）。无 API 破坏性变更、无数据库
迁移。详见 [release-v0.15.1.md](docs/releases/release-v0.15.1.md)。

### Fixed

- **fix(subscription): M6 - reset usage only on group change + row-locked ChangeSubscription**：用量窗口重置改为条件触发（仅 `ToGroupID != fromGroupID` 才清零）；`SubscriptionUsecase` 新增可选 `TxRunner`，`ChangeSubscription` 在 `RunInTx` 内用 `GetByIDInTx`（`SELECT ... FOR UPDATE`）+ `UpdateSubscriptionFieldsInTx` 串行化并发变更；admin-api 接线 `NewTxRunner(repo)`；`subscription_name` 仅在请求实际改变时窄写。影响 `admin-api`、`billing-service`。
- **fix(relay-gateway): 跳过订阅流量的渠道用量统计**：新增 `recordChannelUsageFromDetail` helper，对 `SourceKind == "subscription"` 直接跳过并记 `skipped_channel_stats` 指标，channel 来源保持原逻辑。影响 `relay-gateway`。

## [0.15.0] - 2026-08-04

MINOR 功能版本（6 个提交）：闭合订阅账号 weight 反馈回路（channel selector 的
per-process inflight 计数首次接入 relay 实际占用，`loadFactor` 取
`max(local, crossReplica)`），打通审计平台 actor / request-id 提取（relay-gateway
与 admin 退款 / identity 登录的敏感操作审计记录从此可归因，mutable `*actorHolder`
解决 Go 不可变 request 导致 actor 为空的问题），修复前端 4 个 npm 依赖漏洞
（hono / undici / fast-uri / brace-expansion）。无数据库迁移、无 API 破坏性变更。
详见 [release-v0.15.0.md](docs/releases/release-v0.15.0.md)。

### Added

- **feat(relay,channel): close the subscription-account weight loop (slot feedback)**：channel proto 新增 `RecordSubscriptionAccountSlot`；relay 通过可选 `SubscriptionAccountSlotReporter` 接口在 slot 授予/释放时 fire-and-forget 上报，选择器 `loadFactor` 取 `max(local, crossReplica)`。影响 `relay-gateway`、`channel-service`。
- **feat(audit): resolve actor + request-id extraction, wire admin & login audit**：`extractRequestID` 改读平台中间件；新增 `WithActor`/`ActorFrom` 标准上下文键；admin guard 两条鉴权路径盖戳 actor，退款 handler 显式审计；identity Login 成功/失败分支记录 `LogUserLogin` 并传真实 client IP。
- **feat(audit): mutable actor holder, injected auditors, relay actor, session prefix**：审计中间件注入可变 `*actorHolder` 解决 Go 不可变 request 问题；relay `getAuthSnapshot` 盖戳真实用户；identity auditor 注入 usecase；SessionID 取 8 字符前缀避免泄露会话 token。

### Fixed

- **fix(relay): make slot feedback truly fire-and-forget + idempotent pair**：`reportSubscriptionAccountSlot` 改后台 goroutine + 200ms 超时 + `context.Background()`；`releaseSlotWithReport` 用 `sync.Once` 守卫释放+上报幂等对。影响 `relay-gateway`。
- **fix(web): patch npm vulns (hono, undici, fast-uri, brace-expansion) + remove deprecated tsconfig baseUrl**：hono CVE-2026-69207 ReDoS、undici 6 条注入/desync、fast-uri CVE-2026-18446 host 混淆、brace-expansion CVE-2026-69152 DoS；移除弃用的 TS `baseUrl`。影响 `admin-api` 前端产物。

### Changed

- **test(relay): de-flake slot-feedback assertions for slow CI runners**：slot 上报断言改为集合校验（acquire + release 任意顺序）+ 5s poll 超时。

## [0.14.0] - 2026-08-04

MINOR 版本（4 个提交）：为订阅续费链路补齐 `renewal_strategy` 可观测字段（迁移
`077`，additive）、修复 admin 延长订阅的并发写 clobber 与过期激活缺陷、为 Redis
故障态并发语义补充多副本断言并文档化 fail-open 权衡，完成 code-review L 系列核验。
1 个 additive 数据库迁移。详见 [release-v0.14.0.md](docs/releases/release-v0.14.0.md)。

## [0.13.3] - 2026-08-03

v0.13.2 的 PATCH 修复版本（7 个提交）：订阅购买完成幂等性（claim-before-fulfil，
M10 资金相关）、relay 上游限流（429/423/529）不再误触发断路器、admin 补偿失败错误
透出与卡单检测、CI 矩阵/缓存/多架构推送加固。无 API 破坏性变更、无数据库迁移。
详见 [release-v0.13.3.md](docs/releases/release-v0.13.3.md)。


## [0.13.2] - 2026-08-02

v0.13.1 的 PATCH 修复：billing-service 异步结算分账恢复路径的空指针 panic 导致
crash loop（线上观测到约 1274 次重启），表现为 relay-gateway 的 `ReserveQuota`
gRPC 间歇失败、客户端收到空消息体的 402；以及迁移文件默认旧版共享 DB 历史导致
全新 MySQL / PostgreSQL / SQLite 无法干净建库。详见
[release-v0.13.2.md](docs/releases/release-v0.13.2.md)。

### Fixed

- **fix: resolve intermittent 402 caused by billing-service crash loop**：`ledger_repo.FindByDedupeKey` 在 tx 为 nil 时回退到 `r.data.DB` 不再 panic；`async_billing.processSettlement` 增加 `defer/recover` 防止单任务 panic 击穿 worker；`gatewayErrorMessage` 对每个状态码返回非空客户端安全文案（如 402 → insufficient quota）。影响 `billing-service` 与 `relay-gateway`。
- **fix(migrations): make clean-room DB provisioning work for all drivers**：`061`/`031`/`067` 改为按列/表/schema 存在性守卫（prepared statement，可重入），修复全新 MySQL 单库与 per-service schema 建库失败；`schema_split.sql` 从自动应用迁移中排除（仅作参考 DDL）；`ownership.yaml` 为 log 补 `016`、为 billing 补 `031`、移除 `037`；Postgres 基线 `000` 修复表约束内非法 `COLLATE`；SQLite runner 落实「duplicate column name 幂等 no-op」契约并拆分多列 ALTER（`009`）。

## [0.13.1] - 2026-08-01

v0.13.0 的 PATCH 修复：identity-service 调用 billing-service 的 gRPC 客户端补齐
`SERVICE_TOKEN` 携带，与 billing fail-closed 鉴权对齐；`SERVICE_TOKEN` 为空时服务
启动失败而非带病运行。详见 [release-v0.13.1.md](docs/releases/release-v0.13.1.md)。

### Fixed

- **fix(identity): attach SERVICE_TOKEN to billing gRPC client**

## [0.13.0] - 2026-08-01

生产加固版本（18 个提交）：身份/Token 安全（key 明文改 HMAC-SHA256 哈希、会话撤销、
子网限制、SERVICE_TOKEN gRPC 鉴权）、计费原子性与幂等（dual-track CAS、request_id
唯一索引、actual_cost、refund_reason、兑换码条件更新）、relay/流式稳定性（上游体
上限、SSE 无超时、真实错误比例熔断、类型化 fallback）、错误信息收敛。新增
`identity.v1.ConsumeTokenQuota` RPC 与 additive proto 字段；迁移 `072`–`076`
（additive），修复 `070` MySQL 幂等性。详见 [release-v0.13.0.md](docs/releases/release-v0.13.0.md)。

## [0.12.0] - 2026-07-30

v0.11.0 生产加固与功能补全：落地 v0.11.0 代码评审全部 CRITICAL/HIGH/MEDIUM/LOW
修复（WS 连接池跨命名空间串号、故障转移提前终止、告警永不触发、fallback 成本键错配
等），采纳 sub2api 四项更优实现，新增 Prometheus + Grafana 可观测性监控栈。迁移
`070`–`071`（additive）。详见 [release-v0.12.0.md](docs/releases/release-v0.12.0.md)。

## [0.11.0] - 2026-07-28

核心能力版本：`cache_creation` 全链路计费（五桶 token 语义、observe/charge 开关、
影子成本）、模型规范 ID 治理（canonical ID + 大小写不敏感唯一约束 + 未定价审计）、
统一路由可观测性（订阅账号 weight、selection metrics、routing-ops 运营视图与告警）、
模型版本化导入导出。迁移 `068`–`069`（additive）。详见 [release-v0.11.0.md](docs/releases/release-v0.11.0.md)。

## [0.10.2] - 2026-07-27

问题修复版本：修复 v0.10.0/0.10.1 暴露的模型路由问题（API-key 渠道与订阅账号优先级
硬编码、上游模型 ID 精确映射、GLM Responses 自定义工具 `input_schema` 422）。
详见 [release-v0.10.2.md](docs/releases/release-v0.10.2.md)。

## [0.10.1] - 2026-07-26

PATCH：修复国内订阅账户路由无法命中、上游模型 ID 大小写不敏感匹配丢失，以及 gosec /
Dependabot 安全告警。详见 [release-v0.10.1.md](docs/releases/release-v0.10.1.md)。

## [0.10.0] - 2026-07-25

重大功能版本：独立模型管理系统（账户模型映射、通配符匹配、模型别名、使用统计）与
国内订阅账户支持（智谱 GLM / MiniMax / Kimi：动态模型发现、配额查询、路由恢复探测）。
详见 [release-v0.10.0.md](docs/releases/release-v0.10.0.md)。

## [0.9.3] - 2026-07-20

基础设施升级：Kratos v2 → v3 全量升级（9 服务 + platform 共享库），proto 工具链从
protoc 迁移到 buf（生成与 lint，CI 与 Docker 构建同步接入）。
详见 [release-v0.9.3.md](docs/releases/release-v0.9.3.md)。

## [0.9.2] - 2026-07-19

PATCH：修复启用异步 Billing 与 Schema 隔离后出现的 token 扣费异常。
详见 [release-v0.9.2.md](docs/releases/release-v0.9.2.md)。

## [0.9.1] - 2026-07-19

PATCH：完成 Phase 2.4 Schema 隔离生产启用，修复配置问题并同步本地部署配置。
详见 [release-v0.9.1.md](docs/releases/release-v0.9.1.md)。

## [0.9.0] - 2026-07-18

落地架构重构 Phase 2.1/2.2/2.3/2.4/2.5 与 Phase 3.3：异步计费、渠道加权选路、日志
批量写入、per-service schema 隔离、配置热更新、WebSocket 优雅排空。
详见 [release-v0.9.0.md](docs/releases/release-v0.9.0.md)。

## [0.8.2] - 2026-07-17

v0.8.2 是 v0.8.1 之后的 PATCH 版本，聚焦 `internal/server/http.go` 架构重构拆分与 Phase 0 可观测性基线填充。无 API 破坏性变更、无数据库迁移、无部署配置变更。详见 [release-v0.8.2.md](docs/releases/release-v0.8.2.md)。

### Changed

- **`internal/server/http.go` God Object 拆分**：将 2470 行的主体文件按职责拆分为 13 个聚焦文件（Forwarder / BillingCoord / 按端点的 Handler / 响应与辅助工具），主体降至 472 行。属纯内部重构，无新增/删除端点、无路由变更、无响应格式调整。这是架构重构路线图 Phase 1 的最后一个 P0 项。
- `docs/design/BASELINE.md` 填充全部 16 处 TBD 基线数据（端点延迟、gRPC 调用延迟、缓存命中率、熔断器状态），原始压测结果归档至 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`。
- `docs/TODO.md` 标记 http.go 拆分与 Phase 0 基线填充任务完成。

## [0.7.0] - 2026-07-12

v0.7.0 是 v0.6.1 之后的 Kratos 大仓结构迁移版本。范围覆盖 `v0.6.1..v0.7.0` 共 9 次提交、560 个文件、+8.6k/-3.2k 行。本版为纯结构性重构，不涉及 API 破坏性变更，不新增数据库迁移。

### Added
- 新增 `app/` 大仓结构：8 个子服务（admin/billing/channel/config/identity/log/monitor/notify）各含独立 `cmd/`、`internal/`、`configs/config.yaml`、`Dockerfile` 和 `Makefile`，共用根 `go.mod`。
- 新增 `platform/` 基础设施层：15 个基础设施包（audit/cache/config/database/events/grpc/http/logging/metrics/middleware/registry/security/tls/tracing/websocket），从原 `internal/pkg/` 提取。
- 新增 `domain/` 共享域库：`domain/subscription`（订阅域 biz+data，admin/billing/relay 共享）和 `domain/upstream`（上游 provider+credential），含 `domain/subscription/README.md` 边界与所有权说明。
- 新增 `scripts/check-architecture.sh` 架构边界守卫：7 条层级依赖规则（service→data、biz→service、biz→data、biz→DTO、data→service、data→DTO）+ wireinject 编译检查，集成到 CI。
- 新增 `AGENTS.md` 仓库级编码规约：DTO/DO/PO 三层模型与层间依赖箭头规则、加资源清单和测试策略。
- 新增 per-service `Makefile`（`make wire` / `make wire-check` / `make build`）和 root Makefile `wire` / `wire-check` target。
- 新增 admin `biz` 层（`SystemOption` DO、`SystemOptionsRepo` interface、`SystemOptionsUsecase`），将原 `service.SystemOptionsStore` 重构为符合 DTO→DO→PO 分层。

### Changed
- **Kratos 大仓结构迁移**：relay-gateway 保留为根 `cmd/relay-gateway/` + `internal/`；其余 8 个服务迁移到 `app/<service>/cmd/<service>/` + `app/<service>/internal/{biz,data,server,service}`。
- **配置布局重构**：原 `configs/<service>.yaml` 删除，各服务改为 `app/<service>/configs/config.yaml`；relay-gateway 使用根 `configs/config.yaml`。所有 Dockerfile 设 `ENV CONF_PATH=/configs/config.yaml`。
- **Dockerfile 拆分**：原根 Dockerfile 保留用于 relay-gateway，新增 8 个 `app/*/Dockerfile` 每服务独立构建；`deployments/docker/Dockerfile` 同步更新。
- `api/relay` 重命名为 `api/relay-gateway`；`internal/config` 重命名为 `internal/conf`。
- 导入路径更新：234 处 Go import 路径跨 132 文件更新以匹配新结构。
- `make wire` 全量重生成 9 个 `wire_gen.go`，完全可复现。
- `check-architecture.sh` 更新为支持 flat 结构 + root `internal/`。
- CI 工作流更新：Docker matrix 使用 `include` + path，新增架构检查 step 和生成文件新鲜度验证 step。

### Fixed
- 修复前端 web API 502/404 错误：`SubscriptionPlansPage.tsx` 移除冗余 `/api` 前缀（baseURL 已含 `/api`）；`http.go` 路由顺序修正（`operation-report` 在 prefix handler 前注册）；新增缺失的 `/admin/subscription-plans` SPA 页面路由。
- 修复 `go vet` lock-copy 告警：proto message 含 `sync.Mutex` 不可按值拷贝，改用指针。
- 修复 `go vet` "using resp before checking for errors" 告警：先捕获 err 再解引用 resp。
- 修复 relay-gateway `wire.go`：`newApp()` 返回 error，启动期 discovery/registrar/gRPC client/cache/subscription repo/mTLS 错误正确传播，mTLS 改为 fail-closed。
- 修复 admin `http_test.go`：适配 `service.SystemOptionsStore` → `biz.SystemOptionsRepo + SystemOptionsUsecase` 重构。
- Docker Compose 新增 `SSL_CERT_FILE` 环境变量和 `ca-certificates` 卷挂载，修复 relay-gateway 容器内上游 HTTPS 调用。

## [0.6.1] - 2026-07-09

### Fixed
- 修复 admin logo 静态资源 404（`8c6da60`）。
- 刷新 v0.6.0 品牌和 README（`76628b8`）。

## [0.6.0] - 2026-07-09

### Added
- 新增 `/v1/subscription/usage` API：返回用户订阅额度、已用量、剩余额度和下一次刷新时间。
- 新增管理后台订阅套餐管理页（`SubscriptionPlansPage.tsx`），支持套餐创建、上下架、价格与有效期配置。
- 支付订单新增 `plan_snapshot`，订单发放与后续套餐上下架/删除解耦。
- 新增订阅订单续费幂等、退款撤销/缩短订阅、订单到订阅确定性关联。
- 新增订阅账号治理自动化：fixed 策略 daily/weekly 额度重置、账号恢复、额度告警、治理指标与 runbook。
- 新增 Relay 订阅路径压测脚本、报告生成与压测 runbook。
- 新增单用户单 active 订阅数据库唯一约束（`059`）。

### Changed
- 订阅额度刷新锚点修正为自然日/自然周窗口，并返回 next-refresh times。
- 订阅运营报表新增 usage/fallback ratio。
- 默认订阅用户 RPM 限制保持关闭。

### Fixed
- 支付提交阶段吸收实际订阅用量，避免预估用量和最终账本不一致。
- 修复退款路径在用户已购买新订阅后错误回退到"当前 active 订阅"的风险。
- 修复并发创建 active subscription 时可能绕过业务层检查的问题。

### Security
- gosec SAST：0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks secret scan：0 leaks。
- 临时 replace `github.com/go-kratos/kratos/v2` 到包含 CVE-2026-6993 修复的提交。

## [0.5.0] - 2026-07-05

### Added
- 新增订阅套餐数据层与购买发放流，支持通过 `subscription_plans` 管理套餐并在支付订单成功后分配订阅权益。
- 订阅账号新增本地额度、5h 额度、RPM、会话窗口、额度重置配置和批量额度管理能力。
- 订阅账号额度事件增加幂等记录与聚合分析，管理后台可查看额度事件和账号额度状态。
- relay-gateway 新增 Redis-backed 订阅账号并发控制，并在多副本部署下共享账号并发槽位。
- 管理后台新增用户 RPM 限制展示、订阅账号额度/RPM/窗口控制项和成本分析维度补充。

### Changed
- Docker 构建新增自包含 `go-deps` stage，CI buildx matrix 不再依赖本地 `micro-one-api/go-deps:latest` 镜像。
- `scripts/deploy.sh` 增加依赖镜像 hash tag、可选并行构建和更稳的服务构建流程。
- `scripts/test-e2e-flow.sh` 保留 compose 实时输出，并在 compose 健康检查竞态时重试一次。

### Fixed
- 修复 GitHub Actions Docker matrix 因尝试拉取不存在的 `micro-one-api/go-deps:latest` 而全部失败的问题。
- 修复 gosec G115/G101/G705 告警，新增安全窄化转换工具并标注确认安全的误报点。
- 修复 gitleaks 历史占位符命中，当前文档示例改用环境变量并为确认误报 fingerprint 建立 `.gitleaksignore`。
- 修复订阅账号额度状态展示不完整和用户 RPM 限制展示缺失的问题。

### Security
- gosec SAST：0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks secret scan：0 leaks。
- 临时 replace `github.com/go-kratos/kratos/v2` 到包含 CVE-2026-6993 修复的提交,规避 Kratos `http.DefaultServeMux` confused deputy 告警。

## [0.4.0] - 2026-07-04

### Added
- relay-gateway Responses 路径新增 §5 多层调度:优先复用 `previous_response_id` route,其次复用 `session_hash` sticky channel,最后回退到原 `RelayUsecase.Plan`。
- Responses HTTP/WS sticky session 支持 `session_hash` / `sessionHash` body 字段和 `X-Session-Hash` / `OpenAI-Session-Hash` header,并在 Redis sticky store 中使用独立 `openai_ws_session:` namespace。
- relay-gateway 订阅账号路径新增 §6 `AccountPool` + `RuntimeBlocker` + FailoverLoop:订阅账号选号会跳过运行时熔断账号,subscription adaptor 在上游网络错误、`429`、`5xx` 时短 TTL 熔断当前账号并切换下一个账号重试。
- §7 新增 Codex 5h/7d 配额快照解析与 `account_quota_snapshots` 落点,relay-gateway 可从 Codex 上游响应记录 quota snapshot 并在阈值耗尽时自动暂停订阅账号。
- §7 新增 channel-service 订阅账号 OAuth 授权码绑定端点:支持 Claude/Codex `auth-url` 与 `exchange`,使用进程内 5 分钟 session_store,exchange 后创建 `subscription_accounts` 记录。
- §8 新增订阅系统 Prometheus 指标:覆盖业务订阅配额检查/用量回写、subscription adaptor 请求、failover、runtime block、上游错误透传和 Codex quota snapshot/auto-pause。

### Fixed
- `previous_response_id` 解析拒绝 `msg_` message id,避免把 message id 误当 Responses route id。
- 订阅账号上游 `401` / `403` / `429` / `cyber_policy` 错误改为按 §7 ErrorPassthrough 透传状态码、body 与 `Retry-After`,不再统一包装成网关错误。
- compose E2E mock channel 状态修正为 `ChannelStatusEnabled = 1`,避免模型列表可见但 relay 选号过滤后 chat completion 返回 500。
- `phase1_indexes.sql` 不再重复创建 `billing_ledgers.idx_created_at`,避免干净 MySQL volume 首次初始化时因重复索引中断。
- 订阅套餐支付宝支付成功后由 `billing-service` 自动发放 `user_subscriptions`,并阻止订阅订单在未配置发放器或缺少 `group_id` 时被误标为 `issued`。

## [0.3.1] - 2026-06-29

### Added
- 新增 SQLite3 Lite 部署模式：`deployments/docker-compose/docker-compose.lite.yml`、`.env.lite.example`、`migrations/sqlite/000_create_full_schema.sql`，单机部署可不再启动 MySQL 容器。
- 新增 Postgres 部署模式：`deployments/docker-compose/docker-compose.postgres.yml`、`.env.postgres.example`、`migrations/postgres/000_create_full_schema.sql`。
- 新增统一数据库打开器 `internal/pkg/xdb.Open` / `OpenSQL`，支持 `mysql`、`sqlite3`、`postgres` 三种方言，并支持从 DSN 推断 driver。
- 新增 SQLite3/Postgres 迁移目录说明与 Issue #4 落地文档。

### Changed
- 各服务数据库配置改为通过 `DATABASE_DRIVER` 选择方言，默认保持 MySQL 兼容。
- `cmd/migrate` 支持按 driver 选择表存在性探测与 Postgres `$N` 占位符转换。
- 主服务 Docker 镜像切换为 CGO-enabled Alpine 构建，以支持 `go-sqlite3`。
- MySQL 分区维护在 SQLite3/Postgres 下自动 no-op。

### Fixed
- 修复 `admin-api` system options 在 SQLite3/Postgres 下的连接、占位符与 upsert 兼容性。
- 修复 billing/log 聚合查询中的 MySQL 专用日期函数，使其兼容 SQLite3/Postgres。
- 修复 Postgres baseline 中 `time.Time` 字段类型与 GORM 模型不一致的问题。

## [0.3.0] - 2026-06-29

### Added
- **混合中转网关**:relay-gateway 新增 `Adaptor` 抽象层,把 Codex/Claude OAuth 等订阅账号与 30+ 厂商 API Key 通道统一接入,内含 `apicompat` 四格式转换矩阵(Anthropic ⇄ Responses ⇄ ChatCompletions,含流式 SSE 状态机)与 `identity` 指纹/伪装(metadata.user_id 重写、anthropic-beta 计算、fingerprint 注入)。
- **订阅账号端到端**:`subscription_accounts` 表 + 5 个 admin RPC(列表/创建/更新/删除/启停),管理后台新增「订阅账号」页,使用/费用/日志/渠道健康均按 `subscription_account_id` 维度归因;`scripts/import-subscription-creds.py` 一键导入凭据。
- **架构重构 P0**:http.go 从 2,391 行拆分为 `Orchestrator` + `Forwarder` + `Handler` 矩阵 + `http_raw_helpers` + `http_adaptor`;identity/channel/billing/log 四个 gRPC 客户端接入 sony/gobreaker 熔断 + 4 种降级策略(cache/async/noop/identity)。
- **架构重构 P1**:multi-level cache(L1 内存 + L2 Redis)+ singleflight 防击穿;计费改为异步预扣/批量结算;`SelectChannel` 加权轮询 + 失败率衰减;`logs`/`billing_ledgers`/`billing_reservations` 批量写。
- **架构重构 P2**:`logs` 表按月分区(partition cron 持续维护);统一幂等中间件;relay-gateway 优雅排空(graceful drain);gRPC mTLS 服务间认证;审计日志覆盖 admin 写操作。
- **可观测性补齐**:新增 30+ Prometheus 指标,涵盖 Relay/Selector/Cache/Breaker/Billing/Partition 多维度。
- 新增数据库迁移:`034_create_subscription_accounts.sql`、`035_add_subscription_account_quota_fields.sql`、`036-038_add_subscription_account_id_to_*.sql`、`phase1_indexes.sql`、`phase3_partitioning.sql`。
- 依赖:由 `encoding/json` 切换到 `bytedance/sonic` 提升 apicompat 序列化吞吐。

### Changed
- `internal/relay/server/http.go`:从 2,391 行精简到 1,862 行,只保留路由注册 + 中间件装配。
- 鉴权流程:本地 Auth Cache 命中时不再发起 gRPC;Token 状态变更通过 Redis Pub/Sub 广播失效。
- 计费流程:`ReserveQuota` 改为异步队列提交,失败回退到同步路径(降级策略之一)。
- 渠道选择:同优先级内由纯随机改为加权轮询 + 失败率衰减。
- `scripts/deploy.sh` 部署脚本加固,补齐 migrate 镜像调用与回滚路径。

### Fixed
- 订阅账号健康检查 404(`subscription_account_id` 未透传到 channel-svc gRPC)。
- 编辑订阅账号保存崩溃(`null` → `""` 规范化)。
- 成本归因:cost-analysis 页面之前未按订阅账号分桶,现在全链路带上 `subscription_account_id`。
- gRPC 客户端超时在长上下文场景下误杀,改为可配置 `grpc.dial_timeout` / `call_timeout`。
- relay `Content-Length` 校验对流式响应误判 411,改为仅在非流式路径校验。

### Security
- gosec SAST:本次新增代码 0 issues。
- govulncheck SCA:全代码库 0 vulnerabilities。
- gitleaks 密钥扫描:本次新增代码 0 leaks。
- OAuth 凭据存储:At-Rest 加密 + KMS-style 密钥派生;`scripts/import-subscription-creds.py` 在 stdin 接受凭据而非 argv。

## [0.2.9] - 2026-06-26

### Added
- 新增 Codex Responses WebSocket 协议入站支持：relay-gateway 在 `POST /v1/responses` 上探测 `Upgrade: websocket` 请求，自动切换为 WebSocket 双向转发，可直接作为 Codex CLI 的 WebSocket 后端；非 Upgrade 请求仍走原 HTTP/SSE 路径，向后兼容。
- 客户端 ↔ 上游双向 pump 逐帧镜像转发 Codex 事件，按 turn 解析 usage 并复用现有 reserve / commit / release 计费与 usage 日志链路。
- 新增上游连接池：每渠道空闲连接缓存，经 Ping 健康检查后复用，支持每渠道最大连接数（默认 8）与空闲淘汰（默认 5 分钟）。
- 新增跨进程会话粘滞：`response_id → channel_id` 绑定双写本地热缓存与 Redis，多副本部署下保持多轮会话同一渠道；未配置 Redis 时降级为纯内存。
- 新增多渠道故障转移：上游 dial 失败或首字节前的可重试错误自动按优先级换渠道（默认最多切换 2 次），字节已下发即停止 failover。
- 新增 `openai_ws` 配置块（超时、连接池、failover、sticky、Redis），所有字段可选。
- 依赖新增 `github.com/coder/websocket v1.8.14`。

### Security
- gosec SAST：本次新增代码（`openai_ws_*`）0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks 密钥扫描：本次新增代码 0 leaks（全仓 2 条命中为 README/推广文档中的 `YOUR_TOKEN` 占位符，非真实密钥）。

## [0.2.8] - 2026-06-25

### Added
- 新增 Anthropic Messages API 入站端点 `POST /v1/messages`，使 relay-gateway 能直接对接 Claude Code CLI 及原生 Anthropic SDK 客户端。
- 支持 Anthropic Messages 格式与内部 OpenAI 兼容选路/计费链路的双向转换：string/array content blocks、system prompt、tool_use / tool_result 工具调用。
- 流式响应支持 OpenAI SSE → Anthropic SSE 事件序列转换（message_start / content_block_start / content_block_delta / content_block_stop / message_delta / message_stop）。
- 支持 thinking-mode 模型（DeepSeek-R1、GLM-5.x），将 `reasoning_content` 转换为 Anthropic `thinking` content block。
- 鉴权支持 `x-api-key`（Anthropic 原生）和 `Authorization: Bearer` 两种方式。

### Fixed
- 流式 SSE 中途写入错误不再触发二次 `WriteHeader`。
- `Plan()` 失败返回 Anthropic 错误信封格式，而非 OpenAI 格式。
- 新增 `max_tokens` 上限保护（64000），防止资源耗尽。

### Security
- gosec SAST / govulncheck SCA / gitleaks 密钥扫描全部通过（0 issues / 0 vulns / 0 leaks）。

## [0.2.7] - 2026-06-24

### Added
- 通知记录新增最后一次投递失败原因字段，notify-worker 记录发送错误，管理后台通知面板在失败通知中展示失败原因。

## [0.2.7] - 2026-06-24

### Added
- Prometheus 指标补齐对账任务和渠道健康探测观测：新增对账运行次数/耗时/差异类型计数，以及 monitor-worker 渠道健康 sweep/probe 成功率、耗时和失败原因指标。

## [0.2.6] - 2026-06-20

### Added
- 管理后台新增通知面板，支持查看通知历史、按发送状态筛选、刷新列表和 pending 数量徽标。
- 管理后台新增渠道健康与成本分析页面，补齐健康趋势、成本、收入、毛利等可视化图表组件。

### Fixed
- 管理后台通知接口改为经 `admin-api` 代理到 `notify-worker`，避免前端直接依赖 worker 地址。
- 通知面板兼容 `notify-worker` 直接返回 `{items,total}` 的列表响应格式。
- `admin-api` 补齐 `/admin/channel-health`、`/admin/cost-analysis` 等 SPA 路由，修复刷新或直达页面时的路由回退问题。

## [0.2.5] - 2026-06-19

### Added
- `notify-worker` 新增企业 IM 通知通道：企业微信、钉钉、飞书/Lark 和 Slack。
- 渠道健康告警与对账告警的通知类型支持 `wecom`、`dingtalk`、`feishu`、`slack`。
- 新增 `NOTIFY_WECOM_WEBHOOK_URL`、`NOTIFY_DINGTALK_WEBHOOK_URL`、
  `NOTIFY_FEISHU_WEBHOOK_URL`、`NOTIFY_SLACK_WEBHOOK_URL` 配置项。

### Changed
- 企业微信和钉钉支持配置完整 webhook URL，也支持仅配置 key / access_token 自动拼接。
- 部署文档、README 与示例环境变量补齐企业 IM 告警配置说明。

## [0.2.4] - 2026-06-19

### Added
- 补充渠道健康告警配置文档与 docker-compose 示例环境变量说明。

### Changed
- README 最新发布指针更新到 v0.2.4。

## [0.2.3] - 2026-06-18

### Added
- 渠道健康状态与自动熔断：relay 上游调用会回写成功/失败和响应时间，
  channel-service 连续失败达到阈值后跳过该渠道，冷却期后允许半开恢复；
  管理后台渠道列表展示健康状态、失败次数和熔断冷却时间，并支持手动触发
  `/models` 健康探测。
- monitor-worker 支持定时探测启用渠道的 `/models` 健康状态。
- 新增 `CHANNEL_HEALTH_FAILURE_THRESHOLD`、`CHANNEL_HEALTH_COOLDOWN`、
  `CHANNEL_HEALTH_CHECK_ENABLED`、`CHANNEL_HEALTH_CHECK_INTERVAL`、
  `CHANNEL_HEALTH_CHECK_TIMEOUT` 配置项。

## [0.2.2] - 2026-06-15

### Added
- `notify-worker` 支持 pending 通知实际投递：webhook/event 走 HTTP POST，email 走 SMTP，
  并支持失败重试与最终 failed 状态。
- CI Docker 构建矩阵覆盖全部服务镜像。

### Changed
- 对账告警通知类型可通过 `RECON_ALERT_NOTIFY_TYPE` 配置；
  `RECON_ALERT_RECIPIENTS` 文档改为 webhook URL / email 目标语义。

## [0.2.1] - 2026-06-12

### Added
- **管理后台 Top 用量图表**: `OverviewPage` 增加 Top N 渠道 / 模型用量图表
  (`web/src/pages/admin/OverviewPage.tsx`),导航接入"用量排行"入口
  (`AppNavigation`),并补齐对应单测 (`AppNavigation.test.tsx`)。

### Changed
- 前端构建优化: `web/vite.config.ts` 增加 chunk 拆分策略,
  拆分 vendor / 路由懒加载 / ECharts 等公共依赖,降低首屏包体积。

### Fixed
- 端到端测试与 docker-compose 环境文档对齐: 补齐
  `deployments/docker-compose/.env.example` 的 e2e token / 通知相关变量;
  `test/e2e/main.go` 修正 token 流程以匹配实际 relay 行为;
  同步更新 `.env.example` 与 `docs/deployment.md` / `README.md` 文档。

## [0.2.0] - 2026-06-10

### Added
- **成本与利润分析 (Phase 2)**: `billing_ledgers` 新增 `upstream_cost` 字段(`migrations/029`),
  relay 提交时按渠道侧定价计算上游成本并写入账本;新增收入/成本/毛利相关统计维度。
- **多维 SQL 用量聚合 (Phase 1)**: `billing` 服务新增多维聚合 RPC(按用户/渠道/模型/Token/分组/小时|日),
  取代原先 admin 的 1000 条内存抽样统计;`migrations/028` 为 `billing_ledgers` 补充 `(created_at)`、
  `(channel_id, created_at)`、`(model_name, created_at)` 索引。
- **渠道对账 (Phase 3 起步)**: `RunReconciliation` 增加渠道维度校验
  (本地累计渠道用量/成本 vs 渠道 `used_quota`),以及 ledger/log 双写一致性比对。
- **对账差异告警**: `billing-service` 检测到对账差异时通过 gRPC 投递到 `notify-worker`,
  按 `RECON_ALERT_RECIPIENTS` 创建通知;出错仅记日志,不阻塞对账任务。
- **成本健康 dashboard**: 管理后台新增成本/毛利/渠道余额健康面板
  (基于 `reconciliation_runs` + 账本聚合)。
- **缓存 token 用量展示**: 用量统计与账本写入支持 `cache_read_tokens` 字段
  (`migrations/031`),后台与 `/v1/usage` 路径可见缓存命中率相关指标。
- **管理后台 i18n(zh-CN)**: 关键文案本地化。

### Changed
- 管理员日志/用量统计从内存抽样改为调用 billing 真实聚合,数字可信。
- 对账任务支持通过 `WithNotifier` / `WithRecipients` 选项装配通知;
  不配置通知端点时退化为仅日志模式(向后兼容)。

### Fixed
- 修复 relay 流式响应 logger 偶发空指针 panic。
- dashboard token 趋势 Y 轴在数值跨度大时改为紧凑单位显示。

## [0.1.1] - 2026-05-09

### Added
- 渠道余额刷新适配 OpenAI/DeepSeek/OpenRouter/SiliconFlow 等 provider。
- Docker builder stage 增加 `--platform=$BUILDPLATFORM` 多架构支持。
- `admin-api` 支持外部管理前端构建产物托管。

## [0.1.0] - 2026-05-06

首个公开版本,核心微服务边界确立:
- `relay-gateway`、`admin-api`、`identity-service`、`channel-service`、
  `billing-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`。
- OpenAI 兼容 API 网关、用户/Token/额度/账务基本链路、Docker Compose 部署。
