# 上游账号额度后续工作说明

本文档用于后续接手“上游订阅账号额度”相关工作。它说明当前实现边界、关键文件、数据流，以及还需要继续做什么。

## 背景

本项目有两类“额度”：

- **用户订阅套餐额度**：限制用户能消费多少，落在 `subscription_groups`、`subscription_plans`、`user_subscriptions`，由 billing/subscription 链路维护。
- **上游账号额度**：限制某个 OAuth 上游账号还能被调度多少，落在 `subscription_accounts` 和 `account_quota_snapshots`，由 channel-service 选路、relay-gateway 回写。

第二阶段补的是后者的本地额度闭环。目标是让系统能像 sub2api 一样给不同上游账号配置本地总额、日额、周额和倍率，并在账号耗尽时自动跳过该账号。

## 当前已实现

当前分支提交 `09d4bdb add subscription account local quotas` 已完成以下能力：

- `subscription_accounts` 增加本地额度字段：
  - `quota_limit_usd` / `quota_used_usd`
  - `quota_5h_limit_usd` / `quota_5h_used_usd` / `quota_5h_window_start`
  - `quota_daily_limit_usd` / `quota_daily_used_usd` / `quota_daily_window_start`
  - `quota_weekly_limit_usd` / `quota_weekly_used_usd` / `quota_weekly_window_start`
  - `quota_reset_strategy` / `quota_timezone`
  - `rpm_limit`
  - `session_window_limit_usd`
  - `rate_multiplier`
- `SelectSubscriptionAccount` 会跳过本地总额、5h、24h、7d 任一耗尽的账号。默认日/周额度是滚动 24h/7d；`quota_reset_strategy=fixed` 时日/周额度按 `quota_timezone` 的自然日/自然周重置。
- relay-gateway 会在订阅账号出站前按 `rpm_limit` 做 60 秒滚动 RPM 限制，超限账号只在本次请求内 failover，不触发 runtime cooldown。
- relay-gateway 会按 `session_hash + account_id` 记录 session 成本窗口；同一会话在某账号达到 `session_window_limit_usd` 后跳过该账号，不触发 runtime cooldown。
- relay-gateway 在 billing commit 成功后，按 `CommitQuotaResponse.committed_amount` 折算 USD，调用 channel-service 回写账号用量。
- channel-service 按 `cost_usd * rate_multiplier` 累计账号本地用量。
- `subscription_account_quota_events` 记录账号额度回写事件，按 `reservation_id + subscription_account_id + cost_source` 幂等去重，并保存账号倍率快照。
- channel-service 暴露账号额度事件聚合，admin summary / 成本分析页可以按 `subscription_account_id` 对照 ledger 消费与账号侧额度事件。
- admin 订阅账号页面可以查看、编辑额度/倍率，并可重置本地用量。
- 新增管理接口：`POST /v1/subscription-accounts/{account_id}/reset-quota`，`scope` 支持 `total`、`5h`、`daily`、`weekly`、`all`。
- 第四阶段已补 5h 成本窗口、RPM、session 成本窗口、固定重置周期和时区配置。

## 关键文件

业务模型和调度：

- `internal/channel/biz/channel.go`
  - `SubscriptionAccount` 持有本地额度字段。
  - `IsSchedulableAt` 会调用 `LocalQuotaExceededAt`。
  - `RecordSubscriptionAccountQuotaUsage` 和 `ResetSubscriptionAccountQuota` 是 channel usecase 入口。
- `internal/relay/biz/account_rpm.go`
  - `MemoryAccountRPMLimiter` / `RedisAccountRPMLimiter` 按账号限制每分钟请求数。
  - Redis 可用时多 relay 副本共享 RPM 窗口，Redis 异常时降级到进程内 limiter。
- `internal/relay/server/subscription_session_window.go`
  - 按 `group + session_hash + account_id` 记录 session 成本窗口。
  - 使用 `reservation_id` 幂等去重；Redis 可用时跨 relay 副本共享，异常时降级到进程内窗口。

数据库和迁移：

