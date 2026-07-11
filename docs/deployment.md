# 部署运维文档

## 1. 架构概览

Micro-One-API 由 9 个微服务组成：

| 服务 | HTTP 端口 | gRPC 端口 | 职责 |
|------|----------|----------|------|
| relay-gateway | 8080 | - | OpenAI 兼容 API 网关 |
| admin-api | 3000 | 9000 | 管理端 BFF |
| identity-service | 8001 | 9001 | 用户认证与鉴权 |
| channel-service | 8002 | 9002 | 渠道管理与路由 |
| billing-service | 8004 | 9004 | 钱包账务 |
| config-service | 8005 | 9005 | 动态配置管理 |
| log-service | 8006 | 9006 | 日志聚合 |
| monitor-worker | 8007 | 9007 | 监控与告警 |
| notify-worker | 8008 | 9008 | 通知服务 |

基础设施依赖：
- MySQL 8.0 — 主存储
- Redis 7 — 缓存与分布式限流

## 2. Docker Compose 部署（开发/测试）

### 2.1 前置条件

- Docker 20.10+
- Docker Compose v2+

### 2.2 启动

```bash
cd deployments/docker-compose
cp .env.example .env
# 编辑 .env，至少替换数据库、Redis 和服务密钥

# 启动全部服务
docker compose up -d

# 查看服务状态
docker compose ps

# 查看日志
docker compose logs -f relay-gateway
```

### 2.3 验证

```bash
# 健康检查
curl http://localhost:8080/healthz  # relay-gateway
curl http://localhost:3000/healthz  # admin-api
curl http://localhost:8001/healthz  # identity-service
curl http://localhost:8004/healthz  # billing-service

# Prometheus metrics
curl http://localhost:8080/metrics
curl http://localhost:8004/metrics

# 测试 chat completions
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'
```

### 2.4 停止

```bash
docker compose down           # 停止服务
docker compose down -v        # 停止并删除数据卷
```

## 3. Kubernetes 部署（生产）

### 3.1 前置条件

- Kubernetes 1.25+
- kubectl 配置完成
- Nginx Ingress Controller 已安装
- MySQL 和 Redis 可用（集群内或外部）

### 3.2 创建命名空间

```bash
kubectl create namespace one-api
```

### 3.3 创建 Secrets

```bash
# MySQL 连接 DSN
kubectl create secret generic db-secret \
  --from-literal=dsn='root:password@tcp(mysql:3306)/oneapi?parseTime=true' \
  -n one-api

# Admin API Basic Auth
htpasswd -c auth admin
kubectl create secret generic admin-basic-auth \
  --from-file=auth \
  -n one-api

# TLS 证书
kubectl create secret tls api-tls-secret \
  --cert=api.yourdomain.com.crt \
  --key=api.yourdomain.com.key \
  -n one-api

# 服务间 HTTP 调用令牌（admin-api 和 log-service 共享）
kubectl create secret generic service-token-secret \
  --from-literal=token='replace-with-a-long-random-token' \
  -n one-api
```

### 3.4 部署服务

```bash
# 部署一期服务（5 个核心服务）
kubectl apply -f deployments/k8s/deployment.yaml
kubectl apply -f deployments/k8s/identity-service.yaml
kubectl apply -f deployments/k8s/channel-service.yaml
kubectl apply -f deployments/k8s/billing-service.yaml
kubectl apply -f deployments/k8s/admin-api.yaml

# 部署二期服务
kubectl apply -f deployments/k8s/phase2-services.yaml

# 部署 Ingress
kubectl apply -f deployments/k8s/ingress.yaml
```

### 3.5 验证

```bash
# 检查 Pod 状态
kubectl get pods -n one-api

# 检查服务
kubectl get svc -n one-api

# 检查 Ingress
kubectl get ingress -n one-api

# 查看日志
kubectl logs -f deployment/relay-gateway -n one-api
```

## 4. 环境变量配置

