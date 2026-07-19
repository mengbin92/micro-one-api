# Micro-One-API v0.9.0 发布：异步架构演进与生产级可用性提升

> 2026-07-19 · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.9.0)

v0.9.0 是 `micro-one-api` 项目的一个重要里程碑版本。本版落地了架构重构路线图的 6 个关键阶段（Phase 2.1–2.5 与 Phase 3.3），把 relay-gateway 提交链路上的两个数据库热点改为异步批量处理、补齐 WebSocket 优雅下线、并通过 per-service schema 隔离 + 配置热更新为水平扩展铺路。

**关键特点**：所有新增能力默认关闭或保持旧行为，升级即生效，无需改环境变量或迁移。API 无破坏性变更，没有新增编号业务表迁移。

## 核心亮点

### 1. 异步 Billing —— 解耦网关主路径

在之前的架构中，每次 API 调用后的 quota commit 都会阻塞 relay-gateway 的响应。v0.9.0 通过引入异步 billing 能力，让结算操作入队后台 worker 执行：

```yaml
# billing-service 配置
billing:
  async:
    enabled: true  # 开启异步结算
```

开启后，`CommitQuota` 不再阻塞主路径，立即返回带 `async_enqueued=true` 的临时结果。后台 worker 执行权威的预留态机 + 钱包扣减 + 账本 + 订阅用量流水线。关闭时自动退回同步路径，零行为变更。

**收益**：在高并发场景下，网关响应不再受数据库写入延迟影响，P99 延迟显著降低。

### 2. 批量日志写入 —— 降低数据库压力

原本每条 API 日志都是一次同步 `INSERT`，在生产高频调用下会产生大量数据库连接和写入压力。v0.9.0 引入 `BatchLogWriter`：

```yaml
# log-service 配置
log:
  batch_enabled: true  # 开启批量写入
  batch_flush_interval: 1s  # flush 间隔
```

批量模式下，日志先进入内存队列，定期批量 flush 到数据库。队列满或 writer 关闭时自动降级为同步写入，保证不丢日志。

**收益**：数据库写入次数减少 90%+，连接池压力大幅降低，日志写入失败不影响 API 响应。

### 3. WebSocket 优雅下线 —— 零停机部署

对于 Codex 等 WebSocket 场景，直接关闭服务会导致正在进行的流式连接中断。v0.9.0 实现了优雅排空机制：

- SIGTERM 时先返回 503 + Retry-After，拒绝新升级
- 等待既有连接 drain（默认 30s）后再强制关闭
- `/healthz` 在 draining 期间返回 503，让负载均衡器摘流

```yaml
# relay-gateway 配置
openai_ws:
  drain_timeout: 30s  # 排空窗口
```

**收益**：支持滚动更新和零停机部署，正在进行的流式调用不会被强制中断。

### 4. Per-Service Schema 隔离 —— 为水平扩展铺路

v0.9.0 引入可选的 per-service DB schema 隔离能力，让每个微服务拥有独立的数据库命名空间：

```bash
# 环境变量方式启用（按服务逐个切换）
BILLING_SCHEMA=oneapi_billing
LOG_SCHEMA=oneapi_log
CHANNEL_SCHEMA=oneapi_channel
# ... 其他服务
```

- MySQL：通过 DSN DBName 重写实现
- PostgreSQL：通过 `search_path` 选项注入
- SQLite：忽略（= 换文件）

配合 `migrations/ownership.yaml`，`cmd/migrate` 支持按服务所有权在对应 schema 上只运行相关迁移。

**收益**：为未来水平扩展、跨机房部署、多租户隔离打下基础。

### 5. 配置热更新 —— 无需重启

新增 fsnotify-based 配置热更新，支持在运行时重载配置文件：

- `configs/models.yaml` 变化时自动热替换 `ModelMapper`
- 编辑器原子保存（Rename/Remove）自动 re-add
- 解析失败保留旧快照并告警

**收益**：模型映射变更不需要重启服务，提升运维灵活性。

### 6. 加权选择器端到端验证

新增 `GET /api/v1/admin/channels/selector/stats` 端点，暴露 WeightedSelector 运行态快照：

- 每渠道权重与 `CurrentWeight`
- inflight 请求数、p95 延迟、错误率
- 熔断器状态

集成测试 `TestRelaySelectChannel_WeightedDistribution` 端到端验证高权重渠道严格多选中、延迟反馈环闭环。

**收益**：提供生产可观测性，方便调试和优化渠道路由策略。

## 架构演进路线图

v0.9.0 落地的 Phase 2.1–2.5 与 Phase 3.3 是架构重构的关键里程碑：

| 阶段 | 内容 | 状态 |
|------|------|------|
| Phase 2.1 | 异步 billing 入队 | ✅ |
| Phase 2.2 | 加权选择器验证 | ✅ |
| Phase 2.3 | 批量日志写入 | ✅ |
| Phase 2.4 | Per-service schema 隔离 | ✅ |
| Phase 2.5 | 配置热更新 | ✅ |
| Phase 3.3 | WebSocket 优雅下线 | ✅ |

