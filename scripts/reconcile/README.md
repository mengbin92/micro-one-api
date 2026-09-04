# 对账周期自动化（v0.17 roadmap P1.2）

把 observe 阶段手工核对脚本化为一条命令可重复执行的周期对账：

```bash
./scripts/reconcile/reconcile.sh                 # 触发 billing 全量对账 + DB 侧检查（默认最近 24h）
./scripts/reconcile/reconcile.sh --since 7d --vendor-bill vendor_bill.csv
./scripts/reconcile/reconcile.sh --skip-trigger  # 只跑 DB 侧检查
```

退出码：`0` 无差异；`1` 有差异；`2` 配置/运行错误。无差异时输出
`RESULT: PASS (no discrepancies)`，可直接接入 cron / CI。

## Canonical usage 固定 48h 验收

v0.27 的 production observe 使用固定窗口，而不是运行时滚动的 `last 48h`：

- CST：`2026-09-02 11:12:00.225`（含）至 `2026-09-04 11:12:00.225`（不含）；
- MySQL UTC：`2026-09-02 03:12:00.225`（含）至 `2026-09-04 03:12:00.225`（不含）。

窗口满时执行只读脚本：

```bash
mysql --table < scripts/reconcile/canonical_observe_48h.sql
```

在生产 Docker Compose 主机上可让 MySQL 密码仅在容器内展开：

```bash
docker exec -i mysql sh -lc \
  'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --table' \
  < scripts/reconcile/canonical_observe_48h.sql
```

脚本输出以下固定门禁：窗口是否满 48h、v1 契约与语义、来源与定价快照、ledger/claim
幂等、持久化成本算术、自然流量 delta 解释，以及 billing/log 24 字段多重集一致性。
`step-explore` 是 v0.23 executor 的已留证受控测试 cohort：原始 mismatch 仍原样报告，
但只有排除该 cohort 后的自然流量 mismatch 参与 charge 判定。脚本不会输出用户、请求、
渠道、订阅、token 或金额明细。

同时在 Prometheus 使用相同固定时间范围执行以下查询；值为空按 0 处理：

```promql
sum(increase(micro_one_api_relay_token_usage_invariant_mismatch_total[48h]))
sum(increase(micro_one_api_relay_token_usage_parse_anomaly_total[48h]))
sum(increase(micro_one_api_billing_usage_ambiguous_total[48h]))
sum(increase(micro_one_api_channel_usage_semantic_source_isolation_total[48h]))
max(max_over_time(micro_one_api_billing_async_queue_size[48h]))
```

前四项必须为 0，异步队列当前值与窗口最大值也必须为 0。Prometheus 控制台的查询结束
时间固定为 `2026-09-04 11:12:00.225 CST`；不要用执行当天的滚动窗口替代。Histogram
`_sum` 含负数观察值，不能使用
`increase(micro_one_api_billing_usage_semantics_cost_delta_sum[48h])` 作为金额结论；差额以
SQL 对 ledger + pricing snapshot 的逐桶重建为准。

SQL 全部 `PASS` 且 Prometheus 门禁为 0 仍不自动授权切换 charge。自然生产 delta 必须有
原始供应商 usage、供应商账单或等价不可变外部证据抽样；缺少该证据时结论只能是
“observe 数据面通过，charge 暂缓”。

### 固定月费订阅的供应商证据口径

K3/Kimi 当前由运营确认为固定月费订阅，费用为 `199/月`（本记录不推断币种、是否为每个
套餐分别计费或是否存在超额费用，这些信息必须以套餐凭证为准）。固定月费本身不能与单笔
token、请求或 `upstream_cost` 一一对应，因此：

- 不把 `199` 除以请求数/token 数制造伪精确单请求成本；
- 不把月费写进按日、provider-family token 汇总的 `vendor_bill.csv`；
- canonical usage 的 subset/exclusive 语义由上游返回的 verified usage 字段、billing/log
  双写和冻结的用户售价快照验证；
- `billing_ledgers.cost_audit_status=priced`、非零 `upstream_cost` 只说明系统存在内部配置的
  成本模型，不代表该金额已被供应商逐笔开票；
