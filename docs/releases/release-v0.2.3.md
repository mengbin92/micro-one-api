# Micro-One-API v0.2.3 发布公告

> 2026-06-18 · 上一版: [v0.2.2](./release-v0.2.2.md) (2026-06-15)

v0.2.3 是一个稳定性与运维体验版本，重点加入渠道健康状态、自动熔断与管理后台日志排查能力，同时补齐安全扫描与发布流水线。包含 1 个数据库迁移，无破坏性 API 变更。

## 亮点

- **渠道健康熔断**：relay 上游调用会回写成功、失败和响应时间；连续失败达到阈值后，channel-service 会跳过异常渠道，冷却期后进入半开恢复。
- **定时健康探测**：monitor-worker 支持按间隔探测启用渠道的 `/models` 接口，帮助提前发现不可用渠道。
- **管理后台渠道健康展示**：渠道列表展示健康状态、失败次数、冷却时间，并支持手动触发健康探测。
- **管理后台日志排查增强**：日志列表支持时间过滤、详情弹窗与清理操作，admin-api 补齐日志详情代理。
- **安全与发布流水线修复**：修复安全扫描告警，补齐 GitHub Release workflow，并让 dependency review 在不支持的仓库环境下不阻塞安全流水线。

## 配置变化

新增以下环境变量：

| 变量 | 说明 |
|---|---|
| `CHANNEL_HEALTH_FAILURE_THRESHOLD` | 连续失败达到该阈值后触发渠道熔断 |
| `CHANNEL_HEALTH_COOLDOWN` | 熔断后的冷却时长，冷却结束后允许半开恢复 |
| `CHANNEL_HEALTH_CHECK_ENABLED` | 是否启用 monitor-worker 定时渠道健康探测 |
| `CHANNEL_HEALTH_CHECK_INTERVAL` | 定时健康探测间隔 |
| `CHANNEL_HEALTH_CHECK_TIMEOUT` | 单次健康探测超时时间 |
| `CHANNEL_HEALTH_ALERT_ENABLED` | 是否启用渠道不可用告警（需 notify-worker） |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | 告警通知类型（webhook/email） |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | 告警收件人列表，JSON 数组格式 |

示例：

```bash
CHANNEL_HEALTH_FAILURE_THRESHOLD=3
CHANNEL_HEALTH_COOLDOWN=1m
CHANNEL_HEALTH_CHECK_ENABLED=true
CHANNEL_HEALTH_CHECK_INTERVAL=1m
CHANNEL_HEALTH_CHECK_TIMEOUT=10s

# 告警需要 notify-worker 服务
CHANNEL_HEALTH_ALERT_ENABLED=true
CHANNEL_HEALTH_ALERT_NOTIFY_TYPE=webhook
CHANNEL_HEALTH_ALERT_RECIPIENTS=["https://your-webhook.example.com/alert"]
# 或邮件告警：
# CHANNEL_HEALTH_ALERT_NOTIFY_TYPE=email
# CHANNEL_HEALTH_ALERT_RECIPIENTS=["ops@example.com"]
```

## 数据库迁移

本次新增 1 个迁移：

```text
migrations/032_add_channel_health_fields.sql
```

升级前请按现有部署流程执行迁移，让 channel-service 能持久化健康状态、失败计数、冷却时间和最近探测信息。

## 升级说明

- 无破坏性 API 变更。
- 需要执行数据库迁移 `032_add_channel_health_fields.sql`。
- 使用 Docker Compose 的部署请同步 `.env.example` 和 `deployments/docker-compose/.env.example` 中新增的渠道健康配置项。
- 如果暂时不想启用定时探测，可保持 `CHANNEL_HEALTH_CHECK_ENABLED=false`；relay 调用链路仍会记录渠道成功/失败结果。

## 验证

本次发版前已通过：

```bash
make test
cd web && npm run lint
cd web && npm test
cd web && npm run build
```

GitHub Actions 中 `main` 与 `develop` 在提交 `4d18c40` 上的 CI 和 Security Pipeline 均已通过。
