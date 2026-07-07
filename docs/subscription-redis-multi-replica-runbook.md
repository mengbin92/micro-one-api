# 订阅 Redis 多副本部署 Runbook

> 对应 `docs/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 适用版本：v0.5.0+（Redis-backed 订阅账号并发 limiter、runtime blocker、session sticky、session window）。
> 相关文档：[Relay 稳定性压测与 Runbook](./relay-stress-runbook.md)、[订阅账号额度治理 Runbook](./subscription-account-quota-governance-runbook.md)、[生产发布 Runbook](./subscription-production-runbook.md)。

本 runbook 让新部署人员只按本文档即可在多副本（≥2 relay-gateway）部署下验证 Redis 共享状态生效、Redis 故障时 fail-open、sticky 跨副本可见与 failover。

## 一、前置条件

1. **多副本 relay-gateway**：≥2 个实例，共享同一 Redis 与同一 MySQL。
2. **Redis 可达**：所有 relay 副本 `REDIS_ADDR` / `REDIS_PASSWORD` 指向同一 Redis（生产建议 Redis Sentinel / Cluster）。
3. **hybrid_adaptor 已开启**：`configs/relay-gateway.yaml` 的 `hybrid_adaptor.enabled: true`。
4. **session_sticky 已开启**（验证 sticky 时）：`session_sticky.enabled: true`。
5. **至少 2 个 enabled 订阅账号**服务同一 `model + group`（验证 failover）。
6. **压测前置**：CI smoke（miniredis）随时可跑；预发全量 k6 需真实多副本。

## 二、必填配置

| 配置 / 环境变量 | 位置 | 必填 | 说明 |
| --- | --- | --- | --- |
| `REDIS_ADDR` | relay-gateway env | ✅（多副本） | 共享 Redis 地址 |
| `REDIS_PASSWORD` | relay-gateway env | ✅ | Redis 密码 |
| `hybrid_adaptor.enabled` | `configs/relay-gateway.yaml` | ✅ | 总开关 |
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

Redis 不可用时（`redisClient == nil` 或命令失败）全部降级到进程内内存实现，请求不被全局阻断。

## 三、共享状态清单

| 状态 | Redis key 前缀 | 作用 | Redis 不可用时 |
| --- | --- | --- | --- |
| 账号并发槽位 | `subscription_account:concurrency:{account_id}` | ZSET 共享 in-flight 租约，TTL 回收 | 降级内存 limiter，单副本内仍 cap |
| runtime block | （Redis blocker 内部 key） | 429/5xx/529 冷却跨副本可见 | 降级内存 blocker，仅本副本可见 |
| 账号 RPM 窗口 | `subscription_account:rpm:*`（近似） | 60s 滚动 RPM 跨副本共享 | 降级内存 RPM limiter |
| 用户 RPM 窗口 | `subscription_user:rpm:*`（近似） | 用户级 RPM 跨副本共享 | 降级内存用户 RPM |
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

临时停 Redis（或挡连接），请求应仍成功（降级内存 limiter），且 `micro_one_api_relay_account_concurrency_fallback_total{reason="acquire_error"}` 递增。恢复 Redis 后 fallback 指标停止递增。CI smoke 对应 `TestStress_AccountConcurrency_RedisOutageFailsOpenWithFallbackMetric`。

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
