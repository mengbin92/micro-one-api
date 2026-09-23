# Micro-One-API v0.32.0 发布：运行观测、订阅额度解释与账号治理

> 2026-09-23 · 上一版：[v0.31.1](./release-v0.31.1.md)（2026-09-23）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.32.0)

v0.32.0 是 v0.31.1 之后的 **MINOR 功能与可靠性版本**：完成下一阶段计划第二批
O1–O4 及 O5 正确性修复，新增路由注册强制观测、告警投递与请求追踪关联、订阅
已结算/冻结/可用额度解释，以及账号恢复筛选和探测用量指标。

**API/proto 增量扩展，无新增数据库迁移**。升级涉及 `billing-service`、
`channel-service`、`notify-worker`、`admin-api`、`relay-gateway`，独立挂载的
前端，以及 Prometheus/Alertmanager/Grafana 配置。

## 功能与修复

### 1. 注册路由强制观测与请求关联

**根因**：入口分别记录终态，前置中间件拒绝、内部 deadline、panic 和 raw 路径
容易漏记；响应包装可能混淆 1xx/最终状态，资源 ID 可能进入指标标签。

**修复**：注册时明确执行/只读/不支持类别，统一请求终态和固定路径标签；保留
流式 Flush、WebSocket Hijack 语义及实际来源、健康与计费的单次记录。Relay
接入 HTTP span，兼容请求 ID、OTel ID、路由审计和 Playground 响应头关联。

**影响服务**：`relay-gateway`、`admin-api`、前端。

### 2. 告警投递、对账详情和运营指标

**根因**：Prometheus firing 不代表外部收到通知；对账通知未完整解释七类差异，
通知失败、Redis 限流降级、去重冲突和探测用量缺少集中的运行视图。

**修复**：Compose 加入 Alertmanager，通过 notify-worker 持久通知队列发送
firing/resolved，沿用现有重试及 sent/failed 状态。未配置接收端明确记为失败；
补齐七类对账详情、低基数指标及 operations 看板。Anthropic/Codex 探测 token
独立计数，missing usage 不解释为零成本。

**影响服务**：`notify-worker`、`billing-service`、`channel-service` 及监控组件。

### 3. 订阅额度的权威快照

**根因**：仅展示已结算用量会遗漏在途订阅预留；冻结量使用最新倍率或错误窗口
计算，会误导可用额。无限、零额度和未知冻结也需要明确区分。

**修复**：billing 在订阅行锁事务内读取结算计数和预留，逐预留应用冻结倍率与
窗口，新增 `GetSubscriptionUsage` RPC。admin/relay 共用该快照；保留旧
`used`/`remaining`，新增 settled/frozen/available、无限/超限、窗口和观察时间。
已配置 billing 时读取失败不静默回退。前端展示冻结/可用额，未知保留为空值。

**影响服务**：`billing-service`、`admin-api`、`relay-gateway`、前端。

### 4. 原子重置与账号恢复入口

**根因**：非原子重置接口及陈旧扫描快照可能清除新窗口用量；manual 账号的
恢复信息可能被无额度分支或手机隐藏列遮蔽。

**修复**：强制仓储原子记录重置与更新用量，保护已推进的窗口，读写复用窗口
计算。新增服务端恢复策略筛选，并在桌面/手机账号名称下展示原因与等待时长。

**影响服务**：`channel-service`、`admin-api`、前端。

### 5. 缓存坏值与降级边界

**根因**：Redis 损坏 JSON、null 或空值可能长期残留并计入命中；失效接口命名
掩盖整片失效，影响撤权故障处置。

**修复**：坏值比较删除后回源，回源失败仍清理；缓存写入与 TTL 原子设置。
失效明确命名为 `InvalidateAll`，移除无人调用的接口，记录 legacy L1/L2 TTL
和 Redis 故障边界。V2 继续逐请求读取权威数据，未恢复旧鉴权缓存。

**影响服务**：`relay-gateway`。

## 兼容性说明

- proto 均为增量扩展，无新增迁移；旧 `used`/`remaining`、`isValid` 别名保留。
  新客户端使用 `available` 并区分 null/0/unlimited。
- 新 admin/relay 依赖 billing 的新 RPC，先升级 billing；恢复筛选依赖新的 channel。
  不改变 24 小时/7 天/30 天窗口、跨窗结算、续费和购买合同语义。
- `OTEL_EXPORTER_OTLP_ENDPOINT` 为可选配置，未配置时不启用 exporter；本版只
  承诺 Relay HTTP span 与审计关联，不代表所有下游服务都有 span。
- Alertmanager 模板加入主 Compose，使用 backend 网络、不公开端口；其他部署
  方式需按现有拓扑接入。外部通知沿用 `NOTIFY_WEBHOOK_URL` 和既有 webhook
  JSON 契约，未配置接收端不能验收为送达。
- 无新增必填配置。默认 executor、gRPC breaker、V2 缓存及计费开关保持既有策略。

## 升级步骤

1. 备份数据库、镜像、监控配置和 `/opt/web/dist`，核对当前迁移状态；本版无新 DDL。
   重启 Relay 前确认凭证待补写数量为零。
2. 依次升级 `billing-service`、`channel-service`、`notify-worker`，再升级
   `admin-api`、`relay-gateway`。使用发布镜像或本机交叉构建，不在生产主机构建。
3. 单独构建并发布 `web/dist` 到宿主机挂载目录；重建 admin 镜像不会更新该目录。
4. 同步 Compose、Prometheus 告警/抓取配置、Alertmanager 配置和 Grafana 看板；
   配置接收端及可选 OTLP collector，再启动或重新加载相关组件。
5. 核对健康状态、订阅 null/0/冻结、manual 筛选、请求/审计 ID。保存真实通知
   firing/resolved 的接收回执和 sent 状态，核对 collector 实际收到 trace；生产
   QPS/P95 和故障传播延迟按[运维手册](../runbooks/routing-observability-runbook.md)测量。
6. 回滚先恢复前端和 admin/relay，再恢复其依赖服务及监控配置；保留通知、预留、
   账本和重置记录，避免新客户端继续调用旧 billing RPC。

## 验证

- 第二批本地验收已覆盖受影响 Go 模块、关键钱/权限/并发包 race、MySQL 8.4 /
  PostgreSQL 16 / SQLite 冻结与原子重置、Prometheus 三组规则、Alertmanager
  配置、前端相关 23 个单测及桌面/手机 4 个 Playwright 用例。
- 发布候选 `make all`、`make verify` 全部通过，包含 Go 单测、race、分层与
  迁移治理、生成 API 类型一致性、前端 lint、195 个单测及生产构建。
  `make test-integration`、全量 gosec、部署文档/本地链接和 diff 检查通过；阶段证据见
  [第二批审查及验收记录](../design/next-stage-plan-2026-09-22.md#第二批审查及验收记录2026-09-23)。
- 发布流水线执行 MySQL/SQLite 路由、Compose E2E 和管理台浏览器门禁，通过后
  构建九服务 linux/amd64、linux/arm64 镜像并创建 GitHub Release。
- 本版发布不代表已部署生产。真实通知送达、OTel 导出及生产回源成本/Redis
  传播延迟仍待验收；没有向外部接收人发送测试通知。

## 完整变更日志

- `f0d31ecf` feat(operations): complete observability and subscription usage stage
- docs(release): v0.32.0
