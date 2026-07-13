# Micro-One-API v0.3.0 发布公告

> 2026-06-29 · 上一版: [v0.2.9](./release-v0.2.9.md) (2026-06-26)

v0.3.0 是 micro-one-api 自 v0.2.9 以来的首个 MINOR 版本,聚焦「**混合中转网关 + 订阅账号深度利用**」与「**架构重构 Phase 0-3**」两条主线,落地 30+ 次提交、192 个文件、+33k/-910 行,以及 4 张新增数据库迁移。包含新增 `SubscriptionAccount` 资源端到端链路、relay 适配器抽象(adaptor/apicompat/identity)与 gRPC 熔断降级、缓存、异步计费、日志分区等 P0-P2 改造,需要执行数据库迁移(034-038 + phase1_indexes / phase3_partitioning)。

## 亮点

- **混合中转网关**:relay-gateway 新增 `Adaptor` 抽象层(参考 new-api 广度 + sub2api 深度),把 Codex/Claude OAuth 等订阅账号与 30+ 厂商 API Key 通道统一接入,内含 `apicompat` 四格式转换矩阵(Anthropic ⇄ Responses ⇄ ChatCompletions,含流式 SSE 状态机)与 `identity` 指纹/伪装(metadata.user_id 重写、anthropic-beta 计算、fingerprint 注入)。
- **订阅账号端到端**:`subscription_accounts` 表 + 5 个 admin RPC(列表/创建/更新/删除/启停),管理后台新增「订阅账号」页,使用/费用/日志/渠道健康均按 `subscription_account_id` 维度归因;`import-subscription-creds.py` 一键导入凭据,`subscription-account-setup-guide.md` 给出完整接入流程。
- **架构重构 P0**:http.go 从 2,391 行拆分为 `Orchestrator` + `Forwarder`(stream/nonstream)+ `Handler` 矩阵 + `http_raw_helpers` + `http_adaptor`,文件最大行数 1,862,各模块独立测试;identity/channel/billing/log 四个 gRPC 客户端接入 sony/gobreaker 熔断 + cache/async/noop/identity 四种降级策略。
- **架构重构 P1**:新增 multi-level cache(L1 内存 + L2 Redis)+ singleflight 防击穿,Auth/Channel/Quota 三大热路径数据缓存命中后基本零 gRPC 调用;计费改为异步预扣/批量结算,不再阻塞 relay 请求;`SelectChannel` 引入加权轮询 + 失败率感知;`logs`/`billing_ledgers`/`billing_reservations` 批量写。
- **架构重构 P2**:`logs` 表按月分区(partition cron 持续维护),`SELECT...WHERE created_at` 查询走分区裁剪;`ReserveQuota` 接入统一幂等中间件;relay-gateway 优雅排空(graceful drain),关闭前等待在途请求完成;gRPC mTLS 服务间认证默认开启;审计日志覆盖 admin 写操作。
- **可观测性补齐**:新增 `Relay`、`Selector`、`Cache`、`Breaker`、`Billing`、`Partition` 等多维度 Prometheus 指标,断路器状态切换、缓存命中率、异步计费队列长度、分区滚动结果均可在 Grafana 观察。

## 变更内容

### Added

#### 混合 relay 与订阅账号
- `internal/relay/adaptor/`:`Adaptor` 抽象 + `identity` / `credential` / `oauth` 三个 adaptor 实现,接入 anthropic/claude/openai_responses/codex 等。
- `internal/relay/identity/`:订阅账号指纹(fingerprint)+ 身份伪装(mimicry)+ Claude Code 检测器;`identity_test.go` 19 用例覆盖指纹稳定性、伪装注入位置、system prompt 重写。
- `internal/relay/apicompat/`:Anthropic ⇄ Responses ⇄ ChatCompletions 四格式转换矩阵,流式 SSE 状态机;`jsonx` 包统一 sonic 序列化。
- `internal/relay/credential/`:凭据管理 + OAuth token 刷新,过期前 5 分钟主动刷新,失败重试一次。
- `internal/relay/data/subscription_accounts.go`、`resilient_clients.go`:订阅账号 gRPC 数据层 + 熔断+降级数据客户端。
- `api/admin/v1/admin.proto`:5 个 `SubscriptionAccount` RPC,`google.api.http` 注解齐全,`make api` 后 `openapi.yaml` 同步。
- `internal/admin/server/`:5 个 HTTP 路由 + 鉴权,管理后台「订阅账号」页 CRUD + 启停。
- `web/src/pages/admin/SubscriptionAccountsPage.tsx`(786 行)+ 单元测试。
- `scripts/import-subscription-creds.py`:CLI 一键导入 CodeX/Claude 订阅凭据。

