# Micro-One-API v0.22.0 发布：安全配置与契约可靠性收口

> 2026-08-19 · 上一版：[v0.21.0](./release-v0.21.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.0)

v0.22.0 是 v0.21.0 之后的 **MINOR 安全与可靠性版本**。本版本完成渠道凭证加密写入、持久化服务 fail-fast、OpenAPI/前端类型契约门禁、渠道模型映射批量化、管理后台批量删除完整性、请求体分级限制以及前端错误兜底；同时完成 Relay 执行边界 ADR，为后续独立灰度迁移保留清晰边界。

**无 API 破坏性变更、无 proto 破坏性变更、无自动数据库 schema 迁移**。已有生产数据中的渠道 key 明文已通过备份后的手工迁移工具加密，工具可重复执行且不输出凭证内容。

## 1. 渠道凭证与启动安全

- **根因**：旧版持久化 channel 仓储在未配置加密密钥时会回退写入明文；channel / identity 缺少 DSN 时会静默进入易失内存仓储。
- **修复**：新增 `CHANNEL_ENCRYPTION_KEY` 长度校验，所有新渠道 key、订阅账号 access token / refresh token 写入均强制 AES-GCM；新增 `CHANNEL_MEMORY_MODE` / `IDENTITY_MEMORY_MODE` 显式内存模式和 JWT/DSN fail-fast。
- **存量迁移**：新增 `go run ./app/channel/cmd/channel-credentials`。默认 dry-run 只输出计数与记录 ID，`-apply` 在事务中加密疑似明文；疑似其他密钥加密的值列为 `indeterminate`，不会被覆盖。
- **影响服务**：channel-service、identity-service，以及 Compose/Kubernetes 部署配置。

## 2. OpenAPI 与前端契约治理

- **根因**：config-only proto 生成可能覆盖根 OpenAPI 规范，前端 API 类型存在漂移风险。
- **修复**：新增独立 `buf.gen.config.yaml`；`make api-check` 校验规范非空并包含核心路径；CI 校验 `web/src/types/api.ts` 与 proto 生成结果一致；TypeScript strict 和 ESLint 门禁正式启用。
- **影响服务**：admin-api 及 web 管理后台。

## 3. 渠道映射与管理操作完整性

- **根因**：渠道模型同步逐条查询/写入；禁用渠道批量删除固定读取单页，可能静默遗漏超过上限的记录。
- **修复**：模型注册与 managed mapping 改为批量读写；禁用渠道清理反复消费第 1 页并增加无进展保护；补齐超过单页、部分失败和空集合回归测试。
- **影响服务**：channel-service、admin-api。

## 4. 请求体与前端运行时可靠性

- **根因**：不同入口缺少统一请求体上限；前端通知轮询和路由异常处理存在重复状态与白屏风险。
- **修复**：JSON 入口限制 8 MiB，raw/multipart 入口限制 64 MiB，超限统一返回 413；NotificationPanel 改用 React Query，新增路由 ErrorFallback。
- **影响服务**：relay-gateway、admin-api/web。

## 5. Relay 执行边界 ADR

- 完成 executor、adaptor/provider、编排开关、灰度迁移和回滚条件决策。
- Chat Completions executor 首切片继续按独立 PR 灰度，不改变 v0.22 默认路径。

## 兼容性说明

- **API / proto**：保持兼容，无破坏性字段或 RPC 变更。
- **数据库**：无 schema migration；升级已有环境前必须确认 `CHANNEL_ENCRYPTION_KEY` 已配置，并按部署文档完成凭证 dry-run/apply。
- **配置**：持久化 channel-service 必须配置 16/24/32 字节 `CHANNEL_ENCRYPTION_KEY`；缺少 DSN 时必须显式设置对应 `*_MEMORY_MODE=true` 才允许内存仓储。
- **运行时**：未配置生产必需密钥或 DSN 时服务 fail-fast，不再静默启动不安全模式。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.0

# 1. 配置并安全保存 CHANNEL_ENCRYPTION_KEY（16/24/32 字节）
# 2. 先备份数据库，再执行 dry-run
go run ./app/channel/cmd/channel-credentials
# 3. 确认报告后执行一次迁移
go run ./app/channel/cmd/channel-credentials -apply
# 4. 再次 dry-run，确认 suspected_plaintext=0、indeterminate 已处理
# 5. 滚动重启 channel-service，其余服务按发布策略更新镜像
```

## 验证

- `make verify`：unit、race、架构边界、migration-check、前端 lint/test/build 全部通过。
- `make api-check`、`make wire-check` 和前端生成类型漂移检查通过。
- `./scripts/check-deployment-docs.sh`：Compose、K8s、Secret 引用和 Markdown 链接全部通过。
- Compose Go E2E suite：完整身份、管理、Relay、计费链路通过。
- Playwright admin smoke：35 passed，1 skipped。
- 生产凭证迁移：迁移前完成 MySQL 备份；channels 2 条疑似明文已重写，迁移后 dry-run 显示 channels 2 encrypted、subscription_accounts 3 encrypted、suspected_plaintext=0、indeterminate=0。

## 完整变更日志

- fix(security): enforce encrypted credentials and fail-fast startup checks
- fix(v0.22): harden P1 contract and reliability paths
- fix(v0.22): complete P2 reliability hardening
- docs(ci): require commit message bodies for non-trivial commits
- feat: add channel credential audit and idempotent migration tool
- docs: document v0.22 credential migration and deployment prerequisites
