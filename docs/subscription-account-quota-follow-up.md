# 上游账号额度后续工作说明

本文档用于后续接手“上游订阅账号额度”相关工作。它不是第二阶段完成记录，而是说明当前实现边界、关键文件、数据流，以及还需要继续做什么。

## 背景

本项目有两类“额度”：

- **用户订阅套餐额度**：限制用户能消费多少，落在 `subscription_groups`、`subscription_plans`、`user_subscriptions`，由 billing/subscription 链路维护。
- **上游账号额度**：限制某个 OAuth 上游账号还能被调度多少，落在 `subscription_accounts` 和 `account_quota_snapshots`，由 channel-service 选路、relay-gateway 回写。

第二阶段补的是后者的本地额度闭环。目标是让系统能像 sub2api 一样给不同上游账号配置本地总额、日额、周额和倍率，并在账号耗尽时自动跳过该账号。

## 当前已实现

当前分支提交 `09d4bdb add subscription account local quotas` 已完成以下能力：

- `subscription_accounts` 增加本地额度字段：
  - `quota_limit_usd` / `quota_used_usd`
  - `quota_daily_limit_usd` / `quota_daily_used_usd` / `quota_daily_window_start`
  - `quota_weekly_limit_usd` / `quota_weekly_used_usd` / `quota_weekly_window_start`
  - `rate_multiplier`
- `SelectSubscriptionAccount` 会跳过本地总额、24h、7d 任一耗尽的账号。
- relay-gateway 在 billing commit 成功后，按 `CommitQuotaResponse.committed_amount` 折算 USD，调用 channel-service 回写账号用量。
- channel-service 按 `cost_usd * rate_multiplier` 累计账号本地用量。
- admin 订阅账号页面可以查看、编辑额度/倍率，并可重置本地用量。
- 新增管理接口：`POST /v1/subscription-accounts/{account_id}/reset-quota`，`scope` 支持 `total`、`daily`、`weekly`、`all`。

## 关键文件

业务模型和调度：

- `internal/channel/biz/channel.go`
  - `SubscriptionAccount` 持有本地额度字段。
  - `IsSchedulableAt` 会调用 `LocalQuotaExceededAt`。
  - `RecordSubscriptionAccountQuotaUsage` 和 `ResetSubscriptionAccountQuota` 是 channel usecase 入口。

数据库和迁移：

- `migrations/051_add_subscription_account_local_quota.sql`
- `migrations/postgres/000_create_full_schema.sql`
- `migrations/sqlite/000_create_full_schema.sql`
- `internal/channel/data/data.go`
  - `recordSubscriptionAccountQuotaUsageDB` 使用行锁读取账号并累计用量。
  - daily 窗口为滚动 24h，weekly 窗口为滚动 7d。

RPC / API：

- `api/common/v1/common.proto`
  - `SubscriptionAccountInfo` / `SubscriptionAccountSummary` 暴露本地额度字段。
- `api/channel/v1/channel.proto`
  - `RecordSubscriptionAccountQuotaUsage`
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
- `internal/relay/server/openai_ws_forwarder.go`
  - WebSocket 多轮提交时补齐 `SubscriptionAccountID`。

管理端：

- `web/src/pages/admin/SubscriptionAccountsPage.tsx`
  - 列表展示本地总额、24h、7d 用量。
  - 创建/编辑支持额度和倍率。
  - 单账号支持重置用量。

## 当前数据流

1. relay-gateway 完成选路，订阅账号请求会带上 `subscription_account_id`。
2. 请求成功后，relay-gateway 调用 billing-service `CommitQuota`。
3. billing-service 返回 `committed_amount`、`subscription_cost`、`balance_cost`。
4. relay-gateway 用 `committed_amount` 经 `quotaToUSD` 折算成本。
5. relay-gateway 调用 channel-service `RecordSubscriptionAccountQuotaUsage(account_id, cost_usd)`。
6. channel-service 在 `subscription_accounts` 上累计：
   - 总用量：一直累加。
   - 24h 用量：窗口为空或超过 24h 时重开窗口。
   - 7d 用量：窗口为空或超过 7d 时重开窗口。
7. 下一次选路时，channel-service 会跳过本地额度耗尽的账号。

## 已知边界

- 本地账号额度不是 billing 的强事务部分。billing commit 是权威计费，账号额度回写是成功后补记。
- 当前账号额度回写没有独立幂等事件表。正常路径依赖 billing reservation 幂等，极端重试下仍可能重复回写。
- 当前账号并发限制和 runtime blocker 是 relay 进程内状态，多副本之间不共享。
- `rate_multiplier` 只影响上游账号本地用量累计，不改变用户侧订阅扣减或钱包扣减。
- ledger 当前没有保存账号倍率快照。

## 后续任务

### 1. 账号额度回写幂等

