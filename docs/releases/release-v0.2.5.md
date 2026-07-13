# Micro-One-API v0.2.5 发布公告

> 2026-06-19 · 上一版: [v0.2.4](./release-v0.2.4.md) (2026-06-19)

v0.2.5 是一个通知能力增强版本，重点为 `notify-worker` 增加企业 IM 投递通道，让渠道健康告警和对账告警可以直接发送到企业微信、钉钉、飞书/Lark 和 Slack。无数据库迁移，无破坏性 API 变更。

## 变更内容

- **企业 IM 通知通道**：`notify-worker` 新增 `wecom`、`dingtalk`、`feishu`、`slack` 四类通知发送能力。
- **告警类型扩展**：`CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` 和 `RECON_ALERT_NOTIFY_TYPE` 可配置为 `wecom`、`dingtalk`、`feishu`、`slack`。
- **Webhook 配置更灵活**：企业微信支持完整 webhook URL 或 key；钉钉支持完整 webhook URL 或 access_token；飞书和 Slack 使用完整 webhook URL。
- **文档与示例更新**：README、部署文档和环境变量示例补齐企业 IM 通道配置。

## 配置变化

新增以下 `notify-worker` 环境变量：

| 变量 | 说明 |
|---|---|
| `NOTIFY_WECOM_WEBHOOK_URL` | 企业微信 Webhook URL 或 key |
| `NOTIFY_DINGTALK_WEBHOOK_URL` | 钉钉 Webhook URL 或 access_token |
| `NOTIFY_FEISHU_WEBHOOK_URL` | 飞书/Lark Webhook URL |
| `NOTIFY_SLACK_WEBHOOK_URL` | Slack Incoming Webhook URL |

告警通知类型现在支持：

```bash
CHANNEL_HEALTH_ALERT_NOTIFY_TYPE=wecom
RECON_ALERT_NOTIFY_TYPE=dingtalk
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

## 升级说明

- 无数据库迁移。
- 无破坏性 API 变更。
- 如继续使用 webhook / email 通知，无需修改现有配置。
- 如要启用企业 IM 通知，请将对应告警的 `*_NOTIFY_TYPE` 改为 `wecom`、`dingtalk`、`feishu` 或 `slack`，并配置对应 webhook 环境变量。

## 验证

本次发版前已通过：

```bash
make test
cd web && npm test && npm run build
```

新增/更新的通知单元测试覆盖：
- 企业微信、钉钉、飞书和 Slack webhook payload
- 企业微信 key 与钉钉 access_token 自动拼接
- 空 recipient 时使用默认 webhook 配置
- 未配置对应 webhook 时返回 sender not ready
