# 上游供应商历史证据逾期复核

日期：2026 年 10 月 26 日（北京时间）。接续[账单监控](vendor-report-monitoring-2026-10-25.md)与[历史关联补证](vendor-bill-supplement-2026-10-25.md)。

**最新决定：维护者随后明确要求“更新线上，然后关闭相关对照告警，功能已经验证过了，线上可以先不启用”。本轮已停用生产上游供应商证据扫描及两条对应规则，保留已验证代码、历史材料和其他告警。以下启用与复核例外验收属于停用前记录；停用实施结果见文末。**

## 维护者决定与边界

维护者已明确选择“接受已知历史缺口，登记复核例外”。只处理本报告当前 run 的重复逾期提醒，保留 `pending / missing_local / amount unknown / verification_scope none`，不把历史缺口改成核验成功，不修改客户账本、冻结策略或上游供应商金额。

例外绑定下列不可变身份，期限不超过现有清单的 **10 月 27 日零点**；不续登记采购账号或后续账期。新 run、报告更正、例外过期、金额差异、漏导、清单失效和监控故障仍须正常提醒。

| 字段 | 已核实值 |
| --- | --- |
| report ID | `aecaa5c09fe36405cd59d59eb1e8e7c4d62e6c72f04e9c67052db5cf045ed9b1` |
| run ID / sequence | `0cef5ba9b6e4aea942cd4cad92e1d14cc3e75dae4a1e70c57dbaee0398648ed0` / 2 |
| 账期 | 10 月 21 日零点至 10 月 23 日零点（不含） |
| 首次告警 | 10 月 26 日 01:02:50；账期结束满 72 小时后首次扫描与规则等待触发 |
| 主核验 | pending，权威 attempts 为 0，本地水位为空，coverage 证据为空 |
| 未归期记录 | 当前 run 保留 3,115 条 health_probe，不认定为本账期付费调用 |
| 事后算术 | 203 条归档重算 2.04700896 CNY，与上游供应商报告差额 0；execution_join_verified=false |

## 现场调查

- [x] 线上 Prometheus 复现 `VendorEvidenceNeedsReview{state="overdue"}`，值为 1。
- [x] 监控 `pending=1 / overdue=1 / missing=0 / unavailable=0`，抓取正常，检查期限推进。
- [x] Billing 健康接口返回 `{"status":"ok"}`，重启 0、OOM false。
- [x] 一致性只读事务核对 2 reports / 3 runs；当前 leaf 的 sequence=2 与昨日补证原件完全一致，原始 payload SHA 等于 run ID。
- [x] 当前来源全历史 `user` intent 为 0；legacy 关联不能恢复当时未写入的 owner 执行事件。
- [x] 使用昨日私有原件重放 201 条 legacy 关联，检查器通过；仍不授予 owner execution 覆盖。

取证保留真实账号与执行关联的私有原件，不进入 Git。每条 SELECT 限制 10 秒，使用 `REPEATABLE READ / READ ONLY`；大 payload 不在数据库排序。没有调大数据库排序内存或修改数据库配置。

## 实施与验证

- [x] 增加精确 report/run、有期限、附复核人与依据的监控例外。
- [x] 原始 overdue 和 pending 指标保留，已复核逾期单独计数；直接通知与 Prometheus 采用相同边界。
- [x] 新 run、到期、异常清单、其他逾期与金额差异的回归通过。
- [x] 本地跨构建 Linux/amd64，备份配置和运行镜像后仅更新 Billing，热加载告警规则。
- [x] 复查 Prometheus 与 Alertmanager 已恢复、原报告/run/财务事实保持；保留脱敏生产验收。

本次不创建版本 tag。若后续纳入发版，归类为 **PATCH**：完善现有待复核告警的处置生命周期，公开账务契约和核验结论不变。

## 例外登记契约

复用既有 `BILLING_VENDOR_REPORT_EXPECTATIONS_JSON`，增加可选 `review_exceptions` 数组，最多 100 项，每份报告最多一个当前例外。每项包含 `report_id`、`run_id`、`reviewed_by`、`reason`、`evidence_id`、`reviewed_at`、`expires_at`。两个 ID 必须为 64 位小写十六进制；依据为 `sha256:` 引用，复核人与原因不能为空；到期必须晚于复核且不晚于清单有效期。旧配置不受影响。

监控在有效清单、有效期限、pending 且已逾期的前提下，用现有持久化规范的完整 payload SHA 核对 run。匹配时只跳过该项直接通知，写入包含报告/run、复核人、原因、依据及期限的审计日志，新增 `reviewed_overdue` 计数。原始 `pending`、`overdue`、未知金额和报告内容保留。

Prometheus 只在同一 target 的已复核数覆盖全部逾期数时停止 overdue 提醒；其他未复核逾期、attention、missing_overdue 继续提醒。旧二进制没有新指标时仍告警，其他实例/job/stack 的例外不能遮盖本实例。Grafana 原有全 state 历史图可直接显示新计数。

