# Micro-One-API v0.2.9 发布公告

> 2026-06-26 · 上一版: [v0.2.8](./release-v0.2.8.md) (2026-06-25)

v0.2.9 新增 Codex Responses WebSocket 协议入站支持，relay-gateway 现可直接作为 Codex CLI 的 WebSocket 后端，无需中间代理层。无数据库迁移，无破坏性 API 变更。

## 亮点

- **Codex CLI 原生接入**：relay-gateway 在 `POST /v1/responses` 上探测 `Upgrade: websocket` 请求，自动切换为 WebSocket 双向转发，兼容 Codex CLI 的 Responses 协议。非 Upgrade 请求仍走原有 HTTP/SSE 路径，完全向后兼容。
- **协议镜像转发**：客户端 ↔ 上游双向 pump，逐帧转发 Codex 事件（`response.created` → `response.completed` 等），并解析每个 turn 的 usage 触发计费提交与日志写入，复用现有 reserve / commit / release 配额链路。
- **连接池复用**：每个渠道维护空闲上游连接缓存，优先复用（经 Ping 健康检查），避免每个 turn/session 重复 dial 上游；支持每渠道最大连接数与空闲超时淘汰。
- **跨进程会话粘滞**：`response_id → channel_id` 绑定同时写入本地热缓存和 Redis，多副本部署下也能让多轮会话链落到同一渠道；未配置 Redis 时自动降级为纯内存。
- **多渠道故障转移**：在上游 dial 失败或转发首字节前的可重试错误时，按优先级自动选择替代渠道重试（默认最多切换 2 次）；一旦已有字节下发到客户端即停止 failover，避免破坏客户端视图。

## 变更内容

### Added

- `internal/relay/server/openai_ws_client.go`：上游 WebSocket dialer（基于 `coder/websocket`），含 permessage-deflate 与 16MiB 读上限，适配大体积 Codex 事件帧。
- `internal/relay/server/openai_ws_relay.go`：客户端 ↔ 上游双向转发 pump，含空闲看门狗、逐 turn usage 解析与终态事件回调。
- `internal/relay/server/openai_ws_forwarder.go`：`Accept` → 读取首个 `response.create` → `relaybiz.Plan` → dial 上游 → 转发，含逐 turn 配额提交/释放与 usage 日志，复用 `ingestUsageLog` 管道；含多渠道 failover 与 sticky 路由。
- `internal/relay/server/openai_ws_pool.go`：每渠道空闲连接缓存与后台淘汰扫描（默认每渠道最多 8 个连接、空闲 5 分钟淘汰）。
- `internal/relay/server/openai_ws_state_store.go`：本地 + Redis 双层 sticky 状态存储。
- `internal/relay/server/http.go`：在 `/v1/responses` 上检测 WebSocket Upgrade 并分派到 WS forwarder。
- `internal/relay/config/config.go`：新增 `openai_ws` 配置块（超时、连接池、failover、sticky、Redis）。
- 依赖：`github.com/coder/websocket v1.8.14`。

### Configuration

新增 `openai_ws` 配置块，所有字段可选，零值回落到默认值：

| 字段 | 说明 | 默认值 |
| --- | --- | --- |
| `write_timeout` | 每帧写超时 | `2m` |
| `idle_timeout` | 转发空闲超时 | `5m` |
| `dial_timeout` | 上游 dial 超时 | `30s` |
| `first_message_timeout` | 升级后等待首帧超时 | `30s` |
| `max_conns_per_channel` | 每渠道最大连接数 | `8` |
| `failover_max_switches` | 故障转移最大切换次数 | `2` |
| `sticky_ttl` | sticky 绑定 TTL | `1h` |
| `redis_addr` | 跨进程 sticky 存储 Redis 地址（空=纯内存） | 空 |
| `redis_password` | sticky 存储 Redis 密码 | 空 |

## Security

- **gosec SAST**：本次新增代码（`internal/relay/server/openai_ws_*`）0 issues。
- **govulncheck SCA**：全代码库 0 vulnerabilities。
- **gitleaks 密钥扫描**：本次新增代码 0 leaks（全仓 2 条命中为历史文档 `README.md` / `docs/community-promotion-blog.md` 中的 `Authorization: Bearer YOUR_TOKEN` 占位符示例，非真实密钥）。

## 升级指南

1. 拉取最新镜像并重启 relay-gateway 服务。
2. 确保管理后台已为 Codex 使用的目标模型配置可用渠道。
3. Codex CLI 用户将 base URL 指向 relay-gateway 即可接入。

无数据库迁移，无强制配置文件变更，可直接滚动更新。多副本部署如需跨进程 sticky 路由，按需配置 `openai_ws.redis_addr`。

## 测试

- 新增 WS 相关单元测试：forwarder / pool / relay / state store，覆盖连接复用、broken 连接淘汰、dial 错误、sticky 存储与过期、failover 与默认值。
- 新增端到端集成测试：mock 上游 WS 服务回放 `response.created` → `response.completed`（含 usage），验证客户端收到完整帧、计费按 usage 提交（12/8/3）、sticky 路由写入。
- 全量 `go build ./...` 通过；`internal/relay/server` 及各业务包单元测试通过。
