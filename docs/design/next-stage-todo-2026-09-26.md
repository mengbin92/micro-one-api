# micro-one-api 下一阶段待办

- 整理日期：2026-09-26
- 依据：`docs/design/next-stage-plan-2026-09-22.md`（文档更新至 2026-09-27）
- 说明：已于 2026-09-26 对生产通知记录、Prometheus 回源指标和 MySQL 索引计划做只读核对；2026-09-27 补做 O2 只读观察及 O5 隔离故障延迟复测，并上线 O2 OTLP 导出；v0.33.1 发布后再次只读复核，仍无自然规则 firing 或授权路由请求。各项原始口径见对应证据文件。生产已切换 QQ 邮件，并补上 Alertmanager 与 Prometheus 转发；受控 firing/resolved 邮件均为 sent，真实规则触发仍待自然观察。

## 一、优先收口：已实现功能的外部验收

| 顺序 | 事项 | 完成证据 |
| --- | --- | --- |
| 1 | **O2 生产通知后续观察**：QQ 邮件配置、Alertmanager 服务及 Prometheus 转发已上线；受控 firing/resolved 邮件和 Alertmanager 注入 firing 均为 `sent`。2026-09-27 发布后复查仍发现过去 24 小时无真实规则 firing，九个业务抓取目标均正常、规则评估失败数为零。继续观察真实规则触发后的 firing/resolved 及异常送达状态。 | [受控投递及后续观察](../runbooks/evidence/o2-qqmail-production-2026-09-26.json)记录通知 96/97/98、SMTP 接受和 24 小时 Prometheus 查询；真实规则告警及收件箱呈现仍缺独立证据，授权码不入库。[发布后只读复核](../runbooks/evidence/next-stage-postrelease-observation-2026-09-27.json)仍无自然 firing。 |
| 2 | **O2 OTel 授权请求联查**：生产已部署内部 Jaeger OTLP receiver、持久化 Badger 48 小时存储和 Grafana 数据源；Relay HTTP span 已实收，401 探测的请求 ID 与 Jaeger `request.root_id` 一致。发布后从 OTLP 上线时刻起仍无新的授权路由审计行；剩余：用一条授权用户请求把根请求、attempt、trace 和路由审计串齐。 | [上线记录](../runbooks/o2-otel-export-2026-09-27.md)与[脱敏证据](../runbooks/evidence/o2-otel-production-2026-09-27.json)覆盖导出、存储及无认证探测；该探测不产生 attempt，不能算完整关联验收；[发布后复核](../runbooks/evidence/next-stage-postrelease-observation-2026-09-27.json)记录授权审计行数为零。 |
| 3 | **O5 生产缓存边界与成本**：24 小时低流量回源 RPC 基线已采；2026-09-27 修正采样时序后的完整隔离复测通过，Redis 故障下 chat、结算和恢复可用，V2 GetAuthSnapshot P95 为健康 4.60ms、故障 9.14ms、恢复 4.95ms。发布后 24 小时身份、渠道、账务权威 RPC 仍仅约 95、102、47 次增量；后续只在有代表性生产流量后复采成本。 | [生产低流量样本](../runbooks/evidence/o5-production-rpc-baseline-2026-09-26.json)为 identity/channel 各约 123、billing 59 次增量；[隔离复测](../runbooks/o5-redis-fault-isolation-2026-09-26.md)以独立抓取窗口取代旧 P95。identity 内部回退路径和熔断拒绝延迟仍无归因；不据此启动缓存优化；发布后低流量[复核证据](../runbooks/evidence/next-stage-postrelease-observation-2026-09-27.json)亦未改变结论。 |
| 4 | **Q2 支付 F17 后续观察**：支付宝沙箱正常支付往返及丢回调查单、暂时失败重试、重复签名回调、发放失败恢复四项隔离故障验收已完成。后续关注长期查单失败时的告警/重试治理及实际平台重发。 | [沙箱往返证据](../runbooks/evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)与[隔离故障验收](../runbooks/f17-fault-acceptance-2026-09-26.md)分别记录正常付款和四项故障；浏览器 `return_url` 已确认，服务端异步 `notify_url` 没有独立沙箱 HTTP 回执。 |
| 5 | **Q3 后续性能对照**：Linux/amd64 同机基线与候选各三次完整 k6 已完成，历史基线重定为 `v0.32.2` 后的门禁重跑通过；v0.33.1 发布后，当前默认基线更新为 `v0.33.1`。未来候选继续用同一流程比较；executor 流式回归归因前维持 legacy 默认路径。 | [Q3 对照及原始 artifact](../runbooks/q3-rebaseline-2026-09-26.md)记录旧归档基线下 chat P95 +25.2%、aggregate P95 +25.3%，新基线首跑七项门禁全过；[生产 MySQL 对照](../runbooks/evidence/q3-production-mysql-dashboard-index-2026-09-26.json)旧/新索引中位数 229/225ms，不能证明显著生产收益。 |

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
