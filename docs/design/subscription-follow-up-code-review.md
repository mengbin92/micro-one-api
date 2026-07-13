# 订阅系统后续规划 Code Review 报告

> 审查依据：`docs/design/subscription-follow-up-roadmap.md`（7 个分支标 ✅ 已完成）
> 实际代码分支：`feat/subscription-productization-phase2`（commit `ade3f08`）
> 审查方式：静态只读分析 + 部分 Go 测试实跑验证（Relay 压测已编译运行通过）；2026-07-08 二次复核并修正 H4/M1/H10 表述
> 审查日期：2026-07-08

## 0. 重要前提说明

路线图声称的 7 个独立分支名（`feat/subscription-account-ops-automation`、`feat/subscription-plan-lifecycle`、`feat/subscription-renewal`、`feat/subscription-refund-reversal`、`feat/subscription-change`、`test/relay-subscription-stress`、`docs/subscription-production-runbook`）**在本地与远程 git 中均不存在**。所有实现实际合入在 `feat/subscription-productization-phase2` 单一分支，路线图的状态列应按"该分支内已实现的功能"理解，而非按分支名检索。

另外需注意：路线图"非目标"明确警告"余额/订阅权益双向回滚不一致"和"先反推账务模型"是高风险项。本次 review 实际发现，**退款/冲正的账务语义与续费/变更的执行路径存在多个真实的资金一致性缺陷**（见 §2.3），说明这些风险点已部分发生。

## 1. 验收标准总览

| 分支 / 阶段 | 关键验收项 | 结论 | 说明 |
|---|---|---|---|
| 1.1 quota reset | 重复 tick 不重复写事件（幂等） | ✅ 满足 | `reset_runs` 唯一键 `(account,scope,window_start)` 去重 |
| 1.1 quota reset | 非 fixed 不被修改 | ✅ 满足 | `UsesFixedQuotaReset()` 守卫 |
| 1.1 quota reset | 非法时区可定位 | ⚠️ 部分 | 静默回退 UTC，无日志/事件 |
| 1.1 quota reset | reset 写结构化事件 | ⚠️ 部分 | 写 `reset_runs` 而非 `quota_events`，分析链路看不到 |
| 1.2 异常恢复 | 401/403 不自动启用 | ✅ 满足 | `RecoveryPolicyManual` 永不自动恢复 |
| 1.2 异常恢复 | Codex snapshot 耗尽等 reset 再恢复 | ❌ **不满足** | 见 H1：被永久禁用 |
| 1.3 额度告警 | 去重窗口 / 关闭不影响其他告警 | ✅ 满足 | 复用 notify-worker，独立 env 门控 |
| 2.1 上下架 | 已下架不能新下单 / 快照发放 | ✅ 满足 | `plan_snapshot` 机制落地 |
| 2.1 上下架 | 用户侧只展示 for_sale 且 group 可用 | ⚠️ 部分 | 未过滤 group 可用性（M3） |
| 2.2 续费 | 延长 expires_at | ❌ **不满足** | 见 H3：剩余时长被截断 |
| 2.2 续费 | 重复回调不重复延长（幂等） | ✅/⚠️ | `MarkOrderPaid` 单路径 replay 满足；历史/异常 `paid + pending` 订单仍可经完成接口重复发放（M10） |
| 2.2 续费 | 过期未撤销策略固定 | ❌ **不满足** | 见 M2：随定时扫描竞态漂移 |
| 3.1 退款冲正 | 重复退款不重复返钱/冲正（幂等） | ✅ **满足** | 行锁 + 状态守卫 + ledger 唯一键双防护 |
| 3.1 退款冲正 | 退款金额正确 | ❌ **不满足** | 见 H5：用套餐标价而非实付 |
| 3.1 退款冲正 | 对账覆盖 | ⚠️ 部分 | 弱覆盖 + 实付/标价口径不一致误报（H6） |
| 3.2 订阅变更 | 唯一 active subscription | ❌ **不满足** | 见 H10：无 DB 唯一约束 |
| 3.2 订阅变更 | 升级收差价 | ❌ **不满足** | 见 H7：免费升级 + 审计造假 |
| 3.2 订阅变更 | 变更写账本 | ❌ **不满足** | 见 H8：不写 ledger |
| 3.2 订阅变更 | next_cycle 降级生效 | ❌ **不满足** | 见 H9：生产续费流永不触发 |
| 阶段3 Relay | 跨副本并发 cap / failover / fail-open | ✅ 满足 | 真实实现且压测通过 |
| 阶段3 Relay | 故障态并发一致性 | ⚠️ 部分 | 见 H11：fail-open 退化为 per-replica |
| 阶段4 文档 | runbook 交付 | ✅ 满足（文档层面） | 6 份 runbook 均已交付 |

