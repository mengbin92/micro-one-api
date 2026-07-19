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

### 1.1 部署与文档漂移检查

仓库通过 `scripts/check-deployment-docs.sh` 在 PR 阶段检查：

- MySQL、Lite、PostgreSQL 和 E2E overlay 的 Compose 配置能否完成变量展开。
- `kustomize build` 能否渲染生产清单，以及渲染结果能否通过 Kubernetes 1.33 schema 的严格校验。
- 清单引用的 ConfigMap、Secret 和 key 是否已在清单中定义，或在本文档中提供创建命令；生产必需引用不得设置 `optional: true`。
- 根 README 和 `docs/**/*.md` 中的本地文件链接是否存在。

安装 Docker Compose、Go、Python 3、Kustomize 和 kubeconform 后，可在仓库根目录执行与 CI 相同的检查：

```bash
./scripts/check-deployment-docs.sh
```

Secret/ConfigMap 名称、引用 key、创建命令或文件路径变更时，应同步更新部署清单和本文档，否则检查会失败。

## 2. Docker Compose 部署（开发/测试）

### 2.1 前置条件

- Docker 20.10+
- Docker Compose v2+

### 2.2 启动

```bash
cd deployments/docker-compose
cp .env.example .env
# 编辑 .env，至少替换 MYSQL_ROOT_PASSWORD、DATABASE_DSN、
# REDIS_PASSWORD、JWT_SECRET_KEY、SERVICE_TOKEN 和 ADMIN_TOKEN。
# DATABASE_DSN 中的 MySQL 密码必须与 MYSQL_ROOT_PASSWORD 相同。

# 部署前先验证变量展开和 Compose 结构
docker compose --env-file .env config --quiet

# 从干净环境构建并启动全部服务
docker compose --env-file .env up -d --build

# 查看服务状态
docker compose --env-file .env ps

# 查看日志
docker compose logs -f relay-gateway
```

### 2.3 验证

```bash
# 健康检查
curl --fail http://localhost:8080/healthz  # relay-gateway
curl --fail http://localhost:3000/healthz  # admin-api

# 其余服务只暴露在 backend 网络，从 admin-api 容器内检查
for endpoint in \
  identity-service:8001 channel-service:8002 billing-service:8004 \
  config-service:8005 log-service:8006 monitor-worker:8007 notify-worker:8008; do
  docker compose --env-file .env exec -T admin-api \
    wget -qO- "http://${endpoint}/healthz"
done

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
docker compose --env-file .env down           # 停止服务
docker compose --env-file .env down -v        # 停止并删除数据卷
```

仓库自带的全新环境 smoke test 会生成临时变量文件、构建镜像、检查九个服务并自动删除容器和数据卷：

```bash
./scripts/test-docker-compose.sh
```

## 3. Kubernetes 部署（生产）

### 3.1 前置条件

- Kubernetes 1.25+
- kubectl 配置完成
- Kustomize 5+
- Nginx Ingress Controller 已安装
- MySQL 8 和启用密码认证的 Redis 可用（集群内或外部），并能通过清单中的 `mysql:3306`、`redis:6379` 地址访问；使用外部地址时先修改 `app-config`
- 已安装 Go 1.26（执行数据库迁移）
- 已准备九个服务镜像；本仓库的 Release 流程只发布 GitHub Release，不代替用户推送镜像

### 3.2 创建命名空间

```bash
kubectl create namespace one-api --dry-run=client -o yaml | kubectl apply -f -
```

### 3.3 创建 Secrets

