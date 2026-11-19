# 上游 usage 观察留证：实现、配置与回滚

更新：2026-11-17（北京时间）。对应方案：`.codex/upstream-usage-observation-completion-plan-2026-11-16.md`
（本地规划文档，未纳入版本库）。

状态：**M1–M3 已实现，R01–R22 的后续复核又发现跨尝试身份、WS、范围隔离、查询权限与故障恢复边界，已继续修复并补回归；本轮统一验证结果另行留证。
M4 生产发布与 M5 48h 窗口验收尚未执行。**
本文不表示生产已开启观察、尚未进行真实三库/性能/通知收件验证，也不代表任何协议或 cohort
已被验收。未完成项见文末「未完成与未验证」。

## 1. 范围与不变量

- 只做 observation-only 留证：不新增收费、路由、重试、协议回退或结算行为。
- 观察开关独立于 `RELAY_NATIVE_USAGE_V2_ALLOWLIST` 与 `RELAY_EXECUTION_DELIVERY_V1`：
  空财务名单（当前生产状态）下的 HTTP/gRPC/WS 用户请求同样留证。
- 观察事件不携带 `final`、结算指令、可写金额、lease 或财务政策；不放松
  `domain/execution.Event.Validate`。
- 本地落盘失败只标记 degraded/lost 并计数/告警，不改变客户端响应、上游发送次数、
  重试/回退、预扣/commit/release 或账本金额。
- 证据表 `upstream_usage_observations` 不进入普通日志、调用次数、token、额度与费用聚合。

## 2. 链路

```
HTTP / SSE / WS / gRPC
  → internal/biz.WithUsageObservation（共享发送接缝，按 send 建身份）
  → evidence.WithObservationSink → evidence.Do / WS emit
  → Relay 本地观测 outbox（SQLite/WAL，独立文件与预算）
  → Log owner RecordUsageObservation（重算 digest、严格幂等）
  → upstream_usage_observations（Log owner 专用表）
  → ListUsageObservations（摘要，受限 gRPC）/ GetUsageObservation（单条含 payload）
```

关键代码：

| 层 | 文件 | 职责 |
| --- | --- | --- |
| domain | `domain/upstream/evidence/observation.go` | 事件模型、capture/status 分离、毫秒时间、digest、严格校验 |
| domain | `domain/upstream/evidence/http.go` | `Started`/`Checkpoint`/`Terminal`/`NotSent`/`TransportFail`/`Redirect` 接缝；逐次 RoundTrip 记发送 |
| relay biz | `internal/biz/observation_session.go` | 每次实际发送独立 `send_id`、单次终态、无对应发送的响应记 `unmatched`、指标 |
| relay biz | `internal/biz/observation_delivery.go` | 本地先落盘、可取消投递、独立统计 goroutine |
| relay biz | `internal/biz/observation_scope.go` | 观测来源范围（与财务名单无关） |
| relay data | `internal/data/observation_outbox.go` | 0700/0600、WAL、FULL sync、pending 字节预算、独立维护与磁盘余量预检 |
| relay service | `internal/service/observation_sender.go` | Log RPC DTO、conflict 映射 |
| log biz/data | `app/log/internal/{biz,data}/observation.go` | 身份/上限/幂等、专用 PO、完整 allow/deny SQL 作用域、毫秒窗口 |
| log service | `app/log/internal/service/observation.go` | 摘要/单条 DTO（gRPC only，响应字节预算） |

发送身份：`send_id = <observation_id>/<attempt_number>/<uuid>`。重选源、协议回退、重试、
WS 每轮、每个 redirect 跳都会生成新的 `send_id`，因此不会共用终态。事件身份 `event_id =
<send_id>/<category>/<sequence>`；`not_sent` 与 `unmatched` 是请求级事件，按
`<observation_id>/<SendRef 身份摘要>/<category>/<sequence>` 计键，区分同一请求下的不同尝试，**不生成 send_id**。重投复用原字节与 digest。
`observed_at_ms` 全程毫秒精度，查询窗口直接比较，不做秒级 floor/ceil。

## 3. 配置

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `RELAY_USAGE_OBSERVATION_V1` | `false` | 观察 producer 总开关；默认关闭 |
| `RELAY_OBSERVATION_OUTBOX_PATH` | `/var/lib/relay-observation/observations.db` | 独立 outbox 文件（绝对路径，0600） |
| `RELAY_OBSERVATION_SCOPE` | 空（全部来源） | 逐来源灰度：`channel:<id>:<model>`、`subscription:<id>:*`，逗号分隔；与财务名单无关 |
| `RELAY_USAGE_OBSERVATION_EXPECTED` | `false` | 仅用于告警的“本目标应开启”清单，与 producer 开关独立 |
| `RELAY_OBSERVATION_MIN_FREE_BYTES` | `268435456` | 共享磁盘留给财务队列/服务的最小余量；低于该值观测拒绝入队（degraded） |
| `RELAY_OBSERVATION_MAX_PENDING_EVENTS` | `5000` | pending 条数上限（排障/小盘用） |
| `RELAY_OBSERVATION_MAX_PENDING_BYTES` | `67108864` | pending payload 字节上限，按活跃数据而非文件物理大小判定 |

