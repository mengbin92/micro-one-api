# Micro-One API v0.49.0 发布公告

> 2026-11-21 · 上一版：[v0.48.1](./release-v0.48.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.49.0)

v0.49.0 是 **MINOR 版本**。管理台、Admin API 和 Billing 为兑换码列表及 CSV 导出增加可选的筛选、排序契约。运营人员可以在全部授权记录中搜索，再导出同一组结果。此版本也修复资金操作重放、并发续期、跨服务凭据暴露和 WebSocket 首次发送后的重复请求。数据库沿用现有表和索引。

## 1. 兑换码查询、导出与领取

旧管理页在当前页做部分筛选，带关键字时改走无分页搜索。现在列表按完整券码或名称前缀搜索，支持停用、可用、已使用三种状态。启用但库存为零的历史券显示为已使用。数据库在同一组权限和筛选条件下计算总数，再稳定排序和分页。CSV 有独立导出授权，忽略页码，沿用相同的筛选和排序。管理页区分服务错误和空结果，批量创建记录经授权的操作人。

过去账本按整张券去重，多次券第一次成功后，其他用户领取剩余份额会遇到冲突。Billing 现在锁定券记录，在同一事务里按用户和券检查历史领取、扣库存、增加余额并写流水。同一用户重试保留原结果的资金效应。新建券和历史券兑现均须为正面额。新兑换流水使用内部券 ID，普通账单详情、列表和导出隐藏历史流水中可再次使用的券码及其派生字段。

**影响**。Billing、Admin、Web。

## 2. 钱包、支付与订阅

- 旧余额重设入口先读后调余额，重放与并发更新可能互相覆盖。新入口携带预期余额与请求键，由 Billing 在事务内锁定钱包、检查原流水，再比较预期值。相同请求返回重复冲突，保留后续发生的余额变动。Admin 透传充值和兑换的业务失败原因，用户账单列表保留真实流水 ID。
- 两笔旧模式续期曾可能读取同一到期时间并覆盖其中一次延长。现在续期读取和写入共用带行锁的事务。购买或升级后发放失败时，Admin 使用稳定的退款请求键，在有上限的独立超时时间内尝试补偿，并检查退款业务结果。变更审计去除了误作原计划 ID 的价格，即时降级记录零扣款。
- 支付主动查单曾在缺少签名或验签材料时接受响应，创建订单时也曾用浮点数格式化金额。现在查单先验平台 RSA2 签名，下单以整数分生成金额，支付对账按同一游标和渠道范围统计总数。
- 混合预扣释放和历史流水回读可能返回负订阅成本。结算回执现在优先读取实际分摊，历史估算以已结算总额为上限。MySQL 对超出列宽的账本去重键保存摘要，已有可直接存储的键继续按原值查找。

**影响**。Billing、Admin。 [资金写入边界](../design/rbac/account-writers.md)

## 3. 渠道事件、令牌与 WebSocket

渠道创建或更新时，旧版会将完整渠道对象发布到跨服务事件总线。新事件只包含路由缓存失效所需的渠道 ID 和状态。内存事件总线隔离单个订阅处理器的异常，并将投递错误返回发布方。

Identity 现在在令牌行锁中同时检查额度模式和结算回执。无限额度的结算也留有回执，之后切换成有限额度，重放同一预留仍返回首次结果。到期时刻即拒绝令牌。内存存储保留令牌展示前缀并复制模型、路由组等可变字段。

Responses WebSocket 的池化和直连拨号都使用同一套上游出站地址与重定向策略。首次向上游尝试写入后，请求可能已经受理，即使尚未收到响应也停止自动切换。使用过的上游连接随会话关闭，未写入的空闲连接按地址和请求头指纹复用、按时回收并在停机时关闭。配置 Redis 时，多副本直接读取共享绑定，遵循远端到期与删除结果。

**影响**。Channel、Identity、Relay。

## 4. 管理写入、运营任务与前端

公告保存、备份策略修改及手动备份入队在事务边界刷新授权，防止排队期间已撤销的权限仍被使用。Config 创建与更新区分有效修订号，已有记录的异常修订号会触发冲突。

运营 worker 原先用整轮扫描开始时间给所有计划检查到期和认领租约。现在逐项取当前时间，跳过已到期的计划，并用实际完成时间写回执。Web 按十进制输入换算四位账本单位，修复分页参数误删、CSV 公式文本防护，以及 SSE 事件恰好达到上限且分隔符被分段传输时的误报。

**影响**。Admin、Config、Web，以及运行 `evidence-worker` 的 `p3-cost-worker`、`p3-repair-agent`、`p3-weekly-review`。备份权限修复位于 Admin 控制面，实际数据备份仍由独立主机 `backup-worker` 执行。 [备份控制面与主机 worker](../../deployments/backup/README.md)

管理台统一修复了第 20 页跳回首页、十进制半单位金额舍入错误，以及 CSV 将部分算式误判为负数的问题。调试台现在能接收恰好达到 2 MiB、且分隔符跨网络分块的单个 SSE 事件。

## 5. 构建、发布与依赖

