# 订阅账号额度治理 Runbook

> 对应 `docs/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 适用版本：v0.5.0+（含订阅账号本地额度、5h、RPM、会话窗口、reset 配置、批量管理）。
> 相关文档：[订阅账号配置与导入实操指南](./subscription-account-setup-guide.md)、[上游账号额度后续工作说明](./subscription-account-quota-follow-up.md)、[Redis 多副本部署 Runbook](./subscription-redis-multi-replica-runbook.md)。

本 runbook 让新部署人员只按本文档即可完成订阅账号的额度配置、重置、异常恢复与批量治理，并验证账号耗尽后被跳过、恢复后重新进入选路。

> 本 runbook 描述的是当前分支（v0.5.0）已落地的「人工 + API」治理闭环。路线图阶段 1 的「后台自动 reset / 自动恢复 / 额度告警」在独立分支 `feat/subscription-account-ops-automation` 上，未合并进当前基线；待合并后会在此处补「自动化开关」小节。

## 一、前置条件

1. **服务健康**：relay-gateway、channel-service、billing-service `/healthz` 正常。
2. **迁移已执行**：`051_add_subscription_account_local_quota.sql` ~ `056_add_subscription_account_quota_reset_config.sql`，以及 `041_create_account_quota_snapshots.sql`、`052_create_subscription_account_quota_events.sql`。
   ```bash
   make migrate-status
   ```
3. **hybrid_adaptor 已开启**：`configs/relay-gateway.yaml` 的 `hybrid_adaptor.enabled: true`。
4. **至少一个启用状态的订阅账号**：见 [OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md)。
5. **管理员凭证** `ADMIN_TOKEN`。

## 二、必填配置

订阅账号额度字段（`subscription_accounts`，管理后台可编辑）：

| 字段 | 单位 | 说明 |
| --- | --- | --- |
| `quota_limit_usd` / `quota_used_usd` | USD | 账号总额度上限 / 已用，0=不限 |
| `quota_5h_limit_usd` / `quota_5h_used_usd` / `quota_5h_window_start` | USD / 秒 | 滚动 5h 窗口（固定 5h，不可配 fixed） |
| `quota_daily_limit_usd` / `quota_daily_used_usd` / `quota_daily_window_start` | USD / 秒 | 24h 窗口 |
| `quota_weekly_limit_usd` / `quota_weekly_used_usd` / `quota_weekly_window_start` | USD / 秒 | 7d 窗口 |
| `rpm_limit` | 次/分钟 | 60 秒滚动 RPM 限制，0=不限 |
| `session_window_limit_usd` | USD | 同 `session_hash` 在同账号上的成本窗口上限 |
| `rate_multiplier` | 倍率 | 账号本地用量累计倍率，默认 1。只影响上游账号本地预算，不改用户侧扣费 |
| `quota_reset_strategy` | `rolling` / `fixed` | daily/weekly 重置策略，默认 `rolling` |
| `quota_timezone` | IANA 时区 | fixed 策略使用的时区，默认 `UTC`，非法回退 `UTC` |

选路跳过条件（`internal/channel/biz/channel.go` 的 `LocalQuotaExceededAt`）：总额、5h、daily、weekly 任一窗口 `used >= limit`（limit>0）即跳过该账号。

runtime block（relay-gateway 进程内 / Redis 共享）冷却时长，由 `configs/relay-gateway.yaml` 的 `hybrid_adaptor.runtime_block` 调：

| 配置项 | 默认 | 触发 |
| --- | --- | --- |
| `rate_limited_duration` | 5s | 上游 429 |
| `unauthorized_duration` | 2m | 上游 401 |
| `server_error_duration` | 2m | 上游 5xx |
| `overloaded_duration` | 30s | 上游 529 |
| `active_gauge_interval` | 30s | Redis blocker active-block gauge 上报周期 |

## 三、重置额度

### 3.1 单账号重置

```bash
curl -s -X POST http://127.0.0.1:3000/v1/subscription-accounts/{id}/reset-quota \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"scope":"all"}'
```

`scope` 取值（`resetSubscriptionAccountQuota`）：

| scope | 清零字段 |
| --- | --- |
| `total` | `quota_used_usd` |
| `5h` | `quota_5h_used_usd` + `quota_5h_window_start` |
| `daily` | `quota_daily_used_usd` + `quota_daily_window_start` |
| `weekly` | `quota_weekly_used_usd` + `quota_weekly_window_start` |
| `all` | 以上全部 |

留空默认 `all`。

### 3.2 批量重置

```bash
curl -s -X POST http://127.0.0.1:3000/v1/subscription-accounts/batch-reset-quota \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"account_ids":[1,2,3],"scope":"daily"}'
```

返回 `{success, message, updated_count, failed_ids}`。逐个调用单账号 reset 聚合，部分失败不影响其他账号。

### 3.3 批量应用额度模板

```bash
curl -s -X POST http://127.0.0.1:3000/v1/subscription-accounts/batch-quota-template \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "account_ids":[1,2,3],
    "template":{
      "quota_daily_limit_usd":20,
      "quota_weekly_limit_usd":100,
      "rate_multiplier":1,
      "rpm_limit":60,
      "session_window_limit_usd":5,
      "quota_reset_strategy":"fixed",
      "quota_timezone":"Asia/Shanghai"
    }
  }'
