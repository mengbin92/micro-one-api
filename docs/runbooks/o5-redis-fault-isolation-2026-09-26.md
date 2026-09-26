# O5 Redis 故障传播与降级延迟（隔离验收）

> 日期：2026-09-26
> 代码：develop 本分支提交（`test/e2e/routing` 新增 O5a 相位 + 驱动脚本）
> 依据：桌面待办第 3 项 / `docs/design/next-stage-plan-2026-09-22.md` 第 3 节 O5
> 证据：[o5-redis-fault-isolated-2026-09-26.json](evidence/o5-redis-fault-isolated-2026-09-26.json)

## 结论先行

在隔离 Compose 栈（真实服务二进制 + mock upstream + Prometheus）中停止
Redis 并驱动真实 chat 流量：

- **legacy 缓存路径：Redis 断开对用户完全透明**。12 连 chat 全部 200 且
  结算成功，L1（30s TTL）吸收了全部鉴权查找，零回源样本——没有回源风暴。
  L1 过期后的 loader 回源正确性由 `platform/cache` 时间推进单测覆盖。
- **V2 每请求回源路径：Redis 断开期间全部回源 RPC 成功（零非 OK 状态）**，
  chat 全部 200 且结算成功。延迟有界上升：GetAuthSnapshot P95
  4.06 → 8.9ms（约 +4.8ms），GetRoutingGroup 4.53 → 4.98ms，
  GetRoutingCapabilities 0.95 → 2.8ms。恢复后无持续性退化。
- **既有故障语义不受影响**：撤权传播暂停、outbox 积压告警
  firing/cleared、恢复后撤权生效（原 `redis-down`/`redis-recovered`
  断言全绿）。

## 测量方法

- 在 `test-routing-e2e.py` 的相位机中新增 legacy 故障窗口（legacy 相位后
  直接 stop redis），并扩展既有 `redis-down`/`redis-recovered` 相位。
- 每个故障窗口用 12 连 chat（唯一请求 ID）驱动 relay 热路径，随后经
  `waitDependencyRPCScraped` 等待 Prometheus 15s 抓取落地，再以
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
| V2 基线（10m） | 4.06 ms | 4.53 ms | 0 |
| V2 故障（2m） | 8.90 ms | 4.98 ms | 0 |
| V2 恢复（2m） | 8.90 ms（窗口重叠） | 4.98 ms | 0 |
| legacy 故障 | 零回源样本（L1 吸收） | — | 0 |

## 剩余边界

1. **identity 侧 Redis 依赖未归因**：GetAuthSnapshot P95 翻倍推测来自
   identity 自身在 Redis 断开后的存储回退，本次只观察到"有界上升、零错误"。
2. **熔断拒绝是指标盲区**：ResilientClient 的熔断拒绝在 conn 层拦截器
   之上返回，不产生 dependency_grpc_latency 样本；故障期的拒绝延迟不可见。
3. **生产复采仍待代表性流量**：本次为隔离环境证据，不改变
   `o5-production-rpc-baseline-2026-09-26.json` 的结论（低流量样本不支持
   启动缓存优化）；生产 Redis 故障仍不应在受限主机上注入。
4. CheckRoutingSettlement / HasRoutingCandidates 在本 fixture 窗口零样本，
   属该负载下的调用模式（wallet 组不经结算检查），非埋点缺失。

## 执行记录

- `python3 scripts/test-routing-e2e.py`：PASS all phases（含新增
  legacy-redis-down / legacy-redis-recovered）。
- 实现过程中修复三个测量缺陷：`histogram_quantile` 需保留 `le` 标签、
  故障窗口 token 选择、burst 后须等待 Prometheus 抓取再采样（否则
  increase() 读到抓取前样本恒为 0）。修复已并入相位代码。
