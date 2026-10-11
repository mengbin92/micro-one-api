# 订阅账号治理 Runbook（阶段 1）

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 1：订阅账号治理。
> 实现 PR：`feat/subscription-account-ops-automation`。

channel-service 进程内运行三个订阅账号治理后台任务，全部默认关闭，通过环境变量按需开启。它们复用现有 channel-service 的 `ChannelRepo`（数据库直连）和 notify-worker 的 gRPC 通道（告警投递），不引入新的存储或投递链路。

## 管理员启用／停用报 RESOURCE_WRITE_PRECONDITION

若错误包含 `revision context expected=0 actual=0`，检查账号的 `credential_revision`。迁移 108 将老账号的初始值设为 0，而 IAM 管理写入要求正数版本；新建账号已经从 1 开始。

执行迁移 `124_backfill_subscription_account_revision`（MySQL：`make migrate`，SQLite：`make migrate-sqlite`，PostgreSQL：`make migrate-postgres`；沿用部署的 DSN 和迁移流程）。该迁移只把零版本补为 1，已有正数版本不变。刷新管理员账号列表后，再填写原因并启用／停用；请求应携带列表返回的 `expected_revision`，状态变更成功后版本加 1。保留旧版本的请求应报版本冲突。

迁移会使正在使用零版本的凭据刷新或编辑请求失效，应重新读取账号后操作；不要降低原因或版本校验来绕过错误。

同批修复还包括：配置历史零版本由迁移 `125_backfill_config_revision` 补为 1，解除管理员删除配置的冲突；订阅账号批量用量重置和额度模板逐项传递 `expected_revisions`；订阅额度策略列表返回 `revision`，策略／套餐编辑保留编辑时版本；模型别名删除传递 `expected_model_revision`，写入优先使用当前页面／详情缓存；模型路由列表保留 `revision`；渠道操作从渠道列表读取版本，排除健康缓存，编辑保留打开时的版本并按更新接口要求提交数字。这些接口仍拒绝缺失或过期版本。部署时应用 124、125，更新 admin-api 和前端文件，并刷新管理员页面。

## 扫描分片与副本配置

额度重置和账号恢复共用以下配置，默认保持一个扫描 worker：

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_ACCOUNT_OPS_SHARD_COUNT` | `1` | 分片总数，必须大于零 |
| `SUBSCRIPTION_ACCOUNT_OPS_SHARD_INDEX` | `0`（单分片可留空） | 当前 worker 的分片编号，范围 `[0, COUNT)`；多分片必须显式填写 |

按订阅账号内部数字主键 `subscription_accounts.id % COUNT = INDEX` 分配账号。分片过滤在 SQL 内完成，然后按 `id ASC` / `id > last_id` 游标分页；其他分片的账号和额度快照不会被加载。恢复只读取 enabled 账号，重置只读取 fixed 策略账号。分页不依赖总数或 OFFSET，已扫描账号被禁用／删除不会导致后续账号被跳过。超时或单账号重置失败时，进程保存已尝试账号的游标，下个 interval 从后续账号继续；完成全轮后回到起点，失败账号在下一轮重试。进程重启从起点重新扫描，写入安全由幂等／CAS 保证。同一个 sweeper 的并发调用返回 in-progress，避免同时修改游标或重复探测。

例如三个扫描 worker 都设 `COUNT=3`，分别设 `INDEX=0/1/2`。每个分片应有且仅有一个启用扫描的 worker；未参与扫描的服务副本关闭两个 `*_ENABLED` 开关。分片编号由部署配置分配，当前不自动发现副本、抢占分片或接管故障 worker。相同编号会重复扫描／探测，缺失编号会留下无人扫描的账号。非法配置会记录 ERROR 并停用这两个 sweeper，channel-service 其余功能继续运行。

扩缩容时先停止旧扫描 worker，等待正在执行的扫描结束，再统一更新 COUNT 并启动覆盖全部编号的新 worker。不要在同一批运行 worker 中混用分片总数。暂停期间额度入账仍沿用现有窗口逻辑；恢复扫描在重启后的下一个 interval 继续执行。短暂重叠时，数据库事务内的重置去重和恢复 CAS 保护写入，但不会消除重复扫描或上游探测。

MySQL、SQLite、PostgreSQL 的 Compose 配置均透传这两项和两个 sweeper 的开关／间隔／超时。多 worker 必须分别配置 INDEX，不能给所有副本复用同一个编号。

### 两分片部署（一个业务服务 + 一个轻量 worker）

Compose 的可选 `account-ops-shards` profile 提供 `channel-account-ops-worker`，继承主服务镜像与私有连接配置，默认不启动。它设置 `SUBSCRIPTION_ACCOUNT_OPS_WORKER_ONLY=true`，仅运行重置／恢复任务和 `/healthz`、`/metrics`，不注册服务、不暴露业务 HTTP/gRPC、不参与 Redis 业务事件消费、不启动模型同步、outbox 或额外额度告警。恢复前探测仍保留。worker 限制为 0.25 CPU / 256 MiB，Go 内存软限制 192 MiB。

先部署新镜像，停掉旧扫描任务，再将两个 `*_ENABLED` 开关设为 `true`、`SUBSCRIPTION_ACCOUNT_OPS_SHARD_COUNT=2`、主服务 `SUBSCRIPTION_ACCOUNT_OPS_SHARD_INDEX=0`。worker 的编号由 `SUBSCRIPTION_ACCOUNT_OPS_WORKER_SHARD_INDEX=1` 指定。确认两者 COUNT 相同、编号完整后执行：

```bash
docker compose --profile account-ops-shards up -d --no-deps --no-build --pull never channel-service channel-account-ops-worker
```

启用 worker 时，在 Prometheus 的应用 static_configs 中加入 `channel-account-ops-worker:8002`，用 promtool 校验后发送 HUP 重载，并确认 target 为 up；停止 worker 时移除该 target，避免持续 down 告警。不要把默认未启用的 worker 加入所有部署的固定抓取列表。

后续换镜像要同时重建这两个容器；worker 通过 extends 继承主服务的固定 image。worker 的分片配置非法或未开启任何 sweeper 时直接退出，避免空运行却显示健康。检查两个容器启动日志中的 shard_count / shard_index，并在每个 `/metrics` 验证扫描计数／耗时增长。还原为单分片时先停止 worker，再设 COUNT=1、INDEX=0 并重建主服务，避免漏扫。

### 生产验证记录

2026-10-08 已上线两个分片：`channel-service` 为 0/2，`channel-account-ops-worker` 为 1/2。两者使用同一个 linux/amd64 镜像，间隔 5m、单次超时 30s，Prometheus 两个 target 均为 up。已观察到每个分片各两轮重置／恢复扫描，错误计数为零；容器源码标签校正后重新核对健康、镜像及摘要。镜像、时间戳、计数与回滚备份见 [上线验证记录](evidence/subscription-account-ops-shards-2026-10-08.json)。

## 1. quota reset 自动化（fixed 策略）

### 前置条件
- 账号 `quota_reset_strategy = 'fixed'` 且 `quota_timezone` 为合法 IANA 时区（非法回退 UTC）。
- 迁移 `057_create_subscription_account_quota_reset_runs.sql` 已执行。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_QUOTA_RESET_ENABLED` | `false` | 开启 fixed reset 扫描 |
| `SUBSCRIPTION_QUOTA_RESET_INTERVAL` | `5m` | 扫描间隔 |
| `SUBSCRIPTION_QUOTA_RESET_TIMEOUT` | `30s` | 单次扫描超时 |

