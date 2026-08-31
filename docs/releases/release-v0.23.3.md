# Micro-One-API v0.23.3 发布：服务边界与凭证安全加固

> 2026-08-31 · 上一版：[v0.23.2](./release-v0.23.2.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.23.3)

v0.23.3 是 v0.23.2 之后的 **PATCH 安全修复版本**：收紧服务间认证、上游网络访问、登录限流和 relay 编排器灰度凭证边界，并修复 CodeQL 报告的凭证配置风险。

本版本 **无数据库迁移、无公共 API / proto 破坏性变更**。但服务间 gRPC 调用现在要求使用一致的 `SERVICE_TOKEN`，编排器 allowlist 改为由 `SERVICE_TOKEN` 加 key 的 HMAC-SHA256 摘要；升级前必须完成配置核对。

## 1. 加固服务间认证与入口边界

**根因**：部分内部 gRPC 服务缺少统一的服务令牌校验，入口代理传入的客户端地址也可能被无条件信任，导致内部调用和登录限流边界依赖部署拓扑而非显式配置。

**修复**：为内部 gRPC 服务统一增加 `SERVICE_TOKEN` Bearer 认证；为 identity-service 和 admin-api 增加可信代理 CIDR 配置；将跨副本登录失败计数写入 Redis，并在不可用时保留有界的进程内 fallback。Admin API 的内部 gRPC 暴露面同步收窄。

**影响服务**：`identity-service`、`admin-api`、`billing-service`、`channel-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker` 及 Kubernetes / Docker Compose 部署配置。

## 2. 防止上游 SSRF 与 DNS 重绑定

**根因**：上游 provider 的网络请求可能经过系统代理或在 DNS 解析与实际连接之间发生目标变化，私有地址、保留地址和不安全重定向的检查边界不够集中。

**修复**：统一使用直接连接的 SSRF 安全 HTTP transport，在每次新连接时解析并校验目标 IP，再直接拨号到已批准的地址；默认拒绝私有 / 保留地址和不安全重定向，仅对明确标记的自托管 Ollama 请求允许本地网络访问。

**影响服务**：`relay-gateway` 及共享 upstream provider 适配器。

## 3. 将 relay 编排器 allowlist 改为 keyed HMAC

**根因**：普通 SHA-256 bearer-token 摘要一旦泄露，可被离线猜测；旧配置名也无法表达服务令牌作为密钥的安全约束。

**修复**：将 `RELAY_ORCHESTRATOR_TOKEN_SHA256` 替换为 `RELAY_ORCHESTRATOR_TOKEN_HMAC_SHA256`，使用 `SERVICE_TOKEN` 作为 HMAC-SHA256 密钥；无密钥、无摘要或摘要格式非法时全部 fail closed。编排器仍默认关闭。

**影响服务**：`relay-gateway`、Docker Compose / Kubernetes 配置和部署文档。该变更只收紧灰度入口，不改变旧 handler 的默认运行路径。

## 4. 收紧生产默认配置并修复 CodeQL 凭证告警

**根因**：Grafana Compose 配置仍提供默认管理员密码，部署示例和服务配置未完整表达内部令牌与可信代理的必填 / 可选边界。

**修复**：Grafana 管理员密码改为显式必填；补齐各服务的 `SERVICE_TOKEN`、可信代理 CIDR 和 HMAC allowlist 示例；更新 Kubernetes Secret / ConfigMap 引用，并移除不再需要的内部 gRPC 暴露配置。

**影响服务**：监控栈、所有部署清单和运维文档；不写入任何真实凭证。

## 兼容性说明

- **API / proto**：无公共 API 或公共 proto 破坏性变更；仅内部运行时配置 proto 的 allowlist 字段改名。
- **数据库**：无新增迁移，无需执行数据库 schema 变更。
- **配置**：必须确认所有内部 gRPC 服务与调用端使用同一个 `SERVICE_TOKEN`。旧变量 `RELAY_ORCHESTRATOR_TOKEN_SHA256` 不再读取，启用灰度前必须重新计算 HMAC 摘要。
- **代理与登录**：生产环境应将 `IDENTITY_TRUSTED_PROXY_CIDRS` 和 `ADMIN_TRUSTED_PROXY_CIDRS` 设置为实际入口代理网段；直连端口时保持 admin 值为空。
- **Relay 观察**：本版本不要求为了打 tag 或发布 artifact 重启 `relay-gateway`。executor 观察期间不要构建、加载、重建或重启 `relay-gateway`，不要修改 `RELAY_ORCHESTRATOR_ENABLED` 或 allowlist；只有确认的安全紧急情况才例外。

## 升级步骤

```bash
git fetch --tags
git checkout v0.23.3
```

1. 备份当前镜像、配置和监控数据；不要把明文令牌写入 shell 历史或版本库。
2. 在所有内部服务和调用端配置一致的 `SERVICE_TOKEN`，并为入口代理配置准确的可信 CIDR。
3. 将旧的 `RELAY_ORCHESTRATOR_TOKEN_SHA256` 重新计算为以 `SERVICE_TOKEN` 为 HMAC 密钥的 `RELAY_ORCHESTRATOR_TOKEN_HMAC_SHA256`；不启用编排器时保持 `RELAY_ORCHESTRATOR_ENABLED=false`。
4. 按实际变更服务逐个构建并部署，使用 `docker compose up -d --no-deps <service>`；不要使用会连带重启依赖的宽泛 Compose 命令。
5. executor 观察期间不要部署 `relay-gateway`；若确有安全紧急情况需要重启 relay，应记录并按观察手册重新计算观察窗口。

## 验证

- `go test ./...`：验证服务令牌、可信代理、登录限流、SSRF transport 和 relay HMAC allowlist 回归。
- `./scripts/check-architecture.sh`：通过分层依赖检查。
- `./scripts/check-deployment-docs.sh`：通过部署文档与清单检查。
- 手工核对 Docker Compose / Kubernetes 中的 `SERVICE_TOKEN`、可信代理 CIDR、Grafana 密码和 HMAC allowlist 配置；确认旧 allowlist 变量不会被读取。
- 生产验证应仅针对已批准的服务执行，保留镜像 digest、启动日志和认证失败指标；不得因本次 tag 操作重启 relay。

## 完整变更日志

- fix(security): harden service and upstream boundaries
- fix(security): resolve CodeQL credential alerts
