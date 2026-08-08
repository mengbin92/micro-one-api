# P3.1 — 可复现性能基线执行 Runbook(Linux/amd64)

> 状态:脚本与工具链已修复并完成 arm64 smoke 验证(2026-08-07)
> **延期决策(2026-08-08)**:v0.17.0 发布不依赖 P3.1;Linux/amd64 三版本对比
> 推迟到 v0.17.x / v0.18 独立推进(见 [v0.17 路线图 §3 P3 延期决策](./../design/v0.17-roadmap.md))。
> 待办:在 Linux/amd64 上执行三版本对比并回填 `docs/design/BASELINE.md`(延期执行)
> 关联:[v0.17 路线图 §P3.1](./../design/v0.17-roadmap.md) ·
> `scripts/benchmark/README.md` · `docs/design/BASELINE.md`

## 1. 目标

在同一台 **Linux/amd64** 机器、同一配置与数据集上,分别对三个 Git 版本各跑
**至少 3 次** k6 基线,归档原始结果,回填 BASELINE.md,使版本间可公平比较:

| 版本 | Git SHA | 说明 |
|------|---------|------|
| Phase 0(重构前) | `397e36c` | 架构重构基线 |
| v0.16.0 | `a8e14db` | 最近发布版 |
| develop(执行时) | 执行时 `git rev-parse --short HEAD` | 当前开发版 |

> ⚠️ **不要**用 Apple Silicon 结果做跨版本结论(arm64 smoke 数据仅作脚本验证,
> 见 `scripts/benchmark/results/` 中 `*-smoke-*.json` 与 `jsonx-p32-arm64-*.txt`)。

## 2. 前置条件(在 Linux/amd64 机器上)

- **k6 stable**:`curl https://dl.k6.io/...` 或发行包安装;**三版本必须用同一 k6 版本**。
  ⚠️ 已知问题:k6 v2.1.0 devel 的 `--summary-export` 中 rate 指标 `passes/fails` 字段
  语义反转、`thresholds` 评估字段不可靠;**以 stdout 汇总与退出码为准**(0=阈值全过)。
- **Go 环境(仅构建用)**:本机或交叉编译机 `GOOS=linux GOARCH=amd64`。
- **docker compose 部署环境**:`deployments/docker-compose/`(relay + 依赖服务)。
  若目标机无 Go/构建能力,在开发机交叉构建镜像/二进制后上传(见 §4)。
- **mock upstream**:`scripts/benchmark/mock-upstream/main.go`(仅当前 develop 含此源码,
  历史版本不含——用当前 worktree 的 mock 二进制服务所有版本)。

## 3. 基线环境准备(每版本一次)

### 3.1 部署目标版本

```bash
# 在具备构建能力的机器上交叉编译(不要在生产服务器上构建)
GOOS=linux GOARCH=amd64 go build -o /tmp/mock-upstream-amd64 ./scripts/benchmark/mock-upstream
# 按项目发布流程构建目标版本的镜像并推送到 registry;
# 服务器上 docker compose 拉取该版本 tag 并启动。
# Phase 0 / v0.16.0 用对应 git tag/worktree 构建;develop 用当前 HEAD。
```

### 3.2 启动 mock upstream(服务器本机)

```bash
/tmp/mock-upstream-amd64 -addr 127.0.0.1:18099 -delay-ms 2 &
curl -s http://127.0.0.1:18099/healthz   # 期望 {"status":"ok"}
```

### 3.3 配置渠道指向 mock

在管理后台(或 admin API)创建渠道:

- Provider:OpenAI-compatible
- Base URL:`http://127.0.0.1:18099`(mock 上游,零真实网络 I/O)
- API Key:任意非空测试值
- Models:`gpt-3.5-turbo,gpt-4o-mini`(与 mock 返回一致)
- Group:默认组;Priority/Weight 保持默认

创建 Token(API 密钥),复制完整值作为 `API_KEY`。

### 3.4 冒烟(可选,快速验证环境)

