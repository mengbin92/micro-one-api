# RBAC A4 会话、快照与账号原子写交付

> 2026-10-01 · 基线 `d7427dff` · 工作分支 `codex/rbac-a4-sessions-accounts`。
> A4 后端用例、真实账号启动链与 CLI 限制完成；生产仍保持 legacy，未部署、未切换事实源。下一工作包为 A5；本人会话与救援的新传输适配由 A6 交付。

## 实现与边界

| 落点 | 结果 |
|---|---|
| `platform/security/auth/session.go` | 共用用户 JWT claims 和验证结果 Actor DO；固定 HS256、issuer、audience、token_type、正 user ID 与一致 subject，要求真实非空 JTI/exp，保留 pwd_epoch；无 exp/JTI、过期、错误签名/算法、服务 token 拒绝 |
| identity `biz/auth.go` | 兼容 ValidateSessionToken 复用验证结果；持久化模式从主库复核用户状态和密码 epoch。IAM 模式复核真实 session/context，选择未完成拒绝普通认证路径，返回的兼容 role 不承担 IAM 管理授权；不根据 JWT role 建分配 |
| `biz/iam_runtime.go`、`data/iam_runtime.go` | 按真实 JTI 惰性建立 platform session/context；JTI 与用户及原始 exp 绑定，既有/撤销记录不替换；默认激活当前有效分配的角色闭包，DSD/激活数量冲突进入 selection_required，不任意选取特权组合 |
| 本人会话用例 | 查询、激活、撤销只接受已验证 JWT 的本人 session；组织请求拒绝；激活只能从当前有效授权闭包选择，复用 A3 当前及未来半开区间约束，session-context CAS；独立 context 或全局 session 撤销分别递增对应版本 |
| 主库授权快照 | 一个 repeatable-read 视图读取用户、policy/catalog、角色/继承/grants、分配、约束、session/context 和 user revision。保留各来源路径及强制 deny；过期/撤销来源不能通过旧激活恢复，最早失效时间包含未来 starts_at/expiry/session exp；无授权缓存或清理任务依赖，去除密码 hash |
| 共用创建入口 | bootstrap、Register/邀请码、OAuth 新建、后台创建在 policy 锁内建立账号、默认 member/root 分配、用户/policy 版本与成功审计；OAuth 绑定与路由默认 grant 加入同一事务。origin 固定为 default/bootstrap；legacy 中这些关系不成为授权事实源 |
| 真实 bootstrap | cmd Wire 构造在启动/transport/bootstrap 之前注入共享 Data 的 runtime/runner；锁后重查 CountUsers，只建一个 root，失败无半个账号/分配。密码哈希和 channel 事实查询在回调外；只有最终成功提交者获得一次性生成密码，竞争者不输出密码或伪造成功事件 |
| 账号更新 | 普通资料、邮箱、旧 role、自助、密码/注销、OAuth 绑定分别使用明确字段契约，锁后重读身份，移除旧持久化整行 UpdateUser。密码/状态变化同步撤销已持久化 session，递增用户/policy 版本；不回写财务余额、OAuth 身份或无关 role/凭证字段 |
| 路由账号写 | 旧 group 变动、default/grant/revoke/public_access 与账号 user/policy 版本、默认 grant、routing revision/outbox、成功审计共用 IAMTx。远端 group 预检不进入重试回调；原始持久化 Repository 写入口拒绝，底层 helper 仅由事务适配调用 |
| 旧写门槛 | legacy/idle 允许兼容账号写；blocked/verified 和 IAM 拒绝旧身份/role/group/凭证/后台创建/OAuth 绑定写。IAM/complete 的公开注册、邀请码与 OAuth 新建建立默认分配；后台 IAM 创建的操作者/委派治理由 A5/B1 接入，当前旧后台入口不放行 IAM |
| CLI/回填 | admin-reset 与 routing-backfill 先取得同一 policy 锁，在同事务判定 mode/cutover；IAM 或非 idle 拒绝旧直接 SQL。legacy reset 推进密码 epoch；routing-backfill 同步用户/policy 版本和成功审计。软件门槛不能代替 D0 旧进程/DB 写通道退出证据 |
| 独立救援用例 | RescueRootCredential 要求部署端 `IAM_RESCUE_ENABLED=true`、独立 ADMIN_TOKEN、reason、user/policy CAS、IAM/complete 和当前有效 builtin root 分配；只恢复启用 root 的密码，原子推进 epoch、撤销会话、版本和审计，不附带 role/邮箱/状态改写。A6 尚未绑定 RPC/HTTP/CLI，普通 IAM 管理不能启用它 |
| 审计 | 成功事件随事务提交，失败回滚后独立追加；失败审计不可写时错误可观测，请求仍拒绝。重试只做本事务数据库操作与纯计算；提交后使用同一 event_id 输出结构化日志。账号审计记录安全状态、分配来源与版本，不记录密码、hash、JWT 或救援 token |

