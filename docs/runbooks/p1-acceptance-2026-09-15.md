# 2026-09-15 P1 验收记录

> 本文件合并原 P1-1（路由 / outbox 可观测性）、P1-2（管理台汇总性能与降级）、P1-3（旧迁移记录表升级预检）三份验收记录；验收标准与执行状态以 [v0.30 路线图](../design/v0.30-roadmap.md) 为准。三个子项相互独立，各自对应独立提交，分节保留原始验收内容。

## P1-1 路由与 outbox 可观测性验收

代码基线：`develop@0b2c7562`，加本次 P1-1 工作区改动。未新增公共 API 或数据库迁移。所有故障注入均在本机隔离 Compose 项目中进行；没有生产部署、开关变更或远程 CI 执行。

### 交付

- identity / channel / subscription outbox：全部 pending、最老消息年龄、最后成功投递、最近成功扫描、分操作失败计数；未配置 Redis 仍可观察积压。
- 订阅 outbox 补齐错误回调，三个 owner 输出 owner / operation / event_id 结构化日志。
- 复用权威 RPC 时延直方图并补齐客户端错误计数；路由 resolve / ordered / reserve / recheck 输出有限 reason 标签及请求 / 主体日志。
- 快照内容、解析、摘要、主体 / 订阅绑定失败分别计数，持久化校验失败记录 reservation / request / user ID。
- 九项告警、十个 Grafana 面板，以及[运行手册](./routing-observability-runbook.md)。规则测试随共享 nightly / release E2E 入口执行。

### 验证结果

| 验证 | 结果 |
| --- | --- |
| `make verify` | 通过：format、Go 单元测试、既有 race 门禁、分层、迁移治理、前端 lint / test / build |
| 相关包 Go 回归 | 通过：routingoutbox、metrics、xgrpc、relay biz / service / server、billing biz / data、identity / channel data、subscription data |
| `go test -race ./platform/routingoutbox ./platform/grpc/xgrpc ./internal/biz ./app/billing/internal/data` | 通过：额外覆盖新 worker 指标、错误回传、无 Redis 客户端、超过单批次积压、快照损坏 |
| Prometheus v3.6.0 `promtool test rules routing.test.yml` | 通过：九项新增规则的可执行断言，包含故障触发 / 恢复、空闲队列、扫描过时、采集失败、首次能力 / 快照失败、存储与 RPC 故障和时延 |
| `python3 scripts/test-routing-e2e.py --driver=mysql` | 全阶段通过，包含原始规则的 firing / 恢复及阶段 F 预检；[日志](./evidence/routing-observability-mysql-2026-09-15.txt) |
| `python3 scripts/test-routing-e2e.py --driver=sqlite3 --skip-build` | 使用同一业务镜像，全阶段通过；[日志](./evidence/routing-observability-sqlite3-2026-09-15.txt) |
| Dashboard / 脚本静态检查 | 通过：JSON 可解析、十个面板 ID 唯一且查询非空、Python 语法；未启动 Grafana 做浏览器视觉验收 |

### Redis 故障证据

两个方言都执行以下断言，未缩短生产规则阈值：

1. Redis 断开期间，通过真实接口撤权；数据库保留待投递事件。Prometheus 实际采集到 identity pending > 0 和 publish failures > 0。
2. `/api/v1/query` 查询 `ALERTS`，等待 `RoutingOutboxDeliveryFailing` 与 `RoutingOutboxBacklog` 均为 firing。
3. Redis 恢复后，数据库积压清空，Prometheus 采集到 pending=0 / oldest age=0 / last success>0，两个告警解除。
4. 已撤权 Key 继续拒绝；重新授权后聊天与结算成功。已有用例同时检查拒绝请求不会调用上游或扣款。
5. billing 可达但快照能力关闭时，fixed / ordered 请求均拒绝，`reserve/billing_capability` 计数 > 0，`RoutingCapabilityRejected` firing。

### 边界

