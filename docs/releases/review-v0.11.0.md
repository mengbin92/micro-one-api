# v0.11.0 发版代码 Review 报告

- **日期**:2026-07-29
- **范围**:`v0.10.2..v0.11.0`(17 commits,121 文件,+14,261/-351)
- **方法**:5 个并行审查代理(计费 / 中继路由 / 渠道模型治理 / 管理端 / 前端),全部发现经代理自我验证,CRITICAL/HIGH 经人工二次核验;并与同级目录 `sub2api` 项目的同类实现对比,择优给出采纳建议。

---

## 一、缺陷清单(按严重度)

### CRITICAL(1)

**C1. RoutingOpsPage 白屏崩溃 — `sources` 未做 null 防护**(已核验)
`web/src/pages/admin/RoutingOpsPage.tsx:342` — `data.sources.length` / `:362` `.map` 直接解引用,但后端 `Sources []routingOpsSource`(`app/admin/internal/server/routing_ops.go:37`)nil slice 序列化为 `"sources":null`。三种常见响应都会触发:静默 24h 无流量(聚合循环从未 append)、billing 聚合失败返回 200+partial(`routing_ops.go:156-163`)、`svc==nil` 早退(`routing_ops.go:119-122`)。→ `TypeError` 白屏,而非预期的"无流量数据"。旁边的 `errors`/`alerts` 都有守卫,唯独 `sources` 没有。
**修复**:`const sources = data.sources ?? []`。

### HIGH(4)

**H1. WS 连接池跨命名空间串号 — 错误上游+错误凭证**(已核验)
`internal/server/openai_ws_forwarder.go:574` + `internal/server/openai_ws_pool.go:70-105` — 连接池以裸 `int64 channelID` 为键,`tryReuse` 不校验 wsURL/凭证。v0.11.0 让订阅账号投影(`Channel.ID` = 账号 ID,独立 ID 空间)走同一池 → 普通渠道 #5 与订阅账号 #5 共享池桶,账号 5 的请求复用渠道 5 的连接:流量打到错误上游、用错误凭证、账单记错来源。
**修复方向**:池键改用 `RoutingSourceIdentity`(kind+id),或复用前校验 wsURL+凭证指纹(参照 sub2api `service/openai_ws_pool.go:63-77` 的 `openAIWSAcquireRequest`)。

**H2. 故障转移提前终止 — 健康低优先级渠道永远轮不到**(已核验)
`internal/biz/relay.go:1025` — `SelectFallbackRoutingSource` 中 `SelectChannel` 若返回刚失败(已排除)的渠道,代码将其置 nil 后直接落入 "no fallback routing source",而不是继续走下一优先级层。熔断阈值默认 3 次,单次失败不熔断 → 第二次失败时 `SelectChannel` 仍返回同一渠道 → 排除检查置 nil → WS 连接关闭,健康渠道从未被尝试。
**修复方向**:选择循环内排除已失败源并继续,直到候选耗尽(参照 sub2api 请求级排除集,`openai_gateway_handler.go:1670-1738`)。

**H3. UpstreamCostMissing 告警在其要监测的场景下永不触发**(已核验)
`deploy/prometheus/alerts/alerts.yml:236-241` — `A - B` 按 `provider_family` 做向量匹配,某 family 全无 priced 记录(正是告警要抓的状态)时 LHS 序列被直接丢弃 → 告警沉默。
**修复**:`or vector(0)` 零填充 / `or on(provider_family)` 补全序列。

**H4. CacheCreationShadowCostDrift 告警稳态 observe 模式下永不触发**(已核验)
`deploy/prometheus/alerts/alerts.yml:246-256` — shadow cost 只以当前活跃 mode 标签上报,稳态 observe 模式下 `{mode="charge"}` 为空向量 → 减法无结果 → 永不触发。
**修复**:`or vector(0)` 零填充。

### MEDIUM(6,本次不修,记录备查)

