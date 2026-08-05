# Micro-One-API v0.15.1 发布：订阅换组用量保留 + 行锁并发保护、订阅流量渠道统计去噪

> 2026-08-05 · 上一版：[v0.15.0](./release-v0.15.0.md)（2026-08-04）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.15.1)

v0.15.1 是 v0.15.0 之后的 **PATCH 修复版本**（2 个提交），收尾订阅变更链路的最后两项代码审查缺陷：订阅换组（change subscription）的用量窗口只在**真正跨组**时才重置（同组改套餐保留已跑用量），并为 `ChangeSubscription` 增加行锁串行化（`SELECT ... FOR UPDATE`）防止并发变更互相覆盖。

**无数据库迁移、无 API 破坏性变更、无新增配置项**。受影响的运行时服务为 `relay-gateway`、`admin-api`、`billing-service`。

## 变更内容

### 1. 订阅换组用量窗口：仅在跨组时重置（M6）

**根因**：`ChangeSubscription` 的立即生效分支无条件清零日 / 周 / 月用量窗口
与窗口起始时间——即便用户只是**同组内**切换套餐（例如从基础版升到同价位高级版），
也会把正在累积的用量清掉。这既丢失了真实用量数据，又允许通过"同价套餐来回切"
免费刷新配额。

**修复**：

- 用量窗口重置改为**条件触发**：仅当请求的目标组与当前组不同
  （`req.ToGroupID != fromGroupID`）时，才将日 / 周 / 月用量清零并重置窗口起点；
  同组内的套餐变更保留运行中的用量，不再丢数据，也不再被"刷新配额"利用。

**影响服务**：`admin-api`、`billing-service`（共享 `domain/subscription`）。

### 2. 订阅变更行锁并发保护（M6）

**根因**：`ChangeSubscription` 的读-校验-改-写在无锁路径下执行，两个并发的变更
请求会各自基于同一份旧快照做判断，最后互相覆盖写回，导致变更结果丢失或写入脏状态。

**修复**：

- `SubscriptionUsecase` 新增可选 `TxRunner`（`SetTxRunner`）；当接线后，
  `ChangeSubscription` 在 `RunInTx` 内执行完整的读-校验-改-写，使用
  `GetByIDInTx`（`SELECT ... FOR UPDATE`）+ `UpdateSubscriptionFieldsInTx`，
  使生产环境的并发变更串行化而不是互相覆盖。
- 内存 / 测试模式下回退到无锁路径，不依赖真实事务。
- tx 作用域上下文（`txCtx`）被全程透传；`subscription_name` 字段仅在请求实际改变
  它时才写入（窄字段写，domain-H1）。
- `admin-api` 接线 `NewTxRunner(repo)`，生产环境的订阅变更从此串行。

**影响服务**：`admin-api`、`billing-service`。

**已知边界**（记录于 `docs/design/m6-usage-reset-and-concurrency-decision.md`）：

- 并发双重扣费未在本版处理（admin 钱包扣款是行锁事务前的独立 RPC，需要
  请求级幂等）。
- MySQL 对"相同值更新"返回 `RowsAffected==0`，可能被误报为 not-found。
- SQLite 出于设计跳过 `FOR UPDATE`。

### 3. 订阅流量渠道用量统计去噪

**根因**：订阅来源（`SourceKind == "subscription"`）流量的 `ChannelID` 是从订阅
账号 ID 派生的合成值，`channel-service` 查不到对应渠道，导致每次 commit quota
都刷一条 `failed to record channel usage ... channel not found` 告警。该流量本身
已有独立的 `subscription-account` / `session-window` 计费路径，不应再走 channel
维度统计。

**修复**：

- 新增 `recordChannelUsageFromDetail` helper：对 subscription 来源直接跳过并记录
  `skipped_channel_stats` 指标；channel 来源保持原逻辑。替换 async / sync 两处
  调用点。model 用量统计对所有来源不变。

**影响服务**：`relay-gateway`。

## 兼容性说明

- **API**：无破坏性变更。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.15.1

# cross-build 并部署受影响的三个服务
./scripts/deploy-update.sh relay-gateway admin-api billing-service
```

## 验证

- 同组内切换套餐：日 / 周 / 月用量保持不变，不被清零。
- 跨组切换：用量窗口按预期重置。
- 并发 `ChangeSubscription`：变更串行执行，不再互相覆盖写回。
- tx 错误向上透出，不产生部分写。
- 订阅来源流量不再刷 `failed to record channel usage ... channel not found` 告警。
- `go build ./...`、`go vet`、69 个包单元测试通过。

## 完整变更日志

- fix(subscription): M6 - reset usage only on group change + row-locked ChangeSubscription
- fix(relay-gateway): 跳过订阅流量的渠道用量统计
