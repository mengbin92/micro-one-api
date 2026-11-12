# C1：Passkey 无密码登录

2026-11-12，北京时间。关联 [下一阶段计划](../design/next-stage-plan-2026-11-08.md)。状态：**工程验证与生产部署完成；真实设备验收待 RP 配置后执行**。

## 执行摘要

Passkey（WebAuthn）注册/登录随 v0.41.0 发布（identity `dd347f03`、web `777d9fbd`、rbac `cf0e7318`），复用安全中心做凭据管理。功能保持暗开关：未配置 `IDENTITY_PASSKEY_RP_ID` / `IDENTITY_PASSKEY_ORIGIN` 时 ceremony 返回 503 `MFA_UNAVAILABLE`（内部 reason 不外泄）。

## 验收结果

| 状态 | 检查项 | 结果 |
|------|--------|------|
| ✅ | 单元/集成门禁 | identity 包测试、全仓 `make test-unit`（126 包）、`make test-race`、`make migration-check` 全绿 |
| ✅ | 生产迁移 149 | 运行账号 DML-only 且 root@'%' 锁定，按 147/148 先例用本地 root socket 手工落 DDL；`identity_passkey` 表结构与迁移文件一致 |
| ✅ | schema_migrations | 已登记 `149_create_identity_passkey` |
| ✅ | 运行账号授权 | `iamd_identity_*` 获 `identity_passkey` 的 SELECT/INSERT/UPDATE/DELETE；模板沉淀为 `migrations/manual/149_passkey_runtime_grants.example.sql` |
| ✅ | 生产部署 | identity-service v0.41.0 运行中，日志无权限报错 |
| ✅ | 暗开关行为 | `POST /api/user/security/passkey/login/begin` → 503 `MFA_UNAVAILABLE`（非 404，路由存在） |
| ⏳ | 真实设备 enrollment/登录 | 需先配置 `IDENTITY_PASSKEY_RP_ID` / `IDENTITY_PASSKEY_ORIGIN` 并重启 identity-service，再由维护者用真实设备/浏览器验收 |

## 待维护者验收项

1. 配置 RP ID 与 Origin（生产域名），`docker compose up -d --no-deps --force-recreate identity-service`。
2. 安全中心注册 passkey → 退出 → passkey 登录全旅程；覆盖桌面 + 移动浏览器。
3. 吊销后旧凭据不可再用；恢复码路径不回归。

## 回滚

服务器已打 rollback 锚点（rollback-20261112-131942）；迁移 149 为纯新增表，回滚代码即恢复暗开关，无需删表。
