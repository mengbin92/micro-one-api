# Micro-One-API v0.10.2 发布：统一上游模型路由 + GLM 工具 Schema 修复

> 2026-07-26 · 上一版：[v0.10.1](./release-v0.10.1.md)（2026-07-25）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.10.2)

v0.10.2 是一次**问题修复版本**，聚焦解决 v0.10.0 / v0.10.1 引入模型管理与国内订阅账户后在实际生产中暴露的若干模型路由问题：API-key 渠道与订阅账号之间的优先级被硬编码、上游模型 ID 未能精确映射导致命中错误供应商、以及 GLM（智谱）等 Anthropic 兼容上游对 Responses API 自定义工具的 `input_schema` 严格校验导致 422。

本版**包含数据库迁移**（`066_add_upstream_model_ids.sql`，新增列、有幂等回填），**无 API 破坏性变更**（仅新增 proto 字段与一个内部 gRPC 方法），**无端点增删**。

## 问题现象与根因

### 1. API-key 渠道与订阅账号的优先级硬编码（fix: unify model routing across upstream providers）

v0.10.1 之前，`RelayUsecase.Plan` 的调度顺序是：先用客户端模型名选 API-key 渠道 → 失败后回退到已映射模型 → 仍失败才走订阅账号粘性会话 → 最后才做订阅账号选择。这意味着：

- 订阅账号**只能作为 API-key 渠道全部失败后的兜底**，无法与 API-key 渠道按配置的 `priority` 同层竞争，即使订阅账号优先级更高也不会被选中。
- 多个上游来源（普通渠道、订阅账号）无法在同一优先级层做平滑加权轮询。

### 2. 上游模型 ID 未能精确映射

同一个公开模型（例如 `glm-5.2`）在不同上游可能需要不同的精确标识（例如 NVIDIA 上游是 `z-ai/glm-5.2`）。v0.10.1 的 `ResolveChannelModel` 只能保留渠道配置的大小写拼写，没有独立的「上游精确模型 ID」字段，导致：

- 同一公开模型命中不同上游时，无法为每个上游单独指定真实模型 ID。
- 价格/账单维度与路由维度混淆（用户价格用公开小写名，上游成本用精确 ID），见 `docs/model-pricing-follow-up.md`。

### 3. GLM / Anthropic 兼容上游对自定义工具返回 422（fix(relay): prevent GLM tool schema 422 errors）

Codex 的 `apply_patch`、`web_search` 等 Responses API 自定义工具不带 `parameters` schema。转换到 Anthropic 兼容请求时，这些工具的 `input_schema` 被原样透传为 `null`。智谱 GLM、部分 Anthropic 兼容上游会校验 `input_schema` 必须为 JSON object，遇到 `null` 直接返回 `422 Unprocessable Entity`，导致带工具的 Responses 请求整体失败。

### 4. 渠道删除入口缺失 / 部署流程未文档化

- 后端 `DELETE /api/channel/{id}` 早已存在，但管理后台渠道页只有启用/禁用，没有删除入口。
- 跨平台部署拓扑（arm64 开发机 → x86_64 服务器、buildx 跨构建、前端挂载卷）此前散落在脚本里，没有沉淀进 AGENTS.md。

## 修复内容

### 1. 统一上游路由选择器

- **`internal/biz/upstream_selector.go`（新增）**：`UpstreamRouteSelector` 跨 API-key 渠道与订阅账号统一应用优先级分层（取最高 `priority` 的候选层），再在获胜层内做**平滑加权轮询**（Smooth Weighted Round-Robin，复用经典 nginx 算法）。带 4096 scope 上限的内存状态，避免长跑泄漏。
- **`RelayUsecase.Plan`**：重写为「粘性会话优先 → 否则并行尝试 API-key 渠道与订阅账号 → 用 `UpstreamRouteSelector` 在同优先级层裁决」。订阅账号不再硬编码为兜底，能与渠道按 `priority`/`weight` 公平竞争。
- **`Channel` DO 新增 `Weight uint32` 与 `UpstreamModelID string`**，`SubscriptionAccount` DO 新增 `UpstreamModelID string`，让权重与精确上游 ID 流经整个调度链。

### 2. 上游模型 ID 精确化

- **`upstream_model_id` 列**（迁移 `066_add_upstream_model_ids.sql`）：为 `model_channel_mapping` 与 `model_subscription_mapping` 新增 `upstream_model_id varchar(255) NOT NULL DEFAULT ''`，幂等回填：从 `abilities` / `subscription_account_abilities` 中按大小写不敏感匹配把已配置的真实模型名写入新列。
- **postgres / sqlite 对应迁移**：`migrations/postgres/004_add_upstream_model_ids.sql`、`migrations/sqlite/005_add_upstream_model_ids.sql` 保持三套 schema 同步。
- **proto**：`ModelChannelMapping.upstream_model_id`、`ModelSubscriptionMapping.upstream_model_id`、`UpsertChannelModelMappingRequest.upstream_model_id`、`UpsertSubscriptionModelMappingRequest.upstream_model_id`、`ChannelInfo.upstream_model_id`、`SubscriptionAccountInfo.upstream_model_id` 均为新增字段，向后兼容。
- **文档**：新增 `docs/model-pricing-follow-up.md`，明确「用户价格用公开小写模型名、上游真实 ID 只用于路由映射」的约定与后续清理计划。
- **前端**：`ModelDetailPanel`、`PricingPage`、`LogsPage` 配合展示上游模型 ID 与定价候选，并补齐对应单测。