```bash
export BASE_URL=http://localhost:8080
export API_KEY=<创建的 token>
BASE_URL=$BASE_URL API_KEY=$API_KEY SMOKE=1 \
  ITERATION_START_RATE=5 ITERATION_TARGET_RATE=20 \
  k6 run scripts/benchmark/k6-baseline.js
```

冒烟通过后再跑全量(每版本 3 次)。

## 4. 全量基线采集(每版本 ×3)

```bash
cd <repo>/deployments/docker-compose   # 或 repo 根
export BASE_URL=http://localhost:8080
export API_KEY=<创建的 token>
# SMOKE 不设置;ITERATION_TARGET_RATE 按机器能力覆盖(默认 200 iter/s)
for i in 1 2 3; do
  make benchmark-baseline   # 归档 raw-<sha>-<ts>.json + summary-<sha>-<ts>.json
done
```

> `make benchmark-baseline` 已修复:summary 用 k6 原生 `--summary-export` 写出
> (旧 `RESULTS_FILE=` 环境变量 k6 不读取,summary 从未落盘)。命令要求
> `BASE_URL` 与 `API_KEY` 已 export、k6 已安装。

每个版本完成后:

1. 记录机器指纹:`uname -m`、`nproc`、内存、Go 版本、Kratos 版本、k6 版本。
2. 将 3 次 summary 取**中位数**,填 `docs/design/BASELINE.md` 对应版本表格。
3. raw(通常 1MB+/次)不强制入库,但 summary 必须提交或作为 CI artifact 保留。

## 5. Prometheus 内部指标采集(与 k6 同窗口)

k6 只测 HTTP 层;以下 gRPC/内部指标在 k6 运行期间从 Prometheus 查询
(查询集见 `docs/design/BASELINE.md` §Prometheus query reference):

| 指标 | PromQL(5m 窗口) |
|------|-----------------|
| gRPC 服务延迟 P95 | `histogram_quantile(0.95, sum(rate(micro_one_api_dependency_grpc_latency_seconds_bucket[5m])) by (le, service))` |
| billing commit 延迟 P95 | `histogram_quantile(0.95, sum(rate(micro_one_api_billing_commit_duration_seconds_bucket[5m])) by (le, mode))` |
| routing selection 延迟 P95 | `histogram_quantile(0.95, sum(rate(micro_one_api_routing_selection_duration_seconds_bucket[5m])) by (le, source_kind))` |
| 缓存命中率 | 见 BASELINE.md(通用 L1/L2 与 billing quota-cache 查询) |
| 熔断器状态 | `micro_one_api_resilience_circuit_breaker_state` |

> ⚠️ 若 Prometheus 不可用,可用 relay-gateway `/metrics` 直采(admin-api 已支持
> `RELAY_METRICS_ENDPOINT` 降级);gRPC 延迟类指标在直采下不完整,需在结果中注明。

## 6. 归档规范

```text
scripts/benchmark/results/
  raw-<sha>-<timestamp>.json         # k6 原始样本(大,不强制入库)
  summary-<sha>-<timestamp>.json     # k6 summary-export(必须入库)
  env-<sha>-<timestamp>.txt          # 机器指纹 + 版本信息(建议)
```

回填 BASELINE.md 时:
- 历史不可恢复数据(Phase 1/2)保留 `N/A — not recorded` 说明,不以推测值消除。
- 三版本同机同配置;每版本 3 次取中位数;表格注明 k6 版本与机器指纹。

## 7. 本机 smoke 验证记录(2026-08-07,Apple M5 Pro / arm64)

- k6 v2.1.0(devel)对 mock upstream 直打:42.34 req/s、0.00% 失败、0 dropped
  (summary-9abff45-smoke-20260807-154820.json,归档于 scripts/benchmark/results/)。
- 结论:脚本、SMOKE 模式、summary-export、raw 输出链路均可用;arm64 数据仅作
  脚本验证,不作性能结论。

## 8. 完成定义核对

- [ ] 三版本 × 3 次在 Linux/amd64 完成,raw + summary 归档
- [ ] BASELINE.md 表格回填(中位数),版本间可比较
- [ ] Prometheus 内部指标与 k6 同窗口采集并记录
- [ ] 机器指纹(CPU/内存/Go/Kratos/k6 版本)记录在结果中
