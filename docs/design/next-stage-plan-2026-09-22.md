# 下一阶段计划：可靠性补缺、运行可见性与文章技术债收口

> **当前执行入口**。制定：2026-09-22；更新：2026-09-26。状态：第一批 R1–R4 已随 v0.31.1 发布；第二批 O1–O4 及 O5 正确性修复已完成本地实施和隔离验收，纳入 [v0.32.0](../releases/release-v0.32.0.md) 并已上线。生产通知接收端与 OTel 导出仍待线上验收；生产 Redis 故障不在受限主机注入，其传播/降级延迟已在隔离环境测量完成（第 3 节），生产成本复采保留；生产 V2 回源 RPC 已采低流量基线，见第 3 节。第三批 Q1 安全头、取消终态、历史快照与充值状态均已上线；Q2/Q3 的可复现门禁已补齐，生产 MySQL Dashboard 索引同负载对照、支付宝沙箱正常支付往返、F17 故障子项隔离验收与当前提交 Linux/amd64 三次全量 k6 均已完成（Q3 基线重定至 `v0.32.2`，见第 4 节）。详情见第 4 节。
>
> 初始规划基线：`develop@77e4db45`（[v0.31.0](../releases/release-v0.31.0.md)）；第二批审查基线：`develop@d70e02f2`（已合入 [v0.31.1](../releases/release-v0.31.1.md)）。第一批实现与隔离验收记录已纳入版本；第二批本地证据见第 3 节，不代表生产验收。
>
> 来源：桌面 `/Users/neo/Desktop/micro-one-api-articles/` 的 04–25 共 22 篇原文、用户补充的四组建议，以及仓库既有验收记录。`wechat/` 是发布副本，不重复记债；目录中没有 01–03 原文，不推测其内容。
>
> 用户于 2026-09-23 纠正边界：**恢复既有 Responses→Chat/Anthropic 转换及默认兼容行为；仅对确实无法转换的能力明确报错。保留流式中断、禁止重复执行、正确结算和显式同模型故障切换。**

> **后续执行更新（2026-09-23）**：用户确认按推荐顺序从 D1 推进。本轮完成 D1 的多 Relay OAuth 协调、负载退化告警及部署边界，**已通过本地/隔离验收并于 12:25–12:28 UTC 上线**；实现、文档及部署证据随本次 D1 提交归档。生产保持单 Relay、单 channel，已启用 `redis` 协调；当前没有 OAuth 账号，真实供应商轮换及多副本故障切换仍待验收。D2–D7 按条件保留，前三批仍缺外部证据的验收不因此关闭。

## 1. 阶段范围和现状纠偏

本阶段优先解决会丢凭证、无法隔离故障、错误与计费归属不清的缺口。文章中的所有遗留项在第 6 节登记去向；“登记”不代表全部纳入本阶段发布。暂不预定发布版本，按验收完成的批次决定 PATCH/MINOR。

以下旧描述不能直接当作当前待办：

| 文章/建议中的描述 | 当前证据与结论 | 本阶段处理 |
| --- | --- | --- |
| OAuth Store 失败只有日志 | [base_token_provider.go](../../domain/upstream/credential/base_token_provider.go) 已有三次短重试和 `CredentialPersistFailures`；仍用标准库日志，未检出对应告警规则 | 补持久化状态、主动补写及告警，不重复造计数器（R1） |
| outbox 没有积压监控 | [outbox 指标](../../platform/routingoutbox/report.go) 与 [P1-1 验收](../runbooks/p1-acceptance-2026-09-15.md) 已有积压、年龄、失败和恢复证据 | 复用；承接外部告警送达与毒事件处置（O2、D3） |
| raw 路径完全没有模型健康 | `handleRawRelay → ExecuteWithCandidates → recordHealth → RecordModelHealth` 已贯通，见 [raw handler](../../internal/server/http_raw_handler.go)、[retry.go](../../internal/biz/retry.go) | 修正旧结论；核验成功/失败覆盖和恰好一次记录（O1） |
| 不同来源 ID 冲突；候选每次重算 | [RoutingSourceIdentity / RoutingCandidateList](../../internal/biz/relay.go) 已实现；[候选回归](../../internal/biz/retry_candidates_test.go) 含同号跨来源 | 保留回归；候选耗尽仍有重新选路分支，需明确策略，不宣称候选永不扩展（R2、R3） |
| SSE 没有空闲超时 | [stream_timeout.go](../../domain/upstream/provider/stream_timeout.go) 已按字节活动滑动计时，流式客户端没有 `http.Client.Timeout` | 检查各入口接入，分开总预算/首包/空闲语义（R4） |
| 所有数据库 smoke 只有 MySQL | [CI](../../.github/workflows/ci.yml) 已跑 MySQL/PostgreSQL；[共享 E2E](../../.github/workflows/e2e.yml) 含 MySQL/SQLite 路由；v0.30 有三方言升级证据 | 只补历史 schema 语义及未覆盖场景（Q2） |
| `isValid` 永远 true | [handleSubscriptionUsage](../../internal/server/http_status_handler.go) 已在未配置/无订阅时返回 false，有订阅时按状态计算 | 保留兼容别名；补字段契约及冻结额，不删字段（O3） |
| Playground 不显示请求 ID | [PlaygroundPage.tsx](../../web/src/pages/PlaygroundPage.tsx) 已有请求检查器和错误 ID 展示 | 补与根请求/OTel 的关联和端到端证据（O2、Q1） |
| 服务器完全没有 CSP 实现 | [SecurityHeaders](../../platform/middleware/security.go) 已有 CSP，但尚不能据此证明 admin 静态页面接入或生产生效；其 `connect-src 'self'` 也不满足跨域 Relay | 核对静态服务与入口代理、补适配后的头及浏览器验收（Q1） |
| v0.30 仍是下一阶段 | v0.30 路线图各项已完成；v0.31 已发布并补齐结算恢复、尝试追踪、通知重试等 | 旧路线图归档，本文件接管剩余事项 |

## 2. 第一批：请求与凭证可靠性（R）

R1 → R2 → R3 → R4 已完成实现及验收，见[第一批实施记录](../runbooks/first-batch-reliability-2026-09-22.md)。本批实现、回归测试与验收记录一并提交，提交记录见本文件 Git 历史。下表所述风险来自静态核对，新增缺陷先用最小回归复现，不以文章断言代替测试结果。

### R1 · P0：轮换凭证保留、周期补写和告警

**状态：已完成（2026-09-22，本地/隔离验收，未部署）**。