### 3. GLM 工具 Schema 修复

- **`internal/apicompat/responses_to_anthropic_request.go`**：`web_search` / `google_search` / `web_search_20250305` 分支以及 default 分支（Codex `apply_patch` 等自定义工具）统一走 `normalizeAnthropicInputSchema`，无 schema 时回退为 `{"type":"object","properties":{}}`，杜绝 `input_schema: null`。
- **`internal/server/http_adaptor.go`**：订阅账号上游返回非 2xx 时，新增 `zap.Warn` 记录 `status_code` / `platform` / `channel_id` / `subscription_account_id` / `model` 与脱敏截断（2048）后的上游错误体，便于定位 422 类上游校验问题。

### 4. 管理后台与文档

- **渠道删除按钮**：`web/src/pages/admin/ChannelsPage.tsx` 在渠道操作区新增 Delete 按钮（Trash2 图标 + 确认弹窗），复用既有 `DELETE /api/channel/{id}`，沿用 subscription-accounts 页同款模式。
- **跨平台部署文档**：AGENTS.md 记录 arm64→x86_64 的 buildx 跨构建 + scp + docker load 标准流程、service→Dockerfile/path 映射表、buildx builder 一次性设置、前端 `web/dist` 挂载卷部署注意事项与验证/配置重启片段。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.10.2

# 开发者环境：重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：应用迁移 + 重建镜像 + 滚动重启
make migrate
docker compose build
docker compose up -d
```

**注意事项：**

- **必须执行数据库迁移**：`066_add_upstream_model_ids.sql` 为两张 mapping 表新增 `upstream_model_id` 列，默认空串，并幂等回填已配置 abilities 中的真实模型名。升级前建议备份 `model_channel_mapping` / `model_subscription_mapping`。
- **路由行为变化**：升级后，配置了更高 `priority` 的订阅账号将与 API-key 渠道同层竞争，不再被强制降级为兜底。若你的环境希望保持「渠道优先、订阅兜底」旧行为，请把订阅账号 `priority` 调到低于目标渠道即可。
- **上游模型 ID 回填**：迁移会自动从 abilities 回填，但仅有大小写不敏感匹配命中的行会被填充；未命中的行保持空串，relay 会回退到渠道配置的模型名，行为与 v0.10.1 一致。
- **开发者**：proto 新增字段与内部 gRPC 方法 `GetSubscriptionAccountWithSecrets`，需 `make proto` 重新生成；该 RPC 仅在服务内网使用，响应含解密密钥。
- **前端**：构建 `cd web && npm run build`；如使用挂载卷部署，按 AGENTS.md 前端流程单独 scp `web/dist`。

## 兼容性说明

- **API**：无破坏性变更；仅新增 proto 字段（`upstream_model_id` 系列）与一个内部 gRPC 方法，旧客户端无感知。
- **数据库**：有迁移（`066`），新增列均为 `NOT NULL DEFAULT ''`，幂等可重入。
- **配置**：无新增/删除环境变量。
- **运行时**：调度语义修正——订阅账号参与同优先级层竞争；其余行为与 v0.10.1 一致。

## 验证

发布前已确认：

- `go build` / `go vet` / `go test` 全部通过
- `internal/biz/upstream_selector_test.go` 覆盖优先级分层与平滑加权轮询
- `internal/biz/relay_test.go` 更新覆盖新调度路径（渠道 vs 订阅账号同层裁决）
- `internal/apicompat/anthropic_responses_test.go` 新增 `TestResponsesToAnthropicRequest_AllToolsGetObjectInputSchema`，断言所有工具 `input_schema` 为 object 且不含 `null`
- `internal/server/http_adaptor_test.go` 覆盖上游拒绝告警路径
- `app/channel/internal/data/model_test.go`、`app/channel/internal/service/channel_model_probe_test.go`、`subscription_model_probe_test.go` 覆盖上游模型 ID 读写与探测
- `web` 端 npm build / test / lint 全部通过（含 LogsPage、PricingPage 新增用例）

## 完整变更日志

- b8cd79b feat(admin): add channel delete button to channels page
- 11a1cb9 docs(agents): document cross-platform deploy workflow (arm64 dev -> x86_64 server)
- d175497 fix: unify model routing across upstream providers
- 3a47d6c fix(relay): prevent GLM tool schema 422 errors

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
