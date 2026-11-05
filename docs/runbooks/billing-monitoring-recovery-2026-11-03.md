# 计费监控修复记录（2026-11-03）

## 已复现的现象

| 问题 | 证据 | 实际影响 |
|---|---|---|
| 毛利告警计算 | 15 笔请求中 10 笔差额为 -1 quota、5 笔为 0；旧 P50 显示 -50，带负值 `_sum` 的 `rate()` 却为正 | 本次存在配置价差；监控还存在全零误报及净差额漏报条件 |
| 后台未定价监控 | 每分钟一次 `ListUnpricedRoutedModels` 返回 `PermissionDenied`；admin-api 可采集但未定价计数缺失 | 后台监控盲区，不代表请求无法定价 |
| default 无单独策略 | 启动后 65 笔 default 请求已完成结算，合计扣量 1,677 quota | 无策略行时继承默认倍率与 `subscription_first` 是受支持状态，无需补策略消除日志 |

上述金额均为账本扣量与配置采购估价的比较，不等同于上游供应商的实际结算账单。
本次缓存读取售价为 $0.006/百万 token，配置采购估价为 $0.007/百万 token，15 笔请求
逐笔重算与账本完全一致，合计负差额 10 quota（$0.001）。价格调整属于独立的运营决策。

## 修复边界

1. 毛利直方图增加整数负值边界 `-1`；告警使用负请求占比，不使用 P50 的符号。
   非负盈利、亏损计数器的速率之差计算总差额，兼容进程重启。
2. 毛利仅在落账或事务提交成功后观测，一次成功请求观测一次；重试、拆账与回滚均有回归覆盖。
3. `CountUnpricedRoutedModels` 是仅限专用 admin 服务身份的 gRPC 统计入口，仅返回两种来源计数。
   它复用模型查询，不接入人工会话。人工 `ListUnpricedRoutedModels` 仍由 owner 校验权限。
4. 读取价格失败、配置缺失或解析失败时保留上次计数，报告求值失败；成功时间不会被失败刷新。
   新告警同时覆盖失败、指标缺失与后台停止刷新，目标本身不可采集由既有采集告警处理。
5. 不修改 default 策略，不重写历史账本，不修改线上售价或采购配置。

## 本地验证

- [x] 旧告警规则已被回归材料复现：零值误报、净负差额漏报。
- [x] 新毛利规则 `gross-margin.test.yml` 通过。
- [x] 新监控失败规则 `unpriced-metric.test.yml` 先失败、修复后通过。
- [x] 毛利落账、事务回滚与幂等的 Go 回归：`go test ./app/billing/internal/biz -count=1`。
- [x] 后台统计、人工权限及配置读取错误的 Go 回归：
      `go test ./app/admin/internal/service ./app/channel/internal/server ./platform/metrics ./platform/security/serviceidentity -count=1`。
- [x] IAM 入口契约、架构检查：`scripts/check-architecture.sh` 与
      `python3 scripts/rbac-matrix-refresh.py --check` 均通过。
- [x] 生产部署与监控窗口验证：2026-11-03 12:25 完成，见下节。

Prometheus 回归沿用 CI 的 `promtool test rules`，两个新测试文件已加入 CI。
本地使用现有 Prometheus 镜像执行，无需在生产主机运行测试或构建镜像。

## 生产部署记录（2026-11-03 12:25）

| 项 | 值 |
|---|---|
| 镜像标签 | `deploy-20261103-122545-629d5158`（commit `629d5158`） |
| 回滚标签 | `rollback-20261103-122545`（三服务共用同一时间点） |
| 部署顺序 | channel-service → admin-api → billing-service |
| 容器状态 | 三者均 running，RestartCount=0 |
| 告警文件备份 | `alerts.yml.bak.20261103-123149` |
| Grafana 面板备份 | `billing.json.bak.20261103-123459` |

部署后核对：

- admin-api 自 12:26 重启后 `unpriced routed metric evaluation failed` 告警日志计数为 **0**（部署前每分钟 1 条）；
  `micro_one_api_model_unpriced_routed_evaluation_success{instance="admin-api:8000"} = 1`，
  成功时间持续刷新，两种来源计数均可读。
- billing-service 新计数器已出数：`gross_profit_positive_quota_total=0`、
  `gross_profit_negative_quota_total=5`（重启后首次采样），直方图 `le="-1"` 桶随后累计到 16，
  说明重启后已结算请求全部为负差额。
- `NegativeGrossMargin` 在 reload 后处于 firing：这是配置价差导致的真实信号
  （缓存读取售价 $0.006/百万 token 低于配置采购估价 $0.007/百万 token），
  不是本次修复引入的误报。售价或采购价的调整仍需运营决策。
- `UnpricedRoutedMetricUnavailable` 为 inactive，符合成功求值的预期。
- 三个服务在部署后 12 分钟内无 error 级日志、无 panic。

注意事项：

- 线上 `alerts.yml` 与仓库存在两处先于本次改动的本地差异：分组名 `p3-vendor-evidence`
  （仓库为 `relay-gateway`），以及 `VendorEvidenceMonitorUnavailable` /
  `VendorEvidenceNeedsReview` 两条告警线上缺失。本次只按补丁应用了本任务的两处改动，
  没有覆盖整份文件，也没有恢复这两条告警；是否与仓库对齐需另行确认。
- 新增 `-1` 桶的序列从部署时刻才开始，与 `_count` 的 15m 窗口长度不同，
  部署后约 15 分钟内「负请求占比」会偏低；以补齐窗口后的数值为准。
- Prometheus 会按浮点格式规范化 `le` 标签（线上为 `-1.0`），告警正则
  `-1|-1[.]0+` 同时覆盖这两种写法。

## 部署与验收

1. 先更新 channel-service，使专用统计 RPC 可用，再更新 admin-api；配套更新 billing-service。
   镜像必须本机 cross-build 为 Linux/amd64，部署前保存各服务回滚标签。
2. 更新 Prometheus 告警文件并校验、重载；新计数器与 `-1` 桶需要新服务提供采样数据，
   滚动更新期间的 15m 混合窗口不作为完整新口径验收。
3. 至少观察两个后台求值周期：不再出现该 `PermissionDenied`，求值成功为 1，
   成功时间持续刷新，两种来源计数均可读；不能把缺失指标当作零。
4. 用账本按请求合并后的差额核对新计数器。真实配置倒挂应继续告警，监控修复不会把负差额隐藏。
5. 售价或采购价调整需另行确认，必须沿现有配置发布入口保存并刷新缓存，不直接改写历史价格快照。
