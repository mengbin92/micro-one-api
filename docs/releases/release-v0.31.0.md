# Micro-One-API v0.31.0 发布：计费恢复、请求追踪与跨来源故障切换

> 2026-09-22 · 上一版：[v0.30.1](./release-v0.30.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.31.0)

v0.31.0 是 v0.30.1 之后的 **MINOR 功能与可靠性版本**，汇总
`v0.30.1..a81fcaa4` 的 27 个非合并提交，另含一次主分支同步合并。
本版补齐异步计费恢复、有限额度 Key 幂等扣减和对账回读，增加根请求与每次尝试的持久追踪、
管理台路由审计，并接通普通渠道与订阅账号之间的 Chat Completions 故障切换。

**API/proto 为增量扩展，新增数据库迁移 101–107，升级覆盖九个服务及管理前端。**
MySQL、PostgreSQL、SQLite 均有迁移文件。现有生产环境已分批部署本版修复；其他部署从
v0.30.1 升级仍须核对迁移、服务依赖与前端产物，不能只更新 relay-gateway。

## 1. 异步结算任务持久化与有限额度 Key 幂等扣减

**根因**：异步结算受理与后续写入之间缺少持久恢复任务，进程退出可能丢失尚未完成的
工作；有限额度 Key 扣减缺少请求级去重，响应丢失后的重试不能可靠返回原扣减结果。
gRPC 转发后的结算失败也可能进入上游重试边界。

**修复**：

- billing 在异步受理前保存 settlement task，持续领取、重试，并在重启后恢复未完成任务。
- 同步与异步结算补齐上游订阅账号用量回写，保留原始发生时间，避免历史用量挤入当前窗口。
- identity 按 reservation、用户与 Token 保存扣减结果，重放返回已记录余额；修复 MySQL
  `OnConflict(DoNothing)` 的 `RowsAffected` 方言差异造成的重放报错。
- HTTP/gRPC 转发后的结算失败不再重放上游；提交 RPC 失败记录包含 reservation 的告警。

**影响服务**：`billing-service`、`identity-service`、`channel-service`、`relay-gateway`。
会话窗口仍由 relay 侧维护，本版不宣称所有派生状态都已迁移为 billing 持久恢复任务。

## 2. 对账历史、差异回读与支付补查

**根因**：对账完成日志不等于历史落库；部分差异在持久化、DTO 与管理页面之间丢失。
全生命周期账本与有保留期的日志直接比较会产生假差异，支付回调丢失后的收敛又依赖用户查询。

**修复**：每次对账保存运行记录，区分 `completed`、`partial`、`failed` 和错误信息；
七类差异贯通存储、billing、admin 与页面，共同保留窗口用于 ledger/log 核对。
增加有界 pending 支付主动查单任务，并显示查询失败。退款响应明确站内冲正与外部退款的
不同状态，不再将无法追踪归属的旧订单关联到当前 active 订阅。

**影响服务**：`billing-service`、`admin-api`、`log-service`、管理前端。
外部原路退款仍不支持；历史账务与日志缺口不会因升级自动回填或冲正。

## 3. 请求尝试与实际来源持久追踪

**根因**：重试使用独立计费键但没有稳定根请求关联，直通和订阅适配器路径可能遗漏或
记录错误的实际上游模型，导致一次用户请求的多次尝试难以追踪。

**修复**：reservation 与消费日志保存 `root_request_id`、`attempt_number`、
`source_kind`、`upstream_model_id` 等字段；结算沿用预留时冻结的来源，重放校验身份，
订阅账号在协议转换前应用各自模型映射。billing 新增按用户和根请求分页查询尝试的 RPC，
管理员可通过 `GET /api/log/attempts` 查询。

**影响服务**：`relay-gateway`、`billing-service`、`log-service`、`admin-api`。
旧记录的未知来源保持空值或零值，不推测回填。

## 4. 管理台路由审计与数据口径统一

**根因**：选路过程只存在于进程日志；分页账本排序、渠道健康状态、订单角色判断和成本
导出使用的语义不一致，影响查询和核对。

**修复**：选路 planned/outcome 按用户、根请求和阶段幂等保存到 log-service，管理员
`GET /api/log/routing-audit` 合并选路事件与 billing attempts，调用日志页提供审计入口。
账本排序在数据库分页前执行并使用字段白名单；新增成本 CSV 导出与公式注入防护；统一
健康 unknown/disabled 表达，订单页采用服务端校验的当前角色。

**影响服务**：`relay-gateway`、`log-service`、`billing-service`、`admin-api`、管理前端。
审计沿用日志保留策略；relay 写入失败有指标和日志，但不承诺断网或强杀时审计完整无损。

## 5. 普通渠道与订阅账号双向故障切换

**根因**：Chat 重试锁定初始来源类别，订阅账号走独立重试路径，真实上游失败后无法
跨来源回退。provider 构建期间发生的 DNS 解析失败也未被识别为可重试网络错误。

