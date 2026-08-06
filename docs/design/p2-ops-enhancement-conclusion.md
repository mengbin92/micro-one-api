# P2 运营增强结论(v0.16)

> 完成日期:2026-08-06
> 基线:`develop@15cfea3`(P1 完成)
> 设计依据:`docs/design/p2-ops-enhancement-plan.md`

## 摘要

P2 三项运营增强的最终结论:

| 任务 | 状态 | 结论 |
|------|------|------|
| P2.3 Routing Ops 去 Prometheus 依赖 | ✅ **已实施** | 双源降级,Prometheus 故障时 relay-gateway 直连 scrape |
| P2.1 账号级会话窗口(Claude Pro 5h) | ⏸️ 暂缓 | 前置条件不满足(无 Claude 多账号池) |
| P2.2 负载感知排队 | ⏸️ 暂缓 | 前置条件不满足(账号池未饱和) |

---

## P2.3 — Routing Ops 去 Prometheus 依赖:✅ 完成

### 问题(§9.1 风险 2)

`admin-api` 的 `/api/admin/routing-ops` 通过 `PROMETHEUS_URL` 查询外部 Prometheus
获取 relay-gateway 的路由指标(error/fallback rate)。Prometheus 未配置或查询失败时
返回 `partial=true`,错误率和回退率不可用。

### 方案:双数据源 + 优雅降级

```
admin-api /api/admin/routing-ops
  ├── 1. Prometheus (首选,PromQL increase() 精确窗口增量)  ← PROMETHEUS_URL
  ├── 2. relay-gateway /metrics 直连 (降级,累计计数器)       ← RELAY_METRICS_ENDPOINT
  └── 3. partial=true (两者均不可用)
```

**首选 Prometheus**:`increase(metric[window])` 精确计算时间窗口内增量,处理 counter
reset,是与 billing 聚合同窗口的正确路径。

**降级 scrape relay-gateway**:当 Prometheus 不可用时,admin-api 直接 HTTP GET
relay-gateway 的 `/metrics`,用 `expfmt` 解析 exposition format,读取 counter 当前累计值。
累计值 ≠ 窗口增量,但在 Prometheus 故障期间提供了路由健康度的基线可观测性。

**响应标注**:`routingOpsRates` JSON 新增 `source`(`"prometheus"` / `"relay_scrape"`)和
`cumulative`(bool)字段,前端据此区分"窗口增量"与"累计值"展示。

### 实现确认

| 层 | 文件 | 实现 |
|----|------|------|
| platform/metrics | `routing_rates.go` | `RoutingRates` 新增 `Source` 字段;`QueryRoutingRates` 设置 `Source="prometheus"` |
| platform/metrics | `routing_rates_scrape.go`(新) | `ScrapeRoutingRates`:HTTP GET `/metrics`,`expfmt.NewDecoder` 解析,聚合 selection/fallback counter |
| admin server | `routing_ops.go` | handler 块改为调 `loadRoutingRates`,返回 `(rates, errors)` |
| admin server | `routing_ops_rates.go`(新) | `loadRoutingRates`:Tier1 Prometheus → Tier2 relay scrape,错误透传 |
| docker-compose | `docker-compose.yml` | admin-api 段新增 `RELAY_METRICS_ENDPOINT=http://relay-gateway:8080` |
| go.mod | `prometheus/common` | 从 indirect 提升为 direct(expfmt 依赖) |

### 回归测试

| 测试 | 覆盖点 |
|------|--------|
| `TestScrapeRoutingRates_AggregatesCounters` | exposition 多行 counter 按 result label 正确聚合 |
| `TestScrapeRoutingRates_EmptyMetrics` | 空 /metrics 返回零值,source="relay_scrape" |
| `TestScrapeRoutingRates_ServerError` | relay-gateway 返回非 200 时报错 |
| `TestScrapeRoutingRates_IgnoresUnrelatedMetrics` | 过滤无关 metric family |
| `TestQueryRoutingRates_SetsPrometheusSource` | Prometheus 查询设置 source 标签 |
| `TestLoadRoutingRates_PrometheusSucceeds` | 双源可用时 Prometheus 胜出 |
| `TestLoadRoutingRates_FallbackToRelayScrape` | Prometheus 故障 → scrape 降级成功,partial=false |
| `TestLoadRoutingRates_PrometheusNotConfigured` | 仅配 relay → 直接 scrape,无冗余错误 |
| `TestLoadRoutingRates_BothFail` | 双源均故障 → partial=true + 2 条错误 |
| `TestLoadRoutingRates_NeitherConfigured` | 均未配置 → partial=true + "not configured" |

### 质量门禁

| 门禁 | 状态 |
|------|------|
| `make test-unit` | ✅ 全绿 |
| `./scripts/check-architecture.sh` | ✅ exit 0 |
| `make api-check`(`buf generate`) | ✅ 无 diff |
| `go vet ./app/admin/... ./platform/metrics/...` | ✅ 无问题 |
| `gofmt -l` | ✅ 全部格式化 |

---

## P2.1 — 账号级会话窗口(Claude Pro 5h 滚动窗):⏸️ 暂缓

### 结论:暂不实施,条件触发

**原因**:P1.3 结论确认当前生产流量为单账号(zhipu/kimi)订阅,无 Claude Pro 多账号池。
5h 滚动窗追踪在单账号场景下无调度收益(只有一个可选账号)。被动 runtime blocker
冷却(429/529 → 独立冷却时长)已足够处理上游限流。

**已有互补组件**:
- Codex `quota_snapshot`:已解析 primary/secondary window(覆盖 Codex 账号)
- 下游 `subscription_session_window`:用户会话级 spend ceiling
- 账号本地额度:`total/daily/weekly USD limit`(#4 已实现)

**触发条件**:
1. 配置 Claude Pro 多账号订阅池(同平台多账号)
2. 确认 Claude API 可解析窗口边界(从 usage / 429 error 解析)

届时按 `p2-ops-enhancement-plan.md` §P2.1 设计草案实施,预计 3-4 人日。

---

## P2.2 — 负载感知排队(Wait Plan):⏸️ 暂缓

### 结论:暂不实施,条件触发

**原因**:当前生产 97% 流量为单账号订阅(P1.3 结论),并发上限 18(内存+Redis 双版)
从未打满(`RelayAccountConcurrencyFallbackTotal` 无 fire)。选不到账号时直接 failover
到其他来源或返回 502 的策略在当前规模下完全适用。

**已有互补组件**:
- `AccountConcurrencyLimiter`:内存 + Redis 双版,请求前占槽,流式期间持有
- `SelectSubscriptionAccount`:满额账号视为"健康但忙" → failover 且不冷却
- `RuntimeBlocker`:429/529/5xx 分级冷却

**触发条件**:
1. 账号池并发频繁打满(`RelayAccountConcurrencyFallbackTotal` 持续增长)
2. 多实例部署(排队需跨 replica Redis 公平队列协调)

届时需先解决跨实例排队协调(Redis fair queue),预计 5-7 人日。

---

## v0.16 P2 完成定义

- [x] Routing Ops 去 Prometheus 依赖已实施并有回归测试(P2.3)
- [x] P2.1/P2.2 以前置条件不满足为由暂缓,触发条件已落档
- [x] 设计文档与结论文档落档
- [ ] 发布说明(v0.16 release note,待 P0 闭环后撰写)
