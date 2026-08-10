# P3.2 — jsonx Marshal / Encoder 性能决策

> 状态:初步证据已产出(Apple Silicon arm64 微基准 + CPU profile)
> **已决策(2026-08-10)**:Linux/amd64 复测完成,sonic 在 Marshal 和 Unmarshal 方向均全面优于 std。
> 最终决策:**保留 `pkg/jsonx` 单一封装层,不回退任何方向到 `encoding/json`。**
> 已完成:Linux/amd64 代表性负载微基准(count=5)+ CPU profile,最终决策已固化。
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

## 3. 结果

### 3.0 Linux/amd64 复测结果(2026-08-10,最终决策依据)

> 环境:Intel Xeon E5-2686 v4 @ 2.30 GHz / Ubuntu 24.04 / go1.26 / 36 核 / 31 GB
> count=5 取中位数,归档于 `scripts/benchmark/results/p32-amd64/`


#### pkg/jsonx 对比(sonic ConfigStd vs encoding/json)— Linux/amd64

| Benchmark | jsonx (sonic) | std (encoding/json) | jsonx/std | 方向 |
|-----------|---------------|---------------------|-----------|------|
| Unmarshal 小请求 | 2069 ns / 7 allocs | 7161 ns / 12 allocs | 0.29x | **jsonx 快 3.46x** |
| Marshal 小请求 | 1151 ns / 3 allocs | 1501 ns / 2 allocs | 0.77x | **jsonx 快 1.30x** |
| Marshal LargeResponses (~3KB) | 5500 ns / 2 allocs | 16527 ns / 1 allocs | 0.33x | **jsonx 快 3.00x** |
| Unmarshal LargeResponses | 16961 ns / 31 allocs | 79952 ns / 40 allocs | 0.21x | **jsonx 快 4.71x** |
| Marshal AnthropicDelta (~1.3KB) | 2539 ns / 2 allocs | 6679 ns / 1 allocs | 0.38x | **jsonx 快 2.63x** |
| Marshal AggMapSlice | 9064 ns / 8 allocs | 20845 ns / 55 allocs | 0.43x | **jsonx 快 2.30x** |
| Unmarshal AggMapSlice | 13357 ns / 69 allocs | 26908 ns / 97 allocs | 0.50x | **jsonx 快 2.01x** |
| Encoder LargeResponses | 6382 ns / 4 allocs | 6729 ns / 0 allocs | 0.95x | **jsonx 快 1.05x**(持平,差距<5%) |

> **关键发现:在 Linux/amd64 上,sonic 在所有 Marshal 和 Unmarshal 场景均优于或持平 encoding/json。**
> 这与 arm64 的初步结论(Marshal 慢 2.1x)完全相反——原因是 arm64 的 sonic neon 汇编
> 路径在 Marshal 方向有开销,而 amd64 的 SSE/AVX 路径在 Marshal 方向同样高效。
> 小请求 Marshal jsonx 快 1.30x(1151 vs 1501 ns);大请求 Marshal jsonx 快 3.0x(5500 vs 16527 ns)。

#### apicompat 热路径整体吞吐(sonic,无 std 对比)— Linux/amd64

| 转换 | ns/op | allocs/op |
|------|-------|-----------|
| AnthropicToResponses(带 tools + 3 消息) | ~91.7 µs | 106 |
| ResponsesToChatCompletionsRequest | ~68.2 µs | 140 |
| AnthropicToResponsesResponse | ~2.4 µs | 9 |
| ResponsesEventToSSE(流式逐事件) | ~17.8 µs | 14 |

#### CPU profile(Marshal + Unmarshal 大响应,Linux/amd64)

热点为 sonic 的 encode/decode 汇编路径:`_quote`(8.93%)、`decode_`(5.36%)、
`encode_apicompat.ResponsesOutput`(4.02%)、`encode_*apicompat.ResponsesResponse`(0.89%)。
GC 相关:`runtime.scanObject`(8.26%)。无异常热点。

### 3.1 arm64 初步结果(方法验证,不作最终结论)

> ⚠️ 以下 arm64 数据仅用于方法验证。**Linux/amd64 结论与 arm64 相反**(见 §3.0),
> 最终决策依据为 Linux/amd64 数据。
### 3.2 pkg/jsonx 对比 — arm64(初步证据,2026-08-07)

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

**最终决策(Linux/amd64,2026-08-10):保留 `pkg/jsonx` 单一封装层,不回退任何方向到 `encoding/json`。**

理由:
1. **Linux/amd64 上 sonic 在所有方向均优于或持平 std。** Marshal 小请求快 1.30x、
   大请求快 3.0x、AnthropicDelta 快 2.63x;Unmarshal 快 2.0–4.7x。Encoder 持平(0.95x)。
2. **arm64 的 Marshal 慢 2.1x 是架构特定问题**,不影响 Linux/amd64 生产环境。
3. **单一 JSON 语义层**(HTML escaping、sorted map keys、copied strings)不可破坏。
4. **NewEncoder** 维持现状:仅 1 个 relay 热路径使用点(error handler),其余为
   admin/health 端点,均非性能瓶颈。
5. 版本边界注意事项(go1.27+ 回退 std)仍有效,保持 go.mod 在 go 1.26。

## 5. Linux/amd64 复核执行记录

已在固定 Linux/amd64 runner 上执行(2026-08-10):

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

**✅ 复核已完成。** 最终决策见 §4。原始数据:
- `scripts/benchmark/results/p32-amd64/jsonx-p32-amd64-ff518b1-20260810.txt`
- `scripts/benchmark/results/p32-amd64/apicompat-p32-amd64-ff518b1-20260810.txt`
- `scripts/benchmark/results/p32-amd64/jsonx-cpuprofile-amd64-20260810.txt`

NewEncoder 使用点盘点:全部 21 处使用 `jsonx.NewEncoder`(0 处 `json.NewEncoder`),
仅 1 处在 relay 热路径(`internal/server/handler/chat.go:167`,error handler,非主路径)。

## 6. 归档

- arm64 原始输出:`scripts/benchmark/results/jsonx-p32-arm64-20260807-*.txt`
- amd64 原始输出:`scripts/benchmark/results/p32-amd64/jsonx-p32-amd64-ff518b1-20260810.txt`
- amd64 apicompat:`scripts/benchmark/results/p32-amd64/apicompat-p32-amd64-ff518b1-20260810.txt`
- CPU profile:`/tmp/jsonx-p32-cpu.prof`(本机临时,不入库)
- benchmark 源码:`pkg/jsonx/bench_representative_test.go`(外部测试包,避免
  apicompat→jsonx 的 import cycle)、`internal/apicompat/bench_test.go`