**修复**：hybrid Chat 与 adaptor orchestrator 按每次候选重新分派 provider/adaptor，
重新校验授权、解析凭据并应用模型映射。失败释放对应预留，成功由实际服务来源结算，
审计与健康反馈保持一致；订阅 SSE 转换保留真实 usage。DNS 解析失败纳入重试分类，
SSRF 私网拒绝仍不可重试。

**影响服务**：`relay-gateway`。
流式回退只发生在向客户端输出开始前；provider-only 入口继续保留来源限制，结算失败
不得触发上游重放。

## 6. Redis 消费、通知与 OAuth 失败可见性

**根因**：Redis Streams pending 消息没有接入自动重领；通知失败、平台业务拒绝与
OAuth 轮换凭据持久化失败的状态或指标不完整。Kimi endpoint 覆盖配置在 provider
构造后才赋值，实际未生效。

**修复**：

- 为 Stream consumer group 接入空闲 pending 的 `XAUTOCLAIM`，处理失败继续保留消息，
  并记录 `micro_one_api_events_stream_failures_total`。
- 通知增加 processing 租约、条件完成与失败重试，校验 webhook 业务回执；到期提醒和
  对账告警接入可用的通知 sink，关闭或未配置时不再伪装为已发送。
- OAuth Store 增加有限退避重试，失败后保留进程内完整轮换凭据，新增
  `micro_one_api_credential_persist_failures_total`；Kimi endpoint/client ID 覆盖在
  provider 构造前生效。

**影响服务**：使用共享事件总线的服务、`notify-worker`、`billing-service`、`relay-gateway`。
毒消息尚无投递次数上限或死信队列；OAuth 持久化持续失败后的进程重启仍可能丢失轮换凭据，
新增重试与指标不等于持久恢复保证。

## 7. 模型统计、配置版本与监控依赖

**根因**：模型统计遗漏最终失败请求，平均延迟口径不一致；未注册模型的样本丢弃缺少
指标。配置写成功不能证明运行实例已加载，monitor 的错误依赖地址又阻断渠道巡检。

**修复**：记录最终失败并按样本数加权平均延迟，存储错误传递给调用方；增加
`micro_one_api_channel_model_usage_dropped_total`。config 持久化递增 `revision` 并在
读写响应中返回，明确其只代表存储版本。Compose 补齐 monitor 的 channel RPC 地址，
探测与健康回写失败可见；billing 配置补齐 channel 客户端。

**影响服务**：`channel-service`、`config-service`、`monitor-worker`、`billing-service`。
现网部分上游模型列表返回 HTML，主动巡检保持暂停；不得把依赖连接恢复当作上游探测通过。

## 8. CI 与安全扫描修复

**根因**：proto 扩展后跟踪的前端 API 类型未同步；pre-push 安全检查依赖交互终端 PATH；
Gitleaks 将历史证据中的 `token_name` 显示名称误判为密钥。

**修复**：同步生成的 TypeScript 类型并纳入 `make verify` 漂移检查，使用安全整数转换
处理延迟，修正 pre-push 工具查找并增加回归覆盖。仅对该条已核实的历史显示名称指纹
添加 `.gitleaksignore`，其他密钥检测保持启用；补齐故障测试 mock 的 Anthropic 接口。

**影响服务**：开发与发布流水线。

## 兼容性说明

- API/proto 新增字段及请求尝试查询 RPC，无删除或重编号；管理查询沿用管理员认证。
- 新增迁移均为建表、加列或加索引，三种数据库方言均有对应文件：

| Owner | 迁移 | 内容 |
| --- | --- | --- |
| billing | 101、102、106 | 对账运行状态、结算恢复任务、预留请求追踪 |
| identity | 103 | 有限额度 Key 扣减去重记录 |
| notify | 104 | 通知 processing 租约 |
| config | 105 | 持久配置 revision |
| log | 107 | 消费日志请求追踪 |

- 可选 `KIMI_TOKEN_REFRESH_URL` / `KIMI_OAUTH_CLIENT_ID` 为空时继续使用既有默认值；
  自定义容器配置须显式传入需要的覆盖值。
- billing 的 `clients.channel.endpoint` 和 monitor 的 `CHANNEL_GRPC_ENDPOINT`
  需指向实际 channel 服务；Compose 默认地址为 `channel-service:9002`。
- config revision 不表示动态配置已应用，退款成功不表示外部支付渠道已原路退款。

## 升级步骤

1. 备份数据库和 `/opt/web/dist`，保存当前镜像摘要与回滚标签，核对各 schema 的迁移
   状态。已分批部署本版的环境应对照实际表、列、索引与迁移登记，避免重复执行 DDL。
2. 使用本版迁移工具执行待应用的 101–107。schema 隔离部署逐库指定 DSN，并使用
   `-ownership billing|identity|notify|config|log` 中对应的一项；共享库按本库迁移状态执行。
   MySQL 使用 `migrations`，PostgreSQL 使用 `migrations/postgres`，SQLite 使用
   `migrations/sqlite`，数据库 driver 与目录须匹配。先 `-status` 核对，再应用，再回读状态。