## 2. 关键发现（按严重程度）

### 🔴 H1 — Codex snapshot 耗尽被永久禁用，违反"等待 reset 再恢复"【High】
- **位置**：`relay/server/http_adaptor.go:716-718` → `AutoPauseAccount` → `data.go:588-606` / `1094-1095`
- **现象**：Codex 账号 snapshot 用量达 95% 阈值（`codex.go:61 ShouldAutoPause`）即被 `AutoPauseAccount` 置 `status=disabled` 并强制 `recovery_policy=manual`。而 `account_ops.go:343-346` 对 `manual` 永不自动恢复。
- **后果**：违反验收标准 1.2"Codex quota snapshot 耗尽等待 snapshot reset 后再恢复"。账号一旦触发即永久不可用，必须人工 re-enable / OAuth 重绑。
- **根因**：见 H2。

### 🔴 H2 — `RecoveryPolicyQuota` / `RecoveryPolicyCodex` 未被生产写入，恢复逻辑不可达【High】
- **位置**：`data.go:2155-2176`（`stampRecoveryMetadata`）只产出 `manual/auto/rolling`；`AutoPauseAccount` 又强制覆盖为 `manual`；未发现生产路径会持久化 `quota/codex`，导致 `account_ops.go:347、399-407` 的专用恢复分支不可达。
- **现象**：本地 quota 耗尽的账号实际被当作 `rolling`（默认）走 TTL 分支（`account_ops.go:408-414`）。若其 `RateLimitedUntil==0`，每轮 tick 都 `clearMarkers` 写"已恢复"指标，但 `LocalQuotaExceededAt` 仍为 true → 账号依然不可调度。
- **后果**：指标虚高（"ttl_elapsed success"），管理员误判已恢复；这是 H1 的根因，也是 quota 自动恢复的半成品。

### 🔴 H3 — 续费延长逻辑错误：剩余时长被截断而非累加【High】
- **位置**：`internal/billing/biz/subscription_assigner.go:204-205`（回调 `assignFromSnapshot`）、`subscription_usecase.go:114-121`（`AssignOrExtend` 分支）
- **现象**：支付回调计算 `expiresAt = now + durationDays` 传绝对时间。`AssignOrExtend` 仅在 `req.ExpiresAt <= active.ExpiresAt`（剩余 > 续费时长）时累加；否则**直接覆盖**为 `now+duration`，丢掉剩余权益。
- **复现**：用户还剩 5 天，续费 30 天 → 得到 `now+30d` 而非 `now+35d`，白丢 5 天。"临近到期才续费"恰是最常见场景 → 必然触发。
- **对比**：管理端 `CompleteSubscriptionPurchase` 路径写的是 `base + durationDays`（`admin/service/subscription.go:292-301`），**两条发放入口语义不一致**，生产支付回调用的是错误的那条。

> 二次复核修正：原 H4「支付成功页 + 异步通知跨路径并发双发」不成立为通用 High。当前 `MarkOrderPaid` 在同一事务内行锁订单，并同时写 `status=paid` 与 `asset_issue_status=issued`；正常支付宝通知 replay 不会二次调用 assigner，完成接口看到 `issued` 会直接返回当前订阅。仍保留一个条件性风险见 M10：历史/异常 `paid + pending` 订单会被完成接口重复发放，因为该接口发放后不回写 `asset_issue_status=issued`。

### ⚪ H4（撤回）— 原跨路径双发结论已降级为 M10【已修正结论】
- **结论**：正常 `MarkOrderPaid` 支付回调路径具备订单行锁 + `status=paid` 幂等守卫，原 High 结论不应作为 P0 资金风险排期依据。

