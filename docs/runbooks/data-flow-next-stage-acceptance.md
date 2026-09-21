# 数据流闭环下一阶段验收 Runbook

本 Runbook 对应 `docs/data-flow-closure-analysis.md` 第 7 章第五批之后的线上与隔离验收。代码已部署不等于故障恢复已证明；每项结果都必须记录执行窗口、镜像摘要、样本范围和证据文件。

## 执行边界

- 生产环境只执行只读检查、健康检查、管理接口回读和固定窗口聚合。
- Redis pending 重领、数据库提交失败、进程重启、通知平台拒绝、OAuth Store 失败、支付回调丢失和跨来源 failover 必须在隔离环境执行。
- 不在生产禁用渠道/订阅账号，不重放上游请求，不修改历史账务，不开启 canonical 灰度。
- `NO_SAMPLE`、`BLOCKED` 和 `UNKNOWN` 不能写成 PASS；SQL、指标或接口读取失败必须保留原始错误。

## 阶段 A：生产只读验收

1. 使用 [data-flow-readonly-check.sh](data-flow-readonly-check.sh) 检查对账历史、告警状态、通知行和 stale reservation。
2. 使用 [data-flow-coverage-check.py](data-flow-coverage-check.py) 固定 24 小时窗口检查 reservation、ledger、log、channel usage、订阅窗口和通知聚合。
3. 认证读取 F19/F20 管理接口，至少验证未认证 `401`、缺参数 `400`、空结果 `200`，以及一个真实根请求的 planned/outcome/attempt 关联。
4. 保存容器重启次数、健康状态、镜像摘要、迁移版本和采样窗口；结果写入 `docs/runbooks/evidence/data-flow-next-stage-<date>.json`。

生产只读通过的含义是“当前样本和查询链路可读”，不能替代下面的故障矩阵。

## 阶段 B：隔离故障矩阵

| 场景 | 触发方式 | 必须观察的结果 | 失败判定 |
| --- | --- | --- | --- |
| F7/F8 结算提交后进程退出 | 在持久 settlement task 受理后停止 billing | 任务重启后只提交一次，账号/会话/渠道派生结果可回读 | reservation 被释放但无可恢复任务，或重复扣费 |
| F9 有限 Key 回包丢失 | identity 成功扣减后丢弃响应并重试 | 同一 reservation 只产生一次扣减，失败可查询/补偿 | 重试再次扣减或欠记无持久状态 |
| F10 gRPC 提交失败 | 让 billing RPC 返回业务失败/Unavailable | 不重放上游；reservation、ledger、log、usage 终态一致 | service 忽略失败，或产生孤儿扣费 |
| F11 Redis pending | handler 首次失败且不 ACK，停止并重启 consumer | `XAUTOCLAIM` 接管，幂等处理后 ACK，失败计数可见 | 只有新事件才能触发处理，或重复副作用 |
| F12/F14 通知失败 | notify endpoint 返回业务拒绝、非 2xx、写状态失败 | 状态为 queued/retry/failed，关闭开关不产生 sent | 业务拒绝仍记录 sent，或租约丢失导致并发发送 |
| F15 模型统计 | 成功、最终失败、未注册模型、repo 错误各执行一次 | success/error 分母明确，存储错误不被吞掉，平均值按样本数计算 | 失败不计数、repo 错误伪装为空或重复统计 |
| F16 配置发布 | 写入新 revision，模拟消费者未刷新/发布失败 | 持久版本可读，实际应用版本边界明确 | 仅凭写成功宣称实例已应用 |
| F17 支付查单 | 丢回调、查单暂时失败、重复回调、发放失败 | pending 可见并重试，paid/closed 收敛，站内冲正与外部退款分开 | 依赖用户 GET 才收敛或旧订单归属当前订阅 |
| F18 OAuth Store | refresh 成功后让持久化连续失败并重启进程 | 退避/告警可见；不得继续使用旧 refresh token | 凭据只存在内存且重启后静默丢失 |
| F19/F20/F22 failover | 普通渠道/订阅账号各执行一次可控失败回退 | 根请求、attempt、来源和实际模型可回读；账本只归属实际服务来源 | 重试改写既有 attempt，或来源字段缺失 |

可控跨来源场景使用 [post-release-forced-failure-verification.md](post-release-forced-failure-verification.md) 及 `scripts/verify-forced-failure.sh`。脚本退出码为 `0` 才能记录该场景 PASS；前置条件不足记录 `BLOCKED` 并保留 preflight 输出。

## 证据格式

每个场景至少记录：`captured_at_utc`、环境、代码提交/镜像摘要、触发动作、根请求或 reservation 标识的脱敏值、状态转移、重复/幂等计数、查询接口响应摘要和最终结论。禁止记录 token、请求正文、上游响应正文和支付凭据。

完成阶段 B 后，回写主分析文档第 7 章“第六批实施状态”和阶段表；未完成的场景保持 `待验收`，不要用服务健康或空表推断恢复正确。