3. 将九个服务统一升级到 `v0.31.0`。先升级存储与 RPC 提供方，再升级 billing、admin
   和 relay 等调用方；核对 billing/monitor 的 channel 地址，保留现有运营开关设置。
   使用发布的 `linux/amd64` 镜像，或在本机按 `scripts/deploy-update.sh` 交叉构建并传输；
   不在资源受限的生产主机上构建。
4. 独立构建管理前端并更新宿主机 `/opt/web/dist`，保留旧目录备份。admin-api 使用只读
   挂载，更新镜像不会替换该目录，更新前端无需重启容器。
5. 检查服务健康、迁移状态、对账历史、根请求的 planned/outcome/attempt 回读，以及固定
   时间窗的 reservation/ledger/log 一致性。失败恢复与跨来源故障注入在隔离环境执行。
6. 回滚时恢复已保存的兼容镜像和前端；先确认新增结算任务无待处理积压，避免降级后失去
   恢复消费者。保留新增表列与迁移记录，不以删表或历史改账作为回滚方式。

## 验证

- 发布候选本地 `make verify` 全部通过：Go 单测、race、架构与迁移治理、生成 API
  类型漂移检查、Web lint、193 个前端测试及生产构建；`make test-integration` 通过。
- gosec、Gitleaks 历史扫描、部署文档检查、提交正文与 `git diff --check` 均通过。
- 已有 [隔离故障矩阵](../runbooks/evidence/data-flow-fault-matrix-2026-09-22.json)
  覆盖结算恢复、有限 Key 重放、Redis 重领、通知失败、模型统计、配置版本和 OAuth Store
  失败；修复后通过的场景与持续失败边界分别保留在证据中。
- 已有 [非流式跨来源矩阵](../runbooks/evidence/data-flow-failover-isolated-2026-09-22.json)
  验证双向上游 500、连接拒绝和 DNS 不可达的回退、单一来源计费及去重；流式双向切换
  已有 HTTP 单元回归，尚未完成隔离栈流式矩阵。
- F17 只验证了查单失败可见性；支付丢回调收敛、重复回调与发放失败仍待支付宝沙箱或
  支持 QueryOrder 的隔离 provider 验收。供应商账单与历史人工补偿也不在本版完成声明内。
- 发布流水线执行 MySQL/SQLite 路由验收、Compose E2E 与管理台 Playwright 检查，通过后
  才构建九服务双架构镜像并创建 GitHub Release。具体结果以该标签的 Actions 为准。

生产分批部署及后续只读结果见 [验收 Runbook](../runbooks/data-flow-next-stage-acceptance.md)。
服务健康和成功样本不代替未执行的故障场景。

## 完整变更日志

以下为 `v0.30.1..a81fcaa4` 的全部非合并提交；发布材料提交另计。

- `a81fcaa4` docs: record production fault-matrix deployment verification
- `080a43c5` docs: record isolated F7-F18 fault matrix results and fixes
- `f37aaa4e` fix(relay): apply kimi endpoint overrides before provider construction
- `f92417eb` feat(credential): meter OAuth persist failures; allow kimi endpoint override
- `ad353c0c` feat(channel): count usage samples dropped for unregistered models
- `04382c4c` feat(events): count stream processing failures left pending for reclaim
- `8926b5e5` feat(relay): log post-forward quota commit failures
- `5125ac9e` fix(identity): make token-quota dedupe replay dialect-safe
- `e776d8bb` docs: record sixth-batch production deploy and isolated failover matrix
- `fe746176` test(e2e): serve anthropic messages endpoint in mock upstream
- `52762f26` fix(relay): classify upstream DNS resolution failure as retryable
- `bd3f0f03` fix(ci): allow historical token display name in secret scan
- `31250763` fix(relay): enable credential-aware cross-source chat failover
- `4413d367` fix(verify): correct failover attribution checks
- `abefca59` docs: record sixth-batch read-only acceptance
- `8d7320ef` fix(ci): keep frontend API types in sync
- `469be45c` docs: record fifth-batch production deployment
- `1aa092dd` feat: close routing audit and admin data gaps
- `fc44d676` docs: record request trace production deployment
- `bb914b12` fix: persist request attempts and actual upstream identity
- `f523c0bd` fix(ci): stabilize generated API and pre-push security gate
- `795132ff` docs: update data flow deployment status
- `23817c94` fix: close third-batch data flow gaps
- `d4cd62d5` fix: close second-batch data flow gaps
- `0b89f9d0` fix: close billing data flow recovery gaps
- `88226505` docs: complete data flow audit and verify desktop reports
- `3c4ef306` docs: document production data flow closure audit

同步合并：`9bb871eb` merge: main into develop (sync dependabot go.mod bump #21)。

[完整提交对比](https://github.com/mengbin92/micro-one-api/compare/v0.30.1...v0.31.0)