| # | 位置 | 问题 |
|---|---|---|
| M1 | `http_chat_handler.go:144,176`、`anthropic_inbound.go:501,533`、`http_raw_handler.go:131` | 重试闭包内 usage-log 用 `applyPlanInputs(plan)`(原始 plan 的 SourceKind/UpstreamModelID/PromptExclusive)+ 实际执行的 fallback 渠道 `ch` — 计费用 A 的成本键和 token 桶语义记 B 的用量,可致 prompt token 为负、成本键错配。WS 路径已用正确的 `applyChannelInputs(ch)`(`http_usage_log.go:31`),HTTP 三条路径没有 |
| M2 | `http_responses_handler.go:168,206,242,279,380` | failover 到普通渠道 B 成功后,`storeResponseRoute` 仍存原始 plan 的 `SubscriptionAccountID: A`;新消费者 `ResolveStoredRoute`(`response_scheduler.go:48-63`)优先取订阅账号 → 下一轮 `previous_response_id` 把请求路由回已失败的 A |
| M3 | `app/admin/internal/service/upstream_cost.go:165` | `MigrateUpstreamCostKeys` 把属于订阅账号的 legacy 键误改写为 `channel:<id>:…`(数字 ID 撞名时),订阅流量上游成本静默归零 |
| M4 | `app/admin/internal/service/upstream_cost.go:236-241` | `SetUpstreamCost` 整条目覆盖只写 input/output price,管理员改价会静默删除同键的 `cache_read_price`/`cache_creation_*_price` |
| M5 | `app/admin/internal/server/models.go:530-544` | `model_unpriced_routed` gauge 只在人工访问 `/api/admin/models/unpriced` 时更新 → RoutedModelsUnpriced 告警依赖有人看页面 |
| M6 | `app/admin/internal/server/http.go:159-189` + `deployments/docker-compose/docker-compose.yml:215` | `/admin/routing-ops` 未注册 SPA fallback(刷新 404);`PROMETHEUS_URL` 默认为空且仓库内无设置 → routing-ops 页指标永久 partial |

### LOW(6,记录备查)

- L1 `internal/server/http_usage_log.go:121` — `cache_read_input_ratio` 分母漏 `CacheReadTokens`,Anthropic 缓存重流量下比率可到 100(应 ~0.99)
- L2 `app/billing/internal/biz/billing.go:874-885` — 上游成本 metric 只在 dual-track 路径上报,legacy 路径不上报 → 告警分子恒近零
- L3 `app/admin/internal/service/upstream_cost.go:193` — 迁移 apply 会用陈旧 legacy 价覆盖已配置的新 canonical 价
- L4 `app/channel/internal/data/model.go:333` — 孤儿映射虚增 ChannelCount → UnpricedRoutedModels 误报
- L5 `app/channel/internal/data/model_exchange_preflight.go:81` — 同一文档内转移 alias 被误判冲突,需分两次导入
- L6 `app/channel/internal/data/model.go:290` — `escapeLike` 无 `ESCAPE` 子句,SQLite 下含 `_`/`%` 的关键字搜索静默返回空
- L7 `internal/server/openai_ws_state_store.go:197` — 滚动升级期间旧 Redis sticky 绑定(裸整数)被解码为订阅账号 ID,可能错路由至 TTL 过期

---

## 二、sub2api 对比 — 建议采纳的更优实现

### 直接对应缺陷的修复范式