### 行为
- 每 interval 扫描本分片的 fixed 策略账号，当 `quota_daily_window_start` / `quota_weekly_window_start` 落后于当前自然日/自然周起点时，将对应 `used` 设为零、`window_start` 设为目标自然日／周边界；不按旧窗口累加周期。
- 幂等：在同一个事务内锁定账号、写 `subscription_account_quota_reset_runs`（唯一键 `account_id+scope+window_start`）、重新核对存储窗口并重置。重复 tick / 多副本返回 duplicate；已经进入目标或更新窗口时不再清零，因此旧扫描结果不会擦掉新窗口用量。写入失败时重置记录一并回滚，可重试。锁内同时复核当前 fixed 策略、时区和目标边界；扫描后配置改变的旧任务返回 stale，既不清零也不占用重置去重键。
- rolling 策略账号不被扫描。
- 非法时区回退 UTC，可在 reset_runs 表的 `timezone` 列定位。

### 指标
- `micro_one_api_subscription_account_quota_resets_total{scope,result}`：success / duplicate / stale / error
- `micro_one_api_subscription_account_quota_reset_scan_duration_seconds`

### 验证
```bash
# 查看最近 reset 记录
SELECT subscription_account_id, scope, window_start, timezone, reset_at
FROM subscription_account_quota_reset_runs
ORDER BY reset_at DESC LIMIT 10;
```

## 2. 异常账号自动恢复

### 前置条件
- 账号被 `SetTempUnschedulable`（上游 429/5xx/529）或 `AutoPauseAccount`（授权异常/codex 耗尽）标记。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED` | `false` | 开启恢复扫描 |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_INTERVAL` | `5m` | 扫描间隔 |
| `SUBSCRIPTION_ACCOUNT_RECOVERY_TIMEOUT` | `30s` | 单次扫描超时 |

### 恢复分层
| 策略 | 触发条件 | 自动恢复？ |
| --- | --- | --- |
| `auto` | 上游 429/5xx/529，TTL 到期 | 是 |
| `manual` | 上游 401/403 或 AutoPause | 否，需 OAuth 重绑或人工启用 |
| `quota` | 本地额度耗尽 | 等窗口 reset 后自动恢复 |
| `codex` | codex snapshot 耗尽 | 等 snapshot reset 后自动恢复 |
| `rolling` | 无明确标记 | 按 TTL 自动恢复（向后兼容） |

