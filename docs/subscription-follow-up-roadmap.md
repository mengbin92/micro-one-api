# 订阅系统后续规划路线图

> 基线: v0.4.0 / v0.5.0 联合发布后,当前 `develop`。
> 范围来源: `docs/release-v0.4.0-v0.5.0.md` 的“后续规划”。

本文档把发布稿里的后续方向拆成可执行分支和验收标准。原则是先补可观测和运维闭环,再推进会改变账务语义的产品化能力。

## 目标

- 订阅账号治理可自动化: quota reset、异常账号恢复、额度事件告警都有清晰策略和可观测结果。
- 订阅套餐可运营: 套餐上下架、续费、退款/冲正、订阅变更和运营报表按低风险顺序落地。
- Relay 稳定性可验证: Redis 并发控制、session sticky、failover 组合场景有压测脚本、指标和回归门槛。
- 生产文档可交付: OAuth 绑定、套餐配置、额度治理和生产发布 runbook 能直接指导部署与排障。

## 非目标

- 不在一个大分支里同时修改订阅账号治理、支付账务、Relay 压测和文档。
- 不先实现复杂退款/订阅变更,再反推账务模型。账本冲正和幂等边界必须先明确。
- 不把更多 sub2api 字段直接搬进调度热路径。先确认运营需求,再进入选路逻辑。

## 推荐分支拆分

| 顺序 | 分支 | 目标 | 主要风险 | 状态 |
| --- | --- | --- | --- | --- |
| 0 | `docs/release-v0.5-followup-plan` | 固化本路线图并更新发布稿引用 | 无代码风险 | ✅ 已完成 |
| 1 | `feat/subscription-account-ops-automation` | 账号 quota reset 自动化、异常账号恢复、额度事件告警 | 自动恢复误启用异常账号 | 独立分支,已合入当前基线， ✅ 已完成 |
| 2 | `feat/subscription-plan-lifecycle` | 套餐上下架、在售状态审计、用户侧展示收敛 | 已下单套餐快照与当前配置混淆 | ✅ 已完成 |
| 3 | `feat/subscription-renewal` | 同分组续费闭环、到期时间延长、续费订单展示 | 重复回调导致重复续期 | ✅ 已完成 |
| 4 | `feat/subscription-refund-reversal` | 退款/冲正账本语义、订单状态机和对账 | 余额/订阅权益双向回滚不一致 | ✅ 已完成 |
| 5 | `feat/subscription-change` | 订阅升级/降级/变更策略 | 需要依赖续费和冲正语义 | ✅ 已完成 |
| 6 | `test/relay-subscription-stress` | Redis 并发、sticky、failover 组合压测与指标门槛 | 本地和 CI 环境资源差异 | ✅ 已完成 |
| 7 | `docs/subscription-production-runbook` | 生产 runbook、OAuth 绑定、套餐配置、额度治理文档 | 文档与真实配置漂移 | ✅ 已完成 |

## 阶段 1: 订阅账号治理

### 1.1 quota reset 自动化

现状:

- `subscription_accounts` 已有总额、5h、daily、weekly、RPM、session 窗口和 reset 配置。
- 管理后台支持单账号/批量重置,但 reset 主要依赖人工操作。

任务:

- 明确 reset 策略枚举和执行边界:
  - `rolling`: 保持当前请求回写时滚动窗口的行为。
  - `fixed`: 支持按 `quota_timezone` 的自然日/自然周边界自动清零 daily/weekly。
- 增加后台任务或现有 worker 任务,定期扫描需要 fixed reset 的账号。
- reset 操作写入结构化事件,避免只有账号字段变化而无法审计。
- 指标暴露:
  - reset 执行次数。
  - reset 失败次数。
  - reset 扫描耗时。

验收:

- 同一账号 fixed daily 到时后只重置一次,重复 worker tick 不重复写事件。
- 非 fixed 策略账号不被后台任务修改。
- 非法时区按既有逻辑回退 `UTC`,并可在事件或日志中定位。

