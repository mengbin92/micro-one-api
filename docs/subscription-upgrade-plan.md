# micro-one-api 订阅系统增强方案

---

## 0. 落地原则

1. **接口先行**。每个新子系统先在 `biz` 包定义接口(`Scheduler`、`TokenCache`、`AccountRepository`、`RefreshHook`、`SubscriptionRepository`);用 channelv1 gRPC + Redis 实现默认实现;`Noop*` 兜底,保证单测/集成测试不需要外部依赖也能跑。
2. **可灰度**。所有新增能力走 feature flag(沿用 `cfg.HybridAdaptor.*` + 新增 `cfg.Subscription.*`),默认 `false`;回退路径保留 `RefreshTask`、原 `SelectChannel`、原 `RetryExecutor`、原 `RelayUsecase.Plan`。
3. **不破坏现有 wire**。`wire_gen.go` 是手写生成,所有变更都按 `s/oldName = .../newName = .../` 风格小改,不要重排 import 顺序、不要重排 provider 列表、不要触发 wire 全量重生成。
4. **可观测性**。每个子系统内部用 `atomic.Int64` 累计指标,导出 `Metrics() Snapshot` 结构体;HTTP `/metrics` 端点把这些 counter 暴露成 Prometheus。sub2api 的 ops 面板我们做不到 1:1,但 ops 指标全部走 Prometheus,后续可接 Grafana。
5. **错误统一**。所有上游错误统一归类到 `passthrough.UpstreamError` 结构体,字段 `StatusCode` / `Body` / `Kind`(kind 枚举: `Retryable` / `RetryableOnSameAccount` / `NonRetryable` / `CyberBlocked` / `Passthrough`),`failover_loop` 跟 `passthrough_handler` 都消费它,避免错误处理散落各处。
6. **两套配额**。本文区分**用户配额**(日/周/月 USD,业务层,desktop 方案 §3.2)和**上游配额**(Codex 5h/7d,技术层,relay §2.7),不要混用。前者在请求入口拦截,后者在选号 / 响应后回写。

---

## 1. 整体架构

```
                    ┌──────────────────────────────────────────────────────┐
                    │  业务层 (internal/subscription) - 桌面方案             │
                    │  • UserSubscription (用户订阅实体)                    │
                    │  • SubscriptionGroup (分组配置)                        │
                    │  • SubscriptionUsecase (分配/进度/撤销)                │
                    │  • QuotaChecker (日/周/月 USD 配额)                    │
                    │  • SubscriptionExpiryChecker (定时)                    │
                    └────────────────────────┬─────────────────────────────┘
                                             │ 配额检查通过
                                             ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  请求入口 (relay-gateway HTTP)                                               │
│  • Identity auth (existing)                                                  │
│  • Billing ReserveQuota (existing)                                           │
│  • NEW: QuotaChecker.CheckQuota(ctx, userID, estimatedCost)                  │
│         → 不通过 → 429 + Retry-After                                         │
└────────────────────────┬─────────────────────────────────────────────────────┘
                         ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  Relay 层 (internal/relay) - 旧版方案                                        │
│                                                                              │
│  ┌──────────────┐  ┌──────────────────┐  ┌────────────────┐                 │
│  │ Scheduler    │→ │ AccountPool      │→ │ TokenProvider  │                 │
│  │ (多层选号)   │  │ (运行时熔断)     │  │ (Redis 缓存)   │                 │
│  └──────┬───────┘  └────────┬─────────┘  └────────┬───────┘                 │
│         │                   │                     │                         │
│         │     ┌─────────────┴────────────┐        │                         │
│         │     │ StickySession (Redis)    │        │                         │
│         │     │ PreviousResponse (Redis) │        │                         │
│         │     │ RuntimeBlocker (Redis)   │        │                         │
│         │     └──────────────────────────┘        │                         │
│         │                                          │                         │
│         ▼                                          ▼                         │
│  ┌──────────────────────────────────────────────────────┐                   │
│  │  Adaptor.BuildUpstreamRequest (mimicry + inject)    │                   │
│  └──────────────────────────────────────────────────────┘                   │
│         │                                                                   │
│         ▼                                                                   │
│  ┌──────────────────────────────────────────────────────┐                   │
│  │  Upstream HTTP call (Codex / Claude)                 │                   │
│  └──────────────────────────────────────────────────────┘                   │
│         │                                                                   │
│         ▼                                                                   │
│  ┌──────────────────────────────────────────────────────┐                   │
│  │  Response 处理 (Passthrough / Concurrency / 配额窗口) │                   │
│  │  + FailoverLoop (同账号重试 / 跨账号切换)             │                   │
│  └──────────────────────────────────────────────────────┘                   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────┐                   │
│  │  TokenRefreshService (后台 5min cycle)               │                   │
│  │  → 临时封禁 / 退避 / post-refresh hooks               │                   │
│  └──────────────────────────────────────────────────────┘                   │
└────────────────────────┬─────────────────────────────────────────────────────┘
                         ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  用量回写 (异步)                                                             │
│  • Billing CommitQuota (existing)                                            │
│  • NEW: SubscriptionUsecase.RecordUsage (用户日/周/月累计)                   │
│  • NEW: Codex 5h/7d snapshot 提取 + 配额自动暂停                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 数据库迁移

| 编号 | 迁移 | 来源 | 用途 |
|---|---|---|---|
| 040 | `user_subscriptions` 表 | 桌面 §3.1.1 | 用户订阅实体 |
| 041 | `subscription_groups` 表 | 桌面 §3.1.2 | 分组配置(独立于 `users.group` 字符串) |
| 042 | `subscription_account_groups` 表 | 桌面 §3.1.3 | 账号→分组 N:N;**注意**:与 channelv1 现存 `subscription_account_abilities` 重复,本次**只新增表的预留**,实际路由继续走 abilities 表 |
| 043 | `oauth_authorization_codes` 表 | 桌面 §3.1.4 | OAuth 临时码;**实际不落表**,本次用进程内 `session_store`(admin 操作,非高频,跨副本靠重定向解决) |
| 044 | `account_quota_snapshots` 表 | 旧版 §2.7 | Codex 5h/7d 配额窗口快照 |

### 2.1 user_subscriptions (桌面方案 §3.1.1)

```sql
CREATE TABLE `user_subscriptions` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NOT NULL,
  `group_id` BIGINT NOT NULL,
  `subscription_name` VARCHAR(128) NOT NULL DEFAULT '',
  `status` VARCHAR(32) NOT NULL DEFAULT 'active',  -- active / expired / revoked
  `starts_at` BIGINT NOT NULL,                       -- unix
  `expires_at` BIGINT NOT NULL,
  `daily_usage_usd` DECIMAL(12,4) NOT NULL DEFAULT 0,
  `weekly_usage_usd` DECIMAL(12,4) NOT NULL DEFAULT 0,
  `monthly_usage_usd` DECIMAL(12,4) NOT NULL DEFAULT 0,
  `daily_window_start` BIGINT NOT NULL DEFAULT 0,
  `weekly_window_start` BIGINT NOT NULL DEFAULT 0,
  `monthly_window_start` BIGINT NOT NULL DEFAULT 0,
  `metadata` TEXT,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_user_subs_user_id` (`user_id`),
  KEY `idx_user_subs_group_id` (`group_id`),
  KEY `idx_user_subs_status` (`status`),
  KEY `idx_user_subs_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.2 subscription_groups (桌面方案 §3.1.2)

