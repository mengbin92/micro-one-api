# Micro-One-API v0.37.0 发布：用户自助认证、服务档位计价与微信 Native 充值

> 2026-10-29 · 上一版：[v0.36.6](./release-v0.36.6.md)（2026-10-26）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.37.0)

v0.37.0 是 v0.36.6 之后的 **MINOR 功能版本**：集中发布第一轮 A1–A4，包括注册与邮件恢复、TOTP/恢复码与首批敏感操作 step-up、有限范围的 `service_tier` 计价、微信 Native 余额充值，以及全轮复核发现的 12 项修复。新增公开契约、认证状态和三库迁移 143–145，不能仅按最后一批缺陷修复判为 PATCH。

**发布代码不等于启用收费或完成生产升级。** 强制 step-up、管理员强制 MFA、服务档位计价和微信新单继续按原配置关闭。A1/A2 与 A4 的既有生产验收继续有效；A3 仅有部分兼容代码与 Billing 144 迁移随 A4 上线，Relay/Log 尚未完成 A3 升级、Log 145 尚未迁移。本轮 12 项修复尚未部署，最终发布流水线和生产升级结果分别记录。

## 功能与修复

### 1. A1：完整注册、邮件验证与密码恢复

**根因**：此前自助链路缺少公开注册策略、BFF 代理、真实邮件投递与跨实例 challenge，页面无法按实际配置完成注册和恢复。

**新增与修复**：按配置展示邮箱、邀请码、验证码和 Turnstile；补齐中英文恢复页与路由。Identity 复用既有 SMTP 配置，通过共享 Redis 管理 TTL、冷却、限次、重发替换及原子单次消费；投递失败撤销 challenge。公开恢复在查询账号和投递前统一有界异步接受，避免响应暴露邮箱存在性；注册奖励幂等发放，密码重置后旧会话失效。本轮再为异步邮件 worker 增加脱敏 panic 隔离。

**影响服务**：`identity-service`、`admin-api`、Web。A1 已完成真实邮件收件与既有生产验收，Turnstile 保持关闭；本版未重新执行外部收件。见 [A1 实施验收](../runbooks/a1-self-service-acceptance-2026-10-27.md)。

### 2. A2：TOTP、恢复码和首批敏感操作二次验证

**根因**：账号登录缺少第二因子，现有会话不能证明敏感操作前曾重新验证；浏览器和直接 owner 路径需要统一执行认证状态机。

**新增与修复**：密码/OAuth 登录统一使用短期 MFA challenge，通过 TOTP 或一次性恢复码后才签发会话。安全中心支持设置、确认、换码和禁用；认证秘密加密存储，恢复码只保存不可逆摘要。五分钟证明绑定当前会话、密码 epoch 和 MFA revision，首批覆盖冻结清单内的密码/邮箱、秘密读取与导出、Token 创建和 IAM 高权限写操作。并发重放、恢复码消费和账号变化沿用 SQL/Redis 原子边界。

同时纳入此前 A2 复核修复：活跃 challenge 检查、新 Secret 重放水位、BFF 安全 reason 透传、OAuth 交接失败恢复与三套 Compose 配置注入。本轮将 legacy 管理员改邮箱的证明检查移入写锁，修复 gRPC 登录依赖故障被误报为密码错误，以及浏览器跨会话秘密残留。

**影响服务**：`identity-service`、`admin-api`、首批涉及的 owner 服务和 Web；新增 Identity 143 迁移。强制策略保持关闭；已启用 TOTP 的账号仍执行 MFA 登录，不能用关闭强制开关绕过。真实外部 OAuth、实际手机验证器与生产用户 enrollment 保留待验。见 [A2 契约](../design/a2-security-contract-2026-10-27.md)、[A2 验收](../runbooks/a2-security-acceptance-2026-10-27.md)及[运行账号权限修复](../runbooks/a2-login-permission-hotfix-2026-10-27.md)。

### 3. A3：单来源、单模型的服务档位计价 MVP

**根因**：请求档位可能与上游供应商实际执行档位不同，原价格与账单缺少独立观察、预扣上界和可重放的档位快照。

**新增与修复**：在有明确输出上限的 Chat Completions/Responses HTTP JSON 与 SSE 纯文本请求中，分别记录请求和实际档位。完整保存 default/priority/flex 五桶售价与可选采购估价；预扣覆盖逐桶最高价，接纳时冻结配置、来源和规则，结算沿用冻结快照。实际档位缺失、未知或冲突时保留证据缺口，按 `actual_or_max_v1` 最高价策略结算；usage 缺失、非法、取消或不完整流释放预扣，不用估算值冒充证据。

新增 PricingSnapshot v2、账本与日志解释字段，管理端以 revision 原子保存整个配置并同时校验售价和采购权限。本轮补齐 null 原始证据校验、无 trace 重放来源约束及配置别名拒绝；档位准入错误统一返回 400，不再误报为余额不足或影响来源健康。