完整的架构重构路线图见 [ARCHITECTURE_REFACTOR.md](../design/ARCHITECTURE_REFACTOR.md)。

## 安全与稳定性改进

本版包含多项安全性和稳定性修复：

### Schema 注入防护

`xdb.withPostgresSearchPath` / `withMySQLDBName` 在拼进 DSN 前按正则表达式校验 schema 标识符，防止 SQL 注入：

```
^[A-Za-z_][A-Za-z0-9_]{0,62}$
```

攻击者设置 `DATABASE_SCHEMA='public -c statement_timeout=1'` 会被拒绝。

### Shutdown Drain 正确性

- `AsyncBillingUsecase.Close()` 在取消 worker 前设置关闭标志，让并发 `Settle` 回退同步路径
- `BatchLogWriter.Stop()` drain 队列，shutdown 期间不丢日志
- `config.EnvFileSource` 终止广播 goroutine，防止资源泄漏

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.9.0

# 检查并替换 deployments/docker-compose/.env 中的生产密钥
cd deployments/docker-compose
docker compose --env-file .env config --quiet

# 旧数据卷升级前先备份；全新环境直接启动
docker compose --env-file .env up -d --build
```

### 可选启用新能力

按需启用以下环境变量（不设置时保持旧行为）：

```bash
# 异步 billing（billing-service）
BILLING_ASYNC_ENABLED=true

# 批量日志写入（log-service）
LOG_BATCH_ENABLED=true

# per-service schema 隔离（按服务逐个切换）
# BILLING_SCHEMA=oneapi_billing
# LOG_SCHEMA=oneapi_log

# WebSocket drain 窗口
OPENAI_WS_DRAIN_TIMEOUT=30s
```

**注意**：Phase 2.4 schema 隔离启用前必须先完成 `docs/TODO.md` 的 R1–R6 前置修复。

## 适合哪些场景

v0.9.0 特别适合以下团队：

- **高频 API 调用场景**：异步 billing + 批量日志大幅降低数据库压力
- **需要零停机部署**：WebSocket 优雅下线支持滚动更新
- **计划水平扩展**：per-service schema 隔离为多实例部署铺路
- **频繁调整模型映射**：配置热更新减少重启次数
- **需要生产级可观测性**：selector stats 端点支持渠道路由调试

## 兼容性说明

- **API**：无破坏性变更。`CommitQuotaResponse` 新增 `async_enqueued` 字段，老客户端会忽略未知字段
- **数据库**：没有新增编号业务表迁移。schema 隔离相关的 `schema_split.sql` 和 `ownership.yaml` 是可选运维工件
- **配置**：所有新能力默认关闭，升级即生效，无需修改环境变量

## 验证与测试

发布前已执行：

```bash
go build ./...                    # 通过
go vet ./...                      # 通过
./scripts/check-architecture.sh   # 通过（exit 0）
```

各 feat/fix 提交内附带的针对性测试（xdb schema 校验、async billing shutdown drain、batch log writer、weighted distribution 集成测试、WebSocket drain 503 flip、config hot reload 等）均已在各自 commit 中验证通过。

## 完整变更日志

- 9911671 feat(billing): wire async billing into commit hot path (Phase 2.1)
- feb7d79 feat(log): wire batch log writer into LogUsecase (Phase 2.3)
- 3acff1f feat(relay): wire graceful WebSocket drain into openai_ws_* (Phase 3.3)
- cdcd165 feat(channel): verify weighted selector end-to-end (Phase 2.2)
- 3571d5d feat(phase2): add per-service DB schema isolation and config hot reload (Phase 2.4 / 2.5)
- b88ae3d fix(security): correct gitleaks ignore fingerprint path for renamed doc
- eb6a92e fix: address review issues from recent Phase 2/3 commits
- 7a81294 fix(review): address Phase 2/3 review findings (security, drain, data-loss)
- 8cd6b57 docs(todo): register Phase 2.4 schema isolation prod-enablement risks and plan
- 06bd5a3 feat(conf): migrate all 9 services to proto-based configuration
- da97e45 feat(channel): migrate config to proto definition (conf.proto)

## 项目简介

`micro-one-api` 是一个基于 Go Kratos 的多服务 AI API 网关与管理系统。它参考了 one-api 的多渠道 OpenAI API 分发思路，也借鉴了 sub2api 在订阅额度窗口、账号池、限流和用量管理上的场景经验，将用户鉴权、渠道管理、钱包账务、日志监控和管理后台拆分成清晰的微服务。

如果你正在维护多个上游模型渠道，希望统一 API 入口、统一用户 Token、统一钱包余额和用量记录，并且希望系统后续具备更强的可维护性与扩展性，这个项目可以作为一个参考实现。

## 下一步

后续版本计划包括：

- 完善渠道健康检查和自动熔断
- 强化用量统计、成本分析和对账能力
- 增加更细粒度的用户、团队、分组和模型权限
- 完善前端运营体验和可观测性面板
- 加强生产部署文档、安全基线和高可用方案

欢迎关注、试用和参与改进：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
