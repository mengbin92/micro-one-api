# Micro-One-API v0.20.0 发布：入口指标、分区账本幂等与管理后台稳定性

> 2026-08-14 · 上一版：[v0.19.1](./release-v0.19.1.md)（2026-08-13）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.0)

v0.20.0 是 v0.19.1 之后的 **MINOR 功能版本**（10 个提交，`47e619e` → `79108d5`），主线是「可观测性落地 + 分区账本幂等加固 + 管理后台筛选竞态修复 + Nightly E2E 修复」：接通 relay-gateway HTTP 入口的 `HTTPRequestTotal` / `HTTPRequestDuration` 指标（P3-0），为 MySQL RANGE 分区后的 `billing_ledgers` 建立非分区全局 dedupe claim 表以保证资金写幂等，修复 admin 表格 URL 筛选在快速连续操作时静默丢参的竞态，并修复 nightly compose E2E / Playwright smoke 的四处环境与协议适配问题。

**无 API 破坏性变更、无 proto 公共契约变更**；**包含数据库迁移 `078`**（新增 `billing_ledger_dedupe_claims` 表并回填存量 claim，additive）；billing-service 内部 `partition.tables` 配置字段标记 deprecated 且运行时忽略。受影响服务：relay-gateway、billing-service、log-service、admin-api（含管理前端 web/dist）。

## 1. Relay HTTP 入口请求指标接通（`47e619e` + `eab271b`，P3-0）

- **根因**：`metrics.HTTPRequestTotal` / `HTTPRequestDuration` 已在平台 metrics 注册，但从未挂到任何 HTTP server，P3 gate 的「metric-triggered」触发条件（502/429 持续阈值、延迟）无法在 Prometheus 查询，评估只能靠人工。
- **修复**：新增 `platform/middleware.NewHTTPMetricsMiddleware`，挂载在 relay-gateway 路由中间件链**最外层**——记录内部中间件改写后的最终 status code；路径标签将 UUID / 纯数字 / 长（>32 字符）段归一为 `{id}` 以控制 label 基数；`/healthz` 与 `/metrics` 注册在链外，不计入（避免 scrape 噪声）。同步重新生成 `wire_gen.go`。P3 五项主题（load-aware queueing / session-window 统一 / 表分区 / log-service 合并 / grpc-gateway 迁移）均完成触发条件评估，结论为维持 trigger-based（`docs/design/v0.19-p3-gate-assessment.md`）。
- **影响服务**：relay-gateway（需重新部署后生效）；部署后 Prometheus 中出现 `http_request_total{service="relay-gateway",...}` 与延迟直方图。

## 2. 分区账本幂等加固（`88a2c82`）

- **根因**：`billing_ledgers` 按 `created_at` 做 MySQL RANGE 分区后，MySQL 要求所有唯一键包含分区列，表上的全局 `UNIQUE(ledger_dedupe_key)` 必须移除——资金写路径将失去请求级幂等闸门。此外分区维护代码存在三处缺陷：`billing_ledgers`（DATETIME / `TO_DAYS`）与 `logs`（Unix epoch）共用 `UNIX_TIMESTAMP(...)` 边界表达式；分区名比较用了 `2006-01` 格式而生成用 `200601`；自动维护会把财务审计账本分区按 6 个月通用保留期直接 DROP。
- **修复**：新增非分区表 `billing_ledger_dedupe_claims`（主键即 `ledger_dedupe_key`），`CreateLedgerInTx` 在**同一事务**内先插入 claim 再插入 ledger——并发写由主键原子裁决，冲突统一映射 `biz.ErrLedgerDedupeExists` → `ErrDuplicateRequest`（调用方 409），事务回滚同时释放 claim。迁移 `078` 建表并 `INSERT IGNORE ... GROUP BY` 幂等回填存量账本（容忍手动改过的重复键环境）；手动分区脚本拆为 `phase3_billing_ledgers_partitioning.sql` / `phase3_logs_partitioning.sql`，ledger 分区前须先应用 `078`，全局唯一改由 claim 表承载。分区管理器按表选择正确的边界表达式，修复分区名格式比较，`pYYYYMM` 上界改为次月 1 日，且**财务账本分区不再被自动 DROP**（logs 保留 6 个月策略，ledger 归档需独立审批）。billing-service 的 `partition.tables` 字段 deprecated：旧配置仍可解析但运行时只维护 `billing_ledgers`。
- **影响服务**：billing-service、log-service；**必须先执行迁移 `078` 再启用/继续分区维护**；已分区环境按 runbook 执行手动 SQL 前请先备份。