### 4.1 通用环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `CONF_PATH` | 配置文件路径 | 容器内为 `/configs/config.yaml`；本地为 `app/<service>/configs/config.yaml` |
| `DATABASE_DSN` | MySQL 连接字符串 | - |
| `REDIS_ADDR` | Redis 地址 | - |
| `LOG_LEVEL` | 日志级别 | `info` |
| `LOG_FORMAT` | 日志格式 (json/text) | `json` |
| `SERVICE_TOKEN` | 服务间 HTTP 调用令牌；admin-api 访问 log-service 详情与清理接口时使用 | - |
| `LOG_MEMORY_MODE` | 允许 log-service 在无数据库 DSN 时使用内存日志仓库；仅用于开发/测试 | `false` |
| `LOG_RETENTION_DAYS` | log-service 业务日志保留天数，`0` 表示不自动清理 | `30` |
| `LOG_GRPC_AUTH` | 是否启用 log-service gRPC 服务令牌鉴权；启用前需确保客户端会发送 Bearer token | `false` |

### 4.2 Relay Gateway 专用

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `IDENTITY_GRPC_ENDPOINT` | identity-service gRPC 地址 | - |
| `CHANNEL_GRPC_ENDPOINT` | channel-service gRPC 地址 | - |
| `BILLING_GRPC_ENDPOINT` | billing-service gRPC 地址 | - |
| `CHANNEL_HEALTH_FAILURE_THRESHOLD` | 渠道连续失败熔断阈值 | `3` |
| `CHANNEL_HEALTH_COOLDOWN` | 渠道熔断冷却时间，过期后允许半开重试 | `5m` |
| `CHANNEL_HEALTH_CHECK_ENABLED` | monitor-worker 是否定时探测渠道 `/models` | `true` |
| `CHANNEL_HEALTH_CHECK_INTERVAL` | 定时渠道健康探测间隔 | `5m` |
| `CHANNEL_HEALTH_CHECK_TIMEOUT` | 单个渠道健康探测超时 | `10s` |
| `CHANNEL_HEALTH_ALERT_ENABLED` | 渠道首次进入 `unavailable` 时是否投递通知 | `false` |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | 告警类型：`event` / `webhook` / `email` | `event` |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱 | `[""]` |
| `RATE_LIMIT_REQUESTS_PER_SECOND` | 每秒请求数限制 | `100` |
| `RATE_LIMIT_BURST` | 突发请求上限 | `200` |
| `CORS_ALLOWED_ORIGINS` | CORS 允许的源 | - |

### 4.3 Admin API 专用

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `IDENTITY_GRPC_ENDPOINT` | identity-service gRPC 地址 | - |
| `CHANNEL_GRPC_ENDPOINT` | channel-service gRPC 地址 | - |
| `BILLING_GRPC_ENDPOINT` | billing-service gRPC 地址 | - |
| `LOG_HTTP_ENDPOINT` | log-service HTTP 地址；未配置时 `/api/log/{id}` 详情与 `/api/log/` 清理保持禁用兼容响应 | - |
| `ADMIN_WEB_ROOT` | 管理前端静态文件目录；目录内必须有 `index.html`，未配置或不可用时使用二进制内嵌资源 | - |

#### 管理前端静态资源

`admin-api` 默认继续使用编译进二进制的前端资源，适合单二进制或镜像内置前端的部署方式。

如果需要前端独立更新，可将 `ADMIN_WEB_ROOT` 指向外部构建目录，例如：

```bash
make web-dist
ADMIN_WEB_ROOT=/srv/micro-one-api/web/dist ./admin-api
```

运行中的 `admin-api` 会直接从该目录读取 `index.html`、`assets/*`、`favicon.svg` 等文件。替换目录内的前端构建产物后，无需重新编译 Go 后端；浏览器刷新即可加载新的 Vite hash 资源。若目录不存在或缺少 `index.html`，服务会自动回退到内嵌资源。

Docker Compose 部署已将宿主机 `web/dist` 挂载到容器 `/web`，并设置 `ADMIN_WEB_ROOT=/web`。使用该模式时先执行：

```bash
make web-dist
cd deployments/docker-compose
docker-compose up -d admin-api
```

