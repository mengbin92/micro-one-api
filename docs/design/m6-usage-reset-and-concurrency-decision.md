# M6 决策备忘：升级清零用量窗口 + 并发写保护

> 状态：**待拍板**（review §0.1 唯一 open 项，2026-08-05 更新）
> 关联：[subscription-follow-up-code-review.md](./subscription-follow-up-code-review.md) M6、`domain/subscription/biz/subscription_change.go`、`domain/subscription/data/subscription_repo.go`
> 结论预览：**子问题 1 建议补一个防御（仅 group 真正变化时才清零）后接受现状；子问题 2 建议加行锁事务（对齐现有 InTx 模式），不建议乐观锁。**

---

## M6 是什么

M6 实际是**两个独立的问题**被合并在一个编号里，需要分开决策：

| 子问题 | 核心问题 | review 状态 |
|--------|---------|------------|
| ① 升级 immediate 清零用量窗口 | `ChangeSubscription` 在 immediate 变更时无条件把日/周/月用量清零并重置窗口起点，旧 group 本期已发生用量被丢弃，成本分析少计 | 已认定为"刻意设计"（domain-H1 窄字段写已落地），但存在一个未识别的边界（见下） |
| ② 无乐观并发锁 | `ChangeSubscription` 是「读 → 内存改 → 写」三步，无事务、无行锁、无版本号；并发变更会互相覆盖 | 未确认，本次待决策 |

### 涉及代码（现状）

- `subscription_change.go:108-154`：immediate 分支 `GetSubscriptionByID`（无锁读）→ 内存改 group/name/metadata → **无条件清零 usage + 重置 window_start** → `UpdateSubscriptionFields`（窄字段写，`SubscriptionFieldUsageAll`）。
- `subscription_repo.go:208-235`（对照）：`addUsageDB` 是**带 `SELECT ... FOR UPDATE` 行锁的事务**读-改-写。Change 路径没有同等保护。
- `app/admin/internal/service/subscription.go:379-466`：admin 先调 billing 扣差价，再调 `ChangeSubscription`；失败走 `TopUpQuota` 补偿退款。

---

## 子问题 ①：升级 immediate 清零用量窗口

### 现状行为

immediate 变更（含升级、同价切换、group 切换）时：

```go
sub.DailyUsageUSD = 0
sub.WeeklyUsageUSD = 0
sub.MonthlyUsageUSD = 0
sub.DailyWindowStart = now
sub.WeeklyWindowStart = now
sub.MonthlyWindowStart = now
```

即：**无论 group 是否真的变化，一律清零**（`subscription_change.go:130-135` 是无条件的）。

### 选项 A：维持现状（零改动）

**优点**
- 语义直观：新套餐/新 group 从零开始，用户升级后立即获得完整额度窗口，对新 group 公平。
- 已落地且有测试（domain-H1 窄字段写保证不清除 status/expires_at），零回归风险。
- 避免跨 group 用量换算的复杂度——不同 group 有不同 `RateMultiplier`，旧 group 的 usage 口径不能直接沿用。

**缺点**
- 成本分析（cost analysis）按 group 维度统计时，旧 group 本期已发生用量被清零 → **该 group 本期用量少计**（运营报表失真，对账需注意口径）。
- 升级前最后一笔刚入账的用量也丢。
- ⚠️ **未识别的边界（本次发现）**：清零是无条件的。**同 group、同价换 plan 也会清零**（admin 请求 `ToGroupID == 原 group` 时），此时旧 group 的用量本来仍适用，清零既丢数据、又允许"同价 plan 反复切换无限刷新额度"（`charged=0` 不扣款）。虽然切换只能由管理员触发（`ADMIN_TOKEN` 保护），仍是不合理行为。

### 选项 B：按剩余时间折算结转（pro-rata carry-over）

**优点**
- 成本分析连续准确；额度不因升级中断。

**缺点**
- 实现复杂：需按 window 剩余比例折算 + 新 group multiplier 换算，三个窗口（日/周/月）口径不一。
- 跨 group 语义模糊：旧 group 用量乘新 multiplier 后的"折算值"没有明确业务含义。
- 引入大量边界测试；与"新套餐从零开始"的用户直觉冲突。

### 选项 C：不清零，保留旧用量

**缺点**：新 group 额度被旧 usage 占满 → 升级后立即被限流，用户"花钱升级反而立刻不能用了"，最反直觉。**基本不可选。**