```bash
# MySQL 连接 DSN；名称必须与清单中的 db-credentials 一致
kubectl create secret generic db-credentials \
  --from-literal=dsn='root:password@tcp(mysql:3306)/oneapi?parseTime=true' \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

# Redis 密码
kubectl create secret generic redis-credentials \
  --from-literal=password='replace-with-the-redis-password' \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

# JWT 签名密钥和管理端共享令牌
kubectl create secret generic app-secrets \
  --from-literal=jwt-secret-key='replace-with-at-least-32-random-bytes' \
  --from-literal=admin-token='replace-with-a-long-random-token' \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

# Admin API Basic Auth
htpasswd -c auth admin
kubectl create secret generic admin-basic-auth \
  --from-file=auth \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

# TLS 证书
kubectl create secret tls api-tls-secret \
  --cert=api.yourdomain.com.crt \
  --key=api.yourdomain.com.key \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret tls admin-tls-secret \
  --cert=admin.yourdomain.com.crt \
  --key=admin.yourdomain.com.key \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -

# 服务间调用令牌（relay、admin、billing 和 log 必须使用相同值）
kubectl create secret generic service-token-secret \
  --from-literal=token='replace-with-a-long-random-token' \
  -n one-api --dry-run=client -o yaml | kubectl apply -f -
```

所有上述 Secret 都是生产必需项，清单不会以 `optional: true` 忽略缺失值。不要把 Secret 内容写入 YAML 或提交到仓库。

### 3.4 构建并替换镜像

清单中的 `your-registry/<service>:v0.7.2` 是带固定版本的占位镜像。生产环境必须使用不可变版本号或镜像 digest，不要改成浮动的 `latest`。

每个服务的 Dockerfile 和构建参数以 `.github/workflows/ci.yml` 的 `docker` matrix 为准。例如：

```bash
export REGISTRY=registry.example.com/micro-one-api
export IMAGE_TAG=v0.7.2

docker buildx build --platform linux/amd64,linux/arm64 \
  -f app/identity/Dockerfile \
  --build-arg SERVICE_NAME=identity-service \
  --build-arg SERVICE_PATH=./app/identity/cmd/identity \
  -t "$REGISTRY/identity-service:$IMAGE_TAG" --push .
```

推送九个镜像后，用 Kustomize 一次性替换全部占位名称和标签：

```bash
cd deployments/k8s
for service in relay-gateway identity-service channel-service billing-service \
  admin-api config-service log-service monitor-worker notify-worker; do
  kustomize edit set image \
    "your-registry/${service}=${REGISTRY}/${service}:${IMAGE_TAG}"
done
cd ../..

# 输出中不应再出现占位 registry 或 latest
kubectl kustomize deployments/k8s | grep -E 'image:'
```

`kustomize edit` 会更新本地 `deployments/k8s/kustomization.yaml`；应将环境专用的 registry 覆盖保留在部署系统中，不要提交私有 registry 地址。

### 3.5 执行数据库迁移

在启动应用 Pod 前执行迁移。`MIGRATIONS_DSN` 与 `db-credentials` 中的 `dsn` 必须完全一致。自动迁移会执行编号迁移和 `phase1_indexes.sql`；`phase3_partitioning.sql` 是有额外主键、停机窗口和 MySQL 分区前置条件的可选运维脚本，不会自动执行：

```bash
MIGRATIONS_DRIVER=mysql \
MIGRATIONS_DSN='root:password@tcp(mysql:3306)/oneapi?parseTime=true' \
  go run ./cmd/migrate -dir ./migrations
```

数据库只能从集群内访问时，使用 `deployments/docker/Dockerfile.migrate` 构建一次性迁移镜像，并通过只引用 `db-credentials` 的 Kubernetes Job 运行同一命令。

### 3.6 部署服务

```bash
# 首次部署或升级均使用同一个入口
kubectl apply -k deployments/k8s

# 等待九个 Deployment 逐个完成 rollout
for deployment in $(kubectl get deployment -n one-api -o name); do
  kubectl rollout status -n one-api "$deployment" --timeout=10m
done
```

### 3.7 验证