后续只更新前端时，重新执行 `make web-dist` 并刷新浏览器即可；必要时可 `docker-compose restart admin-api`。

#### 日志代理前置条件

`/api/log/{id}` 的 `GET` 和 `/api/log/` 的 `DELETE` 都走 `log-service` HTTP 代理。`GET` 用于查看单条业务日志详情，`DELETE` 仅删除 `log-service` 中的业务日志，并要求传入 `end_time`。这些操作都不会删除 `billing-service` 的 ledger/账务流水。

**启用该能力的硬性前置条件（缺一不可）：**

1. `admin-api` 必须配置 `LOG_HTTP_ENDPOINT`，指向 `log-service` 的 HTTP 地址。
2. `admin-api` 与 `log-service` 必须配置**相同的** `SERVICE_TOKEN`。

未满足上述条件时：
- `admin-api` 启动会输出 `log service proxy disabled: missing [...]` 的 WARN 日志；
- 路由保持注册，但实际调用返回 `501 NotImplemented` 的稳定占位响应（`{"success":false,"message":"log detail is not configured"}` 或 `{"success":false,"message":"log delete is not configured"}`）。

#### 订阅套餐支付与支付宝回调

用户在订阅套餐页自助购买时，前端优先提交 `plan_id` 创建 `asset_type=subscription` 的支付订单。生产环境应使用 `channel=alipay`，不能回落到 `mock`；`mock` 只会创建未支付订单，不会跳转到支付宝。

`subscription_groups` 仍然表示权益和日/周/月 USD 限额；`subscription_plans` 表示可购买商品，一个分组可以挂多个套餐。`payment_orders.plan_id` 记录套餐 ID，`payment_orders.group_id` 记录套餐所属分组，`asset_amount` 记录下单时的有效天数快照。支付成功后 `billing-service` 会按 `plan_id` 自动发放或续期订阅，并写入 `user_subscriptions`。旧的按 `group_id` 购买路径仍兼容；如果订阅订单缺少可用的 `plan_id`/`group_id`，或 `billing-service` 未接入订阅发放器，订单不会被标记为 `paid/issued`，避免出现“订单已支付但我的订阅为空”的脏状态。

用户已有 active 订阅时，同一分组的套餐会在原 `expires_at` 后续期；不同分组仍会被拒绝，以保持当前计费链路“一名用户同一时间一个 active subscription”的假设。

#### 上游订阅账号本地额度

`subscription_accounts` 支持账号级本地额度：`quota_limit_usd`（总额）、`quota_daily_limit_usd`（滚动 24h）、`quota_weekly_limit_usd`（滚动 7d）和 `rate_multiplier`。额度单位为 USD，limit 为 0 表示不限制。relay-gateway 成功提交计费后会按实际 `committed_amount` 折算 USD 并回写账号用量；channel-service 选路会跳过本地额度已耗尽的账号。管理端订阅账号页面可查看/编辑这些字段，并可重置 total/daily/weekly/all 用量。

启用支付宝前，部署环境至少需要配置：

| 变量 | 说明 |
|------|------|
| `ALIPAY_ENABLED` | 必须为 `true` 才会注册真实支付宝支付渠道 |
| `ALIPAY_FORM_URL` | 支付宝网关地址；沙箱通常为 `https://openapi-sandbox.dl.alipaydev.com/gateway.do` |
| `ALIPAY_APP_ID` | 支付宝应用 ID |
| `ALIPAY_PRIVATE_KEY_PATH` | 应用私钥 PEM 文件路径，容器内路径需可读 |
| `ALIPAY_PUBLIC_KEY_PATH` | 支付宝公钥 PEM 文件路径，容器内路径需可读 |
| `ALIPAY_RETURN_URL` | 用户支付完成后的同步返回页面 |
| `ALIPAY_NOTIFY_URL` | 支付宝异步通知 URL，必须允许公网 `POST` 到达应用 |

回调路由有两种可选接法：

1. 直接把 `ALIPAY_NOTIFY_URL` 指向 `billing-service` 的 `/api/v1/user/payments/alipay/notify`。
2. 指向 `admin-api`，由 `admin-api` 代理到 `billing-service` 的同名路径。

