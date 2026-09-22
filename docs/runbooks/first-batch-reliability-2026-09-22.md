# 第一批 R1–R4 实施与验收记录

日期：2026-09-22。对应[下一阶段计划](../design/next-stage-plan-2026-09-22.md)第一批。

代码基线为 `develop@77e4db45`。本记录随第一批实现一并提交，提交记录见本文件 Git 历史；**未部署生产、未发布版本**。下述测试均为本机或一次性隔离环境；不代表生产观察或外部通知送达。

## R1：轮换凭证与补写

- 缓存保存完整凭证、revision 和待写入时间。失败及恢复的 counter/gauge 已加入断言。短重试失败仍保留最新 access/refresh token；后台成功后的 Invalidate 只清理已落库条目。强制刷新、到期刷新优先使用待落库的最新凭证。
- sweep 先遍历所有待补写项，只调用 Store；同一轮到期扫描跳过已尝试补写的账号。冷账号无需新请求即可补写。
- 新增 credential-only `StoreSubscriptionCredentials` RPC，经 biz repo → data 条件更新，只修改凭证。账号完整更新也检查并递增 revision，防止陈旧快照覆盖人工重授权。重复写入相同 revision/凭证可确认先前已成功但响应丢失的写入。
- 迁移 `108_add_credential_revision.sql` 同步覆盖 MySQL/PostgreSQL/SQLite，归属 channel；历史账号默认 revision=0。事务回滚不提前改变调用方 revision。
- 凭证刷新错误不回显 OAuth 响应、token 或含凭证 URL；写入日志仅记录账号 ID 和有限平台名。

证据：`TestPendingRotationSurvivesBackgroundSuccess`、`TestPendingRotationUsedByNextRefresh`、`TestPendingPersistenceConflictDropsStaleRotation`、`TestPendingSweepDoesNotRotateAgainWhenScannerAlsoFindsAccount`、`TestRefreshErrorsNeverExposeCredentialResponse`、`TestCredentialCASFencesReauthorizationAndReplays`、`TestCredentialRevisionMigrationDialects`。三方言 CAS/新迁移已在一次性数据库通过，SQLite 全迁移与增量升级也通过。

指标：保留 `micro_one_api_credential_persist_failures_total`；新增 `credential_pending`、`credential_pending_oldest_age_seconds`、`credential_sweep_interval_seconds`、`credential_persist_results_total`（均带 `micro_one_api_` 前缀）。标签仅 platform/result。失败五分钟增量 warning、待写年龄超过两轮 critical 的触发及恢复由 `deploy/prometheus/alerts/credential.test.yml` 验证。

边界：待写数据仍在进程内，数据库持续不可用时强杀进程不能保证凭证无损；恢复写入前避免计划重启，旧 refresh token 被撤销时明确要求重新授权。跨副本协调属于 D1；实际告警外部送达属于 O2。

## R2：低流量熔断与候选

普通渠道和订阅账号 selector 新增跨采样窗口的连续失败条件，与原有“至少 10 个样本且错误比例 >50%”取或。`CHANNEL_SELECTOR_CONSECUTIVE_FAILURES` 默认 5，只允许正整数；成功清零。冷却结束只有一个并发探测名额，成功探测同时清理旧采样窗口。

账号选择移除固定 8 次上限，使用排除集推进，并检查 context 与重复 ID。保留 `(kind,id)` 隔离、请求内候选顺序及重新授权校验。同源重试仍归并为一次来源健康结果，客户端错误、取消、限额拒绝与提交失败沿用健康中性分类。

计划纠偏：`SubscriptionAccountSelector.Acquire/Release` 有实际生产调用，由 `RecordSubscriptionAccountSlot` RPC 回传负载，故保留并修正注释。它不是可删除的空转接口。普通渠道持久健康状态与 selector 的内存熔断属于不同机制，本次未合并。

证据：`reliability_test.go` 覆盖跨窗口连败、成功清零、半开单探测及恢复；`TestAccountSelectionPassesEightBlockedAccounts` 覆盖第 9 个账号可用和重复候选无进展。既有同号跨来源、同源健康归并与撤权回归继续通过。

## R3：能力错误和故障切换

未知类型及未实现原生类型返回类型化能力错误；明确的兼容枚举仍有效，type=4 保留原有 OpenAI-compatible 别名。预先可判断的 Responses 不兼容请求零上游调用；OpenAI-compatible Responses 原样发往 `/responses`。上游 Responses 404/405/501 终止执行，不转 Chat、不换 provider；415/422 保留客户端 400 分类。删除 HTTP handler 中失效的协议降级分支，纯转换器仍保留给既有显式适配和单测使用。

`/v1/` 最后注册兜底，返回同形状 OpenAI 501。已注册端点、资源 404、方法错误及 CORS 优先；未知路径请求指标统一为 `/v1/unknown`。错误携带根 request ID 和实际选中来源的 kind/id，未选来源不填造 ID，不暴露 key、账号名称或上游 URL。SSRF 全局关闭时启动日志给出结构化警示。

显式同公开模型故障切换保留，实际映射模型、来源和 reservation 独立归属。能力错误、已出流及账务提交失败均不可触发重放。线上未支持类型盘点保留为部署前置检查，本次未访问或修改生产配置。

