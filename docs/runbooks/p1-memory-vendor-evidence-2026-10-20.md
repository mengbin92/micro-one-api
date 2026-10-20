# P1：最大摘要内存与供应商证据详情

日期：2026-10-20（Asia/Shanghai）。执行范围为 [N1/N2](../design/next-stage-plan-2026-10-20.md)，实现与验收完成，完整原生三库 CI 通过。

## 测量前固定预算

- 使用独立原生 Linux/amd64 构建机，不在生产运行压测。Relay、Log 测量进程各在 4 CPU / 1 GiB 容器运行；数据库另置隔离容器。
- Relay 进程 RSS / cgroup 峰值不得超过 **768 MiB**，为同容器正常业务留出至少 **256 MiB**；Log 峰值不得超过 **512 MiB**，留出至少 **512 MiB**。这只是本次受限验收预算，不宣称覆盖生产所有业务和内存配置。
- 同时记录 Go 活跃堆、空载、饱和与排空；三个周期排空后活跃堆相对空载增长最多 **16 MiB**，保留 GC 后测量边界。容器 OOM、峰值超预算、关联缺失或计数失衡均阻断。
- Relay 固定 4096 队列 + 256 worker，最大合法摘要分别拥有独立字符串，不能用同一对象共享内存低估保留量。超出项拒绝，恢复在现有 RPC 期限内完成；另测取消后的有界关闭。
- Log 使用 128 条 × 128 attempts 的最大批次与真实 RPC 往返，验证去重、完整关联和原值读回；不改变旧 P95 门槛。仅当修改生产策略时重跑吞吐长测。

维护者确认线上 Jaeger 已调大为 **768 MiB**。版本化 Compose 原先仍为 512 MiB，本轮已同步为 768 MiB，Compose 展开值、四种配置组合、41 项 Kubernetes schema、83 项引用及文档检查通过。它与 Relay/Log 预算分开记录；原 48h FAIL 和新实例 TTL/WS 的独立验收边界保持。

## 根因与实现

最大合法夹具包含 128 attempts、22 stages、32 reasons 及长度上限字符串，使用独立分配的 `<` 覆盖 HTML 转义扩张。单摘要 JSON **368,407 B**，protobuf **69,409 B**；11 项越界拒绝及 JSON/protobuf 往返通过。

原 128 条批次的一条 INSERT 带 **47,156,096 B JSON**。单库 PostgreSQL 峰值约 633 MiB，超过原定 512 MiB；分配 profile 中 pgx 参数编码与 Bind 缓冲合计约占 75%。[失败回归](evidence/p1-memory-vendor-2026-10-20/statement-budget-red.log)与 [分配记录](evidence/p1-memory-vendor-2026-10-20/postgres-allocations.txt)保留原结果，没有调高门槛。

最小修复：同一事务内将每条 INSERT 的计时 JSON 限为 **4 MiB**；普通小摘要仍走原 `Create` 路径。关联引用每条语句最多 1,024 行、5,120 个参数，保持三库兼容，避免最大引用组过多往返。128 份私有快照、事务原子性、持久去重、2 秒事务期限、Relay worker/队列和 5% P95 门槛保持。无 schema、API 或价格变更。

最大输入 RPC 测试先突发 128 份摘要，再按可持续速率重放；只允许既有的有界 DeadlineExceeded 超载响应，并检查超时组没有部分关联，恢复后恰好 128 logs / 128 claims / 16,384 refs，查询值完整相符。最终三库实际突发的 deadline 响应均为 **0**。这不是任意最大负载下无损送达的承诺。

## 原生最大输入验收

独立 Linux/amd64 构建机，Go 1.27.2；每个数据库/测试进程单独启动 4 CPU / 1 GiB cgroup。RPC harness 将客户端和 Log owner 放在同一进程，所测峰值包含双方；它比单独服务的预算更保守。Relay 使用 background context，不能据此推导任意请求上下文的保留上界。

| 范围 | RSS 峰值 | cgroup 峰值 | 结果与范围 |
| --- | ---: | ---: | --- |
| Relay 最大摘要 4,352 项，三轮饱和/排空 | 740.07 MiB | 728.23 MiB | PASS；每轮全部已接收项导出，失败 0；额外 1 项快速拒绝并计数，排空后活跃堆无持续增长 |
| Log SQLite 最大批次 | 149.47 MiB | 184.37 MiB | PASS；128 条、16,384 refs，重放不新增 |
| Log MySQL 最大批次 | 146.16 MiB | 126.38 MiB | PASS；128 条、16,384 refs，重放不新增 |
| Log PostgreSQL 最大批次，拆分后 | 197.70 MiB | 178.63 MiB | PASS；128 条、16,384 refs，持久去重与读回边界分别验证 |
| 最大摘要 RPC，SQLite | 291.51 MiB | 372.25 MiB | PASS；突发、恢复重放、关联及完整读回 |
| 最大摘要 RPC，MySQL | 280.41 MiB | 252.75 MiB | PASS；突发、恢复重放、关联及完整读回 |
| 最大摘要 RPC，PostgreSQL | 389.04 MiB | 360.64 MiB | PASS；突发、恢复重放、关联及完整读回 |

字节限制是 JSON 数据的语句预算，不包含所有 SQL 元数据；最终资源判断使用进程/cgroup 实测。三轮排空后活跃堆回到约 1.3 MiB，但 Go 仍可能保留已分配页面，不能把活跃堆回落解释为 RSS 立即回落。