所有固定目录操作仍为 draft/unbound，没有新增 HTTP/RPC 注册或前端管理入口。A4 不提供 A5 委派治理/管理 authorizer，也不表示 B 阶段所有资源已安装 IAM 执行点。显式开发内存模式保留 legacy 测试 seam，不提供持久化 IAM 降级。

兼容性收紧：持久化 SetRole 需要已验证且锁后重读的用户操作者；原来仅静态 ADMIN_TOKEN 的系统角色修改路径拒绝。尝试保留该绕过行为的修改被自动审批以权限边界放宽拒绝，因此本批保持拒绝路径。账号角色管理使用用户会话；救援仅通过上述独立凭证恢复用例，不能以救援主体做日常角色修改。

## 验证证据

MySQL 8/PostgreSQL 16 使用本机一次性容器，端口 13316/15436；scratch helper 逐项建立/删除临时数据库或 schema。SQLite 使用独立临时文件、外键和 WAL。不读取部署 DSN；三库均实际执行，没有把外部驱动跳过计作通过。

| 检查 | 结果 |
|---|---|
| 真实账号入口/竞争 | 三库真实 bootstrap 两实例竞争只产生一份 root/分配；默认角色容量竞争仅一方提交；Register、邀请码、后台 legacy 创建、OAuth 新建都有 default 分配，已有 OAuth 登录保留授权关系 |
| 失败原子性 | root 分配、OAuth 绑定、成功审计失败都回滚账号/默认关系/版本；成功审计及失败审计都不可用时拒绝且错误可见；group/outbox 故障不留默认组/grant/版本部分修改，只保留独立失败审计 |
| 会话与 CAS | DSD 待选择、非法 root 激活/冲突/旧 revision 拒绝；会话激活只改变 context revision；真实 JTI 不能被 user ID/JWT role 替代，过期 JWT 不建会话，context/全局撤销不可惰性复活；组织请求拒绝 |
| 快照一致性 | 在用户读取后由另一连接提交 user/policy/session-context 更新，本次快照仍全部旧版，下一次全部新版；显式验证三库主库一致视图，未使用 PostgreSQL 默认 READ COMMITTED 拼接 |
| 时间与来源 | 固定时间 biz 测试验证半开 endpoint 后激活立即失效、未来 deny 生效，不运行清理任务；来源与 inactive deny 保留，快照最早失效边界正确，账号停用/password epoch/session expiry 拒绝 |
| 旧写与救援 | 三库 raw Repository 写拒绝、legacy/blocked/verified/iam 门槛、CLI 不改用户或凭证、密码撤销、救援开关/身份/root/CAS 保护与审计无 secret；路由写/回填 IAM 阻断 |
| 定向 race | identity biz/data/server/service/cmd、shared auth、admin-reset、authorization 与 migration 共九组包，显式三库 DSN，全通过 |
| 全仓检查 | make all、make wire-check、架构检查、migration-check、rbac-contract-check、make verify 全通过；verify 含 unit/race、前端 API 类型、lint/test/build |
| 注册清单 | 254 HTTP、178 RPC、10 gRPC 注册保持不变；144 源码摘要，总计 586 行，A4 新写入者及原始存储入口复核已更新 |

可复验：

```sh
GROUP_CONTEXT_TEST_MYSQL_DSN='root:rbac_a4_test@tcp(127.0.0.1:13316)/mysql?multiStatements=true&parseTime=true' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='postgres://postgres:rbac_a4_test@127.0.0.1:15436/postgres?sslmode=disable' \
go test -race ./app/identity/internal/data ./app/identity/internal/biz \
  ./app/identity/internal/server ./app/identity/internal/service ./app/identity/cmd/identity \
  ./platform/security/auth ./cmd/admin-reset ./domain/authorization ./platform/database/migrate -count=1
make all
make wire-check
./scripts/check-architecture.sh
make migration-check
make rbac-contract-check
make verify
```

本批无新 DDL；三库 fresh/repeat 复用全量迁移，negative migration/CAS/SQLite 忙重试与 A2/A3 存储回归一并运行通过。Wire 通过 make 生成；没有手改 proto/Wire 产物、发布 release 或生产部署。完整 RBAC API/跨服务执行矩阵、Playwright 和交接故障演练仍属 A6/B/C/D，不能计作本批已经通过。