证据：`TestResponsesCapabilityFailureNeverSubstitutesProtocol` 覆盖 legacy/orchestrator × stream/nonstream ×404/405/501，均一次上游调用、零提交、一次释放；`TestResponsesAnthropicMismatchMakesNoUpstreamCall`、provider 未知类型测试、`TestUnknownV1RouteMetricsAndIdentityRemainBounded`、`TestMessagesCommitFailureIsNeverReplayable` 及既有跨来源/撤权/提交失败测试通过。

## R4：预算、SSE 和下游熔断

| 配置 | 默认 | 语义 |
| --- | --- | --- |
| `RELAY_CONNECT_TIMEOUT` | 30s | TCP 建连上限 |
| `RELAY_STREAM_HEADER_TIMEOUT` | provider timeout | 等待上游响应头 |
| `RELAY_STREAM_IDLE_TIMEOUT` | provider timeout | 上游字节空闲；keepalive 注释也计为活动 |
| `RELAY_STREAM_TOTAL_TIMEOUT` | 0s | 可选流式总预算；0 不限制健康长流 |
| `RELAY_REQUEST_TIMEOUT` | provider timeout | 非流式请求总预算 |

除流式总预算允许 0 外都要求正时长，非法配置启动失败。请求边界保存同一个开始时间，重试与退避共享剩余预算；调用方更短的 deadline 优先。provider/adaptor 使用共享空闲 reader，移除流式 `http.Client.Timeout`。`micro_one_api_upstream_stream_terminations_total` 的原因标签区分 header_timeout、idle_timeout、total_budget、client_cancel、caller_deadline、transport。

Chat、Messages、Responses 的旧入口流式结算对齐已有 executor 规则：收到有效协议结束事件并正常读完才提交真实 usage（无 usage 时沿用既有估算规则）；中断、取消、下游写失败释放预留。转换器不再把裸 EOF 合成成功。取消关闭上游并释放账号槽，provider 的 chunk 发送响应 context，避免消费者退出后 goroutine 卡住。结算使用独立有界 context，取消不会阻止释放。

下游仅支持 identity/channel/billing/log 四个配置前缀 `GRPC_<SERVICE>_`：`BREAKER_MIN_REQUESTS`（1..1000，默认5）、`BREAKER_FAILURE_RATIO`（0..1，不含0，默认0.6）、`BREAKER_COOLDOWN`（正值且≤1h，默认30s）、`TIMEOUT`（正值且≤5m，默认沿用调用方配置）。实际策略仍为比例判断；调用方取消不触发依赖熔断，降级标签与 reject 行为一致。鉴权/计费 fail-close、日志故障不阻断请求，原有 resilience 开关默认关闭。

## 验证记录

- `make verify`：通过（格式、全量单测、既有 race 门禁、架构、迁移治理、API 生成一致性、前端 lint/test/build）。迁移新增后同步更新 SQLite 生命周期测试的镜像数量断言。
- `go test -race ./domain/upstream/credential ./app/channel/internal/biz ./domain/upstream/provider ./platform/grpc`：通过。
- `go test ./internal/server ./internal/integration -count=1`：通过；集成测试替身已补新增凭证接口。
- MySQL/PostgreSQL/SQLite 的 `TestCredentialRevisionMigrationDialects`：通过；MySQL/PostgreSQL 使用独立临时容器，非业务库。
- `promtool test rules credential.test.yml routing.test.yml`：通过，包含 firing→恢复；未宣称通知已送达。
- MySQL/SQLite 流式隔离矩阵：24/24 通过；[结构化证据](evidence/first-batch-stream-reliability-2026-09-22.json)记录镜像摘要、场景与实际调用/结算次数。两栈已自动清理。

推送前补验：`make verify` 不包含 gosec，首次 pre-push 扫描发现三处 G115。熔断样本阈值改为校验 1–1000 后立即转换，避免在闭包内丢失范围信息；流式/非流式重试审计序号复用 `safecast` 饱和转换，防止窄化为负数。本机旧 gosec v2.26.1（Go 1.26 编译）另有 Go 1.27 语法解析错误，已按 `scripts/tool-versions.env` 安装 v2.28.0。修复后 `sh .githooks/pre-push` 全量扫描及 `go test ./platform/grpc ./internal/server ./pkg/safecast` 均通过，未新增规则豁免。

隔离矩阵入口：`python3 scripts/test-routing-e2e.py --driver=mysql --reliability-only`；SQLite 使用 `--driver=sqlite3 --skip-build --reliability-only`。每种数据库运行 legacy/orchestrator × 普通渠道/订阅账号优先 × 500/连接拒绝/输出后断流，共 12 个场景。真实运行 gateway、channel、identity、billing、log 及数据库/Redis；只模拟上游。初始化先等待账号模型的异步投影完成，再设置映射，避免把初始化并发写入当成请求故障。

部署前依次应用 108 迁移、更新 channel-service、再更新 relay-gateway；先只读盘点渠道类型并确认调用方不依赖已禁用的 Responses 协议替换。生产类型盘点、上线、外部告警送达、长时间观察、支付 F17 和多副本 D1 均不在本次本地交付事实内。
