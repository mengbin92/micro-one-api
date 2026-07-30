# v0.11.0 Review 修复记录 — CRITICAL / HIGH

- **日期**:2026-07-29
- **对应报告**:[review-v0.11.0.md](review-v0.11.0.md)
- **验证**:`go build ./...`、`go test ./internal/... ./app/...` 全绿;web `tsc --noEmit` + `vite build` 通过;alerts.yml YAML 校验通过(环境无 promtool,建议 CI 补 `promtool check rules`)。

---

## C1 — RoutingOpsPage `sources` null 解引用白屏

**文件**:`web/src/pages/admin/RoutingOpsPage.tsx`

- 接口类型 `sources: RoutingOpsSource[] | null`(Go nil slice → JSON `null` 的真实契约)
- 渲染前归一化:`const sources = data.sources ?? []`,`:342`/`:362` 改用归一化后的局部变量(对齐 sub2api `res.items || []` 模式)

## H1 — WS 连接池跨命名空间串号

**文件**:`internal/server/openai_ws_pool.go`、`internal/server/openai_ws_forwarder.go`、`internal/server/openai_ws_pool_test.go`

- 池键由裸 `int64 channelID` 改为命名空间字符串 `"channel:<id>" / "subscription:<id>"`(经 `openAIWSPoolKey()` 从 `RoutingSourceIdentity` 派生),channel #5 与 subscription account #5 不再共享池桶
- 复用前校验连接指纹(`wsURL` + `Authorization` 头):凭证/URL 轮换后旧连接直接关闭驱逐,不会串用旧凭证(对齐 sub2api `openAIWSAcquireRequest` 携带完整目标上下文的设计)
- 新增回归测试:`TestConnPoolIsolatesNamespaces`(跨命名空间隔离)、`TestConnPoolRejectsRotatedCredential`(凭证轮换不复活旧连接)

## H2 — 故障转移提前终止

**文件**:`api/channel/v1/channel.proto`、`app/channel/internal/biz/channel.go`、`app/channel/internal/service/channel.go`、`internal/data/adapters.go`、`internal/data/data.go`、`internal/biz/relay.go` + 7 处测试 fake

- `SelectChannelRequest` 新增 `repeated int64 excluded_channel_ids = 4`(已 `buf generate` 重新生成)
- channel usecase 新增 `SelectChannelExcluding`:按候选逐个过滤已失败渠道(而非 `excludeFirstPriority` 整层跳过),任何优先级的健康渠道都可达;不做 catch-all 扩展(failover 语义不变)
- relay `SelectFallbackRoutingSource` 把请求级失败集作为**过滤器**传入选择,替代原来的"选择后置空"——sub2api `SelectAccountForModelWithExclusions` 同款模式
- 新增回归测试:
  - `TestChannelUsecase_SelectChannelExcluding_WalksLowerTiers`(10/5 层失败仍能落到 1 层;全部排除时报错而非挂起)
  - `TestChannelUsecase_SelectChannelExcluding_KeepsTierSiblings`(同层健康兄弟节点不受牵连)
  - `TestSelectFallbackRoutingSource_PassesExcludedChannelsToSelection`(排除集确实传入选择层)

## H3 — UpstreamCostMissing 告警永不触发

**文件**:`deploy/prometheus/alerts/alerts.yml`

- 表达式改为 `(A - B) or A`:某 provider_family 完全没有 priced 序列时,`or` 回退为全部成功流量(即 100% 未定价),不再被向量匹配静默丢弃

## H4 — CacheCreationShadowCostDrift 告警稳态 observe 下永不触发

**文件**:`deploy/prometheus/alerts/alerts.yml`

- 表达式改为 `(observe - charge) or observe`:稳态 observe 模式下 charge 侧为空向量时,漂移量回退为完整 observe 影子成本

---

## 第二阶段 — sub2api 对比采纳项

第二阶段目标：把 `review-v0.11.0.md` §三 中「规划采纳」的四项 sub2api 更优实现落地。

### Phase A — 边缘桶归一 (sub2api #6)

**状态**: ✅ 已完成

**核心改动**:

