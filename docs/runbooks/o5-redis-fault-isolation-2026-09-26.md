# O5 Redis 故障传播与降级延迟（隔离验收）

> 初次验收：2026-09-26；采样修复后复测：2026-09-27
> 复测代码：`47e3ca1d`（`test/e2e/routing` O5a 相位 + 驱动脚本）
> 依据：桌面待办第 3 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 3 节 O5
> 证据：[复测数据](evidence/o5-redis-fault-isolated-retest-2026-09-27.json)；[旧样本及纠错记录](evidence/o5-redis-fault-isolated-2026-09-26.json)
> 归因复测：2026-09-29，代码 `09ff4aa0`，证据 [o5-attribution-2026-09-29.json](evidence/o5-attribution-2026-09-29.json)——
> 该次服务端/进程采样在 review 中发现窗口缺陷；原始数字保留，归因结论已收窄，见下节。
> 修正窗口后的完整复测：2026-09-29，见 [新证据](evidence/o5-attribution-retest-2026-09-29.json)。

## 结论先行

在隔离 Compose 栈（真实服务二进制 + mock upstream + Prometheus）中停止
Redis 并驱动 chat 流量。2026-09-27 用修复后的抓取时序重跑完整验收，
各故障及恢复相位均通过：

- **legacy 缓存路径：Redis 断开后 12 连 chat 全部 200 且结算成功**。
  新样本在故障突发中观测到 GetAuthSnapshot 回源（Prometheus `increase`
  外推约 1.13）；健康和恢复突发均无样本，因此不能比较三个窗口的 P95。
  L1 过期后的 loader 回源正确性另由 `platform/cache` 时间推进单测覆盖。
- **V2 路径：Redis 断开期间 chat 全部 200 且结算成功**。旧 Prometheus
  样本已被修复后复测取代。GetAuthSnapshot P95 从健康的 4.60ms 升至
  故障的 9.14ms，恢复窗口为 4.95ms；GetRoutingCapabilities 为
  0.95ms → 2.60ms → 0.95ms。两次 Redis 状态切换后的窗口各自重新抓取，
  未与前一突发共用样本。
- **既有故障语义不受影响**：撤权传播暂停、outbox 积压告警
  firing/cleared、恢复后撤权生效（原 `redis-down`/`redis-recovered`
  断言全绿）。

## 测量方法

- 在 `test-routing-e2e.py` 的相位机中新增 legacy 故障窗口（legacy 相位后
  直接 stop redis），并扩展既有 `redis-down`/`redis-recovered` 相位。
- 每个窗口在流量前后各等待一次 Prometheus 抓取；用 12 连 chat（唯一请求 ID）
  驱动 relay 热路径，然后固定查询终点和区间，只对两次抓取之间的样本以
  `increase()` + `histogram_quantile(0.95)` 读取
  `micro_one_api_dependency_grpc_latency_seconds` 的 per-method 样本量与 P95。
- V2 故障窗口使用 Legacy token（`fixed()` 相位已验证其在 V2 下的兼容性）；
  Ordered token 不可用，因为该相位开头的撤权断言会撤掉其依赖的组权限
  ——第一版 O5a 用 Ordered 时复现了这一点（500 access_denied），属测试
  设计错误而非产品缺陷。
- 样本量为 Prometheus 子抓取窗口上的外推值，非整数请求数。

## 复测数字（详见复测证据 JSON）

| 窗口 | GetAuthSnapshot P95 | GetRoutingGroup P95 | GetRoutingCapabilities P95 |
| --- | --- | --- | --- |
| V2 健康 | 4.60 ms | 3.80 ms | 0.95 ms |
| V2 故障 | 9.14 ms | 4.97 ms | 2.60 ms |
| V2 恢复 | 4.95 ms | 4.92 ms | 0.95 ms |
| legacy 故障 | 9.75 ms（约 1.13 个外推样本） | 无样本 | 无样本 |

样本量由 Prometheus `increase()` 外推，并非整数 RPC 次数；P95 只包含
`status="OK"`。本次 V2 三个窗口的 GetAuthSnapshot / GetRoutingGroup
外推样本量分别约为 25.60 / 25.60、25.60 / 25.60、27.20 / 27.20；
未观测到这些方法的非 OK 样本。旧实现的 2 分钟回看可被前一相位的流量
满足，且健康、故障、恢复窗口发生重叠；旧数字仅留在旧证据文件供追溯。

