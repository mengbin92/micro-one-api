# 项目 TODO

> 最后更新：2026-07-15
>
> 当前阶段重点：发布部署和项目展示已经收口，下一步清理过期路线图并明确后续技术决策。

## P0 — 发布与部署可用性

### [x] 合并 OAuth 回调路由修复

- [x] 等待 `develop` 提交 `2cb0a23` 的完整 CI 通过。
- [x] 合并到 `main`。
- [x] 评估并发布 `v0.7.2`：已于 2026-07-15 正式发布。

验收标准：

- GitHub CI 和 Security Pipeline 全部通过。
- OAuth 回调路由的相关单元测试通过。
- `main` 包含 `2cb0a23` 的修复。

### [x] 同步部署方式与部署文档

关联 Issue：[部署方式是否同步更新 #5](https://github.com/mengbin92/micro-one-api/issues/5)

- [x] 统一部署文档与 K8s 清单中的数据库 Secret 名称：使用 `db-credentials`。
- [x] 在文档中补充 `admin-tls-secret` 的创建步骤。
- [x] 为 K8s `billing-service` 和 `log-service` 注入 `SERVICE_TOKEN`。
- [x] 移除生产必需 Secret 上不合理的 `optional: true`。
- [x] 核对 `config-service` 是否确实需要 `SERVICE_TOKEN`：代码不读取该变量，已从 Compose/K8s 移除。
- [x] 文档说明如何替换 `your-registry/<service>:v0.7.2`，生产示例使用固定版本而非浮动 `latest`。
- [x] 核对全部 ConfigMap、Secret、Service、Ingress 名称和端口引用。
- [x] 验证全新 Docker Compose 部署。
- [x] 使用 kind、k3d 或测试集群执行一次 K8s smoke test。

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

## P1 — 文档与项目展示

### [x] 增加软件界面图和用户向文档

关联 Issue：[希望能有软件界面图和文档 #6](https://github.com/mengbin92/micro-one-api/issues/6)

- [x] 增加用户 Dashboard 截图。
- [x] 增加 Token 管理和用量统计截图。
- [x] 增加渠道管理或渠道健康截图。
- [x] 增加成本分析截图。
- [x] 增加订阅套餐或订阅账号截图。
- [x] 增加日志详情或对账页面截图。
- [x] 在 README 中新增“界面预览”章节。
- [x] 增加简化架构图，以及“适合谁 / 不适合谁”说明。
- [x] 补充从空环境部署到创建首个渠道和 Token 的最短流程。
- [x] 修复当前文档重组后遗留的失效相对链接。
- [x] 将 `docs/README.md` 的最新版本从 `v0.7.0` 更新为 `v0.7.1` 或当前最新版本。

建议截图目录：

```text
docs/assets/screenshots/
```

验收标准：

- 新用户在 README 中能快速了解项目界面、服务组成和主要能力。
- README 和 `docs/**/*.md` 的本地文件链接检查无错误。
- 截图不包含真实密钥、邮箱、用户数据或上游账号凭据。

### [x] 增加部署与文档漂移检查

- [x] CI 执行 `docker compose config`。
- [x] 使用 `kubeconform` 或同类工具校验 `deployments/k8s/*.yaml`。
- [x] 增加 Markdown 本地链接检查。
- [x] 对部署清单中的必需 Secret/ConfigMap 引用增加静态检查或 smoke test。

验收标准：

- Secret 名称、失效文档链接或非法 K8s 清单能够在 PR 阶段阻断 CI。

## P2 — 路线图清理

### [x] 重新评估 grpc-gateway 迁移计划

关联决策：[HTTP 转换机制决策](./migration/grpc-gateway-migration-todo.md)

- [x] 标准 unary CRUD 继续使用 Kratos `protoc-gen-go-http` 生成的 HTTP handler。
- [x] 评估 grpc-gateway runtime mux：当前部署和调用链没有足以抵消双运行时维护成本的明确收益，决定不引入。
- [x] 流式响应、WebSocket、Webhook、OAuth 回调和 One-API 兼容路由继续使用自定义 HTTP 实现。
- [x] 将原迁移 TODO 改为正式技术决策记录，grpc-gateway 迁移标记为不再推进。

评审结论（2026-07-15）：

- 标准 HTTP API 从手写 CRUD handler 逐步收敛到 Kratos 生成 handler，而不是迁移到 grpc-gateway。
- `config`、`log`、`monitor`、`notify` 在切换前先补 HTTP 契约测试，核对状态码、JSON 编码、错误格式、鉴权和分页行为。
- `log` 的批量删除、`monitor` 的 latest health check、`notify` 的状态更新存在 proto、生成路由与当前手写路由不一致，按资源单独决策和修复，不作为无行为变化的机械替换。
- 只有在需要独立统一 REST 网关、多语言 gRPC 后端或集中式 HTTP 转码层时，才重新评估 grpc-gateway。

验收标准：

- [x] 路线图不再保留已经过期但没有明确决策的版本承诺。
- [x] 明确只维护 Kratos 生成 HTTP 与必要的自定义 HTTP 两类机制，不增加重复的 grpc-gateway runtime。

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
