# Micro-One-API v0.36.0 发布：LLM 请求计时观测上线（瀑布追踪、账务关联与完整性判定）

> 2026-10-17 · 上一版：[v0.35.2](./release-v0.35.2.md)（2026-10-16）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.0)

v0.36.0 是 v0.35.2 之后的 **MINOR 功能版本**：按 [llm-request-timing-observability-plan](../design/llm-request-timing-observability-plan.md) 落地端到端请求计时观测——Relay 生产端跨 HTTP/SSE/WebSocket/gRPC 记录阶段耗时与业务终态，Log 存储摘要并从 Jaeger 投影瀑布，账单可关联到每次模型调用观测并展示实时结算状态，管理端日志页提供瀑布与失败检索；上线前评审并修复一轮边界问题。相对上一版包含十五个非合并提交，发布文档提交另计。新增迁移 `137`（log）与 `138`（billing），proto 仅新增，运行时影响 `relay-gateway`、`log-service`、`billing-service`、`admin-api` 和宿主机独立前端。

## 功能与修复

### 1. 请求计时生产端（HTTP/SSE/WebSocket/gRPC）

**背景**：线上慢请求与失败请求缺少分阶段耗时证据，定位只能靠日志推断。

**实现**：Relay 在 HTTP、WebSocket 与 gRPC 三条入口统一开启观测，记录解析、路由、上游、首内容、输出终态与处理结束等阶段，区分 success/rejected/failed/cancelled/incomplete 业务终态并标识输出是否完整；WS 路径额外记录握手、首帧等待、连接池获取与上游拨号，多级缓存的 L1 命中作为请求可见阶段写入。异步计费任务携带遥测载体，结算 worker 跨度与请求观测关联。

**影响服务**：`relay-gateway`（新增 `RELAY_REQUEST_TIMING_ENABLED` 开关，默认关闭）、`billing-service`（结算遥测载体）、`channel/config/identity`（初始化 tracing 参与链路）。

### 2. 计时摘要存储、owner 查询与 Jaeger 瀑布投影

**背景**：阶段数据需要可查询、可留存，且不能因为追踪后端丢数据而误判完整。

**实现**：`log-service` 的 `logs` 表新增观测列（`request_timing_json`、`observation_id`、`otel_trace_id`、开始时间与终态，迁移 `137`），观测摘要随日志批量去重写入，另建 `request_trace_attempt_refs` 记录每次重试与预留的关联；新增 `ListRequestTraces` / `GetRequestTrace` owner RPC，按用户与预留分页返回观测摘要，并从 Jaeger v3 投影瀑布：校验里程碑与摘要交叉一致（缺失即 partial 并给出原因）、区分响应/跨度预算截断、豁免 WS 首回合合法早于根跨度的 prelude 阶段、按完成时间而非开始时间判断过期。

**影响服务**：`log-service`；Jaeger 与 OTLP 端点为新增运行依赖（仅观测路径，失败不影响转发）。

### 3. 账单-观测关联与实时结算状态

**背景**：客服与对账需要从一条账单直达产生它的模型调用证据。

**实现**：`billing-service` 新增 `ResolveLedgerRequest`（账单 → 用户/预留解析）与 `GetSettlementStatus`（基于 outbox 任务与预留状态的实时结算水位），结算任务新增遥测信封列（`observation_id`、`trace_parent`、`trace_state` 等，迁移 `138`，不参与财务摘要）；`admin-api` 聚合两者并转发 owner 分页游标，新增 `GET /api/log/{ledger}/trace` 与 `GET /api/log/request-traces/{observation}`。四个 RPC 均已登记 serviceidentity，仅 admin 用户调用方。

**影响服务**：`billing-service`（迁移 `138` 结算遥测列）、`admin-api`。

### 4. 管理端瀑布与失败请求检索

**背景**：观测数据需要可读的前端呈现，且不能凭空推算未采集的阶段。

**实现**：日志页新增失败请求检索与请求追踪面板：瀑布按真实阶段渲染父子跨度（流水线重叠不相加）、区分未观测/实测零值/结算快照，账单行可分页选择关联观测（`page_token` 游标），WS 回合按输出结束标记结束后处理，取消与未完整输出如实展示。

**影响服务**：宿主机独立前端（`/opt/web/dist`）。

### 5. 面板、告警与灰度配置

**实现**：Grafana relay-gateway 面板新增计时导出、分阶段 P95、OTel 导出/队列丢弃与非法异步遥测载体四组图表；Prometheus 新增 request-timing 告警组，覆盖摘要导出丢弃、OTel 队列丢弃、OTLP 导出失败与非法载体，让观测静默丢失可见。docker-compose 接入 OTLP 端点、`RELAY_REQUEST_TIMING_ENABLED`（默认关）、`LOG_GRPC_ENDPOINT` 与 `JAEGER_QUERY_ENDPOINT`。

**影响服务**：部署配置；不改动运行时默认行为。

### 6. 上线评审修复轮

**根因**：七次功能提交合并后按方案做模块评审，发现若干边界缺陷。

**修复**：

