# Relay 稳定性压测与 Runbook

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 3: Relay 稳定性。
> 范围: Redis 并发控制、session sticky、failover 组合场景的压测脚本、固定指标和回归门槛。

本文档定义 Relay 稳定性的压测分层 (CI smoke vs 预发全量)、固定指标集、每个场景的验收标准,以及如何排障。新部署或改动 `internal/relay/...`、`internal/channel/...` 的分支都必须按本文档执行回归门槛。

## 1. 压测分层

| 层级 | 环境 | 工具 | 触发时机 | 覆盖范围 |
| --- | --- | --- | --- | --- |
| **CI smoke** | CI (无外部依赖) | `go test -run TestStress ./internal/relay/...` | 每次 PR / 回归门槛 | 所有场景的 hermetic 等价 (miniredis 模拟多副本 Redis) |
| **预发全量** | 预发集群 (≥2 relay 副本 + 真实 Redis) | `k6 run scripts/benchmark/k6-relay-subscription-stress.js` | 发版前 / Relay 大改后 | 真实多副本 + 真实 Redis + 真实上游 |

CI smoke 用 miniredis (进程内 Redis double) 模拟多副本共享 Redis 的 EVAL/TTL 语义,无需外部 Redis,可在任何 CI runner 上运行。预发全量在真实多副本部署上跑 k6,验证跨进程 sticky、真实 Redis 故障注入和真实上游 failover。

## 2. 固定指标集

每个压测运行 (无论 CI smoke 还是预发全量) 必须产出以下指标。CI smoke 由 `internal/relay/stresstest/report.go` 的 `Report.Print` 输出;预发全量由 k6 + Prometheus scrape 提供。

| 指标 | 说明 | CI smoke 来源 | 预发来源 |
| --- | --- | --- | --- |
| **成功率** | 2xx 终态请求占比 | `report.success_rate` | k6 `relay_success_rate` |
| **p50 / p95 / p99 延迟** | 请求延迟分位数 (ms) | `report.latency_ms_p50/p95/p99` | k6 `relay_latency_ms` |
| **failover 次数** | 跨账号切换总数 | prometheus `RelaySubscriptionFailoverTotal` delta | Prometheus `micro_one_api_relay_subscription_failover_total` |
| **failover 原因** | 按 reason 分类的 failover (429/5xx/529/concurrency/rpm/session_window) | `report.failover_reasons` + prometheus labels | Prometheus label `reason` |
| **Redis fallback 次数** | Redis 故障降级到内存 limiter 的次数 | prometheus `RelayAccountConcurrencyFallbackTotal` / `RelayAccountRPMFallbackTotal` delta | Prometheus `micro_one_api_relay_account_concurrency_fallback_total` / `..._rpm_fallback_total` |
| **账号并发峰值** | 观测到的单账号最大 in-flight 并发 | `report.concurrency_peak` | Prometheus gauge (per-account in-flight) |
| **账号并发是否超限** | 峰值是否超过配置的 `Concurrency` cap | `report.concurrency_within_cap` | 人工核对峰值 ≤ 配置 cap |

## 3. 场景与验收标准映射

### 3.1 多副本 Redis account concurrency limiter

**验收**: Redis 正常时,多副本同账号并发不超过配置。

| 检查项 | CI smoke 测试 | 预发 k6 场景 |
| --- | --- | --- |
| 多副本同账号并发峰值 ≤ 配置 cap | `TestStress_AccountConcurrency_MultiReplicaWithinCap` (biz) | `concurrency_rpm` + Prometheus 核对峰值 |
| Redis EVAL 原子性保证不超限 | 同上 (miniredis 真实 EVAL) | 同上 |

**CI smoke 命令**:
```bash
go test ./internal/relay/biz/... -run TestStress_AccountConcurrency_MultiReplicaWithinCap -v
```

### 3.2 Redis runtime blocker

**验收**: 一个副本观测到的 429/5xx block 对所有副本可见;TTL 到期后自动恢复。

| 检查项 | CI smoke 测试 |
| --- | --- |
| 跨副本 block 可见性 | `TestStress_RuntimeBlocker_CrossReplicaAndExpiry` (biz) |
| TTL 到期自动恢复 (FastForward) | 同上 |

### 3.3 Redis 短暂不可用 fail-open

**验收**: Redis 短暂不可用时,请求 fail-open 到内存 limiter,并有 fallback 指标。

