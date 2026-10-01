# B 阶段进展：服务身份、用户与资源所有者执行切片

2026-10-01 · 第二批源分支 `codex/rbac-b2-b4-execution` · 集成目标 `develop` · **B0–B4 进行中，尚未完成整阶段。**

本记录区分已实现的执行链与后续门槛，不代表 B 阶段完整交付。生产配置、生产数据库和授权事实源未变更；本批没有部署、推送或发布。

已实现服务凭证与固定方法清单、资源授权 RPC、范围 SQL 编译器，以及用户 CRUD、联系信息、凭证/路由写与本人账号执行链。本次渠道、账务及 B4 所有者开始使用范围编译器；具体完成切片与限制见下面的第二批记录，不能据此声明 B 阶段全量闭合。

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

实际 SQLite/MySQL/PostgreSQL 的 `TestIAMB1ManagedUsersDialects` 验证了列表/total/分页、隐藏目标、联系信息、更新 CAS、无凭证委派拒绝、邮箱清空、旧会话撤销、root 保护、审计失败全回滚、本人写及跨用户拒绝。还验证了创建默认分配、创建/删除失败回滚、合法凭证委派及未来 authority 阻断、OAuth/邮箱恢复、self 登出/删除、路由三种 CAS 和 outbox 回滚。首批本人订单入口取消了 IAM 数值 role 扩围；第二批进一步接入 billing 所有者的真实会话与归属复验，完整财务/订阅场景验收仍需补齐。

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

## 第二批：B2–B4 所有者执行切片

此批保留进入任务时的渠道/授权客户端草稿，收紧其专属服务主体豁免：仅当前完整方法的 system capability 可跳过用户决策。其余用户调用由实际 owner 使用自己的专属凭证向 identity 查询，转发独立 operator JWT/JTI。新增 `mode_only` 仅返回授权模式，不返回主体或范围；缺失 identity 连接不推断 legacy。六个 owner 的生产装配和 compose 模板加入 identity 端点，未配置或部署真实专属凭证。

- **渠道/订阅账号**：渠道及账号 CRUD、启停、额度重置/错误清理接入 owner 权限；列表 SQL 在 total/分页前施加范围。组范围读取可命中共享资源的一个组，写入需覆盖原/目标全部组，mandatory deny 优先。事务内锁定并重读归属、凭证和旧状态。普通 update 不能修改 key/token、状态、已用额度或显式模型映射而绕过独立操作；账号价格倍率写和尚未完成的映射操作在 IAM 下拒绝。新建组范围使用目标组事实；不能通过渠道兼容编辑器偷偷新建模型。secret.read 独立脱敏，账号后台 metadata 只返回固定运维键；relay 的精确 capability 仍读取原始凭证。纯内存 channel 不支持 IAM 组事实，拒绝范围查询。
- **路由组**：列表在分页前按实际 group resource_id 过滤，隐藏详情拒绝。members.read 独立控制成员和模型授权数组，`members_visible` 区分脱敏与真正空组。创建要求全局 create；状态写在 row lock 下检查 update 及实际 enable/disable，revision CAS 与 outbox 同事务。渠道/账号 CSV 的归属增删同时要求 `routing_group.members.update` 覆盖原/目标全部组，关系投影和 legacy CSV 事实路径均检查。override 单独授权并锁定组/核实成员；write-only 返回自身写结果，不要求额外 read。archive 与尚不存在的全量替换协议未绑定。
- **账务**：账户读取/批量及余额调整、账本详情/列表/统计、订单列表/详情与退款接入 owner。账本的 account resource_id 指用户账户 ID；文本 user_id 在 PostgreSQL 中绑定文本参数。退款在原有 row-lock 事务回调内验证实际订单所有者，已退款幂等重入也检查实际订单归属；资金写将真实 actor 和当次事务授权版本写入现有账本 remark，要求 reason，并在提交边界检查决策期限。IAM 余额调整需要既有事务 runner。`billing.account.cost.read` 独立控制 upstream cost/profit；统计按每个 bucket 的完整覆盖决定是否返回，DTO 的 `cost_fields_visible=false` 明确表示受限，不能当作零成本。
- **本人路径**：billing 对 identity 本人订单/创建/消费/订阅入口重新验证当前会话，按实际 user_id 或订单归属检查；不能仅凭 identity 服务凭证读取其他用户。log 本人 HTTP 别名使用独立 self 执行链，只查询真实本人；数值 role 不扩大查询范围。
- **日志**：详情、列表/total/分页、统计、选择事件、按范围删除在 log owner 执行。content.read 独立脱敏 message/usage field shape；对正文搜索同时应用 content scope，不能利用关键词探测隐藏正文。摄入仅精确 worker capability。导出/purge 仍未实现，不绑定。
- **配置**：直接 HTTP/RPC 均进入 owner；只有 notice/about/home_page_content 的固定 public GET 可绕过管理权限。实际持久化 namespace/key 决定 content、安全、支付、价格操作；option 特殊写叠加普通 update。未知安全键及敏感 key/value 脱敏，内部精确 config read capability 保留原值。写-only 内容操作从已授权写返回 revision，不在提交后追加 read 权限导致“已写入但响应拒绝”。
- **健康/告警/通知**：monitor 健康/规则、notify 列表/详情的资源范围在 count/分页前生效，最新健康详情不能回退到旧的获准记录。SaveHealthCheck、CreateNotification/UpdateNotificationStatus 与 Alertmanager 回调只接受固定系统 capability；普通 read/list 不授予发送或变更投递状态。不同方法的 capability 不能互相借用。尚无实际 ack/test/rules 管理 handler 的操作仍 unbound。

