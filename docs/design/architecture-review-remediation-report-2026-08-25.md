# 系统架构审查复核、修复方案与执行报告

> 原始材料：`.workbuddy/architecture-review-2026-08-17.md`
> 首次审查日期：2026-08-17
> 复核日期：2026-08-25
> 复核基线：`develop@a1597df`
> 状态：复核与本次修复已完成——v0.22 短期修复和本次配置契约收口均已验证；Relay executor 生产观察仍在进行，不能提前标记架构迁移完成。

## 1. 复核结论

原报告的问题识别总体准确，但原“短期 / 中期 / 长期”路线图不是当前最优实施方案，主要有四个原因：

1. 它把事实审查、修复清单和数月级演进放在同一完成边界，无法判断何时真正完成；
2. 它建议把 relay 编排整体下沉到 `biz`，没有充分区分业务生命周期与 HTTP / SSE / WebSocket 传输职责；
3. 它建议快速收敛 adaptor / provider 双轨，但没有为计费、failover 和流式终止语义设置灰度证据门槛；
4. 报告基于 2026-08-17 的代码，OpenAPI、凭证加密、DSN / JWT、N+1、请求体和前端 strict 等结论在当前基线已经过时。

优化后的原则是：安全与数据正确性立即修；契约与可证明的性能缺陷小步修；高风险执行链按 ADR、feature flag、allowlist、指标观察和独立退场 PR 推进；没有触发证据的结构性整理不提前扩张。

## 2. 三项核心架构结论

### 2.1 Relay 执行边界：诊断成立，原下沉方案需修正

原报告正确指出主流 HTTP 路径中的 `Plan → Reserve → Forward → Commit / Release → Log` 编排长期位于 `internal/server`，会造成入口间重复并提高测试成本。但“把 `orchestrator.go` 整体移入 biz”不是最优解：现有代码同时处理 HTTP header、SSE 生命周期、WebSocket 和响应写回，整体搬迁会反向污染业务层。

采用并继续执行 [Relay 执行边界 ADR](./v0.22-relay-execution-boundary-adr.md)：

- `internal/biz` 只拥有 transport-neutral 的 `Executor`、`Planner`、`QuotaPort`、`Forwarder`、`StreamForwarder` 和 `EventLogger` 契约；
- `internal/server` 保留 body 限额、鉴权头解析、HTTP 状态、SSE flush、WebSocket Upgrade 与连接生命周期；
- server adapter 把现有 usecase、计费、日志与 adaptor 接入 executor；
- Chat Completions、Responses、Anthropic Messages 的 HTTP / SSE 新路径先以 SHA-256 token allowlist 灰度；
- 观察通过前不默认开启、不删除旧 handler、不迁移 `app/relay` 目录。

这比一次性分层搬迁更优，因为它让依赖方向可测试，同时保留一键回滚，并把重复扣费、截断流误 Commit 等高影响风险放进生产证据门槛。

### 2.2 Adaptor / Provider 双轨：诊断成立，使用“迁移期双轨、单请求单路”

长期维护两套分发抽象确实不可接受，但立即删除 `ProviderFactory` 也不安全。当前最优收敛方式是：

- 新 executor 路径只经 adaptor registry 分发；传统 API-key 类型由 registry 的默认 adaptor 在底层复用 `ProviderFactory`；
- 旧 handler 在观察期维持原调用方式；
- 任一请求只能进入新旧路径之一，禁止 shadow 请求造成重复上游调用或计费；
- staging 连续 7 天通过后再扩大 production allowlist；默认开启并再观察一个完整结算周期后，单独删除 legacy 直调。

因此“双轨”当前是有退出条件的迁移机制，而不是最终架构。执行和观察以 [v0.23 路线图](./v0.23-roadmap.md) 与 [executor 观察手册](./v0.23-executor-observation.md) 为事实源。

### 2.3 OpenAPI 契约：原问题成立，当前已关闭

v0.22 已把 config proto 生成和 API OpenAPI 生成拆开，`make api-check` 已进入 CI；`openapi.yaml` 是 ignored 的临时生成物，前端生成类型受漂移检查约束，且已有生产前端模块实际引用生成类型。原报告建议“恢复并跟踪根 openapi.yaml”并非必要，保持可重复生成并在 CI 验证更能避免生成物噪声和空壳覆盖。

## 3. 原问题当前状态