无论采用哪种方式，公网网关或反向代理都必须放行 `POST`。如果公网 `POST` 返回 `404` 或 `405`，支付宝异步通知不会触发；订单可能只能在用户查看订单时通过支付宝查询刷新状态，订阅发放也会延后到该刷新链路。

## 5. 健康检查与监控

### 5.1 健康检查端点

所有服务均提供 `/healthz` 端点：

```bash
curl http://<service>:<port>/healthz
# 返回: {"status":"ok"}
```

### 5.2 Prometheus Metrics

所有服务均提供 `/metrics` 端点，暴露以下指标：

| 指标 | 类型 | 说明 |
|------|------|------|
| `micro_one_api_http_requests_total` | Counter | HTTP 请求总数（按 service/method/path/status） |
| `micro_one_api_http_request_duration_seconds` | Histogram | HTTP 请求延迟 |
| `micro_one_api_http_active_requests` | Gauge | 当前活跃请求数 |
| `micro_one_api_grpc_requests_total` | Counter | gRPC 请求总数 |
| `micro_one_api_grpc_request_duration_seconds` | Histogram | gRPC 请求延迟 |
| `micro_one_api_billing_reservations_total` | Counter | 账务预扣次数 |
| `micro_one_api_channel_selection_total` | Counter | 渠道选择次数 |
| `micro_one_api_billing_reconciliation_runs_total` | Counter | 对账任务运行次数（按 status） |
| `micro_one_api_billing_reconciliation_run_duration_seconds` | Histogram | 对账任务运行耗时（按 status） |
| `micro_one_api_billing_reconciliation_discrepancies_total` | Counter | 对账差异数量（按 type） |
| `micro_one_api_monitor_channel_health_check_runs_total` | Counter | 渠道健康探测 sweep 次数（按 status） |
| `micro_one_api_monitor_channel_health_check_run_duration_seconds` | Histogram | 渠道健康探测 sweep 耗时（按 status） |
| `micro_one_api_monitor_channel_health_probe_total` | Counter | 单渠道探测次数（按 status/reason） |
| `micro_one_api_monitor_channel_health_probe_duration_seconds` | Histogram | 单渠道探测耗时（按 status） |

### 5.3 K8s 健康检查配置

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: http
  initialDelaySeconds: 10
  periodSeconds: 15
readinessProbe:
  httpGet:
    path: /healthz
    port: http
  initialDelaySeconds: 5
  periodSeconds: 10