**影响服务**：`relay-gateway`、`billing-service`、`log-service`、`admin-api`、`identity-service`、`config-service` 和 Web；新增 Billing 144、Log 145 迁移。默认关闭，真实来源、价格审阅与付费探测未完成。executor、WS、订阅来源、工具/媒体/外部会话状态不在本期支持范围，按契约明确拒绝。见 [A3 冻结契约](../design/a3-service-tier-contract-2026-10-27.md) / [A3 验收](../runbooks/a3-service-tier-acceptance-2026-10-27.md)。

### 4. A4：微信 Native 单商户 CNY 余额充值

**根因**：原自助充值依赖单一支付渠道，新增渠道需要复用资金 owner，完整覆盖验签、结果不确定、重试和幂等发放。

**新增与修复**：增加配置驱动渠道清单、Native 下单/查单/正式 BFF 回调和本地二维码展示。平台公钥模式验证签名、时间戳与加密报文，核对商户、AppID、订单、金额及币种。先持久化订单，超时或结果无法验证时保留 pending；同键重试用原单查单并有界恢复二维码，条件认领和 fencing token 防止迟到响应覆盖新结果。查单与通知共用行锁事务完成订单迁移、钱包和账本单次发放。

**影响服务**：`billing-service`、`identity-service`、`admin-api` 和 Web。复用现有支付表，无 A4 专属迁移；默认关闭，真实商户、付款和平台回调保留待验。仅余额充值，微信订阅购买明确拒绝；平台原路退款、拒付、多商户与其他支付模式不在范围。关闭新单仍保留存量查单和回调，排空前不能删除商户密钥。见 [A4 实施验收](../runbooks/a4-wechat-native-acceptance-2026-10-28.md)。

### 5. 第一轮完整复核的 12 项修复与工程加固

| 范围 | 修复 |
| --- | --- |
| Identity，3 项 | legacy 改邮箱在写锁内重验会话和 step-up；gRPC 依赖故障脱敏返回 Unavailable；异步恢复 worker 隔离 panic |
| 计价，4 项 | Chat JSON/SSE null usage 拒绝收费；null 档位不能被后续低价恢复；无 trace 重放仍绑定来源；拒绝配置大小写、尾空格和非 ASCII 别名绕权 |
| Web，5 项 | 迟到密码重置不清新会话；离开安全中心后不发布恢复码；全局安全弹窗随换会话清秘密和旧因子；订单详情绑定选择与授权代次；支付读取纳入 protected cache 和取消信号 |

详见 [完整审查与红绿回归](../runbooks/round-one-code-review-2026-10-29.md)。本次还包含既有 gosec 门禁修复、OAuth render 状态修复、证据扫描误报处理与满载机器 Web 测试等待调整。复核修复没有新增依赖、迁移或配置开关。

## 兼容性说明

- **API**：新增认证、安全中心、支付渠道及档位计价相关契约；未启用 MFA 的旧用户继续原登录，已启用 MFA 的账号首步返回 challenge，调用方须完成第二因子。首批 step-up 入口见冻结契约，不能据此声称所有写操作均已覆盖。
- **迁移与 owner**：MySQL、PostgreSQL、SQLite 均包含 `143_create_identity_mfa`（Identity 新表）、`144_add_billing_service_tier`（Billing 账本/价格快照扩展）、`145_add_log_service_tier`（Log 观察字段）。先迁移后升级；按各服务实际缺失迁移执行，不因生产部分已应用而全局跳过。历史账单金额不重算，旧 v1 快照继续可读，空档位字段保持历史未知。
- **运行权限**：迁移账号能建表不代表运行账号可读写。逐表授权环境须为实际 Identity 账号补齐 `identity_mfa` 的 `SELECT / INSERT / UPDATE`，并用运行凭据验证；不向业务容器授予 DDL 或全库管理权限。
- **配置**：A1 需共享 Redis、完整 SMTP 与公开控制台 base URL；MFA 加密密钥须独立、安全备份并在所有 Identity 副本保持一致。强制认证开关先保持 `false`；`ServiceTierPricing` 默认关闭，启用依赖 `BILLING_REQUEST_SNAPSHOT_V2=true`；`WECHAT_ENABLED=false` 保持默认，秘密由私有配置和只读文件注入。
- **配置 key**：系统选项只接受 ASCII 字母、数字、下划线，`ServiceTierPricing` 必须使用精确大小写；非法别名明确拒绝，不提供兼容映射。仓库现有 key 均满足此约束。
- **历史条件**：上游供应商证据监控继续停用；既有契约灰度、历史待核验账单、executor/Jaeger/TTL/WS 与暂停任务保留各自原边界，本版不自动扩大、恢复或判为通过。

## 升级步骤

