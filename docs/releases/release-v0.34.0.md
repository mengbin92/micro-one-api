# Micro-One-API v0.34.0 发布：RBAC 权限管理与 IAM 正式交接

> 2026-10-06 · 上一版：[v0.33.6](./release-v0.33.6.md)（2026-09-30）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.0)

v0.34.0 是 v0.33.6 之后的 **MINOR 权限管理版本**：交付 RBAC 授权模型、角色与分配管理、服务专属身份和资源执行边界，以及从 legacy 到 IAM 的离线迁移、停写切换与恢复流程；同时修复本人用量统计、设计复审问题和发布门禁。

**涉及全部九个服务、前端、API/proto、三库迁移 110–123 及部署配置**。IAM 切换后管理权限来自服务端分配、会话和固定目录，旧数字角色、静态 ADMIN_TOKEN 与共享服务令牌不能替代 IAM 管理授权。本仓库生产已完成 IAM 交接和后续复审修复；其他部署须按执行手册独立升级，不能仅替换镜像。

## 修复与功能

### 1. RBAC 授权模型、事务与治理

**根因**：旧数字角色和共享令牌不能表达权限来源、资源范围、受限委派与会话激活，也不能保证授权变更和业务写入的一致性。

**修复**：引入固定权限目录、角色继承、allow/deny 来源与范围组合、角色/用户分配和委派约束、SSD/DSD 与成员数约束、会话/密码版本校验、策略 CAS 和原子成功审计。授权判断在业务 owner 执行，查询范围先于总数与分页应用，事务重试重新核验授权。

**影响服务**：`identity-service` 及所有资源 owner。详见 [设计与实施记录](../design/rbac/first-delivery.md)、[事务契约](../design/rbac/transaction-contract.md)。

### 2. 服务身份、资源权限与管理界面

**根因**：共享内部认证不能证明独立 caller，原管理入口和前端按钮缺少精确操作、对象范围与独立字段权限。

**修复**：九服务安装专属 outbound 身份与固定 caller 清单，覆盖用户、渠道/账号/模型/路由组、资金/订单/订阅、日志/配置/健康/通知的读写、导出、批量和复合动作；写入使用 owner CAS 与资源审计。新增角色、权限目录、分配、委派、约束、审计与本人会话角色管理界面，财务与秘密字段独立授权，脱敏模型编辑保留未获授权的价格。

**影响服务**：全部九服务和前端。见 [B 阶段执行边界](../design/rbac/b-execution-progress.md)、[C 阶段界面](../design/rbac/c-management-delivery.md)。新增迁移 110–122。

### 3. IAM 离线迁移与生产交接

**根因**：只切换 feature flag 无法证明旧写者退出、全量用户映射一致、财务记录完整与可恢复性。

**修复**：新增 `iam-migrate` 的清点、候选、shadow、停写、重建、签名 manifest、核验、activate/complete 和幂等 resume；绑定 batch、root 签名、源码摘要和最新 policy CAS。部署补齐 owner 的 DATABASE_DSN/SQL_DSN 一致性、独立数据库最小权限及默认关闭的救援配置。修复迁移 CLI JSON stdout 被 ORM 诊断污染、构建版本文件被排除，以及控制台默认 1 秒请求超时。

**影响服务**：全部九服务、迁移工具、Compose 与前端。生产于 2026-10-05 完成交接为 `iam/complete`，policy revision 7、catalog revision 2，旧共享远程 root 通道已撤权锁定。见 [D 阶段交付](../design/rbac/d-cutover-delivery.md)、[生产交接](../design/rbac/d-iam-production-deployment.md)。

### 4. 权限治理复审与本人用量修复

**根因**：设计复审发现 deny 期限扩权、载荷借用撤销权、委派遗漏、隐藏来源读取、非法范围、归档复活和前端 gate 差异；IAM 专属调用表还遗漏 identity 读取本人账本及按日统计的能力。

**修复**：闭合上述治理与资源边界，root 摘要仅使用真实显式 grant；补齐 identity → billing 本人用量调用，billing 继续验证会话与本人范围。资源版本和兑换码 ID 拒绝 int64 溢出。权限目录新增说明/排序字段及展示界面，对应 identity 所属 `123_add_iam_permission_metadata` 三库迁移。

**影响服务**：全部九服务和前端，重点为 identity、billing、admin。见 [14 项设计复审](../design/rbac/design-review-2026-10-05.md)、[本人用量修复](../design/rbac/self-usage-fix-2026-10-05.md)、[复审生产更新](../design/rbac/review-production-deployment-2026-10-05.md)。

### 5. 全历史扫描与浏览器发布门禁

**根因**：main 定时 Security 使用 `--all --full-history`，识别到了 develop 历史中四个孤立集成测试的固定 JWT 签名字符串；push 的增量扫描通过不能证明定时扫描已修复。通用 Playwright 又误收集需要 `RBAC_C_FIXTURE` 的真实 IAM 专用测试，原 smoke 夹具未提供新的服务端授权摘要，路由验收仍只配置共享服务令牌，无法完成新的 owner 能力探针。

