# Micro-One-API v0.12.0 发布：v0.11.0 生产加固 + 路由可靠性 + 可观测性监控栈

> 2026-07-30 · 上一版：[v0.11.0](./release-v0.11.0.md)（2026-07-28）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.12.0)

v0.12.0 是 v0.11.0 发版后的**生产加固与功能补全版本**。它落地了 v0.11.0 代码评审报告（[review-v0.11.0.md](./review-v0.11.0.md) / [review-v0.11.0-fixes.md](./review-v0.11.0-fixes.md)）中的全部 CRITICAL/HIGH/MEDIUM/LOW 缺陷，采纳了与同级项目 `sub2api` 对比后选定的四项更优实现，并新增了 Prometheus + Grafana 可观测性监控栈。

本版**包含数据库迁移**（`070`–`071`，均为 additive、幂等可重入），新增若干 proto 字段（全部 additive），**无端点增删、无 API 破坏性变更**。

## 背景：v0.11.0 上线后暴露的问题

v0.11.0 的 17 个提交带来了 cache_creation 全链路计费、模型数据治理和路由运营闭环，但发版代码评审（5 个并行代理 + sub2api 同类实现对比）发现 1 个 CRITICAL、4 个 HIGH、6 个 MEDIUM、7 个 LOW 缺陷，以及 4 项 sub2api 更优实现待采纳。此外，订阅账号管理在实际使用中暴露出三层独立的创建/显示 bug，成本计算在 amd64 架构上出现 FMA 漂移，渠道模型映射在创建/编辑时未自动同步注册表。

## 变更内容

### 1. v0.11.0 代码评审 — CRITICAL / HIGH 修复

**C1. RoutingOpsPage `sources` 空数组白屏崩溃**

- **根因**：`web/src/pages/admin/RoutingOpsPage.tsx` 直接解引用 `data.sources`，但后端 Go nil slice 序列化为 JSON `null`，静默 24h 无流量 / billing 聚合失败 / `svc==nil` 早退三种常见响应均触发 `TypeError` 白屏。
- **修复**：渲染前归一化 `const sources = data.sources ?? []`，对齐 sub2api `res.items || []` 模式。

**H1. WS 连接池跨命名空间串号 — 错误上游 + 错误凭证**

- **根因**：连接池以裸 `int64 channelID` 为键，`tryReuse` 不校验 wsURL/凭证。v0.11.0 让订阅账号投影（`Channel.ID` = 账号 ID，独立 ID 空间）走同一池 → 普通渠道 #5 与订阅账号 #5 共享池桶，账号 5 的请求复用渠道 5 的连接：流量打到错误上游、用错误凭证、账单记错来源。
- **修复**：池键改为命名空间字符串 `"channel:<id>" / "subscription:<id>"`（从 `RoutingSourceIdentity` 派生）；复用前校验连接指纹（`wsURL` + `Authorization` 头），凭证/URL 轮换后旧连接直接关闭驱逐。新增回归测试 `TestConnPoolIsolatesNamespaces`、`TestConnPoolRejectsRotatedCredential`。

**H2. 故障转移提前终止 — 健康低优先级渠道永远轮不到**

- **根因**：`SelectFallbackRoutingSource` 中 `SelectChannel` 若返回刚失败（已排除）的渠道，代码将其置 nil 后直接落入 "no fallback routing source"，而非继续走下一优先级层。单次失败不熔断 → 第二次失败时仍返回同一渠道 → 排除检查置 nil → WS 连接关闭，健康渠道从未被尝试。
- **修复**：`SelectChannelRequest` 新增 `repeated int64 excluded_channel_ids`（已 `buf generate`）；channel usecase 新增 `SelectChannelExcluding` 按候选逐个过滤已失败渠道（而非 `excludeFirstPriority` 整层跳过），任何优先级的健康渠道都可达。新增回归测试覆盖跨层落选与同层兄弟节点不受牵连。

**H3 / H4. 告警永不触发**

- **H3 UpstreamCostMissing**：`A - B` 按 `provider_family` 做向量匹配，某 family 全无 priced 记录时 LHS 序列被丢弃 → 告警沉默。修复：表达式改为 `(A - B) or A`，零填充回退为全部成功流量。
- **H4 CacheCreationShadowCostDrift**：shadow cost 只以当前活跃 mode 标签上报，稳态 observe 模式下 `{mode="charge"}` 为空向量 → 减法无结果。修复：表达式改为 `(observe - charge) or observe`。