### 🔴 H5 — 退款金额用套餐名义 `PriceQuota` 而非实付 `MoneyCents`【High】
- **位置**：`internal/billing/biz/refund.go:151-154`
- **现象**：
  ```go
  refundQuota := order.MoneyCents / 100
  if snap.PriceQuota > 0 {
      refundQuota = snap.PriceQuota   // 一律用套餐名义额度覆盖实付金额
  }
  ```
- **后果**：订阅经外部支付（支付宝），`MarkOrderPaid` 不扣钱包，退款是 store-credit 模型（给钱包加钱）。加的是套餐标价额度而非用户实付。折扣/优惠券场景 → ①多退（资损）②少退（客诉）。

### 🔴 H6 — 对账 Step 7 实付/标价口径不一致，折扣订单恒误报【High】
- **位置**：`internal/billing/biz/reconciliation.go:412-418`
- **现象**：`refundedQuota = refundedCents/100`（实付）与 `reversalAmount = ΣPriceQuota`（标价）仅在全价无折扣时相等。任一退款订单有折扣 → 全局聚合永远不等 → `RefundInconsistencies` 每次对账都红，operator 对告警麻木。且只比较全局汇总、不逐笔配对。

### 🔴 H7 — 升级（immediate）不收差价，可免费升更贵套餐，审计谎称已扣【High】
- **位置**：`subscription_change.go:67-71`（注释"caller 负责钱包扣款"）+ 唯一调用方 `admin/service/subscription.go:361-365`（**完全不扣款**）
- **现象**：管理员把用户从便宜套餐切到贵套餐（immediate），订阅 group 立即变更，钱包分文未扣。且 `ChangeResult.ChargedQuota` 被返回并写入订阅元数据 `mergeChangeMetadata`（`subscription_change.go:117-126`）→ **审计记录"已扣差价"但实际从未扣**。
- **后果**：直接资损（少收升级费）+ 财务审计链路造假。一次 admin 操作即可让用户免费享更贵套餐。

### 🔴 H8 — 变更不写 change ledger 条目【High】
- **位置**：`subscription_change.go:17`（文档声明写 change ledger）但 `ChangeSubscription` 全程只 `UpdateSubscription`，无 `Ledger` 写入。
- **后果**：升级/降级无财务台账，与"账本审计链路"验收冲突，后续对账无法核对变更动作。

### 🔴 H9 — `next_cycle` 降级在真实续费中永不生效【High】
- **位置**：`subscription_change.go:148-159`（降级只写 `pending_change` 到 metadata）+ `subscription_usecase.go:106-113`（应用条件 `req.GroupID != active.GroupID`）
- **现象**：应用 pending_change 要求续费请求的 `GroupID` 与新 group 相同。但正常"同套餐续费"创建的订单 group 与当前 active 相同 → `if active.GroupID != req.GroupID` 永不进入 → pending_change 既不应用也不清除。
- **佐证**：测试 `TestChangeSubscription_PendingChangeAppliesOnRenewal`（`subscription_change_test.go:171-177`）**手动把续费请求 GroupID 设成新 group 才通过**，恰好暴露生产续费流（重订同 plan → 同 group）不会触发。
- **复现**：用户 pro 设置降级到 basic(next_cycle) → 到期同套餐续费 → 仍停在 pro，pending_change 永久残留。

### 🔴 H10 — 唯一 active subscription 仅靠非原子代码检查，无 DB 唯一索引【High】
- **位置**：`subscription_usecase.go:98-110`（`GetActiveSubscriptionByUser` 读后判断）+ `migrations/039_create_user_subscriptions.sql`（无 `(user_id, status=active)` 唯一约束）
- **现象**：读不加锁、创建/更新不在同一原子约束内。两笔并发支付回调（或"新购"与"续费/变更"竞态）可同时读到"无 active"，各自 `CreateSubscription` → **同一用户出现 2 个 active 订阅**，权益重复、用量按 `ORDER updated_at DESC` 选取会漂移。

### 🔴 H11 — Redis 故障 fail-open 后全局并发退化为 per-replica，可超配置 N 倍【High（设计权衡）】
- **位置**：`account_concurrency.go:152-178`、`account_rpm.go:140-167`、`subscription_session_window.go:43-55`
- **现象**：Redis 不可用时，每个副本落到各自独立的内存 limiter，副本间无协调。全局同账号 in-flight 上限变为 `N × limit`。
- **后果**：满足"fail-open 不阻断 + 有 fallback 指标"字面验收，但违背"Redis 正常时同账号并发不超过配置"的故障态延续性。压测仅单 limiter 实例断言 `peak ≤ limit`，**无自动化测试覆盖多副本同时 fail-open 超 cap**。