本机可复验功能边界（MySQL/PostgreSQL 需提供 `GROUP_CONTEXT_TEST_*_DSN`，未提供会跳过，不算实库通过）：

```bash
go test ./domain/requesttrace -run TestMaximumTimingBoundsAndTransport -count=1
go test ./app/log/internal/data -run 'TestTimingBatchAtMaximumSize|TestTimingBatchFailureDoesNotCommitAnEarlierSummary' -count=1
TIMING_MAXIMUM_RPC=1 go test ./internal/server -run '^TestRequestTimingMaximumRPCBatch$' -count=1 -timeout=3m
```

独立 **Linux/amd64 + Go 1.27.2** 上复验 Relay 资源预算；在专用 cgroup 运行，不能直接把宿主机进程的共享峰值算作本测试峰值：

```bash
probe_dir=$(mktemp -d)
go test -c ./domain/requesttrace -o "$probe_dir/requesttrace.test"
docker run --rm --network none --cpus=4 --memory=1g \
  -v "$probe_dir:/probe:ro" -e TIMING_MAXIMUM_MEMORY=1 \
  golang:1.27.2-bookworm /probe/requesttrace.test \
  -test.run '^TestMaximumTimingExporterMemory$' -test.v -test.timeout=90s
rm -rf "$probe_dir"
```

Log 的 cgroup 验收同样启用 `TIMING_MAXIMUM_MEMORY=1`，每库单独进程运行 `TestTimingBatchAtMaximumSize/<driver>` 或 `TIMING_MAXIMUM_RPC=1` 的 `TestRequestTimingMaximumRPCBatch/<driver>`；按测试源码编译时的绝对路径只读挂载迁移目录，使用隔离 DSN。三库最大批次和 RPC 的原始 JSON/日志均保存在本轮证据目录。

## 供应商证据详情

提交 `1d945b81` 扩展现有只读详情：报告/分页完整性、双方水位、来源映射、覆盖/舍入证据、用途与未知记录、金额差异、归因及更正前序/差额。金额保持十进制字符串，0 与 null/缺失分开，核验结论与后续动作仍来自 owner。现查询只返回更正链末端，不增加历史版本浏览或写入权限。

复用授权 generation 挂载工作区，撤权后移除选中报告，恢复授权也不会自动重开旧详情。前端全量 **573 项**、新增/扩展页面专项 **9 项**、lint、类型检查、构建及原六项资产预算通过。浏览器中英文 × 明暗主题 × 桌面/手机 **8 项**通过，包含完整 JSON 下载比对和页面/详情无横向溢出；已查看 [桌面截图](evidence/p1-memory-vendor-2026-10-20/vendor-desktop-zh-light.png)与 [手机截图](evidence/p1-memory-vendor-2026-10-20/vendor-mobile-en-dark.png)。全量测试与专项有重叠，不累加为总用例数。

## 容量与剩余边界

源码实现为 `64fa1478`。共享构建机完整容量复验出现 P95 超线及一次 MySQL 超时，保留原始 FAIL；旧基线短诊断也有超线，不据此推断某一代码改动就是根因。短诊断与 CPU 亲和调整不能追认为正式通过，也没有降低 5% 门槛。

独立分支的 [原生三库 SQL 容量 workflow](https://github.com/mengbin92/micro-one-api/actions/runs/39397036764) **完整 SUCCESS**，源码 `64fa14782d7bf0ff637333fef884276ae151f750`。原基线、64 并发、四组交替、每模式 30 秒、十轮 race 与暂停探针保持。三库 candidate 的正式容量、data/server/requesttrace race、缓冲探针及收尾/上传全部退出 0；原基线的 64 并发失败独立保留，不改写为通过。

| 数据库 | 四组 HTTP P95 增量 | 完整持久摘要 | 结果 |
| --- | --- | ---: | --- |
| SQLite | 2.2641% / 1.9325% / 1.5262% / 2.6280% | 149,335 | PASS |
| MySQL | 3.2670% / 1.9576% / 2.9118% / 4.6499% | 149,211 | PASS |
| PostgreSQL | 1.6644% / 1.5436% / 1.9752% / 3.0088% | 149,285 | PASS |

合计 **447,831 条摘要 / 5,376,132 个完成及接收 span**。摘要/claim/refs 数量一致，HTTP/查询错误、摘要失败和丢弃均为 0；最高增量 4.6499%，未放宽 5% 门槛。结论只覆盖上述隔离剖面；不以独立 runner 的通过覆盖本地共享构建机的历史失败。

[汇总](evidence/p1-memory-vendor-2026-10-20/summary.json)、[完整索引/校验值](evidence/p1-memory-vendor-2026-10-20/index.json)、[工作流状态](evidence/p1-memory-vendor-2026-10-20/ci-result.json)保留版本、原始通过/失败报告、三库退出码、十轮 race 和硬件/容器边界。业务源码验证后仅同步 Jaeger Compose 内存值和文档，未更改计时实现。

生产 Jaeger 的 768 MiB 是维护者确认的现状；本轮未修改生产、运行真实模型请求或重开观察任务，未改变钱包、收费、权限与灰度。是否上线仍需结合独立生产验收和实际服务内存预算。

按维护者要求，48h 自动任务原有的未提交记录和证据不纳入本批提交；本文只保留其独立验收边界，原始观察材料由原任务单独收口。独立构建机上本任务的 34 个临时容器、匿名数据库卷和临时镜像已清理，其他服务保持。