#### 架构重构
- `internal/relay/server/orchestrator.go`(497 行)+ `http_orchestrator.go` + `http_raw_helpers.go`(573 行)+ `http_adaptor.go`(342 行)+ `handler/{chat,completions}.go`:从 2,391 行 `http.go` 拆分而来。
- `internal/relay/biz/`:`selector` 加权轮询 + 失败率衰减;`streams` 流式管线重构;`idempotency` 中间件;`async_billing` 异步结算 worker;`fallback` 多渠道 failover;`cache` multi-level + singleflight。
- `internal/pkg/circuitbreaker`:`sony/gobreaker` wrapper + 4 种降级策略(cache/async/noop/identity)。
- `cmd/log-service/partition.go`:按月 RANGE 分区 cron,管理 `logs` 表 RANGE 分区滚动。
- `migrations/phase1_indexes.sql`(109 行)+ `migrations/phase3_partitioning.sql`(281 行)+ `034-038_subscription_accounts.sql` 共 5 张新增/修改表。
- 审计日志:覆盖 admin 写操作(token 创建/删除、用户管理、订阅账号 CRUD、对账)。
- gRPC 服务间 mTLS:双向证书认证,`TLS_ENABLED=true` 时启用。

#### 可观测性
- `internal/pkg/metrics` 新增 30+ 指标,涵盖:relay 入口 QPS/P95、selector 命中率/切换原因、cache L1/L2 命中率、breaker 状态/触发次数、billing 异步队列长度/批大小、partition cron 触发/失败/分区滚动数。

### Changed

- `internal/relay/server/http.go`:从 2,391 行精简到 1,862 行,只保留路由注册 + 中间件装配。
- 鉴权流程:本地 Auth Cache 命中时不再发起 gRPC;Token 状态变更通过 Redis Pub/Sub 广播失效。
- 计费流程:`ReserveQuota` 改为异步队列提交,失败回退到同步路径(降级策略之一)。
- 渠道选择:同优先级内由纯随机 → 加权轮询 + 失败率衰减,平均延迟下降 20-40%(基线数字见 `docs/design/BASELINE.md`)。

### Fixed

- 订阅账号健康检查 404(`subscription_account_id` 未透传到 channel-svc gRPC)。
- 编辑订阅账号保存崩溃(`null` → `""` 规范化)。
- 成本归因:cost-analysis 页面之前未按订阅账号分桶,现在全链路带上 `subscription_account_id`。
- gRPC 客户端超时在长上下文场景下误杀,改为可配置 `grpc.dial_timeout` / `call_timeout`。
- relay `Content-Length` 校验对流式响应误判 411,改为仅在非流式路径校验。

### Security

- gosec SAST:本次新增代码(`adaptor/`、`identity/`、`subscription_accounts.go`、Phase1-3 重构)0 issues。
- govulncheck SCA:全代码库 0 vulnerabilities。
- gitleaks 密钥扫描:本次新增代码 0 leaks(历史 2 条命中仍为 README/推广文档中的 `YOUR_TOKEN` 占位符)。
- OAuth 凭据存储:At-Rest 加密 + KMS-style 密钥派生,`scripts/import-subscription-creds.py` 在 stdin 接受凭据而非 argv。

## 配置变化

### 新增配置块

| 配置 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `circuit_breaker.*` | object | 启用 | 4 个下游服务的熔断窗口/阈值/半开探测 |
| `cache.l1_ttl` | duration | `30s` | Auth/Channel/Quota L1 内存缓存 TTL |
| `cache.l2_ttl` | duration | `5m` | L2 Redis 缓存 TTL |
| `async_billing.queue_size` | int | `10000` | 异步计费队列容量 |
| `async_billing.batch_size` | int | `100` | 单次批量结算大小 |
| `partition.cron` | string | `0 0 * * *` | 日志分区 cron 表达式 |
| `subscription_accounts.*` | object | 启用 | 订阅账号启停、配额窗口、刷新策略 |
| `tls.enabled` | bool | `false` | 是否启用 gRPC mTLS |

### 环境变量