```bash
# 检查 Pod 状态
kubectl get pods -n one-api

# 检查服务
kubectl get svc -n one-api

# 检查 Ingress
kubectl get ingress -n one-api

# 查看日志
kubectl logs -f deployment/relay-gateway -n one-api

# 从 admin-api Pod 检查内部 HTTP 接口和共享令牌
ADMIN_POD=$(kubectl get pod -n one-api -l app=admin-api -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n one-api "$ADMIN_POD" -- wget -qO- http://billing-service:8004/healthz
kubectl exec -n one-api "$ADMIN_POD" -- wget -qO- http://log-service:8006/healthz

# 本地端口转发验证管理端和 Relay
kubectl port-forward -n one-api service/admin-api 3000:3000
kubectl port-forward -n one-api service/relay-gateway 8080:80
```

验收时 `kubectl get pods -n one-api` 应全部为 `Running` 且 `READY` 列满额；`admin-api`、`relay-gateway`、`billing-service` 和 `log-service` 的上述检查都必须成功。若启用了 NetworkPolicy，Ingress Controller 所在命名空间必须为 `ingress-nginx`；否则同步修改清单中的命名空间选择器。

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
| `ADMIN_WEB_ROOT` | 管理前端静态文件目录；目录内必须有 `index.html`，未配置或不可用时返回 500 | - |

#### 管理前端静态资源

自 v0.8.0 起，`admin-api` 不再使用 `go:embed` 将前端编译进二进制，而是统一从 `ADMIN_WEB_ROOT` 指定的目录提供静态资源。目录内必须包含 `index.html`。

例如，将 `ADMIN_WEB_ROOT` 指向外部构建目录：

```bash
make web-dist
ADMIN_WEB_ROOT=/srv/micro-one-api/web/dist ./admin-api
```

运行中的 `admin-api` 会直接从该目录读取 `index.html`、`assets/*`、`favicon.svg` 等文件。替换目录内的前端构建产物后，无需重新编译 Go 后端；浏览器刷新即可加载新的 Vite hash 资源。若目录不存在或缺少 `index.html`，服务会返回 500 `frontend not available`，不再回退到内嵌资源。

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
| `ALIPAY_CERT_DIR` | 宿主机支付宝密钥目录；默认相对 compose 文件为 `../../cert/alipay`，目录会只读挂载到容器 `/cert/alipay` |
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

SQL 迁移文件位于仓库根目录的 `migrations/`。Docker Compose 的一次性 `migrate` 服务会先执行迁移，成功后其他应用服务才启动；迁移失败会直接阻止部署继续。订阅套餐购买需要 `050_create_subscription_plans.sql`；上游订阅账号本地额度需要 `051_add_subscription_account_local_quota.sql`。

查看或手动执行迁移：

```bash
cd deployments/docker-compose

# 查看一次性迁移服务的执行结果
docker compose --env-file .env logs migrate

# 查看迁移记录；命令复用同一个 migrate 镜像和 .env 中的 DSN
docker compose --env-file .env run --rm migrate -status

# 应用尚未记录的迁移
docker compose --env-file .env run --rm migrate
```

### 8.1 从 v0.7.1 或更早的 Compose 数据卷升级

旧版 Compose 把整个 `migrations/` 目录挂载到 MySQL 的 `/docker-entrypoint-initdb.d`。该机制只在空数据目录首次启动时执行，而且 SQL 中途失败后重启 MySQL 会跳过剩余文件，因此 v0.7.2 不再使用它。

升级已有数据卷前必须先备份数据库。如果旧环境已确认完整执行到 `phase1_indexes.sql`，先用 brownfield baseline 只登记历史版本，再启动 v0.7.2：

```bash
cd deployments/docker-compose

docker compose --env-file .env up -d mysql redis
docker compose --env-file .env run --rm migrate -baseline phase1_indexes
docker compose --env-file .env up -d --build
```

`-baseline phase1_indexes` 只适用于已经具备这些历史表、列和索引的旧数据库；它不会补建缺失结构。若旧初始化曾失败、无法确认 schema 完整性，禁止直接 baseline，应先从备份恢复、逐项修复缺失迁移，或迁移到按本文创建的全新数据库。全新 v0.7.2 环境不需要指定 baseline，一次性 `migrate` 会执行所有编号迁移和 `phase1_indexes.sql`，并继续排除可选的 `phase3_partitioning.sql`。

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

