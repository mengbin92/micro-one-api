# 数据流闭环下一阶段验收 Runbook

本 Runbook 对应 `docs/data-flow-closure-analysis.md` 第 7 章第五批之后的线上与隔离验收。代码已部署不等于故障恢复已证明；每项结果都必须记录执行窗口、镜像摘要、样本范围和证据文件。

2026-09-22 经用户明确授权，已直接在生产完成 model 143 的双供应商配置核对、短期有限额度测试令牌、审计回读和 monitor 依赖准备，见[生产前置准备记录](data-flow-production-preflight-2026-09-22.md)。两供应商服务同一模型，分别使用上游 `Kimi-K3` 与 `k3`；来源和计价身份仍独立。该授权及结果限于前置准备，不代表以下故障矩阵通过；monitor 主动探测因上游模型列表返回 HTML 暂停。

同日后续代码修复已在本地通过 Chat Completions 双向失败回退测试，覆盖旧入口 hybrid 路径及 adaptor orchestrator 的流式/非流式请求、凭据解析、最终来源计费和选路审计。修复（`31250763`）已于 2026-09-22 03:22 UTC 部署生产 relay-gateway；此前生产证据中的来源锁结论仍对应采样时版本；线上故障矩阵保持待执行。可复现命令：`go test ./internal/server -run '^TestHTTPChatCrossSource' -count=1`。普通 provider-only 入口仍保留来源限制；流式回退仅发生在开始向客户端输出之前，结算失败不得重放上游。

2026-09-22 晚些时候在本地隔离 compose 栈（MySQL + Redis + 双 mock 上游，无生产数据）完成 F19/F20/F22 非流式跨来源故障矩阵，证据见 [data-flow-failover-isolated-2026-09-22.json](evidence/data-flow-failover-isolated-2026-09-22.json)。上游 500、连接拒绝两个方向四个场景全部通过：planned/outcome、attempt 归属、账本单一归属与 dedupe 零重复均符合。矩阵发现并修复一个真实缺陷：渠道 hostname 无法解析（上游容器/主机消失）时，provider 构建阶段的 DNS 错误不匹配任何可重试模式，请求直接 502 不回退，且健康处置不记录；已在 `internal/biz/retry.go` 将 DNS 解析失败纳入可重试网络错误（SSRF 私网拒绝保持不可重试），修复后该场景通过。该修复尚未部署生产。流式跨来源回退仍只有单元回归覆盖，未在隔离栈验证。

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

F19/F20/F22 非流式部分已于 2026-09-22 在本地隔离栈通过（见上方记录与证据文件）；流式变体与 F7–F18 恢复场景仍待执行。

可控跨来源场景使用 [post-release-forced-failure-verification.md](post-release-forced-failure-verification.md) 及 `scripts/verify-forced-failure.sh`。脚本退出码为 `0` 才能记录该场景 PASS；前置条件不足记录 `BLOCKED` 并保留 preflight 输出。

## 证据格式

每个场景至少记录：`captured_at_utc`、环境、代码提交/镜像摘要、触发动作、根请求或 reservation 标识的脱敏值、状态转移、重复/幂等计数、查询接口响应摘要和最终结论。禁止记录 token、请求正文、上游响应正文和支付凭据。

完成阶段 B 后，回写主分析文档第 7 章“第六批实施状态”和阶段表；未完成的场景保持 `待验收`，不要用服务健康或空表推断恢复正确。
