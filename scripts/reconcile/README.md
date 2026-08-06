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

告警上线前先在观察模式跑 1 个结算周期核对阈值（roadmap §4），避免误报。

## 关联

- 告警规则与 SQL 查询口径：[docs/runbooks/cache-creation-charge-monitoring.md](../../docs/runbooks/cache-creation-charge-monitoring.md)
- 强制失败验证：[docs/runbooks/post-release-forced-failure-verification.md](../../docs/runbooks/post-release-forced-failure-verification.md)
