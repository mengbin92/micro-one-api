# A2 最近三次提交复核与修复

2026-10-27（北京时间）。审查范围固定为 `512a0a9f`、`6f5cb293`、`b4c787a4`，包含调用方、存储、HTTP/gRPC、Web 会话状态与部署配置。按生产多副本、共享 SQL/Redis、浏览器多标签页的实际运行方式审查。

**结论：发现并修复 8 项问题，无未修复的已确认问题。** 本轮修复已于 2026-10-27 14:01 CST 部署 Identity/Admin/Web，源码 `e56fd37d`，见[复核部署证据](evidence/a2-review-production-deployment-2026-10-27.json)；未 push、未发布版本/tag。[首次生产证据](evidence/a2-production-deployment-2026-10-27.json)保留对应 `6f5cb293` 的历史记录。

修复提交：`9e66d6fe`（部署配置）、`35384d2c`（MFA 状态机）、`fdb760d0`（BFF 错误透传）、`241e1b66`（Web 会话隔离）。

后续生产登录暴露新表运行权限遗漏，属于本次部署核验盲点；已补齐最小权限并部署 Identity `e1da0017`，真实管理员登录恢复，见[故障修复记录](a2-login-permission-hotfix-2026-10-27.md)。此前匿名验收不包含已认证登录，不能据此判断密码链路可用。

## 已修复问题

| 状态 / 级别 / 严重度 | 位置 | 触发条件与影响 | 修复与回归 |
| --- | --- | --- | --- |
| ✅ P1 / 高 | [Lite Compose:106](../../deployments/docker-compose/docker-compose.lite.yml)、[PostgreSQL Compose:101](../../deployments/docker-compose/docker-compose.postgres.yml) | 两份独立环境列表遗漏 MFA 三变量；即使 `.env` 开启强制策略，容器仍按默认关闭运行，密钥也不生效 | 补齐注入；部署门禁用非默认密钥/开关检查全部 Compose 变体，修复前在 Lite 报缺失，修复后全部通过 |
| ✅ P2 / 中 | [Admin HTTP:4404](../../app/admin/internal/server/http.go) | 下游返回二次验证错误，BFF 只保留普通 403，Web 无法触发验证或识别需要绑定 MFA | 共享响应函数仅保留两个安全 reason；测试真实 gRPC status 序列化往返，同时确认普通权限 reason 不外露 |
| ✅ P2 / 中 | [MFA 登录:145](../../app/identity/internal/biz/security.go) | 持有已消费但 JWT 尚未到期的 challenge，重复提交错误因子五次，仍会累计 SQL 失败次数并锁住账号 | 因子计算前只读检查 challenge 活跃状态，成功路径仍原子消费；回归确认旧 challenge 不改变计数，新 challenge 可继续登录 |
| ✅ P2 / 中 | [MFA 设置:429](../../app/identity/internal/biz/security.go) | 旧验证器曾使用当前或下一时间步，禁用后立即换新 Secret，新 Secret 继承旧重放水位，正确验证码被拒绝 | 仅在生成新 Secret 时重置水位；完整禁用→重新设置→确认回归通过，原重放测试保留 |
| ✅ P2 / 中 | [登录结果:62、175](../../web/src/pages/LoginPage.tsx) | 离开 MFA/OAuth 登录页面，或另一标签页换会话后，旧请求完成会覆盖当前 token 并跳转 | 流程代次与原 token 同时匹配才写入；OAuth 清理取消请求；分别测试卸载和页面保留时的换会话，以及 StrictMode/错误恢复 |
| ✅ P2 / 中 | [语言切换:221](../../web/src/pages/LoginPage.tsx) | MFA challenge 已从地址栏清理，切换语言重建路由会丢失内存 challenge，用户被迫重做首步 | MFA 或登录请求期间禁用语言切换，返回登录后恢复；challenge 继续仅保存在内存 |
| ✅ P2 / 中 | [验证对话框:47](../../web/src/components/SecurityVerificationDialog.tsx)、[请求发送:48](../../web/src/lib/api.ts)、[验证协调:11](../../web/src/lib/security-verification.ts) | 为会话 A 打开的验证弹窗，在切换为 B 后会将 A 的因子发给 B；不同会话还可能共用同一等待结果 | 请求、promise 与对话框均绑定原 token，发送前拒绝不匹配凭据；旧结果不能完成新请求，跨会话测试通过 |
| ✅ P3 / 低 | [生产证据:22](evidence/a2-production-deployment-2026-10-27.json)、[验收记录](a2-security-acceptance-2026-10-27.md) | 新证据包含真实域名、内部账号名和占位备份路径；401/405 混写不能证明鉴权 | 删除真实地址/账号，现场核实并填回准确备份路径；逐条记录正确方法返回的 401，以及无效 challenge 的 400 |