**修复**：仅豁免已逐条核对的历史 commit/file/rule/line 指纹；新凭证继续扫描。通用 smoke 与专用真实 IAM 配置分别收集各自场景，smoke 显式提供服务端 legacy 授权摘要，普通用户与管理用户分别建模，并等待异步授权完成后打开手机菜单。路由验收使用隔离生成的专属服务身份与固定 receiver map，能力探针按真实 relay caller 调用；离线 routing-backfill 也使用 identity 专属 caller，停写期间读取 channel 事实不依赖已停止的 identity 服务；失败只报告脱敏 gRPC 状态码。

**影响范围**：Security/Nightly/Release 验证流程与测试夹具。前端 axios 更新至 1.20.0，并完成 gosec 的信任边界复审。

### 6. legacy 本人认证与 Lite/PostgreSQL owner 连接

**根因**：完整 Nightly 揭示 legacy 摘要验证了 JWT 和数据库用户，却返回空的 session 主体，导致本人订阅购买错误返回 401；Lite/PostgreSQL 的六个 owner 未配置 identity 地址，授权客户端回退 localhost，SQLite 创建渠道返回 503。

**修复**：legacy 摘要仅投影已验证的会话 ID、用户 ID、上下文和到期时间，不持久化 IAM 会话、不激活角色或增加 grant。Lite/PostgreSQL 按现有 MySQL 拓扑补齐 owner 的 identity 地址，模板门禁拒绝遗漏。

**影响服务**：`identity-service` 的 legacy 本人认证；Lite/PostgreSQL 的 channel、billing、config、log、monitor、notify。真实 admin → identity RPC → 本人购买 HTTP 回归由 401 变为正常输入校验，同时无效和密码 epoch 撤销的会话仍拒绝；三套 Compose 渲染验证通过。

### 7. Responses 续接的固定服务调用能力

**根因**：绑定的 Responses 会话重新验证路由源时，Relay 适配器先用分组 key 读取路由事实，再调用 CheckRoute；固定服务 caller 表遗漏 Relay 的 ListRoutingGroups 能力，新专属身份下续接失败并返回 500。

**修复**：将 Relay 明确列入只读路由事实 RPC 的系统 caller，保留实际源权限复核和其他 caller 拒绝。使用真实 Relay 适配器、gRPC 凭证和固定 caller 拦截器的回归先复现 PermissionDenied，修复后通过，并覆盖源撤权与无关 caller 拒绝。

**影响服务**：`channel-service` 的固定 caller 表，以及 `relay-gateway` 的 Responses/SSE/WebSocket 续接路径。没有向普通用户新增权限或跳过源校验。

## 兼容性说明

- 新增 IAM API/proto、权限目录元数据及资源 CAS 字段。IAM 管理写入需提供变更原因、当前版本和对应精确权限；服务调用使用专属身份和固定 caller。
- 新增 SQLite/MySQL/PostgreSQL 110–123 迁移，按 owner 清单应用。普通服务不得持有 DDL、共享 root 或身份授权列的跨 owner 写权限；billing 财务列权限须独立核准。
- 兼容部署先在 legacy 状态完成配置、迁移和验收，再执行停写交接。IAM complete 后不得回退旧 legacy 二进制或历史角色回填；仅使用已验证的 IAM 兼容镜像和前端回滚。
- 权限目录 description/sort 只影响展示；新增 operation 不自动进入 root 或历史内置角色的 grant。

## 升级步骤

1. 备份 owner 数据库、Compose/.env、九服务 IAM 兼容回滚镜像和前端；按迁移 ownership 应用 110–123，核对实际最小数据库权限。
2. 配置九个独立 `SERVICE_CALLER_TOKENS`/outbound 服务身份、各 owner DSN 和接收方固定 caller map，保持救援关闭。按 [IAM 交接执行手册](../runbooks/rbac-iam-cutover.md) 完成预检。
3. 本机交叉构建并运输 `linux/amd64` 九服务与离线工具，服务器不构建。前端独立发布 `/opt/web/dist`；替换 admin 镜像不能更新挂载的静态资源。
4. 尚在 legacy 的部署按执行手册准备签名证据、停写屏障、事务排空、旧进程/DB 通道撤权与财务核验，执行 block → rebuild/verify → activate → complete 后恢复入口。已经 iam/complete 的部署只核对版本、迁移和事实，不重新切换。
5. 核对 root 管理、普通用户本人用量、错误 caller/共享令牌拒绝、九服务健康、前端和财务摘要；撤销临时迁移/DDL 通道。

本仓库生产已在 2026-10-05 发布九服务 `rbac-review-42b21ad4` 和前端、应用 123 迁移，IAM 状态保持 complete。本次 GitHub 发版归档这些既有结果，并纳入本轮 CI 发现的 legacy 认证和 Lite/PostgreSQL 连接修复；这些追加兼容修复由本轮回归验收，未作为已部署生产的新结果。生产证据见上述交接及复审记录。

## 验证

