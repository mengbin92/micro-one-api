# RBAC A2 存储基础交付

> 2026-09-30 · 基线 `22d7cbed` · 工作分支 `codex/rbac-a2-storage`。
> A2 存储与事务基础完成；运行入口继续 legacy，下一工作包为 A3。未部署、未建立生产分配、未推进事实源切换。

## 实现

| 落点 | 结果 |
|---|---|
| `migrations/{,postgres/,sqlite/}110_create_identity_iam.sql` | 全部 15 张设计 IAM 表、`users.authorization_revision`、identity ownership、查询索引；policy 初始为 revision=1/catalog=1/legacy/idle |
| 固定目录种子 | 显式列举 registry revision 1 的资源、操作、范围、context 与 protected 属性；所有操作 draft/unbound；registry 增加明确 resource/action，复合 action 不会被拆到错误资源 |
| 内置角色种子 | guest/member/platform_admin/root；root 显式引用平台目录；前三者仅建立角色定义，平台管理员的精确旧操作映射由后续迁移交付。没有用户分配，没有根据数值角色自动放权 |
| `internal/data/data.go` | 最小共享 `Data` 持有既有数据库/Redis 客户端，账号 Repository 与 `NewIAMRepo`/`NewIAMTxRunner` 共用连接池。内存模式不提供 IAM 持久化降级 |
| identity biz `iam_storage.go` | 纯 DO 的 IAMRepo、审计事件与账号事务适配契约；不引入 GORM 或 PO。补充角色元数据与分配版本/操作者字段 |
| identity data `iam_models.go` | role/assignment/policy/audit PO 与 DO 转换；JSON 统一 jsonx，scope 严格解析；UTC 毫秒、半开区间、NULL 无限期；整数标志显式转换，兼容 PostgreSQL |
| identity data `iam_tx.go` | 写事务先锁 policy；MySQL/PostgreSQL `FOR UPDATE`，SQLite 首条 SQL 取得 policy 写锁；`xdb.RetryTxOnBusy` 最多五次重试整个事务；只读主库使用同一 repeatable-read 事务视图 |
| identity data `iam_repo.go` | 同事务权威用户、角色/继承/权限、分配、版本和审计读取；role/assignment/user/policy CAS；角色写递增 policy，分配写递增用户版本；非 builtin 角色元数据存储、来源不可改写 |
| 账号事务适配 | 创建用户及既有 routing v2 helper、OAuth 绑定可加入 IAMTx；路由嵌套事务为同一连接的 savepoint，不能单独提交；返回新值，不修改输入。真实 bootstrap/注册/OAuth 用例采用这条链属于 A4 |
| 审计提交 | 成功审计随写提交；仓储检测到变更但未追加成功审计则回滚；重复 event_id 使整个事务回滚；失败审计在回滚后独立追加，错误返回且不可据此放行；仓储只暴露追加/读取接口 |
| `entry-matrix.csv` | 原 254 HTTP、178 RPC、10 gRPC 注册保持不变；源码摘要新增四项至 138，合计 580 行；复核共享 Data 与新存储源文件，未新增授权执行点 |

DDL 校验 canonical context、同域组合引用、唯一关系、合法状态、委派 target_kind 字段组合、凭证委派仅 platform、正版本、有效区间和 origin/batch 组合。组织形状只预留；IAM 仓储当前拒绝组织请求，未创建组织实体/成员/部门。

继承环、SSD/DSD、未来成员/分配/激活上限、委派 ceiling/来源与 root 账号治理不能仅靠 DDL：由 A3/A5 用例完成。`SaveRole` 不保存 graph/grants，并拒绝携带这些字段；内置角色不可通过此元数据入口修改。没有普通 IAM 管理 API，事务 runner 只保证存储边界；调用方仍必须在锁后按已验证身份执行 `PolicyState.CheckWrite`、reason/权限/保护与未来区间检查。切换状态没有普通仓储写接口。

## 验证证据

`app/identity/internal/data/iam_test.go` 使用既有 scratch helper，为每个驱动独立建文件或本机临时 schema；不会读取部署 DSN。MySQL 8 与 PostgreSQL 16 为本机一次性容器（端口 13316/15436），SQLite 使用临时文件、外键及 WAL；验收后删除容器。

| 检查 | 结果 |
|---|---|
| 三库全迁移 fresh/repeat | 全部实际执行，通过；repeat 没有待执行迁移；目录种子与固定 registry 范围/context/protected 一致 |
| 三库 negative migration | 非法 SQL 失败且不记录版本，通过 |
| 三库引用/约束负例 | 非规范 context、跨域 assignment/inheritance/delegation/session-role/constraint-member、继承自引用、相等起止、缺失候选 batch、错误委派字段、非法 policy/max_count 均拒绝 |
| 三库锁/CAS | 八个竞争事务共用同一个 policy expected_revision，只成功一个，其余 409；role 元数据 CAS 与 builtin 保护通过 |
| 三库审计/回滚 | 关系、用户版本、成功审计一起提交/回滚；缺少审计、审计 event_id 冲突不能留下关系或版本修改；失败审计独立保存 |
| 三库主库视图 | 读快照中另一连接提交 policy/user 版本后，后续读取仍为同一旧视图；事务只读/归属/生命周期校验通过 |
| 三库账号适配 | 普通默认分配及 root 分配创建后注入失败，user/OAuth/routing-grant/assignment/audit 全部回滚；两个初始化事务在 policy 锁后重查人数，只建一份 root/user-role/audit。此为存储原语验收，尚非真实启动链采用证据 |
| SQLite 忙重试 | 用独立连接真实持有写锁，证明 policy 取得锁前实际重试；最终回调/审计只发生一次；context 取消不执行回调 |
| 定向 race | identity data/biz、domain authorization、migration package 通过；显式传入三库本机 DSN，无驱动跳过 |
| 全仓检查 | `make all`、`make wire-check`、架构检查、`make migration-check`、`make rbac-contract-check`、`make verify` 通过；SQLite 迁移数量断言由 50 更新至 51，新增 schema 落地断言 |

可复验（DSN 仅示例测试凭证；需先准备可建临时 schema 的本机一次性数据库）：

```sh
GROUP_CONTEXT_TEST_MYSQL_DSN='root:rbac_a2_test@tcp(127.0.0.1:13316)/mysql?multiStatements=true&parseTime=true' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='postgres://postgres:rbac_a2_test@127.0.0.1:15436/postgres?sslmode=disable' \
go test -race ./app/identity/internal/data ./app/identity/internal/biz ./domain/authorization ./platform/database/migrate -count=1
make all
make wire-check
./scripts/check-architecture.sh
make migration-check
make rbac-contract-check
make verify
```

常规 `make verify` 未配置外部驱动时会跳过 MySQL/PostgreSQL scratch 测试，因此不能单独作为三库通过证据；本批另外执行了带显式本机 DSN 的定向 race。pb/Wire/OpenAPI 沿用仓库生成及忽略规则，无新增依赖、无手改生成文件。

## 下一阶段

A3 实现 DAG、SSD/DSD、成员/分配/激活上限与未来半开区间预检，并在图/启停/约束修改时返回受影响成员与会话冲突。A4 再接入真实 JWT/JTI 会话、统一账号用例和 CLI 限制。A2 完成不代表任何业务入口已接入 IAM，也不代表可以部署或开启生产 IAM 管理。