`migrations/mysql`、`migrations/sqlite`、`migrations/postgres` 三套迁移是分别维护的快照。SQLite3 / Postgres baseline 是单一手写文件（`000_create_full_schema.sql`），由 `docs/design/issue-4-sqlite-solution.md` 描述的设计取舍决定：MySQL 迁移依赖 `AUTO_INCREMENT`、`ENGINE=InnoDB`、`COMMENT`、前缀索引、`ON UPDATE CURRENT_TIMESTAMP` 等方言特性，逐文件翻译要么丢能力、要么需要条件 SQL runner；snapshot 形式更易于评审与同步。

新增 schema 变更时请同步更新两套迁移目录（MySQL + 对应方言）；CI 会在 PR 上跑 `cmd/migrate -dir ./migrations/<dialect>` 作为防漂移检查。


## 10. Phase 2.4 — 数据库 Schema 隔离

默认部署模型仍是 **所有服务共享同一个数据库**（`DATABASE_DSN` 指向同一个 MySQL `oneapi` 库或 Postgres 数据库）。Schema 隔离是 **可选** 的：每个服务可通过环境变量 `<SVC>_SCHEMA`（或共享的 `DATABASE_SCHEMA`）指向自己的 MySQL database 或 Postgres schema，互不影响。

### 10.1 何时启用

| 场景 | 是否启用 |
| --- | --- |
| 单机自托管 / Lite | 不启用（保持单库） |
| 标准多实例部署 | 可选，按服务逐步切换 |
| 多租户隔离 / 故障爆炸半径要求 | 强烈建议启用 |
| 高频日志写入影响其他服务 | 建议至少把 `log-service` 切到独立 schema |

### 10.2 配置方式

每个服务的 `config.yaml` 新增 `data.database.schema` 字段（默认空）。生产部署通过环境变量覆盖即可，**无需修改 yaml 文件**：

```bash
# 只切 identity-service 到独立 schema
IDENTITY_SCHEMA=oneapi_identity

# 或全局给所有服务设置共享前缀
DATABASE_SCHEMA=oneapi

# 服务级覆盖优先于 DATABASE_SCHEMA
```

| 服务 | 环境变量 | 建议 schema 名 |
| --- | --- | --- |
| identity-service | `IDENTITY_SCHEMA` | `oneapi_identity` |
| channel-service | `CHANNEL_SCHEMA` | `oneapi_channel` |
| billing-service | `BILLING_SCHEMA` | `oneapi_billing` |
| log-service | `LOG_SCHEMA` | `oneapi_log` |
| admin-api | `ADMIN_SCHEMA` | `oneapi_admin` |
| config-service | `CONFIG_SCHEMA` | `oneapi_config` |
| notify-worker | `NOTIFY_SCHEMA` | `oneapi_notify` |
| monitor-worker | `MONITOR_SCHEMA` | `oneapi_monitor` |

### 10.3 一次性切流（参考脚本）

`migrations/schema_split.sql` 是一份参考 DDL，它在一个已有数据的 MySQL 实例上 **复制**（而非移动）表到 8 个新 schema：

```bash
mysql -h <host> -u root -p < migrations/schema_split.sql
```

切流步骤：

1. 在低峰期执行 `schema_split.sql`，把共享 `oneapi` 库的表复制到 8 个 `<svc>` schema（源库不动）。
2. 对每个 schema 跑一次带 `-ownership` 的 migrate，确保 schema_migrations 和该服务拥有的迁移都已落地：

   ```bash
   for svc in identity channel billing log admin config notify monitor; do
     MIGRATIONS_DSN="root:pw@tcp(host:3306)/oneapi_${svc}"        go run ./cmd/migrate -ownership ${svc%-*}
   done
   ```

