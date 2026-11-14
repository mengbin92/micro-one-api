# N3：账号登录、GitHub OAuth 与 MFA 扫码绑定

2026-11-11，北京时间。关联 [下一阶段计划](../design/next-stage-plan-2026-11-08.md)、[A2 契约](../design/a2-security-contract-2026-10-27.md)。状态：**已完成**。生产管理员密码登录、GitHub 真实 OAuth 登录、实际手机验证器扫码 enrollment、恢复码轮换与消费、BFF/直接 owner 无效证明拒绝及两个全局强制策略均已验收；证据见本记录及 [GitHub 登录证据](evidence/n3-github-oauth-login-2026-11-11.json)。N3 不再保留执行缺口。

## 本轮范围与决定

- 维护者要求先完成账号及真实 OAuth 两项测试，指定生产 `admin`，随后选择 GitHub，并确认需要新建 OAuth App。
- MFA 设置增加二维码扫码绑定；复用已有 `otpauth_uri` 和 `qrcode.react`，二维码在浏览器本地生成，保留手动密钥与验证器链接。
- MFA 真实手机、恢复码及首批敏感操作验收仍按原 N3 清单推进；两个全局强制策略当时保持关闭，后续已于 2026-11-11 21:03 启用并完成验收。

## 生产账号验证

11 月 11 日 08:22:55 起，经部署环境中的 SSH 目标，在生产主机通过 Admin BFF 的 loopback HTTP 入口执行。登录使用运行配置中已有的凭据，仅在远端内存使用；完整响应、密码及 JWT 不进入证据。脱敏结果见 [JSON 证据](evidence/n3-auth-preflight-2026-11-11.json)。

| 状态 | 检查 | 结果 |
| --- | --- | --- |
| ✅ | `POST /api/user/login` | HTTP 200，`success=true`，签发完整 session，不是 MFA challenge |
| ✅ | `GET /api/user/self` | HTTP 200、成功；ID 1、用户名 `admin`、role 100、status 1 |
| ✅ | `GET /api/admin/access` | HTTP 200、成功 |
| ✅ | `GET /api/user/security` | 密码可用、MFA 密钥已配置；账号 MFA 未启用、恢复码 0，两个强制策略关闭 |
| ✅ | 测试会话清理 | 沿 IAM `DELETE /api/user/session` 携本会话 revision 撤销；该会话再次读取本人返回 401。未调用会撤销全部会话的旧 logout 入口 |
| ⚠️ | 浏览器边界 | 现场 Chrome 可见已登录的管理员个人资料和安全中心；本轮密码请求证据来自生产 BFF HTTP，不将其记为新的浏览器表单登录验收 |

上述结果只证明原有密码登录与读取链路，不代表生产 MFA enrollment 或真实手机因子通过。

## GitHub OAuth 条件与部署配置

`GET /v1/oauth/providers` HTTP 200，列表为空。运行 Identity 环境及两份部署环境文件均未配置 GitHub 的启用值、Client ID、Client Secret 或 OAuth 回调基址。维护者已选择新建 OAuth App；初始状态为真实外部回调及 OAuth-only 登录待验证，后续已完成真实授权登录验收。

应用配置的确定值：名称 `micro-one-api`；Homepage 为现有控制台 HTTPS origin；Authorization callback URL 为 `<控制台 origin>/v1/oauth/github/callback`。`OAUTH_BASE_URL` 使用同一控制台 origin，不附带路径或末尾斜杠。应用 Client ID/Secret 通过受限配置文件交接，证据不保存其值。已在维护者浏览器准备应用注册表，保持精确回调、关闭 wildcard/device flow，并保留默认 token 过期策略；注册及 Secret 生成交由维护者本人完成。维护者随后确认注册完成并保存凭据；已核对本机交接文件为本人所有、普通文件、权限 0600，四项配置齐全且回调基址一致。校验只输出存在性与布尔结果，未输出 Client ID/Secret。

同时发现三套 Compose 只列显式容器环境，遗漏了 GitHub OAuth 的四项变量；宿主 `.env` 即使填写也不会进入 Identity。已补普通、Lite、PostgreSQL 的四变量透传和对应环境示例，并扩展原部署门禁：

