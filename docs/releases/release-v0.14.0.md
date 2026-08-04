# Micro-One-API v0.14.0 发布：订阅续费策略可观测、故障态限流断言固化与审查清单收官

> 2026-08-04 · 上一版：[v0.13.3](./release-v0.13.3.md)（2026-08-03）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.14.0)

v0.14.0 是 **MINOR 版本**，包含 4 个提交（1 feat + 2 fix + 1 docs），为订阅续费链路补齐 `renewal_strategy` 可观测字段（含 1 个 additive 数据库迁移）、修复 admin 延长订阅的并发写与过期激活缺陷、为 Redis 故障态并发语义补充多副本断言并文档化 fail-open 权衡，同时完成 code-review L 系列核验（审查清单基本收官，仅剩 M6 设计决策项）。

**1 个数据库迁移（077，additive）**。受影响的运行时服务为 `admin-api`、`billing-service`。

## 变更内容

### 1. 订阅续费策略可观测 —— `renewal_strategy` 字段（M2）

**背景**：「过期未撤销」的续费策略此前随 1h 过期扫描时机漂移，且没有任何字段记录该用户续费实际走了哪条路径。行为本身已由 domain-C1（`expires_at > now` 守卫，v0.13.0）固定：未过期→原地延长、过期→新建。

**修复**：

- `user_subscriptions` 新增 `renewal_strategy` 列（迁移 `077`，MySQL + sqlite + postgres 三驱动，additive，默认空串）。
- `AssignOrExtend` 复用未过期 active 行时写 `extend`；`Assign`/无 active（含过期）新建时写 `new`；`Extend`（admin 延长）写 `extend`。运营与对账可据此区分两种发放路径。
- **`Extend` 并发写修复（domain-H1）**：原实现用全量 `UpdateSubscription` 从读快照回写，会 clobber 并发 `AddUsage` 的 usage 增量；改为窄字段写（`expires_at` + `renewal_strategy`）。
- **`Extend` 过期激活修复**：延长已由 checker 标记为 `expired` 的订阅时，status 一并置回 `active`——否则 active 读路径（`status='active' AND expires_at > now`）会让用户付钱后无可用权益。
- 测试：biz（extend/new 记录 + 窄字段持久化 + 过期激活）+ data（round-trip、窄更新隔离、过期守卫交互）。

**影响服务**：`admin-api`、`billing-service`。

### 2. Redis 故障态并发语义 —— 多副本断言与 fail-open 文档化（M9 / H11 / M8）

**背景**：review M9 指出并发 cap 压测在单进程 smoke 下无法证伪故障场景——Redis 不可用时每个副本各自 fallback 到本地内存限流器，全局 cap 退化为 N×limit，且无自动化测试覆盖。

**修复**：

- 新增 `TestRedisAccountConcurrencyLimiter_MultiReplicaFailOpenExceedsCap`：两个副本 + 共享 store + 注入 Redis 故障——故障期双双放行（per-replica fail-open）、各副本本地限流仍生效、恢复后共享 cap 恢复权威。RPM 限流器同款断言（M8）。
- 三处 limiter 类型注释（`account_concurrency.go` / `account_rpm.go` / `subscription_session_window.go`）明确 fail-open 权衡：**可用性优先，故障期全局 cap = N×limit**；未来若引入副本数感知加权 cap（limit/N）必须有意更新已固化的断言。

**影响服务**：`relay-gateway`。

### 3. Code-review L 系列核验（文档）

- L1 非法时区回退写 stderr 定位日志 ✅；L2 告警去重按 `(kind, window)` 复合 ✅；L3 `clearMarkers` 合并单次原子调用 ✅；L4 快照优先 + 解码 fail-closed（不再回退实时 plan）✅；L5 `Inflight` 用 `ZCount` 排除过期 lease（崩溃槽位受 leaseTTL 约束，接受）✅。
- 至此 review 清单 H1-H11 / M1-M10 / L1-L5 全部有结论；唯一保留项 M6（升级重置 usage 为刻意设计、乐观并发锁未确认）属设计决策。

## 兼容性说明

- **API**：无破坏性变更。
- **数据库**：迁移 `077_add_subscription_renewal_strategy.sql`（MySQL 根目录 + sqlite/ + postgres/），additive 加列，默认空串；旧代码可读写，滚动升级安全，无需回填。
- **配置**：无新增配置项。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.14.0

# cross-build 并部署受影响的两个服务（deploy-update.sh 会自动应用迁移 077）
./scripts/deploy-update.sh admin-api billing-service
```

## 验证

- 迁移 077 应用后 `user_subscriptions.renewal_strategy` 存在，新发放写 `extend`/`new`。
- admin 延长已过期订阅后用户恢复 active 权益（status 回到 active）。
- `Extend` 不再 clobber 并发用量（usage 列保持）。
- 多副本 fail-open 断言在 CI 通过（concurrency + RPM）。
- `go build ./...`、`go vet`、`check-architecture.sh`、全部单元测试通过。

## 完整变更日志

- feat(domain): record renewal strategy on user_subscriptions (M2)
- test(relay): pin multi-replica Redis fail-open semantics (M9/H11/M8)
- docs(review): verify L-series items against current code
- fix(domain): Extend records renewal strategy, narrow-write, reactivates expired (M2)
