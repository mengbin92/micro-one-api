# A2：MFA 与 step-up 实施契约

2026-10-27（北京时间）。关联 [执行计划](next-stage-plan-2026-10-26.md)。状态：已部署生产并通过线上验证；强制策略开关关闭；真实外部因子验收保留；见 [验收记录](../runbooks/a2-security-acceptance-2026-10-27.md) 与 [生产证据](../runbooks/evidence/a2-production-deployment-2026-10-27.json)。

## 冻结的首批入口

| HTTP / BFF | RPC / 业务执行点 | owner | 要求 |
| --- | --- | --- | --- |
| `/api/user/login`、OAuth callback | `IdentityService.Login`、`OAuthLogin` | Identity | 已启用 TOTP 时仅返回 5 分钟 challenge；验证后签发原 IAM JWT |
| `/api/user/security/*` | `IdentityService` 的 Security RPC | Identity | 同一 usecase；设置需当前密码或已有 MFA proof；启用、禁用、换恢复码推进账号 epoch、撤销旧会话 |
| `PUT /api/user/self`（密码/用户名）、`/api/oauth/email/bind` | `UpdateSelf`、`UpdateSelfEmail` | Identity | 短时 step-up；保留现有当前密码/邮件验证 |
| `PUT /api/user`（管理端密码/邮箱） | `IdentityService.UpdateUser` | Identity | 当前 operator proof；保留目标用户权限、CAS 与审计 |
| `/api/user/token`、`POST /api/token[/]` | `CreateAccessToken` | Identity | 返回新秘密前 step-up；已有 Token 只显示摘要，无明文导出 |
| 渠道详情/导出/密钥操作 | `channel.channel.secret.read`、`channel.channel.export`、`channel.channel.secret.rotate` | Channel → Identity | owner 固定执行点调用 Identity 校验；可选秘密字段失败时脱敏，不能吞掉错误后泄露 |
| 兑换码查看/导出 | `billing.redemption.read`、`billing.redemption.list`、`billing.redemption.export` | Billing → Identity | owner 固定执行点校验 |
| IAM 管理写操作 | `IAMGovernanceUsecase.Execute` 中全部实际 write（不含 preview） | Identity | 现有 CAS、scope、审计之外增加 step-up |

step-up 证明保存于共享 Redis，绑定现有 JWT 的 JTI、用户、密码 epoch 和 MFA revision，5 分钟到期；不建立新的登录会话系统。owner 通过既有 `GetResourceAuthorization` 读取并校验该证明，不信任 BFF 的“已验证”标记。IAM 模式同时检查当前会话撤销状态。

## 状态与存储

- TOTP：RFC 6238，SHA-1 / 6 位 / 30 秒，容差 ±1 步；全账号 SQL 原子消费时间步，登录与 step-up 共用重放水位。
- 登录 challenge 复用 A1 Redis `ChallengeRepo` 的 TTL、限次和单次消费；凭据签名限制为专用 audience/type，不能被当成会话。
- MFA Secret 与待确认 Secret 使用独立 `IDENTITY_MFA_ENCRYPTION_KEY`（16/24/32 字节）AES-GCM 加密；恢复码为 128-bit 随机值，只存 SHA-256，明文仅生成响应展示一次。
- SQL 状态通过既有 IAM policy 锁、事务与审计修改；三库新增 identity owner 迁移，不自动迁移生产。
- 失败次数在共享 SQL 中提交，5 次失败锁定 5 分钟，不能通过新 challenge/跨实例重置；依赖不可用时 fail closed。
- 丢设备：密码/OAuth 首步后输入一次性恢复码登录，安全中心用剩余恢复码或 TOTP 获取证明，再禁用/重新设置。不提供邮件绕过 MFA；设备和恢复码全部丢失时不提供自助解除；既有 root rescue 仅重置密码，不能解除 MFA，线下恢复须另行核验和授权。

## 分阶段策略与边界

已启用 MFA 的登录检查始终生效。`IDENTITY_STEP_UP_ENABLED` 控制首批敏感操作强制二次验证，默认关闭便于先部署兼容代码再启用；开启后无 MFA 用户用当前密码获取证明；OAuth-only 账号需要重新 OAuth 登录取得新 JTI 的短期证明。有 MFA 用户只能用 TOTP/恢复码。`IDENTITY_MFA_REQUIRE_ADMIN` 进一步要求首批管理员操作先完成 MFA 设置；不自动为管理员生成 Secret。策略变化不自动发布 IAM 权限。

暂留：Passkey、支付回调、系统/worker 专用凭据、公开邮件恢复的第二因子、除上述清单外的其他 owner 写操作。密码恢复保留 MFA 绑定，不能绕过下次登录的 MFA。外部 OAuth 真实回调、生产 MFA enrollment 和生产策略启用另记验证状态。

## 交付与回滚

受影响：Identity、Admin BFF、Web；Channel/Billing 继续通过既有 owner 授权 RPC 校验。先备份再执行三库相应迁移，配置独立加密密钥，部署 Identity/Admin/Web，最后逐步启用策略。MFA 启用后不可回滚到忽略 MFA 的旧 Identity 镜像；可关闭强制管理员/step-up 策略，但保留 MFA 登录验证及加密密钥。

## 验证记录

- [x] TOTP 向量、时间步/恢复码/challenge 并发重放、限次与密钥错误。
- [x] 三库真实迁移、跨实例 SQL/Redis、epoch 与会话撤销、证明过期/跨会话隔离。
- [x] BFF / owner HTTP / gRPC、密码/OAuth 登录与 Web 安全中心。
- [x] 适用构建、契约、前端与验收门禁。
- [x] 生产部署：2026-10-27 11:30 CST 更新 Identity/Admin/Web，迁移 143 落 `oneapi_identity`，公网验证通过；其余服务未重建。
- [ ] 真实外部 OAuth、实际手机验证器、生产用户 enrollment（不以本地验证替代）。
- [ ] 强制 step-up / 管理员 MFA 策略分阶段启用（本次明确保持关闭）。