关键修复片段（完整上下文与错误处理见上表源码）：

```yaml
- IDENTITY_MFA_ENCRYPTION_KEY=${IDENTITY_MFA_ENCRYPTION_KEY:-}
- IDENTITY_STEP_UP_ENABLED=${IDENTITY_STEP_UP_ENABLED:-false}
- IDENTITY_MFA_REQUIRE_ADMIN=${IDENTITY_MFA_REQUIRE_ADMIN:-false}
```

```go
if reason := kerrors.FromError(err).Reason; reason == "STEP_UP_REQUIRED" || reason == "MFA_ENROLLMENT_REQUIRED" {
    payload["reason"] = reason
}
// 仅在生成新的待确认 Secret 时：
s.LastStep = -1
```

```ts
// 请求已显式绑定会话时，在 Axios 发送阶段再次检查。
if (config.headers.Authorization && !matchesCurrentSession(config)) {
  throw new Error('Session changed before request');
}
// 登录响应写入 token 前：
if (!isCurrent()) return;
```

challenge 的 `Check` 不改变次数或消费状态；最终 `Consume` 仍由 Redis 原子执行，SQL 写路径继续使用既有 IAM policy 锁。未调整资金逻辑、迁移、账号权限或强制策略默认值。失败尝试的审计继续使用 `*.failed_attempt` 与 `verified=false`，`Result=success` 表示限次状态成功提交，符合原事务约定。

## 验证

| 状态 | 验证 | 证据 |
| --- | --- | --- |
| ✅ | `go build ./...`、`go vet ./...` | 通过 |
| ✅ | Identity biz/data/server/cmd 全套及认证专项 race | 通过；覆盖跨副本消费、旧 challenge、重绑定与 Check 的 pending/错误 hash/只读/过期/依赖失败 |
| ✅ | `make verify` | 全部通过；630 项 Web 测试、191 项 critical、仓库 race、906 条 RBAC 契约、迁移/架构/生成类型与构建门禁 |
| ✅ | `scripts/check-deployment-docs.sh` | 四组 Compose、42 个 K8s 对象、85 个引用及本地文档链接通过 |
| ✅ | 真实桌面/移动 Chrome A2 | 两端均通过：设置、恢复码保存、MFA 登录、step-up、创建 Token、换码和禁用；实际 Web 构建产物 → BFF → Identity HTTP/gRPC → 隔离 SQL/Redis |
| ✅ | 复核修复生产部署 | Identity/Admin 镜像与本机清单一致、healthz 通过、重启 0；82 个 Web 文件及公网 index 匹配；桌面/移动登录页、无效 MFA 拒绝、语言切换保护和恢复通过；14 个非目标运行容器未变 |

本轮未重新执行外部 MySQL/PostgreSQL 用例（未提供本机一次性 DSN），未使用真实外部 OAuth/手机验证器或生产账号 enrollment。生产两个强制开关仍为关闭；后续安排真实外部因子及分阶段策略验收。已有用户启用 MFA 时仍禁止回滚到忽略 MFA 的旧 Identity。