**入口**：[base_token_provider.go](../../domain/upstream/credential/base_token_provider.go)、[token_cache.go](../../domain/upstream/credential/token_cache.go)、[refresh_task.go](../../domain/upstream/credential/refresh_task.go)、[AccountLookup 契约](../../domain/upstream/credential/token_provider.go)。

除了文章所述“冷账号无人请求就不补写”，当前路径还有三处需要先复现的缺口：

- Store 持续失败时 `Refresh` 仍返回 nil；后台 `refreshAccount` 随即 `Invalidate`，可能清掉唯一一份新 refresh token。
- `resolve(force=true)` 和凭证临近过期时会读取持久层，未优先使用待落库的轮换凭证。
- `resolve` 命中完整凭证后调用 access-token-only 的 `cache.set`，会丢掉完整凭证字段；当前热路径返回缓存也不补 Store。因此“下次 resolve 自动补写”并不是可靠保证。

**最小实现范围**：

1. 缓存明确保存完整凭证及 dirty/待持久化状态；刷新成功与持久化成功分开表达，不能因“请求可继续”就清除待补写数据。过期再刷新也必须基于最新 refresh token。
2. 复用现有后台 sweep，先扫描待补写项，再做到期刷新；待补写集合不能只来自 `ExpiringSoon`，否则长有效期的冷账号仍会漏掉。补写仅 Store，不再调用 OAuth 刷新接口。
3. 写回使用版本比较/条件更新，防止覆盖人工重新授权或另一写入者的新凭证。当前 `AccountCredentials`/`Store` 无 revision/CAS，不能假定已有版本字段：沿 channel 的 biz repo → data → RPC 增量补齐；需要迁移时三方言同步，生成代码按仓库工具生成。
4. 保留短暂 Store 重试；跨分钟故障交给周期补写。统一结构化、脱敏日志，沿用失败 counter，新增待补写数量/最老年龄及成功补写信号；标签仅用有限平台/结果，账号 ID 只进日志。
5. 告警初始建议：5 分钟内持久化失败增量大于 0 发 warning；待补写年龄超过两个扫描周期发 critical。通过规则测试校准，恢复后自动消警；修正“已有 Redis 实现”和“下次 resolve 会补写”的注释。

**验收**：Store 连续失败后凭证仍可用；无新用户请求也能在数据库恢复后的下一轮补写；后台成功 hook 不清 dirty；强制/到期刷新用最新凭证；人工重授权版本冲突不被覆盖；不泄露 token；故障与恢复均有指标和告警证据。

**明确边界**：进程内补写不能消除“持久化持续失败且进程强杀”的窗口，本项不得宣称跨重启无损。恢复写入前避免计划重启；强杀后须明确暴露重新授权需求。多副本协调另见 D1。

### R2 · P1：低流量熔断与候选完整性

**状态：已完成（2026-09-22，本地/隔离验收，未部署）**。

**入口**：[普通渠道 selector](../../app/channel/internal/biz/selector.go)、[账号 selector](../../app/channel/internal/biz/account_selector.go)、[账号选择循环](../../internal/biz/relay.go)、[重试健康归并](../../internal/biz/retry.go)。

- 两种 selector 都有“60 秒内至少 10 个样本且错误比例 > 50%”的条件；新增连续失败阈值，与比例条件取或。建议首版 `N=5`，显式配置且校验正值；独立于 60 秒样本窗口，在成功后清零。
- 连击以“同一请求、同一来源的最终健康结果”为单位，同源内部重试不能凑满 N；沿用已有错误分类，客户端取消、本地并发/RPM拒绝、计费提交失败和模型专属错误不能误伤整个来源。
- 对用户建议的“直接半开”，本计划采用现有状态机的受控恢复：触发后 open 冷却，再 half-open 放行一个探测，失败重新 open、成功清理连击和旧失败窗口。直接放行全部半开请求不能解决低流量故障。需验证多并发探测时确实只有一个名额，不能只看 sentinel/常量。
- 普通渠道与订阅账号同号继续用 `(kind,id)` 区分，覆盖排除集、健康统计与审计。候选快照保留请求内顺序；所有重选仍须重新验证撤权和可调度性。
- 清除“固定尝试 8 次，池中第 9 个账号可用却返回无账号”的上限问题：优先消费现有候选/服务端排除能力，受请求预算约束且检测无进展，不简单改成更大的魔数。已核实账号 selector 的 Acquire/Release 由 `RecordSubscriptionAccountSlot` 生产 RPC 调用，保留并修正空转描述。

**验收**：不足 10 个窗口样本、跨窗口累计 N 次连败会隔离；中途一次成功清零；比例熔断仍生效；半开并发仅一个探测；同号来源互不污染；8 个被封账号后仍能命中可用账号；成功探测不被旧失败样本立即再次熔断。

### R3 · P1：明确能力错误、统一 501、保留受控故障切换

**状态：已完成（2026-09-22，本地/隔离验收，未部署）**。

**入口**：[provider factory](../../domain/upstream/provider/factory.go)、[routes.go](../../internal/server/routes.go)、[retry.go](../../internal/biz/retry.go)、现有兼容性矩阵和路由审计。

- 明确允许的 provider 类型/别名和协议能力。已声明但未实现的原生类型保持明确错误；未知数字类型不得走默认 OpenAI 分支。部署前只读盘点存量类型，明确兼容类型沿用显式枚举；不因配置错误发送上游请求。
- 未实现且无法转换的能力错误类型化并禁止重放；保留既有协议适配。OpenAI-compatible Responses 404/405/501 允许同来源转换一次到 Chat；Anthropic 直接转换，状态资源能力单独校验，原生成功不转换。
- 网络错误、可重试上游失败只按显式策略在同一公开模型的授权候选间切换；不换成另一公开模型。每次 attempt 固定实际来源、映射后的上游模型及计费预留，复用 v0.31 根请求追踪。最终错误带 request ID 和已授权、非敏感的 channel/source 标识，服务端审计记录完整尝试；无已选来源时不捏造 ID。
- 所有已实现和明确不支持的路由之后，最后注册 `/v1/` 前缀兜底，复用同形状 OpenAI 501 handler；现有 29 个声明保留能力清单作用。已匹配资源的 404、已匹配端点的方法错误、鉴权限流、CORS/OPTIONS、body limit 仍按现有契约处理；`/api/*`、`/healthz`、`/metrics` 不受影响。
- 兜底路径使用固定指标标签，不能把任意 URL 放进 Prometheus。流式一旦向客户端开始输出，禁止换上游；账务提交失败禁止重放上游。

