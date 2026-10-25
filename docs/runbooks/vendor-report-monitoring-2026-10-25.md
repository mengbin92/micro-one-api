# 上游供应商账单监控与漏导检测

日期：2026 年 10 月 25 日（北京时间）。本次只补监控及证据覆盖，不调整用户收费、供应商金额或既有核验结论。

版本判定：PATCH，修复既有监控覆盖与告警可用性，不改变公开 API 或账务语义。本轮授权包括更新线上服务后提交并推送 `develop`，不创建版本 tag。

## 决策与范围

- [x] 开启生产 `BILLING_VENDOR_REPORT_MONITOR_V1`，保持现有每小时检查周期。
- [x] 备份生产 Compose 后只重建 Billing；首次扫描成功，`pending=1`、`overdue=0`、`unavailable=0`，Prometheus 已抓取。
- [x] 修复 Alertmanager 到通知服务缺少 Bearer 认证的问题。复用既有 monitor caller，凭据只保存在 Alertmanager 数据卷，权限 `0400`，配置通过校验和热加载。
- [x] 无写入的 GET 验证：未认证返回 401，认证后返回 405。未创建测试通知；已有通知记录含成功发送历史，不将其冒充本次告警的实际送达。
- [x] 发布预期账号/账期清单检测及规则；漏导、清单到期、误关开关和检查冻结均有回归覆盖，生产指标与规则正常。

清单独立于已导入账单登记，不能从现有报告反推完整采购账号清单。初始生产范围以可核实材料为准，未登记账号不声称已覆盖。不推测上游供应商的日结或月结周期；使用明确的固定账期及清单有效期，过期必须提醒续登记。

## 配置契约

`BILLING_VENDOR_REPORT_EXPECTATIONS_JSON` 是严格 JSON，最多 100 项；缺失、非法、重复或过期均不能显示为覆盖正常。示例使用占位账号：

```json
{
  "valid_until": "2026-10-28T12:00:00+08:00",
  "reports": [{
    "scope": {
      "provider": "upstream",
      "account": "account-a",
      "currency": "CNY",
      "unit": "currency",
      "start": "2026-10-21T00:00:00+08:00",
      "end": "2026-10-23T00:00:00+08:00",
      "timezone": "Asia/Shanghai",
      "granularity": "account_period"
    },
    "due_at": "2026-10-26T00:00:00+08:00"
  }]
}
```

固定清单与报告的完整 scope 精确匹配：供应商、账号、项目、模型、币种、单位、时区、粒度、半开账期全部一致。时间点按 UTC 归一；不能用另一个账号、部分重叠窗口或不同币种消警。`due_at` 不早于账期结束，`valid_until` 不早于全部导入截止时间。

`pending` 报告表示已经收到材料，只解除“完全未导入”；原 pending、未知金额及最终性闸门继续独立生效。清单只登记义务，不生成供应商金额、执行事件或财务分录。

清单从环境变量在启动时加载。续登记或修改截止时间后，校验 Compose 并仅重建 Billing；启动会立即重新检查，不需要等待下一小时。

## 监控与告警

| 状态 | 含义 | 动作 |
| --- | --- | --- |
| `expected` / `missing` | 已登记及尚无匹配报告的账期数 | 截止前保留待办 |
| `missing_overdue` | 已到导入截止时间仍缺报 | 进入待复核告警 |
| `expectations_unconfigured` | 清单缺失或校验失败 | 进入监控不可用告警 |
| `expectations_expired` | 当前时间已到清单有效期 | 补齐后续义务并重新配置 |
| `unavailable` | 无法完整扫描最新有效报告 | 不推断覆盖或金额 |

Billing 正常被抓取、但供应商监控指标消失时也进入告警，避免误关 feature flag 后静默。新增指标只使用有限 state 标签；具体缺报账号/账期写监控日志，不作为 Prometheus 动态标签。

