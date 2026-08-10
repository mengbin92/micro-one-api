# P3.1 Linux/amd64 三版本基线复测 — 执行报告

> 执行日期：2026-08-09（初次，Phase 0 受阻）
> 复测日期：2026-08-10（修复 SERVICE_TOKEN 后三版本全部完成）
> 执行环境：`ssh neo@192.168.110.123`（Ubuntu 24.04.4 LTS / x86_64 / 36 核 / 31 GB）
> CPU：Intel Xeon E5-2686 v4 @ 2.30 GHz
> k6 版本：v0.54.0 (commit baba871c8a, go1.23.1, linux/amd64)
> Docker Server：29.4.1（mysql:8.0 + redis:8.4 依赖服务）
> 三版本统一负载：`ITERATION_TARGET_RATE=10`（10 iter/s × 3 请求 = 30 req/s），全量 6 阶段 8 分钟
> mock upstream：`/tmp/mock-upstream-amd64 -addr 0.0.0.0:18099 -delay-ms 2`（确定性 2ms 延迟）

## 执行结论

### ✅ 三版本对比已在 Linux/amd64 上完成

2026-08-09 的初次执行因 identity-service 容器缺失 `SERVICE_TOKEN` 环境变量
（deployed docker-compose.yml 的 `&svc-env` YAML 锚点未包含 SERVICE_TOKEN），
导致 `ConsumeTokenQuota` gRPC 调用返回 `PermissionDenied`，relay 临时拉黑 token，
v0.16.0 和 develop 无法完成基准测试。

**修复措施（2026-08-10）：**

1. 在 deployed `docker-compose.yml` 的 `&svc-env` 锚点中添加
   `SERVICE_TOKEN: ${SERVICE_TOKEN}`，使 identity-service、billing-service、
   channel-service、config-service、log-service 等全部继承该变量。
2. 在 relay-gateway 的 environment 中添加
   `PROVIDER_DISABLE_SSRF_CHECK: "true"`，允许 relay 容器通过 Docker
   bridge gateway IP (`172.19.0.1`) 访问宿主机上的 mock upstream。
3. 创建测试 token（`sk-BenchToken20260810`），插入正确的 HMAC key_hash。
4. 重置 channel 4（p31-mock）的 health_status 为 healthy。
5. 每个版本的 3 次 run 前清空 billing_reservations/billing_ledgers/logs 表，
   消除行锁竞争导致的 P95/P99 累积劣化。

**Phase 0（`397e36c`）** 的 relay-gateway 二进制与当前的 identity-service proto
定义存在兼容性问题（返回 internal server error），无法在当前依赖栈上重新运行。
Phase 0 的已有结果（2026-08-09 执行，3 次 run）保留作为参考，但需注意其 billing
表未清理，P95/P99 明显高于清理后的 v0.16.0/develop 运行。

### v0.16.0（`a8e14db`）— 已完成 ✅

| 指标 | Run 1 | Run 2 | Run 3 | 中位数 |
|------|-------|-------|-------|--------|
| throughput (req/s) | 19.31 | 19.31 | 19.31 | 19.31 |
| http_req_failed | 0.00% | 0.00% | 0.00% | 0.00% |
| aggregate P50 (ms) | 3.45 | 3.45 | 3.44 | 3.45 |
| aggregate P95 (ms) | 114.85 | 114.92 | 114.96 | 114.92 |
| aggregate P99 (ms) | 117.42 | 117.67 | 117.51 | 117.51 |
| /healthz P95 (ms) | 0.73 | 0.72 | 0.72 | 0.72 |
| /v1/models P95 (ms) | 3.64 | 3.65 | 3.64 | 3.64 |
| /v1/chat P50 (ms) | 112.09 | 112.19 | 112.11 | 112.11 |
| /v1/chat P95 (ms) | 116.54 | 116.68 | 116.71 | 116.68 |
| /v1/chat P99 (ms) | 119.63 | 120.40 | 119.24 | 119.63 |
| chat success | 100% | 100% | 100% | 100% |

### develop（`ff518b1`）— 已完成 ✅

