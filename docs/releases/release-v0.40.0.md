# Micro-One-API v0.40.0 发布：MFA 二维码绑定、GitHub OAuth 生产化与账号安全验收包

> 2026-11-11 · 上一版：[v0.39.3](./release-v0.39.3.md)（2026-11-05）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.40.0)

v0.40.0 是 v0.39.3 之后的 **MINOR 功能版本**：安全中心新增 MFA 二维码扫码绑定；GitHub OAuth 从配置到真实绑定全链路打通（含部署透传修复与回调超时根因修复）；个人资料页新增第三方账号绑定状态回显。对应 N3 验收包主体，真实手机验证器、恢复码与敏感操作验收仍为后续单项。无数据库迁移，不改动计费契约与价格。

版本判 MINOR 而非 PATCH 的依据：新增面向用户的公开能力（MFA 二维码绑定入口、资料页绑定状态回显、GitHub OAuth 绑定全链路），不只修复既有行为；其中 OAuth 回调超时修复若单独发布属 PATCH，随本批能力一并发布。

## 新增内容

### 1. MFA 二维码扫码绑定

**内容**：安全中心 MFA 设置新增二维码扫码绑定，复用既有 `otpauth_uri` 与 `qrcode.react`，二维码在浏览器本地生成，保留手动密钥与验证器链接；取消或启用完成时随设置状态清除。无新增接口、存储或依赖。

**验证**：SecurityCenter/Profile 组件回归 9 项通过；`tsc -b` 与定向 ESLint 通过；`e2e/security-qr.spec.ts` 桌面/移动 × 中英 4 项通过（暗色模式白底黑码、无横向溢出、取消清理、无外部请求）。真实手机扫码 enrollment 待生产由维护者完成。

### 2. GitHub OAuth 生产化（配置透传 + 回调超时根因修复）

**根因**：GitHub 交换串行三次出站请求（token、user、emails），各带 10s 客户端超时，合计最坏约 30s，恰好等于平台 HTTP 服务 30s 读超时灌入请求 context 的预算。生产真实绑定时交换真实消耗了几乎全部预算，绑定事务继承已死请求 context，首条 SQL 立即 `context deadline exceeded`——GitHub 侧已完成同意，绑定却写不进库。

**修复**：

- 交换收敛到 25s 独立预算；交换成功后的绑定/登录/注册奖励改用 `context.WithoutCancel` 脱离请求生命周期的 5s 写 context（值保留，含授权凭证），两处回调入口同改；阶段预算之和不超过 30s 请求预算。
- Identity 显式声明 30s 请求超时并注释预算约束。
- 部署侧：三套 Compose 与对应环境示例补齐 GitHub OAuth 四变量（`OAUTH_BASE_URL`、`*_ENABLED`、`*_CLIENT_ID`、`*_CLIENT_SECRET`）透传，`scripts/check-deployment-docs.sh` 增加渲染断言门禁（本机缺 kustomize，完整门禁未执行）。

**验证**：隔离回归 `TestOAuthBindCallbackDeadline`（请求 context 在交换结束时死亡，绑定仍须落库）红→绿；生产 12:22 维护者真实授权绑定成功，`user_oauth_identities` 落库且 `account.oauth.bind` 审计 success。

### 3. 第三方账号绑定状态回显

**内容**：`/api/user/self` 新增 `oauth_providers` 字段（`BoundOAuthProviders`：identities 表 + 既有 OAuth 登录用户 legacy 列，禁用 provider 的存量绑定仍回显，查询失败返回 503 不吞错）；个人资料页"第三方账号"按绑定态显示"已绑定"并消费 `?oauth_bind=success` 弹成功提示、清 URL。

**验证**：`TestSelfListsBoundOAuthProviders` 与 biz 表驱动用例通过；前端组件用例（已绑定禁用态 + 未绑定可点）通过。

## 兼容性说明

- **API**：`/api/user/self` 响应新增 `oauth_providers` 字段，纯增量；新增失败态 503（绑定状态暂不可用）。
- **数据库**：无新增迁移；绑定数据沿用 `user_oauth_identities`。
- **配置**：GitHub OAuth 为可选启用；Compose 环境需显式设置四变量才开启，默认关闭不变。K8s 清单当前不透传 OAUTH_* 变量（遗留缺口，另行决定）。
- **部署顺序**：identity-service 与前端制品成对更新；admin-api 随本版携带备份槽位修复（66709a1b）。
- **回滚**：identity-service / admin-api 镜像回滚标签 `rollback-20261111-152729` / `rollback-20261111-152834`；前端 `/opt/web/dist.bak.*`。GitHub 运行配置保持启用不受镜像回滚影响。

## 升级步骤

1. 获取 `v0.40.0`。已有环境无需数据库迁移。
2. 如需启用 GitHub OAuth：按部署环境示例设置四变量，重建 identity-service。
3. 前端走宿主 dist 发布；identity-service / admin-api 经 `scripts/deploy-update.sh` 交叉构建部署。
4. 验收：安全中心出现 MFA 二维码；资料页"第三方账号"绑定后显示"已绑定"。

## 验证

- Go：`go build ./...`、`go vet ./app/identity/...`、`go test ./app/identity/...`（server 7.4s、biz、data 12.6s、service、cmd 隔离回归）全部通过。
- Web：`tsc -b`、定向 ESLint、vitest 13 项、Playwright `security-qr.spec.ts` 桌面/移动 × 中英 4 项通过。
- OCR（v1.13.7，high）：本批增量 0 P0/P1；预算一致性意见已采纳（写阶段 15s→5s），2 条低优先级意见按最简原则不采纳（无 error 重定向路径、provider 列表单一来源暂未合并）。
- 生产冒烟（13:28/15:29 部署后）：identity-service / admin-api 新镜像运行，console 200，`/v1/oauth/providers` 含 github，`/api/user/self` 未授权 401，日志无 error/panic。
- 遗留单项：真实手机验证器扫码、恢复码与丢设备恢复、首批敏感操作验收、两个全局强制策略的启用窗口，见 [N3 记录](../runbooks/n3-mfa-auth-acceptance-2026-11-11.md)。

## 完整变更日志（v0.39.3 → v0.40.0）

- `fix(backup): skip scheduled slots already recorded`（本版随 admin-api 部署）
- `docs(billing): align peak calendar with statutory holidays`
- `docs(billing): record glm-5.2 orphan price cleanup`
- `docs(plan): plan production acceptance after v0.39.3`
- `docs(n1): record pricing verification and acceptance`
- `docs(backup): record N2 offsite recovery and daily acceptance`
- `docs(backup): close N2 acceptance after second daily slot`
- `docs(n3): sync docs index with N3 acceptance progress`
- `chore(rbac): refresh reviewed source facts after identity http changes`
- `chore(security): stop flagging the synthetic QR fixture as a leaked secret`
- `fix(identity): complete oauth binds beyond the request deadline`
- `feat(identity): report bound oauth providers on user self`
- `feat(web): bind MFA with a QR code in the security center`
- `feat(web): show third-party account bound state on profile`
- `fix(deploy): forward github oauth variables through compose`
- `docs(n3): record GitHub OAuth and MFA acceptance`
