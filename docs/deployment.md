# 部署运维文档

## 1. 架构概览

Micro-One-API 由 9 个微服务组成：

| 服务 | HTTP 端口 | gRPC 端口 | 职责 |
|------|----------|----------|------|
| relay-gateway | 8080 | - | OpenAI 兼容 API 网关 |
| admin-api | 3000 | 9000 | 管理端 BFF |
| identity-service | 8001 | 9001 | 用户认证与鉴权 |
| channel-service | 8002 | 9002 | 渠道管理与路由 |
| billing-service | 8004 | 9004 | 配额账务 |
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
  -H "Authorization: Bearer YOUR_TOKEN" \
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
| `CONF_PATH` | 配置文件路径 | `configs/<service>.yaml` |
| `DATABASE_DSN` | MySQL 连接字符串 | - |
| `REDIS_ADDR` | Redis 地址 | - |
| `LOG_LEVEL` | 日志级别 | `info` |
| `LOG_FORMAT` | 日志格式 (json/text) | `json` |
| `SERVICE_TOKEN` | 服务间 HTTP 调用令牌；admin-api 访问 log-service 详情与清理接口时使用 | - |

### 4.2 Relay Gateway 专用

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `IDENTITY_GRPC_ENDPOINT` | identity-service gRPC 地址 | - |
| `CHANNEL_GRPC_ENDPOINT` | channel-service gRPC 地址 | - |
| `BILLING_GRPC_ENDPOINT` | billing-service gRPC 地址 | - |
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
- `account_inconsistencies` — 配额不一致的账户列表
- `total_accounts` — 总账户数
- `total_reservations` — 当前活跃预抽数

建议通过 cron 定期调用：

```bash
# 每小时执行一次对账
0 * * * * curl -s -X POST http://billing-service:8004/v1/reconciliation >> /var/log/reconciliation.log
```

### 6.1 对账告警投递

`billing-service` 可以把对账差异写入 `notify-worker`，再由 `notify-worker` 发送到 webhook 或 SMTP：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `NOTIFY_GRPC_ENDPOINT` | notify-worker gRPC 地址；留空时只记录日志 | - |
| `RECON_ALERT_ENABLED` | 是否启用对账告警 | `true` |
| `RECON_ALERT_NOTIFY_TYPE` | 告警类型：`event` / `webhook` / `email` | `event` |
| `RECON_ALERT_RECIPIENTS` | JSON 数组；webhook/event 可填 URL 或留空走 `NOTIFY_WEBHOOK_URL`，email 填邮箱 | `[""]` |
| `RECON_ALERT_INTERVAL` | 自动对账间隔 | `1h` |
| `NOTIFY_WEBHOOK_URL` | 默认 webhook 地址，供 `event` / `webhook` 告警 fallback 使用 | - |
| `NOTIFY_SMTP_HOST` / `NOTIFY_SMTP_PORT` / `NOTIFY_SMTP_USER` / `NOTIFY_SMTP_PASS` / `NOTIFY_SMTP_FROM` | SMTP 邮件投递配置 | - |

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

SQL 迁移文件位于仓库根目录的 `migrations/`，Docker Compose 启动 MySQL 时会自动执行。

手动迁移：

```bash
# 进入 MySQL
docker compose exec mysql mysql -u root -p oneapi

# 执行迁移文件示例
source /docker-entrypoint-initdb.d/001_create_users.sql
```
