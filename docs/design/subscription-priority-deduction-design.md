# 订阅优先扣减模型改造设计

> 状态：**方向已明确，设计已通过四轮 review 修订**（修复订阅套餐"预付额度包"实现偏差，待实施）
> 范围：前端已完成套餐购买位置迁移（"我的订阅"→"充值/订阅"），本设计聚焦后端扣减链路修复
> 关联代码：`internal/billing/biz/billing.go`、`internal/subscription/biz/subscription_usecase.go`、`internal/relay/server/{http.go,subscription_middleware.go}`、`internal/billing/data/account_repo.go`、`internal/subscription/data/subscription_repo.go`、`internal/billing/biz/{cleanup.go,reconciliation.go}`

---

## 1. 背景与现状

当前 token 扣减链路（经源码确认）：

```
请求 → [订阅中间件 CheckQuota] → [reserveQuota 预扣余额] → 转发上游 → [commitQuota 结算]
```

- **订阅中间件**（`subscription_middleware.go`）：仅做限额预检。有活跃订阅且日/周/月用量超限 → 直接 **429 拒绝**，**不降级到余额**。
- **reserveQuota**（`billing.go:ReserveQuota`）：从用户**余额**预扣估算成本，`AvailableBalance < cost` 报 `ErrInsufficientQuota`。全程操作 `accountRepo.UpdateBalance / UpdateFrozenAmount`。
- **commitQuota**（`billing.go:CommitQuotaWithUsage`）：按实际 token 多退少补调整**余额**，写 ledger，再调用 `recordSubscriptionUsage → RecordUsage` 把用量**累加**到订阅统计（仅用于限额判断，**不参与扣费**）。

**本质**：订阅套餐 = 日/周/月 USD 消费上限（限流）+ 套餐定价倍率（`RateMultiplier`）。买不买订阅，token 都从余额扣；订阅超限是硬拒绝。

这与"优先使用订阅、订阅达限后再用余额"的预期不符。

---

## 2. 目标模型

**订阅优先、达限再扣余额**：

- 订阅额度内（未触日/周/月限额）的消耗 → **不扣余额**，仅累加订阅用量
- 超出订阅额度的消耗 → 从余额扣
- 订阅超限不再硬拒绝，而是自动降级到余额扣费（余额也不足才拒绝）

---

## 3. 设计意图确认（已明确：当前实现是 bug）

### 3.1 订阅套餐的设计目的

订阅套餐数据模型（`SubscriptionGroup`）本身就表达了"预付额度包"语义：

- `PriceQuota`：用户花钱购买套餐的价格
- `DailyLimitUSD / WeeklyLimitUSD / MonthlyLimitUSD`：套餐包含的日/周/月 USD 额度
- `RateMultiplier`：额度倍率（如 0.5 表示套餐内消耗按半价计入额度，相当于额度翻倍）
- `DurationDays`：额度有效期

**设计意图**：用户花 `PriceQuota` 买下这个额度包，在日/周/月限额内的消耗应由套餐承担（已付过钱，不再扣余额），超出限额的部分才按量扣余额。

### 3.2 当前实现违背了设计目的

当前代码把订阅退化成了"消费上限限流器"：

- `CheckQuota`：超限直接 429 拒绝，不降级到余额
- `RecordUsage`：只累加用量统计，不承担费用
- `reserveQuota / commitQuota`：**每次请求都从余额扣**，买不买订阅都一样

**后果是双重收费**：用户花 `PriceQuota` 买了套餐，套餐限额内的消耗还要再从余额扣一次钱。订阅套餐沦为"花钱买个使用上限"，违背了"预付额度包"的设计目的。

### 3.3 修复目标（不是商业模式变更，是修复实现偏差）

将扣减链路修正为符合设计意图的"订阅优先"模型：

| 行为 | 修复前（bug） | 修复后（符合设计） |
|------|---------------|-------------------|
| 限额内消耗 | 扣余额 | **套餐承担，不扣余额** |
| 超出限额 | 429 拒绝 | **降级扣余额**，余额不足才拒绝 |
| RateMultiplier 作用 | 仅放大统计数字 | 调整套餐额度倍率（如 0.5 → 额度翻倍） |

**下文按此修复目标设计。**

---

## 4. 技术方案总览

引入**双轨预扣 + 订阅优先结算**：

```
请求 → [订阅中间件：仅采集 metrics，拒绝交给 billing]
     → [reserveQuota：订阅额度优先预留，不足部分预扣余额]
     → 转发上游
     → [commitQuota：实际成本优先消耗已预留订阅额度，余额只承担剩余]
     → 失败/超时/release：统一释放两侧预留
```

核心思路：每笔请求估算成本 `costUSD` 拆成两段——订阅可吸收部分（`subscriptionAmountUSD`）+ 余额承担部分（`balanceAmountQuota`）。冻结量**独立记录在每个 reservation 上**，不挂订阅行，避免跨窗口释放错乱（见 5.1）。

---

## 5. 详细设计

### 5.1 数据模型变更（冻结量挂 reservation，不挂订阅行）

> Review 反馈：把 `FrozenDaily/Weekly/MonthlyUSD` 挂在 `user_subscriptions` 滚动窗口字段上，预扣在窗口 A、结算在窗口 B 时会出现释放错窗口或负数。
> 修正：**冻结量独立记录在 reservation 上**，订阅行零改动；CheckQuota 计算剩余时动态汇总该用户所有 active reservation 的预留量。

**`user_subscriptions` 表：零改动**。不加任何 frozen 列，窗口滚动逻辑（`RollUsageWindows`）完全不变。

**`Reservation`（`internal/billing/biz/reservation.go`）新增字段**：

```go
type Reservation struct {
    // ... 现有字段 ...
    Amount           int64   // 现有：预留余额（quota）。兼容旧路径，新流程下=balanceAmountQuota

    // 新增：订阅侧预留
    SubscriptionID             int64   // 关联的活跃订阅；0 表示无订阅预留
    SubscriptionAmountUSD      float64 // 本次预留的订阅额度（USD，未乘 multiplier）
    SubscriptionDailyWindowStart   int64 // 预扣时的订阅日窗口起点快照
    SubscriptionWeeklyWindowStart  int64 // 预扣时的订阅周窗口起点快照
    SubscriptionMonthlyWindowStart int64 // 预扣时的订阅月窗口起点快照

    // 新增：余额侧预留（与现有 Amount 冗余但语义清晰，便于对账）
    BalanceAmountQuota int64 // 本次预留的余额（quota）；无订阅预留时等于 Amount
}
```