- 真实 Redis 告警演练覆盖 identity 发布路径；三个 owner 的指标、投递失败和恢复由 SQLite 数据边界测试覆盖。本次未配置 MySQL / PostgreSQL 单元测试专用 DSN，因此其可选数据边界子测试跳过；MySQL 由完整服务 E2E 提供真实数据库验证，PostgreSQL 未重新验收。
- `last_success` 是本 worker 成功发布和确认时间，不能替代消费者进度或授权断言。共享订阅表的 pending 不能把不同副本相加。
- 当前没有 Alertmanager，证据证明 Prometheus 告警状态变化，不代表外部通知已发送。生产观察窗口与小额使用样本仍需后续执行。
- E2E 默认清理本次私有容器、网络和卷；证据文件仅保留通过的场景和观察断言，不包含随机测试凭据、原始 Compose 配置或服务日志。

## P1-2 管理台汇总性能与降级验收

代码基线：`develop@835aa092`，加本次 P1-2 工作区改动。未新增公共 API 或数据库迁移；`/api/admin/summary` 响应键保持稳定，仅新增 `partial` / `sections` / `alerts_complete` 字段，失败分项由伪造零值改为 `null`。所有测量均在本机测试固件中进行；没有生产部署或远程 CI 执行。

### 根因

1. `handleAdminSummary` 串行执行约 20 次独立 RPC，延迟逐个累加。
2. 聚合挂在入口请求 context 上，尾部调用继承临近过期的 deadline，健康后端也报 DeadlineExceeded；此前加的 60 秒 `WithTimeout` 不能延长先到期的父 context。
3. 分项失败被替换为空集合或零值，前端把不可用显示成真实零值；定价配置读取全部站点配置并在出错时静默使用默认值。

### 修复

- 聚合编排下沉到 service 层 `LoadSummary`；HTTP handler 只保留输入输出适配。
- 独立查询受限并发执行：每请求最多 4 路，总预算 8 秒，单分项预算 2 秒；客户端断开向下取消，排队任务在取消后不再调用依赖，无分离后台 goroutine。
- ID enrichment 保持在排名结果之后的第二阶段，顺序依赖不变。
- 每个分项返回 `available` / `reason`（`timeout` / `canceled` / `unavailable`）状态；失败字段编码为 `null`，`partial=true`，告警来源不全时 `alerts_complete=false`。
- 定价配置收窄为概览实际使用的 5 个公开键；读取失败向上传播，不再静默套用默认值。
- 前端区分“暂无数据”和“数据暂不可用”：部分失败显示 amber 横幅与失败分项名称，可手动重试；失败统计卡片显示“暂不可用”而不是 0；告警来源不完整时不再显示“运行正常”。

### 同负载前后对照

负载：测试固件固定每次 RPC 延迟 20ms，每组 12 次汇总请求，只替换 RPC 边界。

| 场景 | 修复前 | 修复后 |
| --- | --- | --- |
| 单客户端 P50 / P95 | 376.8ms / 380.5ms | 105.6ms / 109.3ms |
| 4 并发客户端 P50 / P95 | 378.2ms / 379.0ms | 105.5ms / 105.8ms |
| 单请求 RPC 峰值并发 | 1 | 4（等于上限） |
| 4 客户端 RPC 峰值并发 | 4 | 16（每请求 4 路上限） |
| 部分失败行为 | 失败字段显示为零 / 空集，断言失败 | 失败字段为 `null`，健康区块保留，断言通过 |

### 验证结果

| 验证 | 结果 |
| --- | --- |
| `make verify` | 通过：format、Go 单元测试、race 门禁、分层、迁移治理、前端 lint / test / build |
| `go test -race ./app/admin/internal/server ./app/admin/internal/service` | 通过；修复了测试固件在并发聚合下未加锁的 `aggregateReqs` 记录 |
| 同负载对照 `TestAdminSummaryLoadProfile` / `TestAdminSummaryBoundedParallelism` | 通过：峰值并发 >1 且 ≤4，P95 如上表 |
| 降级 `TestAdminSummaryPartialFailure` | 通过：注入 AggregateUsage 不可用后收入字段为 `null`，健康数据保留 |
| 超时 / 取消 service 测试 | 通过：慢分项不取消健康兄弟任务；请求预算包含排队；取消后不再调用依赖；无后台工作逃逸请求 |
| 定价配置 `TestSummaryPricingOptionsScopeAndFailures` | 通过：只读 5 个键及既有别名；读取失败传播错误 |
| 前端 `OverviewPage.test.tsx` 4 例 | 通过：部分不可用横幅与重试恢复、真空数据显示为空 / 零、请求失败不伪造健康与收入 |
| `npx tsc -b --noEmit` 与 eslint | 通过 |