### 1.2 异常账号自动恢复

现状:

- runtime blocker 和账号 quota 耗尽会让账号跳过选路。
- 管理后台可看到状态,但异常恢复策略仍偏人工。

任务:

- 给异常类型分层:
  - 临时上游错误: `429`、`5xx`、`529`,按现有 TTL 自动恢复。
  - 授权异常: `401`、`403`,默认不自动启用,需要 OAuth 重绑或人工确认。
  - 本地额度耗尽: 等待窗口 reset 或人工重置。
  - Codex quota snapshot 耗尽: 等待 snapshot reset 后再恢复。
- 增加恢复前探测策略,只对可安全探测的平台执行轻量请求。
- 管理后台展示“为什么不可调度”和“预计恢复时间”。

验收:

- 授权异常不会被自动恢复任务误启用。
- quota reset 后账号能重新进入可调度集合。
- runtime TTL 到期后无需人工操作即可再次参与选路。

### 1.3 额度事件告警

任务:

- 基于 `subscription_account_quota_events` 和账号 quota 状态生成告警:
  - 账号额度已耗尽。
  - 账号额度即将耗尽。
  - 账号长时间无用量。
  - quota event 回写失败或持续降级。
- 复用现有 notify-worker 通道,避免新建告警投递链路。
- 管理后台风险告警里区分渠道告警、对账告警和订阅账号告警。

验收:

- 告警具备去重窗口,不会每次请求都重复投递。
- 关闭订阅账号治理告警后不影响已有渠道健康和对账告警。

## 阶段 2: 订阅产品化

### 2.1 套餐上下架

现状:

- `subscription_plans` 已有 `for_sale` 和排序字段。
- 用户购买路径已经优先使用 plan。

任务:

- 管理后台提供上下架操作和状态过滤。
- 下单时保存套餐快照,支付回调继续按订单快照发放,不受之后上下架影响。
- 用户侧只展示 `for_sale=true` 且关联 group 可用的套餐。

验收:

- 已下架套餐不能新下单。
- 已创建但未支付订单仍按订单创建时的快照完成或关闭。

### 2.2 续费

任务:

- 同一分组 active 订阅续费时延长 `expires_at`。
- 已过期但未撤销订阅可按策略重新激活或新建订阅,策略需固定为一种并记录在文档中。
- 支付回调幂等,重复回调不能重复延长有效期。

验收:

- 同一订单多次回放只产生一次续费效果。
- 续费订单、订阅记录和 billing ledger 可互相追踪。

### 2.3 退款/冲正

任务:

- 先定义账务语义:
  - 退款订单状态。
  - 冲正 ledger 类型。
  - 已消费订阅权益是否允许退款。
  - 退款后用户订阅是否撤销、缩短或保持。
- 实现退款/冲正幂等键。
- 增加对账任务覆盖退款和冲正。

验收:

- 重复退款回调不会重复返钱或重复冲正。
- 退款后 dashboard、usage、orders、cost analysis 口径一致。

### 2.4 订阅变更

任务:

- 在续费和冲正语义稳定后再实现升级/降级。
- 明确是否支持按剩余时间折算、立即生效或下周期生效。
- 同一用户仍保持唯一 active subscription,除非另起方案明确支持多 active。

验收:

- 变更前后的订阅权益、账本、订单和用量窗口有完整审计链路。

### 2.5 运营报表

任务:

- 增加套餐维度报表:
  - 新购/续费/退款订单数。
  - 套餐收入。
  - active / expired / revoked 订阅数。
  - 订阅用量和余额兜底比例。
- 支持按时间、套餐、分组、用户筛选。

验收:

- 报表数据来自账本/订单/订阅表聚合,不依赖前端内存抽样。
- 与现有成本分析页的收入口径一致。

## 阶段 3: Relay 稳定性