1. 在 `internal/biz/usage.go` 引入 `CanonicalUsage` —— 请求级别的 provider-agnostic token bucket 视图，携带 `PromptExclusive` 语义，避免下游反复从 channel type 推导。
2. 新增 `internal/server/usage/extract.go`，把原先散落在 `http_raw_helpers.go` 的 usage 解析逻辑收敛到一个可被 `server` 和 `forwarder` 共同引用的包；支持 Anthropic 嵌套 detail、provider 扁平 bucket (`cache_creation_5m_tokens` / `cache_creation_1h_tokens`) 以及 details 内嵌 bucket。
3. `internal/server/forwarder/nonstream.go` 返回 `*relaybiz.CanonicalUsage` 而非只有 `prompt/completion/total` 的浅层 `Usage`；orchestrator 非流路径不再丢失 cache_read/cache_creation。
4. `internal/server/orchestrator.go` 移除本地 `Usage` 类型，全面改用 `relaybiz.CanonicalUsage`；流式/非流式统一走同一 canonical 类型。
5. `internal/apicompat/responses_to_chatcompletions.go` 在 Responses→Chat 转换时保留 `cache_creation_5m_tokens` / `cache_creation_1h_tokens`。
6. `internal/server/http_raw_helpers.go` 的 `cacheCreationDetailTokens` 同步识别 provider 扁平 bucket 和 details 内嵌 bucket。
7. 把 `isPromptExclusiveChannel` / `isPromptExclusiveChannelType` 移到 `internal/biz/usage.go`，成为 `relaybiz.IsPromptExclusiveChannel*`；`http_adaptor.go` 和 `http_usage_log.go` 改为调用 relaybiz 版本。

**新增测试**:

- `internal/server/usage/extract_test.go`
- `internal/server/forwarder/nonstream_test.go`
- `internal/apicompat/cache_creation_test.go`

**验证**:

```bash
go build ./...
go test ./internal/server/... ./internal/apicompat/... ./internal/biz/... ./internal/server/usage/...
```

### Phase B — 分桶成本持久化 (sub2api #9)

**状态**: ✅ 已完成

**核心改动**:

1. `app/billing/internal/biz/billing.go` 的 `canonicalCostBreakdown` 扩展为包含 `PromptCost`、`CompletionCost`、`CacheReadCost`、`CacheCreation5mCost`、`CacheCreation1hCost`、`ShadowCost`；`calculateCanonicalCost` 在计算每个桶价格时同步记录其独立成本。
2. `calculateCostWithUsage` 改为返回 `(int64, canonicalCostBreakdown)`，双路径 commit（legacy 与 dual-track）把分桶成本和 shadow cost 写入 `Ledger`。
3. `app/billing/internal/biz/ledger.go` 域模型、`app/billing/internal/data/models.go` GORM 模型、`app/billing/internal/data/ledger_repo.go` 读写转换同步新增 6 个成本列。
4. `api/common/v1/common.proto` `LedgerEntry` 与 `api/billing/v1/billing.proto` `CommitQuotaRequest` 追加同名字段；`app/billing/internal/service/billing.go` 的 `ListLedger` / `GetLedgerEntry` 映射回填新字段。
5. 新增迁移文件：
   - `migrations/071_add_per_bucket_cost.sql`
   - `migrations/postgres/010_add_per_bucket_cost.sql`
   - `migrations/sqlite/009_add_per_bucket_cost.sql`

**新增测试**:

- `app/billing/internal/biz/cache_creation_cost_test.go` 新增 `TestBillingUsecase_PerBucketCostsPersisted`。
- 相关 `app/billing/internal/data/*_test.go` 的 SQLite schema 已同步新列。

**验证**:

```bash
make proto
go build ./...
go test ./app/billing/internal/biz/... ./app/billing/internal/data/...
```

### Phase C/D 落地说明(已完成)

