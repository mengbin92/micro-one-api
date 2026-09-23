# 订阅 Redis 多副本部署 Runbook

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 适用版本：v0.5.0+（Redis-backed 订阅账号并发 limiter、runtime blocker、session sticky、session window）。
> **D1 更新（2026-09-23）**：OAuth 多 Relay 协调已完成本地/隔离验收并上线，实现及证据随本次 D1 提交归档，基线 `feb9918c`。生产已应用迁移 109、更新 channel/Relay/identity 并启用 `redis`，保持单 Relay/单 channel；真实 OAuth 轮换和多副本故障切换待验收。该能力要求所有 channel/relay 使用本轮代码，不能仅凭旧版本已有 Redis limiter 扩 OAuth 副本。
> 相关文档：[Relay 稳定性压测与 Runbook](./relay-stress-runbook.md)、[订阅账号额度治理 Runbook](./subscription-account-quota-governance-runbook.md)、[生产发布 Runbook](./subscription-production-runbook.md)。

本 runbook 覆盖多 Relay 的凭证刷新、Redis 共享限流、故障退化、sticky 和 failover。**channel-service 仍为一个运行实例**：普通渠道/账号 selector 的健康窗口、熔断和半开名额，以及 OAuth 绑定会话，尚未跨 channel 副本协调。Relay 扩容不等于 channel 可扩容。

## 一、前置条件

1. **多副本 relay-gateway**：≥2 个实例，共享同一 Redis 与 channel 持久层；先完成下述 OAuth 升级顺序。持久层支持 MySQL/PostgreSQL/SQLite，SQLite 仍须由同一个 channel owner 访问。
2. **Redis 可达**：所有 relay 副本 `REDIS_ADDR` / `REDIS_PASSWORD` 指向同一 Redis keyspace。当前客户端是单端点 `redis.Client`，本轮不新增 Sentinel/Cluster 客户端。
3. **hybrid_adaptor 已开启**：`configs/config.yaml` 的 `hybrid_adaptor.enabled: true`，后台凭证 sweep 保持启用，默认 `refresh_interval: 10m`。
4. **session_sticky 已开启**（验证 sticky 时）：`session_sticky.enabled: true`。
5. **至少 2 个 enabled 订阅账号**服务同一 `model + group`（验证 failover）。
6. **压测前置**：CI smoke（miniredis）随时可跑；预发全量 k6 需真实多副本。
7. **channel 单 owner**：Kubernetes 示例 Deployment/HPA 固定为 1、更新策略为 Recreate，更新时有短暂不可用；不得通过 HPA 自动扩 channel 来承接 Relay 扩容。

## 二、必填配置

| 配置 / 环境变量 | 位置 | 必填 | 说明 |
| --- | --- | --- | --- |
| `REDIS_ADDR` | relay-gateway env | ✅（多副本） | 共享 Redis 地址 |
| `REDIS_PASSWORD` | relay-gateway env | ✅ | Redis 密码 |
| `RELAY_CREDENTIAL_COORDINATION` | relay-gateway env | ✅（OAuth 多副本） | `redis`；默认 `single` 只允许一个 OAuth relay，启动会提示。未知值、协调模式无 Redis/后台 sweep 均拒绝启动 |
| `hybrid_adaptor.enabled` | `configs/config.yaml` | ✅ | 总开关 |
| `hybrid_adaptor.runtime_block.*` | 同上 | ⬜ | 冷却时长，默认 429=5s/401=2m/5xx=2m/529=30s |
| `hybrid_adaptor.runtime_block.active_gauge_interval` | 同上 | ⬜ | active block gauge 上报周期，默认 30s |
| `session_sticky.enabled` | 同上 | ⬜（验证 sticky 时 ✅） | session→account sticky 开关 |
| `openai_ws.sticky_ttl` | 同上 | ⬜ | sticky 绑定 TTL，默认 1h。session sticky 复用此 TTL |

wire 层（`cmd/relay-gateway/wire_gen.go`）在 `redisClient != nil` 时自动接入：
- `NewRedisRuntimeBlocker(redisClient)` → `SetRuntimeBlocker`。
- `NewRedisAccountConcurrencyLimiter(redisClient)` → `SetAccountConcurrencyLimiter`。
- `NewRedisAccountRPMLimiter(redisClient)` → `SetAccountRPMLimiter`。
- `NewRedisUserRPMLimiter(redisClient)` → `SetUserRPMLimiter`。
- `SetOpenAIWSStickyStore(redisClient)` → 同时初始化 response sticky、session sticky、session window store。

