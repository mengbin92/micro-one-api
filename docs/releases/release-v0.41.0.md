# Micro-One API v0.41.0 发布：Passkey 无密码登录与 /v1/rerank 中继（C1/C2 备选功能包）

> 2026-11-12 · 上一版：[v0.40.0](./release-v0.40.0.md)（2026-11-11）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.41.0)

v0.41.0 是 v0.40.0 之后的 **MINOR 功能版本**：落地下一阶段计划 §4 备选功能包的 C1（Passkey 无密码登录）与 C2（/v1/rerank 中继）。安全中心与登录页新增通行密钥（WebAuthn passkey）登录与全生命周期管理；中继网关新增 `/v1/rerank` 路由，复用 embeddings 计费管线。新增 Identity 149 迁移（三方言），不改动计费契约与现有价格。

版本判 MINOR 而非 PATCH 的依据：新增两项面向用户的公开能力（passkey 登录入口/管理界面、`/v1/rerank` 网关面），属于向后兼容的新功能而非对既有行为的修复。

## 重新发版修订（2026-11-12，北京时间）

v0.41.0 首次发布后，develop 上两次 CI run 在 `Backend / Check Go formatting` 失败，且发布文档存在事实偏差。现以**同一版本号重新发布**（覆盖原 `v0.41.0` tag），把修复与文档校正一并纳入。版本号仍判 MINOR：本轮为缺陷修复与文档修正，未新增公开能力、API、迁移或计费契约。

### 1. 根因

- `app/identity/internal/data/challenge.go` 将 `errors` import 排在 `encoding/hex` 之前，gofmt 门禁失败；该步骤位于 Backend job 首位，导致后续 test / race / architecture / RBAC / 生成文件校验全部未执行。
- 公开 passkey 登录的诱饵 ceremony（绑定 user 0）在 complete 阶段仍会查询 `iam.User`，其 not-found 分支与真实账号的无效断言返回不同错误，账号枚举防护的响应形状被破坏。
- 发布文档把 `/v1/rerank` 预算写成 4 MiB、表名写成 `identity_passkeys`，并把 `passkey_enabled` 描述为账号级开关。
- 登录页新增「使用通行密钥登录」按钮后，`web/e2e/admin-smoke.spec.ts` 的非精确 `getByRole('button', { name: '登录' })` 同时匹配该按钮与提交按钮，Playwright strict mode 违规；Release 的 E2E gate（chrome / mobile-chrome）失败，镜像构建、manifest 发布与 GitHub Release 创建随之被跳过。

### 2. 修复

- `a1c2593d`：gofmt + import 分组；对 user-0 ceremony 在 IAM 查询与断言解析之前直接返回 `ErrMFAInvalid`；补 biz 回归与 24576 字节解码预算边界测试；刷新 RBAC SOURCE 摘要并补评审说明。
- `c03ab6a8`：校正 release notes / CHANGELOG，并如实记录 CI 失败。
- `fix(web)`：把 admin-smoke 的登录按钮定位改为 `exact: true`（与 `a2-security.spec.ts` 既有写法一致）；本地定向复跑 chrome / mobile-chrome 两个用例 2/2 通过。
- 本轮 `docs(release)` 提交：补齐重新发版的发布工件，并同步 README.md 与 README.en.md。

### 3. 影响服务

仅 identity-service（已线上热更新并验收）；relay-gateway、web、迁移 149 与计费契约不变。E2E 改动只在测试代码，不影响运行时服务。passkey 仍为暗开关，真实设备旅程待配置 RP 后验收。固定版本 tag 与同名镜像被覆盖，已按旧 tag 部署的环境不会自动更新，需显式重新部署。

## 新增内容

### 1. Passkey 无密码登录（C1）

**内容**：identity-service 新增六个安全端点（登录/注册的 begin+complete、列表、删除），WebAuthn 通行密钥作为首个抗钓鱼认证因子，与既有 TOTP MFA 共存——账号同时绑定 TOTP 时，passkey 登录后仍需完成第二因子。注册/删除通行密钥始终要求新鲜的 step-up 证明（与 ManageMFA 同语义），并受全局暗开关（`IDENTITY_PASSKEY_RP_ID` / `IDENTITY_PASSKEY_ORIGIN` 与 MFA 加密密钥）门控；列表接口的 `passkey_enabled` 是该全局能力的回显，并非账号级开关。凭据存于 IAM 写事务内，共享账号审计与 revision 语义。

**安全设计**：

- 公开的登录 begin 端点对未知、禁用、无通行密钥的账号返回**诱饵 ceremony**（discoverable begin + 按用户名确定性的伪 allowed credential），形状与真实响应一致，无法被用于枚举账号存在性或通行密钥归属；诱饵 ceremony 绑定 user 0，complete 阶段以与真实无效断言相同的错误失败。
- complete 拒绝签名计数器不回退也不前进的凭据（go-webauthn 将克隆 authenticator 报告为 CloneWarning 而非错误），阻断克隆重放。
- 凭据表以 `credential_id` 为主键，记录 `sign_count`/`last_used_at`，三方言迁移 149。

