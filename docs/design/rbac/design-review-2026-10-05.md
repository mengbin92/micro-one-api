# RBAC 对照设计代码审查与修复

2026-10-05 · 基线 `develop@c8bbcd9e` · 工作分支 `codex/rbac-review-fixes`。

依据 [主设计](../rbac-permission-management.md) 和 [实施计划](../rbac-permission-management-implementation-plan.md)，复核固定目录/执行点、范围评估及 SQL 编译、IAM 会话/治理/存储、账号写入、管理协议、前端 gate/缓存、菜单和迁移交接。沿用 828 行入口契约与既有 B0–B4/D HTTP/gRPC 角色回归。以下是本轮发现并修复的具体问题；历史交付记录保留当时证据，本轮结果不替代生产交接证据。

## 发现与修复

| # | 级别 | 设计要求与原问题 | 修复与回归 |
|---|---|---|---|
| 1 | P1 | §4.1/5.2：mandatory deny 消失须预检扩权。分配只有 `revoked=true` 才检查；推迟开始或缩短到期同样会暴露其他分配的 allow，却能提交。 | 统一比较旧分配从当前时刻起的剩余区间；新窗口不能覆盖旧窗口时检查该用户全部未来最大 allow。管理单项/批量与 A3 authorizer 共用规则。`TestIAMReviewAssignmentDenyWindow` 覆盖延期开始、提前到期和撤销，以及 ceiling 充分时的正例。 |
| 2 | P1 | §3.2/5.2：分配与撤销分别授权。Assign/Batch 的 `revoked` 载荷只要求 assign/batch 操作和委派动作。 | 任一撤销效果追加 `identity.user_role.revoke`，用 revoke 委派动作校验管理范围和扩权。`TestIAMReviewAssignmentPayloadCannotBorrowRevocationAuthority` 覆盖两入口。 |
| 3 | P1 | §4.3：管理创建设置认证字段同样检查独立操作、账号接管委派和默认授权 ceiling。原 create 只检查创建及路由操作。 | 密码检查 `credential.update`；提供邮箱再检查 `email_binding.update`。创建前检查操作/目标组，账号及默认分配写入后、提交前用真实新 ID 复核 `user_credentials`。失败回滚账号/分配/路由。`TestIAMReviewManagedCreateCredentialsDialects` 覆盖缺操作、缺委派、目标不覆盖与有效创建，三库实际执行。固定 create 执行点同步登记组合操作。 |
| 4 | P2 | §4.2：非法范围必须拒绝。`AllowCovers` 遇早先 all 即返回，可能漏检后续非法范围；`DenyMatches` 对非法范围返回未命中。 | 先验证完整 allow 集合；非法 mandatory deny 按拒绝处理。`TestInvalidScopeNeverBecomesAllowOrDisappearingDeny` 通过真实纯评估及 QueryScope seam 复现。上游决策/SQL 已有校验仍保留。 |
| 5 | P2 | 实施 §4.2/7：root 也是固定有限 grant，新增目录操作不自动授予。前端摘要却将 `root=true` 视为每个启用操作都存在。 | 摘要完全由真实来源/deny 计算，不增加 root 隐式操作。`TestIAMReviewRootDisplayUsesExplicitGrants` 验证已启用但未显式授予的操作不出现。 |
| 6 | P1 | §7/8：角色授权来源须受独立读取范围约束。GetRolePermissions 只检查来源最终角色，泄露未授权中间继承角色 ID/path。 | 检查整条来源路径上的 `iam.role.permissions.read` 与管理范围；影响预览复用同一 helper。`TestIAMReviewRoleSourcesHideIntermediateRoles`。 |
| 7 | P1 | §7/8：用户有效权限/解释不能作为隐藏 role grants 的旁路。原检查了 assignment 可见性，却返回无 grant 读取权的来源。 | 查询其他用户时须对每条来源的完整 grant path 具有读取权，否则拒绝；本人独立来源查询保留。解释计算不会通过删掉隐藏 deny 得到虚假允许。`TestIAMReviewEffectivePermissionsRequiresGrantReadAuthority`。 |
| 8 | P1 | §4.3/6.2：支持范围是固定注册契约。分配边界可包含继承操作不支持的维度，写成功后目标下一次快照失败。 | 完整提交状态检查每个非撤销分配与结构闭包中所有 grant 的范围兼容性，覆盖 draft/disabled 节点；分配、权限和继承变更均在提交前拒绝。`TestIAMReviewAssignmentBoundaryMatchesInheritedOperations`。 |
| 9 | P2 | §3.1：退役操作不复用。权限已 archived 后仍能通过 status API 重新 enabled。 | archived 为终态，普通 metadata/status/archive 写均拒绝；界面同步禁用动作。`TestIAMReviewArchivedPermissionCannotRevive`。 |
| 10 | P2 | §1/3.1：权限目录有说明、分类和排序。原 DTO/DO/PO/界面完全缺失说明和排序。 | 补充 description/sort、精确 update_mask、目录默认 `sort,id` 数值排序及 category/risk_level/sort 筛选排序。`TestIAMReviewPermissionMetadataDialects` 验证 DTO 往返、真实 CAS 写、保留省略字段和重新查询；前端测试验证实际模拟载荷。新增 123 三库迁移。 |
| 11 | P2 | §3.2/9：按钮绑定固定精确操作。公开组访问控件用了不存在的 `identity.routing_access.public.update`，有权用户仍被禁用。 | 改为 `identity.routing_access.public_access.update`。组件回归验证有权启用；新增前端字面量操作码与固定目录一致性的 CI 测试，防止拼写漂移。 |
| 12 | P2 | 实施 §6：查询与写按钮各自 gate。会话列表按 revoke 权启动，与实际 GET 的 `iam.authorization.user.read` 不一致。 | 按 read 启动会话查询，撤销按钮单独检查 revoke；只在需要角色选项的页启动附属 role list，切换解释目标重建解释组件清除前一目标结果。`IAMPage.test.tsx` 验证只读用户可读会话且不能撤销。 |
| 13 | P2 | §3.3：菜单父节点组织可见子节点，不授予父路由/兄弟操作；图标及排序按白名单配置。原菜单投影会漏掉无独立操作权的父节点，导航全部用 Layers 且平铺。 | 服务器保留可见叶子的启用祖先，未知/循环/停用祖先拒绝该分支；前端按层级和数值顺序显示，仅独立 page/menu gate 通过时生成链接，并映射白名单图标。`TestIAMReviewMenuAncestorsDoNotGrantParentOperations` 与菜单 helper 测试验证父标签无授权、兄弟不可见、排序和停用子树。 |
| 14 | P2 | 设计/实施状态一致。主设计顶部仍称 C/D 未开始和生产 legacy，与实施 §9.12 冲突；共享模型注释也仍称未进入生产。 | 主设计同步引用已有 D1 的 iam/complete 交接记录，明确本轮修复尚未部署；纠正共享模型/目录注释，保留各阶段历史叙述。 |

