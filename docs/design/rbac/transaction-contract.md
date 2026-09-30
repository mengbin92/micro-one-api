# 首批事务与切换契约

> 2026-09-30 · 首批冻结的事务契约；A2 PO/迁移/事务 runner 已实现，证据见 [A2 存储基础交付](./a2-storage-delivery.md)。真实账号用例接入与会话生命周期仍属 A4。

biz 接口：`app/identity/internal/biz/iam.go` 的 IAMTx/IAMTxRunner。共享 PolicyState、WriteKind、ValidateOrigin 在 `domain/authorization/state.go`。data 从同一个 Data 长期连接实现 runner，现有 Repository 与 IAMRepo 不各建连接池；构造返回 biz 接口。

## 一次写入的唯一提交边界

1. 开事务并先锁 `iam_policy_state` 单例；采用 `ponytail: 单 policy 行限制管理写吞吐，竞争成为瓶颈再按 context 拆锁`。
2. 在锁后读取 mode/cutover/batch，验证 CheckWrite；再读权威用户、真实 JTI/session/context、策略/目录及版本。多个角色/用户/session 关系按稳定 ID 升序锁定。
3. 读取受影响直接/继承分配、委派、图、SSD/DSD、未来起止边界。校验 reason、expected_revision、base_policy_revision/内容摘要、所有权和保护规则；只允许同域关系，当前组织请求拒绝。
4. 写账号/关系、创建来源/候选 origin、password epoch/会话撤销、各版本、默认组/grant 和 routing outbox。所有相关 repo 接收同一个 IAMTx；不得事务外先授权再另开保存事务。
5. 使用同一 event_id 追加成功审计，再提交。失败回滚全部业务关系；回滚后独立追加失败审计，失败可观测且请求保持拒绝。
6. 仅最终成功 attempt 的结果可返回；提交后才写成功日志/输出恢复密码。邮件、RPC、事件发送等外部副作用不进入会被重试的回调。

MySQL/PostgreSQL 写使用锁定读取；SQLite 先写锁 policy 再读事实，通过 `xdb.RetryTxOnBusy` 重试整个回调，不能照搬 FOR UPDATE。ReadIAMSnapshot 在主库使用同一事务视图：MySQL/PostgreSQL 显式 repeatable-read，SQLite 一个读事务；禁止 PostgreSQL 默认 READ COMMITTED 多次查询拼快照。A2 必测并发 CAS、忙重试、失败审计、默认账号/OAuth/root 创建回滚。

跨服务资源引用在事务外由 owner 核实 ID/归属/版本，实际资源执行时 owner 再验证；不将跨服务预检称作跨库原子。未来生效/到期用半开 UTC 区间；快照失效时间包含未来 starts_at 和 expires_at，不能靠清理任务或 TTL 续用旧权。

## 可执行状态门槛

| 状态 | legacy account | 普通 IAM 管理 | 候选写（迁移身份） | rebuild/verify（迁移身份） | bootstrap |
|---|---|---|---|---|---|
| legacy/idle | 允许 | 拒绝 | 允许 | 拒绝 | policy 锁内重新 CountUsers 后允许 |
| legacy/blocked | 拒绝 | 拒绝 | 拒绝 | 唯一 batch 允许 | 拒绝 |
| iam/verified | 拒绝 | 拒绝 | 拒绝 | 同 batch 继续验证 | 拒绝 |
| iam/complete | 拒绝旧身份写 | 新用例允许 | 永久拒绝 | 拒绝旧回填 | 空库受保护初始化允许，使用权威分配 |

CheckWrite 只是状态门槛；迁移身份必须来自独立已验证凭证，布尔标记不能从客户端传入。普通 IAM API 不切换状态。blocked/verified/complete 必有 batch；iam 状态必有非零 verified_at；legacy 不得带 verified_at，idle 不得带残留 batch；其余组合拒绝。运行时新账号写在 complete 经 IAM 专用用例，不是 LegacyAccountWrite。迁移推进仅允许 idle→blocked→verified/iam→complete，最终 verify 和 mode 转换同事务。失败保持停写，同 batch 全量重建，不恢复旧进程或倒退回填。

## 迁移归属冻结

`IAMAssignment.Origin/MigrationBatchID` 固定组合：

| origin | batch | 可写者/含义 |
|---|---|---|
| legacy_candidate | 必填 | 唯一迁移工具；包括 legacy root 候选，最终 rebuild 只替换这类关系 |
| default | 空 | 注册/OAuth/后台创建的统一账号事务 |
| bootstrap | 空 | 受保护空库 root 初始化 |
| explicit | 空 | 正常有权分配用例或核准委派 manifest 的治理写 |

不得靠 created_by/用户名判断候选；非候选也需验证期限/ceiling/域，不能无条件保留。role.creation_delegation_id 只能治理路径原子写，不能通过普通元数据更新换来源。legacy role 仅映射 0/1/10/100，未知值阻断。

A2 将主设计 6.2 的全部 `iam_*` 表登记为 identity ownership，使用当时下一空闲 migration 序号和 MySQL/PostgreSQL/SQLite 镜像；本批不预占编号、不增加空 DDL。初始化 policy 为 legacy/idle，policy/catalog revision 起始值明确，域键 platform/0/platform；同域组合引用和唯一约束不能仅凭 role_id。

D0 屏障必须覆盖 [账号写入者清单](./account-writers.md) 的 CLI/旧实例/任务/跨服务 DB 通道；软件 CheckWrite 无法约束旧二进制。列级财务隔离或暂停结算方案、停写/排空/旧通道撤销证据、最终所有用户对账和兼容 IAM 回滚版本，均是后续交接门槛。

## A2 实现落点

`iam_tx.go` 的 runner 先锁 policy，`iam_repo.go` 的 CAS/账号适配/审计共享同一事务。只读句柄、跨 Data 句柄和已经结束的句柄均拒绝；仓储检测到变更却未追加成功审计时拒绝提交。SQLite 忙重试最多五次，每次重建事务并重新取得 policy 锁；返回结果与外部副作用只能在最终成功提交后采用。

runner 校验持久化状态的合法组合，**不代替调用方的 WriteKind/迁移身份/权限判定**。A3–A6 用例必须在锁后通过同一 repo.Policy 读取并 CheckWrite，再执行完整治理检查。没有公开 IAM 管理 API 或普通 mode/cutover 修改接口。全部 15 张 IAM 表在 110 迁移由 identity 所有；目录 revision 1 的种子明确列举资源/action，全部操作 draft/unbound；没有回填现存账号角色。