```sql
CREATE TABLE `subscription_groups` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(64) NOT NULL,
  `display_name` VARCHAR(128) NOT NULL DEFAULT '',
  `platform` VARCHAR(32) NOT NULL,                    -- anthropic / openai / gemini
  `subscription_type` VARCHAR(32) NOT NULL DEFAULT 'standard',
  `daily_limit_usd` DECIMAL(12,4) NULL,
  `weekly_limit_usd` DECIMAL(12,4) NULL,
  `monthly_limit_usd` DECIMAL(12,4) NULL,
  `rate_multiplier` DECIMAL(4,2) NOT NULL DEFAULT 1.0,
  `status` INT NOT NULL DEFAULT 1,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_sub_groups_name` (`name`),
  KEY `idx_sub_groups_platform` (`platform`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.3 account_quota_snapshots (旧版 §2.7)

```sql
CREATE TABLE `account_quota_snapshots` (
  `account_id` BIGINT PRIMARY KEY,
  `primary_used_percent` DOUBLE PRECISION,
  `primary_reset_after_seconds` INT,
  `primary_window_minutes` INT,
  `secondary_used_percent` DOUBLE PRECISION,
  `secondary_reset_after_seconds` INT,
  `secondary_window_minutes` INT,
  `primary_over_secondary_percent` DOUBLE PRECISION,
  `updated_at` TIMESTAMP,
  `snapshot_paused` BOOLEAN NOT NULL DEFAULT FALSE
);
```

### 2.4 桌面方案需要修正的点

- **`oauth_authorization_codes` 表不创建**。进程内 store 5 分钟 TTL,session 跟发起 auth-url 的副本绑死,OAuth 回调由 admin 操作,跨副本问题通过 admin-web 记住副本路由(或 sticky 到同一副本)解决。
- **`subscription_account_groups` 表只保留 schema 注释,不实际建表**。micro-one-api 已用 `subscription_account_abilities` 表做 group→account 映射(见 `internal/channel/biz/channel.go ListSubscriptionAccountAbilities`),避免重复;`subscription_groups` 跟现有 `channel.group` 字符串的关系是"前者是配置层、后者是路由键"。
- **不引入 `decimal.Decimal` 依赖**。USD 金额用 `*float64`(精度 0.0001,够用),与 sub2api 风格一致(见 `service/user_subscription.go` 字段 `DailyUsageUSD float64`)。

---

## 3. 业务层设计(桌面方案)

### 3.1 包结构

新建 `internal/subscription/`,与 `internal/relay` 平级:

```
internal/subscription/
├── biz/
│   ├── entity.go              # UserSubscription, SubscriptionGroup, QuotaDimension
│   ├── errors.go              # ErrSubscriptionNotFound, ErrQuotaExceeded ...
│   ├── subscription_usecase.go # SubscriptionUsecase (Assign/Revoke/Extend/Reset/RecordUsage/CheckQuota/GetProgress)
│   ├── group_usecase.go       # GroupUsecase (CRUD)
│   ├── quota_checker.go       # QuotaChecker (日/周/月 三维)
│   └── expiry_checker.go      # SubscriptionExpiryChecker (定时)
├── data/
│   ├── subscription_repo.go   # SubscriptionRepository (gorm)
│   ├── group_repo.go          # GroupRepository (gorm)
│   └── quota_snapshot_cache.go # 进程内 quota 缓存 (Ristretto LRU + TTL)
├── service/
│   ├── subscription.go        # gRPC 实现
│   ├── group.go
│   └── middleware.go          # gRPC interceptor: 从 metadata 取 userID
└── server/
    ├── http.go                # admin HTTP endpoints
    └── metrics.go             # /metrics 增量
```

### 3.2 实体(对齐 sub2api `service/user_subscription.go`)

```go
// internal/subscription/biz/entity.go
type SubscriptionStatus string
const (
    SubscriptionStatusActive  SubscriptionStatus = "active"
    SubscriptionStatusExpired SubscriptionStatus = "Expired"
    SubscriptionStatusRevoked SubscriptionStatus = "revoked"
)

type UserSubscription struct {
    ID               int64
    UserID           int64
    GroupID          int64
    SubscriptionName string
    Status           SubscriptionStatus
    StartsAt         int64
    ExpiresAt        int64

    DailyUsageUSD   float64  // 0.0001 精度
    WeeklyUsageUSD  float64
    MonthlyUsageUSD float64

    DailyWindowStart   int64
    WeeklyWindowStart  int64
    MonthlyWindowStart int64

    Metadata  string
    CreatedAt int64
    UpdatedAt int64
}

type SubscriptionGroup struct {
    ID               int64
    Name             string
    DisplayName      string
    Platform         string
    SubscriptionType string

    DailyLimitUSD   *float64
    WeeklyLimitUSD  *float64
    MonthlyLimitUSD *float64
    RateMultiplier  float64
    Status          int32
    CreatedAt       int64
    UpdatedAt       int64
}

type QuotaDimension struct {
    Used      float64
    Limit     *float64  // nil = 无限制
    Remaining float64
}

type QuotaCheckResult struct {
    Allowed    bool
    Reasons    []string
    Daily      *QuotaDimension
    Weekly     *QuotaDimension
    Monthly    *QuotaDimension
}

type SubscriptionProgress struct {
    ID                int64
    Status            SubscriptionStatus
    StartsAt          int64
    ExpiresAt         int64
    DailyUsed         *QuotaDimension
    WeeklyUsed        *QuotaDimension
    MonthlyUsed       *QuotaDimension
    RemainingSeconds  int64
}
```

### 3.3 核心用例

```go
// internal/subscription/biz/subscription_usecase.go
type SubscriptionUsecase struct {
    repo      SubscriptionRepository
    groupRepo GroupRepository
    timeNow   func() time.Time
}

type AssignSubscriptionRequest struct {
    UserID           int64
    GroupID          int64
    SubscriptionName string
    StartsAt         int64       // 0 = now
    ExpiresAt        int64       // 必填
    Metadata         string
}

func (uc *SubscriptionUsecase) Assign(ctx, req *AssignSubscriptionRequest) (*UserSubscription, error) {
    // 1. 校验 group 存在 + status=enabled
    // 2. 校验同 user 没有 active 的同 group 订阅(避免重复分配)
    // 3. 写入 user_subscriptions, status=active, starts=now, expires=req.ExpiresAt
    // 4. 返回实体
}

func (uc *SubscriptionUsecase) Revoke(ctx, id, reason string) error {
    // status=revoked, updated_at=now
}

func (uc *SubscriptionUsecase) Extend(ctx, id, newExpiresAt int64) error {
    // 仅允许 active/expired 状态,不能改 revoked
}

func (uc *SubscriptionUsecase) ResetQuota(ctx, id, scope string) error {
    // scope: daily / weekly / monthly / all
    // 重置 usage + window_start=now
}

func (uc *SubscriptionUsecase) RecordUsage(ctx, userID, costUSD float64) error {
    // 1. 找到 active 订阅
    // 2. 检查并滚动时间窗口(daily/weekly/monthly)
    // 3. 累加 daily/weekly/monthly usage
    // 4. 持久化
}

func (uc *SubscriptionUsecase) CheckQuota(ctx, userID, estimatedCost float64) (*QuotaCheckResult, error) {
    // 1. GetActiveSubscription(userID)
    // 2. 加载 group 配置
    // 3. 滚动时间窗口(用 quotaSnapshotCache 避免每次查 DB)
    // 4. 三维判断(daily/weekly/monthly)
    // 5. 返回 QuotaCheckResult
}

func (uc *SubscriptionUsecase) GetProgress(ctx, userID) (*SubscriptionProgress, error) {
    // ListActiveUserSubscriptions(userID) + 对每个查 usage
}
```

### 3.4 时间窗口滚动规则(对齐 sub2api `subscription_calculate_progress`)

| 维度 | 窗口长度 | 滚动时机 |
|---|---|---|
| daily | 24h | `now - daily_window_start >= 24h` → reset usage, window_start=now |
| weekly | 7d | `now - weekly_window_start >= 7d` → reset |
| monthly | 30d | `now - monthly_window_start >= 30d` → reset |

> sub2api 实际是 calendar-based(每日 0:00 / 每周一 / 每月 1 号),本次用 sliding window 简化(从首次使用起 24h/7d/30d)。如果业务需要 calendar 化,后续可加 cron 触发,逻辑与 quota_checker 解耦。

### 3.5 进程内 QuotaSnapshotCache

为避免每次 `CheckQuota` 都打 DB 查 `user_subscriptions`,加 Ristretto 缓存:

```go
// internal/subscription/data/quota_snapshot_cache.go
type QuotaSnapshot struct {
    UserID           int64
    SubscriptionID   int64
    GroupID          int64
    DailyUsage       float64
    WeeklyUsage      float64
    MonthlyUsage     float64
    DailyWindowStart int64
    WeeklyWindowStart int64
    MonthlyWindowStart int64
    ExpiresAt        int64
    CachedAt         int64
}

type QuotaSnapshotCache struct {
    r *ristretto.Cache  // key=userID, value=*QuotaSnapshot, TTL=30s, max=10k
}

// Get 命中后用 CachedAt 判断新鲜度;Write-through on RecordUsage
// 注意:CheckQuota 用缓存前必须先调 repo.GetActiveSubscription 校验 status/expires_at,
//     因为缓存只缓存"已校验过的快照",不能用陈旧数据判断 quota exceeded
```

### 3.6 gRPC 接口

新增 `api/subscription/v1/subscription.proto` + `api/subscription/v1/group.proto`,由 `protoc` + kratos 工具链生成(参考现有 `api/billing/v1/billing.proto` 的生成脚本):

```protobuf
service SubscriptionService {
  rpc GetUserSubscription(GetUserSubscriptionRequest) returns (GetUserSubscriptionReply);
  rpc GetSubscriptionProgress(GetSubscriptionProgressRequest) returns (GetSubscriptionProgressReply);
  rpc RecordUsage(RecordUsageRequest) returns (RecordUsageReply);
  rpc CheckQuota(CheckQuotaRequest) returns (CheckQuotaReply);
}

service SubscriptionAdminService {
  rpc AssignSubscription(AssignSubscriptionRequest) returns (AssignSubscriptionReply);
  rpc RevokeSubscription(RevokeSubscriptionRequest) returns (RevokeSubscriptionReply);
  rpc ExtendSubscription(ExtendSubscriptionRequest) returns (ExtendSubscriptionReply);
  rpc ResetSubscriptionQuota(ResetSubscriptionQuotaRequest) returns (ResetSubscriptionQuotaReply);
  rpc ListSubscriptions(ListSubscriptionsRequest) returns (ListSubscriptionsReply);
}

service GroupService {
  rpc CreateGroup(...) returns (...);
  rpc UpdateGroup(...);
  rpc DeleteGroup(...);
  rpc GetGroup(...);
  rpc ListGroups(...);
}
```

### 3.7 HTTP 端点(供 admin-web 调用)

| Method | Path | 说明 |
|---|---|---|
| GET | `/api/v1/subscriptions/me` | 当前用户活跃订阅 |
| GET | `/api/v1/subscriptions/progress` | 当前用户订阅进度(三维配额) |
| POST | `/api/v1/admin/subscriptions/assign` | 分配订阅 |
| POST | `/api/v1/admin/subscriptions/:id/revoke` | 撤销 |
| POST | `/api/v1/admin/subscriptions/:id/extend` | 延长 |
| POST | `/api/v1/admin/subscriptions/:id/reset-quota` | 重置 |
| GET | `/api/v1/admin/subscriptions` | 列表 |
| POST | `/api/v1/admin/groups` | 新建分组 |
| GET | `/api/v1/admin/groups` | 列表 |
| PUT | `/api/v1/admin/groups/:id` | 更新 |
| DELETE | `/api/v1/admin/groups/:id` | 删除 |

### 3.8 SubscriptionExpiryChecker(定时任务)

对齐 sub2api `subscription_expiry_service.go` + `subscription_maintenance_queue.go`,进程内 goroutine:

```go
// internal/subscription/biz/expiry_checker.go
const (
    ExpiryCheckInterval  = 1 * time.Hour
    ExpiryWarnBefore     = 24 * time.Hour   // T-24h 发通知
    ExpiryMarkAfter      = 0                // 过期立即标 expired
)

func (c *SubscriptionExpiryChecker) Run(ctx context.Context) {
    ticker := time.NewTicker(ExpiryCheckInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            c.markExpired(ctx)
            c.warnExpiring(ctx)
        }
    }
}

func (c *SubscriptionExpiryChecker) markExpired(ctx) {
    // 找 expires_at < now AND status=active 的订阅, 改 status=expired
}

func (c *SubscriptionExpiryChecker) warnExpiring(ctx) {
    // 找 expires_at < now + 24h AND status=active 的订阅, 发通知(走 notify worker)
}
```

### 3.9 配额检查接入入口

新增 `internal/subscription/server/middleware/quota.go`(HTTP 中间件)或 gRPC interceptor,挂在 relay-gateway 入口:

```go
// 在 relay-gateway HTTP server 的认证之后,channel select 之前
func (s *HTTPServer) withQuotaCheck(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        auth, _ := middleware2.GetAuthSubjectFromContext(r.Context())
        if auth == nil { next.ServeHTTP(w, r); return }
        estimatedCost := estimateCostFromRequest(r)  // 简化: 根据 model 给个上限
        result, err := s.subscriptionUc.CheckQuota(r.Context(), auth.UserID, estimatedCost)
        if err != nil { next.ServeHTTP(w, r); return }  // 失败放行,降级到下游
        if !result.Allowed {
            s.writeQuotaError(w, result)  // 429 + Retry-After
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**feature flag**:`cfg.Subscription.Enabled`,默认 `false`;开启时挂中间件,关闭时直接 pass-through(零开销)。

---

## 4. 业务层 + Relay 层集成点

### 4.1 用量回写

```go
// internal/relay/server/responses.go
// 在 CommitQuota 之后,异步触发 (走 worker pool, 不阻塞响应)
go func(ctx context.Context, userID, groupID int64, costUSD float64) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := s.subscriptionUc.RecordUsage(ctx, userID, costUSD); err != nil {
        s.logger.Warn("subscription.record_usage_failed", zap.Error(err), zap.Int64("user_id", userID))
    }
}(r.Context(), auth.UserID, groupID, costUSD)
```

### 4.2 订阅进度查询

由前端直接调 `subscriptionv1.GetSubscriptionProgress`;不经过 relay-gateway。

### 4.3 业务配额 vs 上游配额

| 维度 | 业务配额 (本节) | 上游配额 (旧版 §2.7) |
|---|---|---|
| 作用 | 限制**用户**消费 USD 速度 | 限制**上游订阅账号**消费窗口 |
| 检查时机 | relay 入口拦截 | 选号 + 响应后回写 |
| 维度 | 日/周/月 | 5h / 7d |
| 超限动作 | 429 + Retry-After | 账号软封禁 / AutoPauseAccount |
| 存储 | `user_subscriptions` | `account_quota_snapshots` |

两者**串联**:用户先过业务配额(拦截),再选号(避开上游配额已耗尽的账号)。

---

## 5. Relay 层设计(旧版方案)

> 旧版方案的 §1 文件布局 / §2 子系统设计保持不变,本节只标注**与业务层整合的关键点**与**新增的 channelv1 RPC**。

### 5.1 新增 channelv1 RPC(支撑旧版 §2.1-§2.7 + 本方案 §2.3)

| RPC | 用途 | 调用方 |
|---|---|---|
| `ListOAuthRefreshCandidates(within)` | 列即将过期的订阅账号 | TokenRefreshService |
| `ClearAccountError(id)` | 刷新成功后清错误 | TokenRefreshService.postRefresh |
| `SetAccountError(id, msg)` | invalid_grant 时标 error | TokenRefreshService |
| `SetTempUnschedulable(id, until, reason)` | 软封禁账号 | TokenRefreshService |
| `ClearTempUnschedulable(id)` | 刷新成功后清软封 | TokenRefreshService |
| `RecordAccountRuntimeBlock(id, until, reason)` | 运行时熔断 | RuntimeBlocker |
| `RecordAccountQuotaSnapshot(id, snapshot)` | 5h/7d 快照落库 | AutoPause |
| `GetAccountQuotaSnapshot(id)` | 读快照 | AutoPause |
| `AutoPauseAccount(id, reason)` | 配额耗尽自动暂停 | AutoPause |

### 5.2 旧版方案 8 阶段摘要

完整设计在 `docs/subscription-upgrade-plan.relay-only.md`,这里给一张表:

| 阶段 | 子系统 | 改动量 | 与本方案 §3 的关系 |
|---|---|---|---|
| §A1 | TokenRefreshService | ~700 LoC | 独立;被 §3 用量回写间接调用 |
| §A2 | Redis token cache | ~200 LoC | 独立 |
| §B1-B3 | 多层 Scheduler | ~1550 LoC | 独立;选号结果会进入 §3 用量累计 |
| §C1-C3 | AccountPool + RuntimeBlocker + EWMA | ~1000 LoC | 独立 |
| §D1 | FailoverLoop | ~400 LoC | 独立 |
| §E1 | Codex 5h/7d 配额窗口 | ~600 LoC | **与 §3 业务配额正交,各自独立** |
| §F1 | ErrorPassthrough + 并发错误 | ~500 LoC | 独立 |
| §G1 | OAuth 授权码流程 | ~900 LoC | **位置调整**:从新建 subscription 包改为 channel-service 内部(见 §6) |
| §H1 | Prometheus 指标 | ~300 LoC | 与 §3 metrics 合并暴露 |

### 5.3 当前落地状态

§5 已在 relay-gateway 的 Responses 路径落地,本阶段不新增数据库迁移:

- **多层调度入口**:`internal/relay/server/response_scheduler.go`。调度顺序为 `previous_response_id` 精确链路 → `session_hash` 粘性会话 → 原 `RelayUsecase.Plan`。
- **Previous-Response**:`internal/relay/server/response_route_scheduler.go` 统一本地 route 与 Redis sticky lookup,并拒绝 `msg_` 形态的 message id,避免把 message id 误当 response id。
- **Sticky Session**:`internal/relay/server/openai_ws_state_store.go` 新增独立 `openai_ws_session:` namespace,避免与 `openai_ws_resp:` response sticky key 冲突;支持 Bind / Lookup / RefreshTTL / Delete。
- **HTTP/WS 契约**:Responses HTTP body 与 WS 首帧支持 `session_hash` / `sessionHash`;同时支持 `X-Session-Hash` / `OpenAI-Session-Hash` header。header 优先于 body。
- **接入点**:`POST /v1/responses` HTTP/SSE 与 Responses WebSocket 共用同一 scheduler;命中 session sticky 时复用已绑定 channel,miss 时走原 normal plan 并在成功响应后绑定。
- **权限边界**:session sticky 命中后仍校验 token 的 allowed models;未配置 sticky store 时保留原 `RelayUsecase.Plan` 行为。
- **已覆盖测试**:`response_scheduler_test.go`、`response_route_scheduler_test.go`、`openai_ws_pool_test.go` 覆盖 fallback 顺序、message id 拒绝、session TTL/删除/续期和模型白名单。

---

## 6. OAuth 授权码流程(整合修正)

> 桌面方案 §3.3 把 OAuth 放在 `internal/subscription/biz/oauth.go`,旧版 §G1 放在 `internal/channel/biz/oauth/`。**本次统一放在 channel-service 内部**,理由:
> 1. OAuth 拿到的 token 直接落 `subscription_accounts` 表(已有),走 `CreateSubscriptionAccount` RPC
> 2. channel-service 已经是 `subscription_accounts` 表的 owner
> 3. 桌面方案用 `oauth_authorization_codes` 表(临时码)本次不建,改用进程内 `session_store`

### 6.1 模块位置

```
internal/channel/biz/oauth/
├── auth_url.go           # PKCE 生成 + Claude/Codex auth URL 构造
├── session_store.go      # 进程内 session map, 5 分钟 TTL, sweeper goroutine
├── claude_exchange.go    # Claude OAuth code 换 token + 解析 id_token
├── codex_exchange.go     # Codex OAuth code 换 token + 补 plan_type + opt-out
├── privacy.go            # disableOpenAITraining 调 chatgpt.com/backend-api
├── account_info.go       # fetchChatGPTAccountInfo 调 /accounts/check
└── service.go            # OAuthService 顶层,被 channel-service HTTP 调用
```

### 6.2 channelv1 HTTP 端点(供 admin-web 调用)

| Method | Path | 说明 |
|---|---|---|
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/auth-url` | 返回 auth_url + session_id |
| POST | `/api/v1/admin/accounts/subscription/oauth/claude/exchange` | 用 code 换 token,创建/更新 subscription_account |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/auth-url` | 同上 |
| POST | `/api/v1/admin/accounts/subscription/oauth/codex/exchange` | 同上 + 补 plan_type + opt-out |

注:这些端点在 channel-service 的 `internal/channel/server/http.go` 已有部分基础设施,新增路由参考现有 `subscription_accounts` 路由。

---

## 7. 阶段交付计划(合并版)

> 跨业务层 + Relay 层,共 8 阶段。每阶段独立可测、独立可回滚(走 feature flag)。每阶段交付物:代码 + 单测 + README + CHANGELOG。

| 阶段 | 名称 | 子方案 | 改动量 | 前置依赖 |
|---|---|---|---|---|
| **§1** | 业务层 DB + Repository + Usecase 骨架 | 桌面 §3.1-3.2 | ~800 LoC | 无 |
| **§2** | 业务层 QuotaChecker + ExpiryChecker | 桌面 §3.3-3.6, §3.8 | ~600 LoC | §1 |
| **§3** | 业务层 HTTP 端点 + 用量回写接入 | 桌面 §3.7, §4.1 | ~400 LoC | §2 |
| **§4** | TokenRefreshService 替换 RefreshTask | 旧版 §A1 | ~700 LoC | 无 |
| **§5** | 多层 Scheduler + Sticky Session + Previous-Response | 旧版 §B1-B3 | ~1550 LoC | §4 |
| **§6** | AccountPool + RuntimeBlocker + FailoverLoop | 旧版 §C1-C3, §D1 | ~1700 LoC | §5 |
| **§7** | Codex 5h/7d 配额 + ErrorPassthrough + OAuth 整合 | 旧版 §E1, §F1, §G1(改位置) | ~2000 LoC | §6 |
| **§8** | Prometheus 指标 + 集成收尾 | 旧版 §H1 + 桌面 §3.9 接入 | ~500 LoC | §3, §7 |

**总规模**:~8,250 LoC 新代码 + 1 个 channelv1 proto 改 + 1 个新 subscriptionv1 proto + 1 个 group proto。

### 7.1 推荐交付顺序

按价值优先 + 风险递减排序:

1. **§1**(业务层骨架) —— 给后续所有阶段提供数据落点
2. **§4**(TokenRefreshService) —— 必须最先,后续所有阶段都依赖 hook 链
3. **§2**(业务层 QuotaChecker) —— 让 §3 的接入有内容
4. **§3**(业务层接入) —— 业务价值早交付
5. **§5**(粘性调度) —— sub2api 最大价值
6. **§6**(运行时熔断 + Failover) —— 生产事故自动止损
7. **§7**(Codex 配额 + 错误透传 + OAuth 整合) —— sub2api 独有亮点
8. **§8**(指标 + 集成) —— 收尾

每个阶段结束给一个 `git tag`(`v0.4.0-rc1` … `v0.4.0-rc8`),出问题可回滚。

### 7.2 并行可能性

- §1 业务层 DB 改动 与 §4 TokenRefreshService 完全独立,可并行
- §2 业务层 QuotaChecker 与 §5 Scheduler 也可并行
- 并行约束:同一文件(尤其 `wire_gen.go`)同一时刻只能有一个人改

---

## 8. 接口契约变更

### 8.1 新增 `api/subscription/v1/`

```protobuf
syntax = "proto3";
package subscription.v1;
option go_package = "micro-one-api/api/subscription/v1;subscriptionv1";

