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

## 关联

- 告警规则与 SQL 查询口径：[docs/runbooks/cache-creation-charge-monitoring.md](../../docs/runbooks/cache-creation-charge-monitoring.md)
- 强制失败验证：[docs/runbooks/post-release-forced-failure-verification.md](../../docs/runbooks/post-release-forced-failure-verification.md)
