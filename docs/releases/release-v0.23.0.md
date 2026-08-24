# Micro-One-API v0.23.0 发布：Chat Completions 非流式执行器灰度与路由可靠性加固

> 2026-08-24 · 上一版：[v0.22.5](./release-v0.22.5.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.23.0)

v0.23.0 是 v0.22.5 之后的 **MINOR 执行边界与灰度能力版本**（9 个功能、修复与测试提交）：为 `/v1/chat/completions` 非流式请求增加默认关闭、token allowlist 保护的 executor staging 路径，补齐 transport-neutral 执行端口、adaptor registry、失败候选结算与错误脱敏，并修复管理后台成本图表标签重叠。

本版本 **不包含数据库迁移、无公共 API / proto 破坏性变更**。executor 灰度默认关闭，未命中 allowlist、关闭开关、流式请求和其他传输仍使用旧路径。

## 1. Chat Completions executor staging 与安全门禁

**根因**：旧 relay handler 同时承担 HTTP 传输、上游转发和结算编排，无法在不复制扣费请求的前提下进行小流量验证；直接切换还会把内部 bearer token 暴露给新路径。

**修复**：新增 `Executor`、`Planner`、`QuotaPort`、`Forwarder`、`EventLogger` transport-neutral 端口；`/v1/chat/completions` 非流式请求通过 adaptor registry 转发。staging 路径由 `RELAY_ORCHESTRATOR_ENABLED` 控制，并要求 bearer token 的 SHA-256 摘要命中 `RELAY_ORCHESTRATOR_TOKEN_SHA256` allowlist；关闭开关或清空 allowlist 即回退旧 handler。

**影响服务**：`relay-gateway`；运行时配置由 compose 传入，默认值保持关闭。

## 2. Failover 与 quota 生命周期加固

**根因**：executor 首切片缺少失败候选 Release、重试幂等键隔离和 commit 不确定状态的完整断言，可能掩盖重复结算风险；上游错误体也不应直接透传给客户端。

**修复**：接入现有 retry executor，失败候选 Release、成功候选只 Commit / Log 一次；重试请求使用独立 request ID；adaptor registry 统一处理 API-key 与订阅凭据解析；上游错误映射为 client-safe 消息并限制错误体大小；补齐 reserve 失败、context cancel、无候选、commit 失败、健康回写失败、日志写入失败和 golden/failover 测试。

**影响服务**：`relay-gateway`；不改变 billing 数据模型和现有 reservation 语义。

## 3. 管理后台成本图表与验证门槛

**根因**：成本图表标签在密集数据或窄宽度布局下发生重叠，影响管理员读取；executor 灰度前缺少可重复的失败矩阵和回滚门禁证据。

**修复**：调整成本图表标签布局并增加回归测试；新增默认关闭、allowlist 清空回滚测试和生命周期失败矩阵；E2E 脚本在测试环境未提供 `CHANNEL_ENCRYPTION_KEY` 时使用测试专用回退值，不修改部署配置。

**影响服务**：`admin-api` 挂载的 `web/dist`、`relay-gateway` 测试与部署脚本。

## 兼容性说明

- **API / proto**：无公共 API 或 proto 破坏性变更。新增的 relay bootstrap 配置仅为内部配置字段。
- **数据库**：无新增迁移，无需执行 `make migrate`。
- **配置**：新增 `RELAY_ORCHESTRATOR_ENABLED` 和 `RELAY_ORCHESTRATOR_TOKEN_SHA256`，默认分别为 `false` 和空 allowlist；配置只保存 SHA-256 摘要，不保存原始 token。
- **路由行为**：非流式 Chat Completions 只有在开关开启且 token 命中 allowlist 时进入 executor；流式、未命中 allowlist 或关闭开关时继续走旧路径。
- **前端**：成本图表修复需要同步 `web/dist`；compose 中 admin-api 使用只读挂载，不需要重启容器即可生效。

## 升级步骤

```bash
git fetch --tags
git checkout v0.23.0
```

1. 确认数据库无需迁移，备份当前 relay 镜像和 `/opt/web/dist`。
2. 部署 `relay-gateway`，再同步 `web/dist`；不要在生产主机上构建镜像。
3. 保持 `RELAY_ORCHESTRATOR_ENABLED=false` 完成基础健康检查。
4. 如启动 staging 灰度，在安全环境计算 token 摘要并配置 allowlist：

   ```bash
   printf %s "$STAGING_TOKEN" | sha256sum
   ```

   只将输出摘要写入 `RELAY_ORCHESTRATOR_TOKEN_SHA256`，不要写入原始 token。
5. 仅对内部 allowlist 开启非流式请求，连续观察至少 7 天，记录 success、error、failover、latency 和 quota outcome，并与旧路径对照。
6. 回滚时关闭 `RELAY_ORCHESTRATOR_ENABLED` 或清空 allowlist 后重建 relay 容器；必要时恢复带时间戳的 rollback 镜像。

## 验证

- `make verify`：unit、race、architecture、migration-check、frontend lint/test/build 全部通过。
- `go test -race ./internal/biz/... ./internal/server/...`、`go vet ./internal/server ./internal/biz ./internal/adaptor` 通过。
- Web Playwright：35 passed、1 skipped；`npm run build` 通过。
- Markdown 链接和 E2E 脚本语法检查通过。
- 生产部署验证：`relay-gateway` `/healthz` 返回 `{"status":"ok"}`；旧 relay 镜像和旧 frontend 均已保留备份。
- 本机 compose E2E 已执行，但 Docker Desktop 并行构建服务镜像因内存不足退出；发布 workflow 的 Release E2E gate 仍是 tag 发布前的最终门禁。

## 完整变更日志

- fix(admin): prevent cost chart label overlap
- test(web): cover cost chart labels and sync v0.23 roadmap
- feat(relay): gate executor staging by token allowlist
- chore(deploy): parameterize relay orchestrator rollout
- fix(relay): sanitize executor error responses
- feat(relay): add executor failover settlement
- refactor(relay): add transport-neutral executor ports
- refactor(relay): route executor through adaptor registry
- test(relay): cover staging failure matrix
