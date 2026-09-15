# 路由与 outbox 观察、告警及 Redis 故障恢复

> v0.30 P1-1 · 2026-09-15。适用于现有 B–F 路由链路；不改变授权、冻结计价和重试规则。

## 入口与部署

- 指标：各服务 HTTP `/metrics`，由[现有 Prometheus 配置](../../deploy/prometheus/prometheus.yml)每 15 秒采集与计算规则。
- 告警：[alerts.yml](../../deploy/prometheus/alerts/alerts.yml) 中 `routing-operations` 组。
- 看板：Grafana **Routing and Outbox Operations**，UID `routing-operations`；[JSON](../../deploy/grafana/dashboards/routing-operations.json)随现有 dashboard 目录自动加载。既有路由选择、账务与依赖看板继续使用。
- 验收：[真实链路入口](../../test/e2e/routing/README.md)，MySQL / SQLite 使用相同规则、阈值与等待时间。

上线涉及 relay、identity、channel，以及嵌入订阅 outbox 的 admin / billing 服务二进制，并同步规则和看板文件。按[部署说明](../../AGENTS.md#deployment)在本机交叉构建镜像；Prometheus 重新加载配置或重启后检查规则状态。本次交付未部署生产、未修改生产开关、未发布版本。

当前部署没有 Alertmanager：**firing 表示 Prometheus UI / Grafana 中的可见告警，不代表已发送短信、邮件或其他外部通知**。配置外部通知属于后续独立部署操作。

## 指标与解释

以下名称均以 `micro_one_api_` 开头。

| 指标后缀 | 标签 | 含义 |
| --- | --- | --- |
| `routing_outbox_pending` | owner | 数据库中该 owner 全部未投递行数，不受单轮 100 条批次限制 |
| `routing_outbox_oldest_pending_age_seconds` | owner | 最老未投递行的年龄，空队列为 0，时钟倒退钳制到 0 |
| `routing_outbox_last_success_timestamp_seconds` | owner | 本 worker 最近一次成功发布并写入 delivered_at 的时间；启动后未成功投递为 0；空轮询不刷新 |
| `routing_outbox_last_scan_timestamp_seconds` | owner | 最近一次成功扫描积压的时间；扫描失败不覆盖旧积压，使用此指标辨别数据是否过时 |
| `routing_outbox_failures_total` | owner, operation | 扫描 `scan`、载入批次 `load`、Redis 发布 `publish`、数据库确认 `acknowledge`、清理 `gc` 的失败次数 |
| `routing_admission_rejected_total` | operation, reason | 准入失败；resolve / ordered / reserve / recheck 按下表定位 |
| `routing_snapshot_validation_failures_total` | operation, reason | `validate/content` 或 `read/decode,digest,subject,subscription,subscription_window,missing_snapshot` |
| `dependency_grpc_latency_seconds` | service, method, status | 复用现有权威 RPC 时延直方图；`_count` 同时提供包含错误状态的调用次数 |
| `dependency_grpc_errors_total` | service, method, error_code | 客户端 interceptor 补齐错误计数；错误码使用 gRPC 枚举，不使用错误文本 |

owner 固定为 `identity`、`channel`、`subscription`。pending / age 是数据库状态的每实例采样；订阅表由多个进程共享，**不能跨副本相加**。看板保留 instance；确需概览时同一数据库用 `max by (owner)`，多个栈还应保留栈标签。最后成功时间只描述本 worker，不是消费者已完成失效的证明；空闲 worker 时间很旧或为 0 不单独报警。

扫描每轮执行，Redis 不可用仍保留队列并重试。未配置 Redis 客户端时继续扫描，存在 pending 才产生 publish 失败。指标失败不将数据库历史状态伪装为零。积压指标不是数据库事务内实时计数，最长可有一个轮询及采集周期的延迟。

### 准入失败定位

| operation | reason | 排查方向 |
| --- | --- | --- |
| resolve / ordered | `relay_capability`、`identity_capability`、`channel_capability` | 三服务版本 / v2 开关；identity 返回完整且启用的事实；channel 实现分组 / probe 能力 |
| resolve / ordered | `entitlements` | 订阅合同开关、授权事实读取依赖；订阅事实目前是进程内读取，不伪装成 RPC |
| resolve / ordered | `group_lookup`、`candidate_probe` | channel 权威读取或候选查询的 RPC 状态和时延 |
| resolve | `policy_denied`、`projection_mismatch` | 分组状态、授权与 token 策略、identity 默认组投影 |
| ordered | `candidate_list`、`bound_group`、`access_denied`、`settlement`、`policy_denied`、`no_candidates` | 有序列表、已绑定会话、授权、结算资格及资源；不会回退全局组 |
| reserve | `billing_capability` | billing 快照版本、fixed 能力、订阅合同能力及能力 RPC 错误 |
| recheck | `admission_changed` | 上游发送前的权威复查；可能同时出现内层 resolve / ordered 失败，不能把不同 operation 的计数相加当作请求数 |

指标只承载有限枚举。准入结构化日志记录 operation / reason、user_id、token_id 与 request_id / trace_id（上下文存在时）；billing 快照读失败日志记录 reservation_id / request_id / user_id。outbox 日志记录 owner / operation / event_id。不要把 Key 原文、用户 ID、请求 ID 加入标签。

## 告警与处置

| 告警 | 条件与持续时间 | 处置 |
| --- | --- | --- |
| `RoutingOutboxDeliveryFailing` | 1 分钟内 publish 失败且 pending > 0，持续 30 秒 | 查 Redis 连通性 / 认证 / Streams 写入；恢复连接后等待自动重试 |
| `RoutingOutboxBacklog` | 最老 pending > 60 秒，持续 1 分钟 | 查 publish / acknowledge 失败、积压增长与数据库吞吐 |
| `RoutingOutboxScanStale` | 最近成功扫描距今 > 30 秒，持续 1 分钟 | 查数据库、迁移与 worker；不要把旧 gauge 当成健康状态 |
| `RoutingOutboxStorageFailing` | 5 分钟内 scan / load / acknowledge / gc 失败，持续 1 分钟 | 查对应 owner 数据库、权限及迁移；成功发布但确认失败可能重复投递，消费者按版本幂等失效 |
| `RoutingCapabilityRejected` | 5 分钟内能力 / entitlements 拒绝，立即 | 查 reason 及服务版本 / 开关，不临时放宽授权规则 |
| `RoutingSnapshotValidationFailed` | 5 分钟内快照校验失败，立即 critical | 保留 reservation / 原始快照和账单证据，排查内容、摘要和主体绑定；不重写冻结快照 |
| `RoutingAuthorityRPCFailing` | 权威 RPC 非 OK > 0.05 次/秒，持续 2 分钟 | 按 instance / service / method / status 查依赖服务 |
| `RoutingAuthorityRPCSlow` | 权威 RPC p95 > 500ms，持续 5 分钟 | 查依赖和数据库；稀疏或无请求窗口可能无数据，不等于零时延 |

worker 存活而扫描失败由 ScanStale 检出；路由相关进程退出或采集失败由 `RoutingMetricsTargetDown`（`up == 0` 持续 1 分钟）检出。该规则匹配仓库默认服务地址，修改采集地址时须同步调整。失败与准入计数在启动时预建零序列，以支持采集基线后的第一起故障；进程启动后第一次采集之前发生的单次事件仍应查结构化日志。

## 可重复的隔离演练

```bash
# 完整服务链路 + 真实 Prometheus；约额外等待两分钟验证积压告警
python3 scripts/test-routing-e2e.py --driver=mysql
python3 scripts/test-routing-e2e.py --driver=sqlite3 --skip-build

# 单独验证规则的触发 / 恢复 / 首次失败 / 空闲与扫描失效
# 在仓库根目录执行；同部署版本，不发布端口
docker run --rm --entrypoint /bin/promtool \
  -v "$PWD/deploy/prometheus/alerts:/rules:ro" -w /rules \
  prom/prometheus:v3.6.0 test rules routing.test.yml
```

脚本先执行规则测试，再启动私有 Prometheus 使用原始生产规则，随后：

1. 通过真实接口建立分组、授权、Key 和账务基线。
2. 停止隔离 Redis；通过接口撤权，确认持久化 pending 和 publish 失败指标。
3. 查询 Prometheus `ALERTS`，等待 identity 的 DeliveryFailing 与 Backlog 均为 firing；请求仍被拒绝。
4. 恢复 Redis，确认数据库积压清空，采集到 pending=0 / age=0 / last_success>0，两个告警解除。撤权仍拒绝；重新授权后请求和结算成功。
5. 关闭 billing 快照能力，确认接口可达但请求被拒绝，`reserve/billing_capability` 指标递增且 CapabilityRejected firing。
6. 清理本项目容器、网络和卷；不接触生产。

演练证据见 [P1-1 验收记录](./p1-acceptance-2026-09-15.md#p1-1-路由与-outbox-可观测性验收)。本次故障演练覆盖 identity 发布路径，三个 owner 的扫描、投递失败和恢复由数据边界测试覆盖；PostgreSQL 不在完整服务 E2E 矩阵中。订阅与多 relay 的重复 / 乱序事件、冻结账单和授权不变式继续由已有验收场景覆盖。