service SubscriptionService {
  rpc GetUserSubscription(GetUserSubscriptionRequest) returns (GetUserSubscriptionReply);
  rpc GetSubscriptionProgress(GetSubscriptionProgressRequest) returns (GetSubscriptionProgressReply);
  rpc RecordUsage(RecordUsageRequest) returns (RecordUsageReply);
  rpc CheckQuota(CheckQuotaRequest) returns (CheckQuotaReply);
}
service SubscriptionAdminService {
  rpc AssignSubscription(AssignSubscriptionRequest) returns (AssignSubscriptionReply);
  rpc RevokeSubscription(RevokeSubscriptionRequest) returns (RevokeSubscriptionReply);
  rpc ExtendSubscription(ExtendSubscriptionRequest) returns (ExtendSubscriptionReply);
  rpc ResetSubscriptionQuota(ResetSubscriptionQuotaRequest) returns (ResetSubscriptionQuotaReply);
  rpc ListSubscriptions(ListSubscriptionsRequest) returns (ListSubscriptionsReply);
}
service GroupService {
  rpc CreateGroup(CreateGroupRequest) returns (CreateGroupReply);
  rpc GetGroup(GetGroupRequest) returns (GetGroupReply);
  rpc ListGroups(ListGroupsRequest) returns (ListGroupsReply);
  rpc UpdateGroup(UpdateGroupRequest) returns (UpdateGroupReply);
  rpc DeleteGroup(DeleteGroupRequest) returns (DeleteGroupReply);
  rpc AssignAccountToGroup(AssignAccountToGroupRequest) returns (AssignAccountToGroupReply);
}
```

### 8.2 channelv1.proto 新增(支撑旧版方案)

见 §5.1 表。

### 8.3 relay-gateway 配置

```yaml
# configs/relay-gateway.yaml
subscription:
  enabled: false           # 业务层 feature flag
  expiry_check_interval: 1h
  quota_cache_ttl: 30s
  default_estimated_cost_usd: 0.01   # 入口处无法估算时的兜底