所有者 gRPC 明确列出已完成方法；IAM 下共享 token、未完成管理方法拒绝。直接 channel OAuth/selector 和 billing reconciliation 已关闭 IAM 旁路，**其完整功能尚未交付**。admin console 的业务 ready 清单仍只开放既有 B1 切片；B2/B3/B4 的完整 admin 代理、复合操作和界面尚未验收，因此不能通过拓宽 guard 假装完成。

三库测试揭示并修复旧数字标志列的 PostgreSQL bool 编码，以及 config `key` 的方言引用。data PO 用 `xdb.Flag` 读取历史 bool/数字列并写数字，biz 保持 bool；无新 DDL。

实际隔离验收：`TestIAMB2ChannelOwnerDialects`、`TestIAMB3BillingOwnerDialects`、`TestIAMB4{Log,Config,Monitor,Notify}OwnerDialects` 均在 SQLite/MySQL/PostgreSQL 下通过 race；B4 另覆盖内存实现。验证包括 total/分页/隐藏详情、强制 deny、共享组读写区别、旧事实写回拒绝、敏感字段、普通更新复合动作拒绝、本人跨用户拒绝、成本标志、配置键组合权限、正文搜索及跨方法 capability 拒绝。数据库容器仅监听 localhost，每个测试运行全部迁移及 repeat、独立建库/schema并清理；未读取部署 DSN。SQL/owner 单元验收不等同于全角色真实跨服务 HTTP 与生产切换验收。

后续复核补齐普通更新的实际副作用检查：余额/状态、账号创建的价格倍率，以及兼容 Models 的映射新增/删除/启用/优先级变化，在独立操作未完成时拒绝；保持不变的普通名称更新仍可执行。普通渠道/账号 DTO 不暴露尚未绑定的原始 mapping JSON。额度重置和错误清理在实际写事务中重新检查归属；配置写直接从本次写 DO 返回 revision，避免提交后读取其他并发写的值。

渠道/账号/路由组、余额调整、退款、配置、告警规则与日志删除在每次数据库事务尝试前通过纯 Resolver seam 重新取得 identity 决策；RPC 不在可 replay 的事务回调内执行。SQLite busy 回滚后再次查询，不重用首次许可；actor/mode 改变、撤权或依赖故障中止。本人订单创建在事务前重新验证当前会话。事务内继续锁定对象事实/核验 CAS 和决策期限；本批不声称 identity 与资源跨库撤销全局串行。

新增 `TestIAMB2RoutingGroupOwnerDialects` 已通过真实三库 race，共七组 owner 三库测试。`TestIAMTransactionRetryRechecksRevocationBeforeReplay` 使用单连接 SQLite，证明决策发生在事务外、首轮写回滚、重试撤权不再进入写回调；另验证 actor/mode 改变与本人 session 撤销。真实 identity RPC 增加 B2–B4 固定 point/operation 的 owner、数值 role、unbound 与 mode-only 测试，防止 fake resolver 接受未注册操作；成本字段独立 operation 的执行声明已核验。admin HTTP 代理转发专属服务凭证和独立已验证 operator，删除客户端伪造 operator header。monitor HTTP 保留业务 400/404 与授权 403，依赖故障返回 503。

本轮验证命令：`make all`、`make wire-check`、`make rbac-contract-check`、`make verify`，以及七组 owner 三库 `go test -race` 和真实 IAM integration 均通过。`go test ./...` 包含未启动服务的 E2E suite，不计作通过；使用仓库默认 `test-unit`/`verify` 门禁。入口矩阵仍有 774 行经复核，完整 B 阶段门槛按下表保留。

第二批定向回归可使用以下命令。实际三库验收需设置前文的两个本机临时 DSN；未设置时 MySQL/PostgreSQL 子测试会跳过，不能记作三库通过。

```sh
go test -race ./app/channel/internal/data ./app/billing/internal/data \
  ./app/config/internal/data ./app/log/internal/data \
  ./app/monitor/internal/data ./app/notify/internal/data \
  -run 'TestIAMB[234].*OwnerDialects' -count=1 -v
go test -race ./internal/integration -run TestIAMB0B1OwnerAndUserBoundaries -count=1
go test -race ./platform/database/authzquery -run TestIAMTransaction -count=1
go test -race ./app/admin/internal/server -run TestOwnerProxyUsesIndependentVerifiedCredentials -count=1
```

## 剩余门槛

| 工作包 | 尚未完成 |
|---|---|
| B0 | 全系统 capability 的资源消费端强制校验；所有直接 HTTP、别名和分支的固定运行时分类；服务专属凭证部署配置核验 |
| B1 | 本人财务/订阅及订单的完整业务场景验收（基本订单归属和会话链已接入）；所有兼容分支、跨服务 token/路由引用和完整角色场景的执行覆盖核验；完整能力核验前仍不计 B1 完成 |
| B2 | OAuth/模型/映射/模型路由/渠道健康完整执行链；路由组 archive/全量替换协议；批量/导出与价格组合；全部 admin 代理、后台 DTO 及跨服务提交/角色场景 |
| B3 | 共享 subscription 管理、兑换码、价格/成本管理、对账、报告导出；附属 request-attempt/定价证据权限；复合模型价格与跨服务不变量 |
| B4 | 全部 admin 代理与总览 section 预检；日志导出/purge；明确通知 ack/test/rules 管理协议；持久化写审计、完整角色跨服务入口验收 |

本批不把“方法已列清单”当作“所有执行链已授权”，也不把拒绝未实现路径当作该资源的完整功能交付。
