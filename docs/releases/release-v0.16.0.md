# Micro-One-API v0.16.0 发布：上线收尾、契约加固、运营增强与工程卫生

> 2026-08-06 · 上一版：[v0.15.3](./release-v0.15.3.md)（2026-08-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.16.0)

v0.16.0 是 v0.15.3 之后的 **MINOR 功能版本**（7 个提交），标志着 v0.11.0 路线图收尾阶段
（P0–P3）的全部完成。核心交付为一个面向用户的新功能——**routing-ops 双源指标降级**，
以及一组契约加固与工程卫生工作：P1 同优先级精确回退 + 并发 active 唯一约束的确定性回归
测试、cache-creation 计费从 observe 切换为 charge 的生产闭环、6 个服务 conf 包测试、
billing_model TODO 清理、v0.16 路线图文档整合。

**无 API 破坏性变更、无数据库迁移、无 proto 变更**。新增 1 个可选配置项
（`RELAY_METRICS_ENDPOINT`），新增 1 个直接依赖（`prometheus/common`，从 indirect 提升）。
受影响服务为 admin-api。

## 变更内容

### 1. P2.3 — routing-ops 双源指标：Prometheus → relay-gateway 直采降级

**背景**：`admin-api` 的 `/api/admin/routing-ops` 通过 `PROMETHEUS_URL` 查询外部
Prometheus 获取 relay-gateway 的路由指标（error/fallback rate）。Prometheus 未配置或
查询失败时返回 `partial=true`，错误率和回退率不可用——这是 v0.11.0 路线图 §9.1 风险 2。

**变更**：引入双数据源 + 优雅降级策略：

```
admin-api /api/admin/routing-ops
  ├── 1. Prometheus（首选，PromQL increase() 精确窗口增量）  ← PROMETHEUS_URL
  ├── 2. relay-gateway /metrics 直连（降级，累计计数器）       ← RELAY_METRICS_ENDPOINT
  └── 3. partial=true（两者均不可用）
```

- `platform/metrics/routing_rates_scrape.go`（新）：`ScrapeRoutingRates` 通过 HTTP GET
  relay-gateway 的 `/metrics` 端点，用 `expfmt` 解析 Prometheus exposition format，
  聚合 `routing_selection_total` 与 `routing_fallback_total` counter。
- `platform/metrics/routing_rates.go`：`RoutingRates` 新增 `Source` 字段
  （`"prometheus"` / `"relay_scrape"`）。
- `app/admin/internal/server/routing_ops_rates.go`（新）：`loadRoutingRates` 实现双源
  fallback（Prometheus → relay scrape），非致命错误透传。
- `app/admin/internal/server/routing_ops.go`：handler 改为调用 `loadRoutingRates`；
  `routingOpsRates` JSON 新增 `source` / `cumulative` 字段，前端据此区分"窗口增量"
  与"累计值"展示。
- `deployments/docker-compose/docker-compose.yml`：admin-api 段新增
  `RELAY_METRICS_ENDPOINT=http://relay-gateway:8080` 默认值。
- `go.mod`：`prometheus/common` 从 indirect 提升为 direct（expfmt 依赖）。

**影响**：Prometheus 故障时 routing-ops 视图保持 `partial=false`，以累计计数器提供路由
健康度基线。仅 Prometheus 与 relay scrape 均不可用时才标记 `partial=true`。10 条回归测试
覆盖双源优先级、降级、全故障等路径。

### 2. P1 — 契约与资金加固（回归测试 + 生产结论）

**P1.1 — 同优先级精确回退（§9.1 风险 1）**：channel-service selector 此前已扩展
`excluded_channel_ids` / `excluded_account_ids`（实现于 v0.15.x），RetryExecutor 按候选
逐个行走。本次补齐两条确定性回归测试：

- `APIKeyChannelSameTierFallback`：同层失败渠道回退到其兄弟渠道。
- `APIKeyChannelExhaustsTierThenLower`：同层全部耗尽后才降层。

**P1.2 — user_subscriptions 并发 active 唯一约束（H10 根治）**：三方言唯一索引
（MySQL 生成列 `059` / SQLite partial `001` / Postgres `003`）此前已落地。本次补齐
5 条确定性回归测试：

- 数据层（真实 SQLite partial unique index）：第二行 active 插入碰撞索引、
  expired/revoked 不受约束、per-user scope 验证。