**新增 DB migration**（`migrations/`）：给 `billing_reservations` 表加列（表名以 `internal/billing/data/models.go:36` 的 GORM model 为准，非 `reservations`）：
- `subscription_id BIGINT NOT NULL DEFAULT 0`
- `subscription_amount_usd DOUBLE NOT NULL DEFAULT 0`
- `subscription_daily_window_start BIGINT NOT NULL DEFAULT 0`
- `subscription_weekly_window_start BIGINT NOT NULL DEFAULT 0`
- `subscription_monthly_window_start BIGINT NOT NULL DEFAULT 0`
- `balance_amount_quota BIGINT NOT NULL DEFAULT 0`
- 索引 `idx_billing_reservations_user_status (user_id, status)`（已存在则跳过，用于 CheckQuota 汇总）

> Review 反馈：文档原写 `reservations`，实际 GORM model 用 `billing_reservations`（`models.go:36`）。所有 migration、索引名、`SumActiveFrozenInTx` 都已改为 `billing_reservations`。

**单位约定（关键）**：

| 量 | 单位 | 说明 |
|----|------|------|
| `DailyLimitUSD / WeeklyLimitUSD / MonthlyLimitUSD` | 记账 USD | 限额，已含 multiplier |
| `rolled.DailyUsageUSD` 等 | 记账 USD | 累计用量，`RecordUsage` 时 `costUSD * multiplier` 累加 |
| `SubscriptionAmountUSD`（reservation 存） | **原始 USD** | 真实上游成本，未乘 multiplier |
| 冻结汇总 `frozenAccountingUSD` | 记账 USD | `Σ reservation.SubscriptionAmountUSD * multiplier`（占用额度时换算） |

**订阅剩余额度计算**（CheckQuota / ReserveQuota 用，单位严格对齐）：

```
// 1. rolled 订阅用量（记账 USD，窗口已滚动，RollUsageWindows 现有逻辑）
rolled = RollUsageWindows(subscription, now)

// 2. 动态汇总该用户所有 active+reserved 状态 reservation 的订阅预留量
//    只统计窗口起点仍匹配当前窗口的预留（跨窗口的预留不再占用当前窗口额度）
//    汇总结果换算成"记账 USD"以与 limit/usage 同单位比较
effectiveMultiplier = (group.RateMultiplier > 0 ? group.RateMultiplier : 1.0)
frozenDailyAccounting   = Σ reservation.SubscriptionAmountUSD * effectiveMultiplier
                          where reservation.UserID == subscription.UserID
                            and reservation.SubscriptionID == subscription.ID
                            and reservation.Status == Reserved
                            and reservation.SubscriptionDailyWindowStart   == rolled.DailyWindowStart
frozenWeeklyAccounting  = Σ ... (同上，按 WeeklyWindowStart == rolled.WeeklyWindowStart)
frozenMonthlyAccounting = Σ ... (同上，按 MonthlyWindowStart == rolled.MonthlyWindowStart)

// 3. 剩余可吸收的"原始 USD"（limit/usage/frozen 都是记账 USD，先在记账空间相减，再除以 multiplier 回到原始 USD）
remainingDailyAccounting   = (dailyLimit   == nil ? +∞ : *dailyLimit   - rolled.DailyUsageUSD   - frozenDailyAccounting)
remainingWeeklyAccounting  = (weeklyLimit  == nil ? +∞ : *weeklyLimit  - rolled.WeeklyUsageUSD  - frozenWeeklyAccounting)
remainingMonthlyAccounting = (monthlyLimit == nil ? +∞ : *monthlyLimit - rolled.MonthlyUsageUSD - frozenMonthlyAccounting)
subscriptionAbsorbableUSD  = min(remainingDailyAccounting, remainingWeeklyAccounting, remainingMonthlyAccounting)
                            / effectiveMultiplier   // 原始 USD
```

> 关键：`limit`、`rolledUsageUSD`、冻结汇总三者必须同在"记账 USD"空间相减，最后除一次 `multiplier` 得到可吸收的原始 USD。`reservation.SubscriptionAmountUSD` 存原始 USD，汇总时乘 `multiplier` 换算到记账空间。冻结量随 reservation 的窗口快照走——预扣在窗口 A 的 reservation，窗口滚到 B 后其窗口快照不匹配，自动不再占用 B 窗口额度，避免"释放错窗口"。

**quota ↔ USD 舍入规则**（关键，避免对账漂移）：

> Review 反馈：cost quota→USD→quota 的舍入未定义，向下取整漏扣、向上取整影响 refund/对账。
> 修正：**整数 quota 是资金事实**（balance/frozen/ledger 都存 quota），USD 仅用于订阅限额空间。转换规则统一：

| 转换 | 规则 | 用途 |
|------|------|------|
| `quotaToUSD(quota)` | `quota / quotaPerUSD`（浮点，不取整） | 限额空间比较、订阅用量累加 |
| `usdToQuotaFloor(usd)` | `floor(usd * quotaPerUSD)` | 订阅承担额度换算为 quota（向下取整，避免余额少扣） |
| 余额侧 reserve/commit | **直接用 quota 差额计算**，不经 USD 中转 | 资金事实，避免舍入 |
| 订阅侧 | USD 浮点，仅在限额空间比较和 `SubscriptionAmountUSD` 存储 | 限额，非资金 |

- `PAYMENT_QUOTA_PER_UNIT`（现有，`http.go:1648`，默认 500000）作为唯一换算基数。
- 余额侧 `reserveQuota` 由 `calculateCost` 直接返回 quota（不经 USD）。
- 订阅承担部分换算为 quota 时统一取 floor：`subscriptionQuota = min(reserveQuota, usdToQuotaFloor(absorbUSD))`。
- 余额预扣：`balanceAmountQuota = max(0, reserveQuota - subscriptionQuota)`。
- 余额结算：`actualAbsorbQuota = min(actualCostQuota, usdToQuotaFloor(actualAbsorbUSD))`，`actualBalanceQuota = max(0, actualCostQuota - actualAbsorbQuota)`。
- 若浮点误差导致 `usdToQuotaFloor(absorbUSD)` 偏差，按 `epsilon=1e-9` 归一化后再 floor，并加边界测试。
- ledger 的 `Amount` / `BalanceAfter` 始终是整数 quota，对账以 quota 为准。