- `migrations/051_add_subscription_account_local_quota.sql`
- `migrations/052_create_subscription_account_quota_events.sql`
- `migrations/053_add_subscription_account_5h_quota.sql`
- `migrations/054_add_subscription_account_rpm_limit.sql`
- `migrations/055_add_subscription_account_session_window_limit.sql`
- `migrations/056_add_subscription_account_quota_reset_config.sql`
- `migrations/postgres/000_create_full_schema.sql`
- `migrations/sqlite/000_create_full_schema.sql`
- `internal/channel/data/data.go`
  - `recordSubscriptionAccountQuotaUsageDB` 使用行锁读取账号并累计用量。
  - `subscription_account_quota_events` 在同一事务内插入，重复事件不再次累计账号用量。
  - `AggregateSubscriptionAccountQuotaEvents` 按账号汇总 `cost_usd`、`charged_usd`、平均倍率、事件数和最近发生时间。
  - 5h 窗口固定为滚动 5h；daily / weekly 默认滚动 24h、7d，`quota_reset_strategy=fixed` 时按 `quota_timezone` 的 00:00 和周一 00:00 重置，非法时区回退 `UTC`。

RPC / API：

- `api/common/v1/common.proto`
  - `SubscriptionAccountInfo` / `SubscriptionAccountSummary` 暴露本地额度字段。
- `api/channel/v1/channel.proto`
  - `RecordSubscriptionAccountQuotaUsage`
  - `AggregateSubscriptionAccountQuotaEvents`
  - `ResetSubscriptionAccountQuota`
- `api/admin/v1/admin.proto`
  - `ResetSubscriptionAccountQuota`
- `internal/channel/service/channel.go`
- `internal/admin/service/admin.go`
- `internal/admin/server/http.go`

relay 回写：

- `internal/relay/server/http.go`
  - `commitQuotaWithResponse` 保留 billing commit 响应。
  - `recordSubscriptionAccountQuotaUsage` 回写 channel-service。
  - `recordSubscriptionSessionWindowUsage` 在 commit 成功后记录 session 成本窗口。
- `internal/relay/server/openai_ws_forwarder.go`
  - WebSocket 多轮提交时补齐 `SubscriptionAccountID`。

管理端：

- `web/src/pages/admin/SubscriptionAccountsPage.tsx`
  - 列表展示本地总额、5h、24h、7d、RPM、session 窗口用量配置。
  - 创建/编辑支持额度和倍率。
  - 单账号支持重置用量。
- `web/src/pages/admin/CostAnalysisPage.tsx`
  - 展示订阅账号 ledger 成本。
  - 展示账号本地额度事件 TOP 5，并对照 ledger 成本/收入。

## 当前数据流

1. relay-gateway 完成选路，订阅账号请求会带上 `subscription_account_id`。
2. relay-gateway 在出站前检查账号并发、`rpm_limit` 和 `session_window_limit_usd`：
   - 并发满、RPM 满或当前 session 对该账号的成本窗口满时，跳过该账号并尝试同优先级/下一优先级账号。
   - RPM 限制是请求数窗口，不写入 5h / 24h / 7d 成本统计。
   - session 窗口按 `session_hash + account_id` 记录成本，不写入账号 5h / 24h / 7d 成本统计。
3. 请求成功后，relay-gateway 调用 billing-service `CommitQuota`。
4. billing-service 返回 `committed_amount`、`subscription_cost`、`balance_cost`。
5. relay-gateway 用 `committed_amount` 经 `quotaToUSD` 折算成本。
6. relay-gateway 调用 channel-service `RecordSubscriptionAccountQuotaUsage(account_id, cost_usd, reservation_id, cost_source)`，并按 `session_hash + account_id` 记录 session 窗口成本。
7. channel-service 先按 `reservation_id + subscription_account_id + cost_source` 插入 `subscription_account_quota_events`：
   - 新事件保存 `cost_usd`、`charged_usd`、`rate_multiplier`、`occurred_at`。
   - 重复事件直接返回，不再次累计账号用量。
8. channel-service 在 `subscription_accounts` 上累计：
   - 总用量：一直累加。
   - 5h 用量：窗口为空或超过 5h 时重开窗口。
   - 24h 用量：默认窗口为空或超过 24h 时重开窗口；fixed 策略下按配置时区自然日累计。
   - 7d 用量：默认窗口为空或超过 7d 时重开窗口；fixed 策略下按配置时区周一 00:00 起的自然周累计。
9. 后台成本分析通过 billing ledger 聚合和 channel 事件聚合，按 `subscription_account_id` 对照用户消费、ledger 成本和账号侧本地扣减。
10. 下一次选路时，channel-service 会跳过本地额度耗尽的账号。

