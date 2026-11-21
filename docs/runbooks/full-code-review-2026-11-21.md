# 全仓代码复核与生产验收（2026 年 11 月 21 日）

从 `develop@ceb7d0f4` 开始，按实际请求、资金写入、权限和部署入口审查服务端、共享领域库、Web、数据库迁移、Compose 与 CI。v0.49.0 汇集本轮修复，并扩展了兑换码查询与导出接口，因此发布为 MINOR。v0.49.1 专门修正已提交扣费的回执，发布范围为 Billing 的 PATCH。完整变更、兼容性和升级步骤见两份公告。 [v0.49.0](../releases/release-v0.49.0.md) · [v0.49.1](../releases/release-v0.49.1.md)

手工审查重点是跨服务调用、资金与权限边界、并发和失败恢复。生成代码通过重生成和编译校验，历史文档与归档证据通过链接和契约检查，未做逐行人工审阅。

## 已修复的问题

分级依据是缺陷可造成的影响。资金损失、凭据滥用等生产后果须另有现场证据确认。

| 优先级 | 合并后的根因与影响 | 修复及回归证据 |
|---|---|---|
| P0 | 余额重设按旧余额计算差额，兑换码重放与并发续期缺少正确的写入边界。会覆盖并发变动、拒绝第二位合法领取者或少延长一次订阅。历史负面额券还可能扣减领取人的余额。 | 锁定钱包并检查原请求键。检查面额，在同一事务中按用户和券领取入账。续期在锁内累加。 [钱包](../../app/billing/internal/biz/balance_reset.go) · [兑换重放](../../app/billing/internal/data/idempotency_test.go) · [续期回归](../../domain/subscription/data/data_test.go) |
| P0 | 渠道变更事件携带完整渠道对象。支付查单曾接受缺少验签材料或签名的结果，金额在下单时经过浮点换算。 | 事件仅保留路由失效所需字段。查单核验签名，下单以整数分生成金额。 [事件发布](../../app/channel/internal/biz/channel.go) · [渠道回归](../../app/channel/internal/biz/channel_test.go) · [支付检查及回归记录](../releases/release-v0.49.0.md#2-钱包支付与订阅) |
| P1 | 扣费提交后再回读流水，读故障可能被报告为扣费失败。重放时不完整的分摊也可能被当作完整回执。 | 首次同步回执直接取已提交事务的分摊，重放校验分摊总额，证据不足返回可重试错误。异步额度上报使用实际提交总额。 [结算](../../app/billing/internal/biz/billing.go) · [业务回归](../../app/billing/internal/biz/dual_track_test.go) · [数据库回归](../../app/billing/internal/data/request_snapshot_test.go) |
| P1 | 权限决定与实际写入之间有时间差。密钥限额模式改变后，旧请求重放可能再次扣减。 | 公告和备份在写事务边界重新授权，Identity 为限额及无限额结算保留原回执。 [公告回归](../../app/admin/internal/data/announcement_test.go) · [备份回归](../../domain/backup/data/backup_test.go) · [密钥回归](../../app/identity/internal/data/token_quota_test.go) |
| P1 | WebSocket 已尝试向上游写入时仍可能切换并重发。拨号曾绕过已配置的出站策略。故障切换后，原网关副本也可能继续选用旧账户。 | 拨号沿用出站策略。首次写尝试之后停止自动重发。启用 Redis 时以共享绑定为准，读取失败按未命中处理。 [转发](../../internal/server/openai_ws_forwarder.go) · [转发回归](../../internal/server/openai_ws_forwarder_test.go) · [跨副本回归](../../internal/server/openai_ws_pool_test.go) |
| P2 | Admin 丢弃 Billing 的业务拒绝，部分读故障显示为空列表。Web 在当前页筛选导致列表、总数和导出不一致。 | 按实际业务结果响应，服务端在分页前应用同一筛选，导出所有授权匹配项。 [管理适配](../../app/admin/internal/service/admin.go) · [HTTP 回归](../../app/admin/internal/server/http_test.go) · [筛选回归](../../app/billing/internal/data/redeem_repo_test.go) |
| P2 | 已过期的运营计划阻断同轮后续任务。测试 Compose 会继承宿主运行配置。前端金额舍入、分页和流式分段存在边界错误。 | 逐计划取当前时间，隔离验收配置，并对金额、页码、CSV 与流式分隔符补回归。 [调度回归](../../domain/operations/data/tasks_test.go) · [Compose 隔离回归](../../scripts/test-e2e-flow-identities.py) · [分页](../../web/src/hooks/useAdminTableState.test.tsx) · [金额](../../web/src/lib/amount.test.ts) · [流式解析](../../web/src/lib/sse.test.ts) |

## 机器评审与人工判定

| 范围 | 输入与结果 | 如何核实 |
|---|---|---|
| v0.49.0 外部 OCR | 经脱敏的独立变更快照中，47 项进入评审，返回 33 条反馈。 | 快照缺少部分未改动的调用方。逐条回到完整仓库检查实际入口、存储行为与测试。最终增量另用本地 `ocr delegate` 复核 28 个文件，确认并修复跨副本会话绑定读旧值。 |
| v0.49.1 外部 OCR | 初版补丁 3 项进入评审，返回 2 条反馈。 | 最终本地 `ocr delegate` 覆盖业务入口、异步 worker、服务适配和两个回归文件，共 5 个。独立故障注入先复现分摊缺项与重放读故障，修复后原用例通过。 [补丁实现](../../app/billing/internal/biz/async_billing.go) · [错误映射](../../app/billing/internal/service/billing.go) · [补丁验收](../releases/release-v0.49.1.md#验证) |

三类误报用源码和运行结果排除。仓库使用 Go 1.27，`new(*user)` 是有效的表达式初始化，内存仓库复制用户对象的测试已经通过。旧客户端省略幂等键时，`normalizeRequestID` 会生成 `auto:` 键，跨请求重放保护仍要求客户端提供稳定键。WebSocket 的重发边界是**首次尝试写入上游**，即便没有收到首帧也可能已经送达。 [Go 版本](../../go.mod) · [对象复制回归](../../app/identity/internal/data/data_test.go) · [幂等键实现](../../app/admin/internal/service/idempotency.go) · [WebSocket 回归](../../internal/server/openai_ws_relay_test.go)

## 验证与发布门禁

| 范围 | 已取得的证据 |
|---|---|
| Go、权限与安全 | ✅ v0.49.0 的 138 个包通过测试，32 个包通过 race。`make verify`、`go vet`、项目 gosec 规则和 `govulncheck` 通过，RBAC 991 条登记与源码一致。v0.49.1 的 Billing 业务、数据和服务三个完整包通过 race。 |
| 数据库与事务 | ✅ v0.49.0 在 MySQL 8.4、PostgreSQL 18 实库通过 12/12 个用例。v0.49.1 在 SQLite、MySQL 8.4、PostgreSQL 18 上通过并发和回滚 6/6 项。8 个请求重放同一预留只结算一次，两条流水的 625 与 375 合计 1,000。 [数据回归](../../app/billing/internal/data/request_snapshot_test.go) |
| Web 与端到端 | ✅ Vitest 816 项、关键覆盖套件 229 项、真实 IAM 浏览器用例 10 项通过。默认 Playwright 131 项通过，桌面项目跳过 1 项仅适用移动端的用例。Compose 完整业务流通过。 [v0.49.0 验证](../releases/release-v0.49.0.md#验证) |
| 远端 CI 与安全流水线 | ✅ 两版对应的 develop 集成提交均通过 CI 和 Security Pipeline。v0.49.0 标签发布流程已完成。 [v0.49.0 CI](https://github.com/mengbin92/micro-one-api/actions/runs/44866643825) · [Security](https://github.com/mengbin92/micro-one-api/actions/runs/44866643762) · [标签发布](https://github.com/mengbin92/micro-one-api/actions/runs/44867614568) · [v0.49.1 CI](https://github.com/mengbin92/micro-one-api/actions/runs/44868743913) · [Security](https://github.com/mengbin92/micro-one-api/actions/runs/44868743968) |

## 生产验收与保留项

| 发布或检查 | 状态与回滚依据 |
|---|---|
| v0.49.0 九个常驻服务与 Web | ✅ 9/9 个 Linux/amd64 容器运行，源码摘要全部匹配，重启计数均为 0。Admin、Relay 健康检查均返回 200，匿名访问各自的受保护入口均返回 401。Web 首页与入口脚本返回 200，摘要均匹配。可选 worker 保持原有未运行状态。镜像回滚标签为 `rollback-20261121-124534`，Web 回滚目录为 `/opt/web/dist.rollback-5uuzzbdh/dist`。 [完整部署记录](../releases/release-v0.49.0.md#验证) |
| 上线时的业务权限与 Redis 检查 | 运维凭证访问兑换码列表、导出均返回 401。在线 Redis 的渠道事件流和死信流均为空。管理员会话及历史备份的补验见下文。 |
| v0.49.1 单服务切换 | ✅ 单独滚动升级 Billing，镜像 ID 前缀为 `b69b777b2cf8`，回滚镜像标签为 `rollback-20261121-131820`。复核 9 个容器均在运行，重启计数均为 0，源码摘要分别匹配本批预期版本。Billing、Admin、Relay 健康检查均返回 200。9 个服务的启动日志抽样中，fatal/panic 命中数均为 0。 |

生产验收使用只读检查。扣费、重放与回滚结论来自独立测试库的事务和故障注入。线上是否发生误扣或凭据被使用，仍需相应账本或访问审计证据。发布制品、各架构镜像的产出时间及工作流结论保存在 GitHub。 [v0.49.1 标签流水线](https://github.com/mengbin92/micro-one-api/actions/runs/44870214396) · [v0.49.1 Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.49.1)

## 11 月 21 日晚补验

### 管理员会话

**✅ 11 月 21 日 21:28（北京时间），生产管理员会话通过了兑换码列表与 CSV 导出的空结果验收。** 首次尝试曾显示“数据暂不可用”。本次完成页面要求的安全验证后，在同一标签页核对了以下结果。

| 验收项 | 实际结果 |
|---|---|
| 兑换码列表 | 保留随机无匹配关键词、状态“可用”和金额升序。页面明确显示“未找到兑换码”，数据行数为 0。 |
| CSV 导出 | 保持筛选条件，点击“Export CSV”。21:28:38 下载完成的文件为 44 字节。逐字节比对确认文件仅含预期的 7 列 CSV 表头和换行，数据行数为 0，与列表一致。 |

本次覆盖已授权的读取与导出成功路径。验收使用空结果，未取样非空兑换码记录。部署机日志抽查未匹配到此次筛选请求，具体 HTTP 状态码未单独核实。

### 历史 Redis 备份

**可读取的 3 份历史 RDB 中，渠道凭据非空字段命中数为 0。另有 4 份已登记成功的异机归档，其内容仍未知。** 以下时间均为北京时间。

| 备份范围 | 实际检查 | 结论 |
|---|---|---|
| 主机额外留存的两份 RDB | RDB 内部创建时间为 10 月 27 日 10:39:10、13:58:45，与各自文件修改时间一致。两份均通过 RDB 校验和检查，各解析出 1 个键。 | 两份均无渠道事件流、死信流、渠道 L2 缓存或渠道 OAuth 会话键。 |
| 受管异机归档，共 5 份成功记录 | 从维护者留存且摘要匹配的密封副本中核查 11 月 9 日 12:19 的 Redis 快照。RDB 校验和通过，两种解析器均读到 1 个键。 | 这 1 份归档未命中上述结构。异机存储已停止，原数据目录缺失。维护者确认没有其他已知副本，余下 4 份无法逐份读取。 |

检查直接解析快照文件，保留快照时存在、现已过期的键，并解码 LZF 压缩字符串。核对范围包含历史渠道事件中的 API Key、访问令牌和刷新令牌，以及渠道 L2 缓存数组中的非空 key。短期 OAuth 验证材料单列检查。合成正例覆盖流、死信队列和已过期缓存。校验和变造的样本被拒绝。当前持久化文件另有 4 个键，上述结构命中数同样为 0，未计入历史备份。核查未启动备份服务，也未恢复或写入生产 Redis。
