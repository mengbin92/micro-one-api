# Changelog

All notable changes to `micro-one-api` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.3.0] - 2026-06-29

### Added
- **混合中转网关**:relay-gateway 新增 `Adaptor` 抽象层,把 Codex/Claude OAuth 等订阅账号与 30+ 厂商 API Key 通道统一接入,内含 `apicompat` 四格式转换矩阵(Anthropic ⇄ Responses ⇄ ChatCompletions,含流式 SSE 状态机)与 `identity` 指纹/伪装(metadata.user_id 重写、anthropic-beta 计算、fingerprint 注入)。
- **订阅账号端到端**:`subscription_accounts` 表 + 5 个 admin RPC(列表/创建/更新/删除/启停),管理后台新增「订阅账号」页,使用/费用/日志/渠道健康均按 `subscription_account_id` 维度归因;`scripts/import-subscription-creds.py` 一键导入凭据。
- **架构重构 P0**:http.go 从 2,391 行拆分为 `Orchestrator` + `Forwarder` + `Handler` 矩阵 + `http_raw_helpers` + `http_adaptor`;identity/channel/billing/log 四个 gRPC 客户端接入 sony/gobreaker 熔断 + 4 种降级策略(cache/async/noop/identity)。
- **架构重构 P1**:multi-level cache(L1 内存 + L2 Redis)+ singleflight 防击穿;计费改为异步预扣/批量结算;`SelectChannel` 加权轮询 + 失败率衰减;`logs`/`billing_ledgers`/`billing_reservations` 批量写。
- **架构重构 P2**:`logs` 表按月分区(partition cron 持续维护);统一幂等中间件;relay-gateway 优雅排空(graceful drain);gRPC mTLS 服务间认证;审计日志覆盖 admin 写操作。
- **可观测性补齐**:新增 30+ Prometheus 指标,涵盖 Relay/Selector/Cache/Breaker/Billing/Partition 多维度。
- 新增数据库迁移:`034_create_subscription_accounts.sql`、`035_add_subscription_account_quota_fields.sql`、`036-038_add_subscription_account_id_to_*.sql`、`phase1_indexes.sql`、`phase3_partitioning.sql`。
- 依赖:由 `encoding/json` 切换到 `bytedance/sonic` 提升 apicompat 序列化吞吐。

### Changed
- `internal/relay/server/http.go`:从 2,391 行精简到 1,862 行,只保留路由注册 + 中间件装配。
- 鉴权流程:本地 Auth Cache 命中时不再发起 gRPC;Token 状态变更通过 Redis Pub/Sub 广播失效。
- 计费流程:`ReserveQuota` 改为异步队列提交,失败回退到同步路径(降级策略之一)。
- 渠道选择:同优先级内由纯随机改为加权轮询 + 失败率衰减。
- `scripts/deploy.sh` 部署脚本加固,补齐 migrate 镜像调用与回滚路径。

### Fixed
- 订阅账号健康检查 404(`subscription_account_id` 未透传到 channel-svc gRPC)。
- 编辑订阅账号保存崩溃(`null` → `""` 规范化)。
- 成本归因:cost-analysis 页面之前未按订阅账号分桶,现在全链路带上 `subscription_account_id`。
- gRPC 客户端超时在长上下文场景下误杀,改为可配置 `grpc.dial_timeout` / `call_timeout`。
- relay `Content-Length` 校验对流式响应误判 411,改为仅在非流式路径校验。

### Security
- gosec SAST:本次新增代码 0 issues。
- govulncheck SCA:全代码库 0 vulnerabilities。
- gitleaks 密钥扫描:本次新增代码 0 leaks。
- OAuth 凭据存储:At-Rest 加密 + KMS-style 密钥派生;`scripts/import-subscription-creds.py` 在 stdin 接受凭据而非 argv。

## [0.2.9] - 2026-06-26