### 5.2 预扣阶段（ReserveQuota 改造）

> Review 反馈（两轮）：① GetAbsorbable → create reservation 之间无锁，并发会超卖；② GetAbsorbable 放 subscription biz 但依赖 billing 的 reservation 表，模块边界没定。
> 修正：吸收额度计算 + 冻结汇总 + reservation 插入 + 余额预扣**全部收敛到 billing usecase 单事务内**，事务内对订阅行加 `FOR UPDATE` 串行化同一订阅的预留；subscription usecase 只暴露纯函数原语（rolled 用量 + group 限额 + RollUsageWindows），不读 billing 表。

**部署前置条件**：
- 新流程要求 `billing_reservations`、`users`、`user_subscriptions` 在同一个 SQL 数据库实例/事务边界内，billing usecase 才能在一个事务里同时锁订阅行、插入 reservation、预扣余额。
- 若部署配置将 billing 与 subscription 指向不同数据库，不能启用本订阅优先扣减实现；需先统一到同一数据库事务边界，或另行实现单独的 billing 域订阅额度锁表/额度快照，避免跨库事务。

`billing.BillingUsecase.ReserveQuota` 新增分支（需注入 `SubscriptionUsecase` 提供原语）：

```
1. 查活跃订阅（若有，订阅快照只读）
2. cost = calculateCost(估算 token)  // 余额单位（quota），估算时向上取整保守预估
3. costUSD = quotaToUSD(cost)
4. 若有活跃订阅：
   a. 开启 DB 事务 T
   b. SELECT ... FROM user_subscriptions WHERE id = ? FOR UPDATE
      （锁订阅行，串行化同一订阅的并发预留；不写订阅行，仅防超卖）
   c. 在 T 内计算 subscriptionAbsorbableUSD：
      - rolled = SubscriptionUsecase.RollUsageWindowsPure(subscription, now)
      - group = SubscriptionUsecase.GetGroupPure(groupID)
      - frozenAccounting = reservationRepo.SumActiveFrozenInTx(T, userID, subID, rolled 窗口起点)
        （同事务内读 billing_reservations 表，与插入串行）
      - subscriptionAbsorbableUSD = ComputeAbsorbablePure(rolled, group, frozenAccounting)
        （5.1 的纯函数公式，billing 内联或注入纯函数）
   d. absorbUSD = min(costUSD, subscriptionAbsorbableUSD)        // 原始 USD
   e. subscriptionQuota = min(cost, usdToQuotaFloor(absorbUSD))
   f. balanceAmountQuota = cost - subscriptionQuota
   g. 若 absorbUSD > 0：
      在 T 内 INSERT reservation（status=Reserved）记录
      SubscriptionID / SubscriptionAmountUSD=absorbUSD / BalanceAmountQuota=balanceAmountQuota /
      三个窗口起点快照=rolled 当前窗口起点
   h. 若 balanceAmountQuota > 0：
      在同一事务 T 内调用 accountRepo.ReserveBalanceInTx(T, userID, balanceAmountQuota)
      （失败则整个 T 回滚，reservation 不落库，订阅冻结自然不存在，不需要 release 补偿）
   i. 提交事务 T（订阅行锁释放，reservation 和余额冻结同时可见）
   j. 若 balanceAmountQuota == 0：余额不动
5. 若无订阅：走现有余额预扣逻辑（Amount = cost，无订阅字段）
```

**关键**：
- **并发超卖修复**：吸收额度计算与 reservation 插入在同一事务 T 内，T 对订阅行 `FOR UPDATE` 串行化同一订阅的所有预留。两个并发请求会串行进入 T，第二个读到的 frozen 汇总已含第一个刚插入的 reservation，不会超卖。
- **模块边界**：冻结汇总查 `billing_reservations` 表（billing 域），放在 billing usecase + billing reservationRepo 内；subscription usecase 只提供 `RollUsageWindowsPure` / `GetGroupPure` 等纯函数原语，不反向依赖 billing。订阅行锁也由 billing 事务持有，前提是同库事务可用。
- **预扣原子性**：reservation 插入与余额冻结在同一事务内完成。余额冻结失败时事务回滚，不能调用 `releaseReservation`，避免未冻结余额却执行退款。
- **不写订阅行**：锁订阅行仅为串行化，不修改其字段；冻结量靠 reservation 汇总。
- **保守预估**：`calculateCost` 估算 token 时向上取整，降低 `actualCostUSD > reservedUSD` 的概率（见 5.3 超扣策略）。

### 5.3 结算阶段（CommitQuota 改造，CAS 幂等 + 订阅优先消耗）

> Review 反馈（多轮）：
> ① 按预扣比例分摊破坏订阅优先 → 实际成本优先消耗已预留订阅额度。
> ② commit 多步骤非原子，任一步失败/重试会重复累加用量、重复扣退余额、留 reservation 仍 reserved → 以 reservation 状态 CAS 为核心设计幂等事务。
> ③ overdue_quota = -newBalance 会重复记录历史欠费 → 记本次新增欠费增量。

**核心：reservation 状态 CAS 幂等机**

引入中间状态 `committing` / `releasing`，所有副作用在同一个数据库事务内完成，并为 ledger/应收定义独立幂等键：

```
状态机：reserved → committing → committed
                 → releasing  → released   （commit success=false 或显式 release）
```

`CommitQuotaWithUsage` 改造（success=true 分支，单事务 CAS）：

