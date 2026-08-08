# P3.2 — jsonx Marshal / Encoder 性能决策

> 状态:初步证据已产出(Apple Silicon arm64 微基准 + CPU profile)
> **延期决策(2026-08-08)**:v0.17.0 发布不依赖 P3.2;Linux/amd64 复测与最终
> 决策推迟到 v0.17.x / v0.18 独立推进(见 [v0.17 路线图 §3 P3 延期决策](./v0.17-roadmap.md))。
> 待办:Linux/amd64 复测后固化最终决策(延期执行)
> 关联:[v0.17 路线图 §P3.2](./v0.17-roadmap.md) · `pkg/jsonx/bench_representative_test.go` · `internal/apicompat/bench_test.go`

## 1. 背景与目标

v0.15.3 将全仓库 JSON 序列化统一收敛到 `pkg/jsonx`(底层 sonic `ConfigStd`,保持
`encoding/json` 语义)。v0.17 P3.2 要求回答:Marshal 方向 sonic 比 std 慢(Apple Silicon
微基准确认小结构约慢 2.1x),是否应该把 Marshal 单独回退到 `encoding/json`?

本决策只允许在 **Linux/amd64 代表性负载上出现可重复的请求级改善** 时才回退
(roadmap 原文:只有 JSON 编码占比或总体 CPU/吞吐/尾延迟出现可重复改善时才评估)。
本文件记录第一步证据:arm64 代表性负载微基准 + CPU profile,用于方法验证与方向判断;
最终结论待 Linux/amd64 复测(见 §5)。

## 2. 方法

- **benchmark fixtures**(新增,按 roadmap 要求的代表性负载):
  - 小 request struct(chat completion 请求,既有 `json_test.go`)
  - 大 Responses response(3 个 output item:reasoning + 长文本 message + function_call,五桶 usage)
  - Anthropic 流式 delta 事件(长 text delta,adaptor 逐事件序列化点)
  - admin/billing 聚合响应(map + slice,5 行 per-model 数据)
  - `NewEncoder` 写 `io.Discard`(大 Responses response)
  - apicompat 热路径整体转换:`AnthropicToResponses`、`ResponsesToChatCompletionsRequest`、
    `AnthropicToResponsesResponse`、`ResponsesEventToSSE`
- 每个 benchmark `-count=3`,取中位数;`-benchmem` 记录分配;CPU profile 用
  `-cpuprofile` + `go tool pprof -top`。
- 环境:**Apple M5 Pro / darwin / arm64 / go1.26.5** — 本文件数据仅作方法验证与方向
  参考,不作最终结论;归档于 `scripts/benchmark/results/jsonx-p32-arm64-*.txt`。

## 3. 结果(中位数,arm64)

### 3.1 pkg/jsonx 对比(sonic ConfigStd vs encoding/json)

| Benchmark | jsonx | std | jsonx/std | 方向 |
|-----------|-------|-----|-----------|------|
| Unmarshal 小请求 | 385.8 ns | 865.4 ns | 0.45x | **jsonx 快 2.2x** |
| Marshal 小请求 | 328.7 ns | 154.0 ns | 2.13x | std 快 2.1x |
| Marshal LargeResponses (~3KB) | 2364 ns | 2159 ns | 1.10x | std 快 1.10x |
| Unmarshal LargeResponses | 2949 ns | 11179 ns | 0.26x | **jsonx 快 3.8x** |
| Marshal AnthropicDelta (~1.3KB) | 844 ns | 876 ns | 0.96x | 持平 |
| Marshal AggMapSlice | 2412 ns | 2296 ns | 1.05x | 持平 |
| Unmarshal AggMapSlice | 1554 ns | 3294 ns | 0.47x | **jsonx 快 2.1x** |
| Encoder LargeResponses | 2506 ns / 4 allocs | 1982 ns / 0 allocs | 1.26x | std 快 1.26x、0 allocs |

### 3.2 apicompat 热路径整体吞吐(sonic,无 std 对比,绝对参考)