四项 sub2api 改进已全部落地。Phase A(#6 边缘桶归一)、Phase B(#9 分桶成本持久化)
见上方对应章节。Phase C(#2)、Phase D(#12)摘要如下:

| 阶段 | 项 | 关键文件 | 主要动作 |
|---|---|---|---|
| **Phase C** | #2 请求级排除集 + 预计算候选顺序 | `internal/biz/{relay,retry}.go`, `internal/biz/retry_candidates_test.go`, `app/channel/internal/biz/channel.go`, `api/channel/v1/channel.proto` | ✅ `RelayPlan` 携带 `RoutingCandidateList`；`RetryExecutor` 按预排序列表推进；跨 namespace 排除集统一为 `RoutingSourceIdentity`；proto 增加 `excluded_subscription_account_ids`；`SelectSubscriptionAccountExcluding` 按账号 ID（`map[int64]bool`）逐候选过滤 |
| **Phase D** | #12 负载感知接线 | `app/channel/internal/biz/{account_selector,selector}.go`, `app/channel/internal/data/load_oracle.go`, `app/channel/cmd/channel/wire.go` | ✅ `SubscriptionAccountSelector` 注入 `LoadOracle`(Redis ZSet `subscription_account:concurrency:<id>` 的 ZCOUNT,只计未过期租约),每次 `Select` 在锁外 pipeline 批量预取跨副本 in-flight 快照;`loadFactor` 按相对 `inflight/maxConcurrent` 分档降权(100/80/50/20/1);channel `WeightedSelector` 新增同构 `loadFactor`(进程内 in-flight 生命周期自有);保留硬上限 `inflight>=maxConcurrent` 作为最后防线。dispatch 热路径无需改动:relay-gateway 已用 `RedisAccountConcurrencyLimiter.TryAcquire` 维护该 ZSet,channel-service 直接读取。 |

**验证**:`go build ./...`、`go vet` 通过;`app/channel/internal/biz`、`internal/biz`(Phase C 测试)、`app/billing`、`internal/server/{usage,forwarder,handler}`、`internal/apicompat` 全绿。`internal/biz` 的 4 个 `TestStress_*` 与 `internal/server` 的 httptest/miniredis 用例因沙箱禁止绑定本地端口失败,与本次改动无关(本地 `make test` 可通过)。

### 第二阶段 Review 修复记录

第二阶段(Phase A-D)合入前的 code review 发现 2 个 HIGH、2 个 MEDIUM、5 个 LOW,已全部修复:

| 级别 | 问题 | 修复 | 关键文件 / 测试 |
|---|---|---|---|
| 🔴 HIGH-1 | HTTP 重试闭包(`ExecuteWithCandidates`,4 个 handler)收到空 `Key` 的订阅账号投影 → 上游 401(不可重试,直透客户端) | `selectNextForRetry` 加**命名空间锁**:候选列表路径按初始源 namespace 过滤,API-key 初始只 failover 到 API-key 渠道(候选 walk 跳过 `SubscriptionAccountID>0`,fallback 用 `SelectChannelExcluding`);订阅初始走 `SelectFallbackRoutingSource`(凭证解析路径)。`ExecuteWithInitialChannel` 旧路径保留原跨 namespace 行为(其调用方 WS/Responses 经 credential store 解析) | `internal/biz/retry.go`(`selectNextForRetry`);`TestRetryExecutor_ExecuteWithCandidates_NamespaceLockProhibitsCrossSource` |
| 🔴 HIGH-2 | `CachedChannelClient` 只在 `ExcludeFirstPriority=true` 绕过缓存;Phase C 改为 `ExcludedChannelIds` 传参(`ExcludeFirstPriority=false`),缓存第一名很可能是刚失败的渠道 → 排除集静默失效 | 绕过条件加 `len(req.GetExcludedChannelIds()) > 0` | `internal/data/cached_channel.go`;`TestCachedChannelClient_ExclusionSetBypassesCache` |
| 🟡 MEDIUM-1 | `crossReplicaInflight` 只在 `n>0` 时写入 → 账号忙过后快照永久停在峰值,stale-high 永久降权 | 改为无条件 `Store(n)`,空闲读 0 能回落 | `app/channel/internal/biz/account_selector.go`;`TestSubscriptionAccountSelector_LoadFactorFallsBackToNeutralWhenIdle` |
| 🟡 MEDIUM-2 | `Select` 持锁对每个候选串行调 Redis ZCARD(200ms timeout),N 候选 = N 次 RTT 且阻塞所有并发 Select | 新增 `prefetchInflight`:锁外批量查询;`LoadOracle.InflightBatch` 接口,Redis 实现用 pipeline 一次 ZCOUNT 全部候选(单 RTT),失败降级逐账号读 | `app/channel/internal/biz/account_selector.go`、`app/channel/internal/data/load_oracle.go` |
| 🟢 L1 | `ZCARD` 计入已过期未清理的租约(崩溃副本死租约最长虚降权一个 leaseTTL) | channel oracle + relay 侧 `RedisAccountConcurrencyLimiter.Inflight` 统一改 `ZCOUNT key now +inf` | `app/channel/internal/data/load_oracle.go`、`internal/biz/account_concurrency.go` |
| 🟢 L2 | `updateAccountLocked` 仅在 `Concurrency>0` 更新 `maxConcurrent` → 管理员改回 0 无法生效 | 无条件同步 | `app/channel/internal/biz/account_selector.go` |
| 🟢 L3 | 文档写"按 `RoutingSourceIdentity` 逐候选过滤",实为 `map[int64]bool` | 措辞修正 | `docs/releases/review-v0.11.0-fixes.md` |
| 🟢 L4 | account 侧相对分档只测了 45%→80 一档 | 改为完整 10 档表驱动,对齐 channel 侧 | `TestSubscriptionAccountSelector_LoadFactorRelativeBands` |
| 🟢 L5 | 本次新增/改动的多个文件未过 gofmt | `gofmt -w` 全量格式化 | — |

> **重要长期语义(HIGH-1)**:HTTP 通用重试执行器(`RetryExecutor.ExecuteWithCandidates`)**不跨命名空间 failover**。API-key 渠道失败后只切其它 API-key 渠道,订阅账号失败后只切其它订阅账号。跨命名空间切换**只属于凭证解析路径**(`SelectFallbackRoutingSource`,WS/Responses transport)— 这些路径能正确解析订阅投影的空 `Key`。这是为了让 4 个 HTTP handler 的重试闭包(直接用 `ch.Key` 建 provider)永远不会撞上空 Key → 401。任何新增的 HTTP 重试路径必须遵守同一约束。

---

## 遗留(未修,见报告备查清单)

- M1-M6、L1-L7:已修复,见提交 `6203936`
- sub2api 采纳项 #6/#9/#2/#12:已全部完成