| 检查项 | CI smoke 测试 |
| --- | --- |
| Redis 故障时请求仍成功 (内存 fallback) | `TestStress_AccountConcurrency_RedisOutageFailsOpenWithFallbackMetric` (biz) |
| fallback 指标递增 | 同上 (`RelayAccountConcurrencyFallbackTotal` delta > 0) |
| 内存 fallback 仍执行 cap | 同上 (`concurrency_peak ≤ limit`) |

### 3.4 session_hash sticky

**验收**: 同一 session_hash 的后续 turn 复用同一账号 (prompt-cache 命中)。

| 检查项 | CI smoke 测试 | 预发 k6 场景 |
| --- | --- | --- |
| 首次 bind 后后续 turn 命中 sticky | `TestStress_SessionHashSticky_CrossReplicaBindAndReuse` (server) | `session_sticky` |
| 跨副本 sticky (Redis 共享) | 同上 (serverA bind, serverB 命中) | 同上 (多副本) |
| sticky 指标 hit/rebind 区分 | 同上 (prometheus `RelaySubscriptionStickyTotal`) | 同上 |

### 3.5 previous_response_id route sticky

**验收**: `previous_response_id` 路由到服务上一 turn 的账号。

| 检查项 | CI smoke 测试 | 预发 k6 场景 |
| --- | --- | --- |
| 跨副本 response→channel 绑定可见 | `TestStress_PreviousResponseIDSticky_CrossReplicaLookup` (server) | `response_sticky` |
| 高并发下 lookup 一致性 | 同上 (32 并发 reader 全部解析到同一账号) | 同上 |

### 3.6 429 / 5xx / 529 failover

**验收**: 上游 429/5xx/529 触发跨账号 failover,客户端收到 200,失败账号被冷却,failover 指标按 reason 区分。

| 检查项 | CI smoke 测试 | 预发 k6 场景 |
| --- | --- | --- |
| 429 failover → 切换到 sibling → 200 | `TestStress_Failover_429_UnderLoad` (server) | `failover_429` |
| 5xx failover → 切换 → 529 → exhausted | `TestStress_Failover_5xx_Then529_BothRecordReasons` (server) | `mixed_failover` |
| failover reason 指标区分 (5xx vs 529) | 同上 (prometheus label delta) | 同上 |
| 429/5xx 账号被冷却 (runtime block) | 同上 (prometheus `RelayRuntimeBlocksTotal`) | 同上 |

### 3.7 RPM failover

**验收**: 账号 RPM 耗尽时 failover 到 sibling,不冷却该账号。

| 检查项 | CI smoke 测试 |
| --- | --- |
| 多副本 RPM cap 跨副本生效 | `TestStress_AccountRPM_MultiReplicaWithinLimit` (biz) |
| RPM 耗尽 failover 不冷却 | `TestHandleChatCompletionsViaAdaptor_RPMFailover` (server, 既有测试) |

### 3.8 session window failover

**验收**: session 窗口耗尽时 failover 到 sibling,不冷却该账号。

| 检查项 | CI smoke 测试 |
| --- | --- |
| session window 耗尽 → failover → 200 | `TestStress_SessionWindowFailover_UnderLoad` (server) |
| 不冷却 session-window-full 账号 | 同上 (`RelayRuntimeBlocksTotal` delta = 0) |

### 3.9 sticky 账号不可用 → failover 并重新绑定

**验收**: sticky 账号不可用时可以 failover 并重新绑定可用账号。

| 检查项 | CI smoke 测试 |
| --- | --- |
| sticky 账号上游 429 → failover → rebind 到 sibling | `TestStress_StickyAccountUnavailable_FailoverRebinds` (server) |

## 4. 回归门槛

### 4.1 涉及 Relay 行为的分支 (必须)

```bash
# CI smoke (hermetic, miniredis)
go test ./internal/relay/... ./internal/channel/...

# E2E suite (docker-compose)
make test-e2e-suite
```

`go test ./internal/relay/...` 包含所有 `TestStress_*` 压测。它们用 miniredis 模拟多副本 Redis,无需外部依赖。

### 4.2 发版前 (预发全量)

