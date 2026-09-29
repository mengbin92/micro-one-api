# micro-one-api 下一阶段待办

- 整理日期：2026-09-26
- 依据：`docs/design/next-stage-plan-2026-09-22.md`（文档更新至 2026-09-28）
- 本轮执行（2026-09-28）：用户确认线上目前仅本人使用，**暂缓 O5 代表性流量复采及缓存优化**，不再以补齐流量样本阻塞后续工作。转入 Q2/F17：支付补偿失败指标、持续失败告警及处置手册已完成本地验收；billing-service 与 Prometheus 规则已上线并完成只读接线核对，保留每分钟重试和账务幂等语义。
- 说明：2026-09-28 已完成 O2 生产只读验收：五组真实规则 firing/resolved、十封通知均为 sent，既有授权请求的 root/attempt/audit/Jaeger trace 关联一致；同日收件人确认第五组（通知 108/109）QQ 收件箱呈现完整、内容与服务端一致。观察窗口含受控采样，不等于纯自然流量。见[本轮验收](../runbooks/o2-production-acceptance-2026-09-28.md)。此前分项证据保留。

## 一、优先收口：已实现功能的外部验收

| 顺序 | 事项 | 完成证据 |
| --- | --- | --- |
| 1 | **O2 生产规则送达已验（2026-09-28）**：四组 HighRelayErrorRate、一组 UpstreamCostMissing 的 firing/resolved 已按 fingerprint/startsAt 配对并回查历史 Prometheus 样本；通知 100–109 均 sent、重试 0。当前无 firing、九个抓取目标正常、规则评估失败 0。第五组（通知 108/109）QQ 收件箱呈现经收件人确认完整、内容一致。 | [本轮验收](../runbooks/o2-production-acceptance-2026-09-28.md)及[脱敏证据](../runbooks/evidence/o2-production-acceptance-2026-09-28.json)。证明真实规则、SMTP 提交及该组收件箱呈现，不证明纯自然流量、其余四组收件箱呈现、邮件阅读或告警根因；替代上次“尚无真实 firing”的现状。 |
| 2 | **O2 OTel 授权请求服务端联查已验（2026-09-28）**：复用 2026-09-27 O5 的 step-5-preview 请求，核对同一 root、一次 committed attempt、planned/success 两条审计和 Jaeger HTTP span。 | [关联记录](../runbooks/o2-production-acceptance-2026-09-28.md#授权请求-root--attempt--audit--trace)与[证据](../runbooks/evidence/o2-production-acceptance-2026-09-28.json)。本轮为数据库/Jaeger 回溯，未重采响应头或管理 HTTP 接口；不扩大为下游 gRPC/供应商 span 验收。 |
| 3 | **O5 生产缓存边界与成本**：24 小时低流量回源 RPC 基线已采；2026-09-27 修正采样时序后的完整隔离复测通过，Redis 故障下 chat、结算和恢复可用，V2 GetAuthSnapshot P95 为健康 4.60ms、故障 9.14ms、恢复 4.95ms。2026-09-27 晚以专用限时 token 完成 `step-5-preview` 生产受控采样：135 个成功路径请求（134 成功、1 次客户端超时），窗口内 GetAuthSnapshot/GetRoutingGroup 各约 294 次 OK、GetRoutingCapabilities 约 147 次 OK，无非 OK，P95 分别为 4.91/4.88/0.96ms。2026-09-29 补充服务端探针；review 发现采样窗口未按各目标抓取对齐，原数字仅作历史记录，已修正采样器待完整复测。identity 同步 RPC 无 Redis 回退；共享 MySQL/VM 争用尚属假设。熔断拒绝本地已按 `rejected` 独立计数，拒绝延迟仍未测。缓存优化是否启动，待自然业务流量达到可代表水平后再复采判断。 | [生产低流量样本](../runbooks/evidence/o5-production-rpc-baseline-2026-09-26.json)为 identity/channel 各约 123、billing 59 次增量；[隔离复测](../runbooks/o5-redis-fault-isolation-2026-09-26.md)以独立抓取窗口取代旧 P95。[受控采样证据](../runbooks/evidence/o5-step5-production-sampling-2026-09-27.json)确认单次成功请求成本为鉴权+选路各约 2 次、额度预留约 1 次权威 RPC 且全部 OK；[证据纠错](../runbooks/evidence/o5-attribution-2026-09-29.json)保留原样本并撤销过度归因，待修正窗口后的复测和 DB/宿主观测。 |
| 4 | **Q2 支付 F17 后续观察**：支付宝沙箱正常往返及四项隔离故障验收已完成。2026-09-28 补齐后台扫描 success/partial/error 指标、最近一轮失败状态及持续 5 分钟告警，本地故障/恢复和规则验收通过；billing-service 与规则已上线，Prometheus 采到失败状态 0，新规则 health=ok/inactive。退避/扫描治理按证据启动，实际平台重发继续保留。 | [沙箱往返证据](../runbooks/evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)与[隔离故障、告警及上线记录](../runbooks/f17-fault-acceptance-2026-09-26.md)区分沙箱、本地注入与生产只读证据；服务端异步 `notify_url` 仍没有独立沙箱 HTTP 回执，新告警尚无生产 firing/resolved 或收件箱证据。 |
| 5 | **Q3 后续性能对照**：Linux/amd64 同机基线与候选各三次完整 k6 已完成，历史基线重定为 `v0.32.2` 后的门禁重跑通过；v0.33.1 发布后，当前默认基线更新为 `v0.33.1`。未来候选继续用同一流程比较；executor 流式回归归因前维持 legacy 默认路径。 | [Q3 对照及原始 artifact](../runbooks/q3-rebaseline-2026-09-26.md)记录旧归档基线下 chat P95 +25.2%、aggregate P95 +25.3%，新基线首跑七项门禁全过；[生产 MySQL 对照](../runbooks/evidence/q3-production-mysql-dashboard-index-2026-09-26.json)旧/新索引中位数 229/225ms，不能证明显著生产收益。 |

2026-09-28 06:37 UTC 已完成一次 [O5 只读复采](../runbooks/evidence/o5-production-rpc-observation-2026-09-28.json)：排除已知受控采样的最近 12 小时，鉴权/选路各约 859 次、额度能力约 404 次 OK，P95 为 4.95/4.85/0.97ms，未返回非 OK 序列。六个小时无调用，三类计数器各重置一次；样本量增加仍不能证明流量具有代表性，也未显示持续高负载或显著回源成本。O5 代表性生产流量验收继续保留，不启动缓存优化、不为凑样本额外制造生产模型调用。下一次复采需标注正常业务/峰值时段、请求分布与样本量，并区分受控流量、计数器重置及抓取缺口。

O5 暂缓决定（2026-09-28）：以上历史样本继续保留，但当前不安排额外复采或生产模型调用；恢复条件是实际多用户/代表性业务流量出现，或有明确回源延迟、QPS、成本问题，再按上述口径测量。2026-09-29 review 后保留延迟归因、拒绝延迟及生产部署验证边界；已确认 identity 同步 RPC 无 Redis 回退，本地 `rejected` 计数回归通过。生产自然 Redis 故障仅作为事后取证机会，需核对可用性、降级、结算、告警通知与恢复后才验收；不主动注入，不改变暂缓决定，见 [O5 runbook](../runbooks/o5-redis-fault-isolation-2026-09-26.md#2026-09-29-归因)。

## 二、按外部条件补齐的验证

- **D1 真实 OAuth 与多副本**：生产目前只有 `static_key` 账号、单 Relay/单 channel。接入 OAuth 账号或准备多 Relay 部署时，验证真实供应商轮换、Redis 协调、进程故障切换、待补写恢复和人工重新授权路径；扩展多 channel 之前，先设计共享 selector/会话协调。现有受控 OAuth、Redis 和三方言隔离测试不代替这些生产证据。
- **Q1 供应商侧取消确认**：Relay 边界的断流、预留释放及 `canceled` 终态已有线上证据。若需要证明 StepFun 内部逐请求停止推理，须取得供应商侧证据；TCP 连接状态不能证明这一点。

## 三、条件启动的功能与技术债

这些项目是计划中的保留项，只有满足触发条件才立项，不应一次性全部开工。

| ID | 触发条件 | 启动后的最小工作 |
| --- | --- | --- |
| D2 额度/订阅产品 | 出现明确平台、套餐、排队或退款需求 | 账号月窗口、用户自然月、USD/上游百分比标定、预约排队、部分退款与升级/降级续费；自然月不得静默改变旧订单，站内冲正不得冒充原路退款。 |
| D3 事件/缓存治理 | 毒事件堵塞、积压/扫描耗时、回源 QPS 或重复告警有证据 | 先故障注入并写处置手册，再做有界隔离/人工重放；按测量做轮询、精确失效、预热、版本缓存与增量化。Redis 故障不能当毒事件跳过。 |
| D4 模型能力/健康选路 | 真实接入新供应商或模型故障影响路由 | 逐家补原生适配；区分模型不可用与全渠道故障，设计恢复探测和防饥饿；完善类型化上游错误、默认模型和可靠成本边界。 |
| D5 存储/安全/审计 | 容量、保留期、安全或审计需求明确 | 按数据规模决定分区和账本归档；维护 SSRF 地址段及启动警示；独立审计查询、限时脱敏内容排障；会话迁 cookie 时同时设计 CSRF 和身份接口。 |
| D6 架构收敛 | 对应业务修改或可比性能证据出现 | 先归因 executor 流式回归、完成观察与回滚验证，再考虑去掉 legacy；按实际变更拆大文件、合并重复抽象，不为行数整仓重写。 |
| D7 体验/质量 | Q1/Q3 基线完成且有具体使用需求 | 安全 Markdown、历史编辑/重发、字体优化、钱/权限/取消关键分支覆盖率及交互时序；对账容差仅在业务证据支持时参数化。 |

## 执行约束

每项记录任务 ID、提交、实际行为、测试或线上证据及剩余边界。将隔离测试、生产事实和供应商/沙箱证据分别标注。涉及钱、权限、迁移和并发时，验证故障及恢复；发布按仓库要求完成 release note、CHANGELOG、README、develop、main 和 tag 的完整流程。
