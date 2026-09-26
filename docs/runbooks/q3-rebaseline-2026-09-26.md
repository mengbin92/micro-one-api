# Q3 性能基线重定（ff518b1 → v0.32.2）

> 日期：2026-09-26
> 基线变更：`Q3_BASELINE_REF` 由 `ff518b1` 改为 `v0.32.2`
> 证据：GitHub Actions run `36218523742`（artifact `q3-linux-amd64-36218523742`）

## 背景

`Q3 Linux amd64 comparison` workflow（develop `9adea747` 引入）用于在归档
x86 压测机不可达后，于同一 `ubuntu-24.04` runner 上对比归档基线
`ff518b1`（v0.17.1 时期）与候选提交。首跑（`36215032026`）因基线 compose
的 socket 型 mysql healthcheck 在 initdb 期间提前变 Healthy，migrate 连接
`mysql:3306` 被拒而失败；已通过 harness overlay 强制 TCP 探测修复
（develop `ef9ea07`，含失败时 dump migrate/mysql 日志的诊断增强）。

## 2026-09-26 首组完整对照证据

修复后 run `36218523742` 首次跑通两侧各 3 轮全量 k6（8 分钟到达率剖面，
2ms mock 上游，相同 synthetic fixture，版本间重建 MySQL/Redis 卷并清空
事务样本）。6 轮数据方差极小，同机对比结论稳定：

| 指标 | ff518b1（baseline） | develop `ef9ea07`（candidate） | 门禁（±20%） |
|------|--------------------:|--------------------------------:|--------------|
| chat P95 | 39.88 ms | 49.94 ms（+25.2%） | FAIL |
| aggregate P95 | 38.20 ms | 47.86 ms（+25.3%） | FAIL |
| models P95 | 1.59 ms | 1.69 ms（+6.1%） | PASS |
| healthz P95 | 0.50 ms | 0.51 ms（+2.3%） | PASS |
| throughput | 19.3062 req/s | 19.3062 req/s | PASS |
| HTTP 错误率 / dropped | 0 / 0 | 0 / 0 | PASS |

candidate 三轮 aggregate P95 为 47.49 / 47.86 / 47.99 ms，非噪声。
models/healthz 等轻路径仅 +2~6%，回退集中在 relay chat 主链路
（+10 ms）。

该对照横跨 436 个提交（v0.17.1 → v0.32.2），期间的请求幂等、账务
dedupe/快照、routing v2、订阅计费等均会增加热链路开销。将此 +25%
记为跨版本功能成本的历史结论，不作为单一回归事件追踪；若未来需要
追回，先按版本区间二分定位再优化。

绝对延迟与 2026-08-10 归档机（Xeon E5-2686 v4，chat P95 116.34 ms）不可
直接比较——runner 规格不同，只有同一 job 内的两侧差值有效。

## 基线重定

- `scripts/benchmark/run-q3-ci.sh`：`Q3_BASELINE_REF` 默认 `ff518b1` →
  `v0.32.2`；每次发版随最新 release tag 递增。
- 基线 tag 的 compose 已原生携带 TCP 型 mysql healthcheck（v0.32.2 验证），
  harness overlay 中的覆盖保留作兜底，防止未来基线回落到旧探测方式。
- 候选侧与 `v0.32.2` 无服务代码差异时，该 job 等价于自检：预期差值 ≈ 0，
  可用来验证 harness/fixture 本身无漂移。

## 原始数据

- run `36215032026`（migrate 竞态失败）：artifact `q3-linux-amd64-36215032026`
- run `36218523742`（首组完整对照）：artifact `q3-linux-amd64-36218523742`，
  含两侧 compose 构建日志、6 份原始 k6 summary、raw JSON 样本与
  `regression-report.json`
