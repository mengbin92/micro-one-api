# micro-one-api

[简体中文](./README.md) | [English](./README.en.md)

![Micro-One-API logo](./docs/assets/micro-one-api-logo-wordmark.svg)

`micro-one-api` 是一个基于 Go Kratos 的多服务 AI API 网关与管理系统。项目参考了 [one-api](https://github.com/songquanpeng/one-api) 的多渠道 OpenAI API 分发思路，也参考了 [sub2api](https://github.com/Wei-Shaw/sub2api) 对订阅额度窗口、账号池、限流和用量管理的设计方向，并将核心能力拆分为更清晰的微服务边界。

本项目面向需要统一管理多个上游模型供应商、钱包余额、访问令牌、账务和运营后台的场景。它不是上游服务的替代品，也不提供任何第三方模型账号、订阅或 API Key。

> 📣 **最新发布**：[v0.37.0 发布公告](./docs/releases/release-v0.37.0.md)（注册与 MFA、服务档位计价、微信 Native 充值及首轮 12 项修复；新增迁移 143–145，强制认证与新收费默认关闭） · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.37.0)

## 功能概览

- OpenAI 兼容 API 网关：支持 `/v1/chat/completions`、`/v1/models`、`/v1/responses` 以及 embeddings、audio、image、moderations 等 raw relay 路由。
- 多渠道模型分发：支持渠道优先级、同优先级负载均衡、禁用渠道过滤、模型白名单和模型映射。
- 多供应商适配：支持 OpenAI-compatible 渠道，并包含 Anthropic、Gemini、Azure、VoyageAI 等 provider 适配器；DeepSeek、Moonshot、Groq、Tongyi、OpenRouter、SiliconFlow、Ollama、Doubao 等按 OpenAI-compatible 方式转发。
- 用户与令牌管理：支持用户鉴权、令牌状态、过期时间、余额检查、模型权限和用户角色控制。
- 钱包与账务：提供金额预扣、释放、结算、ledger、兑换码、支付订单和用量记录等能力。
- 订阅套餐与用量查询：支持订阅套餐购买、续费、退款/冲正、购买时套餐快照，以及 API Key 鉴权的 `/v1/subscription/usage` 查询（额度、已用量、剩余额度和下次刷新时间）。
- 订阅账号治理：支持订阅账号本地额度、fixed daily/weekly 重置、账号恢复、额度告警、单用户单 active 订阅约束和多副本幂等治理记录。
- 模型管理系统：提供独立的模型管理后台，支持模型映射、通配符匹配、别名和使用统计；渠道或上游订阅账号禁用后同步扣减可用来源，来源归零后模型同步禁用，保留模型手动开关。详见[模型管理设计](docs/model-management-design.md)。
- 国内订阅账户支持：新增对智谱 GLM、MiniMax、Kimi 等国内平台订阅账户的支持，包括动态模型发现、配额状态查询、路由恢复探测等能力。
- 成本与利润分析：`billing_ledgers` 记录上游成本，账本聚合支持收入/成本/毛利维度，可按模型、渠道、用户、Token、时间下钻。
- 多维用量聚合：用量统计改为 SQL `GROUP BY` 聚合（按用户/渠道/模型/Token/分组/小时|日），告别 admin 抽样估算。
- 对账与告警：`RunReconciliation` 覆盖账户余额、渠道用量、ledger/log 双写一致性；差异通过 `notify-worker` 投递通知（可配置收件人）。
- 成本健康 dashboard：管理后台展示成本/毛利/渠道余额健康指标；用量统计与账本支持缓存 token（`cache_read_tokens`）与命中率展示。
- 管理后台：提供 React/Vite 前端和 `admin-api` BFF，用于管理用户、令牌、渠道、订阅套餐、订单、兑换码、用量和系统配置。
- 监控与日志：提供健康检查、Prometheus metrics、业务日志聚合、监控 worker 和通知 worker；对账任务与渠道健康探测暴露运行次数、耗时和失败原因指标。
- 部署形态：支持本地开发、Docker Compose 和 Kubernetes 部署。

## 界面预览

以下截图来自测试账号或经过脱敏的管理页面。API Key 未展示；渠道名称、用户标识和账务流水已替换为演示内容，运营金额已替换或模糊处理。

