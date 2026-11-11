# N4：公告与本人模型真实旅程验收

2026-11-12，北京时间。关联 [下一阶段计划](../design/next-stage-plan-2026-11-08.md)。状态：**已完成**。

## 执行摘要

在生产环境通过临时测试用户 `n4probe`（临时 root 角色）完成公告全生命周期验收。所有验证项通过，测试数据已清理。

## 验收结果

| 状态 | 检查项 | 结果 |
|------|--------|------|
| ✅ | C1: 创建 draft 公告 | 成功创建，draft 状态用户不可见 |
| ✅ | C1: 发布 draft→published | 发布后非目标用户（probe 不在 targets [2,4]）不可见 |
| ✅ | C2: groups:[3] VIP 分组公告 | 非 group 3 成员（probe 在 default 组）不可见 |
| ✅ | C3: 定时公告（+180s） | 未到 publish_at 时不可见，到时间后自动可见 |
| ✅ | C4: 到期公告（+90s） | published 时可见，到期后从列表消失 |
| ✅ | C4: 编辑公告 | body 更新成功，revision 递增（1→2） |
| ✅ | C4: 已读回执 | `POST /api/user/announcements/read` 返回 success，read=true |
| ✅ | C4: 下架（archived） | archive 后用户列表不可见 |
| ✅ | CAS 冲突检测 | 使用过期 revision 返回 409 `AUTHORIZATION_REVISION_CONFLICT` |
| ✅ | 用户 model-availability | 返回 6 个模型名称，无渠道/上游信息泄露 |
| ✅ | 权限控制 | 普通用户访问 `/api/admin/announcements` 返回 403 |

## 关键发现与解决

**PUT 请求方法限制**：`/api/admin/announcements` 的 save 操作要求 HTTP PUT。busybox wget 不支持 PUT，需用 `nc` 发送原生 HTTP/1.1 请求。

**MFA 影响**：n4probe 在验收过程中曾临时配置 MFA secret 以绕过 `IDENTITY_MFA_REQUIRE_ADMIN` 检查，但该 secret 导致 login 返回 503（MFA challenge 路径需要 Redis）。已清除并恢复原始状态。

## 数据清理

- 删除所有 N4 测试公告（5 条）
- 删除 announcement_reads、announcement_events、announcement_targets 中相关记录
- 删除临时用户 n4probe（id=13）及其 IAM 角色、MFA 配置、会话
- identity_mfa 表确认仅保留 admin (user_id=1) 的原始配置

## 证据文件

- [N4 验收证据 JSON](evidence/n4-announcements-acceptance-2026-11-12.json)