## 已知边界

- 本地账号额度不是 billing 的强事务部分。billing commit 是权威计费，账号额度回写是成功后补记。
- 账号额度回写已有独立幂等事件表，但仅当请求携带 `reservation_id` 时生效；缺少 reservation 的回写仍会按普通用量累计。
- 当前账号并发限制和 runtime blocker 是 relay 进程内状态，多副本之间不共享。
- `rate_multiplier` 只影响上游账号本地用量累计，不改变用户侧订阅扣减或钱包扣减。
- billing ledger 不保存账号倍率快照；倍率快照保存在账号侧 `subscription_account_quota_events`，作为上游账号本地预算对账来源。

## 后续任务

### 1. 账号额度回写幂等（已完成）

目标：避免同一个 reservation 的成功 commit 被 relay 重试时重复累计账号本地用量。

已实现：

- 新增 `subscription_account_quota_events` 表。
- 唯一键使用 `reservation_id + subscription_account_id + cost_source`。
- channel-service 在事务中先插入事件，再累计 `subscription_accounts`。
- 事件字段包含：
  - `reservation_id`
  - `subscription_account_id`
  - `cost_usd`
  - `charged_usd`
  - `rate_multiplier`
  - `occurred_at`
  - `created_at`

涉及文件：

- `api/channel/v1/channel.proto`
- `internal/channel/biz/channel.go`
- `internal/channel/data/data.go`
- `internal/relay/server/http.go`
- `migrations/052_*`

已验证：

- 同一个 reservation 重放两次 `RecordSubscriptionAccountQuotaUsage`，账号用量只增长一次。
- 并发重放时只有一个事件插入成功。

### 2. ledger 保存账号倍率快照（已按方案 A 完成）

目标：让后续对账能复原“当时按哪个账号倍率累计了上游账号成本”。

现状：

- billing ledger 已有 `subscription_account_id`、`subscription_cost`、`balance_cost`。
- 但 billing-service 不知道 channel-service 的 `rate_multiplier`，也没有保存账号额度扣减快照。

已采用方案：

- relay 在 commit 后回写账号额度事件，事件表作为账号侧对账来源，不改 billing ledger。
- channel-service 新增 `AggregateSubscriptionAccountQuotaEvents`，按账号聚合事件成本、倍率后成本、平均倍率、事件数和最近发生时间。
- admin summary 新增 `top_subscription_account_quota_events`，并在 `top_subscription_accounts` 中补充同账号的事件聚合字段。
- 成本分析页新增“账号本地额度事件 TOP 5”，用于对照账号事件与 ledger 成本/收入。

已验证：

- 后台成本分析能按 `subscription_account_id` 追踪用户消费、账号额度事件和 ledger 消费。

### 3. 多副本账号并发与 runtime block（已完成）

目标：多 relay 实例部署时，同一个上游账号的并发限制和短 TTL 冷却状态可共享。

已实现：

- `AccountConcurrencyLimiter` 已抽为接口，默认仍使用进程内 `MemoryAccountConcurrencyLimiter`。
- 新增 `RedisAccountConcurrencyLimiter`，使用 Redis ZSET 按账号共享并发槽位：
  - key：`subscription_account:concurrency:{account_id}`
  - 获取槽位时原子清理过期租约、检查当前并发、写入新租约。
  - 槽位带 TTL，并在请求执行期间后台续租，防止长流式请求中途过期。
  - release 幂等，正常结束时删除槽位；进程崩溃时由 Redis TTL 回收。
- Redis 并发 limiter 在 Redis 命令失败时降级到原内存 limiter，并通过 `micro_one_api_relay_account_concurrency_fallback_total` 标记降级原因。
- runtime blocker 已使用 RedisRuntimeBlocker，Redis 可用时共享短 TTL 冷却状态，Redis 不可用时保持内存 blocker 行为。
- relay-gateway wire 层使用现有 Redis client 同时接入 Redis runtime blocker 和 Redis account concurrency limiter。

涉及文件：

- `internal/relay/biz/account_concurrency.go`
- `internal/relay/biz/runtime_blocker.go`
- `internal/relay/server/http.go`
- `internal/relay/server/http_adaptor.go`
- `cmd/relay-gateway/wire_gen.go`
- `internal/pkg/metrics/subscription.go`

已验证：