**暗开关**：未配置 `IDENTITY_PASSKEY_RP_ID` 与 `IDENTITY_PASSKEY_ORIGIN` 时全部端点返回 `PASSKEY_UNAVAILABLE`，不建表不走 ceremony；安全信封解码预算 4096 → 24576 以容纳 base64url 认证器数据。

**验证**：biz 层新增 decoy/暗开关/注册删除回归（含 anti-enumeration 形状断言），identity 全包单测、`make test-unit`、`make test-race`、`make migration-check`、RBAC 契约检查全绿；web 侧 LoginPage/SecurityCenter/passkey lib 组件回归在 CI 全绿。真实设备（手机/指纹）式 enrollment 与登录留待维护者在生产配置 RP 环境变量后验收。

### 2. /v1/rerank 中继（C2）

**内容**：中继网关注册 `/v1/rerank` 路由（8 MiB JSON 请求体预算，与 `/v1/embeddings` 等文本端点一致），走 raw relay 路径，复用 embeddings 计费管线——按 `usage` 字段计量的额度结算、每请求 attempt 日志、token 配额账本一致。

**验证**：`internal/server/http_raw_test.go` 新增路由回归；relay-gateway 编译与单测通过。真实付费 rerank 调用留待上游渠道与价格配置后由维护者验收。

## 兼容性

- 新增 Identity 149 迁移（MySQL/Postgres/SQLite 三方言，`identity_passkey` 表）；既有库由 identity-service 启动迁移自动补齐，属追加式迁移，不回写旧表。
- 计费契约不变：rerank 与 embeddings 同口径计量，无新价格表；passkey 不涉及计费。
- 配置新增（均为可选）：`IDENTITY_PASSKEY_RP_ID`、`IDENTITY_PASSKEY_ORIGIN`、`IDENTITY_PASSKEY_DISPLAY_NAME`（缺省 "Micro-One API"）。
- Web 前端需随本版成对更新（登录页与安全中心新增通行密钥交互）。

## 升级步骤

1. 部署 identity-service（自动执行迁移 149）与 relay-gateway；web 执行 `make web-dist` 更新宿主 dist。
2. 需要启用 passkey 时，为 identity-service 配置 `IDENTITY_PASSKEY_RP_ID`（如 `api.example.com`）与 `IDENTITY_PASSKEY_ORIGIN`（如 `https://api.example.com`），重启生效；不配置则功能保持关闭。
3. 需要对外开放 rerank 时，在渠道侧配置对应上游与模型价格（与 embeddings 同口径）。
4. 重新发版会覆盖 `v0.41.0` tag 与同名镜像；已按首次发布部署的环境需按新 tag 显式重新部署，不会自动滚动更新。

## 验证

- develop CI run `43170782758`（修复提交）与 `43177043778`（发布工件）：均为 `conclusion=success`、25 个 job 全绿；Backend 的 format / openapi / test / race / architecture / commit bodies / migration / RBAC / iam-browser / generated files 全部通过，Frontend、Integration、两个 Migration smoke、Deployment docs 与 Docker 18 个构建也全部通过。
- 主分支 CI run `43180264968`：25 个 job 全绿。
- 本地门禁：`make format-check`、`make test-unit`、`make test-race`、`make migration-check`、`make rbac-contract-check`（959 行）、`python3 scripts/rbac-matrix-refresh.py --check`、`make iam-browser-gate-check`、`make api-check`、`python3 scripts/check-markdown-links.py` 全绿；`make api` 后生成文件无漂移。
- `web`：CI 的 lint / test / critical coverage / build 全绿；本机全量 vitest 有 2 例 jsdom 超时基线（748/750），单文件 PlaygroundPage 复跑 15/15 通过；admin-smoke 登录用例修复后本地定向复跑 chrome / mobile-chrome 2/2 通过。
- 线上验收：identity-service 热更新为 `deploy-20261112-145032-c03ab6a8`，running、restarts=0、启动日志无错误；经 admin-api 探测 passkey begin/complete → 503 `MFA_UNAVAILABLE`（暗开关），`GET /api/user/security` → 401；rollback 锚点 `rollback-20261112-145032`。
- 首次重新发版流水线 `43180366130` 因上述 E2E 回归失败并被取消；修复后重跑 Release 流水线，以 GitHub Actions 运行结果为准。
- 待维护者验收：真实设备（手机/指纹）enrollment 与登录需先配置 RP 变量；真实付费 rerank 调用需先确认上游渠道与价格。

## 完整变更日志

- `feat(relay): support /v1/rerank with usage billing`
- `feat(identity): passkey passwordless login and enrollment`
- `chore(rbac): refresh entry matrix for passkey endpoints`
- `feat(web): passkey login and security-center management`
- `fix(identity): restore passkey decoy parity and unblock CI formatting gate`
- `docs(release): correct v0.41.0 facts and record CI failure`
- `docs(release): reissue v0.41.0 with README sync`
- `fix(web): disambiguate login button locator in admin smoke E2E`
- `docs(release): record E2E fix in v0.41.0 reissue artifacts`
