# 项目 TODO

> 最后更新：2026-07-17
>
> 当前阶段重点：v0.8.0 已发布（API 指南页、CC Switch 导入、admin 前端改由 ADMIN_WEB_ROOT 提供）。v0.8.0 前的发布、部署、文档和路线图清理任务已全部完成；Phase 0 可观测性基线已填充，原始结果归档在 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`；下一步推进架构重构 Phase 1 的剩余 P0 项（`internal/server/http.go` 拆分）。
>
> 架构重构总方案见 [design/ARCHITECTURE_REFACTOR.md](./design/ARCHITECTURE_REFACTOR.md)，性能基线表见 [design/BASELINE.md](./design/BASELINE.md)。

## P0 — 架构重构 Phase 1 剩余项

> 依据 `docs/design/ARCHITECTURE_REFACTOR.md` §10.2。Phase 1 的其余 P0 项（gRPC 熔断器、本地缓存层 L1、Redis Streams 事件总线）已落地并在 `cmd/relay-gateway/wire.go` 中接入；仅剩 http.go 拆分未完成。

### [ ] 继续拆分 `internal/server/http.go`

关联设计：[架构重构方案 §4.1 / §10.2](./design/ARCHITECTURE_REFACTOR.md)

现状：

- `internal/server/http.go` 仍为 2296 行 / 63 个方法的 God Object，是架构重构路线图中唯一未完成的 P0 项。
- 已提取 `http_orchestrator.go`（204 行，聊天编排逻辑），但主体文件尚未达到 <400 行的目标。
- Phase 1 已落地的配套能力：`platform/grpc/resilience.go` + `fallback.go`（熔断与降级）、`platform/cache/`（auth/channel 多级缓存）、`platform/events/streams.go`（Redis Streams 事件总线），均已在 relay-gateway wire 中接入。

任务（按风险从低到高顺序）：

- [ ] 步骤 2：提取 `Forwarder`（stream / nonstream / ws 转发逻辑）到独立文件，复用现有 `http_raw_test.go` 做回归。
- [ ] 步骤 3：提取 `BillingCoord`（reserve / commit / release 计费协调，含超时与降级），补单元测试 + 降级测试。
- [ ] 步骤 4：按端点拆分 Handler 文件，使各 Handler 可独立测试。
- [ ] 步骤 5：提取 `Router` 和 `Middleware`，补路由注册测试。
- [ ] 步骤 6：验证所有端点 E2E 测试通过（`make test-e2e-suite`）。

验收标准：

- `internal/server/http.go` 行数降到 400 行以内，无单文件超过 400 行。
- 每一步拆分后 `http_raw_test.go` 与 `make test-unit` 全部通过。
- 拆分行为零变更：无新增/删除端点、无路由变更、无响应格式调整。
- 涉及 Relay 行为时追加 `go test ./internal/relay/... ./internal/channel/...` 与 `make test-e2e-suite`。

## P1 — Phase 0 可观测性基线

> 依据 `docs/design/BASELINE.md`。当前基线表有 16 处 TBD，需先建立量化基线，为后续优化提供对比依据。

### [x] 填充性能基线数据

关联基线表：[design/BASELINE.md](./design/BASELINE.md)

现状：

- `docs/design/BASELINE.md` 中 P50/P95/P99 延迟、错误率、吞吐量、gRPC 服务调用延迟、缓存命中率、熔断器状态均为 TBD（共 16 处）。
- 压测脚本 `scripts/benchmark/k6-baseline.js` 已存在但未运行记录。

任务：

- [x] 在本地或预发环境按 `BASELINE.md` 的「How to Run」章节运行 `k6-baseline.js`。
- [x] 记录 `/healthz`、`/v1/models`、`/v1/chat/completions` 的 P50/P95/P99 与错误率。
- [x] 记录 identity / channel / billing / log 四个 gRPC 服务的调用延迟。
- [x] 记录 auth / channel 缓存的 L1/L2 命中率与 miss 率。
- [x] 记录各下游服务的熔断器状态与 24h trip 次数。
- [x] 将结果填入 `BASELINE.md` 的基线表，并写入 History 表首行。

验收标准：

- `BASELINE.md` 中不再有 TBD 占位项。
- 原始 `results.json` 保存归档，可在后续 Phase 对比。
- 记录测试环境的 CPU / 内存 / Go 版本 / Kratos 版本。

## 已完成

### [x] v0.8.0 发布

- API 指南页、CC Switch 一键导入、admin 前端改由 `ADMIN_WEB_ROOT` 提供。

### [x] 合并 OAuth 回调路由修复

- 等待 `develop` 提交 `2cb0a23` 的完整 CI 通过。
- 合并到 `main`。
- 评估并发布 `v0.7.2`：已于 2026-07-15 正式发布。

验收标准：

- GitHub CI 和 Security Pipeline 全部通过。
- OAuth 回调路由的相关单元测试通过。
- `main` 包含 `2cb0a23` 的修复。

### [x] 同步部署方式与部署文档

关联 Issue：[部署方式是否同步更新 #5](https://github.com/mengbin92/micro-one-api/issues/5)

- 统一部署文档与 K8s 清单中的数据库 Secret 名称：使用 `db-credentials`。
- 在文档中补充 `admin-tls-secret` 的创建步骤。
- 为 K8s `billing-service` 和 `log-service` 注入 `SERVICE_TOKEN`。
- 移除生产必需 Secret 上不合理的 `optional: true`。
- 核对 `config-service` 是否确实需要 `SERVICE_TOKEN`：代码不读取该变量，已从 Compose/K8s 移除。
- 文档说明如何替换 `your-registry/<service>:v0.7.2`，生产示例使用固定版本而非浮动 `latest`。
- 核对全部 ConfigMap、Secret、Service、Ingress 名称和端口引用。
- 验证全新 Docker Compose 部署。
- 使用 kind、k3d 或测试集群执行一次 K8s smoke test。

进度与完成记录：

- `2cb0a23` 的 GitHub CI 和 Security Pipeline 均已通过，`develop` 后续头提交的两条流水线也通过。
- kind v1.33.1 smoke 中，九个应用及 MySQL/Redis 均达到 `1/1 Running`；Admin Pod 可访问 billing/log 内部接口，共享令牌鉴权成功，Relay `/healthz` 成功。
- 全新 MySQL 已一次完成 55 项自动迁移；修复了 SQL 字符串内分号解析、`phase1_indexes.sql` 错误列名，并将可选 `phase3_partitioning.sql` 排除出自动迁移。
- 2026-07-15 使用独立 Compose project 和全新 MySQL/Redis 数据卷完成最终 smoke：一次性 `migrate` 成功退出，九个应用容器均正常运行，七个内部健康端点、log-service 共享令牌鉴权、Relay `/healthz` 和 `/v1/models` 未授权响应全部符合预期，共 23 项通过、0 项失败；测试结束后容器、网络和数据卷均已清理。
- `origin/main` 的 `942b58c` 已包含 OAuth 修复提交 `2cb0a23`；该提交对应的 main CI、Security Pipeline 和 v0.7.2 Release workflow 均已成功完成。

验收标准：

- 新环境可以仅按照 `docs/deployment.md` 完成部署。
- 所有 Pod 正常 Ready，Admin、Relay、billing/log 内部接口可访问。
- 文档中的 Secret 名称、环境变量和清单完全一致。

### [x] 增加软件界面图和用户向文档

关联 Issue：[希望能有软件界面图和文档 #6](https://github.com/mengbin92/micro-one-api/issues/6)

- 增加用户 Dashboard 截图。
- 增加 Token 管理和用量统计截图。
- 增加渠道管理或渠道健康截图。
- 增加成本分析截图。
- 增加订阅套餐或订阅账号截图。
- 增加日志详情或对账页面截图。
- 在 README 中新增“界面预览”章节。
- 增加简化架构图，以及“适合谁 / 不适合谁”说明。
- 补充从空环境部署到创建首个渠道和 Token 的最短流程。
- 修复当前文档重组后遗留的失效相对链接。
- 将 `docs/README.md` 的最新版本从 `v0.7.0` 更新为 `v0.7.1` 或当前最新版本。

建议截图目录：

```text
docs/assets/screenshots/
```

验收标准：

- 新用户在 README 中能快速了解项目界面、服务组成和主要能力。
- README 和 `docs/**/*.md` 的本地文件链接检查无错误。
- 截图不包含真实密钥、邮箱、用户数据或上游账号凭据。

### [x] 增加部署与文档漂移检查

- CI 执行 `docker compose config`。
- 使用 `kubeconform` 或同类工具校验 `deployments/k8s/*.yaml`。
- 增加 Markdown 本地链接检查。
- 对部署清单中的必需 Secret/ConfigMap 引用增加静态检查或 smoke test。

验收标准：

- Secret 名称、失效文档链接或非法 K8s 清单能够在 PR 阶段阻断 CI。

### [x] 重新评估 grpc-gateway 迁移计划

关联决策：[HTTP 转换机制决策](./migration/grpc-gateway-migration-todo.md)

- 标准 unary CRUD 继续使用 Kratos `protoc-gen-go-http` 生成的 HTTP handler。
- 评估 grpc-gateway runtime mux：当前部署和调用链没有足以抵消双运行时维护成本的明确收益，决定不引入。
- 流式响应、WebSocket、Webhook、OAuth 回调和 One-API 兼容路由继续使用自定义 HTTP 实现。
- 将原迁移 TODO 改为正式技术决策记录，grpc-gateway 迁移标记为不再推进。

评审结论（2026-07-15）：

- 标准 HTTP API 从手写 CRUD handler 逐步收敛到 Kratos 生成 handler，而不是迁移到 grpc-gateway。
- `config`、`log`、`monitor`、`notify` 在切换前先补 HTTP 契约测试，核对状态码、JSON 编码、错误格式、鉴权和分页行为。
- `log` 的批量删除、`monitor` 的 latest health check、`notify` 的状态更新存在 proto、生成路由与当前手写路由不一致，按资源单独决策和修复，不作为无行为变化的机械替换。
- 只有在需要独立统一 REST 网关、多语言 gRPC 后端或集中式 HTTP 转码层时，才重新评估 grpc-gateway。

验收标准：

- 路线图不再保留已经过期但没有明确决策的版本承诺。
- 明确只维护 Kratos 生成 HTTP 与必要的自定义 HTTP 两类机制，不增加重复的 grpc-gateway runtime。

## 基线检查

每个任务完成前至少执行：

```bash
./scripts/check-architecture.sh
make test-unit
cd web && npm test && npm run lint
```

涉及部署时追加：

```bash
cd deployments/docker-compose
docker compose config
```

涉及 Relay 行为的分支追加：

```bash
go test ./internal/relay/... ./internal/channel/...
make test-e2e-suite
```
