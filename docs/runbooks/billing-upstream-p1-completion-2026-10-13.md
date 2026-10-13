# P1：原生证据、可靠送达与受控探测工程交付

日期：2026-10-13。基线：`9f096fcf`，接续 `076b056b` 的 P1.1。状态：P1 剩余工程功能已实现并完成本地验证；未部署、未开启新收费或 canary。P0 生产窗口与独立供应商费用验收维持原状态。

关联：[计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游方案](../design/upstream-contract-evolution-plan-2026-10-13.md)、[P1.1](billing-upstream-p1-intake-2026-10-13.md)、[验收索引](evidence/billing-upstream-p1-completion-2026-10-13.json)。本次仅提交方案实现与验收资料，不包含无关自动观察文件。

## 工程闭环

| 功能 | 实现与边界 |
|---|---|
| relay 发件箱 | 每实例 SQLite、WAL、FULL 同步；目录 0700、文件 0600；128 MiB 页数上限、120 MiB 文件水位、最多 10,000 pending 事件；同步 admit、重启恢复、只重投事件，不重打上游。ACK 后保留 digest tombstone 7 天；billing 终态与 inbox 继续保护后续重放 |
| 发送前意图 | billing 在 reserve 的余额事务中写入 intent；返回完整持久描述，relay 再落本地 intent，两端确认后才发送。重复 admit 明确拒绝；不能退款另一个正在执行的相同 attempt |
| lease 与补证期限 | 5 分钟处理 lease，checkpoint 续期不越过 reserve 冻结的 24 小时期限。超 lease 标 evidence_pending，期限内保护冻结资金；最大期限后 sweeper 同事务重查、结束 pending task、CAS 释放。资金 commit/release 竞争仍由原 reservation CAS 排他 |
| inbox 与 ACK | intent/checkpoint/terminal/final 使用 reservation 或内部 attempt/purpose/category/sequence 键；接收端重算 SHA、拒绝冲突。final 原生证据与 pending 财务任务原子接收；received、settlement_accepted、settled 分开。released/expired/超期的迟到证据仍保存，不追扣 |
| 原生 usage v2 | billing/channel 能力先协商；freeze usage、parser、contract、policy、算法版本。缺失、非法、不支持与 verified/estimated/ambiguous 保留区别；合法零值与恢复重放不再变成估算或 reserve amount。未知版本/不完整 consumer/store 失败关闭 |
| 原生证明 | HTTP JSON、SSE、adaptor、gRPC、Responses WS 在转换前采集。保存响应头 checkpoint、有限流式快照、终结/传输失败；receiver 用脱敏原生 usage 重新解析并核对五桶，拒绝仅凭 producer 描述或错账户 override 收费。WS 按真实 active reservation 分轮记录；超过 32 个终结身份后关闭连接，重新连接继续 |
| 异常政策 | 新冻结政策 `native-v2-release-unproven-2026-10-13`：verified 执行既有冻结价格；missing/invalid/unsupported/estimated/ambiguous 不变成估算收费，释放用户冻结。旧请求不回溯改政策；修正路径只在精确 source/model 白名单内启用 |
| 采购与 purpose | user/canary/health_probe/internal_test 在接收边界分流；公网字段/头不能设置免费 purpose。独立 intent 保存供应商精确 source/model 配置费率；用户价格快照仅保存供应商快照引用，不泄露供应商单价。已知零估算用 nullable 0，未知/未计价维度保留 null；tariff_estimate 永不等于 supplier_reported |
| 库存发现 | 定期任务或手工 exact ID 导入，独立 `channel_upstream_inventory` 保存新增/缺失候选和时间；即使 Models 非空也能刷新。保留大小写与命名空间，不改手工 Models/mapping，不自动公开或推断价格。P1 探测模式关闭旧自动发现扩面和无预算 `/models` 循环 |
| canary | monitor 的独立身份调用受控 relay gRPC；仅配置的 source/model/protocol/JSON/SSE/text/tools 目标。复用已注册 adaptor 和原生 parser；固定最小输入，最多 16 输出 token，fake tool 只验证返回，不执行外部工具。内部 intent 无用户 reservation，不能写用户 consume |
| 预算与许可 | 按来源、UTC 日期共享 calls/input/output/cost/unknown exposure 持久预算，来源并发 1；预留完整配置上界，失败不退款预算。未知道成本必须显式允许 unknown calls；不是零成本。relay 通过专用 RPC 原子认领持久、一次性、target SHA 绑定的 permit，跨实例或直接内部调用不能重复发起同一 attempt；剩余 lease 不够有界调用时拒绝 |
| L2 漂移与覆盖 | 有界周期、抖动、失败 backoff；预算/lease/backoff 导致覆盖不足单列。incident first/last、source/model/protocol/revision、新增/缺失/类型变化、脱敏样本、真实流量对照、证据引用、内部费用/预算均持久化。普通真实用户 400 不作漂移；固定有效 canary 的 400 记待核查的契约拒绝。只生成报告，不发布新规则、改价格或扩大收费 |
| 运维与分层 | 三种 dialect 的 126/127/128/130 迁移与 ownership；三种 Compose 默认关闭；Wire/Proto 生成。biz 使用 DO/repo port、data 拥有 PO/WAL/数据库、service 拥有新增 gRPC DTO 转换；新增 RPC 都是 exact dedicated system capability |

原生静态契约沿用 P0 验证基线。混合 Chat/Responses 别名、未独立验证的 Gemini thinking 和未知维度保持 unsupported；没有按字段名字猜计费意义，没有第六个推理桶，也没有 P2 契约发布表/DSL。

## 开关与上线顺序

当前没有修改生产 `.env`、容器或 allowlist。本功能可构建交付，但本次没有创建生产 amd64 镜像或执行部署。

