# Micro-One-API v0.33.4 发布：对账幻影差异修复与支付对账持续失败告警

> 2026-09-28 · 上一版：[v0.33.3](./release-v0.33.3.md)（2026-09-28）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.4)

v0.33.4 是 v0.33.3 之后的 **PATCH 对账准确性与支付对账告警版本**：billing 对账不再把已删除渠道的历史账本报为差异，支付对账持续失败新增 5 分钟持久告警；生产完成 6 项对账差异的数据修复，最新对账运行差异为 0。

**无公共 API/proto 变更、无数据库迁移、无前端资源更新**。受影响的运行时服务只有 `billing-service`（另含一条 Prometheus 告警规则，生产已加载）。

## 修复内容

### 1. 对账不再把已删除渠道的历史账本报为差异

**根因**：渠道删除后其计数器行被移除，但历史消费账本保留。对账把该渠道的全量账本用量与隐式零计数器比较，每次运行都报告幻影差异（生产渠道 6/7/8，差异 -7.2M / -15.5M / -132.9M quota），淹没了对账信号。

**修复**：账本侧渠道没有对应计数器行时跳过比较——已没有权威对象可供比对。仍存在但发生漂移的渠道（如生产渠道 1 漂移 +17.9M）继续正常报告。

**影响服务**：`billing-service`。

### 2. 支付对账持续失败新增持久告警

**根因**：后台支付对账此前只在日志中记录扫描与订单刷新失败，低流量时段反复的提供商查询或资产发放失败没有任何持久信号，只能翻日志发现。

**修复**：每次完成的扫描把最新失败状态记录为指标 `micro_one_api_billing_payment_reconcile_failed`，持续 5 分钟未恢复触发 `PaymentReconciliationFailing`（warning）；进程关停中断扫描时保留此前状态，避免重启掩盖进行中的故障。失败、恢复、取消与提供商独立超时均有用例覆盖，Prometheus 规则测试纳入 CI。

**影响服务**：`billing-service`（指标）与 Prometheus 规则（生产已挂载 `/etc/prometheus/alerts.yml` 并生效）。

### 3. 生产对账差异数据修复（运维）

按仓库既有修复脚本语义（账本为权威）在单个事务内完成，已通过最新对账运行验证：

- 渠道 1 计数器对齐账本权威值（修正 -17,935,970 的历史漂移；连续 6 次对账运行确认漂移恒定、无活跃泄漏），渠道 9 对齐 -76。
- 回填 30 天窗口内 2 条缺失消费日志（09-05，各 96 quota）并补摄入去重声明；修正日志 66848 的 quota（3794 → 3894，relay 日志漏加 completion_tokens）。
- 31 笔幻影应收（overdue 恰等于"预留 − 实际"退款额、钱包全程约 300 万为正，系 09-23 构建产物误报）标记为 settled 并备注原因，应收镜像与透支余额重新一致。

**结果**：对账运行 200（2026-09-28 16:39 +0800）`discrepancy_count=0`，应收 pending 归零。30 天窗口外的全历史缺失日志（40,014 条）未回填——对账只校验 30 天窗口，批量回填旧日志会扭曲日志侧统计。

## 兼容性说明

- 无公共 API/proto、数据库 schema 或前端资源变更；无需新配置项。
- 新增指标 `micro_one_api_billing_payment_reconcile_failed` 与告警规则 `PaymentReconciliationFailing`；Prometheus 需加载更新后的规则文件（生产已完成）。
- 数据修复只影响统计与镜像表：渠道 `used_quota` 为展示统计，应收保留审计行（settled），账务余额与账本未改动。

## 升级步骤

1. 在本机交叉构建 `linux/amd64` 的 `billing-service` 镜像（`scripts/deploy-update.sh billing-service`），不在生产主机上构建。
2. 为当前线上镜像保存回滚标签，上传并加载新镜像，在生产 Compose 目录执行 `docker compose up -d --no-deps billing-service`。无需迁移数据库或更新前端。
3. 确认 Prometheus 规则文件包含 `PaymentReconciliationFailing` 且 Prometheus 已加载；检查 billing 容器状态与近期日志。

生产已完成镜像更新。当前镜像为 `sha256:4a79801e0db45c65962b13cb454dab992de3917ef5870fcf101493aa123e7b94`；此前镜像保留为 `docker-compose-billing-service:rollback-20260928-163748`（`sha256:e35fbb3289df`）。若需回滚，将该标签重新标记为 `latest` 后仅重建 `billing-service`。回滚会重新引入已删除渠道的幻影差异报告；已完成的应收结算与日志回填不受影响。

## 验证

- `go test ./app/billing/...` 全部通过，含新增的已删除渠道对账用例与支付对账告警的失败、恢复、取消、超时用例。
- Prometheus 规则测试（`deploy/prometheus/alerts/operations.test.yml`）纳入 CI 并通过。
- 生产 `billing-service` 重启后自动完成的对账运行 200：`total_accounts=8`、`discrepancies=[]`、应收 pending=0。
- 生产 Prometheus 容器内 `/etc/prometheus/alerts.yml` 已包含 `PaymentReconciliationFailing` 规则。

## 完整变更日志

- `61db8cca` fix(billing): skip deleted channels in usage reconciliation
- `cd3dd957` feat: alert on persistent payment reconciliation failures
- `426b0363` docs: record O5 production RPC follow-up observation
- `23119d12` docs: close O2 production acceptance with real alert evidence
- docs(release): v0.33.4
