# O5 Redis 故障传播与降级延迟（隔离验收）

> 日期：2026-09-26
> 代码：develop 本分支提交（`test/e2e/routing` 新增 O5a 相位 + 驱动脚本）
> 依据：桌面待办第 3 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 3 节 O5
> 证据：[o5-redis-fault-isolated-2026-09-26.json](evidence/o5-redis-fault-isolated-2026-09-26.json)

## 结论先行

在隔离 Compose 栈（真实服务二进制 + mock upstream + Prometheus）中停止
Redis 并驱动真实 chat 流量。以下可用性结论由 chat/结算断言支持；
RPC 样本量和延迟数字须按文末的复核说明重测：

- **legacy 缓存路径：Redis 断开后 12 连 chat 全部 200 且结算成功**。
  旧样本显示零回源，但抓取时序缺陷使该数字不能证明 L1 吸收了全部查找。
  L1 过期后的 loader 回源正确性由 `platform/cache` 时间推进单测覆盖。
- **V2 路径：Redis 断开期间 chat 全部 200 且结算成功**。旧 Prometheus
  样本显示非 OK 为零及毫秒级 P95，但故障和恢复窗口的样本可能未完整抓取，
  不能据此确认全部 RPC 状态或恢复后的延迟。
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

## 关键数字（详见证据 JSON）

| 窗口 | GetAuthSnapshot P95 | GetRoutingGroup P95 | 非 OK 状态 |
| --- | --- | --- | --- |
| V2 旧基线（10m） | 4.06 ms | 4.53 ms | 0 |
| V2 故障（2m） | 8.90 ms | 4.98 ms | 0 |
| V2 恢复（2m） | 8.90 ms（窗口重叠） | 4.98 ms | 0 |
| legacy 故障 | 零回源样本（待复测） | — | 0 |

上表是 2026-09-26 原始记录，不是修复后的复测结果。原实现仅在 V2 窗口
等待过去 2 分钟的累计样本达到 10；先前流量即可满足条件。legacy 窗口没有
等待新抓取，健康基线也在停止 Redis 后读取。恢复窗口与故障窗口重叠，
因此“恢复后无持续退化”不能从旧表推出。现已将基线移至健康相位，并用
前后抓取限定每组样本；需重跑隔离 e2e 才能更新 P95 结论。

## 剩余边界

1. **identity 侧 Redis 依赖未归因**：旧样本中的 GetAuthSnapshot P95 上升
   可能来自 identity 的存储回退，须在修复采样后重新确认。
2. **熔断拒绝是指标盲区**：ResilientClient 的熔断拒绝在 conn 层拦截器
   之上返回，不产生 dependency_grpc_latency 样本；故障期的拒绝延迟不可见。
3. **生产复采仍待代表性流量**：本次为隔离环境证据，不改变
   `o5-production-rpc-baseline-2026-09-26.json` 的结论（低流量样本不支持
   启动缓存优化）；生产 Redis 故障仍不应在受限主机上注入。
4. CheckRoutingSettlement / HasRoutingCandidates 在本 fixture 窗口零样本，
   属该负载下的调用模式（wallet 组不经结算检查），非埋点缺失。

## 执行记录

- 2026-09-26 原实现 `python3 scripts/test-routing-e2e.py`：PASS all phases（含新增
  legacy-redis-down / legacy-redis-recovered）；修复后的完整隔离 e2e 尚未复跑。
- 实现过程中修复三个测量缺陷：`histogram_quantile` 需保留 `le` 标签、
  故障窗口 token 选择、burst 后须等待 Prometheus 抓取再采样（否则
  increase() 读到抓取前样本恒为 0）。最后一项原实现的等待条件仍会被旧
  样本满足，已在本次 review 修正。