### 2. v0.11.0 代码评审 — MEDIUM / LOW 修复（M1-M6 / L1-L7）

| # | 位置 | 问题 | 修复 |
|---|------|------|------|
| M1 | `http_chat_handler` / `anthropic_inbound` / `http_raw_handler` 重试闭包 | usage-log 用原始 plan 的 SourceKind/UpstreamModelID + 实际 fallback 渠道，计费用 A 的成本键记 B 的用量 | 改用 `applyChannelInputs(ch)` 对齐实际执行渠道 |
| M2 | `http_responses_handler` failover | `storeResponseRoute` 仍存原始 plan 的 `SubscriptionAccountID`，下一轮 `previous_response_id` 路由回已失败的账号 | failover 成功后按实际渠道写入路由记录 |
| M3 | `upstream_cost.go MigrateUpstreamCostKeys` | 订阅账号的 legacy 键被误改写为 `channel:<id>:…`，订阅流量成本静默归零 | 迁移保留订阅账号键命名空间 |
| M4 | `upstream_cost.go SetUpstreamCost` | 整条目覆盖只写 input/output price，管理员改价静默删除 `cache_read_price` 等列 | 改为部分更新（fieldmask），保留未传字段 |
| M5 | `models.go model_unpriced_routed` gauge | 只在人工访问 `/api/admin/models/unpriced` 时更新 → 告警依赖有人看页面 | 新增 `unpriced_metric.go` 定期刷新 gauge |
| M6 | `http.go` + `docker-compose.yml` | `/admin/routing-ops` 未注册 SPA fallback（刷新 404）；`PROMETHEUS_URL` 默认为空 → 指标永久 partial | 注册 SPA fallback；`.env.example` 新增 `PROMETHEUS_URL` 默认值 |
| L1 | `http_usage_log.go` | `cache_read_input_ratio` 分母漏 `CacheReadTokens`，Anthropic 缓存重流量下比率可到 100 | 补全分母 |
| L2 | `billing.go` | 上游成本 metric 只在 dual-track 路径上报，legacy 路径不上报 → 告警分子恒近零 | 两条路径均上报 |
| L3 | `upstream_cost.go` | 迁移 apply 用陈旧 legacy 价覆盖已配置的新 canonical 价 | 迁移不覆盖已配置的新价 |
| L4 | `model.go` | 孤儿映射虚增 ChannelCount → UnpricedRoutedModels 误报 | 迁移 `070` 清理孤儿映射 + 级联外键 |
| L5 | `model_exchange_preflight.go` | 同一文档内转移 alias 被误判冲突，需分两次导入 | 放宽同文档内转移校验 |
| L6 | `model.go escapeLike` | 无 `ESCAPE` 子句，SQLite 下含 `_`/`%` 的关键字搜索静默返回空 | 补 `ESCAPE` 子句 |
| L7 | `openai_ws_state_store.go` | 滚动升级期间旧 Redis sticky 绑定（裸整数）被解码为订阅账号 ID，可能错路由至 TTL 过期 | 加 namespace 前缀解码兼容 |

### 3. sub2api 对比采纳项（四项更优实现）

对照 `review-v0.11.0.md` §二中与 `sub2api` 同类实现对比后规划采纳的四项设计，已全部落地：

**Phase A — 边缘桶归一化（#6）**

- 新增 `internal/biz` 的 `CanonicalUsage`：请求级别的 provider-agnostic token bucket 视图，携带 `PromptExclusive` 语义，避免下游反复从 channel type 推导。
- 在 `internal/server/usage` 集中 provider-specific usage → canonical bucket 转换；修复 forwarder 非流式与 Responses→Chat 转换丢弃 `cache_creation` 桶的问题。

**Phase B — 分桶成本持久化（#9）**

- `billing_ledgers` 新增 6 个分桶成本列（`prompt_cost` / `completion_cost` / `cache_read_cost` / `cache_creation_5m_cost` / `cache_creation_1h_cost` / `shadow_cost`），迁移 `071`（MySQL / postgres `010` / sqlite `009`）。
- `calculateCanonicalCost` 重构为返回 `CostBreakdown`；两条 commit 路径（legacy 与 dual-track）均持久化分桶成本和 shadow cost，`sum(bucket) == canonical`。
- proto `CommitQuotaRequest` 新增可选 `prompt_cost`–`shadow_cost`（字段号 19–24），未传时 billing-service 自行计算。
- **意义**：observe 模式的"与供应商账单比对再切 charge"闭环可从持久化数据重建，不再依赖日志和 histogram。

