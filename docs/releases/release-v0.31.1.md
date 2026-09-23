# Micro-One-API v0.31.1 发布：协议转换恢复、凭证补写与流式可靠性修复

> 2026-09-23 · 上一版：[v0.31.0](./release-v0.31.0.md)（2026-09-22）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.31.1)

v0.31.1 是 v0.31.0 之后的 **PATCH 可靠性修复版本**：恢复既有 Responses→Chat／Anthropic
转换和默认兼容行为，保留流式中断释放、禁止重复执行及正确结算；补齐 OAuth 轮换凭证
后台补写与版本校验、低流量熔断和请求预算，并包含 main 分支的安全整数转换修复。

**API/proto 增量扩展，MySQL/PostgreSQL/SQLite 新增迁移 108，归属 channel**。
主要升级服务为 `channel-service`、`relay-gateway`；本版无前端功能变更。

## 修复内容

### 1. 恢复协议转换，按能力明确报错

**根因**：将“不支持原生 Responses”直接视为整条链路不可用，会阻断既有 Chat 和
Anthropic 兼容渠道；另一方面，转换无法表达的状态能力不能静默丢弃。

**修复**：

- OpenAI-compatible 先尝试原生 `/responses`；仅在上游返回 404、405、501 时，
  在同一来源、时间预算和额度预留内转换一次到 Chat。Anthropic 直接转换到 Messages，
  既有 Messages→Chat 继续支持，保留工具调用、历史消息及流式 usage。
- 原生 Responses 保留请求体和查询参数透传；`/responses/<id>` 与
  `/responses/compact` 等资源操作继续走原生路径。
- 仅在需要转换时，对非空 `previous_response_id`、非 null `conversation`、
  `background=true`、`store=true` 返回明确的本地 501 能力错误。原生支持这些字段时
  仍正常透传；鉴权、限流、400、415、422 和临时 500 不触发协议转换。
- 未知或未实现的 provider 类型保留明确错误，type=4 兼容别名继续可用。
  未注册 `/v1/*` 路径返回统一 501，附根请求及已选来源身份，指标路径保持有限集合。
- Anthropic→Responses 保留缓存创建明细；OpenAI usage 内嵌的缓存创建量从包含它的
  input 中扣除，避免重复计费。

**影响服务**：`relay-gateway`。

### 2. 流式中断释放、共享预算与禁止重放

**根因**：裸 EOF 可能被转换器补成成功，旧入口的中断结算规则不一致；逐次重试重置
预算会放大总耗时，上游已成功后的转换或结算错误也不能再次执行生成请求。

**修复**：收到有效协议结束事件且正常读完才提交真实 usage，无 usage 时沿用既有
估算规则。中断、取消、下游写失败释放预留，取消关闭上游并释放账号槽；转换失败、
已经出流和结算提交失败均禁止重放。重试与退避共享请求剩余预算，分别控制连接、
响应头、流式空闲、可选流式总时长和非流式总时长；健康长流默认不受固定总时长限制。
下游 gRPC 配置校验阈值与时长，调用方取消不触发依赖熔断。

**影响服务**：`relay-gateway`。

### 3. OAuth 轮换凭证后台补写与版本保护

**根因**：新 access/refresh token 已轮换但数据库写入失败时，清理缓存或继续使用旧
凭证会丢失可用状态；陈旧账号快照还可能覆盖人工重新授权后的凭证。

**修复**：缓存保留完整最新凭证与待写时间，后台扫描补写冷账号，补写本身不再次
轮换。新增仅写凭证的 `StoreSubscriptionCredentials` RPC，以 `credential_revision`
条件更新；完整账号更新也检查并递增 revision。迁移 108 为历史账号补默认 revision=0。
新增待写数量、最老待写年龄、扫描间隔和补写结果指标及告警规则，错误日志不回显
OAuth 响应、token 或含凭证 URL。

**影响服务**：`channel-service`、`relay-gateway`，以及 Prometheus 告警配置。

待写凭证仍保存在进程内，数据库持续不可用时强杀进程仍可能丢失轮换结果；计划重启
前应确认待写归零。跨副本刷新协调和实际外部告警送达不属于本版完成声明。

### 4. 低流量故障隔离与候选推进

**根因**：只依赖采样窗口失败比例，低流量来源可能长期达不到熔断样本数；固定八次
候选上限会遗漏后续可用账号。

**修复**：普通渠道和订阅账号增加跨窗口连续失败条件，默认五次；成功清零，半开
仅允许一个探测。移除固定候选上限，以排除集推进并检查取消及重复 ID。保留来源
kind/id 隔离、授权校验、同公开模型故障切换与每来源一次健康归因。

**影响服务**：`channel-service`、`relay-gateway`。

### 5. 同步 main 安全修复

**根因**：重试审计序号从 int 直接窄化到 int32 存在溢出风险；闭包中的熔断阈值转换
也使安全扫描无法确认已校验的范围。