恢复时清理 `rate_limited_until` + `last_error` + recovery metadata（`recovery_policy`/`unschedulable_reason`/`unschedulable_since`/`unschedulable_until`/`expected_recovery_at`）。

每次 `SetTempUnschedulable` / `AutoPauseAccount` / 非空 `SetSubscriptionAccountError` 在现有 metadata 内生成新的 `recovery_revision`（UUID），同一秒内相同错误也代表不同版本，无需数据库迁移。扫描在探测前捕获 metadata、状态、TTL 和凭据版本；写入时锁定并重读账号和额度快照，比较捕获状态，并重新确认 enabled、TTL 已过、本地额度未耗尽以及当前上游额度快照未耗尽。未知策略、非法 JSON 或非字符串策略不授予自动恢复权限；普通错误记录也持久化明确策略，401/403 的 manual 分类优先于 codex/quota 关键词，后续瞬时错误不会覆盖 manual 策略及其原始阻断原因；必须通过显式人工操作或重新授权解除。状态不同或仍被阻断时返回 no-op，留给下一轮扫描；不会清掉新封禁或重新启用已经禁用的账号；瞬时 AutoPause 也保留管理员禁用状态。旧数据没有 revision 时仍比较完整捕获状态，新封禁会自动带上 revision。滚动升级期间应先停止旧版本恢复 worker，待所有扫描 worker 更新后再启用，避免旧实现绕过 CAS。

清标记与恢复账号能力在同一个事务提交，重复执行旧版本会成为 no-op；保留其他 metadata 键。告警和其他针对 metadata 的局部写入也在账号锁内读取与更新，避免覆盖新封禁的版本。管理整行更新也携带读取时的恢复基线，在事务内核对；若期间出现新封禁或恢复，更新拒绝并提示重新加载，避免旧 metadata 恢复旧版本。因为 CAS 比较完整 metadata，扫描后出现告警等无关 metadata 更新也会保守跳过一次，下一轮重新评估。账号快照写入和恢复统一按“账号 → 快照”加锁，避免锁顺序倒置。单次恢复扫描超时覆盖 SQL、探测和清理，取消的探测不会触发恢复。

### 指标
- `micro_one_api_subscription_account_recoveries_total{class,result}`：ttl_elapsed / window_reset / already_schedulable / waiting / skipped / stale（写入时状态变化或不再满足恢复条件）/ error；探测另有 probe_confirmed / probe_negative / probe_unavailable
- `micro_one_api_subscription_account_recovery_scan_duration_seconds`

### 管理后台
`SubscriptionAccountSummary` 新增 `unschedulable_reason` / `recovery_policy` / `expected_recovery_at` / `unschedulable_since`，管理后台可直接展示“为什么不可调度”和“预计恢复时间”。

## 3. 额度事件告警

### 前置条件
- `NOTIFY_GRPC_ENDPOINT` 指向 notify-worker。
- `CHANNEL_HEALTH_ALERT_ENABLED=true` 且 notify 通道已配置。

### 必填配置
| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SUBSCRIPTION_QUOTA_ALERT_ENABLED` | `false` | 开启额度告警评估 |
| `SUBSCRIPTION_QUOTA_ALERT_INTERVAL` | `10m` | 评估间隔 |
| `CHANNEL_HEALTH_ALERT_NOTIFY_TYPE` | `event` | 告警投递类型 |
| `CHANNEL_HEALTH_ALERT_RECIPIENTS` | `[""]` | 接收人（JSON 数组或逗号分隔） |

### 告警类别
| kind | 条件 |
| --- | --- |
| `exhausted` | 本地额度窗口已耗尽 |
| `near_exhausted` | 上游 snapshot 主窗口使用率 ≥ 80% |
| `writeback_down` | 额度快照回写已暂停 |
| `idle` | 超过 24h 无用量 |

### 去重
- 同一 account + kind 在去重窗口（默认 1h）内只投递一次，通过 metadata `last_quota_alert_kind` / `last_quota_alert_at` 记录。

### 告警分层
管理后台风险告警列表每条带 `category` 字段：
- `channel`：渠道余额/毛利告警
- `billing`：对账差异告警
- `subscription_account`：订阅账号不可调度 / 回写暂停告警

关闭订阅账号告警（`SUBSCRIPTION_QUOTA_ALERT_ENABLED=false`）不影响渠道健康和对账告警。

### 指标
- `micro_one_api_subscription_quota_alerts_total{kind,result}`：sent / deduped / error

## 验证命令

扫描／恢复回归覆盖新封禁覆盖保护、重复执行、额度变更、事务回滚、分片全覆盖和扫描超时：

```bash
go test -race ./app/channel/...
go test -count=1 -run 'TestAccountOpsAcrossDialects|TestQuotaResetConcurrentAcrossDialects' ./app/channel/internal/data
```

第二条默认执行 SQLite；设置 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN` 后也验证对应驱动。只能使用可创建／删除临时 schema 的本地测试数据库，测试工具拒绝非 localhost 地址，不读取部署 DSN。

全项目检查沿用：

```bash
make test-unit
cd web && npm test && npm run lint
git diff --check
```