## 3. 中等 / 低等问题

### Medium

| 编号 | 分支 | 问题 | 位置 |
|---|---|---|---|
| M1 | 1.1 | 非 applier fallback 路径把 fixed 窗口起点清零（`QuotaDailyWindowStart=0`）而不是写入自然日/周边界，导致 reset 审计窗口与账号窗口短暂不一致；生产接线已注入 applier，暂未触发 | `account_ops.go:226-237` → `data.go:1689-1715` |
| M2 | 2.2 | "过期未撤销"续费策略随 1h 过期扫描竞态漂移，未固定为"重激活/新建"之一，未记录 `renewal_strategy` 字段 | `subscription_repo.go:292-305` + `expiry_checker.go` |
| M3 | 2.1 | 用户侧套餐列表未按 group 可用性过滤，展示但下单被拒 | `plan_repo.go:122-138` |
| M4 | 3.1 | `shorten` 不按剩余时间折算，实际≈`revoke`（整段有效期减完必 < now 被 clamp） | `refund.go:232` + `subscription_usecase.go:211-214` |
| M5 | 3.1 | legacy 订单退款回退到"当前 active 订阅"，可能撤销/缩短错误（新）订阅 | `refund.go:254-287` |
| M6 | 3.2 | 升级 immediate 直接清零用量窗口（丢弃旧 group 本期已发生用量，cost analysis 少计）+ 无乐观并发锁 | `subscription_change.go:130-135` + `subscription_repo.go:243-261` |
| M7 | 3.2 | 差价金额 `NewPriceQuota/OldPriceQuota` 由 admin 请求体传入，后端不校验是否等于真实 plan 价格 | `admin/server/subscription.go:632-657` |
| M8 | 阶段3 | session_window / RPM 的 Redis 故障降级同为 per-replica，全局预算超配 | `subscription_session_window.go:38-87` |
| M9 | 阶段3 | 压测"并发峰值超 cap"在单进程 smoke 不可证伪故障场景，CI 永远绿灯 | `stresstest/report.go:135-142` |
| M10 | 2.2 | 历史/异常 `paid + asset_issue_status=pending` 订单可通过 `CompleteSubscriptionPurchase` 重复发放；该接口发放后不回写 `asset_issue_status=issued`，但正常 `MarkOrderPaid` 路径会原子写 paid+issued，因此不是通用跨路径并发 High | `admin/service/subscription.go:570-630` + `payment_repo.go:123-180` |

### Low

| 编号 | 分支 | 问题 | 位置 |
|---|---|---|---|
| L1 | 1.1 | 非法时区静默回退 UTC，无日志/事件可定位 | `channel.go:751-754` |
| L2 | 1.3 | 告警去重按 kind 单值，kind 跳变（exhausted↔near_exhausted）会跨越去重窗口重复投递 | `quota_alert.go:127-132` |
| L3 | 1.2 | `clearMarkers` 三次独立 repo 写非原子，任一步失败留半成品元数据 | `account_ops.go:420-438` |
| L4 | 2.1 | 快照缺失时回退实时 plan，可能使已下架订单无法完成（脆弱兜底，生产已注入 snapshotter） | `subscription_assigner.go:128-141` |
| L5 | 阶段3 | ctx 取消后 lease 槽位在 leaseTTL(2min) 内仍计入 Redis ZCARD（过度限流，风险低） | `account_concurrency.go:183-239` |

### 部署注意

- **三个后台任务默认全关**：`SUBSCRIPTION_QUOTA_RESET_ENABLED` / `SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED` / `SUBSCRIPTION_QUOTA_ALERT_ENABLED` 在 `wire_gen.go` 均为 `envBool(..., false)`。运维未显式开启则整套治理零生效——"已完成"分支在默认配置下不运行。

## 4. 已确认正确的地方（正面）

以下为 review 中确认实现扎实、验收达标的项，避免在修复时误伤：