1. 核对目标版本与受影响服务，备份 SQL、Redis、运行配置、MFA 密钥引用、商户材料及现有镜像；部署前为各目标镜像打回滚标签。秘密不得写进 Git、发布文档或测试证据。
2. 先应用各 owner 缺失的迁移：Identity 143、Billing 144、Log 145；核对实际运行 DB 账号对新表/字段的权限并验证重复迁移无操作。当前生产已登记 Identity 143 与 Billing 144，Log 145 仍待升级时执行；以现场迁移表核对为准。
3. 在本机或 CI 构建 Linux/amd64 镜像并上传，生产主机不构建。先更新全部 Billing/Log owner，再更新 Relay，随后 Identity/Admin/Config 与 Web；Identity 143 及密钥必须在新 Identity 启动前就绪。Relay 必须取得 Billing 档位协议版本与预扣回显，不能让旧 owner 静默忽略新字段。
4. 按宿主机挂载方式单独发布 `web/dist`；只换 Admin 镜像不会更新页面。对新增认证链路核验真实管理员登录、无会话拒绝、旧会话失效和正常业务读取；健康检查不能代替已认证登录验证。
5. 先保持强制认证、档位计价和微信关闭，验证基础认证、原支付和账单读取。真实因子/商户/上游供应商条件完成后，由维护者分别批准启用和扩面；不将源码发布当成收费启用。
6. 记录各服务镜像/源码摘要、迁移水位、重启次数和日志，完成正常与异常 owner/BFF 验收；生产升级结果单独留证，不能复用 10 月 27–28 日记录声称本版修复已上线。

### 回滚限制

- 一旦用户启用 MFA，**禁止回滚至忽略 MFA 的旧 Identity**；保留 `identity_mfa`、稳定加密密钥、运行权限和已推进的会话 epoch，不恢复旧 SQL/Redis 快照来重新启用会话。全部因子丢失后的恢复须另行核验授权，邮件重置和 root rescue 不绕过 MFA。
- 档位回滚前关闭新接纳并排空既有 reservation/异步结算，仍由能解释冻结快照的新 Billing 结清；保留新列、快照和历史金额，不以回滚重算账本。
- 微信回滚前停新单，排空或交接 pending 订单、查单和回调；不支持微信的旧 Billing 不能接管未完成订单。保留商户材料和资金记录，不通过删单回滚。

## 验证

| 状态 | 证据与边界 |
| --- | --- |
| ✅ | 第一轮修复的 `make test-unit`、`go vet ./...`、`make test-race` 与 Identity 专项 race 已通过；Web 95 文件/666 例、critical 16 文件/196 例、eslint 与 TypeScript 已通过。见[本轮审查证据](../runbooks/evidence/round-one-review-2026-10-29.json) |
| ✅ | 真实 SQLite/MySQL/PostgreSQL `-race`：3 包、8 个顶层测试、63 个叶子场景，0 失败、0 跳过；A3 owner 链路三库各 14 例，另含权限、快照、异步恢复及支付资金/认领并发。一次性容器和凭据已清理 |
| ✅ | 既有源码服务浏览器复核 A1/A3/A4 桌面/移动 Chrome 20/20 通过；不能替代生产 dist/CSP 验收。A1/A2/A4 的此前真实链路、收件或关闭态上线记录分别保留原日期 |
| ⏳ | 本版最终 `make verify`、Go/前端生产构建及构建产物浏览器/CSP 门禁：待发布流程拿到实际结果后补记，未将前次或源码服务结果代用 |
| ✅ | 外部 OCR high 已取有效结论（session `bc260d77-3e2e-444e-b76f-6f482ec049e9`）：2 条 low 均落在证据文档 schema，不影响行为；两个资金路径文件补扫命中项全部为预存量代码，登记至 runbook 待办。两次独立宿主复核结论不变 |
| ⏳ | 本版 tag 对应 CI、Release E2E、amd64 镜像及 GitHub Release：尚未执行，结果以本版 workflow 为准 |
| ⏳ | 本轮生产部署、A3 完整计价上线、真实外部认证因子、Turnstile、上游供应商付费计价及真实商户支付：保留待验，关闭开关不变 |

## 完整变更日志

以下列出 `v0.36.6..HEAD` 全部非 merge 提交，含第一轮复核修复与本版发布文档。

- `docs: plan authentication and billing capability improvements`
- `feat(identity): complete email registration and password recovery`
- `feat(identity): add TOTP recovery and sensitive-action step-up`
- `fix(deploy): complete identity authentication resource contracts`
- `docs(security): record A2 production deployment evidence`
- `fix(deploy): propagate MFA policy in all compose variants`
- `fix(identity): enforce active MFA challenges and fresh replay state`
- `fix(admin): preserve security verification reasons through BFF`
- `fix(web): bind MFA login and verification to their session`
- `docs(security): record A2 review fixes and correct deployment evidence`
- `docs(deploy): record A2 review fixes in production`
- `fix(identity): distinguish login dependency failures from credentials`
- `fix(web): recover login controls atomically after oauth handoff error`
- `docs(runbooks): record a2 login permission hotfix`
- `fix(ci): ignore a2 evidence image-tag false positive`
- `fix(web): stop deriving oauth handoff state from the url during renders`
- `feat(billing): deliver A3 service tier priced MVP`
- `fix(security): clear pinned gosec findings blocking pushes`
- `test(web): widen harness waits for loaded machines`
- `feat(billing): deliver A4 WeChat Native recharge`
- `fix(identity): recheck step-up under write lock and classify login errors`
- `fix(billing): reject unproven tier evidence and alias option keys`
- `fix(web): bind late responses to their session and lifecycle`
