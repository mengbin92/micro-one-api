# v0.36.0 观察期间：P1/P2 隔离验收

日期：2026-10-18（Asia/Shanghai）。承接[当前计划](../design/next-stage-plan-2026-10-18.md)。两项 P1 与 P2 均完成隔离验收；发现的两项修复仅在工作区，未提交、部署或发版。生产的 48 小时观察仍使用原实例和配置，固定截止 2026-10-20 10:10:20 CST。

> 发布更新（同日）：本文“未提交/发版”描述隔离验收时点。后续实现已提交为 `6b656cd8`，纳入 [v0.36.1](../releases/release-v0.36.1.md)；生产实例、配置和原观察窗口保持。

## 1. P1：原生 Linux/amd64 长时性能对照

复用并修改 [TestRequestTimingControlledOverhead](../../internal/server/request_timing_perf_test.go)，不修改请求采集生产逻辑：64 并发、每模式至少 30 秒、四组成对实验，顺序交替为关闭→开启、开启→关闭。保留 50ms 假上游、每模式 8 次预热与真实本地 OTLP HTTP 接收；接收端解析 protobuf，核对收到的 span 与 SDK 完成数。

在已有独立 x86_64 构建机上，以原生 Go 1.27.2/CGO 编译并运行。编译使用本机提供的源码、已生成的 proto 和依赖缓存，编译与测量容器均关闭外部网络；没有在生产构建镜像或施加负载。测量容器限额 4 CPU / 1 GiB，GOMAXPROCS=4，退出码 0、无 OOM，临时容器已删除。[运行环境](evidence/p1-p2-acceptance-2026-10-18/native-runner.json)

| 组 | 顺序 | 关闭 P95 / ms | 开启 P95 / ms | 增量 | 5% 门槛 |
| --- | --- | ---: | ---: | ---: | --- |
| 1 | 关闭→开启 | 52.2915 | 52.9344 | +1.2294% | PASS |
| 2 | 开启→关闭 | 52.4378 | 53.0237 | +1.1173% | PASS |
| 3 | 关闭→开启 | 52.4910 | 53.0621 | +1.0880% | PASS |
| 4 | 开启→关闭 | 52.3265 | 53.0178 | +1.3211% | PASS |

共测量 299,489 次请求，开启侧另含预热的摘要导出 149,428 条；HTTP 失败、摘要丢弃/失败均为 0。完成与实际接收 span 均为 1,234,804，OTLP 接收 221,685,632 B。[全部原始结果](evidence/p1-p2-acceptance-2026-10-18/timing-perf-linux-amd64.json)

CPU 与分配在排空之前取样，排空 CPU、分配和时长单独记录。P95 通过并不代表没有资源开销：进程 CPU 约从每请求 676µs 增至 1101µs，分配约从 51KB 增至 96KB。这里的 CPU 包含同一进程内的测试驱动、假上游与 OTLP 接收器；摘要 sink 为内存成功回调，未覆盖 Log RPC/SQL。此次是受控采集开销门槛，不是生产整体容量或真实供应商性能准入。