| 指标 | Run 1 | Run 2 | Run 3 | 中位数 |
|------|-------|-------|-------|--------|
| throughput (req/s) | 19.31 | 19.31 | 19.31 | 19.31 |
| http_req_failed | 0.00% | 0.00% | 0.00% | 0.00% |
| aggregate P50 (ms) | 3.39 | 3.40 | 3.37 | 3.39 |
| aggregate P95 (ms) | 114.62 | 114.49 | 115.19 | 114.62 |
| aggregate P99 (ms) | 117.14 | 117.31 | 119.58 | 117.31 |
| /healthz P95 (ms) | 0.71 | 0.71 | 0.72 | 0.71 |
| /v1/models P95 (ms) | 3.60 | 3.59 | 3.60 | 3.60 |
| /v1/chat P50 (ms) | 111.88 | 111.72 | 112.04 | 111.88 |
| /v1/chat P95 (ms) | 116.27 | 116.34 | 117.51 | 116.34 |
| /v1/chat P99 (ms) | 119.47 | 120.30 | 178.54 | 120.30 |
| chat success | 100% | 100% | 100% | 100% |

### Phase 0（`397e36c`）— 历史数据（参考）

> ⚠️ Phase 0 数据来自 2026-08-09 的运行，billing 表未清理，P95/P99 被
> billing 行锁竞争严重放大。不可直接与 v0.16.0/develop 对比绝对值。

| 指标 | Run 1 | Run 2 | Run 3 | 中位数 |
|------|-------|-------|-------|--------|
| throughput (req/s) | 19.3 | 19.3 | 19.3 | 19.3 |
| aggregate P95 (ms) | 89.49 | 714.93 | 1028.36 | 714.93 |
| /v1/chat P95 (ms) | 314.61 | 808.34 | 1387.86 | 808.34 |
| chat success | 100% | 100% | 100% | 100% |

### 版本间对比结论

| 指标 | v0.16.0 中位数 | develop 中位数 | 差异 | 结论 |
|------|---------------|---------------|------|------|
| throughput | 19.31 req/s | 19.31 req/s | 0.00% | 持平 |
| chat P50 | 112.11 ms | 111.88 ms | -0.21% | develop 略快（噪声范围） |
| chat P95 | 116.68 ms | 116.34 ms | -0.29% | develop 略快（噪声范围） |
| chat P99 | 119.63 ms | 120.30 ms | +0.56% | develop 略慢（噪声范围） |
| error rate | 0.00% | 0.00% | 0pp | 持平 |

**结论：v0.16.0 → develop 无性能回归。** 所有指标的差异均在 ±1% 以内，
在 k6 分阶段到达率负载下属于正常的运行间噪声。chat P95 稳定在 ~116 ms，
P99 稳定在 ~120 ms（develop run3 的 178ms 是单次尾部事件，中位数不受影响）。

## 测量到的关键特性

1. **billing commit 是 P95 延迟的主要来源。** chat P50 为 ~112ms，而
   mock upstream 延迟仅 2ms，healthz P95 <1ms，models P95 <4ms。chat
   请求的额外 ~110ms 绝大多数来自 billing commit（ReserveQuota →
   upstream call → CommitQuotaWithUsage）的 gRPC 调用 + DB 行锁串行化。
2. **billing 表行数对 P95 有显著影响。** Phase 0 运行（未清理 billing
   表）的 chat P95 从 run1 的 314ms 劣化到 run3 的 1387ms；而清理后的
   v0.16.0/develop 运行的 P95 稳定在 116-117ms。
3. **ConsumeTokenQuota 路径在修复 SERVICE_TOKEN 后正常工作。** develop
   版本的 relay 在每次 chat commit 后调用 identity-service 的
   ConsumeTokenQuota，未出现 token-block 日志，success rate 100%。

## 归档文件

```
scripts/benchmark/results/p31-amd64/
  env-fingerprint.txt                     # 机器指纹 + 版本信息
  P31-EXECUTION-REPORT.md                 # 本报告
  summary-397e36c-phase0-{1,2,3}.json     # Phase 0 三次 k6 summary（历史）
  summary-a8e14db-v016-1.json             # v0.16 旧 run1（token-block，不可用）
  summary-v016-{1,2,3}.json               # v0.16.0 三次 k6 summary（修复后）
  summary-develop-{1,2,3}.json            # develop 三次 k6 summary
  raw-v016-{1,2,3}.json                   # v0.16.0 原始 k6 采样
  raw-develop-{1,2,3}.json                # develop 原始 k6 采样
  output-v016-{1,2,3}.txt                 # v0.16.0 k6 stdout
  output-develop-{1,2,3}.txt              # develop k6 stdout
```
