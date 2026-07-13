# Micro-One-API v0.6.0 发布公告

> 2026-07-09 · 上一版: v0.5.0 (2026-07-05)

v0.6.0 是 v0.5.0 之后围绕「订阅商品化、账号治理、用量查询与发布可运维性」的增量 MINOR 版本。范围覆盖 `v0.5.0..v0.6.0` 共 16 次提交、103 个文件、+11.7k/-161 行。

本版新增数据库迁移 `057-059`。升级前必须备份数据库,并在发布窗口内先执行迁移、再滚动重启服务。

## 亮点

- **订阅用量查询 API**:新增 `/v1/subscription/usage` 能力,返回用户订阅额度、已用量、剩余额度和下一次刷新时间。
- **套餐生命周期管理**:管理后台新增订阅套餐管理页,支持套餐创建、上下架、价格与有效期配置。
- **购买时套餐快照**:支付订单新增 `plan_snapshot`,订单发放与后续套餐上下架/删除解耦。
- **续费、退款与冲正语义**:补齐续费幂等、退款撤销/缩短订阅、订单到订阅的确定性关联。
- **订阅账号治理自动化**:新增 fixed 策略 daily/weekly 额度重置、账号恢复、额度告警、治理指标与 runbook。
- **Relay 稳定性压测**:新增订阅账号路径压测脚本、报告生成与压测 runbook。
- **单用户单 active 订阅约束**:数据库层新增唯一约束,避免并发支付回调或续费竞争产生多条 active subscription。

## 变更内容

### Added

- `api/billing/v1/billing.proto`:新增订阅用量查询与下一次刷新时间字段。
- `docs/design/subscription-usage-api.md`:新增订阅用量 API 文档。
- `web/src/pages/admin/SubscriptionPlansPage.tsx`:新增管理后台套餐管理页面。
- `internal/billing/biz/refund.go`:新增订阅订单退款处理、撤销和缩短订阅逻辑。
- `internal/billing/biz/plan_snapshot*.go`:新增套餐快照捕获与测试。
- `internal/billing/biz/subscription_report.go`、`internal/billing/data/operation_report_repo.go`:新增订阅运营报表与持久化。
- `internal/channel/biz/account_ops.go`:新增订阅账号额度重置与恢复 sweeper。
- `internal/channel/biz/quota_alert.go`:新增订阅账号额度告警评估器。
- `internal/relay/stresstest/report.go` 与 `scripts/benchmark/k6-relay-subscription-stress.js`:新增 Relay 订阅路径压测与报告。
- `migrations/057_add_plan_snapshot_to_payment_orders.sql`:订单记录购买时套餐快照。
- `migrations/057_create_subscription_account_quota_reset_runs.sql`:记录 fixed 策略额度重置运行,保证多副本幂等。
- `migrations/058_add_subscription_id_to_payment_orders.sql`:订单记录实际发放的订阅 ID。
- `migrations/059_enforce_single_active_subscription.sql`:限制同一用户只能存在一条 active subscription。

### Changed

- 订阅套餐发放使用订单创建时的 plan snapshot,避免套餐下架或修改影响已支付订单。
- 订阅额度刷新锚点修正为自然日/自然周窗口,并返回 next-refresh times。
- 订阅运营报表新增 usage/fallback ratio。
- 默认订阅用户 RPM 限制保持关闭,避免无配置时误限流。
- Docker Compose 与 Relay 配置补齐订阅治理相关默认配置。

### Fixed

- 支付提交阶段吸收实际订阅用量,避免预估用量和最终账本不一致。
- 修复订阅额度刷新锚点计算问题。
- 修复退款路径在用户已购买新订阅后错误回退到“当前 active 订阅”的风险。
- 修复并发创建 active subscription 时可能绕过业务层检查的问题。
- 处理订阅 usage API 代码审查反馈与边界用例。

## 数据库迁移

从 v0.5.0 升级到 v0.6.0 需要执行 MySQL 迁移 `057-059`:

```bash
go run ./cmd/migrate -dir ./migrations -status
go run ./cmd/migrate -dir ./migrations
```

发布前必须先检查是否存在重复 active subscription。若下列查询返回数据,需要先按业务规则保留一条 active 订阅、将其余记录改为 expired/revoked 后再执行 `059`:

```sql
SELECT user_id, COUNT(*) AS active_count
FROM user_subscriptions
WHERE status = 'active'
GROUP BY user_id
HAVING COUNT(*) > 1;
```

## 升级指南

1. 备份数据库,重点关注 `payment_orders`、`user_subscriptions`、`subscription_accounts` 和 billing 账本表。
2. 执行迁移状态检查,确认待执行迁移为 `057_add_plan_snapshot_to_payment_orders`、`057_create_subscription_account_quota_reset_runs`、`058_add_subscription_id_to_payment_orders`、`059_enforce_single_active_subscription`。
3. 执行重复 active subscription 预检,确保 `059` 唯一约束不会失败。
4. 执行数据库迁移。
5. 按依赖顺序滚动重启: identity → channel → billing → admin → relay → log/monitor/notify。
6. 发布后验证健康检查、订阅用量 API、套餐购买/发放、续费、退款、账号额度重置、账号告警和 Relay 订阅路径。

## 验证

本次发版前本地已执行:

```bash
make test-unit
cd web && npm run lint && npm test && npm run build
make build
make test-e2e-suite
```

真实 provider e2e 依赖 `PROVIDER_API_KEY`;未配置时相关用例会跳过。生产发版前仍需等待 GitHub Actions 的 CI 与 Security Pipeline 在 `302508a` 或后续发布提交上完成并通过。