**验收**：未知 `/v1/new_endpoint` 为同形状 501；精确/前缀已知路由仍优先；未知类型和预先可判定的不可转换能力零上游调用；支持转换的请求通过新旧入口、流式和非流式矩阵。转换后仍不支持的能力终止执行；受控同模型切换有完整 attempt/计费归属；不同模型、无权限来源、已出流、提交失败均不触发替代执行。

### R4 · P1：SSE 空闲超时与总预算分离

**状态：已完成（2026-09-22，本地/隔离验收，未部署）**。

**入口**：[共享 SSE client](../../domain/upstream/provider/stream_timeout.go)、[adaptor 接入](../../internal/server/http_adaptor.go)、[SSE 转发](../../internal/server/http_raw_helpers.go)、[下游韧性](../../platform/grpc/resilience.go)。

- 复用现有滑动字节空闲超时，不另写 SSE 计时器；检查 Chat、Messages、Responses、provider/adaptor、legacy/orchestrator 的接入一致性。
- 分别定义连接/响应头等待、流式空闲、可选总时长、非流式请求总预算。默认保留健康长流行为，不把非流式 timeout 直接当流式总时长；配置的总预算始终不能超过调用方 deadline。
- keepalive 注释行算网络活动，可重置空闲计时，但不能延长总预算；业务“多久没生成内容”不冒充网络空闲。没有 keepalive 的上游允许明确的空闲参数覆盖，不能靠无限拉大所有超时处理。
- 各次重试及退避共享剩余请求预算；SSE 断流/用户取消关闭上游、释放并发槽，并按现有 usage/结算规则进入一致终态，不统一猜测为成功或退款。
- 区分首包超时、空闲超时、总预算耗尽、客户端取消；复用有限标签观测。
- gRPC 下游 breaker 单独验证：按 identity/channel/billing/log 允许有限的阈值、冷却和预算覆盖，保持鉴权/计费 fail-close、日志故障不阻断请求。默认关闭不是自动开启的理由，故障恢复与误熔断验证通过后再决定启用范围，并同步“连续失败”与实际比例判断不符的注释。

**验收**：注释 keepalive 保活、静默流超时、持续数据长流不被非流式超时截断、持续 keepalive 不能突破显式总时长、取消无 goroutine/槽位泄漏；R3 的已出流不切换和结算不重放同时通过。补 [既有验收记录](../runbooks/data-flow-next-stage-acceptance.md) 中尚未完成的流式跨来源隔离矩阵。

## 3. 第二批：观测与额度解释（O）

| ID / 优先级 | 最小范围与依赖 | 可验证的完成条件 |
| --- | --- | --- |
| O1 / P1 路由注册强制观测 | **已完成并上线，本地验收已通过**。注册强制声明执行/只读/不支持类别；请求终态、实际 source/model、raw 健康与重试结算一次性记录统一。审查补齐中间件前置拒绝、1xx、panic、内部预算超时和固定路由标签；证据归入下节 | 新执行路由缺少观察声明时测试失败；成功、最终失败、取消、501/405、鉴权失败各有正确请求终态。用量仅对实际执行记录，不给 501/鉴权失败造 token；同源重试健康一次、账务一次，raw 不漏模型健康；`Flusher`/`Hijacker` 等能力不被包装破坏。依赖 R3/R4 |
| O2 / P1 告警与追踪闭环 | **实现及本地验收完成，生产送达待验收**。七类对账差异完整；HTTP span/兼容头/路由审计/Playground 关联；Alertmanager 接既有通知队列，新增送达、Redis 降级、去重冲突和探测用量看板 | 七类差异“仅此一类非零”的通知都解释得清；用户提供的请求 ID 能定位根请求/attempt/trace；本地 firing/resolved 通知真实 HTTP 接收并持久化 sent；无接收端为 failed/not_configured。生产验收不得以规则或 queued 日志代替 |
| O3 / P1 订阅用量可解释 | **已完成（本地验收）**。billing 事务内读取结算及订阅部分冻结，admin/relay 使用同一 RPC；UI 展示 settled/frozen/available；旧 used/remaining 保留，补无限/超限与窗口/倍率契约；修正过期后购买为新建 | 三方言覆盖在途多请求、取消、跨窗结算、nil/0、过期、倍率变化；接口覆盖权威失败不回退，DTO 保留 null/0，浏览器覆盖无限/超限。见[接口契约](./subscription-usage-api.md) |
| O4 / P1 账号治理与原子重置 | **已完成（本地验收）**。ChannelRepo 强制原子重置，陈旧扫描不得清除新窗口用量；读写共用窗口计算。manual 服务端筛选、原因/等待时长在手机和桌面均可见；Anthropic/Codex 探测 token 单列 | 故障注入后回滚 reset-run 并可重试；三方言双副本仅成功一次；过期扫描不清新用量；无额度配置的 manual 账号也能显示恢复信息；上游不返回 usage 时明确 missing，不推算美元成本 |
| O5 / P2 缓存与降级边界 | **正确性修复与隔离边界验收完成；隔离 Redis 故障传播与降级延迟测量已完成（2026-09-26）**。坏值比较后删除、回源；缓存写入与 TTL 原子设置；移除无人调用的失效接口、整片失效改名 InvalidateAll、修正事件接线注释；V2 继续绕过缓存 | 坏值不计命中且回源失败仍清理；legacy Redis 断开后的 L1 30s 边界由时间推进测试验证，隔离 e2e 进一步验证 L1 全量吸收突发（零回源、chat 全成功）；V2 撤权下一请求回源拒绝。2026-09-26 隔离故障注入：Redis 停止期间 V2 每请求回源全部成功、GetAuthSnapshot P95 4.06→8.9ms 有界上升、恢复无持续退化（[runbook](../runbooks/o5-redis-fault-isolation-2026-09-26.md)）；生产成本复采仍需代表性流量，低流量基线不支持启动缓存优化的结论不变 |

统一包装只负责观测与强制接入，**不承担业务计费或自行解析响应猜 token**。业务终态仍由 biz/执行器/结算路径产出，避免把“忘记记录”变成“记录两次”。

### 第二批审查及验收记录（2026-09-23）

基于 `develop@d70e02f2`，实现提交 `f0d31ecf`，纳入 v0.32.0。用户确认第二批已部署；2026-09-23 只读核对 admin、relay、channel、billing、notify 容器均运行，线上 `latest` 镜像创建于 05:44–06:03 UTC。镜像时间与容器状态不替代外部通知送达、OTel 导出和缓存成本的线上验收。本节合并 O1 验收内容，不单独保留 O1 runbook。

