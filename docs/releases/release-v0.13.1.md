# Micro-One-API v0.13.1 发布：修复 identity→billing gRPC SERVICE_TOKEN 认证

> 2026-08-01 · 上一版：[v0.13.0](./release-v0.13.0.md)（2026-08-01）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.13.1)

v0.13.1 是 v0.13.0 的 **PATCH 修复版本**，解决 v0.13.0 引入 gRPC `SERVICE_TOKEN` 鉴权后，identity-service 调用 billing-service 的客户端未携带 Token，导致用户账单、消费记录、余额和支付订单等接口返回 gRPC 鉴权错误的问题。

## 修复内容

- `identity-service` 的 billing gRPC 客户端现在通过 `grpc.WithPerRPCCredentials` 携带 `SERVICE_TOKEN`，与 billing gRPC 服务端 fail-closed 鉴权对齐。
- billing 客户端初始化时如果 `SERVICE_TOKEN` 为空，服务会启动失败而不是带着错误的认证状态运行，避免生产环境再次出现“容器启动但接口全挂”。
- 生产 Docker Compose 同步补齐 `SERVICE_TOKEN` 注入：identity/channel/billing/log/monitor/notify 均从 `.env` 获取同一共享密钥。

## 影响范围

受影响接口包括但不限于：

- `/api/user/logs`
- `/api/user/dashboard`
- `/api/user/quota`
- `/api/user/payment/orders`
- 其它由 identity-service 转发到 billing-service 的用户账务接口

## 兼容性说明

- **API**：无破坏性变更。
- **数据库**：无新增迁移。
- **配置**：`SERVICE_TOKEN` 必须非空，且所有需要互调的服务应注入同一个值。
- **部署**：重新构建并部署 `identity-service`；同时确认 compose 中相关服务已注入 `SERVICE_TOKEN`。

## 升级步骤

```bash
git fetch --tags
git checkout v0.13.1

# 重新生成/构建（无 proto 变更，按常规流程执行即可）
make init
make proto
docker compose build identity-service
docker compose up -d --no-deps --force-recreate identity-service
```

## 验证

- `go test ./app/identity/...` 通过。
- `/api/user/logs`、`/api/user/dashboard`、`/api/user/quota`、`/api/user/payment/orders` 均返回成功。
- `/api/admin/summary`、`/v1/channels` 等管理接口未再出现 `service token not configured`。

完整变更日志：

- fix(identity): attach SERVICE_TOKEN to billing gRPC client
