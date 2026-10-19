# Micro-One-API v0.36.2 发布：计时 SQL 容量改进、分页修复与 Q3 启动恢复

> 2026-10-19 · 上一版：[v0.36.1](./release-v0.36.1.md)（2026-10-18）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.2)

v0.36.2 是 v0.36.1 之后的 **PATCH 修复版本**：改进计时摘要的持久化吞吐与累积数据查询，修复授权范围内的分页水位和并发清理后的游标推进，恢复 Q3 隔离基准的独立服务身份配置，并将镜像构建切换为双架构原生 runner。新增 Log **139 号索引迁移**；无 proto/API 破坏性变更，运行时影响 `log-service` 与 `relay-gateway`。

本次发布版本制品，不更新正在观察的生产实例。固定 48 小时窗口仍于 **2026-10-20 10:10:20 CST（北京时间）**截止；隔离容量通过不替代生产 WS、TTL、最大输入内存或 executor 准入。

## 修复与改进

### 1. 计时摘要按组持久化，吸收短时存储停顿

**根因**：旧 exporter 仅有 2 个 worker、256 项队列，每条摘要单独提交 claim、日志和关联引用。在原生 64 并发 SQL 实验中，持久化吞吐不足导致队列饱和；只增加 worker 仍无法摊销事务成本，数据库短时检查点停顿也会造成丢弃。

**修复**：Log 合并同时到达的已认证摘要，最多保留 128 份私有快照，稀疏到达最多等待 5ms，满组立即提交；同组摘要、去重 claim、attempt refs 和最终 ID 关联在同一事务提交，成功确认仍在持久化之后。PostgreSQL 在 claim 插入时分配最终日志 ID，保留重试去重与旧逐条 RPC 兼容。Relay 改为固定 256 个 worker、4096 项队列；RPC 重试、期限、失败与丢弃计数保留。

**影响服务**：`log-service`、`relay-gateway`。导出队列加在途上界从 258 增至 4352，峰值内存和摘要可见延迟可能上升；受控实验的三秒缓冲不构成任意负载或最大输入内存保证。详见[容量定位与验收](../runbooks/request-timing-capacity-improvement-2026-10-18.md)。

### 2. 累积数据查询有界，保持授权与游标正确性

**根因**：摘要增多后，快照水位和整行分页查询会触发不断扩大的扫描/排序；两阶段分页中，保留期清理可能在取键后删除整页，导致游标无法推进。快照水位还必须受调用者实际可见用户范围约束。

**修复**：在完整授权范围内取得最新 ID 水位，先从覆盖索引取分页键，再按同一可见性、搜索条件和快照加载 JSON；游标按已选中的键推进，即使整页随后被清理也可继续。新增 `139_add_request_trace_snapshot_indexes`，为 `logs(source,id)` 与 `logs(source,user_id,id)` 建立索引，覆盖 MySQL、PostgreSQL、SQLite 与迁移所有权登记。

**影响服务**：`log-service`。只新增二级索引，不修改数据列、历史日志、价格或账本；上线前先应用迁移，大表建索引需安排维护窗口。

### 3. Q3 首请求恢复独立服务授权

**根因**：Q3 环境生成脚本只配置共享 `SERVICE_TOKEN`。当前上游执行 claim 要求独立 Relay 系统能力，即使 IAM 处于 legacy 模式，共享令牌也无法取得该能力，候选版本 chat preflight 返回 HTTP 503，尚未进入候选性能测量。

**修复**：两侧隔离环境均复用既有身份生成器和校验器，生成各服务独立 outbound token 与接收方信任表；临时凭据保持私有权限并随实验清理。新增直接执行真实环境生成函数并解析 Compose 配置的快速回归，Q3 在完整对照前先运行该检查。没有放宽业务授权。

**影响服务**：Q3 测试脚本与 workflow；生产服务身份、配置和权限不变。Q3 的 outbox 改为随测试清理的命名卷，避免容器创建的私有绑定目录阻止临时 Git worktree 删除；生产挂载不变。完整基准仍保留每侧三轮、原负载与原比较阈值。按既有发版约定将默认基线从 v0.33.1 更新为最近已发布的 v0.36.1；旧基线跨版本性能 FAIL 与原始样本保留，不视为已优化，详见 [Q3 记录](../runbooks/q3-v0.36.2-2026-10-19.md)。

### 4. 双架构镜像改用原生构建与独立缓存

**根因**：此前在 amd64 runner 上使用 QEMU 编译 arm64，工具安装和 CGO 编译耗时较长；共用缓存 scope 也会造成并行导出冲突。

**修复**：CI 与 Release 按服务、架构拆分原生 runner 与缓存；发布先按 digest 推送镜像，在更新标签前验证恰好包含 linux/amd64 和 linux/arm64，合并后再次检查。构建上下文排除文档与普通 Go 测试，精确保留 Routing 专用验收镜像所需源码。另修复 gRPC span 测试过早读取服务端结束事件的时序假设，生产追踪代码不变。

**影响服务**：CI、发布流水线和构建上下文。九服务镜像的既有标签规则与完整 Release E2E 门禁保留；原生构建耗时与验证边界见[构建验收记录](../runbooks/build-timing-sql-acceptance-2026-10-18.md)。

## 兼容性说明