| 审查发现 | 修复与回归证据 |
| --- | --- |
| 内部 deadline 被误记普通错误，panic 没有终态，103/重复 WriteHeader 干扰实际状态 | `route_observation_test.go` 覆盖内部预算、panic 继续抛出但记录一次、1xx、可选 writer 接口、CORS；HTTP 指标保持首次最终状态和 Flush/WS 语义 |
| 通用 HTTP 指标中间件顺序错误，前置拒绝漏记，资源 ID 可能进入 path 标签 | 生产链指标前置；注册模式作为固定 path 标签，执行/只读/不支持分开统计 |
| 对账告警缺两类详情，追踪 ID 未贯通终态审计 | 七类独立通知测试；W3C 父 span→注册路由→Plan→终态的关联测试，取消后持久化审计仍保留 trace ID；Playground 在错误响应头阶段即可记录 ID |
| 订阅页面不含订阅部分冻结，跨窗/倍率显示易误导 | billing 行锁下读计数和预留；三方言测试验证逐预留倍率、取消释放及跨窗结算；无活跃订阅/过期/零用量/null/0 回归 |
| sweeper 非原子接口及陈旧快照可丢新窗口用量；恢复信息被无额度分支和手机隐藏列遮蔽 | 强制原子仓储接口，陈旧窗口保护；SQLite 故障回滚/重试，三方言并发重置；浏览器复现后将恢复信息放到账号名称下方 |
| L2 损坏/null/空值长期残留且计入命中 | miniredis 回归先失败后通过；比较删除避免误删并发替换，回源失败也不保留坏值 |

已执行：受影响 Go 模块（relay、billing、channel、admin、notify、subscription、cache/tracing/metrics）完整包回归；关键钱/权限/并发包 `go test -race`；Wire/分层检查；MySQL 8.4、PostgreSQL 16、SQLite 的订阅冻结与重置测试；Prometheus 三组规则测试、Alertmanager `amtool check-config`、部署文档检查（四组 Compose 配置、41 个 Kubernetes 资源及 82 个配置引用）、195 份 Markdown 本地链接及 diff 空白检查；前端类型检查/lint、相关 23 个单测、桌面/手机 4 个 Playwright 用例及截图。浏览器 API 使用隔离 fixture，不代表线上数据验收。

主要复现入口：

发布候选另完成 `make all`、完整 `make verify`（含 195 个前端单测与生产构建）、`make test-integration` 和全量 gosec；本地文档链接检查随发布材料增加为 196 份。

```bash
go test ./internal/... ./app/billing/internal/... ./app/channel/... ./app/admin/internal/... ./app/notify/internal/... ./domain/subscription/... ./platform/cache ./platform/grpc/... ./platform/tracing ./platform/metrics ./platform/subscriptiondto ./platform/middleware
go test -race ./internal/biz ./internal/server ./platform/cache ./platform/tracing ./app/billing/internal/biz ./app/billing/internal/data ./app/channel/internal/biz ./app/channel/internal/data ./app/notify/internal/server
./scripts/check-architecture.sh
# 设置隔离测试 DSN 后运行；测试创建/删除独立临时数据库
go test ./app/billing/internal/data ./app/channel/internal/data -run 'TestSubscriptionUsageAuthoritativeWindows|TestQuotaResetConcurrentAcrossDialects' -count=1
docker run --rm --entrypoint /bin/promtool -v "$PWD/deploy/prometheus/alerts:/rules:ro" -w /rules prom/prometheus:v3.6.0 test rules credential.test.yml routing.test.yml operations.test.yml
# web/ 下：npm test -- src/pages/SubscriptionsPage.test.tsx src/pages/admin/SubscriptionAccountsPage.test.tsx src/lib/relay-playground.test.ts src/pages/PlaygroundPage.test.tsx
# web/ 下：npx playwright test e2e/operations-usage.spec.ts
```

部署后待验收：真实通知接收端的 firing/resolved 回执与 sent 记录、OTLP collector 导出及真实 Redis 故障传播延迟。2026-09-26 的[生产 V2 回源低流量样本](../runbooks/evidence/o5-production-rpc-baseline-2026-09-26.json)已记录过去 24 小时 RPC 调用增量与 P95，但近 5 分钟没有调用，不能代表故障/高负载延迟；不据此启动缓存优化。未向任何外部接收人主动发送测试通知。当前 OTel 关联覆盖 Relay HTTP span 和路由审计，生产没有配置 exporter/collector，不宣称所有下游服务都生成 span。告警桥接、缓存应急操作及指标用途见[运维手册](../runbooks/routing-observability-runbook.md)。上述外部条件按原退出门槛继续登记，不以本地规则通过代替生产验收。

## 4. 第三批：前端、验证与性能（Q）

| ID / 优先级 | 范围 | 退出条件 |
| --- | --- | --- |
| Q1 / P1→P2 Playground 和控制台 | 安全头及浏览器取消链路优先；复用现有 CSP 中间件并适配实际 Relay origin/样式需求，先验证再启用。核对每次请求历史快照，编辑历史/重发语义作为独立功能；RechargePage、残余 slate/硬编码图表色按组件迁移，深浅色逐状态检查 | admin 静态页面真实响应头和浏览器无误拦截；Key 不落存储/日志/URL；停止后服务端上游/预留终态可回读；请求检查器与审计一致；充值/图表键盘可用、对比度合格。Markdown 另见 D7，不因排版需求放松 XSS 防线 |
| Q2 / P1→P2 可重现验收和资料治理 | 承接 v0.31 F17 支付沙箱往返、流式隔离矩阵；raw E2E 验证默认模型/模型映射/预扣/成功/失败/释放。补数据库历史基线 schema 语义及 manual DDL 的 owner/前置条件/验证；增量变更选取相关 E2E 到 PR，保留快速 verify 分层 | 复用 mock upstream/共享 workflow，不新建测试框架；记录 commit、镜像、场景、证据及剩余边界。F17 正常支付沙箱往返已有证据，未覆盖的故障子项继续待验收；三方言不是只检查文件名。修正旧验收文档首部与末尾待办矛盾；事故模板记录发现时间/影响范围，未知历史明确未知 |
| Q3 / P2 性能基线和门禁 | 复用 benchmark/构建产物统计：Linux/amd64 代表负载、至少 3 次取中位数；首屏 JS/CSS/字体字节预算、Dashboard 索引 EXPLAIN/同负载对照；测量余额新鲜度再调 staleTime，补高频内部跳转预取。定位 executor 流式回归前维持 legacy | 原始样本、样本量、P95/分配/字节数可重现；索引确实受益再保留结论。明确 20% 回归线和 5/5 E2E 是工程准入线而非统计保证；perf fixture 随代表性协议变更维护，基线脚本化。Go 升级前复测 jsonx，不能因文章结论永久冻结版本 |