```
1. actualCostQuota = calculateCostWithUsage(实际 token)   // 直接得 quota，不经 USD
   actualCostUSD = quotaToUSD(actualCostQuota)
2. 开启 DB 事务 T
3. CAS: UPDATE billing_reservations SET status='committing' WHERE reservation_id=? AND status='reserved'
   - 影响行数 0 → 已在 committing/committed/releasing/released（重试或并发），读取当前状态并返回当前结果（幂等）
4. 读取 reservation（T 内）：
   - reservedSubUSD = reservation.SubscriptionAmountUSD
   - reservedBalQuota = reservation.BalanceAmountQuota
5. 订阅优先消耗（不按比例）：
   - actualAbsorbUSD = min(actualCostUSD, reservedSubUSD)
   - actualAbsorbQuota = min(actualCostQuota, usdToQuotaFloor(actualAbsorbUSD))
   - actualBalanceQuota = max(0, actualCostQuota - actualAbsorbQuota)
6. 订阅部分（actualAbsorbUSD > 0 时，T 内）：
   - RecordUsageForSubscriptionInTx(T, reservation.SubscriptionID, actualAbsorbUSD, now)
    （行锁该订阅 FOR UPDATE，按 ID 结算；同一事务 CAS 防重复累加）
7. 余额部分（T 内）：
   - CommitBalanceInTx(T, userID, reservedBalQuota, actualBalanceQuota, allowOverdraft=true)
     （释放 frozen=reservedBalQuota；balance += (reservedBalQuota - actualBalanceQuota)，允许负数）
     返回 oldBalance, newBalance
8. 应收账款（newBalance < 0 时，T 内，见下方"超扣策略"）：
   - newOverdueQuota = max(0, -newBalance) - max(0, -oldBalance)   // 本次新增欠费增量
   - 若 newOverdueQuota > 0：INSERT account_receivables (reservation_id, overdue_quota=newOverdueQuota, status=pending)
9. ledger 写入（T 内，幂等键 ledger_dedupe_key）：
   - subscription ledger（actualAbsorbQuota > 0）：CostSource=subscription, Amount=-actualAbsorbQuota, ReferenceID=reservation_id
   - balance ledger（actualBalanceQuota > 0）：CostSource=balance, Amount=-actualBalanceQuota, ReferenceID=reservation_id
10. CAS: UPDATE billing_reservations SET status='committed' WHERE reservation_id=? AND status='committing'
11. 提交事务 T
```

**幂等保证**：
- 步骤 3 的 CAS 保证只有一个事务能从 `reserved` 进入 `committing`，后续重试看到非 `reserved` 直接返回。
- 步骤 6/7/8/9/10 全在事务 T 内，任一步失败整个 T 回滚，reservation 回到 `reserved`，可安全重试。
- ledger 使用新增 `ledger_dedupe_key` 唯一键防重复，格式为 `{reservation_id}:{type}:{cost_source}`，例如 `res_x:consume:subscription`、`res_x:consume:balance`、`res_x:refund:balance`。`reference_id` 继续用于查询，不作为唯一幂等键。
- 应收记录继续用 `reservation_id` 唯一约束，因为一个 reservation 最多产生一条欠费应收。

`CommitQuotaWithUsage`（success=false 分支）→ 走 `releaseReservation`（见 5.4）。

**订阅切换/过期处理**：
- `RecordUsageForSubscriptionInTx` 按 `reservation.SubscriptionID` 结算，行锁 `SELECT ... WHERE id=? FOR UPDATE`：
  - active → 累加用量到当前窗口。
  - expired/revoked → 仍累加计数器（行还在），限额已失效不影响判断。
  - 物理删除（不应发生）→ 返回错误，T 回滚，告警，对账兜底。

**超扣策略**（actualBalanceQuota > reservedBalQuota 且余额不足）：

> Review 反馈（多轮）：
> ① overdue_quota = -newBalance 重复记录历史欠费（已有 balance=-100，本次超扣 10，应是 10 不是 110）。
> ② 欠费核销与负余额账务冲突（充值只核销不增 balance 会矛盾）。
> 修正：负余额是资金余额事实，应收表是明细镜像；充值事务必须同时 balance += amount 且按同一金额 settle 应收；应收记录本次新增欠费增量。

**① 允许欠费 + 强制扣到负数**：
- `CommitBalanceInTx` 允许 `allowOverdraft=true`，`balance` 扣到负数，不阻断 commit。
- 保守预估降低概率：`ReserveQuota` 阶段 `calculateCost` 向上取整估算。

**② 记应收账款（明细镜像，增量记录）**（`account_receivables` 表，新增）：

```sql
CREATE TABLE account_receivables (
    id              BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id         VARCHAR(64)  NOT NULL,
    reservation_id  VARCHAR(64)  NOT NULL,          -- 关联触发欠费的 reservation（应收幂等键）
    overdue_quota   BIGINT       NOT NULL,          -- 本次新增欠费（quota，正数增量，非累计负余额）
    overdue_usd     DOUBLE       NOT NULL,          -- 欠费金额（USD，便于运营看）
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',  -- pending / settled / written_off
    created_at      BIGINT       NOT NULL,
    settled_at      BIGINT       NOT NULL DEFAULT 0,
    settled_quota   BIGINT       NOT NULL DEFAULT 0, -- 已核销金额
    metadata        TEXT,
    UNIQUE INDEX uniq_receivables_reservation (reservation_id),  -- 幂等：一个 reservation 最多一条应收
    INDEX idx_receivables_user_status (user_id, status),
    INDEX idx_receivables_status_created (status, created_at)
);
```

- **增量计算**（修复重复记录）：`newOverdueQuota = max(0, -newBalance) - max(0, -oldBalance)`。
  - 例：oldBalance=-100，本次超扣 10，newBalance=-110 → newOverdue=110-100=10 ✓
  - 例：oldBalance=50，本次超扣 80，newBalance=-30 → newOverdue=30-0=30 ✓
  - 例：oldBalance=-100，本次退回 20，newBalance=-80 → newOverdue=0（在恢复，不新增）✓
- 应收表是**负余额的明细镜像**：`Σ pending overdue_quota = max(0, -balance)`（当前欠费总额），不是历史累计。

**③ 充值核销（资金事实 + 镜像同步）**：
- `TopUpQuota` 改造为单事务：
  ```
  T:
    1. balance += amount                                          // 资金事实：余额增加
    2. settleAmount = amount
    3. WHILE settleAmount > 0 AND 有 pending 应收（按 created_at ASC）:
         settle = min(settleAmount, receivable.overdue_quota - receivable.settled_quota)
         UPDATE account_receivables SET settled_quota += settle
         WHERE id = receivable.id AND settled_quota = ?  -- CAS 防并发
         IF receivable.settled_quota + settle == overdue_quota:
           UPDATE status='settled', settled_at=now
         settleAmount -= settle
    4. ledger（充值 + 核销明细）
    5. commit T
  ```
- **关键**：balance 和应收核销在同一事务，amount 同时进 balance 和核销应收——这是同一笔资金的两个视角，不是"先核销剩余进 balance"。充值 60 给 balance=-100 的用户：balance→-40，同时核销 60 应收。
- 应收表 `settled_quota` 支持部分核销（一条应收可能分多次充值核销完）。