| 转换 | ns/op | allocs/op |
|------|-------|-----------|
| AnthropicToResponses(带 tools + 3 消息) | ~16.0 µs | 130 |
| ResponsesToChatCompletionsRequest | ~14.4 µs | 178 |
| AnthropicToResponsesResponse | ~0.57 µs | 9 |
| ResponsesEventToSSE(流式逐事件) | ~2.75 µs | 14 |

### 3.3 CPU profile(sonic Marshal + Unmarshal 大响应)

热点为 sonic neon 原生汇编路径:`encoder/vm.Execute`(12.8% cum)、
`__parse_with_padding_entry__`、`__validate_utf8_fast_entry__`、`__quote_entry__`、
`alg.Quote` — 确认 sonic 在 arm64 走 neon 加速路径,Unmarshal 的 2–4x 优势来自此。

## 4. 初步分析与方向(arm64 证据)

1. **Marshal 差距随负载增大而收敛**。小结构 std 快 2.1x;但 ~1.3KB 的 Anthropic delta
   已持平(0.96x),~3KB 的 Responses 响应仅差 1.10x,map+slice 仅差 1.05x。relay 真实
   负载集中在流式事件与完整响应,恰恰是差距最小的区间。
2. **请求级影响可忽略**。小结构 Marshal 绝对差 ~175ns、大结构差 ~200ns;对照请求 P95
   延迟 38ms 级别(见 BASELINE.md),占比 <0.01%。apicompat 整体转换 14–16µs/op 是
   更真实的请求级成本,且与 std 无对比参照(该成本主要来自多次 Unmarshal,恰是 sonic
   的优势区)。
3. **Unmarshal 收益显著(2.1–3.8x)且是热路径主体** — 上游响应解析、SSE 事件解析均为
   Unmarshal 主导,回退 Marshal 不会破坏这部分收益。
4. **NewEncoder 独立**:std 快 1.26x 且 0 allocs vs 4 allocs。Encoder 在仓库中使用点
   需单独盘点(见 §5 清单),不得随 Marshal 决策一并假设。

**初步方向(arm64,待 Linux/amd64 复核):保留 `pkg/jsonx` 单一封装层,不将 Marshal
单独回退 std。** 理由:差距集中在绝对耗时 <1µs 的小结构;大结构(真实负载形状)差距
收敛到 ~5%;请求级影响 <0.01%;回退会破坏单一 JSON 语义层,且无法带来可测的请求级
改善。NewEncoder 维持现状,待 §5 复核。

## 5. Linux/amd64 复核清单(最终决策依据)

在固定 Linux/amd64 runner 上执行:

```bash
# 1. 代表性负载微基准(count=5 取中位数)
go test -bench . -benchmem -count=5 -run xxxNONExxx ./pkg/jsonx/ ./internal/apicompat/
# 归档:scripts/benchmark/results/jsonx-p32-amd64-<sha>-<ts>.txt

# 2. CPU profile(Marshal vs Unmarshal 热点占比)
go test -bench 'BenchmarkMarshalLargeResponsesJSONX|BenchmarkUnmarshalLargeResponsesJSONX' \
  -benchmem -cpuprofile /tmp/jsonx-p32-cpu.prof -run xxxNONExxx ./pkg/jsonx/
go tool pprof -top /tmp/jsonx-p32-cpu.prof

# 3. NewEncoder 使用点盘点(独立决策)
grep -rn "jsonx.NewEncoder\|json\.NewEncoder" --include="*.go" internal/ app/ domain/ | grep -v _test

# 4. 请求级影响估算
#   用 §3.2 的热路径整体基准 + 生产 P95 延迟与 req/s,计算 JSON 编码占比;
#   仅当占比或总体 CPU/吞吐/尾延迟出现可重复改善时才评估 Marshal 回退 std。
```

复核完成后,将最终决策写回本文件(状态改为「已决策」)并在发布说明中声明。

## 6. 归档

- 原始输出:`scripts/benchmark/results/jsonx-p32-arm64-20260807-*.txt`
- CPU profile:`/tmp/jsonx-p32-cpu.prof`(本机临时,不入库)
- benchmark 源码:`pkg/jsonx/bench_representative_test.go`(外部测试包,避免
  apicompat→jsonx 的 import cycle)、`internal/apicompat/bench_test.go`
