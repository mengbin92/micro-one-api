# v0.36.3 发布核验与剩余现场任务

日期：2026-10-21（Asia/Shanghai）。接续 [当前计划](../design/next-stage-plan-2026-10-20.md)，在 P1 生产交付后完成正式发布，并核对 N3/N4 的现有启动条件。本轮不重复部署、不调用供应商费用或推理 API，不配置 reviewer、权限、收费或灰度，不重开独立观察任务。

## 正式发布完成

- 稳定版 [v0.36.3](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.3) 于 2026-10-21 10:17:15 CST 发布，非草稿、非预发布。
- 发布文档提交 `078bec05`，先合入并推送 develop，再将 develop 快进合入 main，最后推送 tag。发布时 main、develop、tag 均为 `946f25a09df15fb2bef4dcd99de7b12800eb4484`。
- [Release workflow](https://github.com/mengbin92/micro-one-api/actions/runs/39529684743) 完整 SUCCESS：IAM 真实 owner 浏览器、SQLite/MySQL Routing、Compose E2E、Playwright 全部通过；九服务共 18 项 amd64/arm64 构建、九项平台清单和 GitHub Release 发布全部通过。
- 同一提交的 develop [CI](https://github.com/mengbin92/micro-one-api/actions/runs/39529667522) / [安全](https://github.com/mengbin92/micro-one-api/actions/runs/39529667646)、main [CI](https://github.com/mengbin92/micro-one-api/actions/runs/39529680593) / [安全](https://github.com/mengbin92/micro-one-api/actions/runs/39529680701) 全部 SUCCESS。
- 业务源码摘要仍为 `5e78a87315f4ed72566915a87f3ad1e985e1b5bb3e8a2c51e01534c281d481e3`，与 2026-10-20 [已部署版本](p1-production-deployment-2026-10-20.md)一致。发布仅补文档；原观察任务的未提交文件未纳入。

[发布核验 JSON](evidence/release-v0.36.3-2026-10-21.json)保留版本、发布时远端引用、全部任务结论和链接。后续文档提交可继续推进 develop，不改变已发布 tag。

## N3/N4 生产只读核对

10:33:33 CST 读取运行中容器的有限布尔配置，并在 MySQL 一致性只读事务中统计 owner 表；每条 SQL 最长 10 秒。不读取报告正文、用户账本或模型凭据，不输出环境值或连接信息，也没有供应商请求。

| 检查 | 实测 |
| --- | --- |
| `billing_vendor_reports` / `billing_vendor_reconciliation_runs` | 0 / 0 |
| 契约 versions / bindings / probes / audits | 0 / 0 / 0 / 0 |
| Billing 供应商报告监控开关 | false |
| Channel / Relay 契约开关 | false / false |
| Channel reviewer 公钥配置存在 | false |
| Billing / Channel / Relay 进程 | running |

[脱敏只读证据](evidence/post-v0.36.3-readiness-2026-10-21.json)只包含时间、方法、计数和配置存在性。零行说明目前没有已持久化的现场报告/候选/probe，不能据此断言外部供应商没有消费、私有材料不存在或调用费用为零。

## 下一步所需材料与通过条件

### N3：首个生产来源费用闭环

1. 提供一个已授权来源：供应商、账号/项目、生产 source kind/id、精确凭据绑定证明及映射生效区间；只通过现有私有配置交付凭据，不放入 Git 或报告。
2. 提供一个完整已结束账期的原始报告及摘要，固定时区、币种/单位、版本、分页完整性、双方水位及覆盖证据。已有 DeepSeek 六次手工日窗仅能证明该手工范围的合计，不作为生产来源绑定。
3. 若使用现有费用 API 交叉核验，范围仅为 OpenAI/Anthropic 的 USD、UTC 整日 account_period；须有独立费用凭据与同范围完整 CSV。DeepSeek 不套用该 API。
4. 材料通过既有校验后，使用现有 owner 导入/重放，记录一个真实核验 run；重复导入不重复生效。解释缺行、内部调用、unknown 和差额，缺少证据保留 pending/attention/unknown，不修改用户账本。

当前结论：**WAITING_FOR_MATERIALS**。没有生产 report/run 可重放，也没有收到新增来源绑定和账期材料；不以 fixture 或新增付费调用补足。

**后续状态（同日另行授权）**：维护者已选择 DeepSeek 渠道 11、两天核验周期并允许小额测试。当前来源/凭据掩码已核对，10/19–20 零推理基线与两次授权短调用已归档；N3 转为等待 10/21–22 已结束窗口报告及覆盖审查。上述 WAITING_FOR_MATERIALS 保留 10:33 核对时点，详见 [DeepSeek 记录](deepseek-channel11-two-day-2026-10-21.md)。N4 未因此取得签名或晋级批准。

### N4：一个上游契约候选的现场准入

1. 指定一个已审候选的精确生产来源、模型、协议、endpoint/inbound、basis/source/binding revision；候选需有独立回放及相应 CI 证据。
2. 明确独立 reviewer/IAM、reviewer 公钥、凭据账号绑定，以及 candidate/previous 的调用与费用预算。签名材料、密钥和授权继续通过现有私有交付流程管理。
3. 条件齐备后才启用该范围的既有现场流程：至少 30 分钟、100 个完整样本和 fresh proof，核对 usage、切换收敛及通知链路，再给出晋级或隔离/回滚记录。不能把未签、过期或不足样本改为通过。

当前结论：**WAITING_FOR_MATERIALS**。无持久化候选/绑定/probe，reviewer 公钥未配置，契约开关关闭。现有工程保持，未创建候选、签名、启用 gate 或发起 canary。

## 其他剩余边界

原固定 48h FAIL/PAUSED、截止后新 Jaeger 的 TTL/生产 WS，以及自然业务峰值继续由独立生产验收承接，不拼接窗口。executor 七天准入仍需确认起点和同入口/协议/时段可比样本，本轮未重新采集或恢复自动任务。

O5 缓存优化、排队、多副本/真实 OAuth、Playground 原参数恢复/历史编辑，以及容差/归档/分区仍遵守当前计划的明确需求或数据规模条件；本轮没有新增满足条件的证据，不为清空历史 TODO 另开工程。