Compose 三种形态（主 / lite / postgres）已增加变量与 `./data/relay-observation` 卷。
逐来源灰度必须使用 `RELAY_OBSERVATION_SCOPE`；未纳入的来源继续正常业务，但不产生新观测事件。
配置格式非法时进程启动失败，不会静默全量开启。

Log 侧权限：`/api.log.v1.LogService/RecordUsageObservation` 仅授予 `relay` system
capability；`ListUsageObservations` 使用 `log.requests.list` 执行点与 `log.request.list` 操作。
`GetUsageObservation` 独立使用 `log.requests.read`，要求 `log.request.read` 与
`log.request.content.read` 的授权范围交集，列表权限不能获取 payload。完整 allow/deny 作用域在分页或详情读取前下推到
SQL（`user_id` 列），请求里的 `user_id` 只能收窄；无法表达的资源/组作用域 fail-closed，坏游标明确拒绝。
`platform/security/serviceidentity` 已登记全部三条精确方法策略。

## 4. 指标与告警

指标（`platform/metrics/observation.go`，均为有限标签）：

| 指标 | 标签 |
| --- | --- |
| `micro_one_api_relay_usage_observation_started_total` | protocol / source_kind / purpose（三个独立有限枚举） |
| `micro_one_api_relay_usage_observation_terminal_total` | protocol / source_kind / purpose / capture / status |
| `micro_one_api_relay_usage_observation_delivery_total` | result = accepted/acked/retried/failed/lost/conflict/rejected |
| `micro_one_api_relay_usage_observation_transport_sends_total` | protocol / expected / observed（独立发送分母） |
| `micro_one_api_relay_usage_observation_transport_last_missing_timestamp_seconds` | protocol（首个 sink 漏挂事件） |
| `micro_one_api_relay_usage_observation_outbox_pending` | — |
| `micro_one_api_relay_usage_observation_outbox_oldest_age_seconds` | — |
| `micro_one_api_relay_usage_observation_outbox_bytes` | — |
| `micro_one_api_relay_usage_observation_last_stats_timestamp_seconds` | 新鲜度 |
| `micro_one_api_relay_usage_observation_last_anomaly_timestamp_seconds` | protocol / source_kind / status（首样本异常） |
| `micro_one_api_relay_usage_observation_last_gap_timestamp_seconds` | kind（首样本缺口） |
| `micro_one_api_relay_usage_observation_expected` / `_enabled` / `_ready` | 0/1（expected 来自独立配置） |
| `micro_one_api_relay_execution_outbox_last_stats_timestamp_seconds` | 财务队列新鲜度 |
| `micro_one_api_relay_execution_outbox_worker_expected` | 1 表示该目标运行财务 worker |

告警在 `deploy/prometheus/alerts/alerts.yml` 的独立组 `usage-observation`：
`UsageObservationAnomaly`、`UsageObservationAnomalyRate`、`UsageObservationCaptureGap`、
`UsageObservationBacklog`、`UsageObservationBacklogCritical`、`UsageObservationUnavailable`、
`UsageObservationTargetUnexpectedlyOff`、`UsageObservationSendSeamDetached`、
`UsageObservationNoData`，以及财务队列的 `ExecutionOutboxBacklog`、
`ExecutionOutboxStatisticsStale`。回归用例：
`deploy/prometheus/alerts/usage-observation.test.yml`（已纳入 CI promtool 列表）。

规则约定：
- 异常/缺口规则同时看事件时间戳与 `increase()`：首样本为 1 的序列也能立刻告警，重启后靠
  counter 分支补位；时间戳为 0 表示“从未发生”，不会误报。
- 渠道 1 的完整 Chat 观测会校验两套 token 字段是否等值，以及缓存命中与未命中是否合计为输入量。
  缓存写入计数保留在原始 usage，账务继续使用独立的原生解析结果。别名冲突继续计入异常。
