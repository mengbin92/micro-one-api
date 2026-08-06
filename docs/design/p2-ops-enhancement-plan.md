# P2 运营增强实施计划(v0.16)

> 制定日期:2026-08-06
> 基线:`develop@15cfea3`(P1 契约加固已完成)
> 依据:`.workbuddy/artifacts/next-roadmap.md` §P2、`docs/design/v0.11.0-roadmap.md` §9.1/§9.2、`docs/design/sub2api-borrowable-ideas.md` #5/#6/#7

## 概述

P2 含三项"按需推进"的运营增强,工程复杂度与依赖条件差异显著:

| 任务 | 来源 | 复杂度 | 前置条件 | 本期动作 |
|------|------|--------|----------|----------|
| P2.3 Routing Ops 去 Prometheus 依赖 | §9.1 风险 2 | 低 | 无 | **实施** |
| P2.1 账号级会话窗口 | #5 | 中 | 多账号 Claude 池 | **设计 + 评估** |
| P2.2 负载感知排队 | #6 | 高 | 账号池紧张 | **设计 + 评估** |

执行顺序:P2.3(确定可做)→ P2.1/P2.2(设计评估,按流量模式决定是否实施)。

---

## P2.3 — Routing Ops 去 Prometheus 依赖:实施

### 问题

`admin-api` 的 `/api/admin/routing-ops` 通过 `PROMETHEUS_URL` 查询外部 Prometheus
获取 relay-gateway 的路由指标(error/fallback rate)。Prometheus 未配置或查询失败时
返回 `partial=true`,错误率和回退率不可用(§9.1 风险 2)。

### 架构现状

```
relay-gateway ──(inc counter)──► 进程内 Prometheus Registry
       │                                │
       └── /metrics (exposition)        │
                                        ▼
admin-api ──(PromQL increase[])──► Prometheus ──(scrape /metrics)
       │
       └── /api/admin/routing-ops → partial=true when Prometheus down
```

- relay-gateway `/metrics` 端点已存在(`internal/server/routes.go:68`),
  暴露 `micro_one_api_routing_selection_total{source_kind,result,...}` 等 counter。
- admin-api 已有 `httpClient`(`&http.Client{Timeout: 10s}`)和 docker 网络可达性。
- `prometheus/common`(含 `expfmt`)已在依赖树(v0.66.1 indirect)。

### 方案:双数据源 + 优雅降级

```
admin-api /api/admin/routing-ops
  ├── 1. Prometheus (首选,精确窗口 increase)  ← PROMETHEUS_URL
  ├── 2. relay-gateway /metrics 直连 (降级)    ← RELAY_METRICS_ENDPOINT (新)
  └── 3. partial=true (两者均不可用)
```

**首选 Prometheus**:PromQL `increase(metric[window])` 能精确计算时间窗口内的增量,
处理 counter reset,是当前已验证的路径。

**降级 scrape relay-gateway**:当 Prometheus 不可用时,admin-api 直接 HTTP GET
relay-gateway 的 `/metrics`,用 `expfmt` 解析 exposition format,读取 counter 的
**当前累计值**。累计值 ≠ 窗口增量,但在 Prometheus 故障期间提供了路由健康度的
基线可观测性(运维至少能看到总错误数/总回退数是否在增长)。

**响应标注**:`routingOpsRates` 新增 `source` 字段(`"prometheus"` / `"relay_scrape"` /
空),前端据此区分"窗口增量"与"累计值"展示。

### 实施步骤

1. **`platform/metrics/routing_rates_scrape.go`**(新文件):
   - `ScrapeRoutingRates(ctx, client, baseURL) (RoutingRates, error)`
   - HTTP GET `baseURL/metrics`,用 `expfmt.NewDecoder` 解析
   - 聚合 `routing_selection_total`(按 result label)和 `routing_fallback_total`
   - 返回累计值(非窗口增量)

2. **`platform/metrics/routing_rates.go`**(修改):
   - `RoutingRates` 新增 `Source string` 字段,标注数据来源
   - `QueryRoutingRates` 设置 `Source = "prometheus"`

3. **`app/admin/internal/server/routing_ops.go`**(修改):
   - Prometheus 失败后,尝试 `RELAY_METRICS_ENDPOINT` scrape
   - 成功则填入累计值 + `source="relay_scrape"`,`partial` 保持 false
   - 两者均失败才 `partial=true`
   - `routingOpsRates` JSON 新增 `source` / `cumulative` 字段

4. **配置**:
   - `RELAY_METRICS_ENDPOINT` 环境变量(可选,默认空 = 不启用降级)
   - docker-compose.yml admin-api 段添加 `RELAY_METRICS_ENDPOINT=http://relay-gateway:8080`

5. **测试**:
   - `routing_rates_scrape_test.go`:用 httptest server 模拟 relay-gateway /metrics
   - routing_ops 集成测试:Prometheus 失败 → scrape 成功 → partial=false

### 不做的事

- **不引入定时 scrape + 环形缓冲**:在 admin-api 进程内维护历史 counter 快照来
  精确计算窗口增量,复杂度高、ROI 低。Prometheus 已是窗口增量的正确工具。
