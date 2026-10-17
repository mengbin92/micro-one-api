# 大模型请求耗时观测实施与上线验收（2026-10-17）

对应[设计方案](../design/llm-request-timing-observability-plan.md)。本记录覆盖本次实现、实测与生产部署；供应商内部推理耗时、浏览器实际接收时刻仍不可观测。

## 制品与实现

- 源码指纹：`6b63e016919f1b18a202a8a1debc4c0df6c5f098010e781f72b646258cd25d8b`（`scripts/rbac-source-digest.py`）。
- 本地交叉构建的七个 `linux/amd64` 服务镜像使用标签 `timing-20261017-061259-6b63e01691`，均带相同源码指纹；生产不执行构建。
- 独立 observation UUID、HTTP/SSE、WS 每轮、Relay gRPC 生命周期、全部尝试与未预占尝试、网络里程碑、内部服务/DB/Redis、异步结算恢复和共享日志批次关联已实现。
- HTTP 写出结束、上游语义终止、业务结果与输出结果分别记录；未知耗时保留 optional，不将角色帧/心跳当首内容。摘要不修改金融 payload、digest 或幂等键。
- log owner 保存摘要及关联索引；admin 提供独立链路查询和账单关联，管理端展示摘要、尝试与安全瀑布。沿用 `log.request.list/read` 与 owner 范围授权；不要求费用/正文权限。未知用户只允许平台范围。
- Jaeger v3 读取受累计 5 秒、2 MiB、2000 spans、8 个明确批次引用约束，只投影安全字段。缺失、延迟、过期、截断和故障分别返回状态，不能凭部分树宣称完整。

## 本地验证

| 验证 | 结果及边界 |
| --- | --- |
| `make all` / Wire / 架构 / 迁移 / RBAC | 通过；生成类型已同步，RBAC 矩阵 874 行 |
| `make test-unit`、`make verify` | 全部通过；verify 使用隔离临时 Git index 纳入本次未提交生成文件，实际用户 index 未改动 |
| 前端 lint、构建、预算与测试 | 86 个测试文件、544 个测试通过 |
| 真实数据库与 race | MySQL 8.4、PostgreSQL 16、SQLite、内存：迁移、摘要往返、事务回滚、引用/去重/保留清理、查询与结算恢复通过 |
| 真实 IAM 传输 | identity → admin → log：只有日志权限、无费用/正文权限仍能查获准链路；跨用户拒绝、未知用户平台限制、producer 无读取能力均通过 |
| Jaeger 2.20 | 真实 v3 查询、OTLP 注入/转换、归属过滤、共享批次、限额/故障/保留期测试通过 |
| 输出与传播 | HTTP 缓冲/流/早退/bodylimit/关闭 SDK、WS 独立轮与终止后写失败、gRPC 坏 protobuf/身份拒绝/编码失败、可信 W3C 与传输根唯一性通过 |
| 异步与降级 | outbox 重启恢复、重放不改原 parent、金融 digest 不变、损坏 carrier、取消/EOF、禁用 evidence、真实 SDK 队列饱和丢弃计数通过 |

上述受控场景不等同于每个真实供应商协议、每种线上流量峰值均已生产实测。

## 性能与容量

受控测试：固定假上游等待 50 ms、并发 8、每种模式 160 请求，实际 OTLP HTTP 接收；另有 8 个预热请求。

| 指标 | 关闭采集 | 开启采集 |
| --- | ---: | ---: |
| 响应 P95 | 53.185542 ms | 53.933708 ms |
| 每请求分配 | 56,906.5 B | 277,417.25 B |
| 每请求 CPU | 592.86875 μs | 822.49375 μs |
| 摘要导出/丢弃 | 0 / 0 | 168 / 0 |

P95 增量 **1.4067%**，低于本次 5% 门槛；1445 spans、257162 B OTLP 数据。分配和 CPU 明显增加，这个短时基线不证明长流/高峰容量。原始结果见[性能数据](evidence/llm-request-timing-perf-2026-10-17.json)。

生产部署前：主机约 3.7 GiB 内存、可用约 1.65 GiB，磁盘约 24 GiB 可用；Jaeger 2.20/Badger 保留 48h、容器限制 512 MiB、当时 RSS 约 36.46 MiB。近 24h 执行流量约 0.00526 RPS（454 次/日）。按每次 50 spans、每 span 1 KiB 的保守模型，48h 原始数据约 45 MiB，尚未计索引/压缩；此值仅用于当前低流量的初始容量判断，公网拒绝请求、峰值和长期增长仍须持续观测。

摘要 exporter：队列 256、worker 2、最多两次尝试、每次 2 秒，退出有限等待；WS 同时跟踪最多 64 轮，超限有计数。OTel 非阻塞批处理。关注：

- `relay_request_timing_export_total{result}`：摘要入队/导出/失败/容量丢弃。
- `otel_trace_queue_dropped_total`、`otel_trace_export_spans_total{result}`：SDK 队列与 OTLP 结果。
- `relay_async_trace_carrier_invalid_total`：损坏异步载体。
- `relay_request_stage_duration_seconds{phase}`：有限阶段标签直方图，重叠阶段不可简单相加。

## 生产部署记录

