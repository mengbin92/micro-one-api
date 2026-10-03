# B 阶段闭合：服务身份与资源所有者执行链

2026-10-03 · 工作分支 `codex/rbac-b-full-closure` · 集成目标 `develop` · **B0–B4 已完成全量后端闭合。**

本记录对应实施方案第 5 节及 B0–B4 工作包，替代此前两批部分交付的剩余门槛表。代码、协议、迁移、入口矩阵及隔离验收一起交付。2026-10-03 按用户另行授权更新全部 9 个线上服务、前端和分库迁移，验证后提交并合并到 develop，见 [生产更新记录](./b-legacy-production-deployment.md)。C1–C3 的完整管理界面/Playwright、D0–D1 的影子对账、切换演练、正式生产凭证核验与 IAM 交接仍按原阶段推进；未发布版本或切换生产授权事实源。

## 完成清单

| 工作包 | 已闭合执行链 |
|---|---|
| B0 | 专属服务凭证、完整 gRPC 方法 allowlist、固定 owner/user/system capability；所有直接 HTTP、兼容别名及 admin RPC 的外部身份边界；Compose 凭证模板与静态核验脚本 |
| B1 | 用户 CRUD/导出、联系信息与凭证委派、用户路由资格；本人资料/密码/邮箱/OAuth/恢复/登出/删除；本人订单和订阅由财务所有者重验会话及实际归属 |
| B2 | 渠道/账号 CRUD、状态/凭证/OAuth/测试/余额/额度/恢复动作、健康；模型/别名/映射/模型路由/价格组合、导入导出与 canonical/语义隔离管理；路由组 archive、全量成员替换/override、原子批量删除及渠道导出 |
| B3 | 账户/账本/订单/退款、余额重置；兑换码 CRUD/批量/导出；共享 subscription 管理、自购及 quota policy；价格/上游成本、对账、请求尝试/定价证据、财务与经营报告导出 |
| B4 | 日志详情/列表/统计/选择事件/导出/purge、独立正文权限；配置与内容/安全/支付/价格键；健康/告警规则；通知读取、确认、测试、规则管理；admin 代理及总览/路由 section 预检 |

## 服务身份与入口

- `SERVICE_IDENTITY_TOKEN` 只证明当前 caller；接收方 `SERVICE_CALLER_TOKENS` 只配置其需要验证的 caller。重复专属凭证、未知 caller 和缺失 full-method 声明拒绝。共享 `SERVICE_TOKEN` 在兼容认证中标记 `legacy-shared`，不能获得专属系统 capability。
- `platform/security/serviceidentity/registry.go` 与 `domain/authorization/execution.go` 声明固定方法、实际 owner 和精确操作。`GetResourceAuthorization` 没有公开 HTTP annotation；owner 使用自己的连接和独立 operator JWT/JTI 查询，客户端 actor/header/permission 字符串不构成授权事实。
- admin HTTP 和全部 admin gRPC 请求明确标记 external；缺少 IAM 依赖不能退回内部 legacy 用例。重复 operator/reason 元数据拒绝，独立用户凭证进入纯授权 context。ADMIN_TOKEN 救援与普通 IAM 操作者链独立。
- 系统采集、结算、摄入、投递和回调仅接受当前完整方法的 system capability；用户 read/list 不能借用 worker 能力。未知 API 返回 404，不进入 SPA。组织等未交付操作保持 draft/unbound。
- [入口矩阵](./entry-matrix.csv) 有 823 行，经逐项复核 caller、复合动作、字段、范围、事实 owner 和测试契约；检查器同时比较源文件/handler 分支摘要和真实注册。矩阵不生成运行时权限。
- `scripts/check-service-identities.py` 核验 Compose/.env 模板，另支持对指定的已供应凭证文件检查重复/缺失。此次只运行模板检查；生产实际供应与配置核验属于 D1。

## 资源与事务语义

列表、total、分页、导出及聚合在 data SQL 查询之前施加 allow 并集与 mandatory deny；单对象详情/写入使用同一纯范围语义。组读取命中任一允许组，整体写覆盖全部原/目标组。资源 ID 来自实际持久化对象，父对象、来源及引用不能由客户端替换。集合查询对越权行返回空结果；单对象拒绝。

联系信息、渠道/账号凭证、模型价格、财务成本和日志正文分别检查独立权限。成本/member 可见标志区别受限字段与真实零/空值；正文搜索也受 content scope 限制。派生字段不能依赖隐藏来源，兼容更新按实际副作用叠加状态、凭证、映射、价格、成员等权限。单个渠道删除同样验证被级联删除的映射，权限或审计失败回滚对象、关系和 revision。