## 2026-09-29 归因

针对复测遗留的延迟问题，原运行在 O5a 窗口新增服务端处理 P95
（`xgrpc.MetricsUnaryServerInterceptor`，1ms/2.5ms 桶）、进程 CPU 速率
与 goroutine 峰值，记录为八相位 PASS。review 发现：只等待 relay 抓取，
再向前扩 15 秒，不能保证其他服务已抓到突发结束后的样本，还可能混入
相位内准备流量。原数字只保留为历史记录，不能据此关闭延迟归因。

- **原始观测**：identity 服务端 P95 1.68 → 4.67 → 1.60ms；channel
  只有故障/恢复 2.49/0.98ms，基线缺失；billing 故障服务端样本缺失，
  客户端各窗口 0.95ms 不能替代服务端值。恢复值不能填作 channel 基线。
- **DB 次数纠错**：V2 的 legacy-token 路径中，GetAuthSnapshot 为
  FindTokenByKey、FindUserByID 各一次查询，加 GetRoutingFacts 事务内
  user/token/grants 三次查询，共五次 SELECT；GetRoutingGroup 事务内
  group/channels/accounts/model grants 共四次 SELECT，另有事务开销及
  可能的 schema 探测。原文把 repo 调用数当查询数，“约 3/1 次”及
  “延迟与 DB 工作量成正比”的论据均不成立。
- **进程指标边界**：原窗口的 CPU 均值与采样 goroutine 峰值未显示持续
  增压，不能排除短暂重试、重连或调度等待；这些窗口也需修正后重采。
- **已确认的代码事实**：GetAuthSnapshot 同步调用路径没有 Redis 操作，
  不存在该路径内的 Redis 回退；后台 Redis 工作仍可能争用共享资源。

共享 MySQL / Docker VM 争用目前是**待验证假设**。fixture 未采集 DB
耗时或宿主资源等待，也未做可分离该因素的对照。修正采样后重跑，结合
DB/宿主观测，才可确认原因；单机共享拓扑不能外推生产。

本次 review（修正采样代码 `49c9abb9`）已把采样改为等待 relay/identity/channel/billing 各自完成
突发前后的成功抓取，每个目标按自己的时间边界查询并保存边界；移除
固定 15 秒扩窗。查询错误、进程样本缺失，以及客户端测到方法但服务端
无对应样本都会失败。服务端直方图包含所有状态/调用方，客户端 P95
仅含 relay 的 OK 样本，复测仍需核对非 OK 与其他调用方流量。
本次完成聚焦回归，未重建镜像或重跑 Compose，不能把旧八相位 PASS
当成新采样器的完整验收。

### 修正窗口后的完整复测（2026-09-29）

使用 `develop@172bbff9` 重新构建隔离镜像并运行
`python3 scripts/test-routing-e2e.py`。八个阶段均通过，测试项目的容器、
网络和卷已清理。每轮突发前后均等到 relay、identity、channel、billing
各自成功抓取，再按目标自己的抓取边界计算；客户端测到的方法均有服务端
样本。原来缺失的 channel 健康基线和 billing 故障样本已补齐。

| V2 窗口 | GetAuthSnapshot 客户端/服务端 P95 | GetRoutingGroup 客户端/服务端 P95 | GetRoutingCapabilities 客户端/服务端 P95 |
| --- | --- | --- | --- |
| 健康 | 3.8 / 2.2 ms | 2.6 / 1.9 ms | 0.95 / 0.95 ms |
| Redis 故障 | 8.0 / 4.7 ms | 4.8 / 3.5 ms | 0.95 / 0.95 ms |
| 恢复 | 3.8 / 2.2 ms | 2.6 / 1.6 ms | 0.95 / 0.95 ms |