```bash
# 前置: 预发集群 ≥2 relay 副本 + Redis,至少一个 subscription account 已绑定 OAuth
k6 run \
  -e BASE_URL=https://relay-gateway.preprod.internal \
  -e API_KEY=sk-... \
  -e SCENARIO=session_sticky \
  scripts/benchmark/k6-relay-subscription-stress.js

# 依次跑 response_sticky / failover_429 / mixed_failover / concurrency_rpm

# 压测后 scrape Prometheus 核对 failover / fallback / 并发峰值
curl -s $PROM_URL/api/v1/query --data-urlencode 'query=micro_one_api_relay_subscription_failover_total' | jq
curl -s $PROM_URL/api/v1/query --data-urlencode 'query=micro_one_api_relay_account_concurrency_fallback_total' | jq
curl -s $PROM_URL/api/v1/query --data-urlencode 'query=micro_one_api_relay_runtime_block_active' | jq
```

**预发全量验收门槛**:
- `relay_success_rate > 0.99`
- `relay_latency_ms p95 < 2000ms, p99 < 5000ms`
- 多副本同账号并发峰值 ≤ 配置 cap (Prometheus gauge)
- Redis fallback 仅在 Redis 真实故障时出现 (正常时 = 0)

## 5. 常见故障与排障

### 5.1 压测时并发峰值超过配置 cap

**原因**: Redis 未配置或 Redis limiter 未生效,降级到单进程内存 limiter。

**排查**:
1. 确认 relay-gateway 配置了 `REDIS_ADDR`。
2. 确认 `wire_gen.go` 中 `NewRedisAccountConcurrencyLimiter(redisClient)` 返回非 nil。
3. 检查 Prometheus `micro_one_api_relay_account_concurrency_fallback_total` 是否递增 (Redis acquire_error)。
4. 如果 fallback 指标递增,Redis 连接有问题;检查 Redis 可达性和密码。

### 5.2 sticky 命中率为 0

**原因**: session stickiness 未启用,或 Redis sticky store 未配置。

**排查**:
1. 确认配置 `SessionSticky.GetSessionStickyEnabled() = true`。
2. 确认 `SetOpenAIWSStickyStore(redisClient)` 被调用 (需要 Redis)。
3. 检查请求是否携带 `session_hash` (body 或 `X-Session-Hash` header)。
4. 检查 prometheus `RelaySubscriptionStickyTotal{result="miss"}` 是否主导 (说明绑定未建立)。

### 5.3 failover 全部 exhausted (无 sibling 可用)

**原因**: 账号池太小,或所有 sibling 都被 runtime block / concurrency-full。

**排查**:
1. 确认有 ≥2 个 enabled subscription account 服务同一 model + group。
2. 检查 prometheus `RelayRuntimeBlockActive` 是否持续高位 (账号被反复冷却)。
3. 检查 runtime block duration 配置是否过长 (429 默认 5s,5xx 默认 2m)。
4. 如果 failover reason 是 `concurrency`,检查 sibling 的 `Concurrency` 配置是否过小。

### 5.4 Redis fallback 指标持续递增 (Redis 正常时)

**原因**: Redis 连接超时或命令失败。

**排查**:
1. 检查 Redis `ReadTimeout` / `WriteTimeout` 是否过短 (默认 3s)。
2. 检查 Redis 连接池大小是否足够 (`PoolSize` 默认 100)。
3. 检查 Redis 服务器负载和内存。
4. 查看 `RelayAccountConcurrencyFallbackTotal{reason="acquire_error"}` vs `"release_error"` vs `"refresh_error"` 区分故障阶段。

## 6. 文件索引

| 文件 | 说明 |
| --- | --- |
| `internal/relay/stresstest/report.go` | 压测报告 helper: 延迟分位数、成功率、failover/fallback 计数、并发峰值 vs cap |
| `internal/relay/stresstest/report_test.go` | report helper 单元测试 |
| `internal/relay/biz/stress_test.go` | biz 层压测: 多副本 Redis 并发 cap、Redis 故障 fail-open、runtime blocker 跨副本+TTL、RPM cap |
| `internal/relay/server/stress_test.go` | server 层压测: session_hash sticky、previous_response_id sticky、429/5xx/529 failover、concurrency/session-window failover、sticky 账号不可用 rebind |
| `scripts/benchmark/k6-relay-subscription-stress.js` | 预发全量 k6 脚本 (5 场景) |
| `docs/runbooks/relay-stress-runbook.md` | 本文档 |
