# sub2api 可借鉴内容清单(订阅账号处理)

> 对比对象:同级目录 `../sub2api`(单体 Gin + ent)与本项目 `micro-one-api`(Kratos 微服务)。
> 聚焦:上游**订阅账号**(OAuth / setup_token)的处理逻辑。
> 生成日期:2026-07-01。基于代码实证核对,已排除本项目已具备的能力。

> **实施状态(2026-07-01 更新)**:三项 🔴 高价值·低成本已全部实现并测试通过 ——
> #1 同账号重试、#2 账号级并发强制、#3 529 独立冷却。详见各条目下的「✅ 已实现」说明。
> **#7 跨会话账号粘性** 已实现(Chat + Anthropic 入口,独立开关默认关;Responses/WS 沿用既有粘性)——
> 详见 #7 条目下的「✅ 已实现」。其余 🟡/🟢 项(#4、#5、#6)仍为待办。

## 现状澄清(避免重复造轮子)

本项目 **已具备**:

- token 刷新体系(`internal/relay/credential/`,3min skew + 后台 `RefreshTask`)
- Redis 运行时封禁(`internal/relay/biz/runtime_blocker.go`)
- 跨账号 failover(`internal/relay/server/http_adaptor.go`)
- Codex 配额快照 + 阈值自动暂停(`internal/relay/quota/codex.go`)
- 渠道级 sticky(仅 OpenAI WS 的 response→channel,`config.go:229`)
- **订阅组级** `RateMultiplier`(`internal/subscription/data/group_repo.go:21`)
- WS 连接池并发控制(`internal/relay/server/openai_ws_pool.go`)

**半成品(已定义未接线)**:

- `KindRetryableOnSameAccount`(`internal/relay/passthrough/upstream_error.go:12`)已定义,
  但 `server/`、`biz/` 中无任何消费者 —— 分类建了,同账号重试循环没接。
- `SubscriptionAccount.Concurrency`(`internal/channel/biz/channel.go:109`)已一路存到 DB
  (`internal/channel/data/data.go:97`),但调度时**没有信号量真正强制**。