三轮各有约 24 个 OK 的鉴权和选路 RPC、约 12 个 OK 的额度能力 RPC；
这是 Prometheus `increase()` 外推值。identity 与 channel 服务端处理时间随
故障上升并在恢复后回落，billing 对应方法保持约 0.95 ms。进程 CPU 速率
和 goroutine 最大值见[证据](evidence/o5-attribution-retest-2026-09-29.json)，
这些样本没有测量 MySQL 查询耗时或 Docker VM 资源等待，不能确认共享
MySQL/VM 争用是根因。legacy 的健康和恢复窗口均无 GetAuthSnapshot RPC，
故障窗口约一个外推样本，仍不能做三段延迟比较。生产流量复采暂缓决定不变。

## 剩余边界

1. **延迟上升仍待根因归属**：修正窗口后的完整复测确认 identity/channel
   服务端延迟在隔离 Redis 故障时上升；同步 RPC 内 Redis 回退假设已由
   代码路径排除。共享 DB/VM 影响仍未证实，需要 DB/宿主观测或可分离
   因素的对照，不能把本机 P95 当成生产预算。
2. **熔断拒绝计数已修复，延迟仍无样本**：open/half-open 拒绝按
   `result="rejected"` 独立计数，不再增加 `CircuitBreakerFailures`。
   本地计数回归通过；它不产生 dependency_grpc_latency 样本，不能视为
   拒绝延迟盲区已关闭。2026-09-30 已补生产镜像及指标接线核对，见下节；
   当前无 `rejected` 序列，未人为触发生产熔断，拒绝行为仍以本地回归为证。
3. **生产复采仍待代表性流量**：不改变低流量基线不支持启动缓存优化的
   结论，也不在受限生产主机注入 Redis 故障。若发生自然故障，可事后
   核对故障区间、请求成功/降级、账务结算、告警与通知送达、恢复后权限
   和积压清理；证据齐全且符合既有语义才验收，缺项继续保留，发生故障
   本身不等于通过。
4. CheckRoutingSettlement / HasRoutingCandidates 在本 fixture 窗口零样本，
   属该负载下的调用模式（wallet 组不经结算检查），非埋点缺失。

## v0.33.5 生产部署核对（2026-09-30）

本轮只读核对已运行的 identity、channel、billing、Relay；未构建、重启、
执行模型请求或注入故障。四个镜像 ID 与本机保留的
`/tmp/deploy-v0.33.5.log`（2026-09-29 部署）完全一致，容器于
2026-09-29 02:01–02:05 UTC 启动，均为 linux/amd64、restart_count=0，
`/healthz` 均返回 `status=ok`。旧镜像的
`rollback-20260929-095945` 标签仍可解析。

三项依赖方法 GetAuthSnapshot / GetRoutingGroup / GetRoutingCapabilities
均有服务端延迟样本，1ms/2.5ms 桶存在；独立的
`micro_one_api_grpc_requests_total{status}` 可查询 OK/非 OK 计数。
延迟直方图自身不含 status 标签，不能把其 P95 描述为仅 OK 请求。
Prometheus 的九个业务抓取目标全部 up=1。Relay 的熔断请求计数可查询，
但没有 `result="rejected"` 样本；不通过生产故障注入补齐它。

脱敏原始结果见 [生产核对证据](evidence/v0.33.5-production-check-2026-09-30.json)。
镜像没有嵌入 VCS revision，因此版本对应以部署日志、镜像 ID 和已出现的
新增指标交叉核对，不宣称从二进制读到了 tag。此项关闭生产部署/埋点接线
证据缺口；延迟根因、拒绝耗时与代表性业务流量验收仍保留。

## 执行记录

- 2026-09-26 原实现 `python3 scripts/test-routing-e2e.py`：PASS all phases（含新增
  legacy-redis-down / legacy-redis-recovered）；旧 RPC 采样不可用于延迟结论。
- 实现过程中修复三个测量缺陷：`histogram_quantile` 需保留 `le` 标签、
  故障窗口 token 选择、burst 后须等待 Prometheus 抓取再采样（否则
  increase() 读到抓取前样本恒为 0）。最后一项原实现的等待条件仍会被旧
  样本满足，已在 review 中修正。
- 2026-09-27 `python3 scripts/test-routing-e2e.py`：重建当前代码隔离镜像、
  Prometheus 规则测试和全部八个相位通过；私有容器、网络与卷已清理。
  复测样本来自同次运行的 `acceptance.log` 中六个 `o5a[...]` 记录。