限流、sticky 等沿用既有内存降级。**OAuth 刷新单独 fail-close**：协调模式下，Redis 不可用时不发送新的 OAuth 刷新请求；数据库内已持久化且仍有效的 access token 可继续读取使用。数据库不可读时也不使用未经版本核对的本地凭证。

### 2.1 OAuth 协调与升级顺序

1. 先保持一个 OAuth Relay，确认各实例待补写数量为 0；有 pending 时保留其 owner 进程，先恢复持久层。
2. 应用当前数据库方言的 `109_add_credential_refresh_pending.sql`，更新 channel-service，然后更新单个 Relay。该迁移为新增布尔列，旧行默认 false，不写凭证明文。
3. 在所有将参与扩容的 Relay 上设置 `RELAY_CREDENTIAL_COORDINATION=redis`，保持后台 sweep；Compose 已透传开关，Kubernetes 多副本示例默认使用 `redis`。先替换示例镜像为包含本轮代码的镜像。禁止旧版/`single` 与协调模式同时刷新同一账号。
4. 验证刷新成功后 revision 增长、pending 清除，再扩 Relay；channel-service 保持单实例。生产构建仍按仓库规范在本地交叉构建，不在服务器构建。
5. 回退时先停止新增 OAuth 流量并缩回一个 Relay，完成补写或新授权，确认无 unresolved claim 后再切 `single`。保留迁移列；旧二进制不理解 pending，不得带着未解决占用直接回退。

Redis 的 `credential:refresh:<account_id>` 只保存随机 owner token，TTL 60 秒，不存 access/refresh token。数据库在 OAuth 调用**之前**原子推进 revision 并写入 pending；锁失效不会解除该占用。持有新凭证的 owner 可在原 revision 上补写，写成功后清 pending；人工提供新 refresh token 会推进版本并拒绝旧 owner 的迟到写回。请求等待 peer 刷新最多 35 秒，并受调用方 deadline/取消约束；后台竞争者跳过，不改写账号健康状态。

由于无法与外部 OAuth 原子提交，claim 回包丢失、OAuth 失败/超时、进程在刷新期间死亡，都会保留不确定状态，可能需要新授权。不能靠删除 Redis 锁或手工清 pending 重试旧 refresh token。账号名称等普通资料编辑在 unresolved claim 期间返回 revision conflict；重新授权须携带新 refresh token。

## 三、共享状态清单

| 状态 | Redis key 前缀 | 作用 | Redis 不可用时 |
| --- | --- | --- | --- |
| OAuth 刷新准入 | `credential:refresh:*` | 一次刷新 owner；数据库 revision/pending 做持久化保护 | 不刷新；仍有效的持久层 access token 可使用 |
| 账号并发槽位 | `subscription_account:concurrency:{account_id}` | ZSET 共享 in-flight 租约，TTL 回收 | 降级内存 limiter，单副本内仍 cap |
| runtime block | （Redis blocker 内部 key） | 429/5xx/529 冷却跨副本可见 | 降级内存 blocker，仅本副本可见 |
| 账号 RPM 窗口 | `subscription_account:rpm:*`（近似） | 60s 滚动 RPM 跨副本共享 | 降级内存 RPM limiter |
| 用户 RPM 窗口 | `user:rpm:*` | 用户级 RPM 跨副本共享 | 降级内存用户 RPM |
| 登录失败计数 | `identity:login-fail:*` | IP/用户名哈希键的共享失败次数 | 本地计数，新增退化告警；恢复后共享状态重新成为权威 |
| response→channel sticky | （ws sticky store） | `previous_response_id` 路由复用 | 降级内存 sticky，仅本副本 |
| session→account sticky | （session sticky store） | `session_hash` 复用同账号 | 降级内存 sticky |
| session 成本窗口 | （session window store） | `session_hash+account_id` 成本窗口 | 降级内存窗口 |

## 四、验证

### 4.1 并发 cap 跨副本生效

设某账号 `concurrency=1`，两个 relay 副本同时请求该账号同一 model：

```bash
# 副本 A
curl http://replica-a:8080/v1/chat/completions -d '{"model":"claude-sonnet-4",...}' &
# 副本 B（同时）
curl http://replica-b:8080/v1/chat/completions -d '{"model":"claude-sonnet-4",...}' &
```

期望：只有一个请求命中该账号，另一个 failover 到 sibling 或排队。CI smoke 对应 `TestStress_AccountConcurrency_MultiReplicaWithinCap`（miniredis 模拟多副本）。

核对 Prometheus：`micro_one_api_relay_account_concurrency_fallback_total` 正常时 = 0。

