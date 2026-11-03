# cache-creation charge 后监控告警 Runbook

> 对应 `docs/design/v0.17-roadmap.md` §3 P1.1「charge 后监控告警」。
> 前提：生产已切换 `BILLING_CACHE_CREATION_MODE=charge`（v0.16 收尾）。
> 本文档负责：告警规则语义、文档化 SQL 查询、管理端成本视图验证。
> 周期对账脚本见 [scripts/reconcile/](../../scripts/reconcile/README.md)。

## 一、告警规则总览（deploy/prometheus/alerts/alerts.yml）

charge 模式下的三个运营场景由 `routing-cost-governance` 组内三条规则覆盖：

| 告警 | 触发条件 | 语义 |
|------|----------|------|
| `CacheCreationChargeUnpricedTraffic` | 30m 内 `mode=charge,unpriced=true` 影子成本持续累积且请求数 > 100 | cache_creation 5m/1h 桶未配价，token 被统计但**未实扣** |
| `CacheCreationChargeSignalLost` | 30m 内 `mode=charge` 无任何观测，但路由成功流量 > 100 | charge 计费信号消失（可能静默回退 observe 或埋点中断） |
| `NegativeGrossMargin` | 15m 盈利与亏损计数器速率之差 < 0，或负差额请求超过一半；持续 15m | 用户扣量低于配置采购估价（含 cache-creation 桶价差），不是供应商账单已确认亏损 |
| `UnpricedRoutedMetricUnavailable` | admin-api 采集正常，但求值失败、缺少成功信号或最后成功已超过 3m；持续 5m | 未定价模型监控不可用，保留的计数不能代表当前状态 |

另保留 observe 时代的 `CacheCreationShadowCostDrift`（observe 对账 / charge 回滚观察期使用），
charge 稳态下该规则不会触发，无需处理。

新指标承载：

- `micro_one_api_billing_ledger_gross_profit_quota{provider_family}` — 每次成功落账记录
  `实扣 quota - 配置采购估价(quota)`（1 USD = 10000 quota）。整数 quota 的 `le=-1`
  桶精确计数负差额请求；告警不再根据直方图 P50 插值的正负判定。
- `micro_one_api_billing_ledger_gross_profit_positive_quota_total` 与
  `micro_one_api_billing_ledger_gross_profit_negative_quota_total` — 分别累加正差额与负差额的绝对值；
  总毛利速率使用两者 `rate()` 之差。带负值的直方图 `_sum` 会下降，不能用于
  `rate()` / `increase()`，否则负差额会被当成计数器重置。
- `micro_one_api_model_unpriced_routed_evaluation_success` 与
  `micro_one_api_model_unpriced_routed_last_success_timestamp_seconds` — 后台最近一次求值是否成功、
  最近一次成功时间。配置读取或 RPC 失败不覆盖已有未定价计数，也不刷新成功时间。
- 既有 `micro_one_api_relay_token_usage_shadow_cost{mode,unpriced}` 直接承载 unpriced 信号，
  无需新埋点。

> **口径说明（重要）**：`gross_profit_quota` 只在**计费成功并写 ledger 的 commit 路径**
> （`commitQuotaLegacy` 写账本后 / `commitQuotaDualTrack` 事务提交后）埋点，一次请求只记录一次，
> 订阅与钱包拆账不重复计数。结算失败、预约释放
> （release）、或上游成本缺失导致提前返回的请求**不会**进入该直方图——`NegativeGrossMargin`
> 告警只能反映"被成功结算的请求"的毛利，看不到结算失败造成的毛利缺口。排查负毛利时，
> 请同时关联 `micro_one_api_billing_ledger_upstream_cost_recorded{result="unpriced"}` 与
> `RunReconciliation` 输出，区分"定价低于上游"与"未定价/结算失败"两类根因。

后台计数使用仅限专用 admin 服务身份的 `CountUnpricedRoutedModels`，只返回渠道与订阅两种
来源的计数。人工审计仍使用 `ListUnpricedRoutedModels` 并校验用户的模型、价格与映射读取权限。
`ModelPrice` 缺失、读取失败、非法 JSON 或 `null` 均表示本次监控失败；显式空对象 `{}`
表示没有配置任何模型价格，显式零价格条目仍属于已定价模型。

## 二、文档化 SQL 查询（Prometheus 无法直查 DB 的场景）

以下查询针对 `billing_ledgers`（MySQL 语法，`type='consume'` 为实扣账本行）。
阈值建议先观察 1 个结算周期再固化为告警/对账门槛。

### 2.1 未定价桶持续产生流量（DB 侧口径）

```sql
SELECT COUNT(*)                                  AS counted_unbilled_rows,
       COALESCE(SUM(cache_creation_5m_tokens + cache_creation_1h_tokens), 0) AS counted_unbilled_tokens
FROM billing_ledgers
WHERE type = 'consume'
  AND created_at >= NOW() - INTERVAL 24 HOUR
  AND (cache_creation_5m_tokens + cache_creation_1h_tokens) > 0
  AND COALESCE(cache_creation_5m_cost, 0) + COALESCE(cache_creation_1h_cost, 0) = 0;
```

> 说明：未配价桶在 charge 模式下 canonical 收敛到 v0.10.2，`shadow_cost` 可能为 0，
> 因此用「有 cache-creation token 但两桶成本均为 0」作为「数了但不收费」的判据。

### 2.2 毛利异常（负毛利）