**④ 监控与运营**：
- `metrics.NegativeBalanceTotal` / `metrics.OverdueReceivablesTotal` 告警。
- 运营看板：pending 应收列表，支持 `written_off`（坏账）。
- `BILLING_BLOCK_OVERDRAFT_USERS`（默认 true）：`balance < 0` 的用户新请求 `ReserveQuota` 拒绝。

**⑤ 对账**：
- 余额侧：`Σ balance ledger = 余额变动`（容忍负值，整数 quota）。
- 应收镜像一致性：`Σ (pending overdue_quota - settled_quota) = max(0, -balance)`（当前净欠费）。
- 应收核销：`Σ settled_quota = Σ 充值核销额`。
- 开关：`BILLING_ALLOW_OVERDRAFT`（默认 true；关闭时 commit 失败并记录不一致告警，作为极端兜底）。

**窗口归属**（解决跨窗口语义）：
- `RecordUsageForSubscriptionInTx` 用 `now` 滚动窗口后累加（与现有 `RollUsageWindows` 一致）。
- 若 reservation 的窗口快照与当前 rolled 窗口不一致（窗口已滚动），实际用量累加到**当前窗口**（窗口 A 的预留自然失效，用量落在窗口 B）。合理：窗口 A 额度在 A 结束时未用完本就该清零，跨到 B 的消耗算 B 的。
- 冻结量按窗口快照汇总（5.1），窗口滚动后旧 reservation 冻结自动不再占用新窗口额度，与"用量落 B"一致。

### 5.4 释放路径统一（CAS 幂等原子事务）

> Review 反馈（多轮）：① 文档只写成功路径；② releaseReservation 幂等不安全——先检查状态但退余额和状态更新非原子，退余额成功后状态更新失败会导致重复退款。
> 修正：release 走单事务 CAS，"状态 reserved→releasing + 退余额 + refund ledger"在同一事务内完成，再 CAS 到 released。

三条释放路径（success=false / 显式 ReleaseQuota / 过期 cleanup）全部收敛到 `releaseReservation`，单事务 CAS：

```
releaseReservation(ctx, reservationID, reason, finalStatus='released'):
  1. 开启 DB 事务 T
  2. CAS: UPDATE billing_reservations SET status='releasing' WHERE reservation_id=? AND status='reserved'
     - 影响行数 0 → 已在 releasing/released/committed（重试/并发），返回（幂等）
  3. 读取 reservation（T 内）：
     - reservedBalQuota = reservation.BalanceAmountQuota
     - reservedSubUSD = reservation.SubscriptionAmountUSD
  4. 余额侧（reservedBalQuota > 0 时，T 内）：
     - ReleaseBalanceInTx(T, userID, reservedBalQuota)
       （释放 frozen += reservedBalQuota... 实为 frozen -= reservedBalQuota 释放；balance += reservedBalQuota 退还）
     - 写 refund ledger（ledger_dedupe_key=`{reservation_id}:refund:balance`）
  5. 订阅侧（reservedSubUSD > 0 时）：
     - 不累加用量，不调 RecordUsageForSubscriptionInTx
     - 冻结量靠 reservation 状态变化自动从汇总消失（CheckQuota 只汇总 status==reserved）
  6. CAS: UPDATE billing_reservations SET status=? WHERE reservation_id=? AND status='releasing'
     （finalStatus = released 或 expired）
  7. 提交事务 T
```

**幂等保证**：
- 步骤 2 的 CAS 保证只有一个事务能从 `reserved` 进入 `releasing`，消除"退余额成功但状态更新失败导致重复退款"的窗口。
- 步骤 4/6 全在事务 T 内，任一步失败整个 T 回滚，reservation 回到 `reserved`，可安全重试。
- ledger 绑定 `ledger_dedupe_key` 幂等键，重试不重复写。

对应到现有入口：

| 入口 | 现有代码位置 | 改造 |
|------|-------------|------|
| 显式 `ReleaseQuota` | `billing.go:270` | 内部改调 `releaseReservation(ctx, id, reason, 'released')` |
| `CommitQuotaWithUsage` success=false | `billing.go` CommitQuota 失败分支（~line 250） | 改调 `releaseReservation`，不再只退余额 |
| 过期 cleanup | `cleanup.go:50` → `ReleaseQuota` | 自动覆盖，finalStatus='expired' |

**新增 reservation 状态常量**（`reservation.go`）：
```go
ReservationStatusCommitting = "committing"  // 新增中间态
ReservationStatusReleasing  = "releasing"   // 新增中间态
```

**失败恢复**：`committing` / `releasing` 状态只在事务提交后才对外可见；如果事务内任一步失败，整个事务回滚，reservation 回到 `reserved` 可重试。正常成功提交后外部只看到 `committed` / `released` / `expired`。因此 cleanup 不需要扫描中间态，仍只处理超时的 `reserved` reservation。

### 5.5 订阅中间件行为变更（`subscription_middleware.go`）

- **不再**因订阅超限直接 429。
- 改为：仅采集 metrics（订阅配额使用情况），**拒绝统一交给 billing 层 `reserveQuota`** 判断 `ErrInsufficientQuota`（订阅超限 + 余额不足时由 reserveQuota 抛出）。

### 5.6 账单流水拆分（ledger）

`Ledger` 新增字段区分扣减来源：

| 字段 | 说明 |
|------|------|
| `CostSource` 枚举 | `subscription` / `balance` / `mixed` |
| `SubscriptionCost` int64 | 订阅承担的 quota（actualAbsorbUSD 换算） |
| `BalanceCost` int64 | 余额承担的 quota（actualBalanceQuota） |

`mixed` 时拆成两条 ledger（一条 `subscription`、一条 `balance`），便于对账与统计。`AggregateUsage` 等统计接口需相应适配：按 `CostSource` 分别聚合。

### 5.7 gRPC proto 变更（`api/billing/v1/billing.proto`）

- `ReserveQuotaResponse` / `CommitQuotaResponse` 新增订阅分摊金额字段（`subscription_amount_usd` / `balance_amount_quota`），便于 relay 层日志与前端展示。
- `LedgerEntry` 新增 `cost_source` / `subscription_cost` / `balance_cost`。

---

## 6. 并发安全