| 状态 | 验证 | 结果 |
| --- | --- | --- |
| ✅ | 修复前的原 `validate_compose` 函数 | 三套渲染均失败：`identity-service must receive OAUTH_BASE_URL` |
| ✅ | 修复后相同函数 | 普通、Lite、PostgreSQL、测试 overlay 四组合通过，非默认 GitHub 值逐项一致 |
| ✅ | 默认渲染 | 三套 GitHub 仍关闭，Client ID/Secret 为空，回调基址保留原本机默认 |
| ✅ | 脚本静态检查 | `bash -n scripts/check-deployment-docs.sh` 通过 |
| ⚠️ | 完整部署文档门禁 | 本机缺 `kustomize`，未完整执行；聚焦验证直接提取原函数，不复制或替换断言 |

这是确定的部署传参遗漏，原 Compose 渲染已提供秒级红绿反馈，没有添加运行时插桩。11 月 11 日已对生产现有 Compose 和实际 `.env` 精确合并四项配置，按维护者选定的 GitHub 路径启用；仓库示例的默认关闭状态不变。

## GitHub 生产启用与当前阻塞

11 月 11 日 08:53–08:55，候选通过原配置/运行容器漂移核对、全配置差异断言和文件/容器 CAS 后生效。沿用现有镜像 `e0a115eaae44…`，仅以 `--no-deps --force-recreate --no-build --pull never` 重建 Identity；生效前已备份 Compose、环境和私密运行快照，并为原镜像打回滚标签。原只读 CA 证书挂载、MFA 密钥、两个关闭的强制策略及其他项目容器均保持一致。回执见 [GitHub 启用证据](evidence/n3-github-enablement-2026-11-11.json)。

| 状态 | 现场检查 | 结果 |
| --- | --- | --- |
| ✅ | 公网 `/v1/oauth/providers` | HTTP 200，包含 `github` |
| ✅ | 公网 `/v1/oauth/github/authorize` | HTTP 302，目标为 GitHub 授权入口；Client ID、控制台回调匹配，scope 仅 `read:user user:email`，state 非空；未记录完整 Location、cookie、code/state |
| ✅ | Identity 健康/日志 | owner health 通过；最近 3 行日志未见 error/fatal/panic |
| ✅ | 绑定前只读核对 | 原 Identity 运行 DB 账号读取：ID 1 的 GitHub 绑定数为 0；未读取或记录外部身份 ID |
| ✅ | 真实 GitHub 绑定 | 修复部署后 12:22 维护者重新授权，回调 302 跳 `/profile?oauth_bind=success`，绑定落库 `user_oauth_identities`（user 1 / github）且 `account.oauth.bind` 审计 success，见 [回调记录](evidence/n3-github-callback-2026-11-11.json) |

维护者已明确确认新增 GitHub 登录身份并发起真实绑定；第一次失败后 09:36:15 只读回查：绑定行数、成功绑定审计和新账号审计均为 0。第二次用新 state 复现相同错误。直接用未绑定身份走登录入口会新建普通用户，且可能触发注册奖励，不作为本轮默认路径；OAuth-only 分支需另有适用账号。

### 回调超时根因与修复（10:20 已部署）

现场记录显示，绑定事务首条查询仅耗时 0.418ms 就返回 `context deadline exceeded`，说明进入绑定时请求 context 已失效，不能把该耗时当成存储锁等待；外部交换已返回身份。隔离真实 SQLite/IAM、Redis 和 HTTP/BFF 回归另验证：继承 Kratos 默认 1 秒预算时，1.2 秒成功 provider 会得到同样的错误和零写入；显式 30 秒预算后正常返回 302、绑定/成功审计各 1。现有记录没有保留每次真实 GitHub 外部调用的完整计时，三次各 10 秒只是客户端理论上限，不能据此认定现场实际用满了 30 秒或推断 Go 读超时的传播机制。