| 用户仪表盘 | Token 管理 |
|:---:|:---:|
| ![用户仪表盘](./docs/assets/screenshots/user-dashboard.png) | ![Token 管理](./docs/assets/screenshots/token-management.png) |
| 用量记录 | 充值与订阅套餐 |
| ![用量记录](./docs/assets/screenshots/usage-records.png) | ![充值与订阅套餐](./docs/assets/screenshots/subscription-plans.png) |
| 渠道健康监控 | 成本分析 |
| ![渠道健康监控](./docs/assets/screenshots/channel-health.png) | ![成本分析](./docs/assets/screenshots/cost-analysis.png) |
| 账务日志 | |
| ![脱敏后的账务日志](./docs/assets/screenshots/billing-logs.png) | |

完整的 Lite 操作演示（登录、创建渠道、创建 Token、在线调试）见
[脱敏演示视频](./docs/assets/demos/lite-quickstart.mp4)；演示使用隔离测试数据和本地 mock 上游。

## 适合谁 / 不适合谁

| 适合 | 不适合 |
|------|--------|
| 需要自托管统一入口，管理多个 OpenAI-compatible 或专用 provider 渠道的团队 | 只需要单机、单用户、单上游转发，希望一个二进制零配置启动的场景 |
| 需要用户、Token、钱包、订阅、账务、用量、成本和运营后台的内部平台或 SaaS 团队 | 希望项目直接提供上游模型账号、订阅、API Key 或代替第三方模型服务的用户 |
| 需要 Docker Compose / Kubernetes 部署，并希望按服务边界扩展和审计的工程团队 | 不准备维护 Redis 与多服务部署的零运维个人用途 |
| 已获得上游服务合法授权，需要在组织内部做统一鉴权、调度和成本治理的使用者 | 试图绕过上游访问限制、账号规则、计费规则或服务条款的用途 |

## 架构

```mermaid
flowchart LR
    Client[SDK / 应用] --> Relay[relay-gateway]
    Admin[Web 管理后台] --> BFF[admin-api]
    Relay --> Identity[identity-service]
    Relay --> Channel[channel-service]
    Relay --> Billing[billing-service]
    Relay --> Upstream[上游模型服务]
    BFF --> Identity
    BFF --> Channel
    BFF --> Billing
    BFF --> Log[log-service]
    Monitor[monitor-worker] --> Channel
    Billing --> Notify[notify-worker]
```

请求路径以 `relay-gateway` 为入口，鉴权、渠道选择和账务结算分别由对应服务负责；管理前端通过 `admin-api` 聚合管理接口，监控与通知 worker 处理异步运营任务。

当前仓库按 Kratos 服务边界组织：

| 服务 | 职责 |
|------|------|
| `relay-gateway` | OpenAI 兼容 HTTP 网关，负责鉴权、选路、金额预扣、上游转发和响应透传 |
| `admin-api` | 管理端 BFF，并托管或代理管理前端静态资源 |
| `identity-service` | 用户、角色、登录鉴权、Token 校验与权限判断 |
| `channel-service` | 渠道、模型、分组、优先级和可用渠道选择 |
| `billing-service` | 钱包余额、账务流水、兑换码、支付订单和扣费结算 |
| `config-service` | 动态配置管理 |
| `log-service` | 业务日志写入、查询和删除代理 |
| `monitor-worker` | 监控任务与告警触发 |
| `notify-worker` | 通知发送 |

目录结构：

| 目录 | 说明 |
|------|------|
| `api/` | gRPC、HTTP 和 OpenAPI 相关 proto 定义及生成代码 |
| `app/` | 子服务大仓（admin/billing/channel/config/identity/log/monitor/notify），各含 `cmd/`、`internal/`、`configs/`、`Dockerfile`、`Makefile` |
| `cmd/` | 根服务入口（`relay-gateway/`、`migrate/`、`admin-reset/`） |
| `configs/` | relay-gateway 配置文件 |
| `internal/` | relay-gateway 业务实现和 `conf` 配置 proto |
| `domain/` | 共享域库（`subscription` 订阅域、`upstream` 上游 provider），跨服务嵌入 |
| `platform/` | 基础设施层（cache/database/grpc/http/logging/metrics/middleware/registry/security/tls/tracing/websocket） |
| `pkg/` | 纯工具包（errors/safecast/safefile/timeout 等） |
| `migrations/` | MySQL schema 迁移 |
| `web/` | 管理后台前端 |
| `deployments/` | Docker、Docker Compose 和 Kubernetes 部署文件 |
| `scripts/` | 构建脚本、部署脚本、架构检查脚本 |
| `docs/` | 部署运维文档 |
| `test/` | 集成测试和端到端测试 |

## 快速开始

### 个人单机：SQLite Lite

