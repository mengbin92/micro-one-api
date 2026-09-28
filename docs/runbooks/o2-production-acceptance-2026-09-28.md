# O2：生产规则送达与授权请求关联验收

2026-09-28 03:11 UTC 只读复核生产 Prometheus、通知数据库、路由审计、计费预留与 Jaeger，完成 O2 剩余两项服务端验收。复用已有请求和告警，本轮没有发送模型请求、注入告警或变更生产配置。

执行基线为 `794267bc`（完整 SHA 见[脱敏证据](./evidence/o2-production-acceptance-2026-09-28.json)）；这是核验时仓库提交，不代表历史请求运行的镜像版本。历史授权样本来源于 [2026-09-27 O5 受控采样](./evidence/o5-step5-production-sampling-2026-09-27.json)。本记录与证据的提交号以 Git 历史为准。

## 真实规则 firing/resolved 与邮件送达

对通知 100–109，按 `fingerprint + startsAt` 配对 firing/resolved，再在每个区间中点查询 `ALERTS{alertstate="firing",alertname="..."} @ <unix>`。五个区间均查得值为 1 的历史规则样本；当前规则 API 中这两条规则均为 `health=ok`、`inactive`。

| 规则 | 发生区间（UTC） | firing / resolved 通知 | 投递结果 |
| --- | --- | --- | --- |
| HighRelayErrorRate | 09-27 11:59:36–12:23:36 | 100 / 101 | sent / sent |
| HighRelayErrorRate | 09-27 12:29:06–12:33:36 | 102 / 103 | sent / sent |
| UpstreamCostMissing | 09-27 12:57:56–13:22:56 | 104 / 105 | sent / sent |
| HighRelayErrorRate | 09-28 01:19:36–01:20:36 | 106 / 107 | sent / sent |
| HighRelayErrorRate | 09-28 01:26:06–01:29:06 | 108 / 109 | sent / sent |

十条记录的 `retry_count` 均为 0。自上次观察时刻（09-27 09:50:11 UTC）起，通知状态聚合为 `sent=10`，未出现其他状态；Prometheus 24 小时投递增量约 10.0017 是外推值，不作为精确邮件数量。当前无 firing，九个业务抓取目标均为 `up=1`，规则评估失败计数为 0。

`HighRelayErrorRate` 的历史样本标签为 `/v1/messages`、502；`UpstreamCostMissing` 为 `provider_family=other`。这证明实际规则经过 Prometheus → Alertmanager → notify-worker → SMTP 提交并发送恢复通知；告警根因及阈值正确性不由送达验收推断。窗口含受控 O5 采样及其他请求，不能称为纯自然业务流量。

**QQ 收件箱呈现已验（2026-09-28，收件人确认）**：收件人提供了第五组（fingerprint `5fdc4a619b4f0afe`，09-28 01:26:06–01:29:06 UTC，对应通知 108/109）的 firing 与 resolved 两封 QQ 邮件原文。邮件状态、alertname、标签集（`/v1/messages`、502、critical）、annotations、startsAt/endsAt 与 fingerprint 均与服务端记录一致。该组在收件箱中呈现完整、内容正确。其余四组按同一链路推断可达，但只有本组有收件人侧独立证据；邮件是否被阅读不在验收范围。

## 授权请求 root / attempt / audit / trace

选取已存在的 `step-5-preview` 成功请求，按用户 2 和根请求 ID 定位权威存储记录：

- 根请求：`o5-step5-stage2b-1790514467-100`。
- attempt：仅 1 条，`attempt_number=1`，channel 9，`committed`，实际费用为 1 个内部 quota 单位；上游模型为 `step-5-preview`。
- 路由审计：70500 为 planned，70502 为最终 success；二者同一根请求、用户、模型、来源和 `otel_trace_id`。
- trace：`392979152cd3a470f821997679f3f910`；Jaeger 查得 `relay-gateway` 的 `HTTP POST` span，`request.id`、`request.root_id` 均等于上述根请求，时长 787703µs。

attempt 的 `request_id=req_261da490f3f8f638a3fd6f6e24c0f234` 是内部尝试 ID，与根请求 ID 不同；通过 `root_request_id` 关联。该 trace 只有 Relay HTTP 入口 span，不含独立 attempt span。

本次回溯读取与管理查询相同的权威表，未重新调用管理 HTTP 接口或捕获原请求响应头；响应头的既有验证见 [OTLP 上线记录](./o2-otel-export-2026-09-27.md)。因此本次关闭的是授权请求在服务端的关联验收，不扩大为浏览器请求检查器、所有下游 span 或供应商追踪的验收。Jaeger 保留期为 48 小时，归档证据不承诺该 trace 永久在线可查。

## 复核入口与边界

- Prometheus：证据保留每次查询的表达式、时间和值；`@` 后的时间是历史采样位置，结果数组时间是本次查询评估时间。
- 通知：`oneapi_notify.notifications` 按发生时间读取 ID、状态、重试次数、发送时间及告警状态/名称/fingerprint；未归档收件地址、注释正文或授权码。
- 路由：`oneapi_log.logs` 限定 `user_id=2`、上述 `root_request_id` 和 `source='routing-selection'`；预留为 `oneapi_billing.billing_reservations` 的同用户/根请求记录。
- Jaeger：内部只读 `GET /api/traces/392979152cd3a470f821997679f3f910`，只保留服务、span 身份、时间与请求 ID 标签，不保存提示词或令牌。

采集时断言通过：五组 firing/resolved 匹配且均有历史 Prometheus 样本；授权请求只有一次 committed attempt；两条审计的 trace 相同；HTTP span 的根请求 ID 匹配。归档后再次验证 JSON、关键关联、文档链接和 diff 空白。

O2 的规则触发/恢复、SMTP 提交、QQ 收件箱呈现（第五组，收件人确认）与授权请求服务端联查均已验。O5 代表性流量复采、真实 OAuth/多副本、F17 后续观察及条件启动的 D2–D7 按原计划保留，不据本次 O2 结论自动启动。