- 积压、失联、财务新鲜度全部按 `instance` 组合判定，`max()` 不再让健康实例掩盖坏实例。
- `expected=1` 但 `enabled!=1` 才告警“意外关闭”；合法默认关闭（`expected=0`）不误报。
- `transport_sends_total{expected="true",observed="false"}` 及对应事件时间戳是 sink 漏挂的独立证据，
  在实际 HTTP RoundTrip / WS create 发送接缝统计，不依赖 outbox 自身的 started/terminal；首个非零样本也能告警。
- 规则按 job/instance 对齐；缺少 expected 元数据、预期开启却关闭、关闭采集但仍需排空的 worker 统计失联分别检查。
- 统计失败保留旧值并由新鲜度告警暴露；`capture=limited/unknown/unobserved` 与
  `lost/conflict/rejected` 立即判定缺口，不等比率阈值。
- 已持久化事件的有限告警元数据与事件同事务保存在本地 outbox；ACK 清空 payload 但保留这几项元数据。
  重启后 Stats 恢复近 15 分钟的 usage 异常/缺口时间戳，旧快照不会覆盖更新的现场信号。
  本地写入本身失败后进程立即退出，仍不能保证该次内存信号曾被 scrape；不将此边界解释为零缺口。

## 5. 容量与保留

- 本地 outbox：最多 5000 条 pending、64 MiB **活跃 payload**（不按文件/WAL 物理大小判定），
  DB 上限 96 MiB；ACK 墓碑保留 7 天，pending 永不按保留期删除。
- 活跃 payload 按 UTF-8 字节计量。96 MiB 是包含幂等墓碑的独立硬边界，达到页数上限明确返回容量拒绝，
  不提前删除七天内的墓碑来掩盖耗尽；开窗前须按真实流量估算 pending 峰值与七天元数据空间，必要时缩小观察 scope。
- 入队前先做一次有界、独立提交的维护（过期墓碑清理 + WAL checkpoint），因此“满载 →
  全部 ACK → 无人工删库/重启 → 再入队”是受支持的恢复路径。
- 维护遵循调用 context；容量/磁盘余量配置非法、负值或溢出会在初始化时明确失败，不静默采用默认值。
- 磁盘余量预检：共享盘可用空间低于 `RELAY_OBSERVATION_MIN_FREE_BYTES` 时观测拒绝入队，
  为财务完成、WAL/checkpoint 与服务运行保留余量；这只是预检，不是跨进程原子预留。
- 远端证据表：Log 保留任务按 7 天删除（`received_at_ms`），与普通日志 30 天策略分离。
- 证据清理独立执行；普通日志清理失败或关闭不会阻止证据按其保留期清理。
- 坏行在完整校验原始 SHA、规范摘要及行/事件身份后隔离，保留原字节供受控调查，不阻断同批正常事件；
  隔离产生新的 rejected 缺口，不刷新旧 usage 异常的发生时间。隔离记录同样按七天保留期清理。
- 两个队列（财务 / 观测）各有独立预算；观察满额只影响自身入队，不影响财务队列与业务响应。

## 6. 开启步骤（M4，需生产变更确认后执行）

Relay 镜像以 **65534:65534** 运行。三种 Compose 均使用相对 bind mount；首次启动或重建
Relay 前，必须在**实际 Compose 文件所在目录**以有权设置属主的部署身份执行：

```bash
install -d -m 0700 -o 65534 -g 65534 data/relay-outbox
install -d -m 0700 -o 65534 -g 65534 data/relay-observation
```

| 启动形态 | 使用的 Compose 文件 | 启动前操作 |
| --- | --- | --- |
| 主栈 | `docker-compose.yml` | 在该文件所在目录执行上面两条命令，再启动/重建 `relay-gateway` |
| Lite | `docker-compose.lite.yml` | 同上，不依赖 Docker 自动创建目录 |
| PostgreSQL | `docker-compose.postgres.yml` | 同上，不依赖 Docker 自动创建目录 |

`scripts/deploy-update.sh relay-gateway` 已在构建/重建前准备这两个目录；
`scripts/deploy.sh` 的服务重启步骤也执行同一操作。手工 `docker compose -f ... up` 必须先做
相同准备。不能改容器运行用户、放宽目录权限，或让 Docker 自动创建 root 属主的新目录；
准备失败应停止发布。该命令只调整目录自身，不递归改写已有数据库文件；若已有文件来自不同
运行身份，先核查属主与权限，不能通过删库解决。

1. 先部署 Log consumer（迁移 152 + 权限 + gRPC 方法），确认 `RecordUsageObservation`
   在 relay 身份下可用。
2. 再部署 Relay 镜像；保持 `RELAY_USAGE_OBSERVATION_V1=false`，先验证进程健康与
   指标 `..._enabled=0`；同时把 `RELAY_USAGE_OBSERVATION_EXPECTED=true` 写入该目标配置，
   使“应开启但没开”可告警。
