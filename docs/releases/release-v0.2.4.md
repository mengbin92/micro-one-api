# Micro-One-API v0.2.4 发布公告

> 2026-06-19 · 上一版: [v0.2.3](./release-v0.2.3.md) (2026-06-18)

v0.2.4 是一个文档补全版本，补充了渠道健康告警配置说明和示例配置，确保用户能正确启用新功能。无代码变更，无破坏性 API 变更。

## 变更内容

- **补充渠道健康告警文档**：v0.2.3 引入了渠道健康告警能力，但遗漏了相关环境变量文档。本次补充完整说明。
- **更新部署示例配置**：docker-compose 部署的 `.env.example` 补齐了告警相关配置项。

## 配置变化

新增以下环境变量（v0.2.3 已支持，文档未列）：

| 变量 | 说明 |
|---|---|
| `CHANNEL_HEALTH_ALERT_ENABLED` | 是否启用渠道不可用告警（需 notify-worker） |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | 告警通知类型（webhook/email） |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | 告警收件人列表，JSON 数组格式 |

示例：

```bash
# Webhook 告警
CHANNEL_HEALTH_ALERT_ENABLED=true
CHANNEL_HEALTH_ALERT_NOTIFY_TYPE=webhook
CHANNEL_HEALTH_ALERT_RECIPIENTS=["https://your-webhook.example.com/alert"]

# 邮件告警
CHANNEL_HEALTH_ALERT_ENABLED=true
CHANNEL_HEALTH_ALERT_NOTIFY_TYPE=email
CHANNEL_HEALTH_ALERT_RECIPIENTS=["ops@example.com","oncall@example.com"]
```

## 升级说明

- 无数据库迁移。
- 无代码变更。
- 如果已部署 v0.2.3，只需在配置文件中补充上述告警变量即可启用渠道健康告警。
- 如不想启用告警，保持 `CHANNEL_HEALTH_ALERT_ENABLED=false` 或不配置即可。

## 验证

本次发版前已通过：

```bash
make test
cd web && npm test && npm run build
```

新增渠道健康告警单元测试覆盖：
- 从 healthy/degraded 变为 unavailable 时只通知一次
- 已经是 unavailable 时不重复通知
- `CHANNEL_HEALTH_ALERT_ENABLED=false` 或无 notifier 时不通知
- 多 recipients 时逐个创建 notification
