# Changelog

All notable changes to `micro-one-api` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- 渠道健康状态与自动熔断：relay 上游调用会回写成功/失败和响应时间，
  channel-service 连续失败达到阈值后跳过该渠道，冷却期后允许半开恢复；
  管理后台渠道列表展示健康状态、失败次数和熔断冷却时间，并支持手动触发
  `/models` 健康探测。
- monitor-worker 支持定时探测启用渠道的 `/models` 健康状态。
- 新增 `CHANNEL_HEALTH_FAILURE_THRESHOLD`、`CHANNEL_HEALTH_COOLDOWN`、
  `CHANNEL_HEALTH_CHECK_ENABLED`、`CHANNEL_HEALTH_CHECK_INTERVAL`、
  `CHANNEL_HEALTH_CHECK_TIMEOUT` 配置项。

## [0.2.2] - 2026-06-15

### Added
- `notify-worker` 支持 pending 通知实际投递：webhook/event 走 HTTP POST，email 走 SMTP，
  并支持失败重试与最终 failed 状态。
- CI Docker 构建矩阵覆盖全部服务镜像。

### Changed
- 对账告警通知类型可通过 `RECON_ALERT_NOTIFY_TYPE` 配置；
  `RECON_ALERT_RECIPIENTS` 文档改为 webhook URL / email 目标语义。

## [0.2.1] - 2026-06-12

### Added
- **管理后台 Top 用量图表**: `OverviewPage` 增加 Top N 渠道 / 模型用量图表
  (`web/src/pages/admin/OverviewPage.tsx`),导航接入"用量排行"入口
  (`AppNavigation`),并补齐对应单测 (`AppNavigation.test.tsx`)。

### Changed
- 前端构建优化: `web/vite.config.ts` 增加 chunk 拆分策略,
  拆分 vendor / 路由懒加载 / ECharts 等公共依赖,降低首屏包体积。

### Fixed
- 端到端测试与 docker-compose 环境文档对齐: 补齐
  `deployments/docker-compose/.env.example` 的 e2e token / 通知相关变量;
  `test/e2e/main.go` 修正 token 流程以匹配实际 relay 行为;
  同步更新 `.env.example` 与 `docs/deployment.md` / `README.md` 文档。

## [0.2.0] - 2026-06-10

### Added
- **成本与利润分析 (Phase 2)**: `billing_ledgers` 新增 `upstream_cost` 字段(`migrations/029`),
  relay 提交时按渠道侧定价计算上游成本并写入账本;新增收入/成本/毛利相关统计维度。
- **多维 SQL 用量聚合 (Phase 1)**: `billing` 服务新增多维聚合 RPC(按用户/渠道/模型/Token/分组/小时|日),
  取代原先 admin 的 1000 条内存抽样统计;`migrations/028` 为 `billing_ledgers` 补充 `(created_at)`、
  `(channel_id, created_at)`、`(model_name, created_at)` 索引。
- **渠道对账 (Phase 3 起步)**: `RunReconciliation` 增加渠道维度校验
  (本地累计渠道用量/成本 vs 渠道 `used_quota`),以及 ledger/log 双写一致性比对。
- **对账差异告警**: `billing-service` 检测到对账差异时通过 gRPC 投递到 `notify-worker`,
  按 `RECON_ALERT_RECIPIENTS` 创建通知;出错仅记日志,不阻塞对账任务。
- **成本健康 dashboard**: 管理后台新增成本/毛利/渠道余额健康面板
  (基于 `reconciliation_runs` + 账本聚合)。
- **缓存 token 用量展示**: 用量统计与账本写入支持 `cache_read_tokens` 字段
  (`migrations/031`),后台与 `/v1/usage` 路径可见缓存命中率相关指标。
- **管理后台 i18n(zh-CN)**: 关键文案本地化。

### Changed
- 管理员日志/用量统计从内存抽样改为调用 billing 真实聚合,数字可信。
- 对账任务支持通过 `WithNotifier` / `WithRecipients` 选项装配通知;
  不配置通知端点时退化为仅日志模式(向后兼容)。

### Fixed
- 修复 relay 流式响应 logger 偶发空指针 panic。
- dashboard token 趋势 Y 轴在数值跨度大时改为紧凑单位显示。

## [0.1.1] - 2026-05-09

### Added
- 渠道余额刷新适配 OpenAI/DeepSeek/OpenRouter/SiliconFlow 等 provider。
- Docker builder stage 增加 `--platform=$BUILDPLATFORM` 多架构支持。
- `admin-api` 支持外部管理前端构建产物托管。

## [0.1.0] - 2026-05-06

首个公开版本,核心微服务边界确立:
- `relay-gateway`、`admin-api`、`identity-service`、`channel-service`、
  `billing-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`。
- OpenAI 兼容 API 网关、用户/Token/额度/账务基本链路、Docker Compose 部署。
