# P0 账号写入者与旧权限矩阵

> 2026-09-30 · 首批源码契约。表中“目标要求”是后续 A2/B1/D0 的执行义务，当前账号写路径仍是 legacy，尚未接入新状态门槛。

## 账号创建与身份写

| 调用主体/入口 | biz 调用链 | 真实写入者/字段 | 当前事务 | 目标要求/所有者 |
|---|---|---|---|---|
| public 注册、Register RPC、邀请码注册 | Register → RegisterWithAffCode → createUser | Repository.CreateUser → createUserDB：username/display_name/email/group/status/role/password_hash/OAuth/balance/aff_code/inviter_id/pwd epoch | 普通 Create；routing v2 使用 createRoutingUserDB 事务写 user + 默认 grant | identity；policy 锁内写账号、default 分配、版本、路由、审计；奖励通过 billing，不在重试回调发 RPC |
| 后台 collection POST、CreateUser RPC | CreateUser → createUser | 同一个 Repository.CreateUser；调用者不能绕过统一创建入口 | 同上 | identity.user.create；设置认证字段另检 email_binding/credential 与接管委派；group 另检 default/grant；默认 member 分配同事务 |
| OAuth 登录首次建号 | OAuthLogin → createUser；CreateOAuthIdentity | users + user_oauth_identities；现有已注册用户不重置角色 | 用户和 OAuth 身份目前独立写 | identity；用户/default 分配/OAuth 绑定合入一个事务；登录已有账号保留授权 |
| identity 启动 bootstrap | bootstrap.go → repo.CreateUser（绕开注册 helper） | 创建 root，含 role=100/password_hash/default group | CountUsers 和创建目前没有共用 policy 锁 | identity；同锁重新 CountUsers，bootstrap 分配、版本、审计同提交；blocked/verified 拒绝；两个实例仅建一个 root |
| 后台资料、状态别名、UpdateUser RPC | UpdateUser → repo.UpdateUser | updateUserDB 整字段映射：username/display_name/email/group/status/role/password_hash/oauth_provider/oauth_id/aff_code/inviter_id/password_changed_at | routing v2 进入 updateRoutingUserDB，其他情况单 UPDATE | identity；改为明确字段写；email 显式清空同权限；status/role/group/pwd 不能通过普通 update 回写 |
| promote/demote、SetUserRole RPC | SetRole → repo.UpdateUser | role，且现有整字段写会回写其他身份列 | 同 updateUserDB | identity；legacy 操作者等级/root 保护；IAM 模式永久拒绝旧 role 写，转多角色分配 + 委派/CAS |
| self PUT、邮箱验证码绑定 | UpdateSelf / UpdateSelfEmail → repo.UpdateUser | username/display_name/password_hash/password_changed_at/email；仍使用整字段写 | 同 updateUserDB | identity；重新认证/验证码 + 本人所有权；凭证、epoch、全局 session 撤销、版本、审计原子 |
| public 重置密码（验证重置码后）、注销登录 | ResetPasswordByEmail / InvalidateAllSessions → repo.UpdateUser | password_hash/password_changed_at；注销当前实现撤销全部用户会话 | 同 updateUserDB | identity；已验证恢复流程；JTI/session 与密码 epoch 原子变动，不用客户端 user_id 或 JWT role 判权 |
| 本人邀请码生成 | GetOrCreateAffCode → repo.UpdateUser | aff_code（旧整字段写仍回写 role 等） | 同 updateUserDB | identity；最小字段更新，受身份写停写屏障；不能复活已删除/停用账号 |
| OAuth 绑定 | BindOAuthIdentity → repo.CreateOAuthIdentity | user_oauth_identities provider/provider_id/user_id | 独立 Create | identity；本人重新认证/state；撤销与身份版本同事务；后台路径须 credential 接管委派 |
| 后台/自助用户删除、DeleteUser RPC | DeleteUser → repo.DeleteUser → deleteUserDB | 删除 users 行 | 单 Delete | identity；用户保护、候选/分配/会话引用处理同事务；审计不可级联删除 |
| routing-access PATCH（本人仅合法自助动作，后台完整治理）、UpdateUserRoutingAccess RPC | routing access 用例 → routing_access.go | users.default_routing_group_id/routing_access_revision/public_group_access，user_routing_group_grants、routing outbox | RetryTxOnBusy + CAS + outbox | identity；policy 锁后核实原/目标用户与组；实际 grant/revoke/default/public 差异分别授权，同事务加入 IAM 版本 |
| 旧 group 更新、创建 routing v2 用户 | updateRoutingUserDB / createRoutingUserDB | users/group/default/revision、legacy_group grant | 各自创建 transaction | identity；可加入调用方 IAMTx，不能分别提交或嵌套后提前成功 |
| 路由回填 CLI | BackfillRoutingGroups | users.default_routing_group_id/routing_access_revision/public_group_access、legacy_group grant | Serializable + dry-run rollback | identity；D0 清点独立 DB 写通道；非 idle 不允许旧回填；路由迁移与 IAM 候选 batch 不能混淆 |
| admin-reset CLI（DB 直接写） | upsertPassword | 已有账号 password_hash/可选 role；新账号 username/email/role/password_hash/status/quota/group | Count 后 Updates/Create，非 IAM 事务 | cmd/admin-reset/main.go；IAM/非 idle 拒绝旧 SQL；服务端受保护救援使用独立身份/开关/reason/CAS/审计，不能顺带改 role |