已部署方案为 Identity 显式 30 秒请求预算、25 秒外部交换，以及交换成功后保留凭证 context value 的 15 秒独立写阶段。其约定是用户同意且身份交换成功后，即使原调用 context 失效，也有界地完成绑定/登录。IAM 在写事务内重验账号 epoch、有效期和会话撤销；绑定唯一约束、审计与状态同事务，注册奖励按既有 request ID 去重。本次评审按这一约定复核，不把有界持久化本身认定为漏洞。
10:20 交叉构建镜像 `deploy-20261111-102036-a874a563`（含此修复与 30s 显式请求超时），沿用 `scripts/deploy-update.sh identity-service` 重建 Identity，回滚锚点 `rollback-20261111-102036`；冒烟：`/v1/oauth/providers` 200 含 github、authorize 302、健康日志无错误。OCR v1.13.7 high 复核本修复与部署脚本：0 发现。后续 12:22 的真实绑定已回读成功；完整的新 OAuth 登录/会话旅程随后已完成验收。

回滚锚点为 `$DEPLOY_REMOTE_DIR/docker-compose/backups/n3-github-20261111-085312-6140d176`，内含 `compose.original.yml` / `env.original`；镜像标签 `docker-compose-identity-service:rollback-20261111-085312-n3-github`。恢复前需核对没有并发配置变更，仅恢复本轮文件并重建 Identity，不恢复 SQL/Redis，不自动撤销后续真实绑定。

## 绑定状态回显（13:26 前端 / 13:28 Identity 已部署）

真实绑定成功后维护者反馈个人资料页无显示。核查：绑定数据真实落库（identities 表 + 审计），但 `/api/user/self` 的 `userToMap` 不返回任何 OAuth 字段，资料页"第三方账号"卡片只渲染常显"绑定"按钮——回显缺口为既有 UI 缺失，非绑定失败。补齐（最小面）：self 接口增加 `oauth_providers`（初版按已注册 provider 查询；本轮评审已纠正其错误处理与漏显范围，见下节）；资料页按 `oauth_providers` 显示"已绑定"禁用态并消费 `?oauth_bind=success` 弹成功提示、清 URL；后端 `TestSelfListsBoundOAuthProviders`、前端组件用例覆盖。OCR 复核本批 4 条意见已随修复落地（history.state/hash 保留、文案分隔、locale 覆盖顺序）；另有 2 条 check-deployment-docs.sh 意见（门禁仅覆盖 GitHub 四变量、K8s 清单不透传 OAUTH_*）属上一轮候选范围，未扩大处理，留维护者决定。回滚锚点：前端 `/opt/web/dist.bak.*`、Identity 镜像 `rollback-20261111-132752`。

## 二维码绑定制品与验收

安全中心只在已完成当前身份验证、取得待确认设置后显示二维码。使用同一个 `setup.otpauth_uri` 编码；白底黑码并保留 quiet zone，窄屏可缩放；取消或启用完成时随设置状态清除。没有新增接口、存储或依赖。

| 状态 | 验证 | 结果 |
| --- | --- | --- |
| ✅ | SecurityCenter / Profile 组件回归 | 本轮评审后 10 项通过；覆盖显示、取消及确认后清理，保留手动入口 |
| ✅ | 定向 ESLint / `tsc -b` | 通过 |
| ✅ | `npx playwright test e2e/security-qr.spec.ts` | 桌面、移动 Chrome 共 2 项通过；暗色模式白底黑码、无横向溢出、取消清理和无外部请求 |
| ✅ | 生产前端制品 | 本轮只读核对公网 index 与 Profile chunk 均与本机 13:26 制品摘要一致；Profile chunk 已包含二维码实现 |
| ✅ | 真实手机扫码 | 维护者已用实际手机验证器完成扫码 enrollment，生产回读 secret 已置位、恢复码 10、审计 confirm/login 均 success；见下方退出条件与证据 |
| ✅ | 首轮 QR/配置评审（历史） | 独立 ponytail-review 未发现新增问题；OCR v1.13.7 完成 7/7 文件、失败 0、P0/P1 为 0，仅 1 条可选共享 QR wrapper 建议，按最简原则不采纳；详见 [评审摘要](evidence/n3-code-review-2026-11-11.json) |