```

## 6. 账务对账

billing-service 提供对账端点：

```bash
curl -X POST http://localhost:8004/v1/reconciliation
```

返回 JSON 包含：
- `expired_cleaned` — 清理的过期预扣数量
- `account_inconsistencies` — 余额不一致的账户列表
- `total_accounts` — 总账户数
- `total_reservations` — 当前活跃预抽数

建议通过 cron 定期调用：

```bash
# 每小时执行一次对账
0 * * * * curl -s -X POST http://billing-service:8004/v1/reconciliation >> /var/log/reconciliation.log
```

### 6.1 对账告警投递

`channel-service` 可以把渠道不可用告警写入 `notify-worker`，`billing-service` 可以把对账差异写入 `notify-worker`，再由 `notify-worker` 发送到各通知通道：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `NOTIFY_GRPC_ENDPOINT` | notify-worker gRPC 地址；留空时不投递通知 | - |
| `CHANNEL_HEALTH_ALERT_ENABLED` | 渠道首次进入 `unavailable` 时是否投递通知 | `false` |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | 渠道不可用告警类型：`event` / `webhook` / `email` / `wecom` / `dingtalk` / `feishu` / `slack` | `event` |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱，IM 通道留空走对应配置 | `[""]` |
| `RECON_ALERT_ENABLED` | 是否启用对账告警 | `true` |
| `RECON_ALERT_NOTIFY_TYPE` | 告警类型：`event` / `webhook` / `email` / `wecom` / `dingtalk` / `feishu` / `slack` | `event` |
| `RECON_ALERT_RECIPIENTS` | JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱，IM 通道留空走对应配置 | `[""]` |
| `RECON_ALERT_INTERVAL` | 自动对账间隔 | `1h` |
| `NOTIFY_WEBHOOK_URL` | 默认 webhook 地址，供 `event` / `webhook` 告警 fallback 使用 | - |
| `NOTIFY_SMTP_HOST` / `NOTIFY_SMTP_PORT` / `NOTIFY_SMTP_USER` / `NOTIFY_SMTP_PASS` / `NOTIFY_SMTP_FROM` | SMTP 邮件投递配置 | - |
| `NOTIFY_WECOM_WEBHOOK_URL` | 企业微信 Webhook URL 或 key | - |
| `NOTIFY_DINGTALK_WEBHOOK_URL` | 钉钉 Webhook URL 或 access_token | - |
| `NOTIFY_FEISHU_WEBHOOK_URL` | 飞书 Webhook URL | - |
| `NOTIFY_SLACK_WEBHOOK_URL` | Slack Incoming Webhook URL | - |

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

企业微信示例：

```bash
RECON_ALERT_NOTIFY_TYPE=wecom
RECON_ALERT_RECIPIENTS=[""]
NOTIFY_WECOM_WEBHOOK_URL=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx
```

钉钉示例：

```bash
RECON_ALERT_NOTIFY_TYPE=dingtalk
RECON_ALERT_RECIPIENTS=[""]
NOTIFY_DINGTALK_WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=xxx
```

飞书示例：

```bash
RECON_ALERT_NOTIFY_TYPE=feishu
RECON_ALERT_RECIPIENTS=[""]
NOTIFY_FEISHU_WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/xxx
```

Slack 示例：

```bash
RECON_ALERT_NOTIFY_TYPE=slack
RECON_ALERT_RECIPIENTS=[""]
NOTIFY_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/xxx
```

## 7. 故障排查

### 7.1 服务无法启动

```bash
# 检查日志
docker compose logs <service-name>

# 常见原因：
# 1. MySQL/Redis 未就绪 — 检查 depends_on 和 healthcheck
# 2. 配置文件路径错误 — 检查 CONF_PATH 环境变量
# 3. 端口冲突 — 检查端口占用
```

### 7.2 gRPC 连接失败

```bash
# 检查目标服务是否运行
docker compose ps

# 检查网络连通性
docker compose exec relay-gateway ping identity-service

# 检查 gRPC 端口
docker compose exec identity-service netstat -tlnp
```

### 7.3 限流触发

```bash
# 检查限流配置
echo $RATE_LIMIT_REQUESTS_PER_SECOND
echo $RATE_LIMIT_BURST

# 查看限流日志
docker compose logs relay-gateway | grep "rate limit"
```

### 7.4 Metrics 接入 Grafana

1. 添加 Prometheus 数据源，scrape 配置：

```yaml
scrape_configs:
  - job_name: 'micro-one-api'
    static_configs:
      - targets:
          - 'relay-gateway:8080'
          - 'identity-service:8001'
          - 'channel-service:8002'
          - 'billing-service:8004'
          - 'admin-api:3000'
    metrics_path: /metrics
    scrape_interval: 15s
```

2. 导入 Grafana Dashboard，使用 `micro_one_api_*` 前缀的指标。

## 8. 数据库迁移

SQL 迁移文件位于仓库根目录的 `migrations/`，Docker Compose 启动 MySQL 时会自动执行。订阅套餐购买需要 `050_create_subscription_plans.sql`；上游订阅账号本地额度需要 `051_add_subscription_account_local_quota.sql`。

手动迁移：

```bash
# 进入 MySQL
docker compose exec mysql mysql -u root -p oneapi