从 [SQLite Lite Quickstart](./docs/quickstart-lite.md) 开始：生成密钥、启动迁移与服务、
取回初始管理员密码、创建渠道和 Token，再发出首个聊天请求。Lite 使用 SQLite + Redis，
保留现有服务边界；默认后台 `127.0.0.1:3000`、Relay `127.0.0.1:8080`，端口可配置。
首次源码构建时间另计。指南包含钱包充值、备份、恢复与升级边界。

### Docker Compose（MySQL）

适合开发、测试和功能验收。

```bash
cd deployments/docker-compose
cp .env.example .env
# 编辑 .env，至少替换数据库、Redis 和服务密钥
docker compose up -d
```

默认需要在 `deployments/docker-compose/.env` 或环境变量中提供：

- `MYSQL_ROOT_PASSWORD`
- `DATABASE_DSN`
- `REDIS_PASSWORD`
- `JWT_SECRET_KEY`
- `SERVICE_TOKEN`
- `ADMIN_TOKEN`

服务启动后可访问：

- 管理后台：`http://localhost:3000`
- Relay API：`http://localhost:8080`
- 健康检查：`http://localhost:8080/healthz`

首次启动时，如果 `users` 表为空，`identity-service` 会创建初始 root 管理员。可通过 `INITIAL_ADMIN_USERNAME`、`INITIAL_ADMIN_EMAIL`、`INITIAL_ADMIN_PASSWORD` 指定；未设置密码时会生成随机密码。生产环境应设置 `INITIAL_ADMIN_PASSWORD_FILE`，服务会把随机密码写入该 0600 私有文件，不会在日志中打印明文。

### 从空环境到首个渠道和 Token

完整操作见 [Quickstart：登录、渠道、钱包与 Token](./docs/quickstart-lite.md#2-登录与创建渠道)。
MySQL 部署启动后的页面操作相同；先验证 `/v1/models`，再完成一次
`/v1/chat/completions` 请求并检查用量记录。

### 本地开发

生成 proto 和构建：

```bash
make proto
make build
```

运行核心三服务的内存/本地链路：

```bash
make run-all
```

手动运行单个服务：

```bash
make run-identity
make run-channel
make run-relay
```

构建管理前端：

```bash
make web-dist
```

完整部署说明见 [docs/deployment.md](./docs/deployment.md)。

## 发布与升级

- [v0.37.0](./docs/releases/release-v0.37.0.md)：注册/邮件恢复、TOTP 与首批 step-up、服务档位计价、微信 Native 余额充值及第一轮 12 项修复。新增 Identity 143／Billing 144／Log 145；强制认证与新收费默认关闭，A3 完整生产上线和真实外部验收分别保留。
- [v0.36.6](./docs/releases/release-v0.36.6.md)：修复上游供应商漏报、扫描停滞与有界复核例外，保留历史账单补证及开发依赖安全修复；无新增迁移，生产证据监控保持停用。
- [v0.36.5](./docs/releases/release-v0.36.5.md)：Identity 142 持久发布 builtin root 三项契约写权限，修正 IAM 验收期望；渠道 11 两次真实探测通过并进入 1% 灰度，付费目标已撤销。
- [v0.36.4](./docs/releases/release-v0.36.4.md)：上游契约类型检查、首次灰度基线、DeepSeek 独立证明与到期能力，供应商导入/历史费用和安全修复。新增 Identity 140／Billing 141；本地验证及 Release workflow 已通过，实际契约试点另随 v0.36.5 留证。
- [v0.36.3](./docs/releases/release-v0.36.3.md)：最大计时摘要 SQL 内存修复、供应商证据详情与 Jaeger 768 MiB 配置。无新增迁移；已部署生产，发版不重复重启，长期验收独立保留。
- [v0.36.2](./docs/releases/release-v0.36.2.md)：计时 SQL 分组写入、分页修复、Q3 基准恢复与原生构建。新增 Log 139 索引迁移；上线先 Log、后 Relay，生产观察保持原窗口。
- [v0.36.1](./docs/releases/release-v0.36.1.md)：普通 WS 长连接、Lite 首请求与导出告警修复；原生性能和空卷恢复验收。无新增迁移；生产 48 小时观察保持原窗口。
- [v0.36.0](./docs/releases/release-v0.36.0.md)：LLM 请求计时观测、瀑布追踪、账单关联和完整性判定。新增 Log 137／Billing 138 迁移；按说明先迁移，再更新 Relay、Log、Billing、Admin 与独立前端。
- [v0.35.2](./docs/releases/release-v0.35.2.md)：模型定价原子保存、并发版本保护及撤权草稿清理。

完整历史见 [CHANGELOG](./CHANGELOG.md) 和 [发布公告索引](./docs/README.md#releases--版本发布公告)。
升级前阅读目标版本的迁移、配置和回滚步骤；主生产 Compose 的前端由宿主机独立挂载，更新 Admin 镜像不会更新前端。

## API 示例

### 健康检查

```bash
curl http://localhost:8080/healthz
```

### 模型列表

```bash
curl -H "Authorization: Bearer ${API_TOKEN}" \
  http://localhost:8080/v1/models
```

### 订阅用量查询

```bash
curl -H "Authorization: Bearer ${API_TOKEN}" \
  http://localhost:8080/v1/subscription/usage
```

该接口返回当前用户订阅状态、日/周/月额度、已用量、剩余额度和下次刷新时间。详细字段见 [docs/design/subscription-usage-api.md](./docs/design/subscription-usage-api.md)。

### Chat Completions

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello"}
    ]
  }'
