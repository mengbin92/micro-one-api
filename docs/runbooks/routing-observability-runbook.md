# 路由与 outbox 观察、告警及 Redis 故障恢复

> v0.30 P1-1；2026-09-23 补第二批 O2/O5 与第三批 Q1 取消终态验收。适用于现有 B–F 路由链路；不改变授权和冻结计价规则。

## 入口与部署

- 指标：各服务 HTTP `/metrics`，由[现有 Prometheus 配置](../../deploy/prometheus/prometheus.yml)每 15 秒采集与计算规则。
- 告警：[alerts.yml](../../deploy/prometheus/alerts/alerts.yml) 中 `routing-operations` 组。
- 看板：Grafana **Routing and Outbox Operations**，UID `routing-operations`；[JSON](../../deploy/grafana/dashboards/routing-operations.json)随现有 dashboard 目录自动加载。既有路由选择、账务与依赖看板继续使用。
- 验收：[真实链路入口](../../test/e2e/routing/README.md)，MySQL / SQLite 使用相同规则、阈值与等待时间。

上线涉及 relay、identity、channel，以及嵌入订阅 outbox 的 admin / billing 服务二进制，并同步规则和看板文件。按[部署说明](../../AGENTS.md#deployment)在本机交叉构建镜像；Prometheus 重新加载配置或重启后检查规则状态。第二批已随 v0.32.0 上线，服务状态核对与仍待生产验收的项目见[当前计划第 3 节](../design/next-stage-plan-2026-09-22.md#3-第二批观测与额度解释o)。

仓库 Compose 已加入 Alertmanager 并接到 notify-worker；生产 firing/resolved 到外部接收端的闭环仍待验收。**firing、通知入队、外部接收成功是三个不同状态**，只有接收端响应成功并持久化 `sent` 才算完成一次通知投递。

### 通知链路

`Prometheus rules → Alertmanager → notify-worker /v1/alerts/alertmanager → notifications → Dispatcher → NOTIFY_WEBHOOK_URL`。

- [Alertmanager 配置](../../deploy/alertmanager/alertmanager.yml)按 alertname/service/severity 分组，默认等待 30s、组间隔 5m、重复 4h，包含 resolved 通知；只在 backend 网络开放，不映射公网端口。
- notify-worker 先持久化再返回 202。复用既有 webhook sender、重试和通知状态，外部接收端需接受既有 `{subject, content, ...}` JSON；content 包含告警 labels/annotations/firing/resolved。
- 未配置 `NOTIFY_WEBHOOK_URL` 时通知记为 failed，`last_error` 明确 not configured；`notification_delivery_total{result="not_configured"}` 计数。配置接收端后对失败记录执行既有重试操作，不能把历史 queued 改称 sent。
- 新看板 **Delivery, Credentials and Probe Usage**（UID `operations-delivery`）展示真实 sender 尝试、凭证补写年龄、Redis 限流降级、账本重复 claim 与账号探测 tokens/missing usage。通知链路自身不可用时仍可在 Prometheus/Grafana 看到 `NotificationDeliveryFailing`，不要依赖故障中的同一路通知作为唯一判断。
- 本地验证：规则分别验证触发/恢复；`TestAlertmanagerDeliveryAndRecovery` 通过真实 HTTP 测试接收端验证 firing/resolved 内容及 sent/failed 状态。这是分段隔离验收，未声称跑过生产 Prometheus 到真实收件人的整链路。

部署时同步 Compose、Prometheus/Alertmanager 配置、dashboard、notify/relay/billing/channel/admin 二进制和前端。先在目标环境配置合法接收端；按审批后的部署流程重新创建通知及监控服务。验收保存告警指纹、firing/resolved 时间、通知 ID/状态/last_error 和接收端回执，禁止记录 webhook 密钥。

### 请求与 Trace 关联

Playground 请求检查器展示 `X-Request-ID`、兼容 `X-Trace-ID` 和有效时的 `X-OTel-Trace-ID`；收到响应头即记录，HTTP 错误或后续断流不会丢掉身份。接收 W3C traceparent/tracestate，兼容头不强制改写为 OTel ID。

管理员使用 `/api/log/routing-audit?user_id=<id>&root_request_id=<X-Request-ID>` 联查 selection events 和 attempts；也可单独查询 `/api/log/attempts`。路由审计 JSON 和结构化日志携带 `trace_id`、`otel_trace_id`，由 root_request_id 找到 attempt_id、预留和实际来源。ID 不进入 Prometheus 标签。

Relay 仅在配置 `OTEL_EXPORTER_OTLP_ENDPOINT` 时初始化 OTLP/HTTP exporter，默认采样率 1；未配置时兼容 ID 仍可用于日志关联。当前交付包含 Relay HTTP span 与审计关联，不承诺 downstream gRPC/server 或上游供应商都有 span。部署后需在 collector 中核对实际收到的 trace，启动无报错不等于导出成功。

### 流式取消验收

普通渠道 Chat/Messages/Responses 和 orchestrator 流式路径在客户端取消后记录 `canceled`，请求预算耗尽记录 `timeout`。已开始的 SSE 响应仍可能保留 HTTP 200；应以最终路由审计及 `micro_one_api_relay_executor_requests_total` 的 `result` 标签判断执行结果。未收到协议正常终态的断流必须释放预留，不能重放上游或追加 JSON 错误响应。

1. 使用短期、限模型、限额度的测试令牌，保存 `X-Request-ID`；收到首个 SSE 事件后、正常终态前取消请求。
2. 通过 `/api/log/routing-audit` 和 `/api/log/attempts` 按用户及根请求 ID 回读：最终事件 `planned=false`、`result=canceled`，实际来源正确，预留 `released`，取消后无新增尝试。
3. 核对同一预留的实际费用为 0、无消费账本分录，并检查对应 endpoint/execution_path 的取消指标增量。共享生产流量可能影响计数，逐请求结论以审计和预留记录为准。
4. 停用测试令牌，移除临时凭证和隧道；保存脱敏请求 ID、预留 ID、镜像及回滚标签。

受控上游可通过请求上下文取消确认 HTTP/1.1 请求或 HTTP/2 流已中止；HTTP/2 底层连接保持连接可用于复用。供应商内部停止推理需要其逐请求回执，不能从 TCP 连接状态推断。渠道 9 的真实 API 取消及本地受控上游验证见[验收证据](./evidence/next-stage-channel9-cancel-2026-09-23.json)。

### 指标用途盘点

| 信号 | 使用位置 | 含义/限制 |
| --- | --- | --- |
| HTTP/relay executor 请求终态 | Relay Gateway 既有看板 | 请求结果，不根据响应包装猜 token；注册模式做 path 标签 |
| credential pending age / persist failures | 新 operations 看板及 CredentialPersistence* 告警 | 凭证补写失败，恢复存储后观察 pending 清空 |
| routing outbox age/failures | routing-operations 看板及告警 | 事件积压与投递失败，不等于失效消费者完成 |
| account concurrency/RPM fallback | 新 operations 看板及 AccountLimiterRedisDegraded | Redis 故障使全局限额退化为本地；不能认为多副本仍共享硬上限 |
| billing dedupe conflicts | 新 operations 看板 | operation/result 有限枚举；重复不直接代表多扣款，需联查账本 |
| account probe tokens/usage | 新 operations 看板 | 探测独立于用户账单；Codex cached 从 input 拆出，Anthropic cache 桶分别统计；missing 不视为免费，不推算未经定价的美元成本 |
| dependency gRPC count/latency | service-dependencies / routing-operations | 用 count 的 rate 测 V2 回源 QPS、直方图测 P95，先采基线再讨论恢复缓存 |

## 缓存失效与应急边界

事件由 relay composition 接线到 AuthCache/ChannelCache 的 `InvalidateAll`。命名明确表示整片 L1+L2 失效；没有 user/channel 反向索引。L2 无效 JSON/null/空值按比较删除并回源，正常写入和 TTL 是同一 Redis SET，避免半写留下无过期键。

| 路径/故障 | 已验证边界 | 应急操作 |
| --- | --- | --- |
| legacy 鉴权，Redis 持续断开 | 默认 L1 30s 内可能继续命中旧授权，过期后回源并拒绝已撤权 Key；测试将条目时间推进过真实 TTL，不是生产墙钟延迟测量 | 紧急撤权先在权威库生效；必要时暂停入口或按滚动方式清空 relay 进程缓存，确认凭证 pending=0 后再重启 |
| legacy 事件失败但 Redis 仍可读 | L2 auth 5m、channel 10m 是各缓存默认 TTL，最后一次 L2 命中还可能重新填 L1（auth 30s/channel 60s） | 恢复 outbox/Redis 消费并确认积压清零；需要立即失效时同时处理 L1 与 L2，不能只删 Redis 键 |
| V2 鉴权 | `CachedIdentityClient` 每次读取 identity，撤权后的下一请求看到拒绝；Redis 不参与该鉴权缓存 | 不临时关闭 V2 或恢复旧缓存来掩盖依赖故障；检查 authority RPC 和数据库，保持 fail-close |

上述边界是代码契约和隔离证据；在途请求、并发回填、事件重投递及部署网络会影响实际传播时间。生产回源成本与故障延迟仍待测量：记录相同负载下 RPC QPS/P95、撤权时间、最后接受/首次拒绝时间、outbox pending 和 Redis 恢复时间。gRPC resilience 已使用配置策略/default `reject`，不把拒绝上报为 `cache`；本次未开启默认关闭的 breaker，也未恢复 V2 缓存。

manual 筛选在数据库先缩小候选，再在 data 层解析 legacy JSON、过滤后分页，以保证三方言和损坏旧 metadata 行行为一致。候选仍需扫描；大规模账号池的索引化筛选按 D3 测量后推进。

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
