# v0.27 Canonical Usage 48 小时验收清单

这是一次性的生产验收记录模板。当前窗口尚未结束，所有门禁保持待验收状态；在
窗口结束前不得切换 `BILLING_CACHE_CREATION_MODE=charge` 或据此冲正历史账。

## 固定窗口

- 开始（CST）：`2026-09-04 09:49:52.108`（含）
- 结束（CST）：`2026-09-06 09:49:52.108`（不含）
- MySQL UTC：`2026-09-04 01:49:52.108`（含）至 `2026-09-06 01:49:52.108`（不含）
- controlled cohort：`upstream_model_id=step-explore`，只留证，不参与自然流量 charge 判定

## 执行顺序

窗口结束后由值班工程师按顺序执行，并将原始输出保存到受控审计目录：

```bash
mysql --table < scripts/reconcile/canonical_observe_48h.sql \
  | tee canonical-observe-20260904-20260906.txt
```

然后在 Prometheus 控制台把结束时间固定为 `2026-09-06 09:49:52.108 CST`，执行：

```promql
sum(increase(micro_one_api_relay_token_usage_invariant_mismatch_total[48h]))
sum(increase(micro_one_api_relay_token_usage_parse_anomaly_total[48h]))
sum(increase(micro_one_api_billing_usage_ambiguous_total[48h]))
sum(increase(micro_one_api_channel_usage_semantic_source_isolation_total[48h]))
max(max_over_time(micro_one_api_billing_async_queue_size[48h]))
```

## 验收门禁（逐项勾选）

- [ ] SQL `window_elapsed` = `PASS`
- [ ] SQL contract/semantics、source/pricing、idempotency、cost arithmetic、自然流量 delta、billing/log multiset 均 `PASS`
- [ ] Prometheus 前四项为 0；异步队列当前值和窗口最大值均为 0（空值按 0）
- [ ] 从自然流量抽取样本，留存供应商原始 usage 或等价不可变外部证据
- [ ] K3/Kimi 固定月费只核对套餐主体、账期、币种和超额规则；未把月费伪分摊为单请求成本
- [ ] 任何 mismatch 都已按 request/ledger 外部证据归因；`step-explore` 与已书面确认的 legacy fallback 单列
- [ ] 值班工程师、账务负责人和运营负责人完成复核并签名

## 决策

| 决策 | 条件 | 记录 |
|---|---|---|
| `observe 通过，charge 暂缓` | 数据门禁通过但没有供应商 usage/账单证据 | 默认安全结论 |
| `observe 通过，可提议 charge` | 所有数据门禁通过且外部证据抽样通过 | 仍需人工批准和变更窗口 |
| `FAIL，延长 observe` | 任一 SQL/Prometheus 门禁失败或队列不为 0 | 记录根因、修复和新窗口 |

任何人工批准都必须引用本清单、SQL 原始输出、Prometheus 截图/导出和外部证据索引；
本清单不授予自动切换或自动冲正权限。