- 供应商证据改为核对套餐主体、账期、固定月费、币种和是否有超额计费。若存在超额计费，
  超额部分仍必须取得供应商 usage/账单后才能纳入毛利结论。

因此，固定月费账单的逐笔 token 对账记为“不适用”，不再单独阻塞 canonical 用户计费
语义灰度；但在套餐币种、计费范围和超额规则留证前，不得用 ledger 的内部
`upstream_cost` 宣称已完成真实供应商毛利对账。

## 历史漂移一次性修复

升级并完成 080-082 迁移后，使用
`repair_usage_accounting.sql` 修复历史 channel `used_quota` 与缺失的 consume
日志。脚本以 ledger 为权威来源，先按 reservation 去重，并默认以 `ROLLBACK`
结束；先检查预览结果，再把最后一行改成 `COMMIT` 执行。旧流水缺少当时的
上游模型/价格快照，迁移只将其标记为 `cost_audit_status=legacy`，不会用现价
伪造历史成本。

## 覆盖的检查

| 检查 | 来源 | 判定 |
|------|------|------|
| billing 全量对账 | `POST <billing>/v1/reconciliation` | 任一 inconsistency 数组非空即失败（account/channel/log/subscription/receivable/refund/stuck） |
| ledger dedupe key | `billing_ledgers` | `ledger_dedupe_key` 空或非 legacy 重复即失败；legacy 回填数仅提示 |
| cache-creation counted-but-unbilled | `billing_ledgers` | 窗口内「有 cache-creation token 但 5m/1h 成本均为 0」行数 > `--unpriced-max`（默认 0）即失败 |
| 毛利 | `billing_ledgers` | `SUM(ABS(amount) - upstream_cost) < 0` 即失败 |
| 缓存命中率口径 | `billing_ledgers` | `cache_read / (cache_read + creation_5m + creation_1h)`，低于 `--cache-hit-min`（默认关闭）告警 |
| token 桶 vs 供应商账单 | 账单 CSV vs `billing_ledgers` | 任一桶/金额相对偏差 > `--vendor-tolerance`（默认 5%）即失败 |

## 环境变量

| 变量 | 必填 | 说明 |
|------|------|------|
| `SERVICE_TOKEN` | 触发对账时必填 | billing-service `POST /v1/reconciliation` 的 Bearer token（`.env` 已含） |
| `BILLING_RECON_ENDPOINT` | 否 | 默认 `http://127.0.0.1:8004/v1/reconciliation`，生产可指向服务地址 |
| `RECONCILE_DSN` / `DATABASE_DSN` | DB 检查时必填 | 计费库 MySQL DSN（`.env` 已含 `DATABASE_DSN`） |

脚本自动从仓库根 `.env` 读取 `SERVICE_TOKEN` / `DATABASE_DSN` /
`BILLING_RECON_ENDPOINT`（已设置的环境变量优先）。

## 供应商账单 CSV 格式

```csv
provider_family,date,cache_creation_5m_tokens,cache_creation_1h_tokens,cache_read_tokens,prompt_tokens,completion_tokens,upstream_quota
openai,2026-08-06,100000,50000,300000,1200000,400000,250000
anthropic,2026-08-06,80000,20000,250000,900000,300000,180000
```

- `provider_family`：`openai` / `anthropic` / `google` / `zhipu` / `deepseek` /
  `alibaba` / `other`（与指标 label 一致）。
- `date`：`YYYY-MM-DD`，与账本 `created_at` 当日对比。
- `upstream_quota`：供应商账单金额换算成内部 quota（1 USD = 10000 quota）。
- 模板见 `vendor_bill.example.csv`。

## 阈值调整

```bash
./scripts/reconcile/reconcile.sh --skip-trigger \
  --since 7d \
  --vendor-bill vendor_bill.csv \
  --vendor-tolerance 0.05 \
  --unpriced-max 0 \
  --cache-hit-min 0.5
```

## 阈值校准（首次上线必读）

当前默认阈值是**首次假设值**，未经生产数据验证：

| 参数 | 默认值 | 含义 |
|------|--------|------|
| `--vendor-tolerance` | 0.05 | 供应商账单对比相对偏差容限（5%） |
| `--unpriced-max` | 0 | 窗口内「已统计未计费」cache-creation 账本行数上限 |
| `--cache-hit-min` | 0（关闭） | 缓存命中率下限，低于则告警 |
| 告警 `for` / 请求数阈值 | 30m / >100 | `alerts.yml` 中 unpriced/信号丢失规则 |

