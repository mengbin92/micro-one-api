# Micro-One-API v0.8.2 发布公告

> 2026-07-17 · 上一版：[v0.8.1](./release-v0.8.1.md)（2026-07-17）

v0.8.2 是 v0.8.1 之后的 **PATCH** 版本。本版聚焦 `internal/server/http.go` 的架构重构拆分，以及 Phase 0 可观测性基线的填充。

本版**没有新增业务表迁移**，**没有 API 破坏性变更**，**没有部署配置变更**，**没有新增/删除端点或路由**。拆分属纯内部重构，请求路径与响应格式与 v0.8.1 完全一致，从 v0.8.1 平滑升级即可。

## 亮点

- **`http.go` God Object 拆分**：将 2470 行的 `internal/server/http.go` 按职责拆分为 13 个聚焦文件，主体文件降至 472 行，使各 Handler / Billing / Forwarder 可独立维护与测试。这是架构重构路线图（[ARCHITECTURE_REFACTOR.md](../design/ARCHITECTURE_REFACTOR.md)）Phase 1 的最后一个 P0 项。
- **Phase 0 可观测性基线落地**：填充了 `docs/design/BASELINE.md` 中全部 16 处 TBD 数据（P50/P95/P99 延迟、gRPC 调用延迟、缓存命中率、熔断器状态），原始压测结果归档到 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`，为后续 Phase 1 性能优化提供量化对比基线。

## 变更内容

### Changed

#### `refactor(server): split internal/server/http.go into focused files`（`254caab`）

- `internal/server/http.go` 从 2470 行降至 472 行；剩余内容为 `HTTPServer` 结构体定义与运行时 Setter 配置方法。
- 拆分出的 13 个聚焦文件（均为 `package server`，行为零变更）：
  - `http_forwarder.go`（42 行）—— stream / nonstream raw 转发逻辑（架构重构步骤 2：Forwarder）。
  - `http_billing.go`（220 行）—— 配额 reserve / commit / release 协调与超时降级（步骤 3：BillingCoord）。
  - `http_chat_handler.go`（251 行）—— `/v1/chat/completions` 处理器（步骤 4）。
  - `http_responses_handler.go`（671 行）—— `/v1/responses` 处理器（步骤 4）。
  - `http_raw_handler.go`（140 行）—— One-API 兼容 raw 透传处理器（步骤 4）。
  - `http_status_handler.go`（332 行）—— `/api/status`、`/api/models`、`/api/group`、`/healthz`、`/metrics`（步骤 4）。
  - `http_oneapi_handler.go`（133 行）—— One-API 代理处理器（步骤 4）。
  - `http_unsupported_handler.go`（19 行）—— 不支持端点的统一 501 响应（步骤 4）。
  - `http_response.go`（116 行）—— 响应写入辅助。
  - `http_response_route.go`（53 行）—— 响应路由表管理。
  - `http_usage_log.go`（91 行）—— usage log 输入构造。
  - `http_helpers.go`（123 行）—— 公共辅助函数。
  - `http_config.go`（25 行）—— 配置常量。
- 路由注册（`routes.go`，83 行，`RegisterRoutes`）此前已提取，本次复用（步骤 5：Router / Middleware）。
- `docs/TODO.md` 标记该 P0 任务全部步骤完成。

#### `docs(baseline): fill Phase 0 observability baseline and archive results`（`397e36c`）

- `docs/design/BASELINE.md` 填充 16 处 TBD：测试环境信息、端点 P50/P95/P99 与错误率、4 个 gRPC 服务调用延迟、auth/channel 缓存 L1/L2 命中率、4 个服务熔断器状态与 24h trip 次数。
- 新增 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`，归档 k6 压测原始结果。
- `.gitignore` 增加 benchmark 结果目录忽略规则。
- `docs/TODO.md` 标记 Phase 0 基线填充任务完成。

## 数据库迁移

本版没有新增编号迁移文件，也没有 schema 变更。迁移执行方式与 v0.8.1 一致。

## 破坏性变更

API、数据库 schema 和部署配置均无破坏性变更。升级 v0.8.2 不需要修改环境变量或配置。

## 升级步骤

```bash
git fetch --tags
git checkout v0.8.2

# 检查并替换 deployments/docker-compose/.env 中的生产密钥
cd deployments/docker-compose
docker compose --env-file .env config --quiet

# 旧数据卷升级前先备份；全新环境直接启动
docker compose --env-file .env up -d --build
```

Kubernetes 部署应先运行迁移，再执行 `kubectl apply -k deployments/k8s`，并等待九个 Deployment rollout 完成。完整步骤和 Secret 清单见 [docs/deployment.md](../deployment.md)。

## 验证

发布前已执行：

```bash
# 基线检查：全量单元测试
make test-unit
# 通过（internal/server 及所有包 PASS）

# 架构边界守卫
./scripts/check-architecture.sh
# 通过
```

生产环境验证（relay-gateway，linux/amd64，`internal/server` 拆分后的新镜像）：

```bash
# 健康检查
curl http://localhost:8080/healthz
# {"status":"ok"}

# Kimi-K3 聊天转发（非流式）
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer <key>" \
  -d '{"model":"Kimi-K3","messages":[{"role":"user","content":"..."}]}'
# 200 OK，正常返回 chat.completion 与 usage

# GLM-5.2 聊天转发（流式）
# 正常 SSE 流式返回
```

日志确认新代码路径生效：`caller` 指向拆分后的 `server/http_chat_handler.go`、`server/http_responses_handler.go`、`server/http_billing.go` 等新文件。

## 完整变更日志

- 254caab refactor(server): split internal/server/http.go into focused files
- 397e36c docs(baseline): fill Phase 0 observability baseline and archive results