> Review 反馈（多轮）：
> ① `UpdateFrozenAmount` 独立 read-modify-write 有丢更新；② GetAbsorbable → insert reservation 无锁会超卖；③ commit/release 多步骤非原子，任一步失败/重试会重复累加/扣退；④ releaseReservation 幂等不安全（退余额与状态更新非原子）。
> 修正：余额侧三原子事务方法；订阅侧预留单事务锁订阅行；commit/release 引入 CAS 状态机（reserved→committing→committed / reserved→releasing→released），所有副作用在 CAS 事务内完成，ledger 绑定 `ledger_dedupe_key`、应收绑定 `reservation_id`。

| 资源 | 原子操作 | 原语 |
|------|----------|------|
| **余额预扣（新）** | 事务内 read-check-update balance + update frozen | `accountRepo.ReserveBalanceInTx` |
| **余额结算（新）** | CAS 事务内：释放 frozen + 多退少补 balance（允许负数）+ 写应收增量 | `accountRepo.CommitBalanceInTx`（返回 oldBalance, newBalance） |
| **余额释放（新）** | CAS 事务内：释放 frozen + 退还 balance + refund ledger | `accountRepo.ReleaseBalanceInTx` |
| **订阅额度预留（新）** | 单事务内：锁订阅行 FOR UPDATE → 汇总 frozen → 计算 absorbable → INSERT reservation | `reservationRepo.SumActiveFrozenInTx` + INSERT |
| **订阅用量累加（结算）** | CAS 事务内行锁 `FOR UPDATE` 按 subscriptionID | `RecordUsageForSubscriptionInTx` |
| **commit 状态流转（新）** | 单事务内 CAS: reserved→committing→committed，副作用全在事务内 | `CommitQuotaWithUsage` 单事务 |
| **release 状态流转（新）** | 单事务内 CAS: reserved→releasing→released，退余额+ledger 全在事务内 | `releaseReservation` 单事务 |
| **订阅额度释放** | UPDATE reservation 状态（行级，无跨行竞争） | 状态变化即从汇总消失 |

**关键收益**：
- **超卖修复**：吸收额度计算与 reservation 插入同事务，锁订阅行串行化。
- **幂等修复**：commit/release 用 CAS 状态机，只有一个事务能从 reserved 进入 committing/releasing，消除重复累加/扣退。ledger 绑定 `ledger_dedupe_key`，应收绑定 `reservation_id`。
- 余额侧三原子方法消除 `UpdateFrozenAmount` 丢更新窗口。
- 订阅侧不写订阅行字段（仅锁），无跨行字段竞争。

**预扣原子性**：预扣阶段在同一事务 T 内完成 reservation 插入和余额冻结；任一失败则 T 回滚，无需补偿，也不能调用 release 退款。

**失败恢复**：中间态只在事务内短暂存在；事务失败回滚到 `reserved`，事务成功落到终态。cleanup 只需要扫描超时 `reserved` 并执行 release。

**模块边界**：
- 冻结汇总查 `billing_reservations` 表（billing 域）→ billing `reservationRepo.SumActiveFrozenInTx`。
- subscription usecase 只暴露纯函数原语，不反向依赖 billing 表。
- 订阅行 `FOR UPDATE` 锁由 billing 事务持有（锁是并发控制，不是数据所有权），要求 billing/subscription 同库事务可用；分库部署需改用 billing 域锁表。

---

## 7. 兼容性

- **无订阅用户**：走原余额流程，零改动。
- **有订阅用户**：走新双轨流程，订阅额度优先，超出部分走余额。
- **数据迁移**：`billing_reservations` 表新增列默认 0，对存量数据无影响；`user_subscriptions` 表零改动。
- **同库约束**：billing 与 subscription 必须在同一个 SQL 数据库事务边界内；不满足时启动应失败或拒绝注入订阅优先扣减依赖。
- **对账**（`reconciliation.go`）：现有对账基于余额 ledger。新增订阅扣减后，扩展对账逻辑：
  - 余额侧：`Σ balance ledger = 余额变动`（不变）
  - 订阅侧：`Σ subscription ledger 的 SubscriptionCost = Σ reservation.Committed 的 actualAbsorbUSD × multiplier`（新增）

---

## 8. 风险清单

| 风险 | 等级 | 缓解 |
|------|------|------|
| 并发超额用订阅额度 | 高 | 吸收额度计算 + reservation 插入同事务，锁订阅行 FOR UPDATE 串行化；加 100 并发测试 |
| 余额预扣并发丢更新 | 高 | `ReserveBalanceInTx/CommitBalanceInTx/ReleaseBalanceInTx` 原子事务方法，balance+frozen 同事务 |
| **commit/release 多步骤非原子导致重复** | 高 | CAS 状态机（reserved→committing→committed / releasing→released），副作用全在 CAS 事务内，ledger 绑 `ledger_dedupe_key`、应收绑 reservation_id |
| **releaseReservation 幂等不安全** | 高 | 退余额+状态更新+ledger 在同一 CAS 事务内（reserved→releasing→released），消除退余额成功但状态更新失败的窗口 |
| **overdue 重复记录历史欠费** | 高 | 增量计算 `max(0,-newBalance) - max(0,-oldBalance)`，应收表是负余额镜像非累计 |
| **欠费核销与负余额账务冲突** | 高 | 负余额是资金事实，应收是镜像；充值事务同时 balance+=amount 且按同金额 settle 应收（同一笔资金两视角） |
| multiplier 单位错算 | 高 | 5.1 单位约定表，frozen 汇总换算记账 USD 同空间相减后除 multiplier；加 multiplier≠1 单测 |
| 结算时订阅已切换/过期 | 高 | `RecordUsageForSubscriptionInTx` 按 reservation.SubscriptionID 结算，过期仍累加计数器 |
| **quota↔USD 舍入漂移** | 中 | 5.1 舍入规则：整数 quota 为资金事实，余额侧直接用 quota 差额不经 USD，订阅侧 USD 仅限额空间 |
| **跨库事务不可用** | 高 | 要求 billing/subscription 同库；分库部署需改用 billing 域锁表 |
| **余额预扣失败补偿误返钱** | 高 | reservation 插入与余额冻结放同一事务，失败整体回滚，不走 release 退款 |
| 余额超扣（actual > reserved） | 中 | 允许欠费扣负数 + 应收镜像 + `BILLING_BLOCK_OVERDRAFT_USERS` 拦截 + 告警 |
| 应收账款核销一致性 | 中 | `TopUpQuota` 单事务 balance+=amount 且 settle 应收；对账 `Σ(pending overdue-settled)=max(0,-balance)` |
| 跨窗口预留/结算语义 | 中 | reservation 记录窗口快照，冻结按快照汇总，用量落当前窗口；加跨窗口测试 |
| **committing/releasing 卡死恢复** | 低 | 中间态只在事务内可见；失败回滚、成功落终态，cleanup 只扫超时 reserved |
| 对账逻辑遗漏订阅维度 | 中 | 同步改对账，加对账测试；容忍负余额 |
| 双重收费存量数据影响 | 中 | 修复前已按余额扣费的存量订阅用户需评估是否补偿 |
| ledger 拆分影响现有统计/报表 | 中 | `AggregateUsage` 等接口按 `CostSource` 适配 + 回归测试 |

