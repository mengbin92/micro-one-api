# 历史账本只读审计（v0.27）

该审计用于在 canonical 48 小时观测窗期间整理历史漂移证据。它只查询
`oneapi_billing.billing_ledgers` 与 `billing_pricing_snapshots`，不写数据库、不调用
billing RPC、不生成退款或冲正。历史行缺少当时的 usage/价格快照时必须标为
`candidate` 或 `unknown`，不能用当前价格补推。

## 运行

在有权限的 MySQL 客户端执行：

```bash
mysql --batch --raw < scripts/reconcile/historical_ledger_audit.sql \
  > historical-ledger-audit.tsv
```

将原始 TSV 转成确定性 CSV 或 JSON（字段顺序沿用 SQL，行顺序不变）：

```bash
python3 scripts/reconcile/format_historical_ledger_audit.py --format csv \
  < historical-ledger-audit.tsv > historical-ledger-audit.csv
python3 scripts/reconcile/format_historical_ledger_audit.py --format json \
  < historical-ledger-audit.tsv > historical-ledger-audit.json
```

生产主机上可将密码限制在 MySQL 容器内展开：

```bash
docker exec -i mysql sh -lc \
  'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --raw' \
  < scripts/reconcile/historical_ledger_audit.sql \
  > historical-ledger-audit.tsv
```

需要限定窗口时，先在 SQL 文件开头把 `@audit_start` / `@audit_end` 设置为 UTC
`DATETIME(3)`；结束时间为开区间。导出后按 `ledger_id`、`created_at` 排序的顺序保持
不变，便于重复运行后做 diff。JSON/CSV 转换应在受控审计环境完成，并保留原始 TSV
作为不可变输入；不要把用户、token 或渠道凭证加入导出。

## 分类与人工复核

| 分类 | 含义 | 后续动作 |
|---|---|---|
| `verified` | v1 verified usage、有效来源、存在 immutable pricing snapshot | 可进入供应商证据抽样；仍需外部账单/usage 才能下毛利结论 |
| `candidate` | v1 usage 可解释，但缺少来源、定价快照或仍为 estimated | 补证据或保持 observe；不得自动改账 |
| `unknown` | legacy、ambiguous 或结构不完整 | 仅记录并归档；不得按现价重算 |

重点字段包括来源 (`source_kind`/`upstream_model_id`)、usage contract 与 parse 状态、五个
canonical 成本桶、`upstream_cost`、`cost_audit_status`、pricing hash、重建差额
`candidate_delta_quota` 和 `evidence_source`。差额是候选证据，不是授权冲正金额。

只有在 `verified` 行完成供应商原始 usage/账单抽样、人工批准并生成单独的 reversal
草案后，才可讨论后续账务动作；本脚本本身永远不执行该动作。
