# C1：Passkey 无密码登录

2026-11-12 建档，2026-11-20 补验（北京时间）。**生产 RP 已启用，公网挑战烟测通过，真实设备验收待补。** [当前路线图](../design/next-stage-plan-2026-11-08.md) · [生产部署](../releases/release-v0.46.3.md#验证)

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
| ✅ | 生产 RP 配置及挑战烟测（2026-11-20） | 以已核对的 HTTPS 控制台 Origin 与主机名设置 `IDENTITY_PASSKEY_RP_ID` / `IDENTITY_PASSKEY_ORIGIN`，随 v0.46.3 同次重启 Identity。公网 begin 返回 200，RP ID、challenge 和 ceremony 均校验通过。 |
| ⏳ | 真实设备注册、登录与吊销 | 仍需维护者用桌面和移动设备完成下列旅程。 |

11 月 20 日按维护者决定保留「待验收」。生产探测验证 RP 与挑战，尚缺可与桌面和移动测试对应的注册、登录及吊销审计。

## 待维护者验收项

1. 在桌面和移动浏览器通过安全中心注册临时 passkey，退出后使用真实设备登录。
2. 吊销临时凭据并确认旧凭据无法再次登录，核对第二因子及恢复码路径。

## 回滚

恢复 11 月 20 日部署前的 Identity Compose 配置可撤回 RP 启用。迁移 149 为纯新增表，回滚保留该表和已有凭据记录。 [v0.46.3 回滚边界](../releases/release-v0.46.3.md#升级步骤)