检查期间保持不可用标记，供应商检查使用独立超时。`vendor_report_next_scan_deadline_seconds` 记录下一次检查最迟时间（既有周期加 5 分钟余量）；指标仍在但检查循环不再推进时也会告警。

继续使用现有 Prometheus → Alertmanager → 通知服务链路，规则持续 5 分钟后触发。`RECON_ALERT_ENABLED` 控制 Billing 直接通知，本次保留其既有配置；不影响独立 Prometheus 告警。每小时扫描意味着跨过截止时间后最多还需等待一个扫描周期，再加规则及通知分组时间。

## Alertmanager 认证配置

接收端要求既有 monitor caller 的 Bearer 凭据。将其安全写入 `/alertmanager/notify-monitor.token`：通过 stdin 或本机安全文件传递，不写进命令参数、日志或 Git；文件只允许 Alertmanager 实际 UID 读取。新部署必须先准备该文件，再启用仓库默认的认证配置。

变更前备份 Compose、告警规则及 Alertmanager 配置。配置使用 `amtool check-config` 校验，保留 bind mount inode 后发送 HUP 热加载；检查 `alertmanager_config_last_reload_successful=1`。GET 返回 405 仅证明带认证可到入口，不代表邮件或 webhook 已送达。

## 验证与回滚

- [x] `go build ./...`、Billing 全包测试、供应商相关 biz/data race、Wire 重新生成及注入器编译、架构检查通过。项目要求 Go 1.27，未降级为通用指令中的 1.26。
- [x] Prometheus 五套规则测试通过，覆盖误关、清单失效、漏导和恢复消警。
- [x] 仓库 Alertmanager 配置使用占位凭据文件通过 `amtool check-config`。
- [x] 新版 Billing 与规则的生产验证、当前清单及有效期核对。
- [ ] 本轮自然告警实际送达；当前无触发条件，不主动制造告警。

回滚仅恢复带时间戳的配置备份及原 Billing 镜像，重新加载规则或重建指定服务。保留账单报告和审计事实；不得通过删报告、补造执行事件或改用户账本消警。

## 初始登记

维护者已明确选择“按现有核实范围上线”，确认当前登记范围及清单到期提醒；本次确认不需要再次修改生产配置。

生产先登记现有已核实的一个账号账期：北京时间 10 月 21 日零点至 10 月 23 日零点（不含），导入截止 10 月 26 日零点，清单有效期到 10 月 27 日零点。范围来自已核实的独立材料，不枚举或推定其他采购账号。后续须独立登记新增账号与下一账期；未续登记会因清单过期告警，而不会无限期显示覆盖正常。

## 生产验收

北京时间 10 月 25 日 10:15 完成验证：Linux/amd64 镜像已替换，健康接口正常，自动重启 0、OOM false；`expected=1 / missing=0 / pending=1 / unavailable=0`，清单未失效。检查期限、覆盖指标已被 Prometheus 抓取，两条规则均为 `health=ok / inactive`，当前没有应触发的供应商告警。

镜像源码摘要已与本机本轮输入重新计算比对；首次验收时旧 Compose 的容器源码标签为历史值，验收未将其作为新镜像来源。镜像身份、源码摘要、回滚点及脱敏结果见[首次生产核验 JSON](evidence/vendor-report-monitoring-2026-10-25/production-verification.json)。回滚镜像为 `docker-compose-billing-service:rollback-20261025-100911`。

维护者随后授权“更新线上服务，然后提交并推送”。北京时间 10 月 25 日 10:58 已完成后续验收：运行镜像与当前代码一致，补齐 Billing Compose 的源码标签后仅重建 Billing；镜像及容器源码摘要一致，健康正常，自动重启 0、OOM false。覆盖指标和新的检查期限已被 Prometheus 抓取，`expected=1 / missing=0 / pending=1 / unavailable=0`，两条告警规则正常且未触发。见[服务更新核验 JSON](evidence/vendor-report-monitoring-2026-10-25/service-update-verification.json)。本轮未修改清单范围、导入截止时间或有效期。