**校准流程**：生产 charge 切换后的**第 1 个完整结算周期**内，以观察模式运行
（`--skip-trigger`，先不接 cron/CI），记录每次输出的真实差异分布：

1. `--vendor-tolerance` 5% 频繁误报 → 记录真实偏差中位数/分位数后上调，避免 token 桶口径边界（时区、舍入）造成噪声；
2. `--unpriced-max 0` 触发但确属已配价桶的边界行 → 分析是否为遗留未计费行，按真实值调整；
3. 告警规则（`>100`、`for: 30m` 等）同样按首周触发情况校准。

校准结论回写本文件「阈值调整」段落与
[runbook](../../docs/runbooks/cache-creation-charge-monitoring.md) 的告警规则表，作为生产基线。
未完成校准前，告警以观察模式（或暂不接入告警通道）运行，避免误报疲劳。

### 首周期校准记录（2026-08-10，生产 `oneapi_billing` schema，v0.18 P1 C6）

执行：触发 billing 全量对账（`POST /v1/reconciliation`）+ DB 侧检查（`checks.go`，
窗口 24h / 7d）。生产为观察口径（`BILLING_CACHE_CREATION_MODE` 未启用 charge），当前有真实流量
（24h 内 1537 行 consume ledger）。

**触发对账结果：8 处不一致（全量维度）**

| 维度 | 数量 | 明细（归因见下） |
|------|------|-----------------|
| account | 2 | user 1 `expected=-20321930 actual=3000000`；user 3 `expected=-17152 actual=0` |
| channel | 4 | channel 2/3/4/5 `expected_used_quota>0 actual=0`（差 -320599 / -102908 / -1651601013 / -11820303） |
| receivable | 1 | `user_id="" pending=1421` |
| stuck issuance | 1 | `PAY183ad96f0b7561784772414` user 1, 2000 cents, since 2026-07-23 |

**DB 侧检查（真实数据）：全 PASS** — ledger dedupe key 空/重复均为 0；
cache-creation counted-but-unbilled = 0；**cache hit rate = 100%**（read=107,775,744 / cacheable 全命中）；
gross margin = **+1,917,426 quota**（24h）/ **+6,184,918 quota**（7d），正毛利；token 桶 zhipu/deepseek/other 正常。

**归因与判定**

- account / channel / receivable 7 处 = **历史口径遗留**：`channels.used_quota` 列从未随
  ledger 消费写入、`users.balance` 列与 ledger 净额分离（旧版账务结构）、应收行 `user_id` 为空。
  均为 v0.17 之前数据迁移遗留，非本轮引入。**判定：文档化接受为基线差异**，对账判定逻辑本身正确，
  不因历史遗留调整；下一结算周期复核这些维度是否随新写入自然收敛。
- stuck issuance 1 处 = **真实待修**：paid+issued+unfulfilled（M10 卡住态），按
  [reconciliation runbook](../../docs/runbooks/cache-creation-charge-monitoring.md) /
  `reconciliation_job.go` 指引应重触发 `CompleteSubscriptionPurchase` 修复（涉及真实用户资金，
  由运营决策执行时机）。

**阈值决策（首周期基线）**

| 参数 | 结论 | 依据 |
|------|------|------|
| `--unpriced-max 0` | **保持** | 实况 0（observe 模式亦无未计费行） |
| `--cache-hit-min` | 建议开启 `0.5`（待定） | 实况 100%，远高于任何合理下限 |
| `--vendor-tolerance 0.05` | **保持默认** | 尚无供应商账单 CSV 可比对，待有账单后复校 |
| 告警 `for 30m / >100` | **保持** | 首周期无告警触发记录，无噪音证据 |

**后续**：对账建议按结算周期接入 cron（当前手工/按需）；`--cache-hit-min` 是否开启、
stuck 单修复时机由运营/用户决策。

### v0.20 迁移后首个结算周期对账（2026-08-14，v0.21 P0）