- 最终源代码 `develop@3808a85b` 的 [CI](https://github.com/mengbin92/micro-one-api/actions/runs/37328283978) 全部 25 个 job 通过，包括后端单元/race、真实集成、前端 lint/test/build、MySQL/PostgreSQL 迁移、部署文档检查及九服务 linux/amd64 + linux/arm64 共 18 个镜像构建。
- 最终源代码 `develop@3808a85b` 的 [Security Pipeline](https://github.com/mengbin92/micro-one-api/actions/runs/37328283747) 通过：gosec、govulncheck、Gitleaks、Trivy、CodeQL、许可证与 SBOM。
- 本次全历史 Gitleaks 复现四条测试误报；加入精确历史指纹后，使用与失败 CI 相同的 Gitleaks 8.24.3 扫描 1022 个提交、约 24.96 MB，零发现。
- 本次 SQLite 订阅购买/续期幂等、单连接兼容及事务授权重试定向测试重复十次通过。
- 真实 HTTP 超时回归：临时恢复旧 1 秒期限时，约 1.01 秒返回 500/context deadline exceeded；恢复 develop 的 30 秒期限后返回 200。原 main 购买失败发生于约 1.04 秒，与此期限故障模式吻合；原日志未上传服务内部错误，不能仅凭时长断言其底层数据库错误。
- 本次修正后的完整桌面/手机浏览器 smoke：65 项通过，1 项仅适用于手机的桌面条件跳过；修改文件 ESLint 通过。
- 隔离服务身份独立性/固定 receiver map 核验与路由测试辅助、传输/服务身份测试通过。三套 Compose 的 owner identity 地址渲染核验通过。
- legacy 本人购买真实 HTTP/RPC、回复 DTO 转换及密码撤销回归先失败后通过；IAM snapshot/切换及管理入口专项 race、identity biz/service、admin service 和 IAM DTO 全包测试通过。
- SQLite/MySQL 的原订阅购买、续期、撤销与结算/换购/退款路径已在 [路由验证](https://github.com/mengbin92/micro-one-api/actions/runs/37325231523) 中通过；该轮暴露的后续 Responses caller 缺项已另有先失败后通过的 RPC/race 回归。
- 最终 [Nightly E2E](https://github.com/mengbin92/micro-one-api/actions/runs/37328306697) 的 SQLite/MySQL 路由全阶段与浏览器门禁通过：包含订阅生命周期、Responses/SSE/WebSocket 续接、绑定源撤权、raw 协议、Redis 故障/恢复、能力缺失与创建回滚；Compose 套件也通过，最终 Nightly 四个 job 全部成功。
- 已归档的 RBAC 三库 race、真实 HTTP/gRPC、7 组 IAM 浏览器场景、迁移/财务隔离及生产检查见各阶段交付记录；本次不将历史验收计作新执行结果。

## 完整变更日志

- `951f1686` docs(rbac): add permission management design and implementation plan
- `c91ff89c` feat(rbac): add P0 and A1 authorization contracts
- `b9fd7629` feat(rbac): add A2 IAM storage and transaction foundations
- `39883ca7` feat(rbac): add A3 constraint evaluation and atomic changes
- `2b16355e` docs(rbac): record legacy production update after A3
- `109bea87` docs(relay): record two-token orchestrator canary
- `e2ab6e1a` docs(evidence): add shareable production transaction report
- `d7427dff` fix(rbac): scope contract inventory reads to repository root
- `c4ded5e9` feat(rbac): add A4 sessions and atomic account writes
- `26a53182` feat(rbac): add A5 governance and A6 management APIs
- `1ad40bc5` docs(rbac): record A4-A6 legacy production update
- `a133642e` feat(rbac): add service identities and user execution boundaries
- `29009419` feat(rbac): enforce B2-B4 resource owner permission slices
- `8edcf96e` fix(deps): upgrade axios to 1.20.0 to resolve CVE alerts
- `0501b7d1` chore(sec): annotate justified gosec findings with #nosec
- `3dd4736d` chore(rbac): re-review SOURCE digests for nosec annotations
- `62963348` docs(rbac): record production owner endpoint recovery
- `c8958d1b` feat(rbac): complete B0-B4 resource permission execution
- `9d755058` fix(rbac): reject overflowing resource versions and redemption IDs
- `feb4a7fc` feat(rbac): complete C1-C3 permission management UI
- `b1c2ae3d` feat(rbac): implement D0 migration and cutover rehearsal
- `f82b6009` fix(rbac): complete production IAM handoff and deployment verification
- `5a3ae532` fix(rbac): restore self usage through dedicated identity caller
- `7969a3cb` fix(rbac): close governance and permission management gaps
- `88651bba` docs(rbac): record verified production review rollout
- `0372ad6c` fix(ci): exempt reviewed historical IAM test signing fixtures
- `a7b3692a` fix(e2e): restore shared browser gate after IAM introduction
- `88629402` fix(e2e): provision dedicated callers in routing acceptance
- `ce27b504` fix(deploy): wire owner authorization endpoints in Lite and PostgreSQL
- `bc24a11e` fix(identity): preserve verified self principal before IAM cutover
- `3f250dad` fix(e2e): authenticate offline routing backfill as identity
- `a43c4d49` fix(routing): permit relay routing fact reads for stored responses
- docs(release): v0.34.0