```

模板字段全部可选（`*float64` / `*int32` / `*string`，只更新传入的字段）。管理后台列表支持多选 + 模板应用。

## 四、异常账号恢复（v0.5.0 基线）

### 4.1 异常分层

| 异常类型 | 来源 | 自动恢复？ | 恢复方式 |
| --- | --- | --- | --- |
| 临时上游错误（429/5xx/529） | relay runtime block | 是（TTL 到期） | `MemoryRuntimeBlocker` / `RedisRuntimeBlocker` TTL 过期自动从 block 集合移除 |
| 授权异常（401/403） | relay runtime block（2m TTL） | 是（TTL 到期），但 **上游仍会拒** | 需重新 OAuth 绑定拿新 token，否则恢复后立刻又被 block |
| 本地额度耗尽 | `LocalQuotaExceededAt` | 等窗口 reset | 调 §三 的 reset，或等 fixed/rolling 窗口自然滚动 |
| Codex snapshot 耗尽 | `ShouldAutoPause` → `AutoPauseAccount` | 否（`status=2`） | 等 Codex 上游 snapshot reset 后人工启用，或见下 |
| AutoPause（任意原因） | `AutoPauseAccount` | 否 | 人工 `PUT /v1/subscription-accounts/{id}/status` 启用 |

### 4.2 手动恢复

```bash
# 启用被 AutoPause 的账号
curl -s -X PUT http://127.0.0.1:3000/v1/subscription-accounts/{id}/status \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"status":1}'
```

`ChangeSubscriptionAccountStatus` 改 `subscription_accounts.status`。

### 4.3 清理临时不可调度

`ClearTempUnschedulable`（channel repo）清 `rate_limited_until` + `last_error`。管理后台单账号编辑可触发；API 侧通过 `PUT /v1/subscription-accounts/{id}` 更新。

> v0.5.0 没有「自动恢复扫描」后台任务（那是阶段 1 的 `AccountRecoverySweeper`）。当前基线靠 runtime block TTL 自然过期 + 人工处理 AutoPause。

## 五、验证

### 5.1 账号额度状态

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e "
SELECT id,name,status,
       quota_limit_usd,quota_used_usd,
       quota_5h_limit_usd,quota_5h_used_usd,quota_5h_window_start,
       quota_daily_limit_usd,quota_daily_used_usd,quota_daily_window_start,
       quota_weekly_limit_usd,quota_weekly_used_usd,quota_weekly_window_start,
       rpm_limit,session_window_limit_usd,rate_multiplier,
       quota_reset_strategy,quota_timezone,
       rate_limited_until,last_error
 FROM subscription_accounts ORDER BY id\G"
```

### 5.2 耗尽即跳过

把某账号 `quota_daily_limit_usd` 设小（如 0.01），消费一次使其 `quota_daily_used_usd >= limit`，再请求应 failover 到 sibling 账号（或返回无可用账号）。查 relay-gateway 日志 / `RelayAccountPoolChecksTotal{reason="local_quota_exceeded"}`。

### 5.3 reset 后恢复

对耗尽的账号调 `POST /v1/subscription-accounts/{id}/reset-quota -d '{"scope":"daily"}'`，`quota_daily_used_usd` 归零、`window_start` 归零，下一次请求重新选中该账号。

### 5.4 额度事件幂等

```bash
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e "
SELECT subscription_account_id,reservation_id,cost_source,cost_usd,charged_usd,rate_multiplier,occurred_at
 FROM subscription_account_quota_events
 WHERE subscription_account_id=<id> ORDER BY occurred_at DESC LIMIT 10;"
```

同一 `reservation_id + subscription_account_id + cost_source` 只有一条（唯一键去重）。重复回写不重复累计。

### 5.5 Codex 自动暂停

