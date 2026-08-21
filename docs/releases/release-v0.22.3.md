# Micro-One-API v0.22.3 发布：计费、Claude 兼容与渠道健康修复

> 2026-08-21 · 上一版：[v0.22.2](./release-v0.22.2.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.3)

v0.22.3 是 v0.22.2 的 **PATCH 生产修复版本**，包含计费价格键归一化、CC Switch / Claude 兼容性和渠道健康误判修复。修复了大小写不一致导致的计费回退、Anthropic 客户端模型探测失败、SSE 流式响应被中间件截断，以及上游敏感词策略拒绝误触发渠道熔断等问题。

**无 API / proto 破坏性变更、无数据库 schema 迁移、无新增配置项**。Token 明文 key 仅在创建成功时返回一次；详情和更新接口统一返回 `masked_key`，调用方不能再依赖这些接口补取完整 key。

## 1. Billing 价格查找大小写归一化

**根因**：relay 透传的模型名称可能保留用户请求中的大小写，而 billing 的价格、倍率和上游价格配置键按大小写精确匹配，未命中时静默使用默认倍率 `1.0`。

**修复**：查找侧与配置解析侧统一执行 `TrimSpace + ToLower`；模型价格、模型倍率、completion 倍率和三级上游价格键均覆盖，GroupRatio 分组名保持原有大小写敏感语义。

**影响服务**：`billing-service`。

## 2. CC Switch / Claude 请求兼容与流式稳定性

**根因**：Claude 客户端使用 `x-api-key` 请求 `/v1/models`，relay 模型端点只接受 Bearer；单模型查询还缺少鉴权和模型可见性校验。前端会错误添加 `sk-` 前缀，并允许把脱敏 Token 传入 CC Switch。Metrics ResponseWriter 未透传 `http.Flusher`，导致 SSE 请求被报告为 `streaming not supported`。

**修复**：统一 `x-api-key` / Bearer 提取逻辑，模型列表和单模型查询共享鉴权、分组模型和 Token 白名单校验；CC Switch 原样使用平台 Token，仅在创建成功弹窗提供导入入口；Metrics wrapper 补齐 `Flush` 和 `Unwrap`。identity-service 只有创建接口返回完整 key，详情和更新接口只返回 `masked_key`。

**影响服务**：`relay-gateway`、`identity-service`、Web 前端 `web/dist`。

## 3. 上游策略拒绝不再误触发渠道熔断

**根因**：部分上游将 `sensitive_words_detected` 业务策略拒绝编码为 HTTP 500，relay 将所有 5xx 都计为渠道基础设施故障，导致健康失败累加并打开渠道熔断。

**修复**：该错误仍允许跨渠道 fallback，以保留请求成功率；但 `sensitive_words_detected` 被记录为上游可达的策略拒绝，不增加渠道失败次数，fallback 原因标记为 `policy_rejection`。真正的 5xx、超时和网络错误仍按原策略参与熔断。

**影响服务**：`relay-gateway`、`channel-service` 健康状态记录链路。

## 兼容性说明

- **API / proto**：无破坏性变更；模型查询继续兼容 Bearer，并新增 Anthropic `x-api-key` 支持。
- **Token 安全语义**：创建接口返回一次完整 `key`；列表、详情和更新接口只返回 `masked_key`。关闭创建成功弹窗后无法恢复明文 key，需要重新创建 Token。
- **数据库**：无 schema migration。
- **配置**：无新增配置项。
- **计费**：模型价格配置键现在按大小写不敏感方式匹配；GroupRatio 分组名行为保持不变。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.3
```

按 [部署文档](../deployment.md) 重新构建并滚动部署以下服务：

- `billing-service`
- `relay-gateway`
- `identity-service`
- 发布新的 `web/dist`

无需执行数据库迁移。升级 relay-gateway 后，如存在已打开的内存熔断器，重启会清除 relay 进程内状态；channel-service 持久化健康状态会在成功探测或成功记录后恢复。

## 验证

- `go test ./app/identity/...`：通过。
- `go test ./platform/middleware ./internal/server`：通过。
- `go test ./internal/biz ./internal/server -count=1`：通过。
- `go vet ./platform/middleware ./internal/server ./app/identity/...`：通过。
- relay-gateway / identity-service Wire 构建：通过。
- Web 测试 34 个文件、116 个用例：通过；`npm run lint`、`npm run build`：通过。
- 线上 relay `/v1/models` 无凭证返回 401；`neo` 渠道健康状态恢复为 `healthy`，连续失败为 0。

仓库架构检查仍有与本版本无关的既有 channel 生成代码问题：`app/channel/internal/service/model.go` 使用了当前 `channelv1` 中不存在的 `Suppliers` 字段。

## 完整变更日志

- fix(billing): normalize pricing model keys
- fix(relay): harden CC Switch Claude compatibility
- fix(relay): exclude policy rejections from channel health