```

流式响应：

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello streaming"}
    ],
    "stream": true
  }'
```

## 配置要点

常用环境变量：

| 变量 | 说明 |
|------|------|
| `CONF_PATH` | 服务配置文件路径，例如 `configs/config.yaml` |
| `MODELS_PATH` | relay-gateway 模型映射配置路径；本地可设为 `configs/models.yaml` |
| `DATABASE_DSN` | MySQL 连接字符串 |
| `REDIS_ADDR` / `REDIS_PASSWORD` | Redis 地址与密码 |
| `JWT_SECRET_KEY` | 用户登录和鉴权相关密钥 |
| `SERVICE_TOKEN` | 服务间 HTTP 调用令牌 |
| `ADMIN_TOKEN` | 管理 API 兼容鉴权令牌 |
| `LOG_MEMORY_MODE` | 允许 log-service 无数据库时使用内存日志，仅用于开发/测试 |
| `LOG_RETENTION_DAYS` | log-service 业务日志保留天数 |
| `IDENTITY_GRPC_ENDPOINT` | identity-service gRPC 地址 |
| `CHANNEL_GRPC_ENDPOINT` | channel-service gRPC 地址 |
| `BILLING_GRPC_ENDPOINT` | billing-service gRPC 地址 |
| `RELAY_HTTP_ADDR` | relay-gateway HTTP 监听地址 |
| `RELAY_GRPC_ADDR` | relay-gateway gRPC 监听地址；默认 `0.0.0.0:9003` |
| `RELAY_PROVIDER_TIMEOUT` | 上游 provider 请求超时 |
| `CHANNEL_HEALTH_FAILURE_THRESHOLD` / `CHANNEL_HEALTH_COOLDOWN` | 渠道自动熔断阈值和冷却时间；默认连续 3 次上游失败后跳过 5 分钟 |
| `CHANNEL_HEALTH_CHECK_ENABLED` / `CHANNEL_HEALTH_CHECK_INTERVAL` / `CHANNEL_HEALTH_CHECK_TIMEOUT` | monitor-worker 定时渠道 `/models` 健康探测开关、间隔和单次超时 |
| `CHANNEL_HEALTH_ALERT_ENABLED` | 渠道健康状态首次进入 `unavailable` 时是否投递通知 |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | 渠道不可用告警通知类型，支持 `event` / `webhook` / `email` / `wecom` / `dingtalk` / `feishu` / `slack` |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | 渠道不可用告警目标，JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱，IM 通道留空走对应配置 |
| `SUBSCRIPTION_USER_RPM_LIMIT` | relay 订阅用户 RPM 限制；默认 `0` 表示关闭，避免无配置时误限流 |
| `RATE_LIMIT_REQUESTS_PER_SECOND` / `RATE_LIMIT_BURST` | 网关限流参数 |
| `CORS_ALLOWED_ORIGINS` | CORS 允许来源，逗号分隔；空值默认拒绝跨域请求，生产必须配置真实 HTTPS 来源 |
| `ADMIN_WEB_ROOT` | admin-api 使用的外部前端构建目录 |
| `NOTIFY_GRPC_ENDPOINT` | channel 健康告警和 billing 对账告警投递目标（notify-worker gRPC）；留空则不投递通知 |
| `RECON_ALERT_ENABLED` | 是否启用对账差异告警（`true`/`false`） |
| `RECON_ALERT_NOTIFY_TYPE` | 对账告警通知类型，支持 `event` / `webhook` / `email` / `wecom` / `dingtalk` / `feishu` / `slack` |
| `RECON_ALERT_RECIPIENTS` | 对账告警目标，JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱，IM 通道留空走对应配置 |
| `RECON_ALERT_INTERVAL` | 对账任务执行间隔，例如 `1h`、`30m` |
| `NOTIFY_WEBHOOK_URL` | notify-worker 默认 webhook 投递地址 |
| `NOTIFY_SMTP_HOST` / `NOTIFY_SMTP_PORT` / `NOTIFY_SMTP_USER` / `NOTIFY_SMTP_PASS` / `NOTIFY_SMTP_FROM` | notify-worker 邮件配置；Identity 注册/恢复可整组复用 |
| `IDENTITY_PUBLIC_BASE_URL` | 注册/恢复邮件必填的控制台公网 URL，用于生成重置链接 |
| `IDENTITY_SMTP_HOST` / `PORT` / `USER` / `PASS` / `FROM` / `MODE` | 可选独立 Identity SMTP 组；设置 HOST 后整组使用，不混用 Notify 凭据 |
| `REGISTRATION_EMAIL_VERIFICATION_ENABLED` | 是否强制邮箱验证，默认 `false`；邀请奖励始终要求邮箱证明 |
| `TURNSTILE_CHECK_ENABLED` / `TURNSTILE_SITE_KEY` / `TURNSTILE_SECRET` | 可选注册安全验证；启用时 site key 与 secret 均须配置 |