```sql
-- 全局 24h 毛利
SELECT SUM(ABS(amount) - upstream_cost) AS gross_profit_quota,
       COUNT(*)                          AS ledger_rows
FROM billing_ledgers
WHERE type = 'consume' AND created_at >= NOW() - INTERVAL 24 HOUR;

-- 按模型下钻，定位亏损来源（含 cache-creation 桶价差）
SELECT model_name,
       SUM(ABS(amount) - upstream_cost) AS gross_profit_quota,
       SUM(ABS(amount))                  AS revenue_quota,
       SUM(upstream_cost)                AS upstream_quota
FROM billing_ledgers
WHERE type = 'consume' AND created_at >= NOW() - INTERVAL 24 HOUR
GROUP BY model_name
HAVING gross_profit_quota < 0
ORDER BY gross_profit_quota ASC
LIMIT 20;
```

### 2.3 观察成本 vs 实扣（影子成本偏离）

```sql
SELECT DATE(created_at)                                   AS d,
       SUM(shadow_cost)                                   AS shadow_quota,
       SUM(COALESCE(cache_creation_5m_cost, 0)
           + COALESCE(cache_creation_1h_cost, 0))        AS charged_quota,
       SUM(shadow_cost) - SUM(COALESCE(cache_creation_5m_cost, 0)
           + COALESCE(cache_creation_1h_cost, 0))        AS drift_quota
FROM billing_ledgers
WHERE type = 'consume' AND created_at >= NOW() - INTERVAL 30 DAY
GROUP BY DATE(created_at)
HAVING drift_quota <> 0
ORDER BY d DESC;
```

> charge 模式下全桶配价时 `shadow_quota == charged_quota`；差异行 = 该时段存在
> observe 遗留、未配价桶或部分定价，需回查 system_options 的 ModelPrice 配置。

### 2.4 token 桶 vs 供应商账单（周期汇总）

```sql
SELECT DATE(created_at)              AS d,
       SUM(prompt_tokens)            AS prompt_tokens,
       SUM(completion_tokens)        AS completion_tokens,
       SUM(cache_read_tokens)        AS cache_read_tokens,
       SUM(cache_creation_5m_tokens) AS cache_creation_5m_tokens,
       SUM(cache_creation_1h_tokens) AS cache_creation_1h_tokens,
       SUM(upstream_cost)            AS upstream_quota
FROM billing_ledgers
WHERE type = 'consume' AND created_at >= NOW() - INTERVAL 24 HOUR
GROUP BY DATE(created_at);
```

与供应商账单核对时：按日把五桶 token 与账单 token 对比、把 `upstream_quota`
换算成 USD（`÷10000`）与账单金额对比，偏差超过阈值（建议 5%）即差异。
该核对已脚本化：`scripts/reconcile/reconcile.sh --vendor-bill <csv>`。

### 2.5 缓存命中率口径

对账统一口径：`cache_read / (cache_read + cache_creation_5m + cache_creation_1h)`，
即「命中的缓存读取 token 占可缓存流量（读 + 新建缓存）的比例」。

```sql
SELECT ROUND(100.0 * SUM(cache_read_tokens)
       / NULLIF(SUM(cache_read_tokens + cache_creation_5m_tokens
                + cache_creation_1h_tokens), 0), 2) AS cache_hit_rate_pct
FROM billing_ledgers
WHERE type = 'consume' AND created_at >= NOW() - INTERVAL 24 HOUR;
```

> 该口径只覆盖计费账本（provider 侧），与 relay L1 缓存命中率
> `micro_one_api_cache_hits_total` 是两个不同维度，勿混用。

## 三、管理端 routing-ops / 成本视图验证（charge 后展示正常）

1. **模式展示**：`GET /api/pricing` 返回 `cache_creation_mode: "charge"`。
   admin 进程的展示值读取自身环境变量，仅作运维提示；实扣以 billing-service
   的 `BILLING_CACHE_CREATION_MODE` 为准，两个进程需保持一致。
2. **routing-ops 视图**：`GET /api/admin/routing-ops`（24h 窗口）应满足：
   - `totals.cache_creation_5m_tokens` / `cache_creation_1h_tokens` > 0；
   - `totals.gross_profit` 可读且为正值（负值会在 `alerts` 出现 `negative_margin`）；
   - `alerts` 中无 `upstream_cost_missing`（有收入但上游成本为 0）；
   - `partial=false`（Prometheus 或 relay 直采可用）。
3. **Grafana**：billing dashboard 新增面板
   「Cache-Creation Shadow Cost (rate by mode)」「Unpriced cache-creation traffic (charge)」
   「Configured margin per request (estimated P50)」仅为桶内插值估算，接近零的数值不能用于判断是否亏损；
   负差额以 `le=-1` 桶或按请求合并的账本为准。charge 稳态下应持续出数，
   unpriced 面板归零之前先确认求值成功且时间新鲜。
4. **发布后强制失败验证**：见
   [post-release-forced-failure-verification.md](./post-release-forced-failure-verification.md)，
   确认回退原因、来源归属与单次计费三项结论。

## 四、告警上线前注意事项

- 先在观察模式验证阈值（roadmap §4）：`NegativeGrossMargin` 用 15m 窗口观察
  一个结算周期，确认无误报再保留 critical 级别。
- 回滚到 observe 时：`CacheCreationChargeUnpricedTraffic` / `CacheCreationChargeSignalLost`
  会自动停止触发，`CacheCreationShadowCostDrift` 恢复生效，无需改规则。