**Phase C — 请求级排除集 + 预计算候选顺序（#2）**

- `RelayPlan` 携带 `RoutingCandidateList`，每请求只构建一次；`RetryExecutor` 按预排序列表推进，跨命名空间排除集统一为 `RoutingSourceIdentity`。
- proto `SelectSubscriptionAccountRequest` 新增 `repeated int64 excluded_subscription_account_ids`；channel-service 新增 `SelectSubscriptionAccountExcluding` 按账号 ID 逐候选过滤。
- **重要语义**：HTTP 通用重试执行器（`ExecuteWithCandidates`）**不跨命名空间 failover**——API-key 渠道失败后只切其它 API-key 渠道，订阅账号失败后只切其它订阅账号。跨命名空间切换只属于凭证解析路径（WS/Responses transport）。新增 HTTP 重试路径必须遵守同一约束。

**Phase D — 负载感知选择接线（#12）**

- `SubscriptionAccountSelector` 注入 `LoadOracle`（Redis ZSet `subscription_account:concurrency:<id>` 的 `ZCOUNT now +inf`，只计未过期租约），每次 `Select` 在锁外 pipeline 批量预取跨副本 in-flight 快照（单 RTT）。
- `loadFactor` 按相对 `inflight/maxConcurrent` 分档降权（100/80/50/20/1）；channel 侧 `WeightedSelector` 新增同构 `loadFactor`。
- 修复 `crossReplicaInflight` 只在 `n>0` 时写入导致的 stale-high 永久降权（改为无条件 `Store(n)`）；修复 `ZCARD` 计入已过期未清理租约（统一改 `ZCOUNT now +inf`）。

### 4. Prometheus + Grafana 可观测性监控栈

- **Prometheus v3.6.0**：抓取全部 9 个后端服务的 `/metrics`（15s 间隔，后端网络内），加载 23 条告警规则，TSDB 保留 15d，`:9090`（仅内网，无 host port 映射）。
- **Grafana 12.4.3**：自动 provision Prometheus datasource + 3 个 dashboard（Relay Gateway Overview、Billing Performance、Service Dependencies Health），`:3001`（唯一公网可观测入口，密码保护）。
- admin-api `PROMETHEUS_URL=http://prometheus:9090` 接线，routing-ops 页可查询 fallback/error rates。
- 修复既有的 PromQL 笔误（`cache_misses_total{5m}` 无效标签选择器 → `cache_misses_total[5m]` 区间向量；`sum by(cache)` 两边对齐基数）。
- 配置文件纳入 `deploy/`（`prometheus.yml`、`alerts/alerts.yml`、`grafana/provisioning/`）。

### 5. 其他修复

**订阅账号创建三层 bugfix**

- admin-api `writeServiceResponse` 吞掉 `success:false`（protobuf `json:"success,omitempty"` 省略 false），客户端看到空对象误判成功 → 用显式 `apiResponse` envelope 保证 `success:false` 上线。
- `ListSubscriptionAccounts` / `ListChannels` 在 channel-service 报错时静默返回空列表 → 改为记录并传播 gRPC 错误。
- channel-service 异步模型探测用请求 ctx，Create 返回后 ctx 已取消 → 探测 goroutine 改用 `context.Background()`。

**计费确定性取整（amd64 FMA 漂移）**

- amd64 编译器把 `input*InputPrice + cacheRead*CacheReadPrice` 融合为单条 FMA 指令，`0.406` 变为 `0.40600000000000002753`，`math.Ceil` 上取整到 `4061`（arm64 无此问题，CI-only 失败）。重构为每个桶独立 `math.Round` 到整数 quota 单位后求和，跨架构 bit-for-bit 确定性。

**渠道模型治理**

- 渠道创建/更新时自动同步 model registry（`syncChannelModelMappingsTx`），否则经编辑新增的模型在注册表填充后对 `/v1/models` 不可见。
- 保留 managed model mappings（修复编辑渠道时丢失已管理映射）。
- `ListUnpricedRoutedModels` 用 `safecast.IntToInt32Saturating` 替代裸 `int32()` 强转，清除 gosec G115。

**前端与用户体验**

- 订阅账号使用记录耗时不显示（`http_adaptor.go` 流式/非流式路径未设 `ElapsedTime`）。
- 成本分析页渠道/订阅账号显示 Unknown 或 ID：新增 `FetchChannelSummariesByID` / `FetchSubscriptionAccountSummariesByID` 按 top-N 实际 ID 批量获取摘要，已删除记录显示"已删除渠道 #N"。
- cc-switch 导入参数修正（`build_claude_settings` 的 model 参数映射到 `ANTHROPIC_MODEL`）。
- API 使用指南页面重构：OS Shell 变体切换、Claude Code `settings.json` 配置方式、步骤时间线布局。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.12.0