1. 先应用 billing 126、channel 127/130、monitor 128 的所属迁移，更新消费者及 relay/monitor；新开关保持 false。`130` 将 MySQL 来源隔离模型键改为大小写精确 VARCHAR(255)，Postgres 扩长度，SQLite 原有 TEXT/BINARY 不需改型。
2. 为每个 relay 实例准备独立持久目录。模板使用 `./data/relay-outbox:/var/lib/relay-outbox`，实例不能共享 WAL 文件。远程 `scripts/deploy-update.sh relay-gateway` 预检会以 65534:65534、0700 创建目录；手工 Compose 启动需要先完成相同准备。
3. 验证新的 exact 身份链：relay→billing RecordExecutionEvidence、relay→channel GetUsageCapabilities/RecordUpstreamInventory、monitor→relay ProbeUpstream、relay→monitor ClaimProbePermit。复用已独立配置的 SERVICE_IDENTITY_TOKEN / receiver SERVICE_CALLER_TOKENS；shared token 不能取得这些权限。
4. 收费修正只在 `BILLING_EXECUTION_DELIVERY_V1=true`、`RELAY_EXECUTION_DELIVERY_V1=true`，且 `RELAY_NATIVE_USAGE_V2_ALLOWLIST=source_kind:source_id:exact_upstream_model` 命中的受控路径生效。空白名单保持旧请求路径。它包括异常释放和合法零费用，属于真实政策变化，必须先验收费用差异再选来源启用；不能借工程交付自动放行。
5. canary 与收费灰度分开：relay/monitor/channel 的 `UPSTREAM_PROBES_ENABLED=true`，relay 与 monitor 使用同一 `UPSTREAM_PROBE_TARGETS_JSON`；monitor 需要数据库预算/报告/覆盖表，relay 需要持久 outbox，billing 需要内部 evidence 接收能力。MONITOR_GRPC_ENDPOINT 为 monitor-worker:9007，RELAY_GRPC_ENDPOINT 为 relay-gateway:9003。优先配置独立供应商项目/账号。

配置示例（示意 source/model，不是生产启用指令）：

```json
[{"id":"pilot-json","source_kind":"channel","source_id":1,"model":"vendor/exact-model","protocol":"openai_chat","capability":"text","stream":false,"calls_per_day":10,"input_tokens_per_day":1280,"output_tokens_per_day":160,"max_output_tokens":16,"cost_per_day":0,"unknown_cost_calls_per_day":10}]
```

`cost_per_day=0` 与 `unknown_cost_calls_per_day=10` 表示明确接受最多 10 次未知成本暴露，不表示免费。已知成本上界可配置 `cost_upper_bound`（既有固定精度单位）；配置必须覆盖实际来源合同和固定请求长度。原生计数超过输入 128/目标输出上界会暂停该来源当日后续探测；输出长度参数不能证明未知供应商成本上界。多个模型/协议任务的同来源每日预算必须一致，不能拆任务扩大预算。

`inventory=true` 建立库存任务，可不设 model；缺少 list API 时提供 `manual_model_ids`。`capability=tools` 请求固定 `p1_probe`，不会执行外部工具。调度周期 `UPSTREAM_PROBE_INTERVAL` 默认 5m，允许 1m–24h；来源失败 backoff 最多 16 个周期，覆盖状态会标明跳过与下一时点。64 个目标顺序执行，有界队列延迟最多包含每目标 35 秒调用窗口。

## 报告与恢复

- `monitor_probe_coverage` 说明未覆盖、预算、lease、backoff 与 executed；不能用 `/models` 成功代替推理契约通过。
- `upstream_drift_reports` 保存 L2 incident，`monitor_probe_attempts` 保留对应内部 attempt/result，`upstream_execution_evidence` 保存与真实流量的来源证据，`billing_execution_intents` 保存完整独立采购费率与期限。
- 可在具有 monitor 所属 DSN 的环境运行 `go run ./app/monitor/cmd/upstream-report -driver <mysql|postgres|sqlite3> -out /absolute/new/report.json`。输出限每类 1000 条，0600、新建文件，不含上游 key、请求内容或真实用户提示。
- `micro_one_api_relay_execution_outbox_pending` 与 oldest_age、monitor probe runs/coverage 使用固定枚举标签；source/model/request ID 留在受控存储。
- unsupported source/model 进入持久隔离，普通时间到期或后续 verified 都不能解除；使用现有带身份、理由与 revision 的人工 resolve。invalid 立即进入既有时间隔离，missing 按既有阈值隔离；两者都不会变成 estimated。
- 回滚先停止新 admit/探测，保留新消费者和所有持久表/卷继续收单、排空。关闭生产者开关不能丢弃旧 outbox；启动会恢复已存在的文件，billing 仍接收已持久意图的在途证据。不要在 pending 未结束时直接回退到不识别新协议的旧消费者或删除数据。

## 验收与仍需现场核验的条件

本地测试覆盖完整 P1 工程闭环：WAL/FULL/权限/磁盘满、重启保留与 duplicate admit、digest/原生证明/账户 override、零费用与重放、冻结用户/供应商价格、lease/最大期限、关闭后的迟到证据、独立 canary 无用户 quota、同来源预算/重启/旧 completion 竞态、一次性 permit/剩余 lease、库存不改授权、unsupported 人工解除、L2 及前端零/未知状态。具体命令与通过情况以验收索引为准。

P0 完整生产窗口、独立供应商账单核验、生产灰度费用差异及新镜像 linux/amd64 现场部署验证仍未完成。磁盘/进程崩溃可恢复已同步写入事件；单节点持久卷和主机同时损毁不能保证取回原文，billing intent/期限仍保护用户资金。这些边界不由本地单元回归替代。