### 子问题 ① 建议

**接受"升级清零"的总体设计，但补一个防御：仅当 `req.ToGroupID != 变更前 group` 时才清零；同 group 的 plan 变更保留原用量窗口。**

- 理由：跨 group 切换时清零是正确的（旧 group 用量确实不再适用）；同 group 切换时旧用量仍然适用，清零纯属丢数据 + 可被刷额度。
- 改动量：`subscription_change.go` 一个条件判断 + 一个测试，约半小时。
- 若不接受任何改动，则维持选项 A，但需书面接受"同价同 group 切换可刷新额度"这一运营风险。

---

## 子问题 ②：无乐观并发锁

### 竞态场景

1. **两个管理员并发变更同一订阅**（如同时升级到不同套餐）：
   - 各自 `GetSubscriptionByID` 读到同一快照 → 各自内存修改 → 各自写回。
   - 后写覆盖先写：**一次变更丢失，但两次都扣了差价**（扣款在 admin 层独立完成，H7 只保证"单次失败有补偿"，不保证两次并发都成功时的正确性）。
2. **Change 与 relay AddUsage 并发**：
   - `addUsageDB` 有行锁，Change 无锁。若 Change 先读快照（usage=5），AddUsage 提交（usage=6），Change 再提交（usage=0）→ 升级后立刻产生的用量被清零或覆盖；若顺序反过来则新用量基于 0 累加，正常。**时序依赖，非确定性**。
3. **Change 与 Extend/续费并发**：窄字段写已避免 clobber status/expires_at（domain-H1），此场景基本安全，但 change 与续费同时发生时语义上仍有歧义（如续费应用 pending_change 的同时 admin 又改 group）。

### 选项 A：不加锁（现状）

**优点**
- 零改动。
- Change 是低频管理操作（非热路径）；窄字段写已挡住最危险的状态覆盖。

**缺点**
- 并发变更 lost update（两次变更只生效一个、可能扣两次款）。
- 与 AddUsage 竞态时序不确定，升级瞬间的用量统计可能错。
- 无幂等/无版本保护，后续每加一个新写者都要再评估一遍。

### 选项 B：乐观锁（version 列 / WHERE 条件 CAS）

**优点**
- 无长事务、无行锁开销；冲突时快速失败，上层可重试。
- 实现轻量。

**缺点**
- 需要加列 + 迁移（当前 `user_subscriptions` 无 version 列）。
- 冲突后管理操作直接报错，体验取决于上层是否重试；若重试逻辑不完善，管理端会看到偶发失败。
- 与现有 `addUsageDB` 的行锁模式不一致，同一张表两种并发策略，心智负担增加。

### 选项 C：事务 + `SELECT ... FOR UPDATE` 行锁（对齐 InTx 模式）

**优点**
- 与 `addUsageDB` / `AssignOrExtendInTx` / `RecordUsageForSubscriptionInTx` 的既有模式完全一致——**项目已经建立了"资金/状态相关写走 InTx 行锁"的惯例**（billing-H2、domain-H1 修复都用它）。
- 读-改-写原子化，Change 与 Change、Change 与 AddUsage 在同一行锁上天然互斥，竞态全部消失。
- 变更与扣款失败时的补偿语义更干净。

**缺点**
- 需把 `ChangeSubscription` 改为 InTx 变体（`GetSubscriptionByID` → `FOR UPDATE` 锁行 → 修改 → 同一事务写回）；admin 层扣款在 billing 独立完成，事务边界仍是"billing 扣款 + 订阅变更"两个独立事务（无法真正跨服务原子，但行锁已消除订阅行上的竞态）。
- 改动面比 B 略大（新增 repo 接口 + usecase 变体 + 测试），预计 0.5–1 天。

### 子问题 ② 建议

**采用选项 C：为 `ChangeSubscription` 补 InTx 行锁变体（`SELECT ... FOR UPDATE`），对齐项目既有模式。**

- 理由：订阅变更涉及资金（升级扣差价），与 AddUsage 同级，值得与 `addUsageDB` 同等保护；项目已有成熟的 InTx 基础设施和测试惯例，改造成本可控；乐观锁（B）需要加列且与现有行锁模式不一致，不推荐。
- 若不接受 C，可接受 B 的轻量级妥协（但需加迁移），或明确接受 A 的并发风险（管理端并发变更极低频时风险可接受，但"扣两次款只生效一次"是资金问题，建议至少做 B）。

