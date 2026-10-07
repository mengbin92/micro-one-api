# C 阶段交付：授权导航、IAM 管理与既有页面

2026-10-03 · 工作分支 `codex/rbac-c-management` · 基线 `9d755058` · **C1–C3 已完成。**

对应 [实施方案第 6 节](../rbac-permission-management-implementation-plan.md#6-前端与完整管理产品)。实现、协议补充、入口契约和本机隔离验收一起交付；本轮没有部署、推送、发布或修改生产配置。生产继续沿用既有 legacy 授权。D0 影子比较、切换故障注入以及 D1 生产凭证核验/交接仍待执行。

## C1：授权查询与本人会话

- `GET /api/user/authorization` 返回主库同一快照内的 mode、服务端 legacy 管理标志、已分配角色元数据、启用且实际绑定的操作存在性摘要、可见菜单和版本。角色元数据不包含完整 grants 或隐藏继承关系。摘要使用 active allow 和全部有效分配的 mandatory deny；不能作为对象执行或委派凭证。
- legacy/idle 查询只读取主库用户及版本，不创建候选 IAM 会话，也不激活角色；JWT 数值角色不能替代数据库角色。blocked/verified 状态仍拒绝。真实本人 HTTP 测试验证 root/member、即时数据库角色变化、零新增 IAM 会话和 blocked 的 403。
- 前端 `authorization.ts` 提供 `can/canAll/canAny` 和共用受保护查询。查询 key 包含 platform context、凭证变更后的身份 generation 和授权版本；权限未知、查询失败或未激活时不发送受保护查询，手动 refetch 同样检查权限。
- AdminRoute 独立核对 console 和页面权限。没有总览权时进入首个可访问页面；导航、通知计数、健康轮询、详情/自动补全独立 gate。菜单 route/icon 仅使用注册白名单。管理入口不读取 JWT/localStorage 数值 role；自助订单页也改用授权快照选择管理或本人接口。
- 403 保留有效 JWT，先取消在途请求、移除受保护缓存再刷新 authorization，禁止查询重试；401 清理登录。角色激活、权限快照重取、版本变化和最早失效时间到达均清除旧受保护数据。刷新失败保持拒绝，authorization 错误不递归刷新。

> **后台续验修复（2026-10-07，本地已验、未部署）**：上条保留 C 阶段交付时点。后续普通后台轮询在摘要仍有效且权限/身份不变时保留展示与 owner read cache，避免整页卸载；写入/预检仍需要非 fetching 的实时授权状态。403、明确权限重验、失败、到期及身份/权限变化仍清旧数据，角色初始化页仅保留同一身份的本地冲突/选择状态。十项真实 owner 浏览器已通过，见 [诊断记录](../../runbooks/console-authorization-refresh-2026-10-07.md)。
- `/session-roles` 展示本人可激活角色、来源和强制 deny；提交 reason 与本人 session CAS。DSD 的 selection_required 会话先选择角色，不能进入普通业务；选择冲突由服务端拒绝，成功后重取授权并进入可访问首页。

## C2：完整 IAM 工作区

| 页面 | 交付行为 |
|---|---|
| 权限目录 | 分页/筛选、草稿、绑定/保护状态、固定支持范围、资料/启停/归档、引用关系；元数据不能扩大执行能力 |
| 角色与继承 | 草稿创建、资料/成员上限、启停/归档、复制为草稿；直接授权、继承来源、scope、allow 与局部 deny；模块树和资源×操作矩阵编辑同一状态；继承图与成员/引用查询 |
| 用户角色 | 多角色、开始/到期、分配边界、来源与版本；整批预检、撤销分配；独立会话列表与撤销 |
| 授权委派 | role、role_creation、user_credentials 三类；动作、目标用户范围、ceiling、期限与再委派；服务器保留创建来源，撤销不移除已有业务分配 |
| 职责分离 | SSD/DSD、角色集合、最大数量、启停与未来冲突预检 |
| 菜单管理 | 父菜单、顺序、启停、固定 route/icon、required_all/required_any；菜单不创造后端权限 |
| 有效权限与模拟 | 受管理范围约束的用户/对象解释、来源路径、deny、决策原因与版本；每个管理变更使用服务端模拟显示受影响用户、修改前后差异及未来冲突 |
| 授权审计 | 服务端范围过滤的分页/筛选和脱敏详情；独立 `iam.audit.export` 从受保护导出接口遍历分页下载，拒绝后无本地回退 |

IAM DTO 的 int64/uint64 ID 与版本保持字符串。所有治理写先填写原因，调用 `authorization:simulate`，保留实际审阅请求、目标 CAS、base policy 和内容摘要；提交使用同一份请求，后台再次验证。修改原因会清除旧预检；409 清除摘要、关闭旧编辑并要求重新加载/预检，不自动更新 CAS 或重试。权限树的批量允许只枚举当前已绑定启用项，并保留已有 deny 和范围。

`ListUserSessions` 新增独立 `target_revision`，避免误将操作者的 user revision 当作目标 CAS。会话撤销按后台既有协议使用 reason、目标/策略 CAS 和明确确认；该动作不支持治理模拟，界面不会调用一个必然被拒绝的模拟入口。

角色授权详情在已启用角色上投影真实继承路径；草稿/停用角色仍返回可编辑的直接 grant，但不伪造有效继承来源。临时投影通过模型的合法标识校验，返回时清除虚拟 assignment ID；实际来源仍由后端治理逻辑决定。

中文页面沿用现有 i18n；新增英文文案位于 `web/src/locales/en-US.rbac.ts`。

## C3：既有页面与字段/动作

用户、渠道、订阅账号/OAuth、模型及映射、路由组/资格/账务配置、订阅组/计划/权益、价格/上游成本、订单/兑换码、日志、总览/经营分析、设置、健康与路由运维页面均使用授权查询和明确动作按钮。附属详情、资金、模型使用、选择审计、配置和通知查询独立核对操作。

渠道和订阅账号的列表/详情返回 owner 计算的 `permitted_actions`。对象事实取自存储，按整体写的全部组覆盖语义核对，因此共享组对象可读时也可能没有状态/凭证写按钮。前端不执行 scope 或委派计算，服务端仍在实际提交点重新判断。

既有写接口在 IAM 下补齐 reason 和各 owner 返回的显示版本，包括单项/批量、父模型、用户/策略、配置 key 和上游成本视图 CAS。只从当前受保护缓存取已经显示的版本；保留显式陈旧 CAS，不在编辑后偷偷抓取最新版本覆盖冲突。旧数字 JSON 适配器拒绝不安全整数；IAM 新 DTO 始终使用字符串。

模型编辑增加 `preserve_pricing`：价格字段不可见或没有价格修改权时，表单锁定并省略价格，owner 明确保留持久化原值。它保持 false 时原有完整更新合同，并且仍受原 CAS、范围、副作用和审计保护。定向 SQL 测试确认真实非零价格未被脱敏零值覆盖，直接清零请求无财务权时被拒绝，陈旧 CAS 仍失败。导入/导出价格使用独立复合权限；模型启停同时要求模型修改和状态权限。

用户资金、模型价格和汇总 section 区分受限与真实零/空值。经营分析改为受保护后端报表导出，叠加报告/账本/成本权限，403 不回退本地 rows 导出。总览/成本页按 owner 的 restricted/unavailable 状态显示，而不吞成零值。

## 验收证据与复验

最终检查全部通过：

| 检查 | 结果 |
|---|---|
| `make all`、`make wire-check` | proto/config/Wire 自动生成与真实构造编译通过 |
| `make rbac-contract-check` | 824 行 source/HTTP/RPC/registration 契约已审查，源摘要与分类一致 |
| `make verify` | 格式、全仓 unit、默认 race、架构、迁移治理、重新生成前端 API 类型、lint、57 个文件/208 个前端测试、build 和构建体积预算全部通过 |
| C 定向 race | identity/channel 授权显示、隐藏模型价格保留、legacy 只读 HTTP 及真实 IAM 管理入口通过；全套 `internal/integration` race 回归通过 |
| RBAC Playwright | 7 组真实浏览器场景通过；HTTP → admin → 专属凭证 gRPC → identity/资源 owner → biz/data → SQLite，无浏览器授权响应 mock |

真实浏览器场景：

1. alice：继承的 east 范围、west 不可见、共享组可读/整体写拒绝、密钥脱敏、无关账号与通知不轮询。
2. 财务只读：无总览默认进入订单，订单按用户范围过滤；退款与报表导出独立拒绝。
3. 审计只读：审计页可读，管理写和审计导出拒绝。
4. member：本人角色可见；伪造 JWT 数值 role 和 localStorage role=100 均不能获得管理权限。
5. bob：委派覆盖的角色分配实际预检/提交成功；本人扩权及隐藏 alice 目标拒绝。
6. root：创建 draft、真实继承来源、树→矩阵保留修改、授权预检提交；重新读取后端确认直接 grant 持久化。
7. DSD：未选择时没有业务操作摘要；冲突角色激活失败，选择有效集合后进入获准页面。

退款测试装配真实退款 usecase、账户/订单/账本仓储与共享订阅 usecase；兼容 HTTP 失败 envelope 同时断言 `success=false` 和授权拒绝原因，其他管理拒绝按实际 403 验证。Vitest 另覆盖 authorization 刷新失败/403 不注销、手动 refetch gate、缓存清除、树/矩阵/allow+deny、无效范围、同版预检/409 和字段脱敏编辑；后台仍有既有 owner 边界反例测试。

```sh
make all
make wire-check
make rbac-contract-check
make verify

go test -race ./app/identity/... ./app/channel/... ./internal/integration \
  -run 'TestIAMSessionDisplayProjection|TestIAMCRedactedModelUpdatePreservesPrices|TestIAMA6RealAdminIdentityEndpoints' -count=1

go test -race ./internal/integration -run 'TestIAMCLegacyAuthorizationHTTP|TestIAMA6RealAdminIdentityEndpoints' -count=1
go test -race ./internal/integration -count=1

IAM_C_PLAYWRIGHT=1 go test ./internal/integration \
  -run '^TestIAMCRealBrowserMatrix$' -count=1 -v
```

浏览器命令要求 `web/node_modules` 与本机 Chrome，并占用本机 5174 端口；Go harness 自动创建迁移后的隔离 SQLite、短期测试凭证和临时 manifest，再启动 Vite 代理。浏览器截图/失败 trace 输出到忽略的 `web/test-results`。普通 `go test` 明确跳过 opt-in 浏览器测试，不能把跳过计作 Playwright 通过。日志在本次本机 `/tmp/rbac-c-*.log`；关键结果以上述命令可重复验证。

本阶段没有 DDL。新 SQL 行为使用真实 SQLite；本轮未配置 MySQL/PostgreSQL 临时 DSN，因此不声称重新完成三库 fresh/repeat/negative。B 阶段三库验收证据保留在原交付记录，D 阶段仍须按自身门槛执行验证。生成 pb/OpenAPI/Wire 遵循仓库既有忽略规则，跟踪的前端 API 类型由命令生成。

## 后续边界

C1–C3 没有剩余实现项。D0 需要影子比较、候选/全量对账、停写/恢复与切换故障演练；D1 需要生产实际凭证、旧写者/数据库通道退出、部署能力核验和正式交接授权。C 阶段隔离测试中的 iam/complete 只作用于临时数据库，不能作为生产切换证据。