- **不改 relay-gateway**:relay-gateway 已暴露标准 `/metrics`,无需新增端点。
- **不新增 gRPC RPC**:HTTP scrape 足够,避免 proto/codegen 成本。

---

## P2.1 — 账号级会话窗口(Claude Pro 5h 滚动窗):设计评估

### 问题

Claude Pro 账号有 5h 滚动会话窗(session window),上游按窗口计费/限流。本项目
现有 Codex `quota_snapshot`(已解析 primary/secondary window)和下游用户 session
window(Redis spend ceiling),但缺**上游 Claude 账号**的会话窗追踪。

### 现状盘点

| 组件 | 覆盖范围 | 位置 |
|------|----------|------|
| Codex quota_snapshot | Codex 账号 primary/secondary window | `app/channel/internal/biz/`、`quota_alert.go` |
| 下游 session_window | 用户会话级 spend ceiling | `internal/server/subscription_session_window.go` |
| 账号本地额度 | total/daily/weekly USD limit | `subscription_accounts` 表 |

**缺失**:Claude 账号的 5h 滚动窗。Claude Pro 每个会话窗有用量上限,超限触发
上游限流(返回 429/529),当前只能在 runtime blocker 冷却后被动处理。

### 设计草案(待流量模式确认)

- **数据模型**:复用 `quota_snapshots` 表或新增 Claude 专用字段,记录:
  - `session_window_start` / `session_window_end`(5h 滚动)
  - `session_window_status`(active / reset / unknown)
  - `window_usage_tokens`(当前窗口已用量)
- **窗口追踪**:Claude API 响应不含显式窗口边界,需从 usage 累计 + 时间推断,
  或从上游 429 的 `retry-after` / error message 解析窗口重置时间。
- **调度集成**:`AccountPool.IsSchedulable` 增加窗口余量检查,接近上限的账号
  降权或排除(类似 Codex quota_snapshot 的阈值自动暂停)。
- **与 #4(账号级本地额度)的关系**:本地额度是 USD 维度,会话窗是 token/时间
  维度,两者互补而非替代。

### 前置条件(决定是否实施)

1. **多账号 Claude 池**:当前生产只有单账号 zhipu/kimi 订阅(P1.3 结论),
   无 Claude Pro 账号池,5h 窗口追踪无实际调度收益。
2. **上游窗口边界信号**:需确认 Claude API 是否提供可解析的窗口信息。
3. **流量规模**:单账号低流量下,被动冷却(runtime blocker)已足够。

### 结论:暂不实施,列入条件触发

触发条件:配置 Claude Pro 多账号订阅池 + 确认可解析窗口边界。
届时按上述设计草案实施,预计 3-4 人日。

---

## P2.2 — 负载感知排队(Wait Plan):设计评估

### 问题

账号池全部饱和(并发满、runtime blocked、额度耗尽)时,当前直接 failover 到
其他来源或返回 502。sub2api 的做法是排队削峰(wait plan),平滑处理突发流量
而非直接拒绝。

### 现状盘点

| 组件 | 状态 |
|------|------|
| `AccountConcurrencyLimiter` | ✅ 内存 + Redis 双版,请求前占槽,流式期间持有 |
| `SelectSubscriptionAccount` | ✅ 满额账号视为"健康但忙" → failover 且不冷却 |
| `RuntimeBlocker` | ✅ 429/529/5xx 分级冷却 |
| 排队/Wait Plan | ❌ 无 |

### 设计草案(待账号池规模确认)

- **排队语义**:当所有候选账号并发满时,不立即返回 502,而是放入一个
  有界等待队列,超时后仍无可用账号才返回 502/429。
- **实现**:per-account `chan struct{}`(buffered = concurrency limit),
  `select { case <-slot: acquire; case <-time.After(waitTimeout): reject }`。
- **超时传播**:返回 429 + `Retry-After`(基于预估等待时间)。
- **背压**:队列长度有上限(防 OOM),超限直接拒绝。

### 前置条件(决定是否实施)

1. **账号池紧张**:当前生产 97% 流量为单账号订阅(P1.3 结论),
   并发上限 18 从未打满(`RelayAccountConcurrencyFallbackTotal` 无 fire)。
   无排队需求。
2. **多实例协调**:排队需跨 replica 协调(Redis 队列或共享信号量),
  单进程排队在多实例下语义不正确。
3. **成本**:排队引入请求保持、超时管理、背压控制等复杂度,ROI 取决于
   账号池是否真的成为瓶颈。

### 结论:暂不实施,列入条件触发

触发条件:账号池并发频繁打满(`RelayAccountConcurrencyFallbackTotal`
持续增长)+ 多实例部署。
届时需先解决跨实例排队协调(Redis fair queue),预计 5-7 人日。

---

## 质量门禁

```bash
make test-unit
./scripts/check-architecture.sh
make api-check
make test-sqlite && make migrate-status
cd web && npm run lint && npm test && npm run build
```

## P2.3 完成定义

- [ ] `ScrapeRoutingRates` 实现且有单元测试
- [ ] routing-ops Prometheus 失败 → scrape 成功 → `partial=false`
- [ ] docker-compose.yml 配置 `RELAY_METRICS_ENDPOINT`
- [ ] 质量门禁全绿
- [ ] 设计文档落档(本文档)