---

## 决策建议汇总

| 子问题 | 建议 | 工作量 | 风险 |
|--------|------|--------|------|
| ① 清零用量窗口 | 接受总体设计 + **仅 group 变化时才清零**（补一个条件+测试） | ~0.5h | 低 |
| ② 并发写保护 | **InTx 行锁**（选项 C） | ~0.5–1 天 | 低（模式已成熟） |

若两者都采纳，可作为 v0.15.x PATCH（无迁移，或 B 方案才需迁移）或 v0.16.0 的收尾项。

## 附录：sub2api 的处理方式对照（2026-08-05 实地核对）

> 核对对象：`/Users/neo/vscode/mengbin/sub2api`（`backend/ent/schema/user_subscription.go`、
> `backend/internal/repository/user_subscription_repo.go`、
> `backend/internal/service/subscription_service.go`）。

### 核心差异：sub2api 是 (user, group) 多行订阅模型，没有"升级/切换"操作

- `user_subscriptions` 以 **(user_id, group_id)** 唯一（`GetByUserIDAndGroupID`），一个用户可同时在多个
  group 下各有一行订阅；全仓库**搜不到 upgrade / downgrade / switch plan 相关代码**。
- 切换 group = 在新 group 下**新建/激活另一行订阅**，旧 group 的订阅行原样保留（含用量）。
- 因此 **M6 的两个问题在 sub2api 架构下根本不存在**：换 group 不触碰旧行 → 无"升级清零丢用量"；
  无原地 read-modify-write 改 group → 无并发变更覆盖。

### sub2api 的并发策略：原子 SQL + CAS 条件更新，无行锁、无 version 列

| 操作 | sub2api 实现 | 要点 |
|------|-------------|------|
| 用量累加 | `IncrementUsage` 原生 SQL：`daily_usage_usd = us.daily_usage_usd + $1`（原子自增） | 数据库原子增量，天然无 lost update，不需要锁 |
| 窗口重置 | `ResetDailyUsage/Weekly/Monthly`：`WHERE daily_window_start = 期望值` 条件更新，affected=0 视为"另一请求已推进，预期 no-op"（`translateConditionalWindowReset`） | **CAS 乐观并发**，冲突即放弃，不报错 |
| 续期多步写 | `updateExistingSubscriptionTerm` 走 ent 事务（`withSubscriptionUpdateTx`）包裹 ExtendExpiry/UpdateStatus/UpdateNotes | 事务保证原子性 |
| 过期待清零 | `renewedSubscriptionTerm`：过期续期时清零 usage + window start 重置为 `startOfDay` | 只有"过期重激活"才清零，**无"升级即清零"** |
| version 列 | schema 无 version 字段 | 不靠乐观锁列，靠 CAS 业务条件 |

### 对 micro-one-api 决策的影响

**子问题 ①（升级清零）—— 关键事实修正：**

- **micro-one-api 的成本分析走 `billing_ledgers`（`operation_report_repo.go:221-224`，
  `JOIN billing_reservations` + `user_subscriptions`），不读 `user_subscriptions.daily_usage_usd`。**
  usage 列只用于：relay 限额门控、管理端展示、对账（`reconciliation_repo.go:171`）。
- 因此 review 原话"升级清零 → cost analysis 少计"**不成立**：成本/收入报表不受清零影响。
  清零的真实影响是：旧 group 的管理端展示数据被清、对账口径（对账按当前行 usage 比对）。
- 这使选项 A（清零）的代价进一步降低；"仅 group 变化才清零"的防御依然推荐
  （同 group 变更时旧 usage 仍适用，清零无意义且可被同价切换刷额度）。

**子问题 ②（并发锁）—— 出现第四种方案：CAS 条件更新（sub2api 模式）**

- sub2api 证明：**无 version 列、无 FOR UPDATE 行锁，用"原子 SQL += + 业务字段条件更新"也能保证
  一致性**。冲突处理的哲学是"CAS 失败 = 并发已推进 = 预期行为，重读快照即可"。
- 对照 micro-one-api：`addUsageDB` 已用 FOR UPDATE 行锁（比 sub2api 的原子 += 更重但等效安全，
  不必改）；真正缺保护的是 `ChangeSubscription` 的 read-modify-write。借鉴 sub2api，
  可把变更写成条件更新（如 `WHERE group_id = 变更前值`），affected=0 → 重读重试或报冲突，
  比加 version 列（选项 B）更轻，比 FOR UPDATE 行锁（选项 C）改动更小。