### 第三批实施与验收记录（2026-09-23）

本节安全头与取消终态修复已纳入 [v0.32.1](../releases/release-v0.32.1.md)，同时包含 SQLite 创建分组锁冲突修复。发布候选 `make verify` 与 `make test-integration` 通过；其余 Q1–Q3 及生产外部验收按下文保留。

Q1 安全头首段基于 `develop@b733af59` 实施。admin-api 的 SPA 页面与资源响应复用现有安全头；文档 CSP 将 `connect-src` 限定为同源及 `ServerAddress` 的 Relay origin，未配置时可由 `ADMIN_WEB_RELAY_ORIGIN` 对齐前端构建回退地址。非法地址不进入策略，脚本保持同源，运行时图表/进度条的样式属性可用。实现及配置说明见 `app/admin/internal/server/http.go` 和 `web/README.md`。

本地证据：`go test ./app/admin/internal/server ./platform/middleware -count=1` 通过，覆盖真实 admin 页面/SPA 回退/静态资源响应头、配置变更与非法地址；`web/` 下 `npm run build` 通过；生产构建经 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4174` 注入同形 CSP 做隔离浏览器验证，跨域模型与请求成功，请求 ID 可见，无 CSP 违规、页面异常或 Key 落入浏览器存储。该浏览器验证使用 mock Relay，不能代表生产反向代理响应头或服务端账务终态。

线上部署与验证：2026-09-23 07:02 UTC 通过 `scripts/deploy-update.sh admin-api` 在本地构建 linux/amd64 并更新生产 admin-api，现镜像 `sha256:5b36c959f0949fa0e6cb098ec7c78ff2e2c6537e293678d2ae7b04ea73ab4461`，旧镜像保留为 `rollback-20260923-150049`。部署前服务直连 `/playground` 为 200 且无 CSP；部署后为 200，CSP 含 `connect-src 'self' https://api.mengbin.top`，资源 JS 与 `/healthz` 均为 200，容器运行且启动日志无错误。通过临时 SSH 隧道使用真实线上 admin 响应头执行 `node scripts/verify-admin-csp.mjs http://127.0.0.1:4300 https://api.mengbin.top --live-header`，隔离 mock API/Relay 浏览器检查通过；未使用真实 Key 或执行线上模型请求。镜像仅证明本次 admin-api 更新，不将第二批外部验收判为完成。

Q1 渠道 9 线上取消验收（2026-09-23）：当前 StepFun/channel 9 已改为 `step-3.7-flash`、`step-5-preview`，不再使用历史 `step-explore`。正式 `https://console.mengbin.top/playground` 的 GET 为 200，公网代理保留 `connect-src 'self' https://api.mengbin.top`；正式来源的 Relay CORS 预检通过。使用限模型、4096 tokens、30 分钟测试令牌在真实 Playground 发送 `step-3.7-flash` 流式请求，HTTP 200 后点击“停止”：页面显示已停止，浏览器请求为 `net::ERR_ABORTED`，无 CSP 违规、页面异常或 Key 落入浏览器存储；根请求 `playground-chat-a4e0b452-e2c3-4df6-ad76-96f1b024a0d8` 的管理 attempt 回读为 channel 9、`released`、实际费用 0，未有消费账本分录。Relay 将该断流计为 `stream_error`，不是 `canceled`；上游 HTTP 请求绑定取消上下文且关闭响应体，但 StepFun 的 TCP 连接在一次 API 取消后保持 `ESTABLISHED`（可能连接复用），没有供应商侧逐请求取消确认，因此只确认 Relay 边界停止转发和账务释放，不宣称供应商内部已停算。两枚测试令牌已停用。完整脱敏样本及一次完整结算的对照请求见[渠道 9 取消证据](../runbooks/evidence/next-stage-channel9-cancel-2026-09-23.json)。

Q1 取消终态修复与复验（2026-09-23 09:29 UTC 部署）：基于 `6b228152` 工作树修复 legacy Chat/Messages/Responses 的流中断返回值，显式标记响应已开始后的不可重试错误，避免释放预留后仍写成功审计，也不向已经开始的 SSE 追加 JSON 错误。取消和请求超时分别记录为 `canceled` / `timeout`；orchestrator 在流关闭结算时保留取消原因及最终来源。Chat 写出中断后在释放预留前取消上游流，避免清理 RPC 延迟阻塞上游停止。

本地 `TestClientCancellationReachesUpstreamAndFinalizes` 的 10 个场景通过真实 HTTP 链路确认受控上游收到请求取消，覆盖 HTTP/1.1、HTTP/2、legacy/orchestrator 和请求预算超时，断言只预留/释放一次、不提交、不重试，审计及指标终态一致。四个相关 Go 包完整回归、上述取消/断流用例三轮 `-race` 和分层检查通过。HTTP/2 验收对象是请求流；底层连接可以复用。

本地交叉构建后上线 Relay 镜像 `sha256:638c8979c25f0f1d233459a6a25a9b81f6c3f6b7300bd7b255d8d56131c64007`，回滚标签 `rollback-20260923-172746`。渠道 9 真实 API 复验：Messages 根请求 `stage3-cancel-fixed-msg-1790156191446`（837ms 取消）和 Chat 根请求 `stage3-cancel-fixed-chat-1790156194728`（381ms 取消）均在首个 SSE 事件后、正常终态前断开，执行路径为 legacy；两者管理端回读均为一次尝试、channel 9、`released`、费用 0、无消费分录，最终路由审计为 `canceled`，对应执行指标各增加 1。全部本轮测试令牌已停用。部署后本轮验证为真实 API 调用，浏览器停止按钮的证据沿用前一次测试，未重复浏览器测试。

