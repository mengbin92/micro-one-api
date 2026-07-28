# Micro-One-API v0.11.0 发布：cache_creation 全链路计费 + 模型数据治理 + 路由运营闭环

> 2026-07-28 · 上一版：[v0.10.2](./release-v0.10.2.md)（2026-07-26）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.11.0)

v0.11.0 是继 v0.10.x 模型管理与订阅账户之后的**功能版本**，聚焦四件事：

1. **统一 token 桶语义并补齐 `cache_creation` 全链路**（解析 → 日志 → 账本 → 计费），先观察、后收费；
2. **治理模型规范 ID 与价格数据**：收敛仅大小写不同的重复模型，分离用户售价与上游采购成本；
3. **统一路由可观测性**：订阅账号权重语义、跨来源选择/回退记录、运营视图与告警；
4. **模型配置导入导出**：版本化 JSON 契约，支持 dry-run 与事务化导入。

本版**包含数据库迁移**（`067`–`069`，均为 additive），新增若干 proto 字段与管理端 RPC，**无端点增删、无 API 破坏性变更**。设计依据见 [docs/design/v0.11.0-roadmap.md](../design/v0.11.0-roadmap.md) 与 token 桶语义 ADR（`docs/design/token-usage-semantics.md`）。

## 背景：当前缺口

| 缺口 | 影响 |
|------|------|
| `cache_creation_input_tokens` 未承载 | Anthropic/GLM 兼容上游的缓存创建量丢失，成本与利润可能偏低 |
| token 桶语义未统一 | 账务把 `cache_read` 当作 prompt 子集，直接加字段仍可能重复扣减或漏扣 |
| 公开模型 ID 可出现大小写重复 | 线上已出现 `GLM-5.2` 与 `glm-5.2`，映射、价格和统计分裂 |
| 订阅账号上游成本键不稳定 | 普通渠道和订阅账号无法稳定分账，毛利分析不完整 |
| 统一路由缺少运营视图 | 无法确认配置权重是否生效，难以定位回退和流量倾斜 |

## 变更内容

### Phase 1 — `cache_creation` 采集、存储与计费（P0）

发布策略：**先采集、后核对、再收费**。新字段以观察模式上线，确认生产样本与供应商账单一致后再显式切收费。

**1.1 token 桶语义（ADR + fixture）**

- 新增 `docs/design/token-usage-semantics.md`，定义五个不重叠规范桶：`uncached_input_tokens`、`cache_read_tokens`、`cache_creation_5m_tokens`、`cache_creation_1h_tokens`、`output_tokens`。
- provider/relay/billing 共用的表驱动 fixture，覆盖 OpenAI cached、Anthropic 5m/1h、总量无明细、流式首尾 usage 合并和异常值。同一 fixture 经 raw relay、provider 转换和 billing 后得到相同的五桶结果。
- 兼容规则：`prompt_tokens` 保留既有 API/日志含义；OpenAI cached 是 prompt 子集（`uncached = max(prompt - cached, 0)`），Anthropic 三桶互不重叠（不相减）；非法负数归零，明细之和大于总量时记录异常指标而不改变扣费。

**1.2 解析、DTO/DO/PO 与持久化**

- `internal/server/http_raw_helpers.go` 解析 `cache_creation_input_tokens` 及 `ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens`，非流式与 SSE 流式采用相同合并规则。
- 扩展 `api/log/v1`、`api/billing/v1` proto（`cache_creation_5m_tokens` / `cache_creation_1h_tokens` 字段），`make api` 重新生成。
- `logs`、`billing_ledgers` 至少持久化 5m/1h 两个创建桶；迁移 `067_add_cache_creation_token_usage_fields.sql`（postgres `007` / sqlite `008`）新增列，旧行默认 0，三套 schema 同步。

**1.3 价格与扣费（observe / charge）**

- `ModelPrice` / `UpstreamModelPrice` 增加可选 `cache_creation_5m_price` / `cache_creation_1h_price`；未配置创建价格时保持 v0.10.2 扣费行为并标记"未定价"，不默认套用 input price。
- 新增观察开关 `BILLING_CACHE_CREATION_MODE=observe|charge`，默认 `observe`：写 token 与影子成本，但不改变用户余额。
- 用户费用、上游成本、订阅用量和 reconciliation 使用同一个纯计算函数，避免四处复制价格公式。

### Phase 2 — 模型规范 ID 与成本治理（P0）

**2.1 规范 ID 收敛**

