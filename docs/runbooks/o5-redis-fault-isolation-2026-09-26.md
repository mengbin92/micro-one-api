# O5 Redis 故障传播与降级延迟（隔离验收）

> 初次验收：2026-09-26；采样修复后复测：2026-09-27
> 复测代码：`47e3ca1d`（`test/e2e/routing` O5a 相位 + 驱动脚本）
> 依据：桌面待办第 3 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 3 节 O5
> 证据：[复测数据](evidence/o5-redis-fault-isolated-retest-2026-09-27.json)；[旧样本及纠错记录](evidence/o5-redis-fault-isolated-2026-09-26.json)

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

## 剩余边界

1. **identity 侧 Redis 依赖未归因**：复测确认 GetAuthSnapshot P95 在故障
   窗口上升，但尚未定位 identity 内部的具体回退路径。
2. **熔断拒绝是指标盲区**：ResilientClient 的熔断拒绝在 conn 层拦截器
   之上返回，不产生 dependency_grpc_latency 样本；故障期的拒绝延迟不可见。
3. **生产复采仍待代表性流量**：本次为隔离环境证据，不改变
   `o5-production-rpc-baseline-2026-09-26.json` 的结论（低流量样本不支持
   启动缓存优化）；生产 Redis 故障仍不应在受限主机上注入。
4. CheckRoutingSettlement / HasRoutingCandidates 在本 fixture 窗口零样本，
   属该负载下的调用模式（wallet 组不经结算检查），非埋点缺失。

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
