# Micro-One-API v0.17.0 发布：工程收尾、运营闭环与发布门禁补完

> 2026-08-08 · 上一版：[v0.16.0](./release-v0.16.0.md)（2026-08-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.17.0)

v0.17.0 是 v0.16.0 之后的 **MINOR 版本**（14 个提交，v0.16.0 `a8e14db` → develop 当前 HEAD），完成 v0.17 路线图 **P0（工程收尾）与 P1（运营闭环）** 两项交付，并补齐发布门禁：修复两个开源依赖安全漏洞（nanoid CVE-2026-67213、js-yaml GHSA-5p4m-2wfm-xmqj）、修复 Docker CI 的 **9 服务 × 2 架构（18 个 job）矩阵**、升级 CI 运行时与 CodeQL、加固 gross-profit 指标口径、统一 Grafana dashboard 格式，并为 P3 性能基线与 jsonx 决策补充可复现证据。

**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为全部后端服务（主要 admin-api、relay-gateway、billing-service）。

## 变更内容

### 1. P0 — 工程收尾（v0.17 roadmap §3 P0）

- **CI 接入 `-race` 守护**：新增 `make test-race` 目标并接入 `.github/workflows/ci.yml`，覆盖 `domain/subscription/...`、`internal/biz/...`、`internal/server/...` 并发相关包；未来并发测试若自身有 data race，CI 变红。
- **补齐 TODO 日志**：`app/channel/internal/service/anthropic_model_probe.go:168` 的 `// TODO: add logging` 补结构化日志（失败原因、耗时），对齐相邻 probe 风格；仓库非测试代码 TODO 清零（仅保留有意澄清为设计 NOTE 的历史项）。
- **docs 索引对齐**：`docs/README.md` 指向 v0.17 路线图并补齐 v0.16.0 release 索引；`docs/TODO.md` 收尾节与 v0.16.0 发布状态一致。

**影响**：工程卫生，无对外行为变化。

### 2. P1 — 运营闭环（charge 之后，v0.17 roadmap §3 P1）

- **charge 后监控告警**：基于 `relay_token_usage_shadow_cost` / `billing_ledgers` 聚合，为 cache-creation charge 计费的关键场景新增 Prometheus 告警规则（`deploy/prometheus/alerts/alerts.yml`），并新增运行手册 [cache-creation-charge-monitoring.md](../runbooks/cache-creation-charge-monitoring.md) 记录告警口径。
- **对账自动化**：`scripts/reconcile/` 补齐一键对账（`reconcile.sh` + `checks.go` + 供应商账单 CSV 模板），覆盖账本双写一致性、未定价成本、毛利异常等；README 记录阈值校准方法（首次上线在观察模式校准后固化为生产基线）。
- **发布门禁补完 —— 强制失败验证**：新增 `scripts/verify-forced-failure.sh` + `scripts/verify/forced_failure_checks.go`，对普通渠道与订阅账号分别强制失败，断言三项验收（回退可观测、来源归属准确、账单只落一次）；运行手册 [post-release-forced-failure-verification.md](../runbooks/post-release-forced-failure-verification.md) 记录手动执行清单与结论回写要求。v0.17.0 本次发布门禁执行记录见下方「验证」节。

**影响**：admin-api、relay-gateway、billing-service（监控/对账/验证为运维与脚本交付，无生产行为变更）。

### 3. 安全修复

- **nanoid CVE-2026-67213（code-scanning #270）**：`web/package-lock.json` 中 nanoid `3.3.16 → 3.3.17`。受影响的 3.3.16（及更早）存在密码学非安全随机数生成缺陷下的无限循环拒绝服务（`node:crypto` 不可用时回退到 `Math.random` 生成的 `-` 前缀导致 `while(true)`）。修复版 3.3.17 解决了该回退路径。影响服务：admin-api 前端。
- **js-yaml GHSA-5p4m-2wfm-xmqj（code-scanning #269）**：通过 `web/package.json` override 将传递依赖 js-yaml 从 4.3.0 提升到 4.3.1，修复 `!!omap` 默认 schema 解析的二次方 CPU 拒绝服务；lockfile 去重为单一 4.3.1 安装。

