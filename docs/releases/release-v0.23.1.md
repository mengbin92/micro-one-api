# Micro-One-API v0.23.1 发布：渠道健康与日志安全修复

> 2026-08-24 · 上一版：[v0.23.0](./release-v0.23.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.23.1)

v0.23.1 是 v0.23.0 之后的 **PATCH 可靠性与安全修复版本**：修复单模型上游故障误触发整渠道熔断、重试放大健康失败，以及客户端请求凭证流入用量日志的问题；同时加强 `/models` 健康探测响应校验。

本版本 **无数据库迁移、无公共 API / proto 破坏性变更、无新增运行时配置**。

## 1. 隔离模型故障，避免渠道级误熔断

**根因**：同一请求的多次 retry 按 attempt 重复记录渠道健康失败；上游只对单个模型返回“无健康节点”时，渠道级阈值被快速触发，同渠道其他模型也被路由排除。

**修复**：同一请求内同一来源的多次 retry 合并为一次终态健康结算；明确识别模型范围故障并保留 fallback，不推进渠道级熔断；同时修复重复减少选择器 `inflight` 的问题。

**影响服务**：`relay-gateway`、`channel-service` 的健康反馈链路。

## 2. 阻断请求凭证进入用量日志

**根因**：transport-neutral executor 将包含请求 headers、body 和 bearer token 的完整请求传入用量日志接口，CodeQL #277 判定存在明文敏感信息日志风险。

**修复**：日志边界只传递模型、端点、请求 ID 和流式标记；event logger adapter 增加二次过滤，并补充回归测试验证 `Authorization` 等敏感数据不会进入日志钩子。

**影响服务**：`relay-gateway`。

## 3. 加强主动健康探测

**根因**：`/models` 返回 HTML 200 时，monitor-worker 仅依据 HTTP 成功状态判定上游健康。

**修复**：要求响应为包含数组形态 `data` 或 `models` 字段的 JSON；HTML、缺字段和错误字段类型均记录为 `invalid_response` 失败。

**影响服务**：`monitor-worker`、`channel-service` 健康反馈链路。

## 兼容性说明

- **API / proto**：无公共 API 或 proto 破坏性变更。
- **数据库**：无新增迁移，无需执行 `make migrate`。
- **配置**：无新增必需配置，现有配置可直接沿用。
- **部署范围**：重新部署 `relay-gateway` 和 `monitor-worker`；其他服务无需因本版本重启。

## 升级步骤

```bash
git fetch --tags
git checkout v0.23.1
```

1. 备份当前 relay-gateway 和 monitor-worker 镜像，按既有跨平台构建流程构建并发布新镜像。
2. 先更新 `monitor-worker`，确认健康探测指标正常，再更新 `relay-gateway`。
3. 观察模型故障 fallback、渠道健康失败计数、`inflight` 和 `invalid_response` 指标。
4. 如需回滚，恢复上一版镜像并重启受影响服务；本版本不涉及数据库回滚。

## 验证

- `go test ./internal/server`：通过。
- `go test ./internal/biz ./app/monitor/internal/biz`：通过。
- `go test -run '^$' ./internal/... ./app/monitor/...`：通过编译检查。
- 回归测试覆盖模型无健康节点 fallback、同渠道 retry 健康结算去重、HTML 200 探测失败和日志敏感字段隔离。

## 完整变更日志

- fix(security): keep request credentials out of usage logs
- fix(relay): isolate model outages from channel health
- test(config): avoid secret scanner false positive