1. **WS 池键 = 命名空间化身份 + 复用前校验**(对应 H1):sub2api 池 acquire 携带完整目标上下文 `openAIWSAcquireRequest{Account, WSURL, Headers}`(`service/openai_ws_pool.go:63-77`),复用时做 `matchesBetaFeatures` 校验(:869)。
2. **请求级排除集 + 预计算候选顺序**(对应 H2,根治重试语义):sub2api 在 failover 循环里累积 `failedAccountIDs` 并作为**过滤器**传入每次选择(`openai_gateway_handler.go:1670-1738`、`gateway_scheduling.go:34`),候选顺序每请求只算一次、重试按序推进(`openai_account_scheduler.go:986-1178`)。我们的 `SelectChannel` 每次重试重新选择、可反复返回同一失败渠道。最值得整体采纳的设计。
3. **ON DELETE CASCADE 外键**(对应 M6-渠道孤儿):sub2api 所有渠道卫星表都有 `REFERENCES channels(id) ON DELETE CASCADE`(migrations/081, 101)。
4. **告警缺失序列处理**(对应 H3/H4/M5):sub2api 用应用侧定时评估器(`ops_alert_evaluator_service.go:349-390`,连续违约计数+心跳)替代依赖向量匹配的 PromQL;指标采集由带 Redis 选主的后台 collector 驱动(`ops_metrics_collector.go:108-127`),不靠人工页面访问。短期先用 `or vector(0)` 补 PromQL。
5. **前端 null 数组归一**(对应 C1):sub2api 在赋值点统一 `res.items || []`(`ChannelMonitorView.vue:209` 等)。

### 计费正确性(observe→charge 切换前的关键差距)

6. **单一桶语义在边缘归一**:sub2api 在 provider 层就把 OpenAI 子集用量转成互斥桶(`openai_gateway_usage.go:139-146`),计费核心只有一套语义;我们用 proto 传 `prompt_exclusive` 标志+硬编码渠道类型列表分类,新增渠道类型漏配即双倍计 cache_read。建议:边缘归一,去掉 wire 标志。
7. **5m/1h 价格合理性守卫**:sub2api 仅当 `price1h > price5m > 0` 才启用 TTL 拆分(`billing_service.go:828-833`);我们无跨桶校验,错配即少收。
8. **Cache-TTL 按账号实际请求重分桶**:sub2api 计费前把上报的 creation token 重归到账号实际使用的 TTL 并改写 SSE(`gateway_upstream_response.go:1263-1318`);我们按上游上报口径直接计费。
9. **分桶成本持久化**:sub2api 每个 usage 行存 `cache_creation_cost`/`cache_read_cost`,供应商账单对账是一条 SQL;我们只落 token 数和总额,shadow cost 只在日志+histogram — observe 模式的"与供应商账单比对再切 charge"闭环无法从持久数据重建。
10. **未定价 cache creation 不 fail-open 到 $0**:sub2api 有硬编码兜底价+派生规则(input×1.25);我们 charge 模式下未定价桶按 0 提交,ratio 定价模型甚至完全不收 creation 费。
11. **粘性路由再验证**:sub2api 每次查找 sticky/stored route 都重校验(状态/熔断/模型能力/配额)并删除过期绑定(`openai_ws_forwarder_support.go:450-546`);我们 channel 类绑定只 `GetChannel` 不查状态,已禁用/熔断渠道仍服务续传流量。
12. **负载感知是真接线的**:sub2api 调度热路径用 Redis 并发槽批量取实时负载(`openai_account_scheduler.go:1378-1392`);我们的 Acquire/Release/loadFactor(`app/channel/internal/biz/account_selector.go:105-114`)目前是死代码,没有任何 relay 分发路径调用 — v0.11.0 的"负载感知选择"实际零保护。
13. **写入期拒绝重叠模型模式**:sub2api 在渠道创建/更新时拒绝 `claude-*` 与 `claude-opus-4` 这类重叠(`channel_service.go:973-1028`);我们静默接受,registry auto-sync 再静默跳过通配符条目(`app/channel/internal/data/data.go:1982`),纯通配符渠道会产生零 registry 行且无任何告警。

---

## 三、处理结论

| 优先级 | 项 | 状态 |
|---|---|---|
| 立即 | C1(前端白屏)、H1(WS 池串号)、H2(故障转移失效)、H3/H4(告警永不触发) | ✅ 已修复,见 [review-v0.11.0-fixes.md](review-v0.11.0-fixes.md) |
| 本迭代 | M1-M6、L1-L7 | ⏳ 待排期 |
| 规划采纳 | sub2api 对比 #2(请求级排除集+预计算顺序)、#6(边缘桶归一)、#9(分桶成本持久化 — charge 切换前必须)、#12(负载感知接线) | 📋  roadmap |