当 Codex 响应解析出 primary quota `used_percent >= 95` 或 secondary `>= 100`（`ShouldAutoPause`，阈值硬编码 95/100），relay-gateway 调 `AutoPauseAccount` 把账号 `status` 改为 2。查 `account_quota_snapshots` 表与 `micro_one_api_relay_codex_quota_used_percent` 指标。

## 六、常见故障与恢复

### 6.1 账号一直不被选中

**原因**：`status!=1`、本地额度耗尽、runtime block 未过期、`group` 与用户分组不匹配、`models` 不含请求模型名。

**排查**：
1. 查 §5.1 的状态：`status=1`？额度窗口 `used < limit`？`rate_limited_until < now`？
2. 确认 `group` 与用户 `group` 一致。
3. 确认 `subscription_account_abilities` 有对应 `model` 行。
4. 看 `RelayAccountPoolChecksTotal` 的 reason 分布。

**恢复**：reset 耗尽窗口 / 启用账号 / 修正 group 或 models。

### 6.2 reset 后立刻又被跳过

**原因**：上游 Codex snapshot 仍耗尽（`AutoPause`），或 runtime block TTL 未到。

**恢复**：Codex snapshot 耗尽需等上游 reset（看 `account_quota_snapshots.reset_after_seconds`）；runtime block 等配置 TTL 过期，或临时调小 `runtime_block.*_duration`。

### 6.3 fixed 重置没按时触发

**原因**：v0.5.0 基线没有自动 fixed reset 扫描任务（那是阶段 1 的 `QuotaResetSweeper`）。当前 fixed 策略只在「下一次回写」时按 `FixedQuotaWindowStart` 判断窗口是否落后，落后则重开窗口（`EffectiveFixedQuotaWindowUsedUSD`）。

**恢复**：若需要「到点自动清零」而非「下次回写才清零」，需启用阶段 1 的后台任务（合并后），或用 cron 调 §三 的 reset 接口。

### 6.4 批量操作误伤

**原因**：`account_ids` 传错。

**恢复**：批量 reset 只清零用量不清零 limit，误清可用量可从 `subscription_account_quota_events` 重新聚合回填（`AggregateSubscriptionAccountQuotaEvents`）。批量模板误改 limit 需逐个改回。

### 6.5 RPM 满后上游 429

**原因**：RPM 限制是请求数窗口，只在当前 relay 进程/Redis 60s 滚动窗口内生效，不写 5h/daily/weekly 成本。RPM 满后 failover 到 sibling，不冷却该账号（不触发 runtime block）。若 sibling 也满，返回无可用账号。

**恢复**：调大 `rpm_limit`，或增加 sibling 账号。

## 七、指标

| 指标 | 说明 |
| --- | --- |
| `micro_one_api_relay_account_pool_checks_total{reason}` | 账号池检查次数，reason 含 `runtime_blocked` / `local_quota_exceeded` / `concurrency_full` 等 |
| `micro_one_api_relay_runtime_blocks_total{reason}` | runtime block 次数 |
| `micro_one_api_relay_runtime_block_active` | 当前 active block 数（Redis gauge） |
| `micro_one_api_relay_account_concurrency_fallback_total{reason}` | 并发 limiter Redis 降级次数 |
| `micro_one_api_relay_account_rpm_fallback_total{reason}` | RPM limiter Redis 降级次数 |
| `micro_one_api_relay_codex_quota_used_percent{window}` | Codex 主/副窗口使用率 |
| `micro_one_api_relay_codex_quota_snapshots_total{result}` | Codex 快照解析次数 |


> ⚠️ **部署必读（阶段2治理后台任务默认全关）**
>
> 以下三个后台治理任务在 `cmd/channel-service/wire_gen.go` 中默认 **关闭**
> (`envBool(..., false)`)，运维未显式开启则整套治理零生效：
>
> | 环境变量 | 默认 | 作用 |
> |---|---|---|
> | `SUBSCRIPTION_QUOTA_RESET_ENABLED` | `false` | fixed 策略 daily/weekly 额度自动重置 |
> | `SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED` | `false` | 临时禁用账号 TTL 到期后自动恢复（codex snapshot 耗尽等） |
> | `SUBSCRIPTION_QUOTA_ALERT_ENABLED` | `false` | 额度耗尽/临近告警（复用 notify-worker） |
>
> 生产环境**必须**显式设置这三个环境变量为 `true` 才能让阶段2治理生效。
> 不开启时：fixed 策略账号不会自动重置、codex 耗尽账号不会自动恢复、额度告警不投递。