### 4.2 runtime block 跨副本可见

副本 A 触发某账号上游 429 → 该账号被 block 5s。副本 B 在 5s 内请求同账号应直接跳过（不发起上游请求）。5s 后两副本都恢复。CI smoke 对应 `TestStress_RuntimeBlocker_CrossReplicaAndExpiry`。

核对：`micro_one_api_relay_runtime_block_active` 在 block 期间 > 0，过期后归 0。

### 4.3 Redis 故障 fail-open

只在隔离环境临时停 Redis（或挡连接），不需刷新 OAuth 的请求可降级使用内存 limiter，`micro_one_api_relay_account_concurrency_fallback_total{reason="acquire_error"}` 递增。OAuth 需要刷新时必须拒绝刷新，不能 fail-open。恢复 Redis 后 fallback 指标停止递增。既有限流 smoke 对应 `TestStress_AccountConcurrency_RedisOutageFailsOpenWithFallbackMetric`。

本地新准入预算可达到 N × limit，并且故障前已在 Redis 获准的请求仍可能在途；这不是全局上限承诺。恢复后的新 Redis 准入重新受共享 cap 约束，但降级期在途请求/本地 RPM 窗口没有自动合并，应等待排空后再验收全局峰值。不得按未知 N 使用 `limit/N` 声称严格全局保护。

### 4.4 session sticky 跨副本

副本 A 用 `session_hash=abc` 请求，绑定到账号 1。副本 B 用同 `session_hash=abc` 请求，应命中账号 1（prompt-cache 复用）。CI smoke 对应 `TestStress_SessionHashSticky_CrossReplicaBindAndReuse`。

核对：`micro_one_api_relay_subscription_sticky_total{result="hit"}` 主导。

### 4.5 failover

某账号上游 429 → failover 到 sibling → 客户端收 200。失败账号被冷却。`micro_one_api_relay_subscription_failover_total{reason="429"}` 递增。CI smoke 对应 `TestStress_Failover_429_UnderLoad`。

### 4.6 CI smoke（无需真实多副本）

```bash
go test ./internal/relay/... -run TestStress -v
```

用 miniredis（进程内 Redis double）模拟多副本共享 Redis 的 EVAL/TTL 语义，任何 CI runner 可跑。完整场景与验收映射见 [Relay 压测 Runbook](./relay-stress-runbook.md)。

### 4.7 预发全量（真实多副本）

```bash
k6 run -e BASE_URL=https://relay-gateway.preprod.internal \
  -e API_KEY=sk-... -e SCENARIO=session_sticky \
  scripts/benchmark/k6-relay-subscription-stress.js
# 依次跑 response_sticky / failover_429 / mixed_failover / concurrency_rpm
```

预发门槛：成功率 > 0.99，p95 < 2000ms，p99 < 5000ms，多副本并发峰值 ≤ 配置 cap，Redis fallback 正常时 = 0。

## 五、常见故障与恢复

### 5.1 并发峰值超过配置 cap

**原因**：Redis 未配置 / limiter 未生效，降级单进程内存 limiter。

**排查**：
1. 确认 `REDIS_ADDR` 已设。
2. 确认 `xdb.NewRedisClient` 返回非 nil（`redisClient != nil` 才接入 Redis limiter）。
3. 看 `micro_one_api_relay_account_concurrency_fallback_total` 是否递增（Redis acquire_error）。
4. 若递增，查 Redis 可达性 / 密码 / 连接池。

### 5.2 sticky 命中率为 0

**原因**：`session_sticky.enabled=false`，或 Redis sticky store 未配置，或请求没带 `session_hash`。

**排查**：
1. 确认 `session_sticky.enabled: true`。
2. 确认 `SetOpenAIWSStickyStore(redisClient)` 被调（需 Redis）。
3. 请求带 `session_hash`（body 或 `X-Session-Hash` header）。
4. 看 `micro_one_api_relay_subscription_sticky_total{result="miss"}` 是否主导。

### 5.3 failover 全部 exhausted

**原因**：账号池太小，或所有 sibling 都被 runtime block / concurrency-full。

**排查**：
1. 确认 ≥2 个 enabled 账号服务同 model+group。
2. 看 `micro_one_api_relay_runtime_block_active` 是否持续高位。
3. 看 runtime block duration 是否过长。
4. failover reason 若是 `concurrency`，sibling 的 `Concurrency` 配置是否过小。

### 5.4 Redis fallback 持续递增（Redis 正常时）

**原因**：Redis 连接超时 / 命令失败 / 连接池不足。