Compose E2E 按运行生成独立项目并显式使用测试配置，使用真实会话和服务身份完成开户、入账、签发令牌、中继请求及账单核对。容器构建上下文排除本地密钥、证书、凭据载荷和运行数据。发布工作流先校验标签格式、检出对应 Git tag 并检查发布说明，再构建镜像。工具版本检查可在默认 shell 下运行。

Go 依赖 `golang.org/x/crypto` 更新至 `v0.59.0`，同时移除对应 Trivy 豁免。`go.mod` 的 Go 版本保持不变。本版六个业务行为变更服务为 `billing-service`、`identity-service`、`channel-service`、`config-service`、`admin-api`、`relay-gateway`。发布矩阵中的其余服务随版本构建纳入依赖更新。

## 兼容性说明

- 新增的 Protobuf 查询字段和余额重设的 `request_id` 均为可选输入。旧客户端可沿用默认列表排序。HTTP 兑换码列表保留 `data` 数组并返回 `total`，带关键字的调用现在也遵循分页，获取所有匹配项可使用有授权的导出入口。
- 先更新 Billing，再启用新版 Admin 和 Web。旧 Billing 会忽略新增的筛选字段，滚动更新期间应等全部 Billing 副本升级后使用筛选导出。
- 使用平台支付查单的环境须提供有效的验签公钥或平台证书。裸公钥模式使用 `ALIPAY_PUBLIC_KEY` 或 `ALIPAY_PUBLIC_KEY_PATH`。证书模式使用 `ALIPAY_PUBLIC_CERT_PATH` 指定的平台证书验签，并配齐应用证书和根证书。缺少验签材料或响应签名时，查单失败，订单等待已验证的结果。
- **历史 Redis 凭据风险**。升级只改变后续的 `channel.changed` 消息。旧 Redis Streams 条目、持久化文件或备份中可能仍有渠道凭据。按原保留策略和访问记录核查读取范围。确认越权读取或无法排除暴露时，轮换受影响凭据并验证新凭据生效。历史生产核查状态见下表。

## 升级步骤与回滚

1. 保存运行镜像的原始引用、Compose 配置、启用的 worker 版本和宿主 Web 目录。按生产架构在本机交叉构建制品，记录源码提交与镜像摘要。 [部署手册](../deployment.md)
2. 先升级 Billing、Identity、Channel、Config，确认健康和服务间认证，再升级 Admin、Relay。按实际启用的运营任务 profile 切换 evidence-worker 镜像。业务服务外的发布镜像按本环境部署清单核对。
3. 更新宿主机挂载的 Web 静态资源后切换首页。核对首页资源摘要、兑换码列表与导出的筛选范围、匿名受保护入口的拒绝结果，以及后台 worker 健康和重启计数。生产资金检查使用已有订单或流水做只读核对。
4. 如需回滚，恢复上一套 Web 入口和相应服务、worker 的旧镜像与 Compose 配置。保留当前钱包、订阅、领取记录、账本和回执。混版和回滚窗口暂停多次券发放、批量资金重试与依赖新版筛选的导出，回滚旧 Channel 后须重新评估事件中携带凭据的风险。

## 验证

| 范围 | 已取得的结果 |
|---|---|
| Go 测试与并发验证 | ✅ 138 个包通过测试，32 个包通过 race 检查。 |
| MySQL 8.4、PostgreSQL 18 | ✅ 两种独立实库各通过 6 个用例，共 12/12。覆盖并发支付回调与退款、余额重设与导出授权、两笔续期累计、多人领取与重放、负面额保护、筛选排序与分页。 |
| 前端与真实 IAM | ✅ Vitest 816 项通过，真实 IAM 浏览器用例 10 项通过。 |
| 本地发布门禁 | ✅ `make verify` 通过。关键覆盖套件 229 项通过，Compose 完整业务流通过。默认 Playwright 有 131 项通过，桌面项目按条件跳过 1 个仅适用移动端的用例。 |
| 依赖与安全 | ✅ 全仓 `go vet`、项目 gosec 规则和升级后的 `govulncheck` 通过。RBAC 991 条登记与源码一致。 |
| 生产预检 | 九个常驻服务运行中，两个 HTTP 健康入口通过；验签材料可解析。生产切换及回滚点在部署后登记。 |
| 历史 Redis 凭据 | 待核实历史保留范围、访问审计及是否需要轮换。 |

## 完整变更日志

- `docs(runbook): record production announcement verification`
- `chore(deps): upgrade x/crypto and retire the OpenPGP scan exemption`
- `fix(billing): authenticate payment queries and preserve exact cent amounts`
- `fix(billing): make wallet redemptions and subscription renewals consistent`
- `fix(admin): align wallet results and redemption controls with billing`
- `fix(identity): preserve quota receipts across key limit changes`
- `fix(authz): revalidate protected writes and reject wrapped revisions`
- `fix(channel): keep credentials out of change events`
- `fix(relay): prevent WebSocket replay and preserve stream boundaries`
- `fix(operations): keep expired plans from blocking subsequent jobs`
- `test(e2e): isolate compose fixtures and exercise the wallet owner`
- `fix(release): validate tag inputs and exclude private build artifacts`
- `docs(release): v0.49.0`
