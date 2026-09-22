# v0.27 Canonical Usage 48 小时验收清单

这是 2026-09-04 至 2026-09-06 的一次性生产验收记录。该窗口已经结束，但仓库中
没有保存对应的 SQL 原始输出、固定时点 Prometheus 导出、供应商 usage/账单证据或
三方签名，因此所有门禁保持未通过，不能事后补签，也不能据此冲正历史账。

2026-09-20 的只读复核确认生产已是
`BILLING_CANONICAL_USAGE_MODE=charge`、
`BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST=5:k3`、
`BILLING_CACHE_CREATION_MODE=charge`。这表示 canonical usage 只对指定来源限定 charge，
其他来源仍按 legacy 口径计费并计算 shadow；它不等于本清单已获批准。原始脱敏配置与
字段完整率证据见
[`evidence/desktop-data-claims-2026-09-20.json`](evidence/desktop-data-claims-2026-09-20.json)。

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

## 现状复核（2026-09-21）

| 项目 | 记录 |
|---|---|
| 生效范围 | canonical usage 为 `charge`，allowlist 仅 `5:k3`；cache creation 独立为 `charge` |
| 生效时间/批准依据 | 仓库证据缺失，保持 `UNKNOWN`，不得由当前配置反推 |
| SQL/Prometheus 原始结果 | 未归档，旧窗口不得重跑后覆盖原时间 |
| 供应商证据/复核签名 | 未归档 |
| 来源字段 | 2026-09-20 样本中 Kimi 完整、glm-5.3 有缺口；F22 修复已于 2026-09-21 上线。同日用户授权的 model 143 映射切换产生一条 `5:k3` 新样本，来源字段完整，见[下一阶段证据](evidence/data-flow-next-stage-2026-09-21.json) |
| 当前决策 | 维持现有限定范围；不扩大 allowlist，不冲正历史账；扩围须开启新的固定窗口并建立新验收记录 |

新样本的 `canonical_present=1`、`usage_contract_version=1`、`cost_audit_status=priced`，且只有一条 consume 账本和一条账号额度事件。它填补白名单来源的样本空缺，但未独立量出 canonical 与 legacy 收费差异，也没有供应商用量、48 小时指标或复核签名；上面的历史门禁保持未通过。

## 决策规则

| 决策 | 条件 | 记录 |
|---|---|---|
| `observe 通过，charge 暂缓` | 数据门禁通过但没有供应商 usage/账单证据 | 默认安全结论 |
| `observe 通过，可提议 charge` | 所有数据门禁通过且外部证据抽样通过 | 仍需人工批准和变更窗口 |
| `FAIL，延长 observe` | 任一 SQL/Prometheus 门禁失败或队列不为 0 | 记录根因、修复和新窗口 |

任何后续人工批准都必须引用新的固定窗口记录、SQL 原始输出、Prometheus 导出和外部
证据索引；本历史清单不授予自动切换或自动冲正权限。