# 执行迁移文件示例
source /docker-entrypoint-initdb.d/001_create_users.sql
```

## 9. 多数据库方言部署

Micro-One-API 在运行时支持 MySQL、SQLite3 和 Postgres 三种数据库方言。方言由配置项 `data.database.driver`（或环境变量 `DATABASE_DRIVER`）选择；DSN 由 `data.database.source`（或 `DATABASE_DSN`）提供。

| 方言 | `driver` | 适用场景 | Compose 文件 |
| --- | --- | --- | --- |
| MySQL | `mysql` | 多实例、高并发、生产 | `docker-compose.yml`（默认） |
| SQLite3 | `sqlite3` | 单机、自托管、低维护 | `docker-compose.lite.yml` |
| Postgres | `postgres` | 多实例、需要 Postgres 特性 | `docker-compose.postgres.yml` |

驱动也可省略，由 `xdb.InferDriver` 根据 DSN 形状自动推断（`file:*.db`、`.sqlite`/`.sqlite3`/`.db` 后缀 → SQLite3；`postgres://` / `postgresql://` / `host=…` → Postgres；其余默认 MySQL）。

### 9.1 Lite 部署（SQLite3）

Lite 模式去掉了 MySQL 容器和 `docker-entrypoint-initdb.d` 路径，数据落在一个可挂载的 SQLite3 文件卷中。`phase3_partitioning.sql` 在该模式下不适用：`internal/pkg/db.PartitionManager` 检测到非 MySQL 拨号器时会自动把 `Supported` 置为 `false`，所有分区维护调用变成 no-op。

```bash
cd deployments/docker-compose
cp .env.lite.example .env
# 编辑 .env：JWT_SECRET_KEY / SERVICE_TOKEN / ADMIN_TOKEN / REDIS_PASSWORD

# 首次启动会自动跑 migrations/sqlite 下的 baseline（一次性 migrate service）
docker compose -f docker-compose.lite.yml --env-file .env up -d

# 查看迁移日志
docker compose -f docker-compose.lite.yml logs migrate
```

默认 DSN 形如 `file:/data/micro-one-api.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on`；`xdb.Open` 会自动启用这套 PRAGMA 并把 `MaxOpenConns=1` 以避免写锁竞争。

### 9.2 Postgres 部署

```bash
cd deployments/docker-compose
cp .env.postgres.example .env
# 编辑 .env：POSTGRES_PASSWORD / JWT_SECRET_KEY / SERVICE_TOKEN / ADMIN_TOKEN / REDIS_PASSWORD

docker compose -f docker-compose.postgres.yml --env-file .env up -d
```

DSN 形如 `host=postgres user=… password=… dbname=micro_one_api port=5432 sslmode=disable`。Go 侧使用 `github.com/jackc/pgx/v5/stdlib`（`sql.Open("pgx", dsn)`），GORM 侧使用 `gorm.io/driver/postgres`。

### 9.3 手动迁移

`cmd/migrate` 接受 `-driver` 和 `-dir` 标志，并会从 `MIGRATIONS_DRIVER` / `MIGRATIONS_DSN` 环境变量读取：

```bash
# MySQL（默认）
go run ./cmd/migrate -dir ./migrations

# SQLite3
MIGRATIONS_DRIVER=sqlite3 \
MIGRATIONS_DSN='file:/data/micro-one-api.db' \
  go run ./cmd/migrate -dir ./migrations/sqlite

# Postgres
MIGRATIONS_DRIVER=postgres \
MIGRATIONS_DSN='host=127.0.0.1 user=app password=… dbname=micro_one_api port=5432 sslmode=disable' \
  go run ./cmd/migrate -dir ./migrations/postgres
```

`migrations/mysql`、`migrations/sqlite`、`migrations/postgres` 三套迁移是分别维护的快照。SQLite3 / Postgres baseline 是单一手写文件（`000_create_full_schema.sql`），由 `docs/issue-4-sqlite-solution.md` 描述的设计取舍决定：MySQL 迁移依赖 `AUTO_INCREMENT`、`ENGINE=InnoDB`、`COMMENT`、前缀索引、`ON UPDATE CURRENT_TIMESTAMP` 等方言特性，逐文件翻译要么丢能力、要么需要条件 SQL runner；snapshot 形式更易于评审与同步。

新增 schema 变更时请同步更新两套迁移目录（MySQL + 对应方言）；CI 会在 PR 上跑 `cmd/migrate -dir ./migrations/<dialect>` 作为防漂移检查。