部署流水标识：`20261017-061259`。原 compose/env、log 与 billing 涉及表的 SQL 备份保存在生产主机 `/opt/micro-one-api/docker-compose/backups/request-timing-20261017-061259`；目录 0700，配置和 SQL 0600。每个旧镜像保留 `docker-compose-<service>:rollback-timing-20261017-061259`。

1. 默认保持 `RELAY_REQUEST_TIMING_ENABLED=false`；先备份，应用 log-owned 137 和 billing-owned 138 增量。
2. 运行账号没有 DDL 权限，远程 root 账号被锁定；迁移通过现有 MySQL 容器的本地 root socket 执行，没有解锁远程 root，也没有授予运行账号 DDL。
3. 新关联表需要独立运行权限：仅向现有 log 账号授予 `request_trace_attempt_refs` 的 SELECT、INSERT、DELETE，没有跨 schema 授权。
4. 先部署兼容 log consumer，专用 Relay 身份写入两条尝试引用（含一条无 reservation），验证重复请求返回同一 log ID，并验证 producer 读链路被拒绝。通过后才继续发布其他服务。
5. 所有 owner 与网关、admin 健康后再开启 producer；前端 dist 单独发布并保留旧目录备份，保留原 bind mount 目录 inode。

**已完成生产上线与验证。** 七个服务 healthz 正常，运行镜像 ID 与本地制品逐个匹配，重启数 0、OOM 0；producer 已开启，线上提供的前端 index SHA-256 与本地构建一致。

- consumer 验收 observation：`425e98d6-0c0e-4380-adca-545daae8b36f`，log ID `171810`；两条引用、一条无预占，幂等通过，producer 读被拒绝。
- 网关无凭据 POST 返回 401，响应 observation：`d8450f8a-3d17-4339-822d-66aabbe7ebc3`，trace：`708a31079463db95ee8d95102741e893`。摘要已持久化为 rejected、unknown user；真实 Jaeger v3 查到 4 spans，包含 `relay.request`、`request.parse`、`usage.log` 和 log IngestLog RPC。
- 检查时网关摘要 queued/exported 各 1，OTLP success spans 556，SDK 队列丢弃 0。该快照证明本次小流量 canary，无高峰或长期无丢失保证。
- 真实 MySQL EXPLAIN：平台/用户时间列表均使用 `idx_log_trace_time` range（当前仅扫描 2 行），reservation 关联使用 `idx_trace_ref_reservation` ref（1 行）。用户专用复合索引已建；优化器在当前极小摘要数据集上选择平台时间索引。
- 详细安全制品、canary 与索引证据见[部署证据](evidence/llm-request-timing-deploy-2026-10-17.json)。生产没有执行真实模型推理或金融写入。

## 回滚

先关闭 `RELAY_REQUEST_TIMING_ENABLED` 并重建网关；需要时恢复旧前端目录的内容。保留兼容 consumer、新增可空列、关联表与已有观测记录，不执行破坏性 down migration。原配置及旧镜像均在上述备份与 rollback 标签中，按服务使用 `docker compose up -d --no-deps --no-build` 恢复，并检查 healthz。回滚后核对摘要丢弃/导出错误指标和既有转发、结算健康。

摘要遵循日志保留策略，详细 spans 只有 48h；历史未采集显示 `not_recorded`。后台结算状态是请求结束时快照，不能凭 span 成功推断最终账务状态。当前上线验收不向真实模型发送计费请求，也不修改用户钱包/账本。


## 09:07 前端响应契约修复

部署后，已登录管理员打开日志 68668 的「调用链」出现「调用链暂不可用或无权读取」。通过当前 Chrome 会话切换基本信息/调用链稳定复现；接口已返回有效 `items/applicable`，页面没有继续请求详情。

根因是三个前端 queryFn 使用 `unwrapApiData` 读取原始 JSON 响应。`handleRequestTrace` 经 `writeServiceResponse` 直接返回 payload，没有 `{success, data}` 包装；前端因此返回 `undefined`，React Query 报 `Query data cannot be undefined`，随后显示通用读取/权限错误。前端原有 MSW fixture 错加了包装，掩盖了实际契约错误。此前服务端 IAM 验收不能替代已登录页面验收。

修复让账单关联、独立列表及详情三个请求直接读取带类型的 Axios `response.data`。不修改 owner 授权策略。测试先按真实原始 JSON 复现三个失败，再修复并验证：调用链与 LogsPage 两文件共 10 测试通过（包含真正的 403 拒绝），lint、TypeScript、生产构建和制品预算通过。

前端独立发布标识 `20261017-090731`，index SHA-256 为 `56ca18c1fdb191c7871ec1f35975b37cab6b52bcc764c01d8e6385bc3e3c5f95`，旧 dist 内容保存在 `/opt/web/dist.bak.timing-ui-20261017-090731`。未重启容器；校验实际提供的 HTML 和入口引用 assets 均可读取。原始全量部署证据中的前端哈希保留为首次发布的历史值。

原会话刷新后重新打开日志 68668，页面显示「链路详情可用」、真实跨服务阶段瀑布、应用输出 8.55 s、首有效输出 7.24 s，原错误消失。后续 UI 验收应使用实际 HTTP 响应合同，并在已登录页面验证账单→摘要→详情的完整请求链，不能只使用自造包装 fixture 或服务端 canary。