| 原问题 | 当前结论 | 状态 / 证据 |
|--------|----------|-------------|
| 渠道凭证密钥缺失时明文写入 | 高风险判断成立 | ✅ 持久化仓储强制有效 AES key；存量迁移、部署配置和测试已在 v0.22 完成 |
| JWT 缺失随机生成、token hash 固定兜底 | 高风险判断成立，但应在进程入口 fail-fast | ✅ identity 持久化启动要求 JWT；token hash 可回退同一 JWT，测试内存模式保留隔离行为 |
| DSN 缺失静默进入内存仓储 | 高风险判断成立 | ✅ channel / identity / log 仅显式 `*_MEMORY_MODE=true` 才允许内存模式 |
| Redis ping 失败静默降级 | 应按功能语义处理，不能全局 fail-fast | ✅ 允许降级的仓储发结构化 Warn；强一致功能仍由其自身启动 / 请求语义控制 |
| OpenAPI 空壳与 CI 断链 | 成立 | ✅ `make api-check`、CI 漂移校验和前端生成类型接入已完成 |
| TypeScript 未启用 strict | 成立 | ✅ `web/tsconfig.app.json` 已启用 `strict: true` |
| 渠道模型映射 N+1 | 成立 | ✅ 批量查询、`CreateInBatches`、批量更新 / 删除已完成 |
| 禁用渠道只取 1000 条导致漏删 | 成立 | ✅ 反复消费第一页直至排空，并有无进展保护与回归测试 |
| JSON 请求统一 64 MiB 缓冲 | 成立 | ✅ JSON 入口 8 MiB、大载荷入口 64 MiB、超限统一 413 |
| 前端通知手写轮询、无 ErrorBoundary | 成立 | ✅ 已迁 React Query 并增加路由错误兜底 |
| Relay server 层编排与新旧路径 | 成立 | 🟡 transport-neutral executor 与三个 HTTP / SSE 入口已完成；生产 7 天观察进行中 |
| Admin / identity 大文件 | 可维护性问题，不是独立功能缺陷 | ⏸ 触碰对应业务域时一次拆一个域，不做无行为收益的全量搬家 |
| 公共 ptr / pagination / strings 包 | 重复存在，但原方案有过度抽象风险 | ⏸ 仅在一个任务同时修改至少两处且能减少 API 面时提取 |
| 登录限流 / selector 进程内状态 | 多副本风险成立 | ⏸ 以多副本偏差指标或容量计划为触发条件，避免无证据引入 Redis 强依赖 |
| Compose / Dockerfile / relay 目录重排 | 工程一致性问题 | ⏸ 在基础镜像升级或 legacy 路径删除时顺带收敛，不独立制造大 diff |

## 4. 优化后的实施批次

### Batch A：安全与正确性（已完成）

- 渠道凭证加密、存量审计 / 迁移和持久化写入 fail-fast；
- channel / identity / log 显式内存模式；
- identity JWT 启动校验和 Redis 降级告警；
- OpenAPI / 前端类型 CI 契约；
- N+1、批量删除完整性、请求体 413、TypeScript strict 与前端错误兜底。

验收事实源：[v0.22 路线图](./v0.22-roadmap.md)。

### Batch B：配置契约收口（本次）

- relay gRPC 监听地址支持 `RELAY_GRPC_ADDR` 环境覆盖；
- 修正 `session_sticky.enabled: true` 与“默认关闭”注释矛盾，不改变当前运行行为；
- 同步环境变量示例、README 和配置加载回归测试。

完成条件：相关 Go 测试、配置检查、架构边界检查和 Markdown 链接检查通过。

### Batch C：Relay executor 灰度（进行中）

- 代码级端口、HTTP / SSE 三入口、错误矩阵、结算和 failover 测试已完成；
- 2026-08-24 17:30 CST 起进行连续 7 天生产观察；
- 只有成功率、5xx、P95、quota outcome 和 failover 全部门槛通过，才能进入 production 小流量灰度；
- production 灰度通过后才评审默认开启；旧路径删除必须是后续独立变更。

此批次受真实时间窗口约束，本次复核不能把它虚报为完成。

### Batch D：触发式结构治理（未启动）

- admin / identity 按业务域拆文件；
- selector 与登录限流跨副本状态；
- Dockerfile / Compose 收敛；
- relay 目录归位、WebSocket executor 化、legacy 删除。

这些工作只有达到 v0.23 路线图中的触发条件才立项，不纳入当前修复的完成判定。

## 5. 本次验证记录

> 状态：✅ 通过（2026-08-25）。

| 验证 | 结果 |
|------|------|
| `go test ./cmd/relay-gateway ./internal/conf` | ✅ 通过；覆盖真实 `configs/config.yaml` 中 HTTP / gRPC 地址环境变量解析 |
| `./scripts/check-deployment-docs.sh` | ✅ 通过；三套 Compose、Kustomize、41 个 Kubernetes 资源和 62 个资源引用均有效 |
| `python3 scripts/check-markdown-links.py` | ✅ 通过；141 份 Markdown 文档本地链接有效 |
| `./scripts/check-architecture.sh` | ✅ 通过；服务边界、biz / data / service 依赖和 Wire 注入器检查无违规 |
| `git diff --check` | ✅ 通过 |

本次实际修改：

- `configs/config.yaml` 的 gRPC 地址改为 `${RELAY_GRPC_ADDR:-0.0.0.0:9003}`；
- 修正 session sticky 已启用却标注“默认关闭”的注释，不改变运行值；
- `.env.example` 与根 README 增加 `RELAY_GRPC_ADDR`；
- 增加基于真实 relay 配置文件的地址覆盖回归测试；
- 本文进入 `docs/design` 并加入文档索引。

## 6. 最终状态定义

- “本次修复完成”：Batch B 的代码、文档和验证全部通过；
- “v0.22 短期风险关闭”：Batch A 已有发布与测试证据；
- “架构迁移完成”：必须等 Batch C 观察、默认开启观察和 legacy 删除完成，当前不得使用该状态；
- Batch D 不属于当前完成边界。
