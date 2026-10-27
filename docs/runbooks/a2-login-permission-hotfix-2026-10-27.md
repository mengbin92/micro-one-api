# A2 管理员登录故障修复

2026-10-27（北京时间）。状态：**已恢复，应用修复已部署**。详见[脱敏证据](evidence/a2-login-permission-hotfix-2026-10-27.json)。

## 根因与复现

管理员使用原凭据登录时，公网返回 HTTP 200、`success=false`、`message=invalid credentials`。此时账号启用、密码未变；A2 在签发会话前读取 `identity_mfa`，但迁移 143 创建该表后遗漏了 Identity 运行账号的逐表权限。

使用运行时同一 DB 凭据读取 `users`、`iam_policy_state` 成功，读取 `identity_mfa` 返回 MySQL 1142 / SQLSTATE 42000。应用将这个 IAM 存储依赖错误也统一转换为凭据错误，掩盖了真实原因。本机隔离测试使用完整存储权限，未覆盖生产逐表授权；先前 healthz、匿名拒绝和静态页面验收不能证明登录成功。该权限核验是部署遗漏。

## 修复

| 时间 | 操作 | 边界 |
| --- | --- | --- |
| 14:25 CST | 通过既有 MySQL 管理 socket，仅给实际 Identity 运行账号增加 `oneapi_identity.identity_mfa` 的 `SELECT / INSERT / UPDATE` | 备份原/新 grants，原权限保留；不授予全库或 DDL 权限，不解锁账号、不改用户密码或业务行 |
| 14:29 CST | 部署 Identity `e1da0017` | 本机 Linux/amd64 构建；已知 IAM/MFA/challenge 存储故障返回固定脱敏 503；错误密码、未知用户继续返回统一凭据错误 |
| 14:32 CST | 完成运行与真实浏览器登录验证 | 镜像 ID/源码摘要匹配，healthz 通过，restart_count=0；MFA 密钥与两个关闭的强制策略保留 |

Identity 镜像 `docker-compose-identity-service:deploy-20261027-142840-e1da0017`，ID `sha256:78e7d5a5d1e640eeec8102b8e72090d80af5c35f0971ba8152a5e4116c50aa86`，源码摘要 `4d8ebe5226f9…`。校验使用镜像本身的源码标签；既有 Compose 容器标签可能覆盖镜像标签，不能将其作为编译来源。

Admin 与 Web 保持前次 `e56fd37d` 构建；未新增迁移、未更改注册策略或开启强制 MFA。本次仅修复既有能力，若独立发版判为 **PATCH**；本轮没有 push 或发布版本/tag。

## 验证

| 状态 | 检查 | 结果 |
| --- | --- | --- |
| ✅ | 原故障反馈环 | 同一授权凭据从 `success=false` 转为登录成功；未记录密码或 JWT |
| ✅ | 运行账号权限 | SELECT 成功；零行 INSERT/UPDATE 事务探测通过并回滚；业务行未变 |
| ✅ | 已认证读取 | `/api/user/self`、`/api/admin/access` 均 HTTP 200 且成功 |
| ✅ | 真实 Chrome 登录 | 登录表单→dashboard→管理员访问校验通过，无页面 JS 错误 |
| ✅ | 错误分类回归 | 三类包装依赖错误先复现错误的 200，再修复为 503；错误密码及未知用户响应保持不变，驱动详情不外露 |
| ✅ | 本地验证 | Identity 全包测试、Identity vet、906 条 RBAC 源码契约及 diff 检查通过；本机交叉构建成功 |

用于排障的授权凭据仅保存在权限受限的临时位置，验证后已删除；不进入源码、文档、日志或证据。未重置管理员密码，未用真实账号执行 MFA enrollment、换码或敏感业务写入。真实外部 OAuth、手机验证器和强制策略验收继续待办。

## 后续部署约束

1. 迁移创建新 owner 表后，先核对实际运行 DB 账号的逐表权限；迁移账号建表成功不代表运行账号可访问。
2. `identity_mfa` 只需 Identity 运行账号持有 `SELECT / INSERT / UPDATE`。通过原管理通道授予实际账号，不将全库权限或迁移通道注入业务容器。
3. 使用实际运行凭据在正确库/schema 执行以下无业务写入探测；所有语句须在同一连接，出现错误时停止部署。SQLite 不涉及 GRANT，仍可检查表可访问性。

```sql
BEGIN;
SELECT user_id FROM identity_mfa WHERE 1 = 0;
INSERT INTO identity_mfa (user_id, secret, pending_secret, pending_session, recovery_hashes)
SELECT id, '', '', '', '[]' FROM users WHERE 1 = 0;
UPDATE identity_mfa SET failures = failures WHERE 1 = 0;
ROLLBACK;
```

4. 最后用授权账号完成一次真实登录及只读管理员访问检查；healthz/匿名 401 只证明各自分支。

grants 前后快照与部署记录保存在原受限备份目录 `backups/backup-20261027_135752-a2-review`。Identity 镜像保留 `rollback-20261027-142840`；回滚应用仍保留新表权限、MFA 表、密钥和部署后的业务数据，不恢复旧 SQL/Redis 快照。
