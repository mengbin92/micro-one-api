# Micro-One-API v0.22.2 发布：Kimi K3 请求参数兼容修复

> 2026-08-21 · 上一版：[v0.22.1](./release-v0.22.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.2)

v0.22.2 是 v0.22.1 的 **PATCH 兼容性修复版本**，适配 Kimi K3 官方接口对聊天请求参数和输出 token 字段的限制，修复调用 Kimi K3 持续返回错误的问题。

**无 API / proto 破坏性变更、无数据库 schema 迁移、无新增配置项**。本版本只需重新部署 `relay-gateway`；其余服务无需变更。

## 1. Kimi K3 聊天请求参数兼容

**根因**：relay-gateway 将通用 Chat Completions 请求中的固定采样参数（如 `temperature`、`top_p`、`n`、`presence_penalty`、`frequency_penalty`）以及旧版 `max_tokens` 原样转发给 Kimi K3。Kimi K3 官方接口不接受这些固定参数，并要求使用 `max_completion_tokens`，因此请求会被上游拒绝。

**修复**：在 provider 边界仅针对 Kimi K3（含别名及版本前缀）规范化请求：移除不支持的固定参数，将 `max_tokens` 映射为 `max_completion_tokens`，并保留 K3 支持的 `reasoning_effort`。流式、非流式、结构化和原始转发路径统一执行相同规范化；其他模型保持原有行为。

**影响服务**：`relay-gateway`。

## 兼容性说明

- **API / proto**：无破坏性变更；外部请求格式保持不变。
- **数据库**：无 schema migration。
- **配置**：无新增配置项。
- **上游行为**：Kimi K3 请求不再携带其拒绝的固定采样参数；非 Kimi K3 请求行为不变。参数限制依据 [Kimi K3 官方重要限制](https://platform.kimi.com/docs/guide/kimi-k3-quickstart#%E9%87%8D%E8%A6%81%E9%99%90%E5%88%B6)。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.2

# 仅重新构建并部署 relay-gateway，无需执行数据库迁移
./scripts/deploy-update.sh relay-gateway
```

## 验证

- `go test ./domain/upstream/provider ./internal/server`：通过。
- `make verify`：通过（unit、race、架构检查、migration-check、前端 lint/test/build）。
- 部署后使用 Kimi K3 做一次非流式和流式请求，确认上游不再因固定参数或 `max_tokens` 字段返回参数错误。

## 完整变更日志

- fix(provider): adapt Kimi K3 chat limits