源码：`app/identity/internal/biz/{auth,bootstrap,routing}.go`、`app/identity/internal/data/{data,routing,routing_access}.go`、`cmd/admin-reset/main.go`。生产使用 DB Repository；内存 Repository 是测试/dev 专用，也不能作为权限降级路径。SOURCE 摘要纳入入口矩阵，修改上述代码会触发重新核对。

## 财务与脚本写通道

| 写入者 | 调用主体/字段 | 事务/目标要求 |
|---|---|---|
| accountRepo.UpdateBalance / UpdateBalanceInTx | 后台调余额、兑换、购买/退款等；users.balance | billing transaction 或 subscription Tx；管理路径 balance.adjust/reset，self 验证买方，系统回调验证支付；保留账本/幂等/余额不变量 |
| accountRepo.IncrementBalanceInTx | TopUpQuota/系统注册与邀请奖励；users.balance | billing 事务；注册奖励仅专属系统 capability，operator_id="system_register" 本身不认证服务 |
| accountRepo.UpdateUsage / UpdateUsageInTx | relay 结算；used_amount/request_count | billing/调用方事务；不能把身份写屏障理解为撤销所有 users 财务写 |
| accountRepo.UpdateFrozenAmount | 额度预留/释放；frozen_amount | 原子 SQL；专属财务凭证或暂停排空方案 |
| accountRepo.ReserveBalanceInTx / CommitBalanceInTx / ReleaseBalanceInTx | relay reserve/commit/release；balance/frozen_amount | 调用方 Tx、条件 SQL、幂等任务；停止旧写者时核对在途/恢复重放，不丢财务写 |
| Repository.IncreaseUserBalance → increaseUserBalanceDB | identity 兼容财务 repo seam；balance | 当前生产 biz 无调用；保留为显式潜在 DB 通道清点项，不能遗漏 |
| scripts/test-e2e-flow.sh | 测试直接 UPDATE users.quota | 仅隔离验收库，禁止当生产迁移或放权路径；SOURCE 摘要纳入检查 |

`account_repo.go` 是生产 billing users 写者；`reconciliation_repo.go` 对 users 为读取。D0 支持列级权限时限定 billing 的财务列；SQLite/共享凭证无法隔离时暂停消费/结算写、排空并重放现有幂等任务。服务账号/DB 凭证和运行实例的真实部署核验在 D0/D1，首批不声称已隔离。

## legacy 权限迁移基线

| 主体 | 已有准入 | IAM 候选映射/限制 |
|---|---|---|
| guest=0、member=1 | 无后台；本人资料/Key/订单/订阅等仍各自验证身份/所有权/业务资格 | guest/member；不因 self 操作获得管理 grant；路由资格独立 |
| platform_admin=10 | admin HTTP guard 检查 role>=10；用户 role 修改需独立 operator credential 和目标等级检查 | platform_admin 按入口矩阵明确枚举；新增 catalog 项不自动授予；凭证接管和路由授予收紧需单列差异 |
| root=100 | 同后台；含价格模型 import/export 的额外 root 检查；root 目标禁止普通角色改写 | 受保护 root；仍检查身份/JTI、注册操作/context、领域不变量；明确目录 revision 枚举 |
| ADMIN_TOKEN | admin guard 救援主体，身份角色操作有独立系统标记 | 救援开关 + 固定 allowlist + reason/审计；普通 IAM CRUD 不可修改救援配置 |
| SERVICE_TOKEN | 大多数内部 RPC 共享认证，不能证明不同服务身份；并非统一用户授权 | B0 专属服务凭证 + full method capability；用户管理 RPC 仍需用户 JWT/operator 双验 |
| 未知 users.role | 可能落入旧 >= 比较，不能推测其含义 | 候选与最终 rebuild 阻断，人工核对；不静默转 member/admin |

各 HTTP/RPC 具体复合动作、数据所有者、当前 guard、字段附加操作及后续测试义务见 [entry-matrix.csv](./entry-matrix.csv)。矩阵的 test_contract 列是验收义务，不是这些入口已通过 IAM 测试的声明。
