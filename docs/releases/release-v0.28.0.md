# Micro-One-API v0.28.0 发布：模型健康被动监测与 Websocket 多轮计费修复

> 2026-09-10 · 上一版：[v0.27.0](./release-v0.27.0.md)（2026-09-10）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.28.0)

v0.28.0 是 v0.27.0 之后的 **MINOR 模型可观测性版本**，包含 14 个提交
（3 feat + 1 refactor + 5 fix + 1 perf + 2 docs + 1 chore + 1 merge，不含发布文档提交）。
新增按来源与上游模型粒度的被动模型健康监测与管理台看板，修复多轮 `/v1/responses`
Websocket 连接按累计 usage 重复计费的问题，并把 404 模型不可用判定收窄到 API 形状
响应体，避免误配 `base_url` 把整渠道模型标红。

**proto additive 变更**：`channel.v1` 新增 `RecordModelHealth` / `ListModelHealth` 两个 RPC
与对应管理端 HTTP 端点，既有接口不变。**数据库新增迁移 `091`**（MySQL / SQLite /
PostgreSQL 三方言）：创建 `model_health_states` 表。无新增必填配置项，不改变渠道路由
资格与计费决策。

## 1. 模型健康被动监测

**根因**：渠道级健康无法区分"某个模型故障"与"整个来源故障"，事故定位只能人工翻日志，
且任何探测式巡检都会产生付费的合成长度流量。

**修复**：

- relay 执行器在同源重试收敛后，把终态结果（成功 / 可归因上游失败）异步投递到独立
  队列持久化，**不在请求 goroutine 上写库**，不影响转发延迟。
- 健康状态按 `(source_kind, source_id, model_id, upstream_model_id)` 唯一键聚合，
  同时区分客户端可见模型与实际发往上游的模型，渠道 / 订阅来源和上游重写路由互不
  覆盖。
- 状态机在持久化事务内统一转移：成功即恢复 `healthy` 并清零连续失败；连续失败达到
  阈值（3 次）标记 `unavailable`，中间态为 `degraded`；MySQL / SQLite / PostgreSQL
  与内存实现共享同一套 `ApplyModelHealthOutcome` 语义。
- 管理端新增模型健康页面：分页 / 关键字 / 来源类型 / 状态过滤，展示成功率、平均
  延迟、最近错误与最近成功 / 失败时间。
- 纯被动记录：不发送探测流量，不参与路由选择与计费。

**影响服务**：`channel-service`（新增存储与 RPC）、`relay-gateway`（终态上报）、
`admin-api`（新增查询端点）、Web 控制台；数据库迁移 `091`。

## 2. Websocket 多轮连接按 usage 增量计费

**根因**：`/v1/responses` Websocket 连接的 relay usage 在全连接生命周期内累计，而每个
终态轮次都拿着累计快照结算，导致第 N 轮被重复收取第 1..N 轮的费用，多轮对话显著
超扣。同时终态事件只按事件名分类：`response.done` 携带 `status="failed"` 被记为成功
并重置连续失败计数；客户端导致的内容策略 / 审核 / 敏感词失败被记为上游故障。

**修复**：

- 维护已计费 usage 基线，每轮只上报增量；即使某轮被跳过基线也会前进，重复的终态
  事件无法抬高下一轮账单。
- 终态分类改为读取 payload 结果而非事件名：`response.done` 失败按失败记录；
  `response.failed` 中客户端策略 / 校验类错误记为中性，与 HTTP 路径的排除规则一致；
  解析出的状态随轮次消费，不泄漏到下一轮。

**影响服务**：`relay-gateway`；历史已结算 ledger 不做原地修改。

## 3. 404 模型不可用判定收窄到 API 形状响应体

**根因**：此前任何上游 404 都被记录为模型不可用。当渠道 `base_url` 误配、上游返回
代理 / HTML 404 页面时，该渠道所有模型被标红，而渠道健康仍是绿色，信号自相矛盾。

**修复**：只有 API 形状的 404（JSON 错误体，或响应体点名了该模型）才证明该路由无法
服务此模型；其余 404 回落到渠道中性分支，不再污染模型级健康。

