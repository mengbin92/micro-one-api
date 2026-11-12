# Micro-One API v0.41.0 发布：Passkey 无密码登录与 /v1/rerank 中继（C1/C2 备选功能包）

> 2026-11-12 · 上一版：[v0.40.0](./release-v0.40.0.md)（2026-11-11）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.41.0)

v0.41.0 是 v0.40.0 之后的 **MINOR 功能版本**：落地下一阶段计划 §4 备选功能包的 C1（Passkey 无密码登录）与 C2（/v1/rerank 中继）。安全中心与登录页新增通行密钥（WebAuthn passkey）登录与全生命周期管理；中继网关新增 `/v1/rerank` 路由，复用 embeddings 计费管线。新增 Identity 149 迁移（三方言），不改动计费契约与现有价格。

版本判 MINOR 而非 PATCH 的依据：新增两项面向用户的公开能力（passkey 登录入口/管理界面、`/v1/rerank` 网关面），属于向后兼容的新功能而非对既有行为的修复。

## 新增内容

### 1. Passkey 无密码登录（C1）

**内容**：identity-service 新增六个安全端点（登录/注册的 begin+complete、列表、删除），WebAuthn 通行密钥作为首个抗钓鱼认证因子，与既有 TOTP MFA 共存——账号同时绑定 TOTP 时，passkey 登录后仍需完成第二因子。注册/删除通行密钥始终要求新鲜的 step-up 证明（与 ManageMFA 同语义），并受账号 `passkey_enabled` 开关门控；凭据存于 IAM 写事务内，共享账号审计与 revision 语义。

**安全设计**：

- 公开的登录 begin 端点对未知、禁用、无通行密钥的账号返回**诱饵 ceremony**（discoverable begin + 按用户名确定性的伪 allowed credential），形状与真实响应一致，无法被用于枚举账号存在性或通行密钥归属；诱饵 ceremony 绑定 user 0，complete 阶段必然失败。
- complete 拒绝签名计数器不回退也不前进的凭据（go-webauthn 将克隆 authenticator 报告为 CloneWarning 而非错误），阻断克隆重放。
- 凭据表以 `credential_id` 为主键，记录 `sign_count`/`last_used_at`，三方言迁移 149。

**暗开关**：未配置 `IDENTITY_PASSKEY_RP_ID` 与 `IDENTITY_PASSKEY_ORIGIN` 时全部端点返回 `PASSKEY_UNAVAILABLE`，不建表不走 ceremony；安全信封解码预算 4096 → 24576 以容纳 base64url 认证器数据。

**验证**：biz 层新增 decoy/暗开关/注册删除回归（含 anti-enumeration 形状断言），identity 全包单测、`make test-unit`（126 包）、`make test-race`、`make migration-check`、RBAC 契约检查全绿；web 侧 LoginPage/SecurityCenter/passkey lib 组件回归 750 项通过，lint/build 通过。真实设备（手机/指纹）扫码式 enrollment 与登录留待维护者在生产配置 RP 环境变量后验收。

### 2. /v1/rerank 中继（C2）

**内容**：中继网关注册 `/v1/rerank` 路由（4 MiB 请求体预算），走 raw relay 路径，复用 embeddings 计费管线——按 `usage` 字段计量的额度结算、每请求 attempt 日志、token 配额账本一致。

**验证**：`internal/server/http_raw_test.go` 新增路由回归；relay-gateway 编译与单测通过。真实付费 rerank 调用留待上游渠道与价格配置后由维护者验收。

## 兼容性

- 新增 Identity 149 迁移（MySQL/Postgres/SQLite 三方言，`identity_passkeys` 表）；既有库由 identity-service 启动迁移自动补齐，属追加式迁移，不回写旧表。
- 计费契约不变：rerank 与 embeddings 同口径计量，无新价格表；passkey 不涉及计费。
- 配置新增（均为可选）：`IDENTITY_PASSKEY_RP_ID`、`IDENTITY_PASSKEY_ORIGIN`、`IDENTITY_PASSKEY_DISPLAY_NAME`（缺省 "Micro-One API"）。
- Web 前端需随本版成对更新（登录页与安全中心新增通行密钥交互）。

## 升级步骤

1. 部署 identity-service（自动执行迁移 149）与 relay-gateway；web 执行 `make web-dist` 更新宿主 dist。
2. 需要启用 passkey 时，为 identity-service 配置 `IDENTITY_PASSKEY_RP_ID`（如 `api.example.com`）与 `IDENTITY_PASSKEY_ORIGIN`（如 `https://api.example.com`），重启生效；不配置则功能保持关闭。
3. 需要对外开放 rerank 时，在渠道侧配置对应上游与模型价格（与 embeddings 同口径）。

## 验证

- `go build ./...`、`go test ./app/identity/...`、`make test-unit`、`make test-race`、`make migration-check`、`python3 scripts/rbac-matrix-refresh.py --check`（随 rbac-contract-check）全部通过。
- `web`: `npx vitest run` 750 项通过，`npm run lint`、`npm run build` 通过。
- 生产部署后：`docker ps` + `docker logs` 验证 identity-service/relay-gateway 健康；未配置 RP 变量时 `PASSKEY_UNAVAILABLE` 预期返回。

## 完整变更日志

- `feat(relay): support /v1/rerank with usage billing`
- `feat(identity): passkey passwordless login and enrollment`
- `chore(rbac): refresh entry matrix for passkey endpoints`
- `feat(web): passkey login and security-center management`