## 复现与验证

首先运行既有 `go test ./domain/authorization/... ./app/identity/internal/biz/... ./app/identity/internal/service/... ./app/identity/internal/data/...`，全部通过；原测试未覆盖上述反例。随后新增真实治理/范围/组件 seam 的回归，修复前实际观测到以下失败：

```text
go test ./app/identity/internal/biz -run TestIAMReview -count=1
FAIL TestIAMReviewAssignmentDenyWindow: Expected AUTHORIZATION_PROTECTED_RESOURCE but got nil
FAIL TestIAMReviewArchivedPermissionCannotRevive: Expected AUTHORIZATION_PROTECTED_RESOURCE but got nil
FAIL TestIAMReviewRootDisplayUsesExplicitGrants: actual includes channel.channel.delete
```

后续隐藏路径、有效权限、范围兼容、撤销载荷和菜单祖先测试也先红后绿；创建账号的 SQLite 真实事务测试先观察到缺认证字段操作仍返回成功。回归只使用本机隔离数据库及短期测试凭证，不读取生产 DSN。

已完成的验证：

- IAM 底座/治理/账号/迁移用例的 SQLite、MySQL 8.4、PostgreSQL 16 实际 `-race`；三库新增创建和目录元数据用例通过，没有跳过驱动。最后修复后的相关复验另记录在 `/tmp/rbac-review-three-db-final.log`。
- MySQL/PostgreSQL 完整迁移 fresh/repeat/negative，以及历史 `schema_migrations` 元数据预检/升级/恢复脚本通过；日志 `/tmp/rbac-review-mysql-migration.log`、`/tmp/rbac-review-postgres-migration.log`。SQLite 全量迁移、重复/增量升级及失败门禁通过，新增测试确认 122 → 123 保留旧权限名称、状态和 revision，仅新增展示默认值。
- 最后版本 7 组真实 Chrome Playwright 角色场景通过，HTTP → gRPC → biz/data → SQLite，无浏览器授权响应 mock；日志 `/tmp/rbac-review-browser-final.log`。