3. 用 `RELAY_OBSERVATION_SCOPE` 固定一组来源开烟测：验证 `started/terminal`、
   `delivery_total{result="acked"}`、`outbox_pending=0`、Log 表行数与受限查询；
   未纳入来源应保持正常业务且**不产生**新事件。
4. 逐来源扩面（追加 scope 条目），最后才改为空 scope 全量；每一步都核对
   `transport_sends_total{expected="true",observed="false"}` 为 0。
5. 任一步失败：先移除 scope 条目缩小范围，再回滚镜像；不要靠删除 outbox 或清空证据表解决。

回滚：先关闭 `RELAY_USAGE_OBSERVATION_V1`（producer），**保留 consumer/worker**——进程仍会打开
保留的 DB 并排空已持久事件（`enabled=0` 只表示不再新增）；不删除 pending、不降级 schema、
不删 ACK 墓碑。规则回滚只移除 `usage-observation` 组。

## 7. 测试（本地已执行）

```
python3 scripts/test-deploy-update.py
bash -n scripts/deploy-update.sh scripts/deploy.sh
go test ./domain/upstream/evidence/ ./internal/biz/ ./internal/data/ ./internal/service/ \
        ./internal/server/ ./app/log/... ./platform/metrics/ ./platform/security/serviceidentity/ \
        ./cmd/relay-gateway/
go run ./cmd/migrate-check
docker run --rm --entrypoint /bin/promtool -v "$PWD/deploy/prometheus/alerts:/rules:ro" \
  -w /rules prom/prometheus:v3.6.0 test rules usage-observation.test.yml
```

2026-11-17 的部署脚本回归共 12 项通过，包含两个私有目录的准备顺序及目录失败时停止发布。
另以本机已缓存 Alpine 3.22、无网络、只读根文件系统和临时内存目录验证：两个目录均为
65534:65534、0700；uid 65534 可以创建 0600 文件。该检查不使用生产目录，也不替代 M4 现场验证。

覆盖（对应 code review 编号）：

- R01 指标标签三枚举独立 + 真实 producer 的 registry 合同；
- R02 完整 SSE + 非法 usage = `complete/invalid`，财务判定不变；
- R03/R04 Orchestrator 真实生命周期注入 delivery、计时关闭仍保留已验证 user_id；
- R05/R06 WS 每轮独立身份、首写失败与上游零帧断开都有终态；
- R07 `unmatched`/`not_sent` 不伪造 send；
- R08 redirect 每跳独立 send（上游真实接收次数核对）；
- R09 满载 → 全部 ACK → 再入队成功；R22 磁盘余量预检；
- R10 producer 关闭后保留库仍排空；
- R11 两个投递循环的统计 goroutine 在阻塞发送下持续刷新、失败不置零；
- R12 首样本 counter=1 仍告警、15m 窗口后仍靠时间戳保持；
- R13 期望开启但关闭的目标告警、合法关闭不误报；sink 漏挂的独立分母告警；
- R14 财务新鲜度按实例且仅对运行中 worker 判定；
- R15/R16 已登记执行点、self/users/all+deny/resource/过期作用域；
- R17 列表只返回摘要、单条取 payload；R18 毫秒窗口精确；
- R19 内存接收端拒绝 ACK；R20 scope 内外来源行为；R21 运行时内存接收端已移除。

## 8. 未完成与未验证

以下项目**尚未**完成，不能据本文宣称验收通过：

- 真实 MySQL/PostgreSQL 环境的迁移与唯一键冲突测试（本地只有 SQLite）。
- linux/amd64 目标架构的 P95/P99、写盘耗时、CPU/RSS、WAL 增长与慢盘/接收端不可用测量。
- 生产告警通知的实际收件/ACK 证据（隔离环境故障注入）。
- 48h 完整窗口、逐 send 对账、cohort 最低 100 样本、财务开关对照。
- 生产发布、镜像交叉构建、规则加载与回滚演练。
- HTTP 自动重发（同一 `http.Client.Do` 内由 Go 触发的隐式重发）仍按一次 send 记账；
  redirect 已逐跳拆分，但“TCP 重传不新增业务 send”的口径未在真实网络故障下核对。
- canary / internal_test purpose 目前没有 relay 生产者；health_probe 依据
  `requesttrace.HealthProbeID` 判定。
- 监控探针路径（`internal/data/upstream_probe.go`）尚未挂载 observation sink，
  探针发送暂不计入观察覆盖。
- 告警恢复已覆盖“事件持久化并 ACK 后、首次 scrape 前重启”的场景；不可写本地盘且立即崩溃的信号不在该保证内。