**修复**：重试序号复用饱和转换，熔断阈值在范围校验后立即转换；同步 main 的
`282937f3` 及饱和边界测试。未增加扫描豁免，GitHub Code Scanning 的对应两条
G115 告警已关闭。

**影响服务**：`relay-gateway`，以及安全扫描与回归检查。

## 兼容性说明

- API/proto 仅新增凭证 RPC 和 revision 字段，无删除或重编号；先升级 channel 再升级 relay。
- 数据库新增 `108_add_credential_revision.sql`，三种方言均提供，owner 为 channel。
- `CHANNEL_SELECTOR_CONSECUTIVE_FAILURES` 默认 5，仅允许正整数。
- 可选 `RELAY_CONNECT_TIMEOUT` 默认 30s；`RELAY_STREAM_HEADER_TIMEOUT`、
  `RELAY_STREAM_IDLE_TIMEOUT`、`RELAY_REQUEST_TIMEOUT` 默认沿用 provider timeout；
  `RELAY_STREAM_TOTAL_TIMEOUT` 默认 0，表示不限制健康长流。除流式总预算允许 0 外，
  其余均须为正时长，自定义容器配置须显式透传需要的覆盖值。
- identity/channel/billing/log 的 `GRPC_<SERVICE>_` 配置前缀支持
  `BREAKER_MIN_REQUESTS`、`BREAKER_FAILURE_RATIO`、`BREAKER_COOLDOWN`、`TIMEOUT`；
  既有 resilience 开关仍默认关闭。范围与默认值见[验收记录](../runbooks/first-batch-reliability-2026-09-22.md)。
- 不可转换状态能力和未实现 provider 类型明确报错；既有可转换链路继续默认兼容。

## 升级步骤

1. 备份数据库并保存现有镜像摘要和回滚标签；检查凭证待写数量及最老年龄，补写恢复后
   再计划重启。已分批部署本版的环境先核对实际列及迁移登记，避免重复 DDL。
2. 使用本版迁移工具先 `-status` 核对再应用迁移 108。隔离 schema 指定 channel 的
   DSN 和 `-ownership channel`；MySQL、PostgreSQL、SQLite 分别使用 `migrations`、
   `migrations/postgres`、`migrations/sqlite`，driver 与目录须匹配。
3. 依次更新 `channel-service`、`relay-gateway` 到 v0.31.1，保留现有运营开关。
   使用发布的目标架构镜像，或本机通过 `scripts/deploy-update.sh` 交叉构建并传输，
   不在资源受限的生产主机上构建。本版不要求重新发布独立挂载的 `web/dist`。
4. 同步 Prometheus 凭证告警规则，核对服务健康、凭证补写指标、请求来源及预留/结算
   结果。原生与转换、断流及重放验证在隔离环境执行，避免生产故障注入。
5. 回滚时先确认凭证待写已归零，再恢复保存的兼容镜像；保留新增列及迁移记录。

现网已分批运行等价修复，发布此标签不要求再次重启。服务健康与普通成功样本不代表
已执行生产付费协议转换探测。

## 验证

- 发布候选 `make verify` 全部通过：Go 全量单测、既有 race 门禁、架构与迁移治理、
  API 生成一致性、前端 lint、193 个前端测试及生产构建。
- `make test-integration`、pre-push 全量 gosec、部署文档与 Markdown 链接检查、
  提交正文检查及 `git diff --check` 均通过；发布前 GitHub Code Scanning 无开放告警。
- 本版修复已有协议转换、不可转换状态、流式中断、禁止重放及结算分项的网关回归；
  新增安全转换的饱和边界测试，并保留原有跨来源授权和健康归因覆盖。
- 已有[第一批验收记录](../runbooks/first-batch-reliability-2026-09-22.md)记录三方言
  凭证迁移/CAS、额外 race、Prometheus 规则触发与恢复结果；MySQL/SQLite 流式隔离
  矩阵 24/24 通过，[结构化证据](../runbooks/evidence/first-batch-stream-reliability-2026-09-22.json)
  保留实际调用与结算次数。这些为此前的隔离验收，不冒充本次重新执行或生产观察。
- 发布流水线继续执行 MySQL/SQLite 路由验收、Compose E2E 和管理台 Playwright，
  通过后构建九服务 linux/amd64、linux/arm64 镜像并创建 GitHub Release；结果以
  本标签的 Actions 为准。

## 完整变更日志

以下为 `v0.31.0..c0a613ab` 的全部非合并提交；发布材料提交另计。

- `282937f3` fix(relay): backport safe retry counters to main
- `9d820638` fix(relay): restore Responses conversion without replay
- `a123f632` fix(relay): make reliability counters pass security checks
- `8c55115f` fix(relay): complete first-batch credential and request reliability
- `bbeb3c00` docs: plan next-stage reliability and technical debt work
