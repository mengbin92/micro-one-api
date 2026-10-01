# B 阶段进展：服务身份底座与用户执行切片

2026-10-01 · 分支 `codex/rbac-b-execution` · **B0/B1 进行中，B2–B4 尚未完成。**

本记录区分已实现的执行链与后续门槛，不代表 B 阶段完整交付。生产配置、生产数据库和授权事实源未变更；本批没有部署、推送或发布。

已实现服务凭证与固定方法清单、资源授权 RPC、范围 SQL 编译器，以及用户 CRUD、联系信息、凭证/路由写与本人账号执行链。其他业务所有者尚未使用该范围编译器；不能据此声明渠道、账务、日志等服务已执行 IAM 范围校验。

## 服务身份与执行契约

- 接收方通过 `SERVICE_CALLER_TOKENS` 配置其可信 caller 的独立 opaque 凭证，例如 `{"admin":"<admin专属凭证>","relay":"<relay专属凭证>"}`。只向接收方配置它需要验证的 caller，不把完整凭证集合注入所有进程。
- caller 通过 `SERVICE_IDENTITY_TOKEN` 发送自己的凭证。未配置时保留 `SERVICE_TOKEN` 兼容认证，但该主体标为 `legacy-shared`，`Dedicated=false`，不能证明某个服务身份或获得 `SystemCapability`。
- `platform/security/serviceidentity/registry.go` 固定声明完整方法、所有者及 user/system caller。空 caller 清单表示拒绝专属 caller，不能由请求扩大。新增方法缺清单时 RPC 拒绝，入口契约检查和真实 gRPC descriptor 枚举测试失败。
- `x-service-name`、operator ID 或 JWT service_name 不证明身份。重复/共享的专属凭证、未知 caller 配置拒绝；客户端没有签发其他 caller 身份的共享签名能力。
- `GetResourceAuthorization` 无公开 HTTP annotation。IAM 模式下请求方必须是 execution point 的实际 owner，并提供独立有效用户 JWT/JTI。admin 不能向该方法提交 identity 用户对象事实；浏览器也不能指定任意 permission 取得 capability。
- 兼容认证本身不完成 B0：其他资源消费端仍需逐一使用专属 capability 和 IAM 决策，直接 HTTP 仍需固定分类。当前不能切换生产 IAM。

## 用户执行切片

identity service 的用户 CRUD 和路由资格读取独立 operator 凭证。在 IAM 模式下同时要求专属 admin 服务身份；身份来自 JWT/JTI 与数据库复核，数值 role 和 operator ID 不参与 IAM 放行。

列表在 identity data 层对 `users` 应用 allow 并集与 mandatory deny，并在 count、分页之前过滤。组关系包含默认组及当前有效 grant；读取允许命中一个组，任一 deny 组命中仍拒绝。联系信息拥有独立 scope，缺少其权限时清空 Email。列表/详情同时返回目标 `authorization_revision` 与 `authorization_policy_revision`，proto JSON 使用字符串。

更新要求非空 update_mask、reason、目标和 policy 双 CAS。在 identity policy 锁内重读操作者、会话、分配、委派和目标事实，检查每个实际动作后才写账号、版本、会话撤销及成功审计。scope 查询、单对象验证和原子写分别位于 biz/data 的既有边界。

- `display_name`、enable/disable、email binding、credential 各检查自己的 operation。
- 邮箱设置、替换和显式清空使用同一路径；凭证接管要求 `user_credentials` 委派，并检查目标未来最大 allow 与 authority。root 和本人不能通过普通管理接管路径改凭证。
- 邮箱/密码变化推进 password epoch 并撤销旧 session；审计不包含密码、哈希或联系信息。
- 旧 group 更新检查原/目标共享组，以及 default/grant/revoke 三种实际效果。独立 routing_access RPC 校验 reason、routing/user/policy 三种 CAS，并复核原/目标共享组；public_access 变更只允许全局范围。本人的 default 只能选择已经有效的授权组。远端组引用在重试事务外核实。
- 本人资料/密码与已经通过 verification code 检查的邮箱绑定，使用同一 IAM 事务并复核本人 JTI；当前密码检查继续生效，不能以用户 ID 冒充本人。
- 创建只建立 member 默认分配，不通过请求设置角色或财务字段；创建和删除都要求 reason/CAS，审计失败回滚账号和关系。
- 本人登出推进 epoch 并撤销会话；本人删除保护 root、保留不可修改的审计。OAuth 绑定复核本人会话和 provider subject，并撤销旧会话。邮箱恢复 proof 绑定挑战发出时的 user ID/password epoch，旧 proof 不能在绑定或凭证变化后重放；root 恢复继续使用专用救援。