**影响**：admin-api 前端依赖修复，无行为变更。`npm ci` 后 `npm audit` 0 漏洞。

### 4. CI 修复

- **Docker CI 矩阵修复（9 服务 × 2 架构 = 18 job）**：原 `ci.yml` 用 `include` 数组 + 独立 `platform` 数组维度组合，GitHub Actions 矩阵语义会折叠为最后一个 include 条目，导致只实际构建 notify-worker 两个平台（2 job）。现改为 `services` job 显式用 jq 计算 9 服务 × 2 平台笛卡尔积（18 个 include 条目），Docker job 据此全量展开为 18 个构建。同时修复 `$GITHUB_OUTPUT` 的 `matrix-ci=` 前缀与输出 key 引用，使 18 条目被正确消费。**本次 CI run 244 验证：22 个 job（4 基础 + 18 docker）全部 success**。
- **CI 运行时升级**：12 个 Actions 升级到 Node 24 majors（checkout v5、setup-go v6、setup-node v5、setup-buildx v4、setup-qemu v4、build-push v7 等），消除 Node 20 弃用漂移告警。
- **CodeQL v3 → v4**：`security.yml` 5 处引用升级，消除 4 条 Security Pipeline 告警。

**影响**：CI 全量 18 个 docker 镜像（amd64 + arm64）可验证构建；无运行时行为变化。

### 5. P3 — 性能基线与 jsonx 决策证据

- **可复现性能基线工具链修复**：`k6-baseline.js` 吞吐改用 `http_reqs.values.rate`、HTTP 500 不计为成功、显式 `summaryTrendStats`；新增 `SMOKE=1` 快速验证模式；Makefile 的 `benchmark-baseline` 改用 `k6 --summary-export` 正确落盘；新增确定性 mock upstream（`scripts/benchmark/mock-upstream/main.go`）；`docs/design/BASELINE.md` 重写为可复现基线规范。
- **P3.1 运行手册**：[performance-baseline-p31.md](../runbooks/performance-baseline-p31.md) 定义 Linux/amd64 三版本对比（Phase 0 `397e36c` / v0.16.0 `a8e14db` / develop）流程、PromQL 查询集、归档规范与完成清单；已归档 arm64 smoke 结果（仅作脚本验证，不作跨版本结论）。
- **P3.2 jsonx 决策证据**：[p32-jsonx-performance-decision.md](../design/p32-jsonx-performance-decision.md) 记录 arm64 代表性负载微基准 + CPU profile：Unmarshal jsonx 快 2.2–3.8x，Marshal 在小请求 std 快 2.1x、大负载收敛到 ~5%，请求级影响 <0.01%，结论**保留 jsonx 为唯一 JSON 层、不回退 Marshal**；Linux/amd64 复测清单已登记。

**影响**：工程卫生与基准工具链，无对外行为变化。

### 6. 其他

- **gross-profit 指标隔离与口径固话**：`gross_profit_metric_test.go` 不再读共享 `prometheus.DefaultGatherer`，`BillingUsecase` 支持 registry 注入的 `SetGrossProfitMetric`；文档明确 `BillingLedgerGrossProfit` 仅覆盖成功 write-ledger 提交（release/失败/早退不计），配置 `scripts/reconcile/README.md` 阈值校准章节。影响 billing-service（测试 + 文档口径，无生产行为变化）。
- **Grafana dashboard 统一为 raw grafana 格式**：billing / relay-gateway / service-dependencies 三个 dashboard 统一格式。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **依赖**：nanoid `3.3.16 → 3.3.17`、js-yaml `4.3.0 → 4.3.1`（web 前端传递依赖提升）。
- **CI**：`services` job 新增 `matrix-ci` 输出并显式计算 18 个 docker include 条目；`make test-race` 新增目标。
- **运行时**：无行为变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.17.0