写入要求 reason 和对应 CAS；创建使用明确创建语义。实际持久化 revision 与对象事实在事务内锁定重读，成功审计与业务写同事务。channel/account 使用自身 revision，映射/别名使用父版本，路由组、模型路由、语义隔离块和配置使用持久化版本。配置删除保留 tombstone，重建继续推进版本，旧 incarnation 的 revision 不能修改重建对象；固定 key mutex 串行化并发首次创建。

每次可重试资源事务开始前刷新身份/授权决策；远端 RPC 不放入可 replay 的数据库回调。撤权、actor/mode 改变、到期及依赖故障中止重试。本人订单/订阅使用实际会话与用户/订单归属；内存实现无法保证持久化审计或 IAM 权威事实时拒绝敏感写。资源与 identity 跨库不宣称全局串行撤权。

OAuth 待完成会话绑定真实 actor、目标来源/组及凭证版本；回调重验授权与归属。通知 ack 使用独立确认状态，不能改投递状态冒充确认；测试发送仅针对获准规则，持久化规则实际影响事件收件人和禁用行为。手动语义隔离 resolve 独立于系统 verdict 写入，模型路由 upsert 区分 create/update 并拒绝陈旧版本。

总览与 routing-ops 在启动 section 查询/RPC 前预检各来源操作，随后仍由每个 owner 过滤。无法表达资源谓词的全局 Prometheus section 要求 all；不能先启动聚合再在响应中过滤。admin HTTP 的 ID/revision 使用字符串，proto 与前端 API 类型按源码生成。

## 迁移与兼容

新增 112–122 三库迁移及 ownership 声明：持久化资源写审计、通知管理、告警规则/订阅 quota policy/兑换码/system options/渠道模型版本、上游成本稳定资源 ID、模型路由/语义隔离块版本、配置 tombstone 与 key mutex。SQLite 全量迁移现为 63 个 runnable 文件；MySQL/PostgreSQL 按各方言实际文件数核验。

历史 bool/数字旗标使用 data 层 `xdb.Flag`，保持 biz 纯模型；配置 key 与 CSV 查询在 data 内处理方言差异。生产保持既有 legacy 配置，新的 IAM 写协议和独立凭证在切换前须随服务及全部迁移交付；不能仅更新 admin 或仅部署前端。

## 验证

2026-10-03 在本机实际 SQLite/MySQL/PostgreSQL 运行 B1–B4 owner race 测试，覆盖用户/路由资格、渠道/模型/映射/组/OAuth/动作/canonical/语义版本/批量导出、账务/订阅、日志/配置/健康/通知。MySQL `13316`、PostgreSQL `15436` 仅监听 localhost；各用例新建隔离库/schema、运行完整迁移与 repeat 后清理，不读取部署 DSN。

跨服务 B0/B1、B2、B3、B4 使用真实 identity gRPC/session/biz/data 和真实资源 owner，覆盖 root、审计、渠道/平台运维、财务、member 的适用角色组合、数值 role 伪造、共享 token、owner/header 伪造、JTI/分配撤销、隐藏来源、字段脱敏、复合写、409 和成功审计。B3 另通过真实 admin HTTP → billing gRPC 验证财务范围、独立成本导出权限与余额 CAS。

验证命令（两个外部 DSN 必须明确指向本机临时库；未配置而跳过不算三库通过）：

```sh
make all
make wire-check
make rbac-contract-check
make migration-check
make verify
python3 scripts/check-service-identities.py

go test -race ./app/identity/internal/data ./app/channel/internal/data \
  ./app/billing/internal/data ./domain/subscription/data \
  ./app/config/internal/data ./app/log/internal/data \
  ./app/monitor/internal/data ./app/notify/internal/data \
  -run '^TestIAMB|^TestIAMTransaction' -count=1

go test -race ./internal/integration -run '^TestIAMB' -count=1
scripts/test-migration-smoke.sh mysql
scripts/test-migration-smoke.sh postgres
```

`make verify` 的后端 unit/race、架构、迁移治理及前端 lint/197 个测试/build 通过；`make all` 和 Wire 装配、823 行入口契约、模板检查通过。生成类型以本次自动生成结果为比较基线；本地使用临时 Git index 验证重新生成无漂移，未改变用户暂存区。SQLite 迁移生命周期/失败门禁、MySQL/PostgreSQL fresh/repeat/negative 与历史元数据预检均通过。未启动部署服务的 E2E suite 与 C3 Playwright 不计作本批通过。

## 剩余门槛

C1–C3 已于后续工作包完成，见 [C 阶段交付记录](./c-management-delivery.md)。本文件前文保留 B 交付时的验收状态。

B0–B4 无待闭合项。后续仍需：D0 影子比较、故障注入与切换演练；D1 实际生产服务凭证、部署/迁移和运营交接。后端验收完成不授权提前切换生产。
