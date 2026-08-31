# Micro-One-API v0.26.0 发布：用量语义与可审计计费

> 2026-08-31 · 上一版：[v0.25.0](./release-v0.25.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.0)

v0.26.0 是 v0.25.0 之后的 **MINOR 用量语义与计费审计版本**：将上游 reported usage 与规范五桶计费值分开保存，加入 ambiguous 语义隔离和候选成本审计，冻结每笔消费使用的定价快照，并在管理端展示完整的五桶证据链。

本版本包含数据库迁移 `085`–`088`，影响 `relay-gateway`、`billing-service`、`log-service`、`channel-service`、`admin-api` 和 `web/dist`。它不包含完整 Go 1.27 modernization；观察窗口期间仍不得部署或重启 `relay-gateway`。

## 1. 统一五桶用量语义

**根因**：不同 provider / adaptor 对 prompt、cache-read、cache-creation 和 total 的字段语义不同，旧链路可能依赖 channel 类型猜测 prompt 是否包含缓存桶，导致展示值、计费值和原始上报值难以解释。

**修复**：解析层根据实际协议字段判定 verified / estimated / ambiguous 语义，统一输出互斥的 `uncached_input`、`cache_read`、`cache_creation_5m`、`cache_creation_1h`、`output` 五桶；原始 reported 值、字段形状、协议和决策原因独立保留。无法证明语义时保留候选，不用算术关系静默推断。

**影响服务**：`relay-gateway`、`billing-service`、`log-service`、`channel-service`。

## 2. 增加语义隔离与可审计账本字段

**根因**：异常或 ambiguous usage 可能持续进入计费路径；历史账本又没有足够信息区分旧口径和新解析结果，无法安全地自动回溯或冲正。

**修复**：billing ledger 和 usage log 增加 reported / billable totals、五桶、protocol、field shape、parse status、contract version、decision reason 和候选成本字段；channel-service 新增按执行来源、上游模型和 adapter 协议持久化的 `usage_semantic_source_blocks`，连续 ambiguous 来源可被隔离并人工恢复。历史行保持 `legacy`，不根据 token 算术自动回填语义。

**影响服务**：`billing-service`、`log-service`、`channel-service`、`admin-api`。

## 3. 冻结每笔消费的定价证据

**根因**：系统选用的 ModelPrice、倍率和 cache-creation mode 会随配置变化，历史 ledger 只能从金额反推价格，无法证明当时实际使用的单价。

**修复**：新增 `billing_pricing_snapshots` 和 `billing_ledgers.pricing_config_hash`，在同一事务中记录有效输入、输出、cache-read、cache-creation 单价、group ratio 和计费模式；相同 hash 幂等复用。该阶段只增加证据，不改变当前计费金额。

**影响服务**：`billing-service`、`admin-api`、数据库和 Web 用量审计页。

## 4. 增加五桶用量审计界面与部署门禁

**根因**：管理端只能看到聚合 token 数，无法同时核对上游上报值、规范计费值、候选成本和定价快照。

**修复**：管理端用量详情展示五桶 token / 单价 / 成本、reported 与 billable total、语义状态、ambiguous 候选、pricing hash、倍率和 cache-creation mode；兼容部署未接入 snapshot repo 时不写入无法解析的 hash。部署配置补齐 canonical usage producer gate，避免 producer / consumer 不匹配。

**影响服务**：`admin-api`、`web/dist`、Docker Compose 部署配置。

## 兼容性说明

- **API / proto**：新增 usage envelope、ledger、log ingest/response 和 channel 控制面字段均为兼容性新增；旧客户端可继续调用。公共展示可能从单一 total 变为 reported / billable 双口径，legacy 行会明确标记“历史口径”。
- **数据库迁移**：MySQL、PostgreSQL、SQLite 必须按 `085 → 086 → 087 → 088` 顺序执行。`085` / `086` 为 ledger/log 语义列，`087` 为语义隔离表，`088` 为定价快照表和 ledger hash 列。
- **历史数据**：已有行默认 `usage_parse_status=legacy`、语义字段为空，不根据旧 token 数字关系自动回填；历史审计或冲正必须依赖原始 usage、不可变审计证据或供应商账单。
- **计费行为**：`088` 只冻结并记录实际使用的定价证据，不改变既有计费金额；新语义下 billable total 可包含 cache-read / cache-creation 桶，不能继续把 reported total 当作唯一计费总量。
- **灰度配置**：`BILLING_CANONICAL_USAGE_MODE` 支持 `legacy`、`observe`、`charge`；`RELAY_CANONICAL_USAGE_PRODUCER` 默认关闭。启用前必须确认 producer / consumer 版本和告警已就绪。
- **回滚**：`088` 是新增表加新增列，可在停写 / 备份后回滚；`085`–`087` 的列和隔离状态回滚需结合已写入服务版本评估，不应直接删除字段。应用回滚前保留数据库备份并确认旧版本忽略新增字段。
- **Relay 观察**：本版本包含 relay usage parser 代码，但 executor 观察期间不要构建、加载、重建或重启 `relay-gateway`，不要修改 `RELAY_ORCHESTRATOR_ENABLED` 或 allowlist。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.0
```

1. 备份 billing / log / channel 数据库，记录迁移版本、当前计费模式和 producer gate 配置。
2. 在 MySQL、PostgreSQL、SQLite 中按 `085`、`086`、`087`、`088` 顺序执行迁移，分别验证新增列、隔离表和定价快照表。
3. 先部署兼容的 `channel-service`、`billing-service`、`log-service`，再部署 `admin-api` 并同步 `web/dist`；每个服务使用 `docker compose up -d --no-deps <service>`。
4. 先保持 `BILLING_CANONICAL_USAGE_MODE=observe` 和 `RELAY_CANONICAL_USAGE_PRODUCER=false`，核对 verified / estimated / ambiguous 分布、候选成本、隔离来源和 snapshot hash。
5. 确认告警、审计页和账本逐桶成本稳定后，再按变更审批逐步开启 canonical producer / charge；观察期间不要部署 relay。

## 验证

- `go test -race ./internal/server/... ./domain/upstream/provider/... ./app/billing/... ./pkg/usage/...`：验证解析、投影、计费和并发边界。
- billing、log、channel、admin 的单元 / 集成测试和 SQLite lifecycle：验证迁移 `085`–`088`、隔离控制面和定价快照幂等。
- Web lint、测试、`tsc -b` 和生产构建：验证五桶用量审计页、历史口径徽标和定价快照展示。
- `./scripts/check-architecture.sh`、`./scripts/check-deployment-docs.sh` 和 Markdown 链接检查：通过。
- 生产验证保留逐桶成本、ledger amount、reference / dedupe key、parse status 和 pricing hash；不得因 tag 操作重启 `relay-gateway`。

## 完整变更日志

- feat(billing): enforce parser-proven usage semantics and auditable five-bucket billing
- refactor(usage): unify inclusive usage projection behind pkg/usage
- feat(billing): freeze per-request pricing snapshots on consume ledgers (088)
- feat(admin): five-bucket usage audit view with frozen pricing evidence
- docs(design): record usage-semantics phase 2 implementation status
- fix(billing): harden usage audit remediation
- fix(deploy): pass canonical usage producer gate