## 3. Admin 表格筛选竞态修复（`e749f32` + `e074068` + `79108d5`）

- **根因**：`useAdminTableState` 每次 URL 更新都基于 render 时闭包里的 `searchParams` 计算，而 router 导航是异步提交的——两个筛选器快速连续变更时，第二个 onChange 从 stale closure 出发，把第一个筛选条件静默丢掉（CI 中 payment-orders 丢 `channel=alipay`，真实用户同样可触发）。第一版修复引入的 render 期 ref 读写又被 react-hooks v7 lint 禁止。
- **修复**：用 ref 累积最近 issued params（仅外部 URL 变化时 resync），`pendingIssued` 标记导航在途期间不回读中间态 URL，最终提交与 issued 完全一致才清除；同步逻辑移入 `useEffect`，不再在 render 期触碰 ref。补齐三组回归单测（stale closure 连续更新、三连 back-to-back、外部导航）。全部 16 个 admin 表格页经由该 hook 受益。
- **影响服务**：admin-api（含管理前端 web/dist）；需同时发布前端产物。

## 4. Nightly E2E / Compose 测试修复（`5ebe403` + `485b0da` + `20e1216` + `3b36c36`）

- **修复**：compose 中 prometheus/grafana 卷从硬编码 `/opt/micro-one-api/...`（仅生产主机存在，CI bind-mount 报 "not a directory"）改为仓库相对 `../../deploy/{prometheus,grafana}`；Playwright admin smoke 全面对齐中文文案与重构后的组件结构（含 ModelMultiSelect、移动端滚动对话框点击、strict violation 消歧）；每次筛选变更后等待 URL 提交再进行下一步，users 导出等待 `?status=1` 后再点击；`actions/upload-artifact` v4 → v7；compose-e2e job 补齐生成 git-ignored 的 `*.pb.go`（镜像 `ci.yml` 的 `make api` 步骤）；e2e suite 的 gRPC 调用通过 `grpc.WithPerRPCCredentials` 附带 `SERVICE_TOKEN`（适配 service-token 拦截器）。
- **影响服务**：无运行时影响（CI / 测试基础设施）；生产主机自行维护的 compose 拷贝不受影响。

## 兼容性说明

- **API / 公共 proto**：无破坏性变更、无对外契约变更。
- **数据库**：**包含迁移 `078`**（新增 `billing_ledger_dedupe_claims` + 存量 claim 回填，additive、幂等）。手动分区 DDL 为可选运维操作，须按 runbook 在维护窗口执行。
- **配置**：billing-service `partition.tables` 标记 deprecated——旧值仍可解析，运行时忽略，仅维护 `billing_ledgers`；无需强制修改，但建议清理。
- **行为变化**：relay-gateway 开始产出 HTTP 入口请求 / 延迟指标；分区维护不再自动 DROP 财务账本分区。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.0
# 1) 执行数据库迁移（必须，先于服务滚动）：
#    docker compose run --rm migrate   或   make migrate
# 2) 重新构建部署：relay-gateway、billing-service、log-service、admin-api（含 web/dist 前端产物）
# 3) （可选）已分区/计划分区环境：按 docs/runbooks/table-partitioning-runbook.md
#    执行 migrations/manual/phase3_*.sql，执行前备份。
```

## 验证

- `make migration-check`：通过（含 `078` 三方言镜像与 ownership 校验）。
- `go test ./platform/middleware/... ./app/billing/... ./platform/database/partition/...`：通过（含 metrics 中间件、dedupe claim 并发、分区边界回归）。
- `web` 单测（`useAdminTableState` 竞态回归）+ Playwright admin smoke：34 passed, 1 skipped（chrome + mobile-chrome，本地）。
- Nightly compose E2E：修复后通过（生成 stubs + SERVICE_TOKEN 附带）。

## 完整变更日志

- feat(observability): wire relay HTTP entry request metrics (v0.19 P3-0)
- fix(observability): regenerate wire_gen.go for P3-0 metrics middleware
- fix: harden partitioned ledger idempotency
- fix(ci): repair nightly E2E — compose mounts and admin smoke locators
- fix(ci): serialize URL-driven filter updates in admin smoke, bump upload-artifact
- fix(web): stop dropping rapid admin table filter updates
- fix(web): stop resyncing table params while our navigation is in flight
- fix(ci): generate git-ignored proto stubs in compose E2E job
- fix(e2e): attach SERVICE_TOKEN to gRPC calls in the compose suite
- fix(web): move table-params ref sync out of render for lint