- 从 v0.36.1 升级需新增 Log 139 迁移；更早版本先遵守对应版本的迁移步骤。三种数据库目录均已提供迁移，Log 所有权已登记。
- proto、公开 API、服务身份权限、前端业务资源没有变化；宿主机 `/opt/web/dist` 无需因本版重建或替换。
- Log 保持旧 Relay 的逐条 RPC 兼容；先升级 Log，再升级启用更高导出并发的 Relay。
- 导出仍是有界、尽力交付：超出固定预算或超过关闭期限仍可能丢弃，并保留计数与告警。更大的缓冲不是无损持久队列。
- 回滚旧二进制可保留两个兼容索引；不删除业务数据，不自动回退 schema。

## 升级步骤

1. 记录现有镜像与实例边界，备份数据库和部署配置。本次发版不部署、不重启生产服务。
2. 等原定 48 小时观察结束并形成结论，再安排维护窗口；若提前更换实例，记录新的进程边界，不能拼接重启前后数据作为同一稳态窗口。
3. 通过既有迁移工具，选用部署数据库对应目录，先应用并确认 `139_add_request_trace_snapshot_indexes`；共享数据库按现有全量迁移流程，分库按 `log` 所有权执行。大表需预留建索引时间并观察资源和锁等待。
4. 按仓库本机交叉构建流程先更新 `log-service`，确认健康与摘要写入，再更新 `relay-gateway`。生产 Linux/amd64 镜像在本机交叉构建后传输，禁止在资源受限生产机上构建。
5. 验证摘要/claim/refs 关联、去重、分页可见范围、队列积压、失败/丢弃计数与告警，结合生产峰值内存和 TTL/WS 重新评估容量；前端无更新步骤。
6. 回滚使用原兼容镜像及配置，可保留 139 索引；重启后的生产观察单独记录。

## 验证

- 原生 Linux/amd64 三库 SQL 容量门禁通过：各库四组、64 并发、每模式 30 秒，合计 448,532 条摘要，5,384,544 个完成/接收 OTLP span；摘要失败/丢弃、HTTP/查询错误均为 0，各组 HTTP P95 增量不超过 3.34%。三库各十轮 race 与独立暂停探针通过。源版本与完整资源边界见[最终验收](../runbooks/request-timing-capacity-improvement-2026-10-18.md#最终通过结果2026-10-19)。
- Q3 身份回归先在原环境生成器上失败，修复后全部三项 E2E/Lite/Q3 启动身份回归通过；服务身份、gRPC allowlist 和 Channel 执行 claim 聚焦测试通过。Bash 语法、提交正文与差异格式检查通过。
- Q3 修复 503 后的 [v0.33.1 完整对照](https://github.com/mengbin92/micro-one-api/actions/runs/39211342704) 两侧零 HTTP 错误、零丢弃，但 models/chat/aggregate 延迟比较失败，保留为 FAIL。按既有约定以最近发布的 v0.36.1 检验本版增量；每侧三轮原剖面与原比较阈值不变。源码边界、旧版失败报告和正式门禁说明见 [Q3 记录](../runbooks/q3-v0.36.2-2026-10-19.md)。打标签以前须确认正式 Q3、CI 与安全检查全部通过。
- [v0.36.1 正式对照](https://github.com/mengbin92/micro-one-api/actions/runs/39219348260) 七项性能门禁全部通过，chat P95 -0.75%、models P95 +0.12%，HTTP 错误与丢弃均为 0；该运行因收尾 worktree 权限退出 255，整体仍记 FAIL。Q3 overlay 改用 outbox 命名卷后需确认完整 workflow 包括清理阶段通过。
- 发布前常规门禁：[CI](https://github.com/mengbin92/micro-one-api/actions/runs/39212165288)、[安全](https://github.com/mengbin92/micro-one-api/actions/runs/39212165320)；发布流水线继续执行完整 Release E2E、九服务双架构构建与清单验证。
- 生产 48 小时观察、WS 自然样本、存储 TTL、最大输入内存、供应商账期与 executor 扩面继续遵守独立门禁。

## 完整变更日志

- `perf(ci): build images on native runners with isolated caches`
- `fix(ci): reject incomplete platform pairs before updating release tags`
- `test(timing): exercise authenticated log RPC and SQL delivery under load`
- `perf(build): exclude documentation and Go tests from image contexts`
- `test(tracing): await server transport end before inspecting spans`
- `fix(build): retain routing acceptance tests in Docker contexts`
- `docs: record native build gains and timing SQL capacity boundaries`
- `perf(timing): group durable SQL summary commits under bounded concurrency`
- `fix(ci): generate ignored proto stubs before native capacity compilation`
- `perf(timing): bound snapshot and page reads as summaries accumulate`
- `fix(ci): wait for TCP database readiness in native timing gates`
- `fix(timing): anchor grouped claim ID updates to their integer column`
- `fix(ci): preserve private native capacity artifacts for upload`
- `perf(timing): pipeline RPC groups without expanding the export queue`
- `fix(timing): advance page cursors past concurrently purged rows`
- `perf(timing): coalesce sparse writes and flush full groups immediately`
- `perf(timing): reserve final PostgreSQL claim IDs in the insert`
- `perf(timing): amortize durable commits over bounded larger groups`
- `fix(timing): fence pagination within the authorized owner scope`
- `perf(timing): index snapshot orders without relying on analyze`
- `perf(timing): buffer brief durable-storage pauses within a fixed budget`
- `docs(timing): record native SQL capacity and resource budgets`
- `fix(benchmark): provision Q3 service identities before startup`
- `fix(benchmark): compare Q3 with the latest published release`
- `fix(benchmark): isolate Q3 outbox state from temporary worktrees`
- `docs(release): v0.36.2`