执行：迁移 `078`（`billing_ledger_dedupe_claims` 非分区全局幂等闸门）应用后首个完整结算
周期对账，生产 `oneapi_billing` 真实数据。完整记录见
[docs/design/v0.21-p0-reconciliation-record.md](../../docs/design/v0.21-p0-reconciliation-record.md)。

**结果摘要**：

| 检查 | 结论 |
|------|------|
| 重复 `ledger_dedupe_key`（含 legacy） | 0 ✅ |
| claim ↔ ledger 双向一致性 | 19 条迁移窗口孤儿 ledger（04:07:14–04:20:46 UTC，旧代码写入，无 claim）；claims without ledger = 0 |
| claim 冲突 → 409 映射 | ✅ 代码 + 单测链路（`ErrLedgerDedupeExists` → `ErrDuplicateRequest` → gRPC `AlreadyExists` → HTTP 409） |
| billing 全量对账 | account 3 / channel 5 / receivable 1（历史口径遗留，同 v0.18 基线）；stuck 0 |
| DB 侧检查（24h） | 空键 0、重复 0、unbilled 0、cache hit 100%、毛利 **+720,759**（7d **+7,362,712**） |
| Prometheus | dedupe/负毛利/信号丢失零噪音；`RoutedModelsUnpriced`(firing)、`UpstreamCostMissing`(pending) 为既有配置缺口 |

**阈值决策（v0.20 幂等语义下复核）**：

| 参数 | 结论 | 依据 |
|------|------|------|
| `--unpriced-max 0` | **保持** | 实况 0 |
| `--cache-hit-min` | **保持 0（关闭）** | 实况 100%，无噪音价值；开启与否由运营决定 |
| `--vendor-tolerance 0.05` | **保持** | 无供应商账单 CSV |
| 告警 `for 30m / >100` | **保持** | 结算窗口零噪音 |

**新增建议**：DB 侧检查已增加「claim 覆盖完整性」（`ledger 无 claim` + `claim 无 ledger`
双向校验）——✅ 2026-08-14 落地，正/负路径均验证；迁移窗口 19 条孤儿账本已补插 claim
（claims = ledgers = 26,879），当前双向一致性 0/0。

### 首周期 charge 告警观察（2026-08-10，v0.18 P1 C8）

生产实况：`BILLING_CACHE_CREATION_MODE=charge`（charge 已启用）。prometheus 规则
`alerts.yml` 实测（`/api/v1/alerts` + 指标查询）：

| 规则 | 状态 | 实测依据 |
|------|------|---------|
| `CacheCreationChargeUnpricedTraffic` | **无触发** | `shadow_cost{mode=charge,unpriced=true}` 30m rate = 0（无观测） |
| `CacheCreationChargeSignalLost` | **无触发** | charge 观测为 0，但路由成功 rate ≤ 100（未达 `>100` 门槛）；计费信号未消失 |
| `NegativeGrossMargin` | **无触发** | `gross_profit_quota_sum[15m]` rate = **+0.53 quota/s**（正）；DB 侧毛利 24h +1.9M quota |

**结论：三条目标规则无噪音 ✅；毛利数据已开始沉淀（正值）。**

**但发现 2 条关联告警 firing（配置缺口，非规则噪音，需运营处理）**：

1. `RoutedModelsUnpriced`（since 2026-08-09）：**2 个 public/enabled/routed 模型无 ModelPrice**，
   charge 模式下以零/猜测成本服务——正是 `CacheCreationChargeUnpricedTraffic` 的种子，一旦这些
   模型产生 cache-creation 流量即触发规则 1。**处置：为模型配 ModelPrice。**
2. `UpstreamCostMissing`（since 2026-08-10，`provider_family=deepseek`）：deepseek 成功请求未记录
   上游成本（`UpstreamModelPrice` 缺失）——毛利口径缺口，可能掩盖负毛利。**处置：配 deepseek 上游价。**

次要：`CacheHitRateLow`（auth L1 缓存 50%，pending，非目标规则）。

## 关联

- 告警规则与 SQL 查询口径：[docs/runbooks/cache-creation-charge-monitoring.md](../../docs/runbooks/cache-creation-charge-monitoring.md)
- 强制失败验证：[docs/runbooks/post-release-forced-failure-verification.md](../../docs/runbooks/post-release-forced-failure-verification.md)
