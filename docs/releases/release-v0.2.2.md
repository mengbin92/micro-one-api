# Micro-One-API v0.2.2 发布公告

> 2026-06-15 · 上一版: [v0.2.1](./release-v0.2.1.md) (2026-06-12)

v0.2.2 是一个运维可靠性补丁版，重点补齐 notify-worker 的实际投递闭环，并修正对账告警配置语义。无数据库迁移、无破坏性 API 变更。

## 亮点

- **notify-worker 实际投递**：pending 通知现在会被后台 dispatcher 扫描并发送。
- **Webhook / Email 支持**：`event` / `webhook` 通过 HTTP POST 投递，`email` 通过 SMTP 投递。
- **失败重试闭环**：投递失败会递增 `retry_count`，达到 `NOTIFY_MAX_RETRY` 后标记为 `failed`。
- **对账告警配置修正**：`RECON_ALERT_NOTIFY_TYPE` 控制通知类型，`RECON_ALERT_RECIPIENTS` 明确为 webhook URL / email 目标语义。
- **全服务 Docker 构建覆盖**：CI Docker matrix 覆盖 README 声明的全部服务镜像。

## 配置变化

新增或明确以下环境变量：

| 变量 | 说明 |
|---|---|
| `RECON_ALERT_NOTIFY_TYPE` | 对账告警类型：`event` / `webhook` / `email` |
| `RECON_ALERT_RECIPIENTS` | JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱 |
| `NOTIFY_WEBHOOK_URL` | notify-worker 默认 webhook 投递地址 |
| `NOTIFY_SMTP_HOST` / `NOTIFY_SMTP_PORT` / `NOTIFY_SMTP_USER` / `NOTIFY_SMTP_PASS` / `NOTIFY_SMTP_FROM` | SMTP 邮件投递配置 |
| `NOTIFY_DISPATCH_INTERVAL` / `NOTIFY_DISPATCH_BATCH` / `NOTIFY_MAX_RETRY` | pending 通知扫描、批量和重试配置 |

Webhook 示例：

```bash
RECON_ALERT_NOTIFY_TYPE=event
RECON_ALERT_RECIPIENTS=[""]
NOTIFY_WEBHOOK_URL=https://hooks.example.com/micro-one-api
```

Email 示例：

```bash
RECON_ALERT_NOTIFY_TYPE=email
RECON_ALERT_RECIPIENTS=["ops@example.com"]
NOTIFY_SMTP_HOST=smtp.example.com
NOTIFY_SMTP_PORT=587
NOTIFY_SMTP_USER=ops@example.com
NOTIFY_SMTP_PASS=change-me
NOTIFY_SMTP_FROM=ops@example.com
```

## 升级说明

- 无需执行数据库迁移。
- 使用 Docker Compose 的部署请同步 `.env.example` 中的 notify 配置项。
- 如果之前使用 `RECON_ALERT_RECIPIENTS=[admin]` 这类用户名配置，请改为 webhook URL、邮箱，或留空并配置 `NOTIFY_WEBHOOK_URL`。

## 验证

本次发版前已通过：

```bash
go test ./internal/notify/... ./internal/billing/biz ./cmd/billing-service ./test/integration
make test
GOCACHE=/private/tmp/micro-one-api-gocache CGO_ENABLED=0 go build ./cmd/...
git diff --check
```