---

## 9. 实施阶段

| 阶段 | 内容 | 产出 |
|------|------|------|
| P1 | 数据模型 + migration（reservation 新增字段） | migration + entity 改动 |
| P2 | subscription biz 暴露纯函数原语 `RollUsageWindowsPure` / `GetGroupPure` / `ComputeAbsorbablePure` / `RecordUsageForSubscriptionInTx` | 新方法 + 单测 |
| P3 | billing data 新增 `ReserveBalanceInTx/CommitBalanceInTx/ReleaseBalanceInTx` 原子方法 + `reservationRepo.SumActiveFrozenInTx` | 新方法 + 并发单测 |
| P4 | billing biz `ReserveQuota`（订阅行锁事务内汇总+插入）/ `CommitQuotaWithUsage`（订阅优先消耗 + 超扣策略）/ `releaseReservation` 改造 | 改造 + 单测 |
| P5 | middleware 行为变更 + `BILLING_ALLOW_OVERDRAFT` | 配置 + 改动 |
| P6 | ledger 拆分 + proto + 统计接口适配 | proto + 接口 + 测试 |
| P7 | 对账逻辑扩展 + 端到端集成测试 | 对账测试 + 集成测试 |

建议 P1–P4 先合入（纯新增和默认无订阅回落），P5–P7 完成后再上线。

---

## 10. 测试策略

- **单测**：
  - `ReserveQuota` 并发超卖：100 goroutine 同时预留同一订阅（限额 $10），断言总预留 ≤ $10、无超卖。
  - `ReserveBalanceInTx` 并发：100 goroutine 预扣同一用户余额，断言总冻结量 ≤ 预扣前余额，无丢更新。
  - **commit CAS 幂等**：同一 reservation 并发/重复调 CommitQuotaWithUsage，断言订阅用量只累加一次、余额只扣一次、每个 `ledger_dedupe_key` 只写一次。
  - **release CAS 幂等**：退余额成功后模拟状态更新失败（事务回滚），重试 release，断言余额不重复退、ledger 不重复写。
  - `CommitQuotaWithUsage` 订阅优先消耗：纯订阅 / 纯余额 / 混合 / 预留订阅 > 实际成本（应全走订阅）四种场景。
  - multiplier≠1：limit/usage/frozen 单位对齐验证（如 multiplier=0.5，限额 $10，已用 $4 记账，预留 $2 原始 → 剩余原始 USD 计算正确）。
  - **舍入一致性**：余额侧 reserve/commit 用 quota 差额，不经 USD 中转；订阅侧 USD 仅限额空间；对账以整数 quota 为准。
  - `RecordUsageForSubscriptionInTx` 订阅切换：预留时订阅 A 活跃，结算前 A 过期/撤销，用量仍记到 A 的计数器，不失败不串到 B。
  - 跨窗口结算：预扣在窗口 A、结算在窗口 B，用量落 B，A 的冻结自动失效。
  - **overdue 增量**：oldBalance=-100 本次超扣 10 → newOverdue=10（非 110）；oldBalance=50 超扣 80 → newOverdue=30；oldBalance=-100 退回 20 → newOverdue=0。
  - **充值核销镜像**：balance=-100 充值 60 → balance=-40 且 settle 60 应收（同事务，不是"先核销剩余进 balance"）；部分核销 settled_quota 正确。
  - 余额超扣：actualBalanceQuota > reservedBalQuota 且余额不足时扣到负数，ledger BalanceAfter 为负，写 account_receivables pending，告警触发。
  - 欠费拦截：`BILLING_BLOCK_OVERDRAFT_USERS=true` 时，balance<0 的用户新请求被 ReserveQuota 拒绝。
  - 释放路径：success=false / 显式 ReleaseQuota / cleanup 三条路径都正确释放两侧预留；幂等性（重复 release 不重复退）。
  - **中间态事务回滚**：模拟 commit/release 事务内错误，断言 reservation 回到 reserved，可安全重试。
  - **预扣原子性**：`ReserveBalanceInTx` 失败后整个事务回滚，reservation 不落库，余额不变，不产生 refund ledger。
  - **同库校验**：billing/subscription 不在同一数据库事务边界内时启动拒绝。
- **集成测试**：relay 端到端，有订阅用户连续请求直到订阅达限，后续请求自动切余额；订阅达限 + 余额不足时报 `ErrInsufficientQuota`；超扣场景余额为负且告警。
- **对账测试**：
  - 余额侧：`Σ balance ledger = 余额变动`（容忍负值，整数 quota）
  - 订阅侧：`Σ subscription ledger = Σ Committed reservation 的 actualAbsorbUSD × multiplier`
  - 应收镜像：`Σ (pending overdue_quota - settled_quota) = max(0, -balance)`（当前净欠费）
  - 应收核销：`Σ settled_quota = Σ 充值核销额`

---

## 附：核心文件改动清单