- channel biz 定义唯一入口 `CanonicalModelID = strings.ToLower(strings.TrimSpace(id))`，create/update/alias/自动发现/导入均调用。
- 新增只读 preflight（`CanonicalModelPreflight` RPC / `/api/admin/models/canonical/preflight`）列出重复模型及其 alias、mapping、usage stats、价格引用；事务化 `MergeCanonicalModels` 重指向所有外键/统计后再删除重复行，冲突时终止并输出报告，不做静默覆盖。
- 数据库大小写不敏感规范唯一约束：迁移 `068_add_canonical_model_id_constraint.sql`（MySQL 函数索引 `LOWER(TRIM(model_id))`，postgres `005` / sqlite `006`）。
- 保留精确上游名称到 `upstream_model_id`，公开 `model_id` 不承载供应商前缀或大小写需求。

**2.2 用户售价与上游成本分离**

- 固定成本键 `channel:<id>:<upstream_model_id>` 与 `subscription:<id>:<upstream_model_id>`，relay 通过 `CommitQuotaRequest.upstream_model_id` / `source_kind` 透传，由 `canonicalUpstreamPriceKey` 落地。
- 新增未定价审计 RPC `ListUnpricedRoutedModels`（`/api/admin/models/unpriced`）：列出已路由但无价格配置的活跃模型，未定价不阻断保存，但有醒目状态与审计事件。

### Phase 3 — 统一路由可观测性（P1）

**3.1 订阅账号权重语义**

- `priority` 只用于分层，新增明确订阅账号 `weight` 字段贯穿 DTO/DO/PO（迁移 `069_add_subscription_account_weight.sql`）。`accountSelectorWeight` 优先 `weight`、回退 `priority`、再回退 1，跨来源选择不再把订阅账号权重硬编码为 1。
- `Acquire/Release` 明确保留为**预留 seam**：生产 relay 不调用，`loadFactor` 保持中性，文档修正不再宣称已生效的 load-aware 能力。

**3.3 确定性分布测试**

- 新增 2 个普通渠道 + 2 个订阅账号的确定性分布测试（`cross_source_distribution_test.go`），覆盖不同优先级、权重、健康降权、并发饱和和来源失败，先证明选择行为再制作运营视图。

**3.4–3.5 选择记录与指标**

- 在选择与执行边界记录 `SelectionEvent`（候选来源、最终来源、粘性命中、优先级层、回退原因、执行结果、耗时），由 `MetricsSelectionRecorder` 接入 `relay-gateway/wire.go`。
- Prometheus 低基数指标（`platform/metrics/routing.go` / `routing_rates.go`）：只用 `source_kind`、`result`、`reason`、`provider_family` 等受控 label；channel/account/model 明细放结构化日志或 trace。

**3.6–3.8 运营视图、告警与基线**

- 管理端运营视图 `/api/admin/routing-ops`（前端 `RoutingOpsPage`）：普通渠道/订阅账号流量占比、回退率、错误率、缓存读写 token、用户收入、上游成本与毛利。
- 告警规则：`unpriced_traffic`（未定价流量）、`upstream_cost_missing`（上游成本缺失）、`source_skew`（来源倾斜）、`negative_margin`（负毛利）。
- 性能基线记录于 `docs/design/BASELINE.md`，确认新增统计未使 relay P95/P99 或 billing 提交耗时明显回退。

### Phase 4 — 模型配置导入导出（P2）

- 版本化 JSON schema（v1.0.0），覆盖模型、别名、渠道映射、订阅映射；价格默认不随模型导出，除非显式选择并通过独立权限校验。
- 导入支持 `dry_run`（`DryRunImportModels` RPC），返回新增/更新/跳过/冲突/错误明细；正式导入（`ImportModels`）采用事务，任一非法记录整批回滚。冲突策略默认 `reject`，不得依记录顺序隐式覆盖。
- 设置文件大小与记录数上限，校验 canonical model ID、外键目标、重复 alias 与敏感字段；导出不含渠道 API key、OAuth token 等密钥。
- 管理端端点 `/api/admin/models/export`、`/api/admin/models/import`；前端对话框 `web/src/components/admin/ModelExchangeDialog.tsx`。写入审计事件（操作者、request ID、schema 版本、记录数、content hash、结果）。

### 代码评审加固

- `fa62e1c` 完成 v0.11.0 代码评审的 7 项 CRITICAL/HIGH 修复（billing/admin/relay），`d7eacbc` 进一步硬化路由与模型导入导出边界。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.11.0