**影响服务**：`relay-gateway` 错误归因路径。

## 4. Web 控制台布局与导航

**根因**：管理后台导航分组混乱，控制台在不同断点下空间利用失衡，Overview 页面缺少
错误态展示，且布局回归没有自动化防线。

**修复**：

- 重组 admin 工作区导航分组，新增模型健康入口；跨断点重排控制台布局。
- Overview 页面补齐加载失败等错误态；修复布局评审发现的交互问题。
- 新增 Playwright 布局回归用例（10 个浏览器场景），纳入回归基线。

**影响服务**：Web 控制台，无接口变更。

## 5. 对账 runbook 与工程化

- 新增 canonical usage 48 小时验收与历史账本审计两份 runbook，以及
  `scripts/reconcile/historical_ledger_audit.sql` 只读审计 SQL 与 CSV/JSON 格式化脚本。
- 集成共享 `diagnosing-bugs` 调试 skill（`.agents/skills` / `.claude/skills`）。
- 部署文档补充 Lite Quickstart 索引，明确升级会应用模型健康迁移 `091`。

**影响服务**：文档与离线脚本，不改变线上服务行为。

## 兼容性说明

- **API / proto**：additive。`channel.v1` 新增 `RecordModelHealth`、`ListModelHealth`；
  admin 新增模型健康查询端点；既有请求格式与路由不变。
- **数据库**：三方言新增 `091_create_model_health_states`（唯一键
  `uk_model_health_route` + 两个查询索引）。MySQL 生产本次**需要执行迁移**；
  SQLite / PostgreSQL 升级时随 `migrate` 一并应用。旧库无数据回填需求。
- **配置**：无新增配置项；健康监测默认启用且只写观测表。
- **运行时**：渠道路由资格、重试策略、计费决策均不变；健康写路径异步化，
  失败不影响转发。
- **回滚**：代码回滚后 `model_health_states` 表可保留（无运行时依赖）；如需回退
  schema，采用一致的停机备份恢复，本迁移无 down 脚本。

## 升级步骤

```bash
git fetch --tags
git checkout v0.28.0
```

1. 停机备份数据库（MySQL 生产备份 `model_health_states` 所在库即可，新表无历史数据）。
2. 运行 `migrate`，确认 `091` 应用成功、退出码 0。
3. 先更新 `channel-service`，再更新 `relay-gateway` 与 `admin-api`，最后更新 Web；
   避免在资源受限服务器上构建镜像。
4. 升级后打开管理台"模型健康"页面，确认真实流量开始累积记录（空表属正常现象，
   数据随请求产生）。
5. 历史已结算的 Websocket 账单不做追溯调整；如有争议按既有对账流程处理。

## 验证

- 功能分支合入时（`d9c9fa91`）已完成：`make all`、Go unit / race 测试、migration 与
  architecture 检查、Web 172 个单测、lint / build、10 个浏览器回归场景、部署文档
  drift 检查全部通过。
- 模型健康状态机覆盖成功恢复、连续失败阈值、来源 / 模型键隔离与多方言持久化一致性
  测试。
- Websocket 增量计费与终态分类覆盖基线前进、重复终态事件、失败 payload 与客户端
  策略错误中性化用例。
- 404 收窄规则覆盖 JSON 错误体、点名模型的响应体与代理 / HTML 404 页面三类样本。
- 生产已应用迁移 `091` 并完成 `admin-api`、`channel-service`、`relay-gateway` 更新。

## 完整变更日志

- feat: add passive model health monitoring
- refactor(web): reorganize admin workspace navigation
- feat(reconcile): add v0.27 observe audit materials
- docs(v0.27): record observe preparation status
- docs(release): include model health migration
- chore: integrate shared debugging skill
- fix: address model health review findings
- feat(web): rebalance console layout across breakpoints
- fix(relay): bill websocket turns on usage deltas and classify terminal payloads
- fix(biz): narrow 404 model-unavailable rule to API-shaped bodies
- perf(data): record model health off the request goroutine
- fix(channel): harden model-health persistence details
- fix(web): resolve layout review findings and overview error states
- feat: merge model health monitoring into develop