- usecase 层（并发 race 窗口）：两个 goroutine 竞态穿过 pre-check，DB index 拒绝
  重复——覆盖 `Assign` 与 `AssignOrExtend` 两条路径。

**P1.3 — 订阅粘性收益验证（#7 第一步）**：生产指标落档（445 routing selections，
全部 `sticky_hit=false` / 单 bucket；`sticky_total = 0` series），结论**维持现状**
——粘性子系统已接线但未激活（无客户端发送 `session_hash`，无多账号 fan-out），
复用率数据见 [p1-contract-hardening-conclusion.md](../design/p1-contract-hardening-conclusion.md)。

**影响**：P1 契约加固以确定性测试 + 生产数据闭环。无代码行为变更（测试 + 文档）。

### 3. P0 — cache-creation observe → charge 生产切换

`BILLING_CACHE_CREATION_MODE=observe` 机制（五桶 token 语义、影子成本、开关）随
v0.11.0 Phase 1 落地。observe 模式首发并完成结算周期对账（token 桶、影子成本、供应商
账单、ledger 去重键）后，**生产已切换 `charge`（2026-08-06）**，cache-creation 价格
正式计入用户余额。回滚路径保留：切回 observe 即可，新增列不回滚。

**影响**：计费行为变更（observe 仅记录影子成本 → charge 实际扣费），属运维操作而非
代码变更。无新增迁移、无配置项变更。

### 4. P3 — 工程卫生

- **6 个服务 conf 包测试**：为 admin / config / identity / log / monitor / notify 补
  `registry_test.go`，覆盖 nil-Consul 路径、完整字段映射（Address +
  HealthCheckInterval）、metadata 防御性拷贝隔离。
- **billing_model.go TODO 清理**：`channel_mapped ≡ upstream` 的限制从 TODO 转为永久
  NOTE（结构性变更超出当前计费模型，属已定档设计决策，见
  `docs/model-management-design.md` §13）。
- **v0.16 路线图文档整合**：新增 `docs/design/v0.16-roadmap.md` 作为 P0–P3 收尾文档，
  将各设计/结论文档中的 `.workbuddy` 临时链接替换为提交版链接。
- **mock 竞态修复**：`mockConcurrentCreateRepo` 补 `GetActiveSubscriptionByUser`
  方法加锁，使 P1.2 并发测试在 `-race` 下不再 panic。
- **gofmt 收尾**：`subscription_usecase_test.go` import 排序、
  `routing_rates.go` 重复注释行删除。

**影响**：工程卫生。无对外行为变化。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **数据库**：无新增迁移文件。
- **配置**：新增 1 个可选配置项 `RELAY_METRICS_ENDPOINT`（默认
  `http://relay-gateway:8080`，docker-compose 已内置）。不设置时仅影响 routing-ops
  降级能力，不影响其他功能。
- **依赖**：`prometheus/common` 从 indirect 提升为 direct（`expfmt` 解析依赖）。
- **运行时**：routing-ops 双源降级为 admin-api 内部行为，前端 JSON 新增 `source` /
  `cumulative` 字段为 additive，不影响现有消费方。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.16.0

# 仅 admin-api 有代码变更，交叉构建并部署该服务即可：
./scripts/deploy-update.sh admin-api

# 可选：确认 docker-compose 中 admin-api 段已有 RELAY_METRICS_ENDPOINT
# (v0.16.0 docker-compose.yml 已内置默认值 http://relay-gateway:8080)
```

## 验证

- `make test-unit`：受影响包全绿（`platform/metrics`、`internal/biz`、
  `domain/subscription/{biz,data}`、6 个服务 `conf` 包）。
- `gofmt -l`：受影响目录 clean。
- P2.3 双源降级 10 条回归测试通过。
- P1 契约加固 7 条确定性回归测试通过。
- 生产 cache-creation charge 切换已对账闭环（2026-08-06）。

## 完整变更日志

- test(p1): add deterministic regression tests for P1 contract hardening
- docs(p1): P1 contract hardening conclusion — document stickiness data
- feat(admin): P2.3 routing-ops dual-source metrics — relay-gateway scrape fallback
- test(p3): add conf package tests for 6 services
- chore(p3): clean up billing_model TODO + update TODO.md status
- style(gofmt): fix import ordering in subscription_usecase_test.go
- docs(v0.16): consolidate v0.16 roadmap + fix mock race + dedup comment