3. 滚动重启各服务，环境变量从 `DATABASE_DSN=.../oneapi?...` 切到 `DATABASE_DSN=.../oneapi?...` + `<SVC>_SCHEMA=oneapi_<svc>`。
4. 观察 30 分钟；任何异常只需把 `<SVC>_SCHEMA` 置空并重启对应服务即可回滚（源库未变）。

### 10.4 billing-service 跨 schema 读的过渡处理

`billing-service` 的对账逻辑（`app/billing/internal/data/reconciliation_repo.go`）会读 `channels` / `logs` / `users` 三张表进行跨服务数据对账。当 billing 切到 `oneapi_billing` schema 后，这些读会失败。过渡方案：

- **方案 A（推荐，零代码改动）**：在 `oneapi_billing` 中为这三张表建视图，指向源 schema：

  ```sql
  USE oneapi_billing;
  CREATE OR REPLACE VIEW channels          AS SELECT * FROM oneapi_channel.channels;
  CREATE OR REPLACE VIEW logs              AS SELECT * FROM oneapi_log.logs;
  CREATE OR REPLACE VIEW users             AS SELECT * FROM oneapi_identity.users;
  ```

  > **说明**：
  > - `channels`/`logs` 视图用于 `ListChannelUsage`/`GetLogConsumeSummary` 对账
  > - `users` 视图用于 `SumOverdraftBalances` 钱包透支对账
  > - `user_subscriptions` 已被 `schema_split.sql` 复制到 `oneapi_billing`，无需视图

- **方案 B（长期）**：将对账读改为走 channel-service / log-service / identity-service 的 gRPC 接口。属于后续重构，本次不实现。

### 10.5 ownership manifest

`migrations/ownership.yaml` 声明每个服务"拥有"哪些迁移文件。`cmd/migrate -ownership <service>` 在 per-service schema 上只跑该服务拥有的迁移 + 共享 bootstrap（`schema_migrations`）。manifest 缺失时退化为旧行为（全量应用），因此未启用 schema 隔离的部署完全不受影响。

```yaml
shared:
  - 022_create_schema_migrations
services:
  identity:
    - 014_add_token_management_fields
    - 019_create_user_oauth_identities
    # …
  billing:
    - 008_create_billing_reservations
    # …
```

## 11. Phase 2.5 — 配置热更新

### 11.1 机制

`platform/config/source.go` 的 `EnvFileSource` 现在使用 fsnotify 真正监听 config 文件变化（之前的实现是 noop）。任何 `NewEnvFileSource(path)` + `kratos config.Watch(key, observer)` 的订阅者都会在文件变化时收到回调。fsnotify 不可用的环境（无 inotify 的容器、受限沙箱）自动降级为 noop watcher，**服务仍可启动**，只是不会热更新。

### 11.2 已接入的热更新点

| 组件 | 文件 | 行为 |
| --- | --- | --- |
| relay-gateway ModelMapper | `configs/models.yaml` | 文件变化时重新解析并原子替换映射表；解析失败保留旧快照并告警 |

订阅通过 `platform/config.SubscribeFile(path, callback)`（`subscribe.go`）建立，回调在独立的 goroutine 中执行；服务的 cleanup 函数负责调用返回的 stop。

### 11.3 验证

```bash
# 启动 relay-gateway，请求 gpt-4o → 应解析为 models.yaml 中配置的 actual_name
# 修改 configs/models.yaml 中 gpt-4o 的 actual_name，保存
# 无需重启：日志应出现 "models.yaml hot-reloaded"
# 再次请求 gpt-4o → 解析为新值
```

### 11.4 已知限制

- **配置文件本身的 schema 变化**（端口、DSN、Redis 地址等）不会触发组件重建——这些只在进程启动时读取一次。热更新目前只覆盖"可安全原子替换"的组件（model 映射）。这是有意为之的保守策略：连接池、监听 socket 等重启成本高的资源不做热切换。
- **环境变量替换** `${VAR:-default}` 在每次重载时都会用最新环境变量重新展开，因此修改 env 后再 touch 一下 config 文件即可生效。
