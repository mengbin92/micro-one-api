# RBAC A3 图、职责分离与未来基数交付

> 2026-09-30 · 基线 `5691af25` · 工作分支 `codex/rbac-a3-constraints`。
> A3 约束评估与事务变更子流程完成；生产继续 legacy，无新增 HTTP/RPC、前端入口或部署。下一工作包为 A4。

## 实现与固定计数语义

| 落点 | 结果 |
|---|---|
| identity biz `iam_constraints.go` | 同域全 DAG 校验、启用节点闭包、SSD/DSD、角色成员上限、用户/会话角色数量上限、所有未来半开区间扫描与冲突 DO |
| identity biz `iam_constraint_usecase.go` | 一致视图预检、按 policy/目标 revision 提交、整批最终状态检查、可信授权接口、成功/失败审计；没有 permissive 运行时授权实现 |
| identity data `iam_constraints.go` | 同事务加载全部 platform 角色/分配/约束/会话；graph、constraint/members、limits 与 activation 存储及目标 CAS；构造返回 biz 接口 |
| 三库 `111_add_iam_cardinality_limits.sql` | policy 单例增加可空的正数 `max_roles_per_user`/`max_roles_per_session`，identity ownership；NULL 无限，0/负数拒绝，不修改 mode 或已有角色分配 |
| `iam_repo.go` | 共用 SaveAssignment 现在同时递增用户与 policy revision，让后续默认分配适配也使整域预检版本失效；普通资料/会话采用仍属 A4 |
| `entry-matrix.csv` | 保持 254 HTTP、178 RPC、10 gRPC 注册；新增三项 SOURCE 至 141，合计 583 行；复核新 usecase/评估器/仓储及版本变动 |

继承方向为 senior→junior。所有节点（包括禁用、草稿、归档及不连接当前用户的节点）参与结构校验；自引用、环、未知节点、重复边/节点和跨域引用拒绝。有效闭包在非 enabled 节点切断；菱形和多个独立分配按实际角色 ID 去重。权限范围、allow/deny 和是否激活不改变 SSD 的授权角色计数。

固定上限的具体落点：`role.max_members` 对拥有该角色有效闭包的用户去重计数；平台 `max_roles_per_user` 对每名用户的有效授权角色闭包去重计数；`max_roles_per_session` 对单个会话的有效激活闭包去重计数，均计入继承。当前没有组织实体，两个 policy 上限仅为 platform 设置；组织启用时需按域提供相应存储，不能把多个域合计。停用账号保留分配预约，不借身份停用静默释放名额；账号恢复仍需 A4 的身份/约束检查。

SSD 检查全部有效分配的闭包；DSD 仅检查当前单个 session-context 的有效激活闭包，不合并不同会话。主动激活 junior 不激活 senior/sibling。过期/撤销的分配、角色传播切断、过期/全局或域内撤销的会话不贡献激活计数；stale 激活必须重新与实际授权闭包相交。selection_required 不参与普通激活，并要求没有已激活角色。新激活变更只可选择事务时刻已有的授权角色，未来分配不授予今天的激活资格。

收集从服务端当前时刻起所有相关分配 starts/expires 与 session expires 边界，每个窗口使用 `[start,end)`；相等边界先结束后开始，NULL expiry 为无限期。检查新增、延期、恢复、边界调整、角色启停/继承、约束新增/启用/修改、上限及激活变更后的整批最终状态；不依赖清理任务。连续且内容相同的冲突窗口合并，返回 kind、实际计数/上限、角色/用户/session、约束名称与起止区间。新约束的候选 ID 只在内部使用，对外以 `constraint_id=0/proposed_constraint=true` 表达未提交的约束，不能假装已经落库。

## 事务、安全与后续边界

写用例先由 A2 runner 锁 policy，再读取模式/全量关系与目标版本。仅 iam/complete 可进入管理变更；legacy、blocked、verified 不能借 A3 开放普通 IAM 写。`expected_policy_revision` 为必须的整域预检版本，所有成功批次都会递增，目标 role/assignment/constraint/session-context 另外执行 revision CAS；过时预检或目标版本返回 409，不能拿旧 preview 直接保存。

提交前在内存构造完整结果并统一验证，包含未来窗口和现存会话剩余有效期。冲突返回 typed `AUTHORIZATION_CONSTRAINT_VIOLATION` 与仅给授权治理调用方的冲突 DO；不静默撤销或挑选角色。整批写入、版本和成功审计在一个事务提交；第二个写失败、审计冲突或提交失败不能留下前半批。成功审计的 after 从提交前实际仓储重新读取，使用真实生成 ID，避免候选 ID 被当作已持久化事实。已通过可信授权但提交/预检失败时，在回滚后独立追加失败审计；追加失败仍拒绝并保留两个错误供调用方观察。