Prometheus 指标通过各服务 `/metrics` 暴露。订阅系统新增 `micro_one_api_subscription_quota_checks_total`、`micro_one_api_subscription_usage_records_total`，relay 订阅账号路径新增 `micro_one_api_relay_subscription_adaptor_requests_total`、`micro_one_api_relay_subscription_failover_total`、`micro_one_api_relay_runtime_blocks_total`、`micro_one_api_relay_upstream_passthrough_total`、`micro_one_api_relay_codex_quota_snapshots_total`、`micro_one_api_relay_codex_quota_used_percent`。v0.6.0 起，订阅账号治理还暴露额度重置、账号恢复和额度告警相关指标，详见 [docs/runbooks/subscription-account-ops-runbook.md](./docs/runbooks/subscription-account-ops-runbook.md)。

更多配置见 [.env.example](./.env.example) 和 [docs/deployment.md](./docs/deployment.md)。

## 测试

```bash
# 单元测试和不依赖外部服务的集成测试
make test

# 测试自检：确认关键规则故意改坏后，指定测试确实失败
make test-health

# 指定模块测试
make dev-test-identity
make dev-test-channel
make dev-test-provider

# Docker Compose 端到端测试
make test-e2e
```

`make test-health` 在临时副本逐项变异生产逻辑，保留基线/变异日志与 JSON 报告；每周 CI 运行一次。首次检出需先 `make init proto` 生成构建输入，详见[测试自检手册](./docs/runbooks/test-health-self-check.md)。

## 安全提醒

- 生产环境必须替换 `JWT_SECRET_KEY`、`SERVICE_TOKEN`、`ADMIN_TOKEN`、数据库密码和 Redis 密码。
- 不要将真实上游 API Key、订阅凭证、支付私钥或管理员密码提交到仓库。
- 建议生产环境开启 HTTPS，并按需启用 mTLS、IP 过滤、限流和密钥轮换。
- 管理后台和 Relay API 应分别配置访问控制，不建议直接裸露在公网。
- 使用第三方模型、API、订阅或账号池时，应确认你拥有合法授权，并遵守对应服务条款。

## 免责声明

本项目仅作为 AI API 网关、渠道调度、钱包账务管理和微服务架构实践工具提供。使用者应自行确保部署、配置、账号来源、API Key 来源、调用内容、支付能力和数据处理行为符合适用法律法规及第三方服务条款。

项目维护者不提供任何第三方模型服务、订阅账号、API Key 或绕过访问限制的能力，也不对使用者因滥用、违规接入、账号封禁、额度损失、数据泄露、业务中断或其他后果承担责任。

完整免责声明见 [DISCLAIMER.md](./DISCLAIMER.md)。

## 致谢

- [one-api](https://github.com/songquanpeng/one-api)：提供了多渠道 OpenAI API 管理与分发系统的设计参考。
- [sub2api](https://github.com/Wei-Shaw/sub2api)：提供了订阅转 API、账号池、订阅额度窗口和限流管理等场景参考。
- [go-kratos/kratos](https://github.com/go-kratos/kratos)：提供了微服务框架与工程实践基础。

## 许可证

本项目使用 [MIT License](./LICENSE)。