- Relay：stream 请求被上游 JSON 拒绝时协议误标 `sse`（现按实际响应 Content-Type 判定）；failed/incomplete/cancelled 终态塌缩为单一失败标志（现保留终态 outcome）；WS 故障转移重放首帧重复开启观测、后续回合沿用首轮业务根（现复用挂起回合并携带重试 attempt）；L1 缓存属性挂到父跨度（现挂到 `cache.l1` 阶段跨度）。
- Log：Jaeger 缺少摘要证明已发生的里程碑时误判完整（现交叉校验并标记 `missing_*`）；截断首页无根跨度误判 `trace_not_found`（现返回 partial 及真实预算原因）。
- Billing/Admin：`/api/log/{id}/trace` 丢弃 `page_token` 且每页重算时间上界导致游标校验失败（现透传游标由 owner 恢复快照）；`invalid` outbox 任务误报 `reserved`（现映射 `failed`，已落账/已释放优先）；结算 worker panic 时跨度误标成功（现恢复后以失败结束）。
- Web：取消/未完整输出与成功无法区分；多次观测选中后无法返回列表；候选读取失败死胡同；观测列表不分页。
- 工程：gosec G704 对 `timedHTTPDo` 两处已验证 `client.Do` 的误报按既有惯例加 `#nosec` 注释；RBAC entry-matrix 六个源摘要随修复刷新（授权语义未变）。

**影响服务**：`relay-gateway`、`log-service`、`billing-service`、`admin-api` 与前端；每项修复均带回归测试。

## 兼容性说明

- 新增迁移 `137_add_request_timing_summaries`（log 库：`logs` 观测列与 `request_trace_attempt_refs` 表）与 `138_add_settlement_telemetry`（billing 库：结算任务遥测信封列），均为新增表/列，不修改历史数据；按 `migrations/ownership.yaml` 执行。列缺失期间摘要写入仅记录错误，不回溯补写，建议先迁移再部署或部署后立即迁移。
- proto 仅新增 RPC 与错误原因，无字段变更；旧客户端不受影响。四个新 RPC 仅 admin 用户调用方可达，已在 RBAC entry-matrix 登记。
- `RELAY_REQUEST_TIMING_ENABLED` 默认关闭；开启需要 `OTEL_EXPORTER_OTLP_ENDPOINT`（Jaeger OTLP）及 log-service 的 `JAEGER_QUERY_ENDPOINT`、`LOG_GRPC_ENDPOINT`。观测后端不可用不影响转发与计费。
- 新前端的追踪面板依赖新版 `admin-api`；先部署 Admin 再发布前端，回滚顺序相反。前端经 `/opt/web/dist` 独立挂载，更新镜像不会更新前端。
- 本版不修改价格、账本、授权绑定或灰度开关；RBAC 授权语义未变，仅刷新源摘要。

## 升级步骤

1. 备份全部服务 schema、Compose、运行镜像和前端，记录环境与开关。
2. 应用迁移（静态 linux/amd64 `migrate` 制品，先 `-status` 核对 pending 仅含 137/138）：

   ```bash
   docker run --rm --network docker-compose_backend \
     -v /opt/micro-one-api:/opt/micro-one-api \
     -e MIGRATIONS_DSN="root:${PW}@tcp(mysql:3306)/oneapi_log?charset=utf8mb4&parseTime=True&loc=Local" \
     mysql:8.0 /opt/micro-one-api/migrate -dir /opt/micro-one-api/migrations-current -ownership log
   # billing 同理，DSN 指向 oneapi_billing，-ownership billing
   ```

3. 部署服务：`./scripts/deploy-update.sh relay-gateway log-service billing-service admin-api`。
4. 配置 `.env`：`OTEL_EXPORTER_OTLP_ENDPOINT`、`JAEGER_QUERY_ENDPOINT`、`RELAY_REQUEST_TIMING_ENABLED=true`，必要时 `docker compose up -d --no-deps --force-recreate` 相关服务。
5. `cd web && npm ci && npm run build`，独立发布 `/opt/web/dist`。
6. 回滚：先恢复旧前端，再恢复服务镜像（各服务均有 `rollback-<时间戳>` 标签）；迁移为纯新增可保留，不回滚数据库。

## 验证

- 已于 2026-10-17 部署生产四服务、应用迁移 137/138（12:29 CST 登记）并开启 `RELAY_REQUEST_TIMING_ENABLED=true`，前端同步替换；容器健康，Relay 正常承载真实流量，观测记录持续写入（部署后数百条）。
- CI（Backend、Frontend、Integration、迁移冒烟 mysql/postgres、部署与文档漂移、Security Pipeline）对发布提交全绿；`go test -race` 覆盖 billing/admin 金钱路径；前端 vitest 567/567；RBAC P0 矩阵 876 行复核通过。
- 修复轮每个问题均有「先失败后通过」的回归测试，包括 WS 重试去重、协议判定、完整性判定、分页游标与 panic 跨度。

## 完整变更日志

- `a629ed9f` fix(rbac): refresh entry-matrix digests for the timing fix commits
- `493fd7c2` chore(relay): annotate validated upstream client.Do for gosec G704
- `15a6b275` fix(web): keep the trace waterfall honest at display boundaries
- `f3a8cd4b` fix(billing,admin): paginate ledger traces and report honest settlement status
- `24f7784e` fix(log): judge Jaeger trace completeness against the recorded summary
- `ea392597` fix(relay): record actual protocol and terminal outcome in request timing
- `4941ba7a` fix(web): enforce API envelope contract at call sites
- `48ccd2a0` feat(relay): WS connection and L1 cache timing phases
- `2d902cff` chore(deploy): timing observability dashboards, alerts and rollout config
- `0ffc08e3` feat(web): request trace waterfall and failed-request search in logs page
- `037b95e2` feat(billing,admin): ledger trace association and live settlement status
- `738d07fd` feat(relay): request timing producer across HTTP, WS and gRPC paths
- `5d9bb35b` feat(log): request timing summary storage and owner query APIs
- `6af6ff7c` merge: add reviewed model request timing plan
- `b9eca383` docs(design): review model request timing observability plan