# 开发者环境：重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：应用迁移 + 重建镜像 + 滚动重启
make migrate
docker compose build
docker compose up -d
```

**注意事项：**

- **必须执行数据库迁移**：`067`（cache_creation 字段）、`068`（canonical 唯一约束）、`069`（subscription_accounts.weight）均为 additive，旧行默认值，幂等可重入。升级前建议备份 `models`、`model_channel_mapping`、`model_subscription_mapping`、`subscription_accounts`、`logs`、`billing_ledgers`。
- **canonical 唯一约束（068）可能因数据冲突而失败**：若存在仅大小写不同的重复 `model_id`，`CREATE UNIQUE INDEX` 会中止。**务必先运行 preflight**（`/api/admin/models/canonical/preflight`），按报告用 `MergeCanonicalModels` 合并重复项后再应用 068。068 的 SQL 本身在 `UPDATE` 归一化阶段就会因重复键报错，不会静默覆盖数据。
- **cache_creation 默认 observe**：`BILLING_CACHE_CREATION_MODE` 默认 `observe`，新字段只采集与记录影子成本，不改变用户扣费。生产观察至少一个完整结算周期、核对供应商账单后再切 `charge`；切回 `observe` 即可回滚，新增列保留。
- **路由权重语义**：升级后订阅账号的 `weight` 默认 0（回退到 `priority` 派生），行为与 v0.10.2 一致；配置了 `weight` 后才参与层内加权。
- **前端**：构建 `cd web && npm run build`；如使用挂载卷部署，按 AGENTS.md 前端流程单独 scp `web/dist`（新增 RoutingOpsPage 与 ModelExchangeDialog 页面）。

## 兼容性说明

- **API**：无破坏性变更；新增 proto 字段（`cache_creation_*`、`weight`、`upstream_model_id`、`source_kind` 等）与若干管理端 RPC（preflight/merge/unpriced/export/import/dry-run），旧客户端无感知。
- **数据库**：有迁移（`067`–`069`），新增列均为 `NOT NULL DEFAULT 0` / 函数索引，additive 兼容滚动升级与回滚。
- **配置**：新增可选 `BILLING_CACHE_CREATION_MODE`（默认 `observe`）。
- **运行时**：默认行为与 v0.10.2 一致；cache_creation 在 observe 模式不扣费，weight 默认回退 priority。

## 验证

发布前已确认：

- `go build` / `go vet` / `go test` 全部通过
- token 桶跨层 fixture：raw relay → provider 转换 → billing 结果一致（OpenAI cached、Anthropic 5m/1h、无明细、流式合并、异常值）
- `internal/biz/selection_recorder_test.go`、`response_scheduler_test.go`、`selection_finalize_test.go` 覆盖选择/执行/回退记录
- `app/channel/internal/biz` 确定性分布测试（`cross_source_distribution_test.go`）覆盖跨来源加权/失败回退
- `app/billing/internal/biz` cache-creation 计价与缺省、upstream cost key 单测
- `app/channel/internal/data` model_exchange 事务导入与 dry-run、preflight 单测
- `platform/metrics/routing_rates_test.go` 覆盖低基数指标
- `web/src/lib/model-exchange.test.ts` 覆盖导入导出 schema 校验；npm build / test / lint 全部通过

## 完整变更日志

- cd92ff2 docs: plan v0.11.0 development roadmap
- e8bd24e docs(design): v0.11.0 Phase 0 token usage semantics ADR + cross-layer fixture
- eb90aa7 feat(relay): v0.11.0 Phase 1 §1.1 cache_creation parsing and metrics
- 0d739a9 feat(billing,log): v0.11.0 Phase 1 §1.2 persist cache_creation buckets
- 88735b5 feat(billing): v0.11.0 Phase 1 §1.3 cache_creation pricing with observe/charge
- 5e1ef8e feat(channel): v0.11.0 Phase 2 §2.1 canonical model ID governance
- 0f2f350 feat(billing,admin): v0.11.0 Phase 2 §2.2 upstream cost governance
- 17313b2 feat(channel): v0.11.0 Phase 3 §3.1+§3.2 selector semantics + load-aware inertness
- f5ef12a test(channel): v0.11.0 Phase 3 §3.3 deterministic distribution tests
- b2bcb96 feat(relay): v0.11.0 Phase 3 §3.1 cross-source weight + §3.4 selection records
- 2f4ffac feat(metrics,relay): v0.11.0 Phase 3 §3.5 routing observability metrics
- faa8e95 feat(admin,observability): v0.11.0 Phase 3 §3.6-§3.8 ops view, alerts, baseline
- 2eeae79 feat(phase3-4): complete routing observability wiring and model import/export
- fa62e1c fix(billing,admin,relay): v0.11.0 code review — 7 CRITICAL/HIGH fixes
- d7eacbc fix: harden v0.11 routing and model exchange

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