**缺失**:账号级会话窗口、账号级计费倍率、负载感知/排队。
(账号级并发强制 #2、跨会话账号粘性 #7 已实现。)

---

## 可借鉴清单(按性价比排序)

### 🔴 高价值 · 低成本

#### 1. 补全"同账号重试"循环(半成品收尾) ✅ 已实现

- **问题**:`upstream_error.go:12` 定义了 `KindRetryableOnSameAccount`(409/423),
  但 failover 循环只做跨账号切换,该分类形同虚设。
- **sub2api 做法**:对临时性错误(Google 400、空响应 502)**先同账号重试 ≤3 次、间隔 500ms**,
  再降级到换账号(`handler/failover_loop.go`)。
- **收益**:避免瞬时错误就把好账号踢进 blocker,减少不必要的账号消耗。
- **落点**:`internal/relay/server/http_adaptor.go` failover 循环里,
  进入 `blockRuntimeAccount` 前先判 `RetryableOnSameAccount`。
- **✅ 已实现**:新增 `runSubscriptionAttempt` —— 同账号先重试 ≤3 次
  (`subscriptionSameAccountMaxRetries`)、间隔 500ms(`sleepCtx` 尊重 context 取消),
  不进 blocker、不占换账号预算;耗尽后升级为跨账号 failover(409/423 冷却时长为 0,
  只排除不封禁)。`UpstreamError.RetryableOnSameAccount()` 显式判定。
  测试:`TestHandleChatCompletionsViaAdaptor_SameAccountRetry`。

#### 2. 账号级并发强制(`Concurrency` 字段落地) ✅ 已实现

- **问题**:`Concurrency` 字段已存 DB / 结构体,但调度时无信号量卡它,只有 WS 连接池有 acquire。
- **sub2api 做法**:`SelectAccountWithLoadAwareness` 对每个账号 acquire 并发槽位。
- **收益**:防止单账号被打爆触发上游 429,把限流"前移"到自己这侧。
- **落点**:`AccountPool.IsSchedulable`(`internal/relay/biz/account_pool.go`)旁加 per-account 信号量
  (内存版即可,多实例再上 Redis 计数)。
- **✅ 已实现**:proto 补 `SubscriptionAccountInfo.concurrency=18`,channel-service → relay biz →
  data/adapters 全链路映射;新增内存版 `AccountConcurrencyLimiter`
  (`internal/relay/biz/account_concurrency.go`,`TryAcquire` 幂等释放,limit≤0 即不限)。
  请求前占槽,**流式期间持有直至写完释放**;满额账号视为"健康但忙" → failover 到其他账号且
  **不冷却**(`concurrencyFull`)。当前为单进程内存版,多副本需 Redis 计数器(后续)。
  测试:`account_concurrency_test.go`、`TestHandleChatCompletionsViaAdaptor_ConcurrencyFailover`。

#### 3. 529(Overload)独立冷却状态 ✅ 已实现

- **问题**:529 目前仅在 `openai_ws_forwarder.go:206` 被当普通 retryable,
  未像 sub2api 那样给账号打 `OverloadUntil`(依 `retry-after`)。429/529 语义不同(限流 vs 过载),冷却时长应可区分。
- **落点**:`SubscriptionAccount` 加 `OverloadUntil`,或复用 blocker 但用独立时长策略。
- **✅ 已实现**:`passthrough` 新增 `KindOverloaded`(529)+ 常量 `StatusOverloaded`,529 现在
  跨账号 failover 且耗尽后 passthrough(带 Retry-After);复用 runtime blocker 但用独立时长
  `runtimeBlockConfig.overloaded`(默认 30s,区别于 5xx 的 2m),经 `overloaded_duration`
  全链路可配(`config.go`/`wire_gen.go`/`configs/relay-gateway.yaml`)。
  测试:`upstream_error_test.go`、`TestHandleChatCompletionsViaAdaptor_FailoverOn529`。

### 🟡 中价值 · 中成本

#### 4. 账号级计费倍率 `RateMultiplier`

- **问题**:本项目只有订阅**组级**倍率;sub2api 是两层
  `account.rate_multiplier × group.rate_multiplier`,账号级可为 0(免费账号)。
- **收益**:不同订阅账号成本不同(Pro / Team / 免费)时精确核算;快照进 ledger 便于对账。
- **落点**:`subscription_accounts` 加字段 + `internal/billing/data/models.go` ledger 快照
  `account_rate_multiplier`。

#### 5. 账号级会话窗口(Claude Pro 5h 滚动窗)

- **问题**:本项目的 window 都是**下游用户订阅**的日/周/月计费窗(`subscription_usecase.go`),
  缺**上游账号**的会话窗概念。
- **sub2api 做法**:`SessionWindowStart/End/Status` 追踪 Claude 账号会话窗。
- **说明**:与现有 Codex `quota_snapshot`(已解析 primary/secondary window)互补,补齐 Claude 侧。

### 🟢 结构性 · 高成本(看规模再定)

#### 6. 负载感知调度 + 排队(Wait Plan)

- **sub2api 做法**:`SelectAccountWithLoadAwareness` 按队列深度返回"立即拿"或"排队等"。
- **现状**:本项目选不到就 fallback/报错,无排队。
- **收益**:高并发平滑削峰,而非直接 502。成本较高,账号池紧张时才做。

#### 7. 跨会话账号粘性(会话→账号) ✅ 已实现

- **现状**:已有 response→channel sticky,缺"同一对话尽量固定同一订阅账号"。
- **sub2api 做法**:`sessionHash → accountID` 缓存。
- **收益**:对 Codex/Claude 有上下文缓存的上游,粘性显著提升 prompt cache 命中、降成本。
- **落点**:复用现成 sticky 存储(`config.go:229` StickyTTL + Redis),
  key 从 responseID 扩展到 sessionHash。
- **✅ 已实现(本期范围:Chat + Anthropic 入口)**:
  - **候选选择在 biz**:`RelayRequest.SessionHash` 贯穿;`RelayUsecase.Plan` 在解析 auth 得到
    group 后、订阅选择前,经 `SessionAccountStore.LookupSessionChannel(group, sessionHash)`
    查绑定账号,`selectStickySubscriptionAccount` 用 `GetSubscriptionAccountByID` 物化并校验
    (启用状态 / 同 group / platform+model 匹配 / 非 runtime-blocked;**不校验并发**),命中即用并刷新 TTL,
    未命中回退普通优先级选择。见 `internal/relay/biz/relay.go`。
  - **bind/rebind 在 server**:`handleSubscriptionAccountViaAdaptor` 失败转移循环在**成功(2xx)** 的
    实际服务账号上 `bindSubscriptionSession`(`internal/relay/server/http_adaptor.go`);同账号重试 +
    跨账号 failover 后只绑定一次,故 sticky 账号并发满时自动 failover 到兄弟并**重绑**、不冷却。
  - **入口取值**:chat/anthropic 先读原始 body 再 typed decode,用现成
    `extractSessionHashFromRequest`(先 header `X-Session-Hash`/`OpenAI-Session-Hash`,再 body `session_hash`)。
  - **复用现成存储**:复用 `openAIWSStickyStore`(session→id,内存热缓存 + Redis 兜底,key 前缀
    `openai_ws_session:`);TTL 复用 `openai_ws.sticky_ttl`(默认 1h);Redis 挂掉时 lookup 返 0 → 静默按 miss。
  - **独立开关默认关**:`session_sticky.enabled`(`config.go` / `wire_gen.go` /
    `SetSubscriptionSessionStickyEnabled`),仅在 `hybrid_adaptor.enabled` 打开时生效。
  - **收益指标**:`RelaySubscriptionStickyTotal{result,platform}`,`result ∈ {hit, rebind, miss,
    reused_unschedulable}`;**复用率 = hit / (hit + rebind + miss)** 按 platform(claude/codex)拆分,
    用于评估上游 prompt cache 收益。
  - **说明 / 后续**:Responses/WS 入口**沿用其既有 session→channel 粘性**,未统一进新的
    schedulability-aware 账号粘性(避免动已上线关键路径);待指标验证上游确有 prompt cache 收益后,
    再作为第二步统一。
  - 测试:`internal/relay/biz/relay_test.go`(`TestRelayUsecasePlan_Sticky*`)、
    `internal/relay/server/http_adaptor_test.go`(`TestSubscriptionSticky_*`)。

---

## 建议优先级

- ~~**先做 #1、#2、#3**~~ ✅ 已全部完成(2026-07-01)。
- ~~**#7(会话粘性)**~~ ✅ 已完成 Chat + Anthropic 入口(2026-07-01)。
  下一步:用 `RelaySubscriptionStickyTotal` 复用率验证上游 prompt cache 收益;
  若有收益,再把 Responses/WS 入口统一进新的账号粘性(第二步)。
- **#4 / #5** 视商业化精细度决定。
- **#6** 视账号池规模决定。

---

## 关键文件索引

| 主题 | 本项目 (micro-one-api) | sub2api |
|---|---|---|
| 订阅账号模型 | `internal/channel/biz/channel.go` | `backend/ent/schema/account.go` |
| 账号选择 | `internal/relay/biz/relay.go` `selectSubscriptionChannel` | `backend/internal/service/gateway_service.go` `SelectAccountWithLoadAwareness` |
| 错误分类/failover | `internal/relay/passthrough/upstream_error.go`, `internal/relay/server/http_adaptor.go` | `backend/internal/handler/failover_loop.go` |
| token 刷新 | `internal/relay/credential/refresh_task.go` | `backend/internal/service/token_refresh_service.go` |
| 配额 | `internal/relay/quota/codex.go` | `backend/internal/service/account_usage_service.go` |
| 计费倍率 | `internal/subscription/data/group_repo.go`(仅组级) | `backend/internal/service/billing_service.go`(账号级+组级) |
| 运行时封禁 | `internal/relay/biz/runtime_blocker.go` | 账号行字段 `RateLimitedAt` / `OverloadUntil` / `TempUnschedulableUntil` |
