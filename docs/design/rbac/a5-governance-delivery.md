# RBAC A5 委派、角色治理与模拟交付

> 2026-10-01 · 基线 `87025145` · 工作分支 `codex/rbac-a5-a6-governance-api`。
> A5 后端治理完成；传输交付见 [A6](./a6-management-api-delivery.md)。identity/admin 已按用户授权更新线上，生产继续 legacy；详见 [生产更新记录](./a5-a6-legacy-production-deployment.md)。

## 实现与边界

| 落点 | 交付 |
|---|---|
| `domain/authorization/management` | 共享纯 DO：角色、分配、委派、目录、菜单、约束、请求/结果和影响来源；identity biz 与 admin RPC 适配共享契约，无 proto/GORM/凭证依赖 |
| `domain/authorization/containment.go` | 有限子句包含证明；分别解析目标/操作者 self，不拼接不同来源或维度的 ceiling；不能证明包含则拒绝 |
| identity `biz/iam_governance.go` | 锁内主库复核用户、真实 JWT/JTI、会话激活、mode/cutover、固定执行点与启用目录；操作权限与授予权限分别检查，数值 role 和静态 ADMIN_TOKEN 不承担普通治理授权 |
| 三类委派 | `role` 精确目标角色；`role_creation` 通过不可变创建来源管理新草稿；`user_credentials` 用于凭证接管准入。actions、用户范围、权限/范围 ceiling、有效期、CAS 均固定校验；不能互换类型或以成员权限替代授予权 |
| 创建/复制 | CreateRole/CopyRole 同事务检查初始 grants/继承、来源读取/复制权限与有效创建委派，创建 draft 并写 `creation_delegation_id`；不复制成员或委派 authority。root 特权创建可无创建来源，普通创建不能指定/迁移来源或创建 builtin/root |
| 生命周期/继承 | code/context/创建来源不可变；草稿、启停、归档、授权替换与继承替换复用图/未来约束。检查全部受影响的 senior 角色与现存/未来成员；不能修改自身可激活角色、通过 junior 给不可管理成员扩权，或归档仍被引用的角色 |
| 撤销 deny | 角色停用、授权/继承改变和分配撤销检查受影响用户全部未来最大 allow，要求一条适用委派覆盖；不借另一条业务分配暴露超 ceiling 的能力。当前实现保守地对含 deny 的角色变更采用此检查 |
| 再委派 | 需要 iam.delegation 操作权限、当前有效可再委派父来源及同类型/目标、期限、actions、用户范围和 ceiling 包含；不能修改自身现存/未来管理角色的委派或用多条来源拼接上限 |
| 凭证接管 | `IAMCheckCredentialTakeover` / A3 authorizer 接口检查目标全部直接/继承、未激活/disabled/未来分配的最大 allow，忽略临时 deny/DSD 掩盖。有效或未来重叠 IAM authority、本人/root 目标拒绝；实际账号管理执行点接入属于 B1 |
| 模拟/批量 | PreviewRoleChange、PreviewUserRoleChange、SimulateAuthorizationChange 复用真实 proposal、治理与 A3 约束，返回来源前后差异、受影响用户、冲突、base revision/摘要。执行重新锁定与授权，旧 policy/目标 revision 拒绝；摘要不是授权凭证，批量整批提交或回滚 |
| 存储/审计 | 复用共享 Data、A2 runner/policy 锁；角色关系替换、创建来源、分配、user/policy/catalog revision 与成功审计原子。审计 before 使用实际对象，成功影响来源使用落库 ID；失败回滚后独立追加，追加失败可观测且仍拒绝 |
| 撤销/时间 | 委派撤销保存 actions 为空的墓碑，不级联删除创建引用或业务授权。后续每次治理重新检查来源；会话快照 `valid_until` 包含当前授权管理角色的委派 start/expiry，不依赖清理任务 |

目录启用必须同时有固定代码执行点；未注册草稿、已注册但未绑定的业务操作不能由发布 API 启用。protected 核心操作不能通过普通目录生命周期停用/退役；root 也要求目录显式启用。D0 可信准备流程必须初始化恢复目录并验证能力，普通 API 不改 mode、cutover、binding 或支持范围。

本包提供受限角色治理；目录创建/发布/退役、全局约束和菜单写采用 root 准入，目录元数据可以按固定操作范围管理。角色成员/授权/引用、其他用户有效权限和审计分别校验可见范围；不能用列表权限获取隐藏成员或完整审计 payload。组织 context 当前一律拒绝。

## 验证证据

- 纯用例：未来 authority 接管、deny 不隐藏最大 allow、半开区间、间接继承/未来成员、自身可激活角色、无 senior 授权、撤销 deny 暴露其他分配、再委派类型/用户范围/ceiling/自身保护，以及 root 不能绕过 disabled/unbound 目录。
- 三库 `TestIAMA5A6GovernanceDialects`：创建来源/draft、模拟摘要与 CAS、受限成员分配、外部用户/本人拒绝、超 ceiling 拒绝、过滤列表与影响来源防泄漏、归档引用、两个并发写仅一个成功、委派 expiry 进入快照、撤销委派保留业务来源、legacy 写阻断。
- 三库 `TestIAMA5LifecycleAtomicBatchAndCatalogDialects`：复制、SSD 预检、整批失败无部分分配、删除约束后提交、继承环、allow/deny 差异、未知草稿/未绑定发布/核心停用拒绝、菜单白名单/环、过滤排序分页 total、成功审计故障整批回滚。
- SQLite、MySQL 8、PostgreSQL 16 均实际执行，scratch helper 建立/删除独立数据库或 schema、执行 fresh/repeat 迁移；显式三库 race 回归 A2–A6 的 `TestIAM*`，不以驱动跳过计作通过。
- `make all`、`make wire-check`、`make migration-check`、`make rbac-contract-check`、`make verify` 通过；另补 identity biz/service、authorization、DTO 和真实跨服务入口的定向 race。

可复验（先启动本机一次性 MySQL/PostgreSQL，勿使用部署 DSN）：

```sh
GROUP_CONTEXT_TEST_MYSQL_DSN='root:rbac_a5a6_test@tcp(127.0.0.1:13316)/mysql?multiStatements=true&parseTime=true' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='postgres://postgres:rbac_a5a6_test@127.0.0.1:15436/postgres?sslmode=disable' \
go test -race ./app/identity/internal/data -run TestIAM -count=1 -v

go test -race ./domain/authorization/... ./app/identity/internal/biz \
  ./app/identity/internal/service ./platform/iamdto ./internal/integration -count=1
make all
make wire-check migration-check rbac-contract-check
make verify
```

`make verify` 的前端类型一致性检查与 git index 比较；按仓库流程先生成并暂存预期的 `web/src/types/api.ts`，生成器重跑不得新增差异。本批无 DDL，复用 110/111 迁移；无 release、生产管理开放、B 阶段业务执行绑定或 C 阶段管理 UI。
