# 订阅账号治理 Runbook（阶段 1）

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 1：订阅账号治理。
> 实现 PR：`feat/subscription-account-ops-automation`。

本分支在 channel-service 进程内新增了三个订阅账号治理后台任务，全部默认关闭，通过环境变量按需开启。它们复用现有 channel-service 的 `ChannelRepo`（数据库直连）和 notify-worker 的 gRPC 通道（告警投递），不引入新的存储或投递链路。

## 1. quota reset 自动化（fixed 策略）

### 前置条件
- 账号 `quota_reset_strategy = 'fixed'` 且 `quota_timezone` 为合法 IANA 时区（非法回退 UTC）。
- 迁移 `057_create_subscription_account_quota_reset_runs.sql` 已执行。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_QUOTA_RESET_ENABLED` | `false` | 开启 fixed reset 扫描 |
| `SUBSCRIPTION_QUOTA_RESET_INTERVAL` | `5m` | 扫描间隔 |
| `SUBSCRIPTION_QUOTA_RESET_TIMEOUT` | `30s` | 单次扫描超时 |

### 行为
- 每 interval 扫描所有 fixed 策略账号，当 `quota_daily_window_start` / `quota_weekly_window_start` 落后于当前自然日/自然周起点时，重置对应窗口的 `used` 和 `window_start`。
- 幂等：先写 `subscription_account_quota_reset_runs`（唯一键 `account_id+scope+window_start`），重复 tick / 多副本返回 duplicate，跳过重置。
- rolling 策略账号不被扫描。
- 非法时区回退 UTC，可在 reset_runs 表的 `timezone` 列定位。

### 指标
- `micro_one_api_subscription_account_quota_resets_total{scope,result}`：success / duplicate / error
- `micro_one_api_subscription_account_reset_scan_duration_seconds`

### 验证
```bash
# 查看最近 reset 记录
SELECT subscription_account_id, scope, window_start, timezone, reset_at
FROM subscription_account_quota_reset_runs
ORDER BY reset_at DESC LIMIT 10;
```

## 2. 异常账号自动恢复

### 前置条件
- 账号被 `SetTempUnschedulable`（上游 429/5xx/529）或 `AutoPauseAccount`（授权异常/codex 耗尽）标记。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED` | `false` | 开启恢复扫描 |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_INTERVAL` | `5m` | 扫描间隔 |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_TIMEOUT` | `30s` | 单次扫描超时 |

### 恢复分层
| 策略 | 触发条件 | 自动恢复？ |
| --- | --- | --- |
| `auto` | 上游 429/5xx/529，TTL 到期 | 是 |
| `manual` | 上游 401/403 或 AutoPause | 否，需 OAuth 重绑或人工启用 |
| `quota` | 本地额度耗尽 | 等窗口 reset 后自动恢复 |
| `codex` | codex snapshot 耗尽 | 等 snapshot reset 后自动恢复 |
| `rolling` | 无明确标记 | 按 TTL 自动恢复（向后兼容） |

恢复时清理 `rate_limited_until` + `last_error` + recovery metadata（`recovery_policy`/`unschedulable_reason`/`unschedulable_since`/`unschedulable_until`/`expected_recovery_at`）。

### 指标
- `micro_one_api_subscription_account_recoveries_total{class,result}`：ttl_elapsed / window_reset / already_schedulable / waiting / skipped / error
- `micro_one_api_subscription_account_recovery_scan_duration_seconds`

### 管理后台
`SubscriptionAccountSummary` 新增 `unschedulable_reason` / `recovery_policy` / `expected_recovery_at` / `unschedulable_since`，管理后台可直接展示“为什么不可调度”和“预计恢复时间”。

## 3. 额度事件告警

### 前置条件
- `NOTIFY_GRPC_ENDPOINT` 指向 notify-worker。
- `CHANNEL_HEALTH_ALERT_ENABLED=true` 且 notify 通道已配置。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_QUOTA_ALERT_ENABLED` | `false` | 开启额度告警评估 |
| `SUBSCRIPTION_QUOTA_ALERT_INTERVAL` | `10m` | 评估间隔 |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | `event` | 告警投递类型 |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | `[""]` | 接收人（JSON 数组或逗号分隔） |

### 告警类别
| kind | 条件 |
| --- | --- |
| `exhausted` | 本地额度窗口已耗尽 |
| `near_exhausted` | 上游 snapshot 主窗口使用率 ≥ 80% |
| `writeback_down` | 额度快照回写已暂停 |
| `idle` | 超过 24h 无用量 |

### 去重
- 同一 account + kind 在去重窗口（默认 1h）内只投递一次，通过 metadata `last_quota_alert_kind` / `last_quota_alert_at` 记录。

### 告警分层
管理后台风险告警列表每条带 `category` 字段：
- `channel`：渠道余额/毛利告警
- `billing`：对账差异告警
- `subscription_account`：订阅账号不可调度 / 回写暂停告警

关闭订阅账号告警（`SUBSCRIPTION_QUOTA_ALERT_ENABLED=false`）不影响渠道健康和对账告警。

### 指标
- `micro_one_api_subscription_quota_alerts_total{kind,result}`：sent / deduped / error

## 验证命令
```bash
make test-unit
cd web && npm test && npm run lint
git diff --check
```