admin 的 IAM console 检查与用户资源检查独立。当前仅用户 CRUD、完成的用户别名、独立 routing-access GET/PATCH 及 access 查询可通过业务 guard；其他业务路径保持拒绝，A6 IAM 管理 API 继续使用自己的执行链。用户列表别名在 IAM 下不调用账务补充查询，也不以 0 冒充获准读取的资金字段。HTTP revision 使用字符串。未注册的 `/api/*`、`/v1/*` 返回 404，不能落入 SPA。

## 验证

实际 SQLite/MySQL/PostgreSQL 的 `TestIAMB1ManagedUsersDialects` 验证了列表/total/分页、隐藏目标、联系信息、更新 CAS、无凭证委派拒绝、邮箱清空、旧会话撤销、root 保护、审计失败全回滚、本人写及跨用户拒绝。还验证了创建默认分配、创建/删除失败回滚、合法凭证委派及未来 authority 阻断、OAuth/邮箱恢复、self 登出/删除、路由三种 CAS 和 outbox 回滚。本人订单入口在 IAM 下不再使用数值 role 扩大范围；billing 所有者完整校验仍需 B3 补齐。

本机临时容器只监听 `127.0.0.1`，端口 MySQL `13316`、PostgreSQL `15436`；每个测试创建独立临时库/schema，运行全部迁移及 repeat，并在测试结束删除。未读取部署 DSN。执行方式：

```sh
GROUP_CONTEXT_TEST_MYSQL_DSN='<本机临时 MySQL DSN>' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='<本机临时 PostgreSQL DSN>' \
go test -race ./app/identity/internal/data -run TestIAMB1ManagedUsersDialects -count=1 -v

go test -race ./internal/integration -run TestIAMB0B1OwnerAndUserBoundaries -count=1
go test -race ./platform/security/serviceidentity ./platform/grpc/xgrpc ./platform/database/authzquery
make all
make wire-check
make rbac-contract-check
make migration-check
make verify
```

跨服务测试执行真实 admin HTTP → identity gRPC/service/biz/data，包含共享 token、伪造 caller/owner、无 operator、数值 role 伪造、未注册/未绑定入口、资金字段省略、protobuf JSON 字符串 revision/FieldMask、邮箱清空和 409。`TestSQLMatchesPureScope` 将参数化 SQL 结果与纯范围语义逐组对照。

本批无新 DDL；未执行 C3 全角色 Playwright、D0 影子对账/切换故障注入，不计作通过。`make verify` 的生成类型检查以自动生成的 `web/src/types/api.ts` 为预期结果，生成结果与源码一同提交，未手改生成物。

## 剩余门槛

| 工作包 | 尚未完成 |
|---|---|
| B0 | 全系统 capability 的资源消费端强制校验；所有直接 HTTP、别名和分支的固定运行时分类；服务专属凭证部署配置核验 |
| B1 | 本人财务/订单在 billing 所有者的独立权限链（依赖 B3）；所有兼容分支、跨服务 token/路由引用和完整角色场景的执行覆盖核验；完整能力核验前仍不计 B1 完成 |
| B2 | channel/account/OAuth/model/mapping/routing/group/health 的范围 SQL、total/批量/导出一致性、敏感字段、复合价格权限及事务提交复验 |
| B3 | billing/subscription 的资源范围、系统任务 capability、模型价格组合、后台报表导出与跨服务不变量 |
| B4 | log/config/monitor/notify/system/content 的直接 HTTP/RPC、统计/内容字段、配置键分类及通知动作 |

本批不把“方法已列清单”当作“所有执行链已授权”，也不把拒绝未实现路径当作该资源的完整功能交付。