- 结论修正：**选项 C（InTx 行锁）与选项 D（CAS 条件更新，sub2api 模式）均可行**；项目已有
  InTx 行锁先例（addUsageDB），但 ChangeSubscription 只有一个写者字段集合（group/name/metadata/
  usage），CAS 条件更贴合"冲突即重试"的管理操作语义。建议二选一，倾向 C（与既有惯例一致）。

## 决策记录

**用户拍板（2026-08-05）：子问题①按建议补防御；子问题②选方案 C（InTx 行锁）。**

### 子问题① 实施：仅 group 变化时才清零（已完成）

- `domain/subscription/biz/subscription_change.go`：immediate 分支改为
  `groupChanged := req.ToGroupID != fromGroupID`——仅在 group 真正变化时清零
  usage + 重置 window_start，并追加 `SubscriptionFieldUsageAll`；同 group 变更
  保留原用量窗口（只写 group/name/metadata）。
- 测试：`TestChangeSubscription_SameGroupKeepsUsage`（同 group 同价变更保留
  usage + window_start）、`TestChangeSubscription_GroupChangeResetsUsage`
  （跨 group 变更仍清零）。既有 `TestChangeSubscription_ImmediateUpgrade`
  的"group 变化清零"断言继续通过。

### 子问题② 实施：InTx 行锁变体（已完成，方案 C）

- `SubscriptionUsecase` 新增可选 `txRunner TxRunner` 字段 + `SetTxRunner`；
  `ChangeSubscription` 在 txRunner 已接线时走 `RunInTx` → `changeSubscription(ctx, tx, req)`
  （`GetByIDInTx` 行锁读 + `UpdateSubscriptionFieldsInTx` 写），未接线时回退
  原无锁路径（memory/测试模式）。
- 接线：`app/admin/cmd/admin/admin_helpers.go` 创建 SubUc 后
  `SetTxRunner(subscriptiondata.NewTxRunner(repo))`，生产 DB 路径获得行锁。
- 测试：`TestChangeSubscription_UsesTxRunnerWhenWired`（断言 RunInTx 被调用）、
  `TestChangeSubscription_TxRunnerErrorRollsBack`（runner 错误时无部分写入）。
- 无数据库迁移；无需改 admin service 调用方（usecase 内部自管事务）。

**验证**：build / vet / check-architecture / 全量单测全绿（见当日工作日志）。

### 二次 CR 修复（2026-08-05，提交 `b2fae2f`）

1. **txCtx 契约**：`RunInTx` 回调收到 `txCtx` 参数但内部误用外层 `ctx`（当前 data 层
   实现两者相等，无行为差异，但违反 `TxRunner` 接口契约——runner 可能提供
   tx-scoped 超时/取消/trace）。改为 `uc.changeSubscription(txCtx, tx, req)`。
2. **窄字段写收窄**：immediate 分支 `fields` 原先无条件含
   `SubscriptionFieldSubscriptionName`——即使 `NewPlanName == ""`（本次未改 name）
   也把读快照的旧 name 写回，违反 domain-H1"只写本次改的字段"。改为仅当
   `req.NewPlanName != ""` 时加入该字段。

### 已知边界（记录，非本次引入）

- **并发双扣款不解决**：admin 层钱包扣款（`PurchaseSubscription`）是独立 RPC，
  发生在行锁事务**之前**。M6 行锁保证订阅行不被并发覆盖（变更不丢失），但两个
  并发升级请求仍会各自成功扣款。根治需 admin 层请求级幂等/去重，超出 M6 范围。
- **`RowsAffected == 0` 语义**：`updateSubscriptionFieldsWithTx` 用
  `RowsAffected == 0` 推断 `ErrSubscriptionNotFound`。MySQL 默认在 SET 值与现值
  相同时 RowsAffected 为 0——同秒重复变更场景（metadata 的 `at` 时间戳相同）可能
  误报 not found。属既有数据层语义，触发概率极低，如需根治应改为"先查存在性"。
- **sqlite 不加行锁**：`GetByIDInTx` 对 sqlite 跳过 `FOR UPDATE`（项目既有设计，
  sqlite 不支持行锁）；生产 MySQL/Postgres 正常加锁。