### Added
- 新增 Codex Responses WebSocket 协议入站支持：relay-gateway 在 `POST /v1/responses` 上探测 `Upgrade: websocket` 请求，自动切换为 WebSocket 双向转发，可直接作为 Codex CLI 的 WebSocket 后端；非 Upgrade 请求仍走原 HTTP/SSE 路径，向后兼容。
- 客户端 ↔ 上游双向 pump 逐帧镜像转发 Codex 事件，按 turn 解析 usage 并复用现有 reserve / commit / release 计费与 usage 日志链路。
- 新增上游连接池：每渠道空闲连接缓存，经 Ping 健康检查后复用，支持每渠道最大连接数（默认 8）与空闲淘汰（默认 5 分钟）。
- 新增跨进程会话粘滞：`response_id → channel_id` 绑定双写本地热缓存与 Redis，多副本部署下保持多轮会话同一渠道；未配置 Redis 时降级为纯内存。
- 新增多渠道故障转移：上游 dial 失败或首字节前的可重试错误自动按优先级换渠道（默认最多切换 2 次），字节已下发即停止 failover。
- 新增 `openai_ws` 配置块（超时、连接池、failover、sticky、Redis），所有字段可选。
- 依赖新增 `github.com/coder/websocket v1.8.14`。

### Security
- gosec SAST：本次新增代码（`openai_ws_*`）0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks 密钥扫描：本次新增代码 0 leaks（全仓 2 条命中为 README/推广文档中的 `YOUR_TOKEN` 占位符，非真实密钥）。

## [0.2.8] - 2026-06-25

### Added
- 新增 Anthropic Messages API 入站端点 `POST /v1/messages`，使 relay-gateway 能直接对接 Claude Code CLI 及原生 Anthropic SDK 客户端。
- 支持 Anthropic Messages 格式与内部 OpenAI 兼容选路/计费链路的双向转换：string/array content blocks、system prompt、tool_use / tool_result 工具调用。
- 流式响应支持 OpenAI SSE → Anthropic SSE 事件序列转换（message_start / content_block_start / content_block_delta / content_block_stop / message_delta / message_stop）。
- 支持 thinking-mode 模型（DeepSeek-R1、GLM-5.x），将 `reasoning_content` 转换为 Anthropic `thinking` content block。
- 鉴权支持 `x-api-key`（Anthropic 原生）和 `Authorization: Bearer` 两种方式。

### Fixed
- 流式 SSE 中途写入错误不再触发二次 `WriteHeader`。
- `Plan()` 失败返回 Anthropic 错误信封格式，而非 OpenAI 格式。
- 新增 `max_tokens` 上限保护（64000），防止资源耗尽。

### Security
- gosec SAST / govulncheck SCA / gitleaks 密钥扫描全部通过（0 issues / 0 vulns / 0 leaks）。

## [0.2.7] - 2026-06-24

### Added
- 通知记录新增最后一次投递失败原因字段，notify-worker 记录发送错误，管理后台通知面板在失败通知中展示失败原因。

## [0.2.7] - 2026-06-24

### Added
- Prometheus 指标补齐对账任务和渠道健康探测观测：新增对账运行次数/耗时/差异类型计数，以及 monitor-worker 渠道健康 sweep/probe 成功率、耗时和失败原因指标。

## [0.2.6] - 2026-06-20

### Added
- 管理后台新增通知面板，支持查看通知历史、按发送状态筛选、刷新列表和 pending 数量徽标。
- 管理后台新增渠道健康与成本分析页面，补齐健康趋势、成本、收入、毛利等可视化图表组件。

### Fixed
- 管理后台通知接口改为经 `admin-api` 代理到 `notify-worker`，避免前端直接依赖 worker 地址。
- 通知面板兼容 `notify-worker` 直接返回 `{items,total}` 的列表响应格式。
- `admin-api` 补齐 `/admin/channel-health`、`/admin/cost-analysis` 等 SPA 路由，修复刷新或直达页面时的路由回退问题。

## [0.2.5] - 2026-06-19

### Added
- `notify-worker` 新增企业 IM 通知通道：企业微信、钉钉、飞书/Lark 和 Slack。
- 渠道健康告警与对账告警的通知类型支持 `wecom`、`dingtalk`、`feishu`、`slack`。
- 新增 `NOTIFY_WECOM_WEBHOOK_URL`、`NOTIFY_DINGTALK_WEBHOOK_URL`、
  `NOTIFY_FEISHU_WEBHOOK_URL`、`NOTIFY_SLACK_WEBHOOK_URL` 配置项。

### Changed
- 企业微信和钉钉支持配置完整 webhook URL，也支持仅配置 key / access_token 自动拼接。
- 部署文档、README 与示例环境变量补齐企业 IM 告警配置说明。

## [0.2.4] - 2026-06-19

### Added
- 补充渠道健康告警配置文档与 docker-compose 示例环境变量说明。

### Changed
- README 最新发布指针更新到 v0.2.4。

## [0.2.3] - 2026-06-18

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
