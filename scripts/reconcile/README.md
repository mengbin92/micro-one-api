# 对账周期自动化（v0.17 roadmap P1.2）

把 observe 阶段手工核对脚本化为一条命令可重复执行的周期对账：

```bash
./scripts/reconcile/reconcile.sh                 # 触发 billing 全量对账 + DB 侧检查（默认最近 24h）
./scripts/reconcile/reconcile.sh --since 7d --vendor-bill vendor_bill.csv
./scripts/reconcile/reconcile.sh --skip-trigger  # 只跑 DB 侧检查
```

退出码：`0` 无差异；`1` 有差异；`2` 配置/运行错误。无差异时输出
`RESULT: PASS (no discrepancies)`，可直接接入 cron / CI。

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