**排查**：
1. Redis `ReadTimeout` / `WriteTimeout` 是否过短。
2. `PoolSize` 是否足够（默认 100）。
3. Redis 服务器负载 / 内存。
4. 看 fallback label：`acquire_error` vs `release_error` vs `refresh_error`。

### 5.5 多副本 OAuth 绑定 session 丢失

**原因**：OAuth session 存在 channel-service 进程内（非 Redis），`exchange` 必须回到生成 `auth-url` 的同一副本。

**恢复**：见 [OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md) §六。绑定完成后 `subscription_accounts` 行在 DB 共享，不受此限制。

### 5.6 OAuth 刷新占用长期未解决

先查不含凭证的状态：

```sql
SELECT id, platform, credential_revision, credential_refresh_pending
FROM subscription_accounts
WHERE credential_refresh_pending = TRUE;
```

结合每个 Relay 的 `micro_one_api_credential_pending`、`micro_one_api_credential_pending_oldest_age_seconds` 和含 account_id 的脱敏日志查找持有待补写凭证的 owner。owner 存活时恢复数据库，等待下一轮 `PersistPending`；不要先重启 owner。owner 已丢失且没有可确认的新凭证时，通过现有 OAuth 绑定/账号更新流程提供新 refresh token。更新后确认 pending=false，新的请求回读成功，旧 owner 的迟到 CAS 被拒绝。

`CredentialRefreshUncertain` 使用最近 30 分钟不确定结果触发 critical；pending 账号即使远未过期也会进入 sweep。默认 10 分钟扫描可持续观察冷账号，修改扫描间隔时应同步调整告警窗口。`CredentialCoordinationUnavailable` 与 `LoginLimiterRedisDegraded` 覆盖协调存储失败和登录退化，原 `AccountLimiterRedisDegraded` 覆盖并发/RPM。告警窗口老化不单独证明恢复，须同时确认 SQL 状态和成功请求；外部通知送达仍沿用 O2 的独立验收。

## 六、D1 本地实施验收（2026-09-23）

基线 `develop@feb9918c`，本节记录提交前的本地验收；实现及证据随本次 D1 提交归档。已实际执行：

```bash
go test ./internal/... ./app/channel/... ./app/identity/... ./domain/upstream/credential ./platform/metrics -count=1
go test -race ./domain/upstream/credential ./internal/data ./internal/adaptor ./app/channel/internal/biz ./app/channel/internal/data ./app/channel/internal/service ./app/identity/internal/biz ./app/identity/internal/data ./cmd/relay-gateway
# 两个 TEST_CREDENTIAL_*_DSN 只能指向隔离库，测试拥有并删除其 subscription_accounts 表。
go test ./app/channel/internal/data -run 'TestCredentialRevisionMigrationDialects|TestPendingCredentialClaimRequiresNewAuthorization' -count=1 -v
docker run --rm --entrypoint /bin/promtool -v "$PWD/deploy/prometheus/alerts:/rules:ro" -w /rules prom/prometheus:v3.6.0 test rules credential.test.yml operations.test.yml
make api
make wire
make migration-check
./scripts/check-architecture.sh
make format-check
./scripts/check-deployment-docs.sh
```

前三方言实际为独立 Docker MySQL 8、PostgreSQL 16 和临时 SQLite 文件；没有使用生产 DSN。Wire 的已有安装使用旧 Go 构建，首次生成失败，随后在临时目录使用 Go 1.27.1、Wire 0.7.0 与 x/tools 0.49.0 重新构建工具并经 `make wire` 生成，仓库依赖未改动。

部署文档检查通过：四组 Compose 配置、41 个 Kubernetes 资源、83 个配置引用和 197 份 Markdown 本地链接。本机缺失的 Kustomize 5.8.1 / kubeconform 0.7.0 安装在临时目录；该检查只渲染和校验，没有应用资源。

