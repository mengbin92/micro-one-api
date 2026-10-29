# 第一轮 A1–A4 完整代码审查与修复

2026-10-29（北京时间）。关联[下一阶段排期](../design/next-stage-plan-2026-10-26.md)。审查源码范围为 `918b56c9..89bdddd6`，包含 A1–A4 实现、后续热修、调用方、测试、配置和迁移；按多副本 SQL/Redis、并发结算及浏览器多标签页使用方式核查。

本轮确认并修复 **12 项问题**。修复保留在隔离工作目录，未提交、未发布或部署；工程修复不改变原计划登记的生产状态和外部验收条件。

## 1. 发现与修复

| 编号 / 状态 / 级别 | 位置 | 触发条件与影响 | 修复与回归 |
| --- | --- | --- | --- |
| 1 / ✅ P1 / 高 | [Identity account update:1040](../../app/identity/internal/biz/auth.go#L1040) | legacy 管理员改邮箱的 step-up 只在事务外检查；等待 policy 写锁时证明到期或会话被撤销，仍可写入邮箱 | 复用 `mutateLegacyAccountChecked`，在同一锁内检查 operator 状态、会话 epoch 和证明；`TestLegacyManagedEmailRechecksSecurityUnderWriteLock` 两例先红后绿 |
| 2 / ✅ P2 / 中 | [Identity RPC login:148](../../app/identity/internal/service/identity.go#L148) | IAM/MFA/challenge 依赖故障在 gRPC Login 中被返回成密码错误，遗漏此前 HTTP 错误分流 | 返回脱敏 `Unavailable`，错误密码仍返回原业务结果；`TestLoginDistinguishesSecurityDependenciesFromCredentials` 三类故障先红后绿 |
| 3 / ✅ P3 / 低 | [Recovery worker:87](../../app/identity/internal/biz/challenge.go#L87) | 异步恢复邮件 worker 缺少 panic 隔离；注入依赖 panic 会终止进程 | 增加脱敏 recover，保留槽位释放和 context 取消；`TestRecoveryPanicReleasesWorkerSlot` 从进程 panic 转为通过 |
| 4 / ✅ P1 / 高 | [Chat settlement:204](../../internal/server/http_chat_handler.go#L204)、[provider evidence:150](../../domain/upstream/provider/provider.go#L150) | Chat JSON/SSE 的 typed decode 将 null cache/total/details 转成零，错误地认定用量证据有效并收费 | 仅在内存保留 raw usage，复用严格数字检查，无效证据释放预扣；`TestServiceTierChatRejectsUnprovenUsage` 与 streaming matrix 新增六个先红后绿用例 |
| 5 / ✅ P1 / 高 | [Chat streaming tier:323](../../internal/server/http_chat_handler.go#L323) | SSE 先给出 `service_tier:null`，后给出 default，原逻辑丢弃 null 证据并恢复较低档位 | 保留 raw tier，复用不可恢复的 unknown 观察状态和冻结最高价策略；streaming matrix 的 null→default 回归先红后绿 |
| 6 / ✅ P1 / 高 | [Reservation replay:16](../../app/billing/internal/biz/request_attempt.go#L16) | 无 source trace 的调用复用 request ID 并更换 channel，可取得另一来源的冻结价格 | 档位 reservation 重放始终核对 channel/account；`TestServiceTier` 中无 trace 跨来源重放先红后绿 |
| 7 / ✅ P1 / 高 | [System option key:44](../../app/admin/internal/biz/system_options_authorization.go#L44) | SQL 不区分大小写/重音等价时，别名 key 可绕过档位配置的采购权限、JSON 校验和 revision | 共享读写入口拒绝档位大小写别名及非 ASCII key；现有 key 均为字母、数字、下划线。SQLite NOCASE 存储回归覆盖读、Set、Mutate、SetMany，以及尾空格、重音、零宽输入；先红后绿 |
| 8 / ✅ P2 / 中 | [Password reset:48](../../web/src/pages/PasswordRecoveryPage.tsx#L48) | 重置请求未结束时另一标签页登录新会话；迟到成功响应清除新 token | 只清除请求发起时的原会话，仍清空恢复草稿；同页 replacement-session 回归先红后绿 |
| 9 / ✅ P1 / 高 | [Security center:26](../../web/src/components/SecurityCenter.tsx#L26) | 离开安全中心后，迟到恢复码响应仍向全局弹窗发布秘密并清除会话 | 动作绑定原 token 和组件生命周期，step-up 后重试也复核；状态查询纳入 protected cache 和取消信号。卸载后迟到响应回归先红后绿 |
| 10 / ✅ P1 / 高 | [Security dialog:28](../../web/src/components/SecurityVerificationDialog.tsx#L28) | 全局弹窗位于路由外，换账号后仍保留原账号恢复码或已输入的密码/因子 | 在既有 storage/authorization 事件中清除旧完成弹窗，拒绝旧验证并清输入；正常注销后恢复码仍可一次展示。三项跨会话回归先红后绿 |
| 11 / ✅ P1 / 高 | [Order details:210](../../web/src/pages/OrdersPage.tsx#L210) | 关闭详情后迟到响应重新打开弹窗；同 token 撤销管理权限后，本地详情/二维码仍显示 | selection 和详情响应绑定 authorization generation，旧 token 响应拒绝；只更新仍选中的订单，刷新期间阻止重复操作。关闭弹窗回归先红后绿，权限撤销与迟到响应回归通过 |
| 12 / ✅ P2 / 中 | [Recharge queries:82](../../web/src/pages/RechargePage.tsx#L82) | 换会话只清 protected cache，Native 完整订单仍残留；离开页面后查询继续 | 支付渠道和订单查询使用 `meta.protected` 及 React Query signal；旧账号缓存清理、离页取消回归先红后绿 |

关键修复方式均复用仓库已有边界：事务内 verifier、typed error、raw evidence 检查、reservation 来源约束、protected query、会话和授权代次。未增加依赖、迁移或公开配置开关；未手改生成物。

## 2. 验证

| 状态 | 验证 | 结果 |
| --- | --- | --- |
| ✅ | `make test-unit`、`go vet ./...` | 通过；使用仓库既有单元门禁排除需预先启动环境的 E2E suite |
| ✅ | `make test-race`、Identity 认证专项 race | 通过；资金和认证修复另有针对性回归 |
| ✅ | 三库真实存储与 owner 链路 `-race` | SQLite/MySQL/PostgreSQL，3 包、8 个顶层测试、63 个叶子场景，0 失败、0 跳过；A3 owner 链路三库各 14 例，含配置权限、快照、异步恢复及支付资金/认领并发 |
| ✅ | Web `npm test -- --run` | 95 文件、666 例通过 |
| ✅ | `npm run test:critical` | 16 文件、196 例通过；行覆盖 93.34%，分支覆盖 85.99% |
| ✅ | eslint、`npx tsc -b --noEmit` | 通过 |
| ✅ | RBAC、architecture、migration、IAM browser checker、format | 915 条 RBAC 契约通过；四处源码摘要和对应测试说明已回填；迁移检查仅保留已有 allowlist 提示 |
| ✅ | `scripts/check-deployment-docs.sh` | 四组 Compose、42 个 K8s 对象、85 个资源引用及本地文档链接通过 |
| ✅ | 桌面/移动 Chrome | A1/A3/A4 既有用例 20/20 通过；源码 Vite 服务，非生产构建/CSP 验收。已实际检查移动注册表单与桌面 Native 二维码截图，无溢出 |
| ✅ | 外部 OCR high | session `bc260d77-3e2e-444e-b76f-6f482ec049e9` 取得有效结论：2 条 low（证据 JSON schema 命名/类型一致性，不影响行为，不改）；对 planning 超时未覆盖的 `http_chat_handler.go` / `provider.go` 补 `ocr scan`，命中项全部为预存量代码，本轮 diff 无 P0–P2 |
| ✅ | 两次独立宿主复核 | 连通代码与修复 diff 复核完成；第二次按 `ocr delegate rule` 导出规则覆盖全部 12 个生产文件，无新增确认问题。与外部 OCR 状态分别登记 |

完整 `go test ./...` 的第一次尝试包含未启动环境的 `test/e2e/suite`，因本机服务未监听而失败；其余当时失败项为修复中的红色回归与预期的 RBAC source drift，已分别修复并由最终 `make test-unit` 验证。没有把该次命令记为通过。

临时三库容器、匿名卷及凭据文件已清理，仅保留脱敏日志。三库日志为本机 `/tmp/round-one-review-three-db-20261029.log`；各专项原始输出和结构化结果见[本轮证据](evidence/round-one-review-2026-10-29.json)。

## 3. 兼容性与未执行项

- 配置 key 现在只接受 ASCII 字母、数字、下划线；仓库现有 key 均满足。别名和带符号 key 会明确拒绝，不提供兼容映射。
- 未重新执行真实 SMTP 收件、Turnstile、外部 OAuth/验证器 enrollment、真实商户付款或上游付费计价探测；沿用原验收记录的外部保留项。
- 遵守 no-auto-build，未单独执行 Go/前端生产构建或完整 `make verify`。本轮浏览器采用源码服务，开发预置脚本需要 bypass CSP，因此不作为生产 dist/CSP 验收；A2 未重跑真实浏览器，认证 owner 链及组件专项已验证。
- 未更改生产强制认证/计价/支付开关，未进行生产部署或新版本发布。A3 仍未完整上线，第一轮工程交付与真实外部验收分开登记。

审阅本轮 diff 后按认证、计价和 Web 三组提交。形成部署候选时补生产构建和对应浏览器/CSP 验收，再按现有顺序安排受影响服务上线。当前分支为 `codex/round-one-review`。

## 4. 预存量观察项（OCR scan 命中，非本轮引入，登记待办）

| 级别 | 位置 | 问题 | 建议 |
| --- | --- | --- | --- |
| P1 | domain/upstream/provider/provider.go | `PROVIDER_DISABLE_SSRF_CHECK=true` 全局静默关闭 SSRF 校验（进程内所有 provider，含 dial 时），无告警无环境门禁 | 启动时强告警或要求二次显式开关；审计同类 `*_DISABLE_*` 变量 |
| P2 | domain/upstream/provider/provider.go:442,567 | 非流式非 2xx 分支用 128MB 成功上限缓冲错误 body（应为 1MB `MaxUpstreamErrorBody`），恶意上游可放大内存 | 两个分支改用错误上限；抽公共 helper 统一 body 上限选择 |
| P3 | internal/server/http_chat_handler.go | 流式后置错误策略不统一（commit 失败、StreamError 终止、InvalidPayload 丢弃依赖生产者未文档化约定），客户端收不到终止错误事件 | 定义统一 commit 失败策略；SSE 终止前下发错误事件；为 InvalidPayload 增加显式断言 |
