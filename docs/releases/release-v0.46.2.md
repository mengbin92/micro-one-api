# Micro-One API v0.46.2 发布公告

> 2026-11-19 · 上一版 [v0.46.1](./release-v0.46.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.46.2)

v0.46.2 是 **PATCH 修复版本**，恢复实时语音与上游之间的 WebSocket 双向连接。

## 1. Realtime 保留 WebSocket 可读写连接

**根因**。Relay 用 SSE 客户端连接实时语音上游。SSE 响应体包装仅支持读和关闭，导致 WebSocket 无法取得写入连接，即使上游已经接受握手。

**修复**。Realtime 使用现有的受保护上游 HTTP 客户端建立双向连接。沿用 15 秒连接超时、TLS 验证、上游地址限制和同源重定向限制。

**影响服务**。Relay Gateway。

## 兼容性说明

实时语音的请求协议、路由授权和用量结算规则沿用 v0.46.0 的契约。无需数据库迁移。

## 升级步骤

1. 构建 linux/amd64 Relay Gateway 镜像，保留生产回滚标签。
2. 部署 Relay Gateway，核对健康检查和源码摘要。
3. 确认公网反代为 `/v1/realtime` 传递 WebSocket 升级头。
4. 用专属组 Key 完成一轮对话，核对终态用量和账本扣费。

回滚时恢复上一版 Relay Gateway 镜像。

## 验证

- [x] 模拟上游回归先红后绿，验证实际 WebSocket 握手和双向收发。
- [x] 综合门禁 `make verify` 通过。
- [x] 高强度机器评审覆盖 Realtime 运行时代码并正常结束，无发现。
- [ ] 生产 Realtime 实测和扣费核对。

## 完整变更日志

- `fix(relay): preserve the duplex realtime upstream connection`
- `docs(release): v0.46.2`