本轮[复核决定](evidence/vendor-evidence-overdue-2026-10-26/review-decision.json)记录维护者在本聊天中的授权及接受的缺口；其摘要是配置引用，不是供应商签名或财务确认。配置、决定原件和回滚文件保留在生产私有审计目录。

回归先复现“登记例外后仍重复通知”，修复后通过；`go build ./...`、Billing 全包测试、相关 biz race、五套 Prometheus 规则、53 条规则语法、架构和 Markdown 链接检查通过。真实生产 run 经 typed JSON 往返后 SHA 完全相同，见[兼容回放](evidence/vendor-evidence-overdue-2026-10-26/production-run-replay.txt)。

例外到期撤销沿用每小时扫描，最多等待一个周期，再加 5 分钟规则等待及通知分组时间；不承诺午夜瞬时发送。下一步须独立续登记后续真实采购范围；本轮没有自动延长清单或接受其他账期缺口。

## 生产验收与回滚

两条供应商规则均为 `health=ok / inactive`，Prometheus 与 Alertmanager 当前供应商告警均为 0；`pending=1 / overdue=1 / reviewed_overdue=1 / unavailable=0`，复核应用审计日志已核对。Billing 健康正常，自动重启 0、OOM false，其他容器未重建。见[生产验收](evidence/vendor-evidence-overdue-2026-10-26/production-verification.json)。

上线前后同范围一致性只读快照核对：2 reports、3 runs、201 条历史 ledger、201 条 reservation 的已捕获字段及冻结请求快照摘要完全一致，见[不变性记录](evidence/vendor-evidence-overdue-2026-10-26/invariance.json)。此结论不包含其他正常并发业务的账务变动。

首次部署因健康接口先于供应商扫描完成、过早读取指标而触发自动回滚，原服务健康恢复；第二次等待有界扫描完成，观察到指标由未就绪变为例外生效后才热加载规则。没有绕过校验门禁。

配置与规则原件、复核决定、上线前容器身份和启动观察保存在生产私有目录 `audit-evidence/vendor-overdue-review-20261026T012452Z/`，目录权限 0700；配置备份为 0600。回滚镜像为 `docker-compose-billing-service:rollback-review-20261026T012452Z`。回滚时恢复其中的 Compose 与规则原件，仅重建 Billing，并向 Prometheus 发送 HUP；规则文件保持原 bind mount inode。移除例外或回滚后，原逾期提醒应恢复，不改变历史报告。

本轮未创建新的上游推理调用、测试通知或版本 tag；实现、测试和处置记录按维护者后续指令保留，见文末。

## 后续决定：生产暂不启用

- [x] 记录维护者决定：功能与规则实现保留，生产暂不启用，不将历史报告改成核验成功。
- [x] 核对线上 Billing 镜像、容器和当前源码摘要一致；无需重复构建相同二进制。
- [x] 带时间戳备份生产 Compose、`.env` 与规则，将 `BILLING_VENDOR_REPORT_MONITOR_V1=false`，仅重建 Billing。
- [x] 从生产加载清单撤下 `VendorEvidenceMonitorUnavailable`、`VendorEvidenceNeedsReview` 两条规则，热加载 Prometheus；其他规则保持。
- [x] 验证健康、正常抓取、两条规则已卸载、供应商告警为零及其他容器保持。

`RECON_ALERT_ENABLED` 保持其既有值；不修改支付、结算、服务可用性告警。停用监控不代表成本为零或历史缺口已补齐。原采购清单和复核例外保留为历史材料，停用期间不再催报到期；重新启用前须核实并更新采购范围、清单和例外期限，再同时恢复两条规则。

北京时间 **09:39:42** 完成停用验收：Billing 健康 `ok`，自动重启 0、OOM false；新的成功抓取确认供应商扫描状态序列已停止输出。Prometheus 加载规则由 53 条变为 51 条，除上述两条外，其他规则的查询、标签、注释和等待时间完全一致且健康正常。Prometheus 与 Alertmanager 供应商告警均为 0，其他 20 个容器身份和启动时间保持。见[停用验收](evidence/vendor-evidence-overdue-2026-10-26/production-disabled-verification.json)。

生产 Compose 与 `.env` 已同步关闭开关，避免后续普通重建恢复启用。配置有效值比较确认唯一变化是 Billing 的该开关；镜像与当前代码摘要一致，因此未重复构建或重跑已通过的 Go 测试。本轮校验了有效 Compose、停用后的 51 条 Prometheus 规则及线上实际状态。

本轮配置备份位于生产私有目录 `audit-evidence/vendor-monitor-disabled-20261026T013840Z/`，目录 0700、原件 0600，回滚镜像为 `docker-compose-billing-service:rollback-monitor-off-20261026T013840Z`。恢复旧配置将重新开启该监控，应先更新已过期的清单和例外，再按维护者指令恢复；不要在后续普通部署中自动恢复两条规则。

维护者随后要求“需要保留就提交一次”。功能实现、回归测试、配置示例和脱敏验收材料均需保留，本记录随上述内容合并为一次提交，不推送；私有原件与生产回滚备份不进入 Git。