最终门禁：

| 命令/检查 | 结果 |
|---|---|
| `make test-unit` | 通过，日志 `/tmp/rbac-review-unit-final.log`；初次新增迁移触发固定数量断言，更新 64 个 SQLite 文件及升级断言后复验通过 |
| `make test-race` | 通过，日志 `/tmp/rbac-review-race-final.log` |
| `go test -race ./internal/integration -count=1` | 通过，含真实 B0–B4/A6/D 切换与本人路径；日志 `/tmp/rbac-review-integration-final.log`。此命令中的 opt-in Playwright 不算执行，浏览器另按上一条实际执行 |
| 配置两套本机 scratch DSN 的 identity `-race` | SQLite/MySQL/PostgreSQL IAM 全套通过，后续治理/创建/元数据及最后修复再定向复验通过，没有驱动跳过；日志 `/tmp/rbac-review-three-db.log`、`/tmp/rbac-review-three-db-final.log` |
| 前端 lint、`tsc --noEmit`、`npm test` | 通过，59 文件 / 213 个测试；日志 `/tmp/rbac-review-web-final.log` |
| `make api`、前端 `generate:api` | 自动生成通过；新 DTO 字段未手改生成文件 |
| `make wire-check`、架构检查 | 通过，全部服务 wiring 与分层约束保持 |
| `make migration-check`、`make rbac-contract-check` | 通过，123 ownership/三库镜像完整，828 行入口及源码摘要复审一致 |
| `make format-check`、`git diff --check` | 通过 |

全仓 `go test ./...` 初次执行还包含依赖已启动 localhost:3000/900x 服务的旧 `test/e2e/suite`，因连接拒绝失败；这不计作通过。按仓库 `make test-unit` 门禁排除此套件，并独立运行自包含 `internal/integration`；专项真实浏览器另执行 opt-in。

## 升级与边界

新增迁移 `123_add_iam_permission_metadata` 仅属 identity，为旧目录增加带默认值的展示列，不改变原 110 迁移或既有授权事实。应先对 identity 数据库运行迁移，再更新 identity/admin 与前端；共享范围评估修复还需随消费它的资源服务发布。新 proto 字段 13/14 为兼容追加，已通过 `make api` 和前端 API 类型生成更新。无需新增配置或修改 Wire 依赖。

普通管理创建账号现在需要独立的凭证操作及覆盖新账号/默认授权的 `user_credentials` 委派；仅有创建权的主体收到拒绝。本人注册/邀请码/OAuth 的独立身份流程不借用管理接管权。已存不兼容分配不会被自动扩大，涉及它的治理写应明确修正范围，root 通过受保护治理流程处理。

本轮没有提交、推送、部署、生产数据修改或发布；生产交接状态只引用先前 D1 记录。运行测试的临时数据库容器在验证结束后删除。没有构建服务镜像或前端生产 bundle。