Q1 的 Relay 可控取消边界（请求取消向上游传播、停止转发、预留释放及终态回读）已有上述证据。StepFun 内部是否停止推理仍需供应商逐请求确认，不能由 TCP 状态或网关审计推断；此项保留为供应商侧证据边界。历史请求快照、充值页主题 token、键盘语义和深浅色浏览器检查已完成，并于 2026-09-24 更新生产静态站点；图表既有 token 与排名标签页键盘回归复核通过。Q2 已补 raw 默认模型/映射/预留/失败释放 E2E、历史迁移语义核对、PR 按路径触发 E2E 和事故模板；2026-09-26 [支付宝沙箱正常支付往返](../runbooks/evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)确认订单 `paid/issued`、充值账本和幂等 claim 各 1 条；同日 [F17 四个故障子项完成隔离故障验收](../runbooks/f17-fault-acceptance-2026-09-26.md)（丢回调查单收敛、查单暂时失败重试、重复签名回调幂等、发放失败恢复，含 HTTP 边界负向证据），注入过程中发现并修复了幂等重放覆盖 provider 单号的非资损缺陷（`8a1abde9`）；查单重试无退避/告警等剩余边界按 runbook 记录，不自动扩scope。Q3 已补构建字节预算、三样本中位数回归门禁和 Dashboard 同 fixture 索引证据；2026-09-26 [生产 MySQL 同负载对照](../runbooks/evidence/q3-production-mysql-dashboard-index-2026-09-26.json)证实新索引使用了 type 条件，但同一高用量用户旧/新索引各三次中位数为 229/225ms（仅 1.75%），不得将 SQLite fixture 的 56.82% 收益外推到生产。当前提交的 Linux/amd64 三次全量 k6 已于 2026-09-26 由 `Q3 Linux amd64 comparison` CI job 完成：归档基线 `ff518b1` 对 develop 的完整对照（run `36218523742`）测得 chat P95 +25.2%、aggregate P95 +25.3%（436 个提交的功能成本，证据与处置见 [Q3 基线重定 runbook](../runbooks/q3-rebaseline-2026-09-26.md)），基线随之重定到 `v0.32.2` 并重跑通过（run `36224859013`，七项门禁全过）。完整本地记录见[第三批 Q1–Q3 验收](../runbooks/third-batch-q-acceptance-2026-09-23.md)；第二批生产待验收项不因此关闭。

Q1 安全/取消和 Q2 已发布能力验收可以前移到第一批收尾。样式整理、预取、阈值调优不阻塞凭证修复。

## 5. 条件启动的后续功能与技术债（D）

| ID | 明确保留的待办 | 启动条件与最低边界 |
| --- | --- | --- |
| D1 多副本凭证/负载 | **本地/隔离验收完成，已上线（2026-09-23）**。共享凭证回源、刷新互斥与持久化占用、账号/登录限流退化告警；selector 明确为单 channel owner | 生产已应用迁移 109 并启用 `RELAY_CREDENTIAL_COORDINATION=redis`；Redis 租约只做准入，数据库 revision/pending 保护不确定 OAuth 结果。持锁者崩溃、过期锁、迟到写回及重授权已隔离验收；生产无 OAuth 账号，真实轮换/多副本故障切换待验收。多 channel 共享 selector/会话仍未实现；本地限流降级不承诺故障时全局 cap |
| D2 额度/订阅新产品 | 账号月窗口、用户自然月（现为 30 天）、本地 USD 与上游百分比标定、用户预约/账号排队、部分退款、升级补差/降级续费 | 有明确平台/套餐或退款需求再做。自然月是新增契约，不能静默改变旧订单；部分退款含累计金额、幂等和权益处理；外部原路退款仍属单独支付能力，不把站内冲正当原路退款；不凭百分比推算未经校准的美元额度 |
| D3 事件/缓存规模治理 | outbox 毒事件隔离与人工重放、积压自适应轮询、精确失效反向索引、预热、V2 版本缓存、全量 sweeper/对账增量化、差异确认/忽略 | 毒事件先故障注入和处置手册，出现队首阻塞再补有界次数/隔离/重放；Redis 不可用不是毒事件，不能跳过丢授权事件。性能项以扫描耗时、回源 QPS、积压和重复告警为触发证据；复用现有存储，暂不上 MQ，不默认自动修账 |
| D4 模型能力和健康选路 | 未实现的 Hunyuan/Xingchen/Bedrock/Cloudflare/VertexAI/Replicate/Baidu/Xunfei 原生适配；模型级健康影响路由；上游字符串错误/状态码分类；embeddings 默认模型错误；audio/images/moderations 的 CostBound | 默认模型参数和可操作错误先在 O1/R3 检查；新增原生适配按真实接入需求逐家做。模型健康排除需 model-unavailable 与全渠道故障区分、恢复探测/防饥饿及灰度；类型化上游错误优先，文案兼容限制在对应 provider。没有可靠单位/价格/字段校验时不上虚假成本上限 |
| D5 存储、安全和审计 | 分区启用与保留期、账本归档、手工 SSRF 地址段更新、全局禁用 SSRF 的启动警示、独立审计查询/存储、临时内容排障、localStorage 会话迁 cookie | SSRF 绕过启动告警/配置检查随 R3 做；特殊网段回归随网络栈更新。日志分区先核规模/DDL/readiness；账本不删。内容排障需限时、授权、脱敏、访问审计与清理，不默认保存 prompt。cookie 与 CSRF/身份接口一起设计，不半迁移 |
| D6 架构收敛 | provider/adaptor 双抽象；executor/legacy、重复 RelayRequest/构造器、WS executor；channel/admin/identity 大文件；Dockerfile/Compose、relay 目录结构 | executor 要先解决性能归因、完成独立可比观察和回滚验收，之后单独删 legacy。大文件随对应聚合变更拆，保留事务和分层边界；不因行数启动整仓重写。小服务的分层转换/文件成本是接受的架构代价，不新增模板工程 |
| D7 体验和质量扩展 | 安全 Markdown、历史消息编辑/重发、全站字体优化、覆盖率门禁、更多交互时序用例、对账容差参数化 | 先完成 Q1/Q3 基线；Markdown 先明确安全 renderer/sanitizer 与 CSP 兼容再实现。覆盖率优先关键钱/权限/取消分支，不追求全仓任意百分比；容差先保留同源精确比较，只有业务证据支持才配置化，禁止以放宽容差掩盖并发错账 |

### D1 实施与验收记录（2026-09-23）

基线 `develop@feb9918c`，实现及部署证据随本次 D1 提交归档，提交号见本文件 Git 历史。本轮仅启动用户确认的 D1，多副本边界为 Relay；不将条件清单解释为 D2–D7 全部开工。