# 无数据库迁移；如涉及 docker-compose 部署，重新构建发布的服务镜像即可：
# 受影响服务主要为 admin-api、relay-gateway、billing-service，
# 但本版本已全量验证 9 服务 × amd64/arm64 构建，建议按部署集整体更新。
./scripts/deploy-update.sh admin-api relay-gateway billing-service
```

## 验证

- `make test-unit`、`make test-race`、`./scripts/check-architecture.sh`：全绿。
- `cd web && npm run lint && npm test && npm run build`：全绿；`npm audit` 0 漏洞。
- **Docker CI 矩阵**：CI run 244（commit `13589bd`）22 个 job 全部 success，其中 18 个 docker 构建覆盖 9 服务 × {linux/amd64, linux/arm64}。
- 本地 `npm ci` 安装得 nanoid 3.3.17、js-yaml 4.3.1，无漏洞告警。

### 发布后强制失败验证执行记录

> 对应 [post-release-forced-failure-verification.md](../runbooks/post-release-forced-failure-verification.md) §四。本记录以 staging 环境执行，命令引用 `scripts/verify-forced-failure.sh`。

| 项 | 场景一（禁用普通渠道） | 场景二（禁用订阅账号） |
|----|------------------------|------------------------|
| 被禁用的来源 id | 渠道 `CHANNEL_ID`（staging 回填） | 订阅账号 `SUB_ACCOUNT_ID`（staging 回填） |
| 实际服务来源 kind | subscription | channel |
| fallback reason label | 待回填（如 `upstream_5xx` / `circuit_open`） | 待回填 |
| reservation id | 待回填 | 待回填 |
| 账本行数（每 cost_source） | 待回填（期望 1） | 待回填（期望 1） |
| charged quota | 待回填 | 待回填 |

执行环境：staging（待发布前在安静环境执行并回填上表）。执行命令：

```bash
ADMIN_BASE=http://<admin>:3000 RELAY_BASE=http://<relay>:8080 \
ADMIN_TOKEN=<admin-token> API_TOKEN=<user-api-token> \
TEST_MODEL=gpt-3.5-turbo TEST_USER_ID=<user-id> \
CHANNEL_ID=<渠道id> SUB_ACCOUNT_ID=<订阅账号id> \
RECONCILE_DSN=<mysql-dsn> ./scripts/verify-forced-failure.sh
```

三项验收：① 回退可观测（`micro_one_api_routing_fallback_total{reason}` 增长、routing-ops `fallback_rate>0` 且 `partial=false`）；② 来源归属只落在实际服务维度；③ 账单只落一次（`(reference_id, cost_source)` 恰好一行、dedupe key 无重复）。**本记录需在正式发布前于 staging 环境实际执行并回填上述字段后，方可视为发布门禁通过。**

## 完整变更日志

- docs(release): 发版流程在合并 main 前先推送 develop
- chore(p0): complete v0.17 roadmap P0 — CI race guard, probe logging, docs index
- chore(p1): complete v0.17 roadmap P1 — charge monitoring, reconciliation automation, forced-failure verification
- fix(billing): isolate gross-profit metric for tests + document scope and threshold calibration
- fix(monitoring): unify dashboard files to raw grafana format
- ci: upgrade actions to Node 24 runtimes (fix Node 20 deprecation warnings)
- ci(security): upgrade codeql-action v3 -> v4
- chore(benchmark): repair reproducible performance baseline
- fix(deps): bump js-yaml to 4.3.1 (GHSA-5p4m-2wfm-xmqj)
- chore(p3): P3.1 baseline harness fixes + P3.2 jsonx benchmark evidence
- fix(deps): bump nanoid 3.3.16 -> 3.3.17 (CVE-2026-67213)
- fix(ci): expand Docker matrix to full 9 services x 2 platforms (18 jobs)
- fix(ci): write matrix-ci output with key prefix for GITHUB_OUTPUT
- fix(ci): reference matrix-ci output key correctly
