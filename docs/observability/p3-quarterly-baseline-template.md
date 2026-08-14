# P3-0 季度观察基线模板

> 用途：为 v0.21 P2「P3-0 指标季度观察基线」提供可复用采集与报告格式。  
> 原则：本模板只观察，不触发实现；任何 P3 议题立项仍必须满足 v0.21 roadmap §5 的触发信号与 ADR 要求。

## 1. 报告头

| 字段 | 值 |
|---|---|
| 报告周期 | `YYYY-MM-DD ~ YYYY-MM-DD` |
| 报告人 |  |
| 数据源 | Prometheus `/api/v1/query_range` + production DB 只读查询 |
| 指标分辨率 | PromQL range step 1h；DB 快照取周期首末 |
| 代码基线 | `git rev-parse --short HEAD` |
| Prometheus 基线 | 当前生产镜像版本 |
| 备注 | 外部事件、扩缩容、上游故障、配置变更 |

复制本文件时，将文件命名为：

```text
docs/observability/p3-baseline-<YYYY>Q<Q>.md
```

## 2. 必采指标

所有 PromQL 均假设默认 5m rate 窗口；季度汇总时以 1h step 拉 range，再按周/月聚合。

### 2.1 入口请求量

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway"}[5m]))
```

报告字段：

- 周平均 RPS；
- 月平均 RPS；
- 周峰值 RPS（max over time）；
- 环比增长；
- 前 5 path。

### 2.2 入口延迟

```promql
histogram_quantile(0.50,
  sum by (le, method, path) (
    rate(micro_one_api_http_request_duration_seconds_bucket{service="relay-gateway"}[5m])
  )
)
```

```promql
histogram_quantile(0.95,
  sum by (le, method, path) (
    rate(micro_one_api_http_request_duration_seconds_bucket{service="relay-gateway"}[5m])
  )
)
```

```promql
histogram_quantile(0.99,
  sum by (le, method, path) (
    rate(micro_one_api_http_request_duration_seconds_bucket{service="relay-gateway"}[5m])
  )
)
```

报告字段：

- 全局 P50 / P95 / P99；
- Top 10 慢 path 的 P95；
- P95 超过 10s 的 path 列表；
- 延迟环比变化。

### 2.3 状态码与 P3 触发信号

总请求量：

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway"}[5m]))
```

429：

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway",status="429"}[5m]))
```

502：

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway",status="502"}[5m]))
```

429 占比：

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway",status="429"}[5m]))
/
sum(rate(micro_one_api_http_requests_total{service="relay-gateway"}[5m]))
```

502 占比：

```promql
sum(rate(micro_one_api_http_requests_total{service="relay-gateway",status="502"}[5m]))
/
sum(rate(micro_one_api_http_requests_total{service="relay-gateway"}[5m]))
```

报告字段：

| 指标 | 周均值 | 月均值 | 周峰值 | 触发阈值 | 是否接近 |
|---|---|---|---|---|---|
| 429 ratio |  |  |  | 见 §5 |  |
| 502 ratio |  |  |  | 见 §5 |  |

同时记录连续超过阈值的最长持续时长。

### 2.4 熔断与账号池压力（排队准入佐证）

熔断打开：

```promql
max by (service) (micro_one_api_resilience_circuit_breaker_state == 2)
```

熔断打开累计次数：

```promql
sum(rate(micro_one_api_resilience_circuit_breaker_trips_total[5m]))
```

报告字段：

- 打开次数；
- 最长持续时间；
- 受影响 service / dependency；
- 与 429 / 502 / 延迟峰值的时间相关性。

## 3. Dedupe claim 观察（SQL）

以下 SQL 在 production DB 只读执行。`T0` / `T1` 分别为季度首末快照时间。

### 3.1 行数与增长率

```sql
SELECT COUNT(*) AS claims
FROM billing_ledger_dedupe_claims;
```

增长：

```text
growth = (T1_count - T0_count) / T0_count
```

报告字段：

- T0 / T1 行数；
- 净增长；
- 环比增长率；
- 估算年度增长。

### 3.2 claim ↔ ledger 双向覆盖率

Ledger 有 key 但没有 claim（孤儿 ledger）：

```sql
SELECT COUNT(*) AS ledger_without_claim
FROM billing_ledgers l
LEFT JOIN billing_ledger_dedupe_claims c
  ON l.ledger_dedupe_key = c.ledger_dedupe_key