- 凭证以 channel 持久层为共享权威，每次协调模式的 OAuth 执行都回读；Claude/Codex（含复用 Claude adaptor 的 Kimi）不再被选路快照的 access token 绕过。`setup_token` 和 `static_key` 保留静态语义。该入口缺口由 `TestOAuthAdaptorsHonorAuthoritativeCredentials` 四场景先失败、修复后通过确认；部署前按线上 Kimi `static_key` 盘点补回归，修正其被误送入 OAuth 的分支，适配器/HTTP `-race` 再次通过。
- Redis 60 秒带 owner token 的租约与条件释放负责刷新准入；调用 OAuth 前先以 CAS 推进 revision 并持久化 `credential_refresh_pending`。租约失效、Redis 丢状态、进程重启均不能清除该标记。刷新结果不明时不重放旧 refresh token；持有完整轮换凭证的进程可周期补写，新授权可清除占用且阻止旧写者覆盖。修改名称等普通资料不能解除占用。
- 新增无 HTTP 暴露的 `ClaimSubscriptionCredentialRefresh` RPC、DTO/DO/PO 字段及三方言迁移 `109_add_credential_refresh_pending.sql`；冷账号的 pending 状态也纳入扫描。协调模式缺 Redis 或缺后台 sweep 时拒绝启动；默认 `single` 保留已有单实例行为并输出扩容限制。
- 账号并发/RPM 的 Redis 共享、失败后本地降级、恢复行为沿用已有回归与 `AccountLimiterRedisDegraded`；新增登录共享限流失败计数及告警。未按未知副本数除配额。Kubernetes Relay 示例配置协调模式，channel Deployment/HPA 固定为 1 并使用 Recreate，避免把本地熔断/半开名额误称为集群共享。
- 隔离证据：双 provider 共用 Redis/存储时只刷新一次，等待者可取消；租约过期后旧 owner 不能删除继任租约；进程状态丢失、OAuth 失败/claim 回包丢失不重放；Store 持续失败、回包丢失及恢复补写；人工重授权及热缓存回读；三方言历史 schema 升级、双写者仅一人 claim、CAS 重放及 stale writer 拒绝。相关包回归和关键包 `-race`、Prometheus 告警触发/恢复、Wire 再生成、分层与迁移治理检查通过。命令及运维流程见[多副本 Runbook](../runbooks/subscription-redis-multi-replica-runbook.md#六d1-本地实施验收2026-09-23)。

线上更新（2026-09-23）：在 `oneapi_channel` 应用迁移 109，重复执行为 no-op，三个现有静态账号 pending 均为 0；本地交叉构建后依次更新 channel、Relay、identity，12:29 UTC 核对三个健康端点及公网 Relay 均为 200，容器 restart_count=0。Relay 协调模式为 `redis`，三平台 sweep 均为 600 秒；新 claim RPC 对 id=0 返回预期 Aborted，无写入。Prometheus 校验 46 条规则并成功重载，新增三条规则均为 health=ok/inactive。未更新前端或扩容实例。镜像、回滚标签、迁移/RPC/指标样本见[脱敏部署证据](../runbooks/evidence/d1-deploy-2026-09-23.json)及[Runbook 部署记录](../runbooks/subscription-redis-multi-replica-runbook.md#七d1-生产更新2026-09-23)。

剩余边界：生产当前只有三个 `static_key` 账号，没有 OAuth 账号；本轮未执行真实供应商轮换、模型扣费请求或多进程故障切换，相关故障验证仍来自受控 OAuth HTTP、miniredis 和独立三方言数据库。进程在 OAuth 返回前或未持久化时死亡，可能仍需新授权；这是明确的人工恢复状态，不是自动接管成功。Redis 故障期间并发/RPM/登录限额只约束本地，恢复时须等待降级期间请求排空。多 channel selector/会话协调、第二批外部告警送达/OTel及第三批尚缺证据项继续保留。

## 6. 文章逐篇追踪表

文件均位于上述桌面原文目录。这里保留每篇“未解决”段落的去向，便于后续文章勘误；“待核”表示未做完整运行验证，实施时先复核，不直接当成已证实缺陷。

| 原文 / 段落 | 遗留项与本次处理 |
| --- | --- |
| `04-subscription-account-pool.md` §4.4、§11 | Redis fail-open 及 N×limit、跨副本选择状态 → D1；降级告警 → O2；封禁原因/恢复倒计时 → O4；固定 8 次选取 → R2；Acquire/Release 已核实为生产 RPC 负载回报，保留；负载排队 → D2 |
| `05-multi-provider-adapters.md` §8 | provider/adaptor 双轨 → D6；未知类型默认兼容 → R3；手工特殊 IP 表、SSRF 全局绕过警示 → D5（警示前移 R3）；未实现原生类型 → D4；SSE 语义 → R4 |
| `06-credential-rotation.md` §3.3、§7–8 | Redis 实现缺失/多副本 rotation → D1；冷账号补写、重试时间尺度、错误 Redis 注释、标准日志 → R1；计数器已存在，补告警/恢复信号；新增发现的 dirty 缓存失效路径也在 R1 |
| `07-raw-relay.md` §6.2、§7 | “无模型健康”已过时，统一记录/成功失败矩阵 → O1；默认 embeddings 模型错误 → R3/O1/D4；未知路由 501 → R3；raw 完整 E2E → Q2；非 embeddings CostBound → D4 |
| `08-subscription-quota-windows.md` §9 | 账号月窗口、USD/百分比标定、用户自然月、排队预约 → D2；固定/滚动计算重复 → O4；30 天口径先在 O3 明示，不改历史契约 |
| `09-subscription-account-governance.md` §6 | 非原子重置降级窗口 → O4（生产 repo 已有原子实现，补契约约束）；全量定期扫 → D3；探测成本、预计恢复提示、manual 待处理入口 → O4 |
| `10-subscription-accounting-semantics.md` §7 | expired 续费文档、快照字段演化/默认值、快照与当前配置口径 → O3；部分退款、升级/降级续费枚举 → D2；现有整单终态不冒充部分退款 |
| `11-subscription-usage-api.md` §6 | nil limit/remaining 0 的歧义、重复兼容字段来源、frozen 缺失、超额状态 → O3；“isValid 永远 true”已过时 |
| `12-multilevel-cache.md` §8 | V2 绕过已确认，是当前正确性保护 → O5 测量后 D3；全片清/SCAN/反向索引、预热 → D3；误导命名/无人调用接口、L2 坏值 → O5；删除前逐调用方核查 |
| `13-routing-outbox-eventual-consistency.md` §6、§8 | 积压监控已完成；Redis 故障失效窗口 → O5；实际通知闭环 → O2；固定轮询、毒事件卡批次/死信 → D3；Streams XAUTOCLAIM 已在 v0.31 接入，不等于已有毒事件治理 |
| `14-migration-and-partitioning.md` §6 | 历史方言覆盖、072 边界、manual DDL、owner 人工登记 → Q2；MySQL-only smoke 旧结论纠正；分区默认关闭 → D5，只在容量和保留政策需要时启用，不把默认关闭本身当 bug |
| `15-circuit-breaker-timeout-degradation.md` §6 | gRPC resilience 默认关闭的运行验证、不同下游阈值 → R4/Q2；timeout×retry 预算 → R4；fallback 指标与实际 reject 不符 → O5；整片鉴权失效 → O5/D3。此处 5 样本 gRPC breaker 与 R2 的 10 样本来源 selector 是两层，不能混为一项 |
| `16-reconciliation.md` §7 | 全量扫描、已确认/忽略状态 → D3；七类计数但通知只展示五类的问题已在 O2 补齐；容差硬编码 → D7；receivable 精确镜像并发验证 → Q2，先测不放宽比较 |
| `17-observability.md` §9 | X-Trace-ID/OTel 关联、没人使用的指标、去重冲突低基数指标 → O2；独立审计存储/查询与临时内容排障 → D5（v0.31 路由审计不等于管理操作审计）；手工基线 → Q3 |
| `18-incident-postmortem.md` §6 | 模型健康不参与选路、文案匹配及 413/415/422 分类边界 → D4；统一事故记录、发现耗时与影响范围 → Q2；旧事件无样本就写未知，不编造数字 |
| `19-web-playground.md` §8 | Markdown → D7；CSP 实际接入/生产头 → Q1；多轮快照核对 → Q1，编辑历史功能 → D7；取消到服务端确认 → R4/Q1/Q2；请求 ID UI 已有 → O2/Q1 关联验证 |
| `20-web-apple-redesign.md` §7 | RechargePage 未迁、残余 slate/图表 token、深色模式状态 → Q1；中文字体首屏对照 → Q3。旧文数量不作当前验收数字，不盲目全局替换 |
| `21-console-performance.md` §7 | 内部导航预取、staleTime/余额新鲜度、真实索引收益、首屏性能门禁、字体分片 → Q3；先测当前 v0.31，不套用文章旧体积 |
| `22-testing-strategy.md` §7 | Relay/raw/浏览器取消 E2E、交互时序 → Q2；5/5 依据 → Q3；Postgres/SQLite-only-static 旧结论纠正，历史迁移覆盖 → Q2；关键分支覆盖率 → D7；verify 不带全量 E2E 保留分层，相关 PR 触发 E2E → Q2 |
| `23-code-review-remediation.md` §8 及被引用审查表 | 延期项集中跟踪 → 本表；定期小批复核/发现证据完整性 → Q2；channel data 拆分 → D6；localStorage 会话 → D5；既有报告另含 selector/登录限流多副本 → D1，双 RelayRequest/构造器/目录/部署模板 → D6，不重开已修 P1/P2 |
| `24-performance-decisions.md` §4 | 20% 阈值依据、Go/jsonx 升级边界、常态性能验证、代表负载 fixture → Q3；executor 流式 P95 回归未归因 → Q3/D6，不能凭新一次成功就恢复默认开启 |
| `25-kratos-minimal-slice.md` §6 | DTO/DO/PO 转换重复、文件数、小服务维护成本为已接受架构取舍 → D6 按实际业务触发，遵循 AGENTS 分层；示例建服务顺序不是本项目新增功能清单 |

### 用户四组补充的映射

| 建议 | 处理 |
| --- | --- |
| 1：低流量连败、复合键、请求内候选稳定 | R2；复合键/预计算已存在，重点补低样本触发、半开单探测及候选耗尽策略；R3/R4 守住已出流不换源 |
| 2：复用预刷新补写、持久化失败计数与告警 | R1；counter 已存在，CAS 尚无接口；先修缓存生命周期，再补无请求补偿和真实告警，不加队列 |
| 3：最后注册 `/v1/*` 501、统一包装记录用量 | R3/O1；复用注册入口、区分请求指标/模型健康/实际用量，避免重复结算和遗漏 raw |
| 4：未实现直接错误、实际 channel、SSE 空闲/总超时分开 | R3/R4/O2；保留既有协议转换和显式同模型故障切换；不可转换能力明确报错，禁止模型降级和成功后的重放；已有空闲实现优先复用 |

## 7. 执行、验证与退出门槛

1. **第一批交付 R1–R4**：凭证不丢内存最新值、冷账号可补写；低流量可熔断；错误边界和同模型切换明确；长流/取消/计费终态可验证。每项先补能失败的回归再改实现，R1 可独立提前交付。
2. **第二批交付 O1–O4，按测量推进 O5**：注册层观察、实际告警送达、冻结额度解释及账号运营闭环。不存在接收端时将送达验收保留待办，不能以规则通过冒充有人收到。
3. **第三批按需交付 Q1–Q3**：Q1 安全/取消和 Q2 v0.31 未结验收前移；样式和性能以真实基线安排。D1 是扩副本前置门，其他 D 项按表中触发条件立项。

验证入口如下；第一批实际已执行结果见[验收记录](../runbooks/first-batch-reliability-2026-09-22.md)，全量 verify、integration、补充 race、三方言迁移/CAS、Prometheus 规则及 24 场景流式隔离矩阵均通过：

```bash
go test ./domain/upstream/credential ./domain/upstream/provider
go test ./app/channel/internal/biz ./internal/biz ./internal/server
go test ./app/billing/internal/biz ./platform/cache ./platform/grpc
go test -race ./domain/upstream/credential ./app/channel/internal/biz
make test-integration
python3 scripts/test-routing-e2e.py --driver=mysql --reliability-only
python3 scripts/test-routing-e2e.py --driver=sqlite3 --skip-build --reliability-only
make verify
```

仅按变更范围运行相应命令；钱/权限/迁移/并发路径必须有故障及恢复证据。Prometheus 规则沿用现有规则测试，前端沿用现有单测和浏览器 E2E，数据库变更跑对应三方言检查。生产主机不跑构建或破坏性故障注入；先隔离环境验收，再按仓库部署流程分服务上线。

每项完成记录至少包括：任务 ID、提交、实际行为、测试/运行证据、剩余边界。运行事实、隔离测试、静态核对分开记录；外部通知、支付宝沙箱故障子项、长时间观察缺证据时保持待验收。发布时另走 release note → CHANGELOG → README → develop → main → tag 的完整流程。

本次规划交付检查：22 篇文章与四组建议均已映射；完成项不重复立项；本文件、TODO 和索引一致；本地 Markdown 链接与 diff 空白检查通过后交付。