- 两个 Redis limiter 实例共享同一个 Redis store，同一账号实际并发不超过账号 `concurrency`。
- Redis acquire 异常时回落到内存 limiter，请求不会因 Redis 异常被全局阻断。
- runtime blocker Redis 共享、过期和 fail-open 已有单元测试覆盖。

### 4. 更细的上游窗口策略（5h / RPM / session window / fixed reset 已完成）

目标：如果运营需要，补 sub2api 风格的 5h 成本窗口、RPM、会话窗口等策略。

现状：

- 本地额度有总额、滚动 5h，以及可配置为滚动或固定周期的 daily / weekly。
- `quota_reset_strategy` 支持 `rolling` / `fixed`，默认 `rolling` 保持兼容。
- `quota_timezone` 是 fixed 策略使用的 IANA 时区，默认 `UTC`，非法时区回退 `UTC`。
- fixed 策略下 daily 在配置时区 00:00 重置，weekly 在配置时区周一 00:00 重置。
- relay-gateway 支持账号级 60 秒滚动 RPM 限制。
- relay-gateway 支持同一 `session_hash` 在同一账号上的成本窗口限制。
- Codex 5h/7d 快照已通过 `account_quota_snapshots` 独立记录。

建议：

- 先确认真实运营需求，不要直接把所有 sub2api 字段搬进调度热路径。
- 如果要做，优先从只读展示和告警开始，再进入调度排除。

已实现字段：

- `quota_5h_limit_usd`
- `quota_5h_used_usd`
- `quota_5h_window_start`
- `rpm_limit`
- `session_window_limit_usd`
- `quota_reset_strategy`
- `quota_timezone`

验证：

- 同一账号在 5h 窗口耗尽后被跳过，窗口过期后恢复。
- RPM 限制只影响当前短窗口，不污染 daily/weekly 统计。
- 同一账号 RPM 满后不请求上游、不触发 runtime cooldown，并 failover 到 sibling 账号。
- 同一 session 在某账号达到 session 窗口成本上限后不请求该账号、不触发 runtime cooldown，并 failover 到 sibling 账号。
- fixed daily / weekly 会按配置时区边界重置，weekly 以周一 00:00 为周期起点。

### 5. 管理端批量能力（已完成）

目标：账号数量较多时降低运维成本。

已实现：

- 单账号可以编辑额度、倍率和重置用量。
- 管理端列表支持多选订阅账号。
- 支持批量重置 `total`、`5h`、`daily`、`weekly`、`all` 用量。
- 支持对选中账号批量应用额度模板：
  - `quota_limit_usd`
  - `quota_5h_limit_usd`
  - `quota_daily_limit_usd`
  - `quota_weekly_limit_usd`
  - `rate_multiplier`
  - `rpm_limit`
  - `session_window_limit_usd`
  - `quota_reset_strategy`
  - `quota_timezone`
- 列表支持按当前已加载账号筛选“本地额度耗尽”“即将耗尽”“最近无用量”。
- 新增 HTTP 管理接口：
  - `POST /v1/subscription-accounts/batch-reset-quota`
  - `POST /v1/subscription-accounts/batch-quota-template`
  - `/api/subscription-accounts/...` 别名同步可用。

涉及文件：

- `internal/admin/server/http.go`
- `web/src/pages/admin/SubscriptionAccountsPage.tsx`
- `internal/admin/server/http_test.go`
- `web/src/pages/admin/SubscriptionAccountsPage.test.tsx`

验证：

- 批量操作只影响选中的账号。
- 批量重置后列表统计和后端查询一致。

## 推荐实施顺序

1. 账号额度回写幂等已完成。
2. ledger / 事件对账视图已按方案 A 完成。
3. 多副本部署前做 Redis 并发和 blocker。
4. 更细窗口策略和管理端批量能力等运营需求明确后再做。

## 回归测试清单

- `go test ./internal/channel/... ./internal/admin/... ./internal/relay/...`
- `go test ./test/integration`
- `make test-unit`
- `npm run build`（在 `web/` 下）
- e2e 建议：
  - 两个同优先级订阅账号，一个日额度耗尽，一个未耗尽，请求只落到可用账号。
  - 成功订阅账号请求后，billing ledger 与 `subscription_accounts.quota_*_used_usd` 同步增长。
  - 重置 daily 后账号恢复可选，总额不被清零。