# 开发者环境：重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：应用迁移 + 重建镜像 + 滚动重启
make migrate
docker compose build
docker compose up -d
```

**注意事项：**

- **数据库迁移**：`070`（孤儿映射清理 + `ON DELETE CASCADE` 外键）、`071`（billing_ledgers 6 个分桶成本列），均为 additive、幂等可重入，旧行默认 0。迁移归属已在 `migrations/ownership.yaml` 中补全（`070`→channel，`071`→billing）。
- **新增监控组件**：`docker compose up -d` 会启动 prometheus + grafana 两个新容器；首次启动 Grafana 在 `:3001`（默认账号密码见 `docker-compose.yml`）。如不需监控，可注释这两个 service。
- **`PROMETHEUS_URL` 配置**：生产环境若不使用 bundled prometheus，需在 `.env` 中指向自有 Prometheus 实例，否则 routing-ops 页指标为 partial。
- **前端**：构建 `cd web && npm run build`；如使用挂载卷部署，按 AGENTS.md 前端流程单独 scp `web/dist`（API 指南页与 RoutingOpsPage 有变更）。

## 兼容性说明

- **API**：无破坏性变更。proto 新增字段全部 additive（`billing.v1.CommitQuotaRequest` 字段 19–24、`channel.v1.SelectSubscriptionAccountRequest.excluded_subscription_account_ids`、`common.v1.LedgerEntry` 分桶成本字段）。旧客户端无感知。
- **数据库**：有迁移（`070`–`071`），新增列均为 `NOT NULL DEFAULT 0`、外键 `ON DELETE CASCADE`，additive 兼容滚动升级与回滚。
- **配置**：新增可选 `PROMETHEUS_URL`（默认指向 bundled prometheus）。
- **运行时**：HTTP 重试执行器不再跨命名空间 failover（仅凭证解析路径 WS/Responses 可跨命名空间）。cache_creation 仍默认 `observe` 模式。

## 验证

发布前已确认：

- `go build` / `go vet` / `gofmt` 全部通过
- `app/channel/internal/biz` 选择器与排除集测试全绿（`TestChannelUsecase_SelectChannelExcluding_*`、`TestSubscriptionAccountSelector_LoadFactor*`）
- `internal/biz` 重试候选与命名空间锁测试全绿（`TestRetryExecutor_ExecuteWithCandidates_NamespaceLock*`、`retry_candidates_test.go`）
- `internal/server` WS 连接池隔离测试全绿（`TestConnPoolIsolatesNamespaces`、`TestConnPoolRejectsRotatedCredential`）
- `app/billing/internal/biz` 分桶成本持久化测试全绿（`TestBillingUsecase_PerBucketCostsPersisted`）
- `internal/apicompat` cache_creation 边缘归一测试全绿
- `web` `tsc --noEmit` + `vite build` 通过
- `alerts.yml` YAML 校验通过（环境无 promtool，建议 CI 补 `promtool check rules`）

## 完整变更日志

- c6e61bf fix(channel): auto-sync model registry on channel create/update so /v1/models reflects channel edits
- 5ad82da fix(channel): preserve managed model mappings
- d445260 fix(billing): deterministic cost rounding across GOARCH (amd64 FMA drift)
- eac3d95 fix(channel): use safecast for int->int32 to clear gosec G115
- 686cc1b feat(deploy): add Prometheus + Grafana monitoring stack
- 4b8720e chore(deploy): keep Prometheus internal, expose only Grafana
- 62260ef fix: 订阅账号耗时显示、管理统计名称缺失、cc-switch导入与API指南页面重构
- 6c8b83a fix: subscription account create/edit/list three-layer bugfix
- e09867a fix(relay,channel): v0.11.0 review — WS 连接池命名空间隔离 + 故障转移排除集过滤
- e19a41f fix(admin,deploy): v0.11.0 review — RoutingOps 空数组白屏 + 告警 PromQL 零填充
- 6203936 fix(relay,admin,billing,channel,deploy): v0.11.0 review — 修复 M1-M6/L1-L7 剩余项
- 8008d8c feat(relay,channel,billing): v0.11.0 review — adopt sub2api #2/#6/#9/#12 + review fixes
- 801ac6d fix(deploy): add missing 070/071 to migration ownership manifest

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