浏览器用例使用公开的合成测试密钥，并关闭 trace、video、screenshot；不连接生产，不采集真实验证器秘密或恢复码。

## 后续退出条件

- [x] 指定生产账号密码登录及管理员读取验收。
- [x] 核对真实 OAuth 配置条件，确认 GitHub 及新建应用决定。
- [x] 二维码实现、组件及桌面/移动工程验证。
- [x] 新建 GitHub OAuth App 并通过 0600 文件安全交接四项配置。
- [x] 核对生产候选差异、回调及 GitHub 受控启用；仅重建 Identity，原镜像及 MFA 状态保留。
- [x] 真实 GitHub 授权、完整 callback/session 交接及账号归属回读：维护者浏览器实测，登录页 GitHub 入口 → `?login&oauth=1` 弹窗 → 回调 → 控制台 dashboard 已登录，见 [证据](evidence/n3-github-oauth-login-2026-11-11.json)。
- [x] 二维码及 GitHub 配置候选自审和 OCR。
- [x] 13:26 前端制品已发布；本轮公网 index/Profile chunk 摘要一致，二维码已包含。
- [x] 实际手机扫码 enrollment：维护者用真实手机验证器完成扫码启用；生产只读回读 `identity_mfa`（user 1）secret 已置位、pending 已清空、恢复码 10、revision 2、无锁定，审计 `credential.mfa.setup success`（前两次 setup failure 为维护者重试）、`credential.mfa.confirm success`、`credential.mfa.login success`。
- [x] BFF/直接 owner 无效证明拒绝验收（两阶段全绿）；首批敏感操作清单与丢设备恢复边界已定稿（见上节）。
- [x] 恢复码真实消费验证：维护者完成轮换（rotate 成功，含一次失败重试）并用 1 个恢复码真实登录成功，生产回读余量 10→9、审计 `credential.mfa.recovery/step_up/login` 齐全；当前生效码组为最后一次轮换所示，剩 9 个。
- [x] 两个全局策略已于 2026-11-11 21:03 生产启用（step-up + 管理员 MFA 强制），维护者完成手动验收，回滚锚点 `.env.bak.20261111-210317`。

既有 Identity 与前端部署按上述时间记录保留；本轮工作区评审修复已提交（v0.40.0 窗口），identity-service 与 admin-api 已分别于 15:27/15:28 重建部署，前端制品 15:29 更新，tag v0.40.0 指向修复后 main。后续实际手机 MFA 绑定、恢复码消费及两个全局强制策略启用均已验收，N3 不再保留未完成项。


## 当前工作区评审修复

| 级别 | 可复现问题 | 修复与验证 |
| --- | --- | --- |
| P2 / 读取一致性 | 绑定仓库返回超时，self 仍 HTTP 200 且空列表，误导用户为未绑定 | biz 仅跳过明确不存在，真实错误向上传递；self 返回 503。HTTP 回归先红后绿 |
| P2 / 状态漏显 | 提供方停用后已有绑定消失；仍可用于 OAuth 登录的原 User 绑定字段也未回显 | 查询全部已支持提供方，合并原 User 绑定来源；回归覆盖停用、原有绑定及其他用户隔离 |
| P3 / 证据准确性 | 初始失败响应与后来成功标记混列、已发布 QR 仍记为待部署，且外部调用理论上限被写成现场实测 | 分离初始失败和后续成功，按公开制品摘要回填部署状态，并回填后续真实手机扫描/OAuth 会话结果，移除不必要的外部身份 ID |

受影响 biz/server/cmd 三包测试通过，前端 10 项定向测试、ESLint、类型检查及差异格式检查通过。收口：make test 与 make test-race 全绿；本轮 OCR 3 条意见已处理（写阶段预算收紧至 5s，另两条按最简原则不采纳）；全历史 gitleaks detect 0 发现。本轮评审未操作生产写路径。

## 无效证明拒绝验收（2026-11-11 续）