hybrid_adaptor:
  enabled: false
  refresh_interval: 5m
  refresh_lookahead: 24h
  # 新增
  token_refresh:
    enabled: true
    check_interval_minutes: 5
    refresh_before_expiry_hours: 24
    max_retries: 3
    retry_backoff_seconds: 2
    temp_unsched_duration: 10m
  scheduler:
    enabled: true
    sticky_session_ttl: 1h
    previous_response_ttl: 1h
    max_account_switches: 10
    max_account_switches_gemini: 3
  ratelimit:
    runtime_block_401: 2m
    runtime_block_5xx: 2m
    runtime_block_429: 5s
    quota_auto_pause:
      primary_threshold: 95.0
      secondary_threshold: 100.0
      stale_after: 2h
  passthrough:
    rules:
      - { status: 429, action: passthrough }
      - { status: 401, action: passthrough }
      - { status: 403, action: passthrough }
      - { status_contains: "cyber_policy", action: passthrough }
```

---

## 9. 测试策略

### 9.1 单元测试

每个新文件配 `_test.go`:

| 文件 | 用例 |
|---|---|
| subscription_usecase_test.go | Assign 校验(重复分配/不存在 group);Revoke 仅允许 active;Extend 不能改 revoked;RecordUsage 滚动窗口 |
| quota_checker_test.go | 日/周/月三维独立判断;estimated_cost 超限拒绝;窗口到期重置 usage |
| group_usecase_test.go | CRUD 完整路径;name 唯一约束;删除有订阅的 group 返回 error |
| expiry_checker_test.go | 过期标记;24h 提前通知;revoked 不再发通知 |
| refresh_service_test.go | 成功路径调 1 次 Refresh;3 次失败 → 1 次 SetTempUnschedulable;invalid_grant → NonRetryable;成功后 ClearAccountError + ClearTempUnschedulable + InvalidateToken + OnUnscheduleCleared;退避 2/4/8s;Stop 幂等 |
| redis_token_cache_test.go | nil 客户端返回 ErrCacheMiss;过期返回 ErrCacheMiss;TTL 自适应;Invalidate 删 key |
| sticky_session_test.go | Bind 后 Get 返回 accountID;TTL 到期 miss;RefreshTTL 续期;Delete 删 key |
| previous_response_test.go | 解析合法 resp_xxx;拒绝 message_id 形如 msg_xxx;TTL 失效回退到 Layer 3 |
| layered_scheduler_test.go | Layer 1-4 fallback 顺序;RuntimeBlocker 阻断时降级 |
| account_pool_test.go | IsSchedulable 过滤组合;TempUnschedulable 软封禁;RuntimeBlocker 阻断;QuotaWindow 暂停 |
| runtime_blocker_test.go | 5xx 触发 BlockUntil;401 触发 BlockUntil;TTL 到期自动解除;多源合并 |
| account_selector_test.go | priority desc;LastUsedAt 越久越优先;OAuth 偏好 |
| failover_loop_test.go | 同账号重试 3 次;3 次后临时封禁;跨账号切换 maxSwitches;ForceCacheBilling 标记 |
| quota_window_test.go | primary=300,secondary=10080 → primary=5h,secondary=7d;stale > 2h 放行;5h ≥ 95% 触发 AutoPause |
| error_passthrough_test.go | cyber_policy 走 Passthrough;401/403/429 走 Passthrough;其他 4xx 走 Wrap;5xx 走 Wrap + RuntimeBlocker |
| concurrency_error_test.go | user scope Retry-After 头;account scope;streamStarted=true 时 SSE 事件 |
| oauth_exchange_test.go | PKCE 生成确定性;state 校验;invalid_grant 走 NonRetryable;fetchChatGPTAccountInfo 补 plan_type;disableOpenAITraining 失败 best-effort |

### 9.2 集成测试

`test/integration/subscription_e2e_test.go` 用 `docker-compose` 拉起 channel-service + identity-service + subscription-service + redis + postgresql,模拟:

1. **业务流**:admin Assign → user 调 chat completions → CheckQuota 通过 → 计费回写 → 第二次请求 progress 上升
2. **配额拦截**:user 用完日配额 → 下次请求 429 + Retry-After
3. **OAuth 流程**:admin 点"绑定 Codex" → 调 auth-url → 模拟浏览器走通 → exchange → subscription_account 创建 → 用 codex account 发请求成功
4. **粘性命中**:同一 sessionHash 两次请求命中同一账号(unit test 覆盖)
5. **多账号切换**:3 个账号 token 全 invalid_grant,第 4 次请求得到 401 不是 5xx
6. **指标可见**:`curl /metrics | grep relay_` 能看到全部新增指标

### 9.3 Mock 与 fixture

- `internal/relay/credential/fake_provider_test.go`:可注入 `errs []error` 序列
- `internal/relay/scheduling/fake_account_repo_test.go`:可注入 candidates
- `internal/subscription/biz/fake_repo_test.go`:内存版 SubscriptionRepository
- `internal/channel/biz/oauth/fake_openai_test.go`:`httptest.Server` 模拟 chatgpt.com/backend-api
- `test/integration/compose.yaml`:redis:7-alpine + postgresql:16-alpine

---

## 10. 风险与权衡

### 10.1 风险表

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| wire_gen.go 手改后 wire 命令跟手改冲突 | 中 | wire 不能跑 | wire_gen.go 顶部加注释 "DO NOT REGENERATE";改 wire 顺序时只追加 provider,不动已有顺序 |
| 现有 HTTP handler 改动引入 regression | 中 | 上游功能挂 | 走 feature flag,先在 staging 跑 1 周;每阶段独立 tag |
| Redis 不可用导致 refresh 路径全挂 | 低 | 用户全挂 | `IsTempUnscheduled` 双源(进程内兜底);`RedisTokenCache` 失败时 warn 不 fail 请求 |
| 上游 OpenAI 401 误判把账号封死 | 低 | 用户全挂 | RuntimeBlocker 软封禁 TTL 短(2 分钟);自动过期后下次 cycle 重试 |
| 配额自动暂停阈值过严导致用户被拒 | 中 | 体验差 | 阈值走配置,可调;stale 检查防误伤 |
| 5h/7d 配额窗口解析跟上游变化不一致 | 中 | 自动暂停失效 | sub2api 走 `window_minutes` 比大小;ParseCodexSnapshot 单测覆盖 4 种字段组合 |
| 业务层 DB migration 失败 | 低 | 全站挂 | 迁移用事务,失败回滚;新表加 NOT NULL 时先加可空,数据迁移完再改 NOT NULL |
| `user_subscriptions` 与 `users.group` 字符串冲突 | 中 | 数据不一致 | 业务层独立 group 跟 routing group 分两套,新表命名为 `subscription_groups` 避免歧义 |
| QuotaSnapshot 进程内缓存导致多副本不一致 | 低 | 配额判断延迟生效 | TTL 30s,RecordUsage 后主动 invalidate;用户量小时可接受 |

### 10.2 不在本次范围(明确写出避免 scope creep)

- **TLS fingerprint / uTLS**:需要 utls 库,与现有 `crypto/tls` 不兼容
- **WS v2 重连 + `previous_response_id` 完整 state replay**:本次只读不写,不重放 state chain
- **Anthropic ServiceAccount / Vertex**:本次不覆盖
- **Antigravity**:sub2api 自研平台,本次不覆盖
- **CR/CRS 多租户同步**:sub2api 企业版特性
- **admin 后台 UI 联调**:本次只提供后端能力
- **OpenAI 图像速率、Antigravity 500 惩罚**:sub2api 平台特定逻辑
- **active monitor(`channel_monitor_*`)**:sub2api 主动拨测子系统,需要独立 worker 进程
- **sub2api `OAICompatMessagesBridge` / `OpenAIPassthrough` 完整版**:HTTP/1.1 passthrough 已有简化版
- **EWMA `(1 - errorRate)` 加权选号**:本次只算指标,不参与选号
- **Calendar-based 配额滚动**:本次用 sliding window(从首次使用起 24h/7d/30d)

### 10.3 接受的技术债

1. **OAuth session_store 进程内**。多副本 relay-gateway 部署时,OAuth 授权只能在发起 auth-url 的副本上 exchange。缓解:admin-web 记住副本路由;或者扩到 Redis(留 §7 后续)
2. **QuotaSnapshot 节流进程内**。多副本节流会失效,但 30s 节流本就宽,可接受
3. **EWMA 不参与选号**。本次只算指标,后续可接
4. **`previous_response_id` 简化**。只读不写,不重放 state chain
5. **业务层 group 跟 channel.group 字符串两套**。前者是配置层,后者是路由键。本次不强制统一,新增业务必须用 `subscription_groups`,老路由路径继续用 `users.group`
6. **sliding window 配额**。sub2api 用 calendar,本次用 sliding。后续可加 cron trigger 切到 calendar

---

## 11. 落地 Checklist

- [x] §1: 业务层 DB + Repository + Usecase 骨架
- [x] §2: QuotaChecker + ExpiryChecker
- [x] §3: 业务层 HTTP + 用量回写接入
- [x] §4: TokenRefreshService
- [x] §5: 多层 Scheduler
- [ ] §6: AccountPool + RuntimeBlocker + FailoverLoop
- [ ] §7: Codex 5h/7d + ErrorPassthrough + OAuth 整合
- [ ] §8: Prometheus 指标 + 集成收尾
- [ ] 集成测试 + compose 拉起
- [ ] CHANGELOG + 文档更新
- [ ] 灰度发布:staging 一周,生产开关默认关闭,按 10% / 50% / 100% 灰度

---

## 12. 验收标准

1. **业务流**:admin AssignSubscription → 用户走通 chat completions → progress 显示用量上升
2. **配额拦截**:用户用完日配额,下次请求 429 + Retry-After
3. **OAuth 流程**:admin 点击"绑定 Codex" → auth-url → 浏览器授权 → exchange → 账号入库 → 用新账号发请求成功
4. **故障恢复**:token 缓存清掉 + 关上游 5min,服务能自动 retry + temp_unsched + post-refresh 清错误 3 步走完
5. **粘性命中**:同 sessionHash 两次请求命中同一账号
6. **多账号切换**:3 个账号 token 全 invalid_grant,第 4 次请求得 401 不是 5xx
7. **指标可见**:`curl /metrics | grep -E 'relay_|subscription_'` 能看到全部新增指标
8. **故障注入**:Redis 整体挂,服务仍能跑(走进程内兜底)
9. **代码质量**:`go test ./...` 全绿,`golangci-lint` 无新增 warning,`gofmt -l` 无输出

---

## 13. 文档更新

落地过程中同步更新:
- `docs/ARCHITECTURE_REFACTOR.md`:加 "Subscription Account Lifecycle" + "User Subscription Management" 章节
- `docs/subscription-account-setup-guide.md`:加 OAuth 授权码绑定流程
- `docs/hybrid-relay-adaptor-apicompat-plan.md`:加 scheduler / failover / passthrough 章节
- `CHANGELOG.md`:每阶段一个 entry
- `README.md` 配置章节:加 §8.3 的 yaml 配置

---

## 14. 参考

- 旧版方案:`docs/subscription-upgrade-plan.relay-only.md`(已归档,本方案的 §5 摘录其概要)
- 桌面方案:`~/Desktop/micro-one-api-subscription-enhancement-plan.md`(已合并,见 §2-3, §6-10 的对应)
- sub2api 源码:`/Users/mengbin/vscode/neo/sub2api`
  - `backend/internal/service/subscription_service.go` - 用户订阅管理
  - `backend/internal/service/user_subscription.go` - UserSubscription 实体
  - `backend/internal/service/subscription_expiry_service.go` - 过期检查
  - `backend/internal/service/subscription_maintenance_queue.go` - 维护队列
  - `backend/internal/service/openai_account_scheduler.go` - 多层调度
  - `backend/internal/service/token_refresh_service.go` - Token 刷新鲁棒化
  - `backend/internal/service/openai_gateway_service.go` - 配额窗口 / 故障转移
  - `backend/internal/service/identity_service.go` - fingerprint 管理
  - `backend/internal/service/oauth_service.go` + `openai_oauth_service.go` - OAuth 流程
  - `backend/internal/handler/failover_loop.go` - 故障转移循环
  - `backend/internal/handler/gateway_helper.go` - 错误透传
- micro-one-api 现有实现:`/Users/mengbin/vscode/neo/micro-one-api`
  - `internal/relay/credential/` - token 层骨架(已具备)
  - `internal/relay/identity/` - mimicry 三维(已具备)
  - `internal/relay/adaptor/` - claude_oauth / codex_oauth 适配器(已具备)
  - `internal/channel/biz/channel.go` - SubscriptionAccount 已建模
  - `internal/relay/server/openai_ws_state_store.go` - Redis 跨进程粘性样板(可复用)
  - `internal/pkg/middleware/ratelimit_redis.go` - Redis 限流样板(可复用)

---

> **写在最后**:本方案的目标是同时补齐"用户订阅管理"业务层(让 sub2api 的 sub_admin/sub_user 用法可移植)与"订阅账号代理"技术层(让 sub2api 的故障恢复机制可移植),而不是单做一边。两份原始方案各自有侧重、有重复,合并后去重、调整位置(例如 OAuth 从 subscription 包移到 channel 包)、加 feature flag 与并行说明,形成 8 阶段可独立交付的合并版。