| 文件 | 改动 |
|------|------|
| `internal/billing/biz/reservation.go` | `Reservation` 加订阅预留字段 + 窗口快照 + `BalanceAmountQuota`；新增事务内中间态 `committing`/`releasing` 状态常量 |
| `migrations/NNN_add_billing_reservations_subscription_fields.sql` | billing_reservations 表加列 |
| `migrations/NNN_create_account_receivables.sql` | account_receivables 表（含 settled_quota 部分核销 + reservation_id 唯一约束） |
| `internal/billing/data/account_repo.go` | 新增 `ReserveBalanceInTx` / `CommitBalanceInTx`（返回 oldBalance,newBalance）/ `ReleaseBalanceInTx` 原子事务方法 |
| `internal/billing/data/receivable_repo.go` | 新增 `account_receivables` 的 Create（增量欠费）/ SettlePendingInTx（充值核销，CAS settled_quota）/ ListPending |
| `internal/billing/biz/billing.go` | `TopUpQuota` 改造：单事务 balance+=amount 且 settle 应收；`CommitQuotaWithUsage` CAS 幂等 + 超扣写应收增量；`releaseReservation` CAS 幂等 |
| `internal/billing/biz/repo.go` | AccountRepo 接口加三个 InTx 方法 |
| `internal/billing/data/reservation_repo.go` | 新增 `SumActiveFrozenInTx`（同事务汇总 billing_reservations） |
| `internal/subscription/biz/subscription_usecase.go` | 纯函数原语 `RollUsageWindowsPure` / `GetGroupPure` / `ComputeAbsorbablePure` + `RecordUsageForSubscriptionInTx`（按 ID 行锁结算） |
| `internal/billing/biz/ledger.go` | `Ledger` 加 `CostSource` / `SubscriptionCost` / `BalanceCost` / `LedgerDedupeKey`；唯一约束 `ledger_dedupe_key` |
| `internal/relay/server/subscription_middleware.go` | 移除硬拒绝，仅采集 metrics |
| `internal/relay/server/http.go` | `recordSubscriptionUsage` 调整（结算已分摊，避免重复累加） |
| `api/billing/v1/billing.proto` | 响应/ledger 字段 |
| `internal/billing/biz/reconciliation.go` | 对账扩展（订阅维度 + 应收镜像一致性，容忍负余额） |
| `internal/billing/biz/cleanup.go` | 扫描超时 reserved reservation 并执行过期 release |
| `internal/pkg/metrics` | `NegativeBalanceTotal` / `OverdueReceivablesTotal` 告警指标 |
| 配置 | `BILLING_ALLOW_OVERDRAFT` + `BILLING_BLOCK_OVERDRAFT_USERS` + 同库 DSN 校验 |

---

## 附：Review 采纳记录

### 第一轮 Review

| Review 项 | 等级 | 采纳 |
|-----------|------|------|
| 结算按预扣比例分摊破坏订阅优先 | High | 5.3 改为 `actualAbsorbUSD = min(actualCostUSD, reservedSubUSD)`，订阅优先消耗 |
| 失败/超时/cleanup 释放路径缺失 | High | 5.4 新增统一 `releaseReservation`，覆盖三条释放路径 |
| 余额 `UpdateFrozenAmount` 并发丢更新 | Medium | 6 新增 `ReserveBalanceInTx/CommitBalanceInTx/ReleaseBalanceInTx` 原子事务方法 |
| 冻结字段挂滚动窗口导致跨窗口冲突 | Medium | 5.1 改为冻结量挂 reservation（带窗口快照），订阅行零改动 |
| 文档"前端无新增改动"描述过时 | Low | 头部状态改为"前端已完成位置迁移，本设计聚焦后端扣减链路" |

### 第二轮 Review

| Review 项 | 等级 | 采纳 |
|-----------|------|------|
| GetAbsorbable → insert reservation 无锁导致超卖 | High | 5.2 吸收额度计算 + reservation 插入收敛到单事务，锁订阅行 FOR UPDATE 串行化 |
| RateMultiplier 单位公式错（frozen 未乘 multiplier） | High | 5.1 单位约定表，frozen 汇总换算记账 USD 同空间相减后除 multiplier |
| commit 调 RecordUsage 按 userID 重查会记错订阅 | High | 5.3 新增 `RecordUsageForSubscriptionInTx` 按 reservation.SubscriptionID 结算 |
| CommitBalance 未定义 actual > reserved 超扣策略 | Medium | 5.3 允许扣到负数 + 保守预估 + 告警 + 开关 |
| GetAbsorbable 模块边界未定 | Medium | 6 冻结汇总放 billing，subscription 只暴露纯函数原语 |

### 第三轮 Review

| Review 项 | 等级 | 采纳 |
|-----------|------|------|
| 欠费核销与负余额账务冲突（充值只核销不增 balance 矛盾） | High | 5.3②③ 重写：负余额是资金事实，应收是镜像；充值单事务 balance+=amount 且按同金额 settle 应收（同一笔资金两视角），非"先核销剩余进 balance" |
| overdue_quota = -newBalance 重复记录历史欠费 | High | 5.3② 增量计算 `max(0,-newBalance) - max(0,-oldBalance)`，应收表是镜像非累计；加 overdue 增量单测 |
| commit/release 多步骤非原子导致重复累加/扣退 | High | 5.3/5.4 CAS 状态机（reserved→committing→committed / releasing→released），副作用全在 CAS 事务内，ledger 绑 `ledger_dedupe_key`、应收绑 reservation_id |
| releaseReservation 幂等不安全（退余额与状态更新非原子） | High | 5.4 退余额+状态更新+ledger 在同一 CAS 事务内（reserved→releasing→released），消除退余额成功但状态更新失败的窗口 |
| 表名写错（reservations → billing_reservations） | Medium | 5.1 migration/索引/SumActiveFrozenInTx 全部改为 billing_reservations（以 models.go:36 为准） |
| quota↔USD 舍入规则未定义 | Medium | 5.1 舍入规则表：整数 quota 为资金事实，余额侧直接用 quota 差额不经 USD，订阅侧 USD 仅限额空间；加舍入一致性单测 |

### 第四轮 Review

| Review 项 | 等级 | 采纳 |
|-----------|------|------|
| 余额预扣失败后调用 releaseReservation 会误返钱 | High | 5.2 改为 reservation 插入与 `ReserveBalanceInTx` 同事务，失败整体回滚，不走 release 退款 |
| billing/subscription 可能分库，单事务不可用 | High | 5.2/7 明确同库部署前置条件；分库需改用 billing 域锁表 |
| mixed ledger 拆两条但 reference_id 幂等键冲突 | High | 5.3 改为新增 `ledger_dedupe_key` 唯一键，格式 `{reservation_id}:{type}:{cost_source}`；`reference_id` 仅查询 |
| committing/releasing 中间态与单事务不可见语义矛盾 | Medium | 5.4/6 改为中间态只在事务内短暂存在；cleanup 只扫超时 reserved |
| 舍入公式仍混用 `usdToQuota` / ceil / floor | Medium | 5.1 统一公式：订阅承担 quota 用 floor，余额预扣/结算用整数 quota 差额和 `max(0, ...)` |