WHERE l.ledger_dedupe_key <> ''
  AND c.ledger_dedupe_key IS NULL;
```

Claim 有 key 但没有 ledger（孤儿 claim / 回滚残留）：

```sql
SELECT COUNT(*) AS claim_without_ledger
FROM billing_ledger_dedupe_claims c
LEFT JOIN billing_ledgers l
  ON l.ledger_dedupe_key = c.ledger_dedupe_key
WHERE l.ledger_dedupe_key IS NULL;
```

预期：两个计数都必须为 `0`。非零即为资金安全异常，应先执行
`scripts/reconcile` 并按 v0.20 迁移窗口记录归因，不得把它当作普通观察值。

### 3.3 重复 key 分布

```sql
SELECT ledger_dedupe_key, COUNT(*) AS n
FROM billing_ledgers
WHERE ledger_dedupe_key <> ''
GROUP BY ledger_dedupe_key
HAVING COUNT(*) > 1
ORDER BY n DESC
LIMIT 20;
```

预期：0 行。若有历史归因过的例外，必须在报告中列出 key、数量与归因链接。

### 3.4 冲突率代理口径

当前应用层尚无 `ErrLedgerDedupeExists` Prometheus counter，不能直接以指标计算冲突率。季度报告使用以下代理口径，并明确标注 limitation：

```text
claim growth ratio = billing_ledger_dedupe_claims 增长数
                     / billing_ledgers 新增数
```

正常情况下比值应接近 1。该口径无法区分“并发重试被正确拒绝”与“业务侧重复请求”，只能作为异常漂移信号；不得据此直接触发 P3 立项。

若后续 billing-service 增加低基数 counter（建议 `micro_one_api_billing_ledger_dedupe_conflicts_total{result}`），下一季度报告应替换为：

```promql
sum(rate(micro_one_api_billing_ledger_dedupe_conflicts_total{result="exists"}[5m]))
/
sum(rate(micro_one_api_billing_ledger_dedupe_claims_created_total[5m]))
```

## 4. Grafana 快照要求

报告至少附以下截图或导出 JSON：

1. Relay Gateway Overview；
2. 请求 P95 latency；
3. 429 / 502 状态码趋势；
4. Billing overview 中 settlement lag / ledger write duration；
5. 熔断器状态与 trips。

截图命名：

```text
docs/observability/assets/<YYYY>Q<Q>-<slug>.png
```

## 5. P3 触发阈值参考

这些不是自动立项条件，只是“是否需要启动 ADR 评估”的观察提示。

| 议题 | 观察提示 |
|---|---|
| 负载感知排队 | 429 或 502 周均值持续 > 1%，且伴随熔断/账号池饱和 |
| 会话窗口统一 | 多账号池出现真实窗口冲突案例 |
| 表分区 | ledger / logs 任一表接近 1GB 或 1000 万行 |
| log-service 合并 | 成本 / 运维复杂度有量化对比 |
| grpc-gateway | 外部 REST facade 契约需求变化 |

任何一项满足观察提示，只创建 ADR / 调研 issue，不直接实施。

## 6. 结论模板

```text
### 季度结论

- 入口流量：[增长 /持平 /下降]，环比 X%。
- P95 延迟：[稳定 /恶化 /改善]，主要 path：...。
- 429 / 502：最高 X% / Y%，是否超过观察提示：...。
- Dedupe claim：行数 X → Y，增长 Z%；双向覆盖：PASS / FAIL。
- 熔断：X 次，最长 Y 分钟。
- P3 建议：[无 /启动某议题 ADR]，原因：...。
```

## 7. 数据质量检查

- [ ] Prometheus 查询时间范围覆盖完整季度；
- [ ] 指标无长时间断 scrape（记录最大 gap）；
- [ ] DB 快照取自只读副本或低峰期；
- [ ] claim ↔ ledger 双向检查均为 0；
- [ ] 所有异常峰值均附事件归因；
- [ ] 未将观察数据直接解释为实现收益。