第一阶段（无会话/无效输入）：BFF（admin-api loopback :3000）与直接 owner（identity-service :8001 loopback）两侧一致拒绝——安全状态/verify/confirm/disable/recovery 无会话均 401；伪造 challenge 的 MFA 登录均 400 `MFA_INVALID`。详见 [证据](evidence/n3-mfa-rejection-p1-2026-11-11.json)。

第二阶段（有效会话，待维护者提供实时 TOTP）：脚本已备于生产 `/tmp/n3-reject-p2.sh`，覆盖：MFA 登录先拒无效码再受理有效码、无 step-up 证明时 disable/rotate 拒绝、confirm 伪码拒绝、verify 错误码拒绝、直接 owner 同会话复测、收尾撤销会话并验证 401。完成后连同恢复码消费（丢设备路径）与敏感操作清单回填本记录。

## 第二阶段拒绝验收与会话清理边界（2026-11-11 晚）

以真实 TOTP 通过 MFA 登录取得有效会话后，BFF 与直接 owner 双侧一致拒绝无效证明：disable/rotate 无 step-up 证明 → 403 `STEP_UP_REQUIRED`；confirm 伪码 → 400 `MFA_INVALID`；verify 错码 → 400 `MFA_INVALID`；MFA 登录先拒无效码（challenge 保留）再受理有效码；status 回读 enabled、恢复码 10、两个全局策略关闭。详见 [证据](evidence/n3-mfa-rejection-p2-2026-11-11.json)。

会话清理中发现并确认的边界（代码佐证）：
- `DELETE /api/user/session`（RevokeOwnSession）**只撤销调用会话本身**，`global` 仅区分会话/上下文粒度；必须携带 `expected_revision`（会话 revision）与 `reason`。
- 管理端「撤销用户全部会话」（`RevokeUserSessions`）对操作者本人返回 `ErrIAMProtected`（`credentialScope` 拒绝 uid==actor），是防自我接管的设计；IAM 页面对自己执行必然失败属预期。
- 因此**无自助"撤销我的全部会话"入口**；他人会话由管理端处理，自己的其余会话只能逐个持令牌撤销。
- 本次遗留 2 个测试会话（令牌从未落盘、shell 已退出），12h 会话 TTL 内自然失效；MFA 状态未受影响（secret 置位、恢复码 10、计数 3 未锁）。锁定/挑战/step-up 证明 TTL 均为 5 分钟。

## 敏感操作与丢设备恢复边界（定稿）

step-up 强制面（`RequireStepUp` 代码确认，当前策略关闭时仅 MFA 自我管理始终强制）：修改密码/用户名、修改本人邮箱、创建 API 令牌、MFA setup/confirm/disable/recovery 轮换、IAM 授权变更、托管用户改邮箱/密码、备份安全选项更新、邮箱验证码消费。

丢设备恢复路径：MFA 挑战与 step-up 的 `consumeMFA` 均接受 32 位恢复码（消费一个）→ 用任一恢复码登录 → 立即重新扫码注册新设备（setup/confirm 各需一次 step-up，恢复码可用）→ 轮换出新 10 个恢复码。**恢复码耗尽且设备丢失 = 无自助路径**，仅剩服务认证救援适配（RescueRootCredential）或运维手工清理 `identity_mfa`；建议恢复码低于 3 个时立即轮换。

## 全局策略启用（2026-11-11 21:03，维护者拍板）

生产 `docker-compose/.env` 两个开关置 `true`（备份 `.env.bak.20261111-210317`），仅重建 identity-service，容器内确认变量生效、healthz 正常。启用后维护者完成控制台侧手动验收（含敏感操作 step-up 流程）；服务端只读侧证：启用后首次真实 MFA 登录成功（21:14:34）、恢复码 10、无锁定、失败计数归零。详见 [证据](evidence/n3-policy-enable-2026-11-11.json)。

回滚路径：同文件两开关改回 `false` 并重建 identity-service（渲染+容器注入管道与启用完全一致）。维护者决定不再做 false 全量演练，避免无谓的生产抖动；以 `.env.bak.20261111-210317` 为回滚锚点。
