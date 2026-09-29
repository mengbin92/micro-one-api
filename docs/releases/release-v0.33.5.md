# Micro-One-API v0.33.5 发布：熔断拒绝指标与路由依赖延迟观测

> 2026-09-29 · 上一版：[v0.33.4](./release-v0.33.4.md)（2026-09-28）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.5)

v0.33.5 是 v0.33.4 之后的 **PATCH 观测准确性版本**：Relay 将未发往上游的熔断拒绝独立计数，identity/channel/billing 记录服务端 gRPC 处理延迟，并修正 O5 隔离测试跨服务抓取窗口及历史归因结论。

**无公共 API/proto、数据库迁移、配置项或前端资源变更**。需更新 `relay-gateway`、`identity-service`、`channel-service`、`billing-service`。

## 修复内容

### 1. 熔断拒绝不再被当成上游失败

**根因**：断路器处于 open，或 half-open 并发超过上限时，请求尚未执行，却被计入 `result="failure"` 和 `CircuitBreakerFailures`，掩盖拒绝量并抬高失败指标。

**修复**：两处熔断调用路径均将 `ErrOpenState` / `ErrTooManyRequests` 记录为 `result="rejected"`，不增加上游失败计数。重复运行的指标回归测试清理其专属时序，避免全局计数器污染下一轮测试。拒绝延迟仍无依赖 RPC 样本，不能据此推断其耗时。

**影响服务**：`relay-gateway`。

### 2. 路由依赖服务端 gRPC 延迟可观测

**根因**：服务端处理时间直方图已注册但未接入服务调用链，无法与 Relay 侧依赖 RPC 延迟对照；原 5ms 起始桶无法区分常见的 1–5ms 请求。

**修复**：identity、channel、billing 的 gRPC unary 服务最外层记录方法、状态和处理时间；新增 1ms/2.5ms 桶。指标包括鉴权处理和非 OK 请求，比较时需核对状态及调用方流量。

**影响服务**：`identity-service`、`channel-service`、`billing-service`。

### 3. O5 隔离采样与证据纠错

**根因**：原测试仅等待 Relay 抓取，再向前扩展 15 秒读取其他服务，可能漏掉突发后的样本或混入先前流量；部分查询失败被当成缺失值。历史文档把 repo 调用数当 SQL 查询数，在未测 DB/宿主资源的情况下断言共享 MySQL/VM 根因。

**修复**：等待四个目标各自完成突发前后的成功抓取，按各自边界查询并保存边界；查询错误或关键样本缺失直接使验收失败。历史原始数字保留，撤回尚无证据支持的因果结论。代码检查确认 identity 同步 `GetAuthSnapshot` 路径无 Redis 操作；延迟的具体原因仍待有 DB/宿主观测的复测。

**影响范围**：隔离 E2E 测试与 O5 文档；无新增生产业务行为。

## 兼容性说明

- 无公共 API/proto、数据库 schema、配置、前端资源变更；无需迁移或前端发布。
- 新增/细化 Prometheus 指标序列；现有查询 `micro_one_api_grpc_request_duration_seconds` 的指标名不变，新增低延迟桶。监控面板对 `result="rejected"` 的查询可直接使用现有熔断请求计数器。
- O5 的 2026-09-29 历史服务端/进程窗口不能作为延迟根因验收证据；生产 Redis 故障不主动注入。

## 升级步骤

1. 在本机交叉构建 `linux/amd64` 镜像，依次更新 `identity-service`、`channel-service`、`billing-service`、`relay-gateway`；保存现有镜像的回滚标签，不在生产主机上构建。
2. 在生产 Compose 目录逐个执行 `docker compose up -d --no-deps <service>`；无需数据库迁移和前端更新。
3. 检查四个容器状态、近期日志、健康检查及 Prometheus 抓取；确认服务端 gRPC 延迟指标和 Relay 熔断结果指标可查询。

## 验证

- 聚焦 Go 测试以 `-race -count=2` 通过，覆盖熔断指标、服务端埋点及错位抓取回归；架构与提交信息检查通过。
- Prometheus 3.6 `promtool` 验证成功抓取时间查询返回真实样本时间。
- `python3 scripts/test-routing-e2e.py`（MySQL）与 `--driver sqlite3 --skip-build`（复用同一验收镜像）均八相位通过：Redis 停止期间路由可用、outbox 告警触发，恢复后积压清零且权限生效；两个私有 Compose 项目已清理。
- 部署文档检查通过（41 个 Kubernetes 资源、Compose 配置和 212 个本地链接）。O5 延迟根因仍需 DB/宿主观测另行验证。

## 完整变更日志

- `16f57e2e` fix(metrics): classify open-breaker rejections as rejected, not failure
- `aea564fd` feat(observability): wire server-side gRPC latency on routing dependencies
- `49c9abb9` test(e2e): capture O5 latency within each target's scrape window
- `30b03d1f` docs: qualify O5 attribution and preserve pending acceptance
- docs(release): v0.33.5