| 场景 | 可复现入口与结果 |
| --- | --- |
| 真实 adaptor 跳过 provider 的旧入口 | `TestOAuthAdaptorsHonorAuthoritativeCredentials`，Claude/Codex 正常及拒绝两种路径共 4 个场景先失败后通过；`TestCoordinatedCredentialsPreserveStaticCredentials` 覆盖 setup token 与 Kimi static_key，后者部署前先复现失败后修复 |
| 双副本竞争、等待者取消 | `TestCredentialConcurrentReadersShareRotationAndCancelWait`，只调用一次 OAuth，peer 读取相同结果，本地/跨副本等待均遵守 deadline |
| 过期租约及人工重授权 | `TestCredentialLeaseExpiryCannotDeleteSuccessor`、`TestCredentialReplicasFenceExpiredOwnerAndReauthorization`，旧 owner 不删除新锁，不重复刷新，不覆盖新授权 |
| 进程状态丢失与不明结果 | `TestCredentialLostClaimAndOAuthFailureAreNotReplayed`，新 provider 仍受数据库 claim 约束，claim 回包丢失零 OAuth、OAuth 失败最多一次 |
| 数据库失败、恢复、回包丢失 | `TestCredentialPendingRotationRecoveredWithoutAnotherOAuthCall`、`TestCredentialStoreResponseLossDoesNotReplayOAuth`、`TestCredentialRecoveredStaleRotationRefreshesOnlyAfterPersistence` |
| 三方言迁移/CAS | `TestCredentialRevisionMigrationDialects`，历史行默认值、并发仅一个 claim、重复 Store、旧写者拒绝均通过 |
| 冷账号与状态副作用 | `TestPendingCredentialClaimRemainsInColdAccountSweep`、`TestRefreshTaskCoordinationDoesNotRewriteAccountStatus` |
| 登录与负载退化 | 既有账号 concurrency/RPM 多副本失败/恢复用例、`TestLoginLimiterDegradationIsObservableAcrossReplicas`，有限标签告警触发和恢复规则通过 |

受控 OAuth HTTP 和 miniredis 证明协调协议及故障分支，不代表真实供应商轮换、生产跨进程部署或容量验收。每次协调模式执行新增凭证回源 RPC，生产成本仍应按 O5 测量。生产部署记录如下。

## 七、D1 生产更新（2026-09-23）

用户确认更新线上后，本地交叉构建 linux/amd64 镜像，未在生产主机构建。部署前 pending 指标均为 0；只读盘点发现有效 Kimi 账号使用 `static_key`，补上其不进入 OAuth 刷新的兼容分支，并通过适配器/HTTP 包 `-race` 后再构建 Relay 镜像。

| 服务 | 更新完成（UTC） | 运行镜像 |
| --- | --- | --- |
| channel-service | 12:25:48 | `sha256:6c3d047c11273e0121adfa6cfc05290d2ddd129f9a3fa72de219428487d17d04` |
| relay-gateway | 12:26:55 | `sha256:ca65253266e94accb31d39cb234bb0523405c2af9cf63adb0e311cd329975f95` |
| identity-service | 12:28:06 | `sha256:1f6d95354be65b25230a2925c230f82b45f0e138c500de1b58db053f331fc8b4` |

实际迁移库为 `oneapi_channel`，不是保留旧表的 `oneapi`。使用既有 migrate runner 挂载单独的迁移 109 执行，12:24:03 UTC 登记成功，再次执行返回 `nothing to apply`。迁移源文件和 ownership manifest 同步至服务器；三个静态账号的 revision/pending 保持 0。

生产 Compose 增加 `RELAY_CREDENTIAL_COORDINATION=${RELAY_CREDENTIAL_COORDINATION:-redis}`，核验容器实际值为 `redis`。仅更新以上三个服务，未扩容、未更新前端。Prometheus `promtool check rules` 确认 46 条规则，通过 HUP 重载，三条新增规则在 API 回读中均为 health=ok/inactive。

12:29 UTC 验证三个服务 `/healthz` 均为 200、restart_count=0；公网 `https://api.mengbin.top/healthz` 返回 `{"status":"ok"}`。Relay 三平台 sweep=600 秒，pending=0；identity 的 read/write/clear 退化计数均为 0。内部 claim RPC 对无效账号 id=0 返回 Aborted/credential revision conflict，确认新方法接入且没有写入真实账号；管理读取确认账号 5 仍为 Kimi/static_key。完整脱敏样本见[部署证据](./evidence/d1-deploy-2026-09-23.json)。

三个旧运行镜像统一保留为 `rollback-d1-20260923T122000Z`。原 Compose、告警、ownership manifest 和 channel 凭证/迁移表备份位于服务器 `/opt/micro-one-api/backups/d1-20260923T122000Z`，目录权限 0700；数据库备份未下载、未进入 Git。回退必须先确认 pending 已补写或完成新授权，保留新增列，不手工清 pending。

生产当前三个账号均为 `static_key`，OAuth 账号数为 0。本轮没有真实供应商轮换、多副本故障注入、模型扣费请求或外部测试通知；对应验收及 O2 外部送达/OTel、O5 成本测量继续保留。部署证据与代码一同提交，未创建发布 tag。