任务:

- 补压测脚本,覆盖:
  - 多副本 Redis account concurrency limiter。
  - Redis runtime blocker。
  - `session_hash` sticky。
  - `previous_response_id` route sticky。
  - 429/5xx/529 failover。
  - RPM 和 session window failover。
- 压测输出固定指标:
  - 成功率。
  - p50/p95/p99 延迟。
  - failover 次数与原因。
  - Redis fallback 次数。
  - 账号并发峰值是否超过配置。
- 将压测说明写入 runbook,CI 可先只跑轻量 smoke,完整压测留给预发环境。

验收:

- Redis 正常时,多副本同账号并发不超过配置。
- Redis 短暂不可用时,请求 fail-open 到内存 limiter,并有 fallback 指标。
- sticky 账号不可用时可以 failover 并重新绑定可用账号。

## 阶段 4: 文档与 Runbook

> 状态: ✅ 已完成。实现分支 `feat/subscription-productization-phase2`(与阶段 2/3 同分支落地)。

任务:

- 拆分并更新生产文档:
  - 订阅账号 OAuth 绑定。
  - 套餐配置和购买发放。
  - 订阅账号额度治理。
  - Redis 多副本部署。
  - 生产发布、回滚和排障 runbook。
- 每份文档包含:
  - 前置条件。
  - 必填配置。
  - 验证命令或页面。
  - 常见故障和恢复步骤。

交付物:

- [订阅账号 OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md):授权码 auth-url/exchange 两步流、字段来源、多副本 session 限制、token 刷新健康度、绑定排障。
- [订阅套餐配置与购买发放 Runbook](./subscription-plan-runbook.md):分组/套餐字段、上下架切换、plan_snapshot 快照发放、测试购买、续费幂等验证、运营报表。
- [订阅账号额度治理 Runbook](./subscription-account-quota-governance-runbook.md):reset scope、批量重置/模板、runtime block 分层恢复、AutoPause、额度事件幂等、指标。
- [订阅 Redis 多副本部署 Runbook](./subscription-redis-multi-replica-runbook.md):共享状态清单、并发 cap/跨副本 block/sticky/fail-open 验证、CI smoke 与预发全量。
- [订阅生产发布、回滚与排障 Runbook](./subscription-production-runbook.md):迁移清单与顺序、滚动发布、回滚策略、回归门槛、生产排障、文档索引。
- [Relay 稳定性压测与 Runbook](./relay-stress-runbook.md)(阶段 3 已交付,阶段 4 复用)。

验收:

- 新部署人员只按 runbook 能完成 OAuth 绑定、套餐上架、测试购买和 failover 验证。✅ 各 runbook 均含「验证」小节,可独立完成上述四项。
- release 文档只保留发布摘要,长期操作细节进入 runbook。✅ `docs/release-v0.4.0-v0.5.0.md` 的「后续规划」指向本路线图与 runbook 索引,不再承载操作步骤。
- 每份 runbook 含前置条件、必填配置、验证命令/页面、常见故障与恢复步骤。✅

## 建议优先级

第一轮实现建议从 `feat/subscription-account-ops-automation` 开始。原因是它主要基于 v0.5.0 已落地的数据和指标补闭环,比退款/订阅变更的账务风险低,也能马上提升生产可运营性。

第二轮推进 `feat/subscription-plan-lifecycle` 和 `feat/subscription-renewal`。退款/冲正、订阅变更应等订单状态机和账本冲正语义写清楚后再动手。

## 回归门槛

每个实现分支至少执行:

```bash
make test-unit
cd web && npm test && npm run lint
git diff --check
```

涉及 Relay 行为的分支追加:

```bash
go test ./internal/relay/... ./internal/channel/...
make test-e2e-suite
```

涉及支付、续费、退款或冲正的分支追加:

```bash
go test ./internal/billing/... ./internal/subscription/... ./internal/admin/...
```
