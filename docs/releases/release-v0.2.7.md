# Micro-One-API v0.2.7 发布公告

> 2026-06-24 · 上一版: [v0.2.6](./release-v0.2.6.md) (2026-06-20)

v0.2.7 是一个可观测性增强版本，重点补齐 billing 对账任务与 monitor-worker 渠道健康探测的 Prometheus 指标，便于在生产环境观察任务成功率、耗时、差异类型和上游探测失败原因。无数据库迁移，无破坏性 API 变更。

## 亮点

- **对账任务指标**：新增对账运行次数、运行耗时和差异类型计数，便于监控对账任务是否稳定执行。
- **渠道健康 sweep 指标**：新增 monitor-worker 渠道健康检查批次的成功率和耗时指标。
- **渠道探测 probe 指标**：新增单个渠道 `/models` 探测的成功率、耗时和失败原因标签，便于定位 timeout、上游状态码、SSRF 拦截或 provider 不支持等问题。
- **部署文档补齐**：部署文档列出新增指标名称，方便接入 Prometheus 和告警规则。

## 变更内容

### Added

- `internal/pkg/metrics` 新增 billing 对账指标：
  - `micro_one_api_billing_reconciliation_runs_total`
  - `micro_one_api_billing_reconciliation_run_duration_seconds`
  - `micro_one_api_billing_reconciliation_discrepancies_total`
- `internal/pkg/metrics` 新增 monitor-worker 渠道健康指标：
  - `micro_one_api_monitor_channel_health_check_runs_total`
  - `micro_one_api_monitor_channel_health_check_run_duration_seconds`
  - `micro_one_api_monitor_channel_health_probe_total`
  - `micro_one_api_monitor_channel_health_probe_duration_seconds`
- `RunReconciliation` 在成功、失败和存在差异时记录不同状态标签。
- `ChannelHealthChecker` 记录 sweep 结果、单渠道 probe 结果和失败原因。

## 配置变化

- 无新增环境变量。
- 继续通过各服务已有 `/metrics` 端点暴露 Prometheus 指标。

## 升级说明

- 无数据库迁移。
- 无破坏性 API 变更。
- 从 v0.2.6 升级时，重新构建并发布 `billing-service` 与 `monitor-worker` 即可使用新增指标；如统一发布镜像，可按常规流程重建全部服务。

## 验证

本次发版前执行：

```bash
make test
cd web && npm run lint && npm test && npm run build
```

重点覆盖：

- billing 对账任务指标计数测试。
- monitor-worker 渠道健康 sweep/probe 指标计数测试。
- 前端 lint、单元测试和生产构建校验。