本轮只执行了这一次完整的四组原生测量，全部结果保留。早前 arm64 短测两次超过 5% 的[原始失败记录](post-v0.36.0-acceptance-2026-10-18.md#隔离高并发对照)继续保留，不改写为通过，也不将它与不同架构的新结果拼接。

在原生 Linux/amd64、Go 1.27.2 环境可复跑：

```bash
TIMING_PERF_ACCEPTANCE=1 TIMING_PERF_OUTPUT=/tmp/timing-perf.json \
  go test ./internal/server -run '^TestRequestTimingControlledOverhead$' -count=1 -timeout=6m -v
```

## 2. P1：WS 多轮真实 socket 与结算关联

新增 [WS 集成与历史上限回归](../../internal/server/request_timing_ws_e2e_test.go)。客户端→Relay→假上游均为真实 WebSocket，Billing 为真实本地 gRPC 传输和内存测试服务，使用生产 `xgrpc.ClientStatsHandler` 记录预留、提交与关联；没有把纯函数 mock 当作 RPC 计时证据。

WS 与 race 在本机 darwin/arm64 执行。测试先复现普通连接在第 33 轮断开：转发器始终安装 `durableRecord` 回调，该回调还用于契约反馈，未启用持久化时也存在；relay 却按回调是否存在启用 32 个历史 ID 的持久化硬限制。现在根据预留实际返回的 `ExecutionDeliveryVersion` 启用该限制，普通模式沿用有界历史淘汰；真实持久化模式仍保持原保护，不扩大收费能力。

通过 Go overlay 单独恢复旧判断后，现有回归再次产生 `client read turn 32` 和错误的 `durable_history_limit`；工作区始终保留修复。[红色反馈](evidence/p1-p2-acceptance-2026-10-18/ws-before-fix.json)

修复后：

- 默认 128 轮真实连接及两种持久化上限分支均通过 race 检查；相关三包整包 race 通过。
- opt-in 长跑 1200 轮、69.81s，同一连接在保持打开时逐轮立即导出；1200 条有效摘要、1200 次独立结算，预留与提交逐一对应，每轮输入/输出/缓存读取分别为 12/8/3，无累计重扣。
- 各轮 observation、OTel trace 与业务 root 独立，connection 相同；握手、首帧与拨号仅出现在第一轮。提交状态为 committed，首内容与输出终态均有证据。
- 每轮结束后待处理 turn 和 response 索引为空，已完成 ID 历史不超过 64；关闭后无重复摘要，session 清理完成，摘要失败与丢弃为 0。
- 128 轮与最终 GC 后堆占用分别为 10,569,976 B / 12,130,552 B。测试保留了 fake Billing 请求、断言 ID 和 sticky route，因此该差值不是生产内存曲线或无泄漏证明；有界性断言针对 timing session 的保留状态。

[长跑原始结果](evidence/p1-p2-acceptance-2026-10-18/ws-soak.json)。真实供应商/代理链路的生产 WS 长连接、Jaeger 与真实账单关联仍待自然授权流量；本轮不填补生产零样本。

```bash
go test -race ./internal/server ./domain/requesttrace ./platform/grpc/xgrpc -count=1
TIMING_WS_SOAK=1 TIMING_WS_OUTPUT=/tmp/ws-soak.json \
  go test -race ./internal/server -run '^TestWSTimingMultiTurnSocket$' -count=1 -v
```

## 3. P2：Lite 首请求与空卷恢复

首次完整构建已暴露并复现真实首请求 503：默认指南和 Lite smoke 只生成共享 `SERVICE_TOKEN`，但 `ClaimUpstreamExecution` 要求已验证的独立 Relay 身份；直接调用得到 PermissionDenied。共享令牌不能授予该系统能力，与 IAM 是否切换无关。

只在同一隔离项目补充服务身份、接收方信任表并重建容器后，同镜像、数据库和渠道恢复 HTTP 200，内容与 usage 正确。[对照证据](evidence/p1-p2-acceptance-2026-10-18/lite-identity-regression.json)

修复复用已有 `prepare-rbac-service-identities.py` 和 `check-service-identities.py`，按现有 fixed RPC 权限生成九个独立 outbound token 与最小接收方信任表。同步中英文 Quickstart、Lite 示例注释与 smoke；没有放宽 `ClaimUpstreamExecution` 或修改 IAM 模式。快速回归在任何构建之前验证实际 Compose 配置中接收方信任独立 Relay，缺失时失败。

扩展现有 [Lite smoke](../../scripts/test-lite-smoke.py)：停机备份 SQLite、Redis 和 `.env`，第二个随机命名项目从空卷恢复，复用同次构建镜像；核对原登录、钱包余额/已用/冻结/请求数、Redis marker、原 Token，以及再次聊天证明渠道凭证可解密。用户资料与钱包按当前接口分别读取。

最终全流程退出码 0：构建/迁移/登录/前端、渠道/钱包/Token/首请求、重复迁移与重启持久化、SQLite/Redis 停机备份，以及第二项目空卷恢复全部 PASS。钱包按四项实际字段逐一相等；Redis marker、私有初始密码、原 Token 与恢复后再次聊天全部通过。[脱敏结果与清理核对](evidence/p1-p2-acceptance-2026-10-18/lite-smoke.json)

过程中保留了两类失败：Alpine 软件包 CDN 的临时 TLS 故障重试后恢复；新增恢复断言曾误用用户资料接口不提供的旧 `quota` 字段，已改从现有钱包接口读余额/已用/冻结/请求数并重跑完整流程。没有为了测试通过更改产品 API 或放宽检查。

本轮五个临时 Lite 项目的容器、卷、网络和镜像均已清理，独立原生编译/测量容器也已删除。原始构建日志、`.env` 和备份仅保留在本机私密临时目录，不加入证据目录或 Git。只使用本地假上游，不产生供应商费用。

```bash
python3 scripts/test-e2e-flow-identities.py
python3 scripts/test-lite-smoke.py
```

## 4. 交付边界

源码、指南和测试修改均未提交、推送、打 tag 或部署。生产模型请求、存储/保留期、收费配置和灰度范围未改变；小时观察任务继续按原窗口采样，executor 七天任务继续暂停。存储稳态、生产 WS、供应商账期与 executor 同窗门禁仍各自待证。

[校验与源码指纹](evidence/p1-p2-acceptance-2026-10-18/checks.json) · [证据索引](evidence/p1-p2-acceptance-2026-10-18/index.json)。原生测量所用六份 Go/模块文件已与 `6b656cd8` 验收时点逐一核对 SHA-256；后续发布门禁的 WS 发送边界补修见下节，原始测量不追改。

## 5. 发布门禁补修：快响应终态时序

develop 的 Linux Backend 单测检出 `TestWSTimingMultiTurnSocket`：预留已关联、结算 committed、输出已完成，但 summary outcome 为 rejected。不是修改断言：另加实际 relay 泵的可控写入测试，让第二轮响应先完成、再放行该轮 `WriteFrame` 返回；原实现连续 20 次均失败，稳定复现 CI 症状。

根因是发送状态只在 `WriteFrame` 返回后建立。普通同步测试与带 50ms 假上游的长跑没有触发这个调度顺序。修复把状态转换放在所有准入/契约/持久化检查之后、实际 I/O 之前；写失败仍写入失败状态，未通过准入的请求不进入发送状态。金额、账本和权限判断不变。

本机 darwin/arm64 与原生 Linux/amd64 的同组 100 次 race 重复检查均通过，保留普通多轮 socket、可控早响应、真实持久化历史保护三类检查；完整 `make verify` 已重新通过。原生编译/验证在关闭外部网络的独立 4 CPU / 4 GiB 容器完成，无 OOM，临时容器已清理。[失败与通过证据](evidence/ws-send-boundary-2026-10-18/index.json)

本补修只改 WS 路径，§1 的受控 HTTP 性能数据保留其原始源码指纹，不冒充补修后的新一轮性能测量。生产实例和观察窗口保持原值。发布前还为既有 CRLF 的 RBAC CSV 声明限定的 `cr-at-eol` 属性，保持原文件换行并继续检查其他空白错误；清单仅刷新已评审的源摘要，未改授权分类。
