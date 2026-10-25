# 上游供应商账单：历史关联补证

日期：2026 年 10 月 25 日（北京时间）。范围为既有已核实账号、10 月 21 日零点至 10 月 23 日零点（不含）的同一份账单。维护者要求补证并继续执行；本轮通过现有 IAM owner 入口追加证据，不调整用户收费、既有费率、清单范围或财务分录。

**本次恢复并核验了 201 条 legacy 存储关联，已追加正式 run；主核验仍为 `pending / missing_local / amount unknown / verification_scope none`，不宣称费用闭环完成。**

## 新增事实

| 状态 | 项目 | 核验结果 |
| --- | --- | --- |
| ✅ P1·中 | 恢复私有原始关联 | 201 条消费、201 条 timing、201 条 reservation、201 条 ledger，在同一个跨 schema 一致性只读快照中重新取得 |
| ✅ P1·中 | 逐条绑定 | 用户与 root → 唯一成功 attempt → reservation → ledger 幂等键、计费维度和金额均对应；不要求 request ID 等于 root |
| ✅ P1·中 | 原归档对应 | 201 条原始时间区间及三类 token 与既有公开归档逐条匹配；本窗未发现额外来源 timing、关联 reservation 或未匹配 ledger |
| ✅ P1·中 | 正式补证 | 同一报告追加 sequence=2，生产共 2 reports / 3 runs；actor 为既有 IAM owner，匹配成功审计 1 条 |
| ⚠️ P1·高 | 原执行覆盖 | 该来源历史 `user` intent 为 0；此次恢复的全部用户 timing 仍为 legacy，不产生历史 owner execution 事件 |

本地请求三类 token：未缓存输入 319,226、缓存读取 30,420,736、输出 92,726。已有两次外部测试单独保留来源，总记录 203 条；原归档 CNY 重算为 **2.04700896**，与独立供应商 CSV 报告金额一致，差额 **0**。本次未用当前费率替换历史价格，也不把重算金额当作现金付款凭证。

## 私有原件与复核

旧材料引用的临时私有 owner 目录及原下载 ZIP 已无法在本机定位；公开文件只有私有源摘要，不能恢复原 root/reservation 关联。因此重新从线上原记录取证，不把新快照说成已找回原始字节。

新快照采用 `REPEATABLE READ`、`READ ONLY` 一致性事务，每条 SELECT 最长 10 秒；选择字段不含用户正文、凭据或 token。对该来源全部账期重叠 timing 同时检查失败、取消、重试等 outcome；对已归档的成功子集再逐条绑定。大字段排序在审计进程内完成，没有调整数据库配置。

私有目录均为 `0700`，文件为 `0600`，持久保存两份：

- owner 主机：`/opt/micro-one-api/audit-evidence/vendor-bill-20261025T061438Z/`。
- 本机：`~/.codex/private-evidence/vendor-bill-20261025T061438Z/`。

`private-snapshot.json` 保存导入前的原始关联及原 2 份报告/2 条 run；`after-snapshot.json` 保存导入后核验材料。私有身份字段不进入 Git。只公开整个文件摘要及脱敏结果；摘要是材料引用，不是对数据库或供应商的签名认证。

复核命令（变量指向私有快照与既有归档记录 CSV）：

```sh
python3.11 docs/runbooks/evidence/vendor-bill-supplement-2026-10-25/check.py \
  --snapshot "$BILL_PRIVATE_SNAPSHOT" \
  --archive-records "$BILL_ARCHIVE_RECORDS" \
  --out /tmp/vendor-bill-recheck.json
```

输出路径必须不存在，避免覆盖已有核验结果。该检查器针对本次三桶归档和两次外部测试，不作为通用供应商对账工具。账本拆成 subscription/balance 时 token 只计一次；不把本地 cache-creation 整数零解释成供应商观测零。

真实快照重放通过，并验证 JSON 对象/文本两种存储形式；故意篡改 reservation 关联、ledger 用量、实际结算金额或删除 timing 均被拒绝。见[关联结果](evidence/vendor-bill-supplement-2026-10-25/legacy-bindings.json)和[检查验证](evidence/vendor-bill-supplement-2026-10-25/check-verification.json)。

## 正式追加与验收

只更新同一报告的 `snapshot_evidence_id`、`analysis_evidence_id` 和补证原因。CSV、历史费率、scope、版本、更正链、最终性确认、本地水位与 coverage 均保留原值。请求预先生成并核对预览，通过现有已登录 IAM owner 的控制台提交一次；未读取或导出用户凭据，未直接写报告表。

北京时间 14:20 的独立一致性只读事务核验：

| 字段 | 结果 |
| --- | --- |
| report ID | `aecaa5c09fe36405cd59d59eb1e8e7c4d62e6c72f04e9c67052db5cf045ed9b1` |
| 新 run ID | `0cef5ba9b6e4aea942cd4cad92e1d14cc3e75dae4a1e70c57dbaee0398648ed0` |
| sequence / actor | 2 / iam-user:1 |
| reports / runs | 2 / 3 |
| 匹配成功审计 | 1 条，原因摘要及 decision versions 存在 |
| 原报告、原 run、捕获的 legacy 行 | 全部与导入前私有快照相同 |
| 正式重放金额 / 差额 | 2.04700896 CNY / 0 |
| 正式状态 | pending / missing_local / amount unknown / verification_scope none |
| Billing 健康 | ok |

详见[请求摘要](evidence/vendor-bill-supplement-2026-10-25/request-summary.json)及[owner 落库核验](evidence/vendor-bill-supplement-2026-10-25/owner-verification.json)。

## 仍未满足的条件

- [x] 恢复、私有留存并逐条核验 legacy 记账关联。
- [x] 正式追加补证，并核对不可变历史和成功审计。
- [ ] 原账期 owner execution 覆盖、本地水位及 coverage 证据。历史 `user` intent 未写入，不能用新调用或补填时间恢复。
- [ ] 完整采购账号使用覆盖。成功子集精确相符不能证明没有未观测的 Key 使用。
- [ ] 执行时冻结的 CNY 成本或逐请求供应商实扣凭据。现有 owner 冻结估价为 USD；此次仍是归档 CNY 重算。

新 run 的 3,115 个执行时间未知记录属于该来源全历史未归期探针，不是本账期 3,115 次付费调用或差额。私有检查器的 `legacy_store_join_verified=true` 与 owner 的 `execution_join_verified=false` 分别陈述不同证据层次；owner 不读取私有快照，只验证提交输入绑定并重放归档算术。不得将两者混同后升级主核验结论。

本轮没有新建上游推理调用，没有修改应用运行时代码或重新部署服务。维护者随后要求先提交，本次将补证脚本、脱敏证据和验收记录一并提交，暂不推送。
