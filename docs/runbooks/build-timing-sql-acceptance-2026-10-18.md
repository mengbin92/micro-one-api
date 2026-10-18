# 原生镜像构建提速与计时 SQL 链路验收

日期：2026-10-18（Asia/Shanghai）。承接[当前计划](../design/next-stage-plan-2026-10-18.md)，实现见 [PR #24](https://github.com/mengbin92/micro-one-api/pull/24)。本轮不修改生产实例、配置、保留期、收费或 executor 灰度，既有 48 小时观察窗口保持至 2026-10-20 10:10:20 CST。

**结论：构建提速已验证；三库 8 并发链路门禁 PASS，64 并发容量门禁 FAIL。不能据此扩大生产采集负载。**

## 构建提速

原 CI 在 amd64 runner 上通过 QEMU 编译 arm64，每次九服务、两架构共 18 个任务，且没有远程层缓存。上轮 Relay 的生成工具安装约 1203 秒，服务编译约 1163 秒。现在 CI 与 Release 按服务/架构分开构建，arm64 使用 `ubuntu-24.04-arm`，amd64 保持 `ubuntu-latest`，不更改 Dockerfile 的 CGO/SQLite 构建语义。

- CI 与 Release 各自按服务和架构分配 GHA 缓存 scope，避免并行写同一索引；缓存导出失败不覆盖镜像构建结果。
- `.dockerignore` 排除文档、根 Markdown 和普通 Go 测试；精确保留 Routing 验收镜像所需的 10 份测试源码，避免这类编辑使镜像源码层失效。运行时配置、迁移、proto 和前端源码仍在构建上下文内。
- Release 保留原有完整 E2E 门禁。18 个原生构建先按 digest 推送，再由九个任务合并清单；修改标签前 dry-run 检查必须恰好覆盖 linux/amd64 与 linux/arm64，发布后再次核验，最后才创建 GitHub Release。稳定版与预发布版的既有标签规则保持。
- 实际工作流 shell 在临时本地 registry 上执行：正常双架构通过；少一个 digest、非法 digest、两个不同 amd64 digest 均失败，且未生成对应标签。没有推送任何测试 Docker Hub 标签。

[第一轮 CI](https://github.com/mengbin92/micro-one-api/actions/runs/39123428068)全体 18 个 Docker 构建成功。下列是两个实际 CI 运行的 job 耗时，含准备与缓存导出、不含排队；不是相同硬件上的配对微基准，也不保证每次缓存命中。

| arm64 服务 | 原 QEMU | 原生冷构建 | 缩短 |
| --- | ---: | ---: | ---: |
| Relay | 40m56s | 5m45s | 86.0% |
| Admin | 40m55s | 6m20s | 84.5% |
| Log | 39m35s | 5m14s | 86.8% |
| Billing | 22m08s | 5m12s | 76.5% |
| Identity | 41m15s | 4m59s | 87.9% |
| Monitor | 40m42s | 5m26s | 86.7% |
| Config | 35m37s | 4m56s | 86.1% |
| Channel | 40m24s | 4m13s | 89.6% |
| Notify | 35m39s | 5m18s | 85.1% |

原生日志独立确认 `OS/Arch: linux/arm64`、`Architecture: aarch64`。例如 Log 工具安装 75.6 秒、Go 编译 67.9 秒、缓存准备/导出 129.4 秒；GHA 层缓存不等于跨 runner 持久保存 Go 的 cache mount。缓存可能因仓库容量预算被淘汰，提速不能只依赖暖缓存。

参考：[GitHub runner 规格](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)、[Docker GHA cache scope](https://docs.docker.com/build/cache/backends/gha/)、[多 runner 多平台构建](https://docs.docker.com/build/ci/github-actions/multi-platform/)。

## 真实 Log RPC / SQL 性能

复用原受控实验，保留四组成对交替、每模式 30 秒、每模式预热 8 次与 50ms 假 SSE 上游。新增独立 opt-in SQL 模式：真实 loopback HTTP → Relay 采集包装 → 原有 256 队列/2 worker → 带独立服务身份的真实 Log gRPC → 生产 service/usecase/repository → 完整迁移后的 SQL。每 500ms 通过 Admin 身份调用一次实际摘要查询 RPC；核对摘要、去重 claim、attempt ref 的数据库条数分别等于导出成功数，并核对完成/接收 OTLP span 数。

使用独立原生 Linux/amd64 构建机，测量进程 4 CPU / 1 GiB、GOMAXPROCS=4、网络 none；MySQL 8.0 为 1 CPU / 1 GiB，PostgreSQL 18 为 1 CPU / 512 MiB，数据库仅监听隔离共享网络命名空间的 loopback，不发布端口。SQLite 是临时文件、WAL、synchronous=FULL、单连接。编译与正式测量分开，各数据库实验顺序执行。

这是采集到摘要存储的完整链路，不含真实供应商、业务鉴权/选路/收费全过程或 Jaeger 持久化。查询使用生产传输和 owner 路径，授权 resolver 为 legacy；真实 IAM 权限矩阵沿用既有集成验收。CPU/分配包含同进程内的驱动、假上游、Log 服务和 OTLP 接收器；数据库容器资源采样是时点值，不是峰值或全程平均。

### 64 并发：容量门禁 FAIL

| 数据库 | 开启侧摘要数，含预热 | 落库 / 丢弃 | 丢弃率 | 四组 HTTP P95 增量 |
| --- | ---: | ---: | ---: | --- |
| SQLite | 146565 | 34568 / 111997 | 76.42% | +5.72%、+4.34%、+4.29%、+5.47% |
| MySQL | 148472 | 23821 / 124651 | 83.96% | +0.07%、−0.14%、−0.86%、+1.25% |
| PostgreSQL | 147683 | 52892 / 94791 | 64.19% | +5.13%、+0.99%、+3.47%、+0.41% |

三库的 HTTP 与查询失败均为 0，已接受摘要的 RPC 失败为 0，三张表计数一致，OTLP 接收数等于完成数；队列满造成的摘要丢弃全部计数。SQLite 的落库确认 P95 约 988–1023ms，MySQL 约 1441–1532ms。HTTP 成功且 P95 接近基线不能替代观测完整性准入。所有失败组均保留，没有降低 64 并发门槛或重复挑选通过结果。

### 低负载容量点

另以明确标注的 8 并发运行同一四组实验；它是单独容量点，不替换上面的失败。

| 数据库 | 全部落库，含预热 | 摘要丢弃/失败 | 四组 HTTP P95 增量 |
| --- | ---: | --- | --- |
| sqlite | 18607 | 0 / 0 | +1.83%、+1.31%、+2.61%、+2.25% |
| mysql | 18499 | 0 / 0 | +3.46%、+2.60%、+3.63%、+4.00% |
| postgres | 18523 | 0 / 0 | +3.17%、+3.25%、+2.88%、+2.93% |

三库该档位均 PASS，HTTP/查询失败为 0，三表计数和 OTLP 完成/接收数一致。实测流量约 154–156 请求/秒；8 并发与 64 并发是两个离散测点，不是精确吞吐上限，更不能直接作为生产容量承诺。CPU、分配、落库确认、查询和排空耗时保留在各原始 JSON。

## 失败、背压和恢复

- SQLite/MySQL/PostgreSQL 在关联写入阶段注入存储错误，验证实际事务回滚：摘要、去重 claim、关联 ref 均无残留。恢复后新摘要可写入并可经实际 RPC 查询，重复提交不增加三表条数。
- 在存储边界阻塞写入，401 次提交中恰好接受 258 条（2 个 worker + 256 队列）、丢弃 143 条；队列饱和时额外 HTTP 请求仍返回 `200/ok`，对应丢弃计数增至 144。解除阻塞后 258 条全部落库、failed=0。
- 人为丢失一次成功 RPC 确认，原 exporter 重试后仍只有一份摘要、claim 和 ref。
- 这是隔离存储边界故障注入，不冒充生产数据库故障或供应商真实失败。恢复验证针对之后可接受的摘要；已经因队列满丢弃的观测不会自动补回。

## CI 门禁补修

第二轮 18 个镜像构建全部成功，Config arm64 的工具层实际命中 `CACHED`、缓存导出 9.1 秒；部分任务重建，未将它们统称暖缓存。该轮 Backend race 暴露旧 gRPC span 测试直接在客户端返回后读取服务端 End 的时序假设。普通 Mac race 500 次未复现；用可控服务器 End 延后后，原断言 20/20 失败。修正测试为等待实际 End，再执行原有唯一性、trace ID 与父子关系断言；Mac 与原生 Linux/amd64 各 500 次 race、Linux 整包 20 次 race 通过，生产追踪代码未修改。

同轮 Routing 两库验收镜像因本轮新增的全局测试排除规则缺少测试源码，`go test -c` 报 `no Go files`。已为该专用测试入口补精确例外，并用实际 BuildKit COPY 导出验证全部 10 份验收源码、生产 Go/config/proto/迁移/前端输入保留，服务测试、docs 和环境文件仍被排除。保留失败原因和修复证据，不把这两次门禁失败计为通过。

## 复跑与证据

```bash
# 常规快速回归；外部数据库由现有 helper 限制为 loopback scratch 实例。
go test -race ./internal/server -run 'TestRequestTimingSQL(FailureRecovery|BackpressureAndLostAck)$' -count=20

# 默认 64 并发；高负载不足时预期非零退出，并保留完整 JSON。
TIMING_SQL_ACCEPTANCE=1 TIMING_SQL_DRIVER=sqlite TIMING_PERF_OUTPUT=/tmp/sqlite-64.json \
  go test ./internal/server -run '^TestRequestTimingSQLOverhead$' -count=1 -timeout=7m -v

# 单独的低负载容量点；同一四组成对、每模式 30 秒。
TIMING_SQL_ACCEPTANCE=1 TIMING_SQL_DRIVER=sqlite TIMING_SQL_CONCURRENCY=8 \
  TIMING_PERF_OUTPUT=/tmp/sqlite-8.json \
  go test ./internal/server -run '^TestRequestTimingSQLOverhead$' -count=1 -timeout=7m -v
```

MySQL/PostgreSQL 使用现有 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN`，必须指向可创建/删除 scratch database/schema 的本机一次性数据库，不能填写部署 DSN。没有 DSN 的快速回归会明确 skip，不能计作对应实库通过。本轮三库原生执行日志已单独保存脱敏结果。

[证据目录](evidence/build-timing-sql-2026-10-18/)。保留两次中断预检：初始客户端连接池过小导致本机连接频繁重建，修正后才做原生测量；另一原生探针因与迁移检查重叠而主动停止，不列入正式结果。64 并发 SQLite/MySQL 保留原始二进制/源码摘要，后来只增加测试用的 1–64 并发输入，默认仍为 64；PostgreSQL 与低档使用最终测试二进制。

常规 `make test-unit`（121 个有测试包）、相关九包测试、分层、格式、文档本地链接和 actionlint 通过；SQL 故障 SQLite race 20 次通过，三库功能回归在原生 Linux 单独实跑。临时 registry/builder/network 与原生测量、MySQL、PostgreSQL 容器已删除，其他本机容器保留。最终远端门禁状态见 PR 实时 checks；旧失败运行保留，不用本地通过冒充远端通过。