1. **退款幂等核心（最关键资金项）**：`MarkOrderRefunded` 同一事务内对订单 `FOR UPDATE` 行锁 + 已 refunded 短路 + ledger `UNIQUE(dedupe_key)` 双防护，重复退款回调不会重复返钱/重复冲正。余额回退与账本反向记录同事务原子提交。✅
2. **plan_snapshot 机制**：下单存快照、支付回调按快照发放、不下查实时 plan 配置，已下架/改价不影响已创建订单完成。✅
3. **quota reset 幂等**：`reset_runs` 唯一键 + 事务内先插 run 再改账号 + 重复键跳过，重复 tick 不重复写事件；非 fixed 不进入扫描。✅
4. **401/403 不自动启用 + runtime TTL 自动恢复 + 429/5xx/529 TTL 恢复**：分层策略正确。✅
5. **额度告警去重窗口 + 复用 notify-worker + 后台分类**：满足。✅
6. **Relay 跨副本并发 cap / runtime blocker / sticky / failover / fail-open**：真实实现且压测编译运行通过；sticky 不会绑定到不可用账号（failover 成功才 bind）；多副本并发 cap 确实跨副本生效（单 Redis ZSET + 原子 Lua）。✅
7. **指标暴露齐全**：reset/recovery/alert/negative_balance 等均在 `internal/pkg/metrics/subscription.go`。✅

## 5. 修复优先级建议

**P0（资金/权益直接损失，立即修）**
1. **H7 + H8**：升级补钱包扣差价 + 写 change ledger；`ChargedQuota` 仅在真实扣款后写入，杜绝审计造假。
2. **H5 + H6**：退款金额统一取实付 `MoneyCents/100`（store-credit 语义）；对账两侧用同一口径（实付 vs 实付），折扣订单不再恒误报。
3. **H3**：续费延长改为 `max(active.ExpiresAt, now) + durationDays`，让支付回调与管理端发放入口使用同一续费语义；M10 可随同修复为"完成接口只读已发放状态或调用统一发放事务"。
4. **H10**：`user_subscriptions` 加唯一约束根治并发多 active。MySQL 不能直接建部分唯一索引，建议用生成列（如 `active_user_id = IF(status='active', user_id, NULL)`）再建唯一索引；Postgres/SQLite 可用 `WHERE status='active'` partial unique index；或在创建/续费事务内对用户维度加锁。

**P1（恢复语义错误，影响生产可用性）**
5. **H1 + H2**：`AutoPauseAccount` 对 codex 耗尽仅标 unschedulable（不 `status=disabled`）；本地 quota 耗尽写 `RecoveryPolicyQuota`（非默认 rolling），打通已写好却不可达的恢复分支。
6. **H9 + M2**：续费发起处读取 `pending_change` 据此生成续费订单（而非依赖续费请求 GroupID 巧合匹配）；"过期未撤销"固定策略并写 `renewal_strategy` 字段。

**P2（一致性/可观测性增强）**
7. **H11 + M8 + M9**：文档化 fail-open 降级语义，或引入副本数感知的加权 cap；补充"多副本 Redis 故障注入"集成断言，覆盖故障态全局超发。
8. **M1/M3/M4/M5/M6/M7**：fallback reset 写入自然边界、group 可用性过滤、shorten 真按剩余折算、legacy 订单按 subscription_id 追溯、用量窗口结转、差价服务端校验。
9. **L1-L5 + 部署注意**：非法时区加日志、告警去重按窗口+kind 复合、clearMarkers 合并事务、三个后台任务确认生产默认开启或文档强提示。

## 6. 回归门槛验证建议

路线图末尾的回归门槛（make test-unit / npm test / go test ./internal/billing/... ./internal/subscription/... ./internal/admin/...）**无法捕获本报告中多数 High 项**，因为：
- H3/M10（续费/完成发放）需覆盖剩余时长累加与异常 `paid + pending` 重复完成；现有单测用伪造请求掩盖（H9 同）；
- H7（升级不扣款）是"未实现"而非"失败"，单测若无断言钱包变化则绿；
- H10（多 active）需并发测试；
- H11（故障态超发）需多副本+Redis 注入。

建议在合入上述修复时，补充：① 续费剩余时长累加与异常完成接口幂等断言；② 升级后钱包扣款断言；③ 并发新购/续费生成唯一 active 断言；④ 多副本 Redis 故障注入全局 cap 断言。
