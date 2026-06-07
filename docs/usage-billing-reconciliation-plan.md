# 用量统计 / 成本分析 / 对账能力 — 调研与改进计划

> 分支：`feature/usage-billing-reconciliation`
> 范围：`billing-service`、`log-service`、`channel-service` 及 `admin` / `relay` 相关聚合链路

## 一、架构现状

微服务架构（Kratos 框架），相关三块：

| 服务 | 职责 | 关键表 |
|---|---|---|
| **billing-service** | 配额预留/提交、定价计算、账本（ledger）、支付、对账 | `users`, `billing_ledgers`, `billing_reservations`, `payment_orders`, `reconciliation_runs`, `system_options` |
| **log-service** | 请求/用量日志写入与查询 | `logs` |
| **channel-service** | 上游渠道账户、余额、已用配额 | `channels`, `abilities` |
| **admin / relay** | relay 产生用量事件；admin 聚合展示 | — |

**数据双写**：每次请求结束，relay-gateway 同时写两处 —

- `billing_ledgers`（权威账本，`type=consume`，负数金额，含 prompt/completion tokens、channel_id、elapsed_time）—— `internal/billing/biz/billing.go:198`
- `logs`（`level=consume`，冗余的用量副本）—— `internal/relay/server/http.go:1107`

## 二、已有能力

1. **按日/按模型聚合**：`AggregateLedgerByDate`（`internal/billing/data/ledger_repo.go:235`）—— 但在内存里 group by，且 **admin 层从未调用**（grep 无结果）。
2. **用户自助用量**：`GET /api/log/self/stat` 走 SQL `GROUP BY day, model`（`internal/log/data/data.go:251`），是目前唯一真正落库聚合的统计。
3. **渠道余额刷新**：`RefreshChannelBalance` 适配 OpenAI/DeepSeek/OpenRouter/SiliconFlow，记录失败计数与自动禁用（`internal/admin/service/admin.go:780`）。
4. **对账**：`RunReconciliation`（`internal/billing/biz/reconciliation.go:70`）—— 仅做 ①清理过期预留 ②校验 `account.quota` 与 ledger 净额之差（容差 100）。

## 三、核心缺陷（按严重度）

### 🔴 1. Admin 全局统计是"抽样"，不是真统计
`GetLogStats`（`internal/admin/service/admin.go:1492`）拉最多 1000 条日志在内存里求和。dashboard 的 `request_count` / `quota_used` 在数据量大时**系统性偏低且不可信**。`AggregateLedgerByDate` 已存在却没接上。

### 🔴 2. 没有成本/利润分析维度
账本只记录向用户**计费**的 quota，没有记录该请求的**上游实际成本**。`channels` 有 `balance`/`used_quota`，但与单次请求无关联，无法回答"某模型/某渠道是赚是亏""毛利多少"。成本分析能力实质缺失。

### 🟠 3. 对账只对内（ledger vs account），不对外（渠道）
没有"本地记录的渠道用量/扣费 vs 上游账单/余额变化"的核对。`channel.used_quota` 是否随每次请求累加都未确认，渠道级对账空白。

### 🟠 4. 聚合只有"按用户+日+模型"，缺关键维度
无法按**渠道**、按 **token（API key）**、按**分组**、按**小时**聚合；admin 无跨用户 Top-N（高消耗用户/模型/渠道）。`AggregateLedgerByDate` 也仅限单用户。

### 🟡 5. 双写不一致风险 + 内存聚合性能
ledger 与 logs 可能漂移（两次独立 RPC）；`billing_ledgers` 无 `(model_name, created_at)`、无 `channel_id` 索引，聚合查询全表扫。内存 group-by 在大表上 OOM 风险。

### 🟡 6. 渠道 `used_quota` 累加链路未验证
relay 提交时是否回写 channel 用量未见证据 —— 若没有，渠道成本侧数据是死的。

## 四、改进计划（分阶段）

### Phase 1 — 让统计可信（地基，必做）
- **DB 层聚合下沉**：给 `ledger_repo` 增加多维聚合方法（按 user / channel / model / token / 分组 / 小时|日），用 SQL `GROUP BY` 替代内存循环。
- **接通 admin**：`GetLogStats` / dashboard summary 改为调用 billing 的真实聚合（跨用户），废弃 1000 条抽样。
- **加索引**：migration `028` 给 `billing_ledgers` 加 `(created_at)`、`(channel_id, created_at)`、`(model_name, created_at)`。
- **新增 RPC**：`AggregateUsage(group_by, filters, time_range)` 统一聚合入口。

### Phase 2 — 成本与利润分析（新能力）
- ledger 增列 `upstream_cost`（migration `029`）：提交时按渠道侧定价算出上游成本写入。
- 渠道侧定价配置（每渠道每模型的进价），区别于现有面向用户的 `ModelPrice`（售价）。
- 新增成本分析接口：`收入(计费quota) - 成本(upstream_cost) = 毛利`，可按模型/渠道/时间下钻 + Top-N。
- 验证并补全 relay → channel 的 `used_quota` 回写。

### Phase 3 — 渠道对账（扩展对账）
- `RunReconciliation` 增加渠道维度：本地累计渠道用量/成本 vs `RefreshChannelBalance` 拉到的余额变化，超阈值告警。
- 双写一致性校验：定期比对 `billing_ledgers` 与 `logs` 的 consume 记录数/金额，漂移入 `reconciliation_runs`。
- 对账结果结构化存储已有（`reconciliation_runs`），扩展 discrepancy 类型。

### Phase 4 — 前端与告警（可选）
- `web/src/pages/DashboardPage.tsx` 增加成本/利润、渠道余额健康、Top-N 图表。
- 余额过低 / 渠道亏损 / 对账差异 → 走已有 `notify` 服务告警。

## 五、建议起点

Phase 1 性价比最高 —— 它修复"dashboard 数字是错的"这个最严重问题，且全部基于已有结构（`AggregateLedgerByDate` 扩展 + 接线 + 索引），风险低、无需改数据模型。

## 附：关键代码位置索引

| 主题 | 位置 |
|---|---|
| 定价计算（双路径：ModelPrice / 比率） | `internal/billing/biz/billing.go:527` |
| 配额预留 / 提交 / 释放 | `internal/billing/biz/billing.go:79` / `:151` / `:261` |
| 账本写入（consume ledger） | `internal/billing/biz/billing.go:198` |
| 按日/模型聚合（内存，未接 admin） | `internal/billing/data/ledger_repo.go:235` |
| 动态定价配置（system_options） | `internal/billing/data/pricing_config_repo.go:19` |
| 对账主流程 | `internal/billing/biz/reconciliation.go:70` |
| 用量日志写入（relay → log-service） | `internal/relay/server/http.go:1107` |
| 用户自助用量 SQL 聚合 | `internal/log/data/data.go:251` |
| Admin 抽样统计（待替换） | `internal/admin/service/admin.go:1492` |
| Admin summary 聚合面板 | `internal/admin/server/http.go:447` |
| 渠道余额刷新（多 provider 适配） | `internal/admin/service/admin.go:780` |
| 渠道成本字段（balance/used_quota） | `internal/channel/biz/channel.go:31` |
| Token 用量提取（上游 usage 解析） | `internal/relay/server/http.go:1773` |