`IAMChangeAuthorizer` 是必须的可信 biz 依赖，当前没有生产实现或 server/Wire 接入；nil 拒绝。它必须在同一事务内验证真实身份/会话、每项变更对应的精确操作、全部受影响目标与成员、三类委派的来源/上限、deny 扩权和 protected 账号。不能以客户端 actor ID/布尔值代替验证，也不能在可重试回调里发远程 RPC。A4/A5 实现这条链后，A6 才接 API。A3 审计 action `iam.constraint.batch` 是事件标签，不是新操作权限或任何豁免 capability。

A3 额外拒绝普通 root topology、root 分配和把 root 作为继承/约束成员的变更；完整角色创建、归档引用、委派、账号保护、目标管理范围与自我扩权仍属 A5。存储方法本身不承担授权；A4 默认账号/会话写和 D0 迁移必须在自己的受保护用例中复用约束评估，不能直接调用仓储后声称完成了治理。

现有 JWT/JTI 认证链、真实 session 创建/惰性迁入、DSD 默认角色选择流程及账号/OAuth/bootstrap/CLI 接入仍属 A4。本批 session DO/PO 仅为约束读取与检查后的激活存储提供基础，真实会话身份不会由客户端提供的 session_id 建立。

## 验收证据

| 层 | 实际检查 |
|---|---|
| 纯 biz | enabled 闭包、禁用路径切断、菱形/多来源去重；不连接/禁用节点的环、自引用、未知/重复/跨域拒绝 |
| 纯未来窗口 | 明日同窗拒绝、相邻/无限接受、延期与恢复重新预约、时间推进无需清理；SSD 不受边界或激活隐藏；会话到期恰好碰到未来 starts 不计重叠 |
| 纯 DSD/容量 | junior 激活不提升 senior、不同会话独立、全局/域撤销与到期不计数；角色重新启用后的未来 DSD/会话上限；selection/未知 role/非法零上限拒绝 |
| biz 用例隔离 | fake repo/runner 验证 preview 无写、冲突拒绝、旧 base/目标 revision、legacy 与缺失 authorizer 关闭、请求/原状态不被模拟改写、最终批次激活所有权 |
| 三库真实事务 | 每个驱动独立文件/schema，全迁移 fresh/repeat（含 111）；A2 negative SQL/未记录版本回归、正上限 DDL 负例 |
| 三库未来并发 | policy 锁下同一版本的两个未来预约仅一个成功，另一个 409；重新预检仍因未来名额拒绝，不留下重复分配 |
| 三库图/职责分离 | 继承增加未来成员超限拒绝；反向边成环拒绝；SSD/DSD 启用影响现存成员/会话拒绝；明确撤销后可以同批发布；约束删除不残留 members |
| 三库会话 | graph/重新启用造成当前 DSD 返回真实 JTI 清单；激活/上限含继承，受限变更无半个 session-role 替换；合法子集成功，旧 session-context revision 拒绝 |
| 三库原子审计 | 预检冲突独立失败审计；第二次存储写注入失败及重复成功 event_id，role/graph/limits/分配/policy 皆回滚；success after 为实际状态 |
| 回归/门槛 | identity biz/data、domain authorization、migration 定向 race；make all/wire-check、架构、migration-check、rbac-contract-check、make verify |

本机一次性 MySQL 8（13317）/PostgreSQL 16（15437）与临时 SQLite WAL 文件实际执行；显式测试 DSN，未读取部署 DSN。验收后清理容器。普通 verify 不传 DSN 会跳过外部两驱动，不能单独当三库证据。

```sh
GROUP_CONTEXT_TEST_MYSQL_DSN='root:rbac_a3_test@tcp(127.0.0.1:13317)/mysql?multiStatements=true&parseTime=true' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='postgres://postgres:rbac_a3_test@127.0.0.1:15437/postgres?sslmode=disable' \
go test -race ./app/identity/internal/biz ./app/identity/internal/data ./domain/authorization ./platform/database/migrate -count=1
make all
make wire-check
./scripts/check-architecture.sh
make migration-check
make rbac-contract-check
make verify
```

无新增依赖、无手改生成文件、无生产 DDL/配置或模式切换。下一阶段 A4 将接入真实认证会话、一致授权快照、默认角色与账号原子写和救援 CLI 限制；仍不开放自定义授权到生产。

## 后续生产更新（2026-10-01）

A3 提交并合并 develop 后，按用户独立部署授权更新了全部 9 个生产服务及 identity 待迁移项；继续使用 legacy/idle，没有启用 IAM 执行链。此操作不改变上述 A3 交付范围或 A4–D1 门槛。证据与回滚点见 [生产更新记录](./a3-legacy-production-deployment.md)。