目标：避免同一个 reservation 的成功 commit 被 relay 重试时重复累计账号本地用量。

建议方案：

- 新增 `subscription_account_quota_events` 表。
- 唯一键建议使用 `reservation_id + subscription_account_id + cost_source`。
- channel-service 在事务中先插入事件，再累计 `subscription_accounts`。
- 事件字段建议包含：
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

验证：

- 同一个 reservation 重放两次 `RecordSubscriptionAccountQuotaUsage`，账号用量只增长一次。
- 并发重放时只有一个事件插入成功。

### 2. ledger 保存账号倍率快照

目标：让后续对账能复原“当时按哪个账号倍率累计了上游账号成本”。

现状：

- billing ledger 已有 `subscription_account_id`、`subscription_cost`、`balance_cost`。
- 但 billing-service 不知道 channel-service 的 `rate_multiplier`，也没有保存账号额度扣减快照。

可选方案：

- 方案 A：relay 在 commit 后回写账号额度事件，事件表作为账号侧对账来源，不改 billing ledger。
- 方案 B：billing-service 引入 channel-service 客户端，在 commit 事务内或事务后读取账号倍率并写 ledger 快照。
- 方案 C：relay 把账号倍率快照作为 `CommitQuotaRequest` 的扩展字段传给 billing。

建议优先级：

- 先做方案 A。它对 billing 主链路侵入最小，也更符合“账号额度是上游账号侧本地预算”的边界。

验证：

- 后台成本分析能按 `subscription_account_id` 追踪用户消费、账号额度事件和 ledger 消费。

### 3. 多副本账号并发与 runtime block

目标：多 relay 实例部署时，同一个上游账号的并发限制和短 TTL 冷却状态可共享。

现状：

- `AccountConcurrencyLimiter` 是进程内信号量。
- runtime blocker 是 relay 内存状态。

建议方案：

- Redis key 示例：
  - 并发：`subscription_account:concurrency:{account_id}`
  - 冷却：`subscription_account:block:{account_id}`
- 并发获取需要 TTL，防止进程崩溃后永久占槽。
- Redis 不可用时应降级到当前内存逻辑或正常选路，不应阻断全部请求。

涉及文件：

- `internal/relay/biz/account_concurrency.go`
- `internal/relay/biz/relay.go`
- `internal/relay/server/http_adaptor.go`
- relay config / wire 配置

验证：

- 两个 relay 实例同时请求同一账号，实际并发不超过账号 `concurrency`。
- Redis 异常时请求仍可继续，但日志/metrics 标记降级。

### 4. 更细的上游窗口策略

目标：如果运营需要，补 sub2api 风格的 5h 成本窗口、RPM、会话窗口等策略。

现状：

- 本地额度有总额、滚动 24h、滚动 7d。
- Codex 5h/7d 快照已通过 `account_quota_snapshots` 独立记录。
- 尚未支持账号级 RPM、会话窗口成本、固定重置周期或时区配置。

建议：

- 先确认真实运营需求，不要直接把所有 sub2api 字段搬进调度热路径。
- 如果要做，优先从只读展示和告警开始，再进入调度排除。

可能字段：

- `quota_5h_limit_usd`
- `quota_5h_used_usd`
- `quota_5h_window_start`
- `rpm_limit`
- `session_window_limit_usd`

验证：

- 同一账号在 5h 窗口耗尽后被跳过，窗口过期后恢复。
- RPM 限制只影响当前短窗口，不污染 daily/weekly 统计。

### 5. 管理端批量能力

目标：账号数量较多时降低运维成本。

现状：

- 单账号可以编辑额度、倍率和重置用量。

后续可做：

- 按平台/分组批量设置额度模板。
- 批量重置 daily/weekly 用量。
- 列表筛选“本地额度耗尽”“即将耗尽”“最近无用量”。

验证：

- 批量操作只影响选中的账号。
- 批量重置后列表统计和后端查询一致。

## 推荐实施顺序

1. 先做“账号额度回写幂等”，因为它修正当前闭环里最明确的可靠性缺口。
2. 再做“ledger / 事件对账视图”，否则后续无法解释账号侧用量和账本侧消费差异。
3. 多副本部署前做 Redis 并发和 blocker。
4. 更细窗口策略和管理端批量能力等运营需求明确后再做。

## 回归测试清单

- `go test ./internal/channel/... ./internal/admin/... ./internal/relay/...`
- `make test-unit`
- `npm run build`（在 `web/` 下）
- e2e 建议：
  - 两个同优先级订阅账号，一个日额度耗尽，一个未耗尽，请求只落到可用账号。
  - 成功订阅账号请求后，billing ledger 与 `subscription_accounts.quota_*_used_usd` 同步增长。
  - 重置 daily 后账号恢复可选，总额不被清零。