### 边界

- P95 对照来自确定性测试固件（每次 RPC 固定 20ms），证明并发与预算行为，不代表生产绝对延迟；生产门槛需在真实基线下另行确定。
- `sections` 的 reason 是有限稳定词汇，不回传上游原始错误；明细仍在服务端日志。
- 汇总响应键保持兼容，但失败语义从伪造零值变为 `null`；依赖旧零值行为的前端区块已同步改造。
- 本次未部署生产、未发布版本；生产观察与真实负载对照留待后续窗口。

## P1-3 旧迁移记录表升级预检验收

代码基线：`develop@77cd528d`，加本次 P1-3 工作区改动。未新增公共 API、业务表迁移或运行时配置；没有生产部署、生产数据库变更或版本发布。所有数据库验证均使用本机隔离容器 / 临时 SQLite 文件。

### 交付

- `Runner.Apply` 在 brownfield 标记和首个业务 DDL 之前，用事务执行 `INSERT INTO schema_migrations (version) ...` 探针并回滚，直接验证旧表是否支持 runner 的记录写法。
- 旧表 `applied_at NOT NULL` 且无默认值时，返回可操作的 `preflight schema_migrations` 错误；错误同时提示历史半完成迁移需先核对对象、修复元数据并手工补录版本。
- `Runner.Status` 改为严格只读：`schema_migrations` 不存在时全部报告 pending，不再创建元数据表。
- 保留显式恢复原则：runner 不会仅因同名业务对象存在就自动补录迁移完成。
- MySQL / PostgreSQL migration smoke 接入同一集成用例；SQLite 单元测试覆盖阻断、修复后升级、重复执行与半完成恢复。

### 验证结果

| 验证 | 结果 |
| --- | --- |
| `go test ./platform/database/migrate` | 通过：覆盖 SQLite 生命周期、元数据预检、状态只读及既有迁移行为 |
| `make migration-check` | 通过：迁移前缀、ownership 与方言镜像治理检查 |
| `bash -n scripts/test-migration-smoke.sh` | 通过 |
| `./scripts/test-migration-smoke.sh mysql` | 通过：fresh 94 个迁移、repeat no-op、状态审计、失败注入不记账、旧表预检阻断 / 修复后升级 / 重复执行 / 半完成显式恢复 |
| `./scripts/test-migration-smoke.sh postgres` | 通过：fresh 40 个迁移、repeat no-op、状态审计、失败注入不记账、旧表预检阻断 / 修复后升级 / 重复执行 / 半完成显式恢复 |
| `make verify` | 通过：format、Go 测试、race 门禁、分层、迁移治理、前端 lint / test / build |

### 关键行为

1. **DDL 前阻断**：不兼容元数据表存在时，业务探针表不存在，`schema_migrations` 也没有 probe 版本；探针事务回滚。
2. **修复后升级**：为 `applied_at` 补默认值后，pending 迁移正常执行并记账；再次执行输出 nothing to apply。
3. **半完成恢复**：模拟“业务对象已存在但版本未记账”后，去掉默认值重跑仍先被预检阻断；恢复默认值并显式补录版本后，重跑为 no-op。
4. **状态只读**：空库执行 `Status` 只返回 pending，不创建 `schema_migrations`。

### 边界

- 预检证明元数据表接受 runner 的最小插入语句，不校验历史 `applied_at` 数值的业务语义。
- 历史半完成迁移仍需要人工核对对象与版本后补录；本次没有引入自动修复工具。
- 真实 MySQL / PostgreSQL 验证使用 MySQL 8.4 与 PostgreSQL 16；生产版本差异仍按部署手册在升级窗口核查。