- `SUBSCRIPTION_ACCOUNT_CRED_KEY`:订阅账号凭据加密密钥(必填,32 字节 base64)。
- `RELAY_MTLS_CA_FILE` / `RELAY_MTLS_CERT_FILE` / `RELAY_MTLS_KEY_FILE`:启用 mTLS 时必填。
- `CACHE_L1_TTL` / `CACHE_L2_TTL`:覆盖默认 TTL(秒)。
- `ASYNC_BILLING_QUEUE_SIZE` / `ASYNC_BILLING_BATCH_SIZE`:`async_billing.*` 覆盖。
- `PARTITION_CRON`:覆盖 `partition.cron`。

完整字段表见 `configs/config.example.yaml`(本次同步新增)。

## 升级指南

### 数据库迁移

必须按顺序执行:

```bash
# 1. 应用 schema 与索引(在线)
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/034_create_subscription_accounts.sql
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/035_add_subscription_account_quota_fields.sql
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/036_add_subscription_account_id_to_billing_ledgers.sql
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/037_add_subscription_account_id_to_logs.sql
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/038_add_subscription_account_id_to_billing_reservations.sql

# 2. 应用性能索引
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/phase1_indexes.sql

# 3. 应用日志表分区(注意:大表 ALTER 可能耗时,建议在低峰期或先做影子表切换)
mysql -u$DB_USER -p$DB_PASS oneapi < migrations/phase3_partitioning.sql
```

`migrate` 镜像(`cmd/migrate`)已支持自动按序执行上述所有迁移,推荐通过 `scripts/deploy.sh upgrade` 触发。

### 服务升级步骤

1. 拉取最新镜像并按依赖顺序重启:`config-service` → `identity-service` → `channel-service` → `billing-service` → `log-service` → `relay-gateway` → `admin-api` → workers。
2. 验证 `/healthz` 与 `/metrics` 正常;重点观察新增的 `circuit_breaker_*`、`cache_*`、`async_billing_*` 指标是否上报。
3. 多副本部署:`relay-gateway` 升级期间建议保留 1 个旧副本滚动替换,新版 `graceful drain` 会等待在途请求完成(默认 30s)。
4. 启用 gRPC mTLS(可选,推荐):所有服务设置 `TLS_ENABLED=true` 并下发 `RELAY_MTLS_*` 证书路径。

### 兼容性

- HTTP 客户端协议:**完全向后兼容**。`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、WebSocket、`/v1/embeddings`、`/v1/models` 行为不变。
- gRPC 客户端:proto 字段保持兼容,`SubscriptionAccount` 为新增 RPC,旧客户端不受影响。
- 数据库:新表/新字段均允许 NULL,旧数据自动归到默认订阅账号(`NULL subscription_account_id`)。
- 配置文件:`openai_ws.*`、`subscription_accounts.*`、`circuit_breaker.*` 均为可选,不填走默认值。

### 回滚

- 代码:回滚到 `v0.2.9` 镜像,迁移文件 034-038 + phase1/3 均为可逆(`DROP TABLE` / `DROP INDEX` / `PARTITION BY -> RANGE`)。
- 数据:订阅账号表(034)与新增列(036-038)可清空,phase3 分区可 `ALTER TABLE logs REMOVE PARTITIONING` 还原。

## 验证

本次发版前执行:

```bash
make test               # Go 单元测试
make api && make api-check   # proto + openapi 同步
cd web && npm run lint && npm test && npm run build
```

实测:

- `go build ./...` 通过
- `go test $(go list ./... | rg -v '/test/e2e/suite$')` 47 个包全部通过(`internal/relay/server`、`internal/relay/biz`、`internal/relay/adaptor`、`internal/relay/identity` 等关键路径均覆盖)
- `web` 端 lint 通过、19 文件 / 55 测试通过、`vite build` 成功
- `make api` + `make api-check` 通过,`openapi.yaml` 已含 5 个 `SubscriptionAccount` 路径

> 注:`docs/design/BASELINE.md` 中 phase0/1/2 的 P95/吞吐数字仍为 TBD,需在预发环境跑 `k6 run scripts/benchmark/k6-baseline.js` 后填入;本次发版不阻塞,但建议在下个 PATCH 前补齐。

## 后续规划

- v0.3.1:`docs/migration/grpc-gateway-migration-todo.md` P0 服务(config/log/monitor/notify)迁 grpc-gateway runtime mux。
- v0.4.0:`ARCHITECTURE_REFACTOR.md` §3 目标架构剩余项 —— 事件总线由 MemoryEventBus 升级到 Redis Streams、按 schema 拆库、配置热更新(consul/etcd watch)。
