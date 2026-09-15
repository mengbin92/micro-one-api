# 2026-09-15 P1-1 路由 / outbox 验收记录

代码基线：`develop@0b2c7562`，加本次 P1-1 工作区改动。未新增公共 API 或数据库迁移。所有故障注入均在本机隔离 Compose 项目中进行；没有生产部署、开关变更或远程 CI 执行。

## 交付

- identity / channel / subscription outbox：全部 pending、最老消息年龄、最后成功投递、最近成功扫描、分操作失败计数；未配置 Redis 仍可观察积压。
- 订阅 outbox 补齐错误回调，三个 owner 输出 owner / operation / event_id 结构化日志。
- 复用权威 RPC 时延直方图并补齐客户端错误计数；路由 resolve / ordered / reserve / recheck 输出有限 reason 标签及请求 / 主体日志。
- 快照内容、解析、摘要、主体 / 订阅绑定失败分别计数，持久化校验失败记录 reservation / request / user ID。
- 九项告警、十个 Grafana 面板，以及[运行手册](./routing-observability-runbook.md)。规则测试随共享 nightly / release E2E 入口执行。

## 验证结果

| 验证 | 结果 |
| --- | --- |
| `make verify` | 通过：format、Go 单元测试、既有 race 门禁、分层、迁移治理、前端 lint / test / build |
| 相关包 Go 回归 | 通过：routingoutbox、metrics、xgrpc、relay biz / service / server、billing biz / data、identity / channel data、subscription data |
| `go test -race ./platform/routingoutbox ./platform/grpc/xgrpc ./internal/biz ./app/billing/internal/data` | 通过：额外覆盖新 worker 指标、错误回传、无 Redis 客户端、超过单批次积压、快照损坏 |
| Prometheus v3.6.0 `promtool test rules routing.test.yml` | 通过：九项新增规则的可执行断言，包含故障触发 / 恢复、空闲队列、扫描过时、采集失败、首次能力 / 快照失败、存储与 RPC 故障和时延 |
| `python3 scripts/test-routing-e2e.py --driver=mysql` | 全阶段通过，包含原始规则的 firing / 恢复及阶段 F 预检；[日志](./evidence/routing-observability-mysql-2026-09-15.txt) |
| `python3 scripts/test-routing-e2e.py --driver=sqlite3 --skip-build` | 使用同一业务镜像，全阶段通过；[日志](./evidence/routing-observability-sqlite3-2026-09-15.txt) |
| Dashboard / 脚本静态检查 | 通过：JSON 可解析、十个面板 ID 唯一且查询非空、Python 语法；未启动 Grafana 做浏览器视觉验收 |

## Redis 故障证据

两个方言都执行以下断言，未缩短生产规则阈值：

1. Redis 断开期间，通过真实接口撤权；数据库保留待投递事件。Prometheus 实际采集到 identity pending > 0 和 publish failures > 0。
2. `/api/v1/query` 查询 `ALERTS`，等待 `RoutingOutboxDeliveryFailing` 与 `RoutingOutboxBacklog` 均为 firing。
3. Redis 恢复后，数据库积压清空，Prometheus 采集到 pending=0 / oldest age=0 / last success>0，两个告警解除。
4. 已撤权 Key 继续拒绝；重新授权后聊天与结算成功。已有用例同时检查拒绝请求不会调用上游或扣款。
5. billing 可达但快照能力关闭时，fixed / ordered 请求均拒绝，`reserve/billing_capability` 计数 > 0，`RoutingCapabilityRejected` firing。

## 边界

- 真实 Redis 告警演练覆盖 identity 发布路径；三个 owner 的指标、投递失败和恢复由 SQLite 数据边界测试覆盖。本次未配置 MySQL / PostgreSQL 单元测试专用 DSN，因此其可选数据边界子测试跳过；MySQL 由完整服务 E2E 提供真实数据库验证，PostgreSQL 未重新验收。
- `last_success` 是本 worker 成功发布和确认时间，不能替代消费者进度或授权断言。共享订阅表的 pending 不能把不同副本相加。
- 当前没有 Alertmanager，证据证明 Prometheus 告警状态变化，不代表外部通知已发送。生产观察窗口与小额使用样本仍需后续执行。
- E2E 默认清理本次私有容器、网络和卷；证据文件仅保留通过的场景和观察断言，不包含随机测试凭据、原始 Compose 配置或服务日志。
