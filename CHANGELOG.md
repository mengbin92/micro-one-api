# Changelog

All notable changes to `micro-one-api` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.26.4] - 2026-09-01

v0.26.4 是 v0.26.3 之后的 **PATCH 控制台性能修复版本**：消除 `/dashboard`、`/usage`、`/admin/logs` 等页面约 1 秒的打开延时——哈希静态资源改为一年 immutable 缓存并启用 gzip，charts 依赖隔离到图表页按需加载，导航悬停预取路由模块，账户查询跨组件复用并修复跨账号缓存与旧角色闪现问题；新增迁移 089（Dashboard 聚合联合索引）。无公共 API / proto 变更。详见 [release-v0.26.4.md](docs/releases/release-v0.26.4.md)。

### Added

- 迁移 089：`billing_ledgers` 新增 `(user_id, type, created_at)` 联合索引，加速用户 Dashboard consume 聚合（MySQL / PostgreSQL / SQLite）。

### Fixed

- 修复控制台页面每次打开约 1 秒的延时：懒加载页面先下载 JS 才发 API、charts 公共包被非图表页面预加载、哈希资源无长期缓存且无压缩、导航与页面重复请求账户接口。
- 修复 `AdminRoute` 刷新时信任 `localStorage.userRole`，可能短暂放行旧角色进入管理页面的问题。
- 修复登录/退出后 React Query 缓存残留，可能短暂显示上一用户数据的问题。

### Changed

- `/assets/*` 返回 `public, max-age=31536000, immutable`，文本资源按 `Accept-Encoding` 协商 gzip（尊重 `q=0` 与 `Range`，WOFF2 不压缩，404 不加长缓存，空文件输出合法空 gzip 流）；HTML 保持禁缓存。
- 侧边栏链接悬停/聚焦时预取目标路由模块；导航栏、Dashboard、个人资料、充值、兑换共用账户查询缓存（用户信息 5 分钟、账户概览 30 秒）。

## [0.26.3] - 2026-09-01

v0.26.3 是替代未完成发布的 v0.26.2 的 **PATCH Relay 路由、双语界面与发布门禁修复版本**：完整包含 v0.26.2 的模型路由和界面修复，并同步生成 API 类型、兼容中英文 Playwright 定位器，使 Release E2E 门禁可通过。无公共 API / proto 变更、无数据库迁移。详见 [release-v0.26.3.md](docs/releases/release-v0.26.3.md)。

### Fixed

- 修复模型路由大小写不一致、客户端 `[1M]` 标记泄漏，以及渠道显式 `model_mapping` 被 `upstream_model_id` 覆盖的问题。
- 修复 Web 中英文切换不完整、动态文案粘连及法律页面未翻译的问题。
- 修复生成 API 类型未同步导致的 CI 一致性失败，以及本地化无障碍名称导致的 Release Playwright 冒烟测试失败。

### Changed

- v0.26.3 替代未产出完整 GitHub Release 和多架构镜像的 v0.26.2；升级和部署应直接使用 v0.26.3。
- 管理端 Playwright 定位器同时匹配中英文可访问名称，Web API 类型包含完整 canonical usage 字段。

## [0.26.2] - 2026-08-31

v0.26.2 是 v0.26.1 之后的 **PATCH Relay 路由与 Web 界面修复版本**：统一模型标识处理，恢复显式渠道模型映射优先级，并同步中英文界面文案。无公共 API / proto 变更、无数据库迁移。详见 [release-v0.26.2.md](docs/releases/release-v0.26.2.md)。

### Fixed

- 修复模型路由大小写不一致和客户端 `[1M]` 标记泄漏到路由、计费及上游请求的问题。
- 修复渠道显式 `model_mapping` 被 `upstream_model_id` 覆盖的问题。
- 修复 Web 管理端和用户端中英文切换不完整、动态文案粘连及法律页面未翻译的问题。

### Changed

- 模型精确映射、白名单、能力检查、重试和 sticky 路由统一使用大小写不敏感的标识匹配。
- 前端共享翻译目录增加占位符插值和完整英文法律文案。

## [0.26.1] - 2026-08-31

v0.26.1 是 v0.26.0 之后的 **PATCH 工具链与仓库现代化版本**：统一 Go 1.27 工具链与代码风格，升级兼容依赖，修复 middleware 测试 goroutine 清理，并携带最新 executor observation 文档。无公共 API / proto 变更、无数据库迁移。详见 [release-v0.26.1.md](docs/releases/release-v0.26.1.md)。

### Fixed

- 修复 middleware idempotency 测试的 goroutine 清理路径。

### Changed

- 所有服务、Dockerfile、CI 和 Go module 统一使用 Go 1.27，采用 `any`、整数 range、`min` / `max` 和集合辅助函数。
- 更新运行时 / 依赖文档及 executor observation 的最新窗口记录。

## [0.26.0] - 2026-08-31

v0.26.0 是 v0.25.0 之后的 **MINOR 用量语义与计费审计版本**：分离 reported usage 与规范五桶计费值，增加 ambiguous 语义隔离、逐笔定价快照和管理端五桶审计展示，包含数据库迁移 `085`–`088`。详见 [release-v0.26.0.md](docs/releases/release-v0.26.0.md)。

### Added

- 新增五桶 usage envelope、reported / billable totals、解析状态、字段形状、候选成本和 usage semantic source blocks。
- 新增 `billing_pricing_snapshots` 定价证据表与 `pricing_config_hash`，并在管理端展示逐桶 token、单价、成本和快照信息。
- 新增 MySQL、PostgreSQL、SQLite 的 usage 语义与定价快照迁移 `085`–`088`。

### Fixed

- 修复通过 channel 类型猜测 prompt/cache 语义的问题，ambiguous usage 不再被算术关系静默解释。
- 修复 producer gate 配置不一致导致 canonical usage 证据链不完整的问题。

### Changed

- 新增 `legacy` / `observe` / `charge` canonical usage 模式和历史行“历史口径”展示；`088` 只增加定价证据，不改变当前计费金额。

## [0.25.0] - 2026-08-31

v0.25.0 是 v0.24.0 之后的 **MINOR 模型能力与定价注册版本**：增加模型输入 / 输出模态与 cache-read 定价，统一注册表价格为每 1M tokens，并补齐 `083`、`084` 数据库迁移和旧 MySQL 默认值兼容。详见 [release-v0.25.0.md](docs/releases/release-v0.25.0.md)。

### Added

- channel API、模型管理和导入导出新增输入 / 输出模态与 cache-read 定价字段。
- 新增 MySQL、PostgreSQL、SQLite 的模型模态和定价迁移 `083`、`084`。

### Fixed

- 将历史模型输入 / 输出价格从每 1K tokens 正确转换为每 1M tokens。
- 修复旧 MySQL 默认值兼容和 Web 上游成本 key 格式展示。

### Changed

- Web 模型管理、模态图标、用户定价页和上游成本页面同步新的注册表字段。

## [0.24.0] - 2026-08-31

v0.24.0 是 v0.23.3 之后的 **MINOR Web 体验与运营基础版本**：完成用户端与管理端的
可访问性设计重构，增加中英文界面、中国用户协议与隐私政策、注册显式同意和
可配置的运营主体信息。无数据库迁移、无公共 API / proto 破坏性变更，详见
[release-v0.24.0.md](docs/releases/release-v0.24.0.md)。

### Added

- 新增持久化中英文切换，用户端与管理端文案、日期和数字格式跟随 locale。
- 新增公开用户协议、隐私政策和注册显式同意；`admin-api` 支持配置并公示运营者名称、
  注册地址与隐私联系邮箱。
- 重建 Web 设计令牌、响应式导航、状态组件、登录 / Playground 流程和语义化图表。

### Fixed

- 英文注册页的协议同意文案、法律链接、校验错误和运营主体配置保持英文。

### Changed

- 刷新 Micro-One API 图标、字标、本地 Noto Sans SC 字体资产与授权记录。
- Anthropic native SSE 测试覆盖 `Edit` 工具跨多个 `input_json_delta` 的原样传递；
  无 relay 运行时变更。

## [0.23.3] - 2026-08-31

v0.23.3 是 v0.23.2 之后的 **PATCH 安全修复版本**：收紧服务间认证、上游网络访问、登录限流和 relay 编排器灰度凭证边界，并修复 CodeQL 报告的凭证配置风险。无数据库迁移、无公共 API / proto 破坏性变更，但升级前必须统一配置 `SERVICE_TOKEN` 并重新计算编排器 HMAC allowlist。详见 [release-v0.23.3.md](docs/releases/release-v0.23.3.md)。

### Fixed

- 内部 gRPC 服务统一校验 `SERVICE_TOKEN`，identity-service 增加跨副本 Redis 登录失败限流，入口代理地址改为按可信 CIDR 解析。
- 上游 provider 使用 SSRF 安全 transport，阻断私有 / 保留地址、代理绕过和不安全重定向。
- relay 编排器 allowlist 改为使用 `SERVICE_TOKEN` 作为密钥的 HMAC-SHA256 摘要，旧的普通 SHA-256 配置不再读取。
- Grafana 默认管理员密码改为显式必填，补齐部署清单中的内部令牌和代理边界配置，修复 CodeQL 凭证告警。

### Changed

- 内部部署与运维文档更新服务令牌、可信代理 CIDR、HMAC allowlist 和 fail-closed 运行约束。

## [0.24.0] - 2026-09-04

v0.24.0 是 v0.23.2 之后的 **MINOR Web 体验与运营基础版本**：完成用户端与管理端的
可访问性设计重构，增加中英文界面、中国用户协议与隐私政策、注册显式同意和
可配置的运营主体信息。无数据库迁移、无公共 API / proto 破坏性变更，详见
[release-v0.24.0.md](docs/releases/release-v0.24.0.md)。

### Added

- 新增持久化中英文切换，用户端与管理端文案、日期和数字格式跟随 locale。
- 新增公开用户协议、隐私政策和注册显式同意；`admin-api` 支持配置并公示运营者名称、
  注册地址与隐私联系邮箱。
- 重建 Web 设计令牌、响应式导航、状态组件、登录 / Playground 流程和语义化图表。

### Fixed

- 英文注册页的协议同意文案、法律链接、校验错误和运营主体配置保持英文。

### Changed

- 刷新 Micro-One API 图标、字标、本地 Noto Sans SC 字体资产与授权记录。
- Anthropic native SSE 测试覆盖 `Edit` 工具跨多个 `input_json_delta` 的原样传递；
  无 relay 运行时变更。

## [0.23.2] - 2026-08-27

v0.23.2 是 v0.23.1 之后的 **PATCH 协议兼容与灰度可靠性版本**：修复 Responses 经 Anthropic
API-key 渠道时的本地 502，补齐流式 executor 观察和失败边界，并发布受控 Relay Playground。
无数据库迁移、无公共 API / proto 破坏性变更。详见
[release-v0.23.2.md](docs/releases/release-v0.23.2.md)。

### Added

- staged executor 覆盖流式 Responses、Anthropic Messages 和 Chat Completions，补齐终态感知的
  quota 结算、retry / failover 与按执行路径分组的 Prometheus 指标。
- 新增内存态凭证、模型发现、SSE 解析、取消和请求检查能力的 Relay Playground。

### Fixed

- Anthropic API-key adaptor 支持 Responses 请求、非流式响应和 SSE 双向转换，不再在请求发往
  StepFun 前以 unsupported-format 本地失败并映射为 502。
- post-forward 失败不再触发重复上游调用；协议能力 retry 限定到 Responses；流消费者取消可终止
  chunk 投递；生产 CORS 默认 fail closed。
- 修复 Playground 上线后的重复 CORS 响应头、raw-request 路由与客户端错误映射问题。

### Changed

- relay gRPC 地址可通过可选 `RELAY_GRPC_ADDR` 覆盖；未配置时保持原默认值。
- 新增 executor 7 天观察手册，记录生产回滚、根因、修复 canary 与新旧路径判定口径。

## [0.23.1] - 2026-08-24

v0.23.1 是 v0.23.0 之后的 **PATCH 可靠性与安全修复版本**：隔离单模型上游故障，避免 retry 放大渠道级熔断；阻断请求凭证进入用量日志；并校验 `/models` 健康探测响应。无数据库迁移、无公共 API / proto 破坏性变更。详见 [release-v0.23.1.md](docs/releases/release-v0.23.1.md)。

### Fixed

- 同一请求同一来源的 retry 健康结果合并结算；模型无健康节点不再污染渠道级熔断，也不再重复减少选择器 `inflight`。
- transport-neutral executor 的用量日志仅接收安全请求元数据，敏感请求 headers、body 和 bearer token 不再进入日志边界（CodeQL #277）。
- monitor-worker 拒绝 HTML 200、缺字段或错误字段类型的 `/models` 响应，记录为 `invalid_response`。

## [0.23.0] - 2026-08-24

v0.23.0 是 v0.22.5 之后的 **MINOR 执行边界与灰度能力版本**：新增默认关闭、token allowlist 保护的 Chat Completions 非流式 executor staging 路径，补齐 transport-neutral 端口、adaptor registry、failover 结算和失败矩阵，并修复成本图表标签重叠。无数据库迁移、无公共 API / proto 破坏性变更。详见 [release-v0.23.0.md](docs/releases/release-v0.23.0.md)。

### Added

- 新增 relay executor transport-neutral 端口、adaptor registry 和按 SHA-256 bearer token allowlist 控制的 staging 路径。
- 新增失败候选 Release、成功候选单次 Commit / Log、重试 request ID 隔离及默认关闭 / 一键回滚测试。

### Fixed

- 修复上游 executor 错误体直接透传和 failover 结算边界问题。
- 修复管理后台成本图表标签重叠。

### Changed

- 新增 `RELAY_ORCHESTRATOR_ENABLED`、`RELAY_ORCHESTRATOR_TOKEN_SHA256` 配置，默认关闭且不保存原始 token。

## [0.22.5] - 2026-08-22

v0.22.5 是 v0.22.4 的 **PATCH 账务正确性与安全修复版本**：分离支付与执行核算口径，补齐订阅流量上游成本，为 channel 用量与 usage log 写入增加事务级幂等键，并修复 gosec / gitleaks 扫描阻塞项。包含向后兼容迁移 `080`-`082`。详见 [release-v0.22.5.md](docs/releases/release-v0.22.5.md)。

### Fixed

- 订阅账本不再与钱包余额直接比较；上游成本按请求固化，渠道对账同时核对用量与成本。
- channel 用量以 billing reservation 为幂等键，claim 与计数更新同事务提交，重试不再重复累加。
- usage log 以 `user_id + request_id` 为幂等键，claim 与日志写入同事务提交，历史 consume 日志已回填 claim。
- gosec G705 误报的 SSE passthrough 获得精确 suppression；gitleaks 命中的测试合成密钥改为显式占位符并保留历史 fingerprint。

## [0.22.4] - 2026-08-22

v0.22.4 是 v0.22.3 的 **PATCH 生产修复版本**：修复订阅零余额误扣、Anthropic 工具调用丢失、Responses 回退缓存 Token 丢失，并将订阅续费入口改为日期时间选择器。包含一次向后兼容的数据库迁移 `079`（新增 `balance_amount` 并双写兼容旧列），RPC 新增 `balance_amount` 字段并保留旧字段别名。详见 [release-v0.22.4.md](docs/releases/release-v0.22.4.md)。

### Fixed

- 订阅覆盖金额不再经 float64 USD 向下取整，零余额订阅用户不再被误判为余额不足；无限订阅显式表达为无上限。
- `/v1/messages` 经 adaptor 管线保留 OpenAI 兼容渠道的工具调用增量，Anthropic 工具调用不再被流式桥接丢弃。
- Responses 转 Chat 回退保留 prompt 缓存明细并合并流式 usage，Codex 用量日志不再把缓存命中记录为 0。
- 管理后台订阅续费由原始 Unix 时间戳 `prompt` 改为原生日期时间选择器。

## [0.22.3] - 2026-08-21

v0.22.3 是 v0.22.2 的 **PATCH 生产修复版本**：修复 billing 价格键大小写不一致、CC Switch / Claude 模型探测与流式兼容，以及上游敏感词策略拒绝误触发渠道熔断的问题。详见
[release-v0.22.3.md](docs/releases/release-v0.22.3.md)。

### Fixed

- billing 价格、倍率和三级上游价格键统一大小写归一化，避免静默回退默认倍率。
- CC Switch / Claude 模型鉴权、模型可见性、Token 导入和 SSE 流式响应兼容性。
- `sensitive_words_detected` 不再作为渠道基础设施失败计入健康熔断。

## [0.22.2] - 2026-08-21

v0.22.2 是 v0.22.1 的 **PATCH 兼容性修复版本**：适配 Kimi K3 对聊天请求参数和输出 token 字段的限制，修复 Kimi K3 调用持续返回参数错误的问题。详见
[release-v0.22.2.md](docs/releases/release-v0.22.2.md)。

### Fixed

- Kimi K3 请求移除不支持的固定采样参数，并将 `max_tokens` 规范化为 `max_completion_tokens`。

## [0.22.1] - 2026-08-21

v0.22.1 是 v0.22.0 的 **PATCH 安全修复版本**：阻断 OAuth adaptor 对私有/保留
地址的 SSRF 请求，并为 adaptor JSON 响应补充 `nosniff` 防护。详见
[release-v0.22.1.md](docs/releases/release-v0.22.1.md)。

### Fixed

- 在 OAuth adaptor 发起 outbound call 前校验最终上游 URL，拒绝私有和保留地址。
- 为 adaptor JSON 响应设置 `X-Content-Type-Options: nosniff`。

## [0.22.0] - 2026-08-19

v0.22.0 是 v0.21.0 之后的 **MINOR 安全与可靠性版本**：完成渠道凭证加密写入与存量迁移、持久化服务 fail-fast、OpenAPI/前端契约门禁、批量映射与批量删除完整性、请求体分级限制和前端错误兜底；无 API/proto 破坏性变更、无自动数据库 schema 迁移。详见 [release-v0.22.0.md](docs/releases/release-v0.22.0.md)。

### Added

- 新增 `channel-credentials` dry-run / apply 工具，迁移输出只包含计数与记录 ID。
- 新增 OpenAPI 生成校验、前端生成类型漂移校验和 Relay 执行边界 ADR。

### Fixed

- 禁止渠道凭证因缺少密钥而回退写入明文；channel / identity 持久化仓储缺少 DSN 时 fail-fast。
- 修复渠道模型映射 N+1、禁用渠道批量删除静默遗漏、请求体超限语义和前端通知/路由错误兜底问题。

### Changed

- 持久化 channel-service 必须配置 16/24/32 字节 `CHANNEL_ENCRYPTION_KEY`；升级已有环境前需按发布说明完成存量凭证迁移。

## [0.21.0] - 2026-08-18

v0.21.0 是 v0.20.5 之后的 **阶段收尾版本**：完成 v0.21 路线图定义的资金安全验证、真实 MySQL / PostgreSQL migration smoke、Release E2E 门禁和 P3-0 观察基线闭环。**无 API 破坏性变更、无新增数据库迁移、无 proto / 应用配置变更、无新增运行时功能**。详见 [release-v0.21.0.md](docs/releases/release-v0.21.0.md)。

### Added

- **v0.21 阶段验收记录**：固化首个结算周期对账、MySQL / PostgreSQL migration smoke、真实 Release E2E 和 P3-0 观察基线的验证结论。
- **触发式治理边界**：明确手动分区 DDL、热点文件拆分和 P3 五大议题不作为本版本前置，继续按路线图触发条件管理。

### Changed

- **发布文档状态**：路线图更新为阶段完成并进入 v0.21.0 发布收尾；发布说明补充兼容性、升级步骤和验证证据。

## [0.20.5] - 2026-08-18

v0.20.5 是 v0.20.4 之后的 **PATCH 生产稳定性版本**（4 个提交，`254970a` → `e219741`）：Responses → Chat Completions fallback 改用共享协议转换器，修复 Codex / DeepSeek 工具历史中的并行调用、乱序输出、孤儿输出与中断调用导致上游 400 的问题；管理后台修复 proto3 `omitempty` 造成禁用模型状态显示 `undefined` 的问题；release 工作流在镜像和 GitHub Release 发布前强制执行与 nightly 共用的 compose E2E + Playwright admin smoke。**无 API 破坏性变更、无数据库迁移、无 proto/应用配置变更**。受影响范围：relay-gateway、admin 前端 `web/dist`、release / nightly workflow 与执行文档。详见 [release-v0.20.5.md](docs/releases/release-v0.20.5.md)。

### Fixed

- **Responses fallback 工具历史**：fallback 改用 `internal/apicompat` 共享转换器，合并并行 tool calls、重排 tool outputs、附加 reasoning，并过滤中断调用与孤儿输出；空 input 保留显式空 user message，复杂 Codex / DeepSeek 历史不再被上游 400 拒绝。
- **管理后台禁用模型显示**：`listModels` / `getModel` 将 proto3 `omitempty` 省略的 `status=0` 归一化为禁用状态，列表、详情与启停操作不再显示 `undefined`。

### Changed

- **release 发布门禁**：新增 reusable `e2e.yml`，nightly 与 release 共用 compose E2E + Playwright admin smoke；release 先在目标 tag 上执行 E2E，失败即阻断镜像推送与 GitHub Release，并保留失败工件上传。

### Added

- **发布与观测证据**：记录 nightly E2E 连续 5 / 5 达标与 reusable workflow 远端验证；核实 Prometheus retention 健康并闭环 Grafana 只读凭据，补齐 2026Q3 P3 基线数据质量结论。

## [0.20.4] - 2026-08-16

v0.20.4 是 v0.20.3 之后的 **PATCH 生产稳定性版本**（7 个提交，`3ea0a6f` → `d96b3c0`）：修复路由死端以 `codes.Unknown` 穿越 gRPC 后被 relay 侧 circuit breaker 计为失败，导致 channel-service 整体熔断且无法自愈的生产事故；手动 ledger / logs 分区脚本增加 schema 治理、迁移 `078` 与 claim 覆盖硬前置检查；nightly E2E 等待 committed list query 消除 export 竞态，并沉淀 2026Q3 P3 观察基线。**无 API 破坏性变更、无数据库迁移、无 proto/应用配置变更**。受影响范围：relay-gateway、运维侧手动分区 SQL 与 nightly E2E。详见 [release-v0.20.4.md](docs/releases/release-v0.20.4.md)。

### Fixed

- **relay 路由死端与熔断语义**：`CHANNEL_NOT_FOUND` / `ROUTE_DEAD_END` 分别映射 gRPC `NotFound` / `FailedPrecondition`，不再计入 channel-service circuit breaker 或被当作可重试 `Unknown`；HTTP / Anthropic 边界保持路由死端 503，真实传输失败仍触发保护。
- **手动分区前置守卫**：ledger 脚本先校验迁移治理、`078` applied 与 ledger → claim 零缺失；logs 脚本校验 schema 迁移治理，任一条件不满足即中止。
- **admin export E2E 竞态**：等待同源 committed users list query 后再触发导出，避免 React Router 异步提交期间旧 handler 发出缺失筛选的请求。

### Added

- **2026Q3 P3 观察基线**：建立入口延迟、429/502、熔断与 dedupe claim 覆盖快照，并登记 Prometheus 保留窗口、Grafana 凭据与 counter 缺口；P3 议题维持触发式准入。

## [0.20.3] - 2026-08-15

v0.20.3 是 v0.20.2 之后的 **PATCH 质量门禁版本**（7 个提交，`c366dd9` → `a338a1c`）：CI 新增真实 MySQL / Postgres migration smoke（fresh、repeat no-op、状态审计、失败注入）；修复 compose MySQL healthcheck 过早 healthy 与 admin users export E2E 异步提交竞态；建立 P3-0 季度观察基线并补充延迟分位数、429/502 与熔断面板。**无 API 破坏性变更、无数据库迁移、无 proto/应用配置变更、无服务运行时代码变更**。详见 [release-v0.20.3.md](docs/releases/release-v0.20.3.md)。

### Added

- **迁移质量门禁**：CI 新增 MySQL / Postgres service-container migration smoke，`Makefile` 暴露对应 target，脚本验证 fresh apply、repeat no-op、状态审计与无效 SQL 失败注入。
- **P3-0 观察基线**：新增季度基线模板；Grafana relay-gateway dashboard 增加 P50/P95/P99 延迟、429/502 比例与熔断状态 / trips 面板。

### Fixed

- **compose MySQL readiness**：healthcheck 强制 `127.0.0.1` TCP ping，避免 entrypoint 临时 Unix socket 导致 migrate one-shot 启动竞态。
- **admin export E2E 竞态**：等待 React Router committed filter 与实际导出请求，不再依据早期 URL 状态间接断言，nightly 恢复双 suite 成功。

## [0.20.2] - 2026-08-14

v0.20.2 是 v0.20.1 之后的 **PATCH 补强版本**（5 个提交，`3cde415` → `538f80e`）：管理后台新增「上游成本」页面，支持按渠道 / 订阅账号 / 全局裸模型配置每 1M tokens 的上游采购价、缓存读取价格与 legacy 键迁移；对账脚本新增 ledger ↔ dedupe claim 双向覆盖检查，迁移窗口孤儿账本不再静默漏检。**无 API 破坏性变更、无数据库迁移、无 proto/配置变更**。受影响范围：admin-api（含管理前端 web/dist）与运维侧对账工具。详见 [release-v0.20.2.md](docs/releases/release-v0.20.2.md)。

### Added

- **管理后台上游成本**：新增 `/admin/upstream-costs` 页面与导航/总览入口，支持新增、编辑、删除、legacy 键 dry-run 预览与确认迁移；上游成本条目新增 `cache_read_price` 并预留 5m/1h 缓存创建价格。
- **对账守护**：`scripts/reconcile/checks.go` 新增 `checkClaimCoverage`，双向校验 `billing_ledgers` 与 `billing_ledger_dedupe_claims` 覆盖完整性，孤儿 ledger/claim 均判失败。

### Fixed

- **上游成本可选价格语义**：通过 `*_set` 标记区分「未发送」与「显式清空」；服务端拒绝负 input/output/cache 价格；迁移结果返回实际 `executed` 数量并正确计入目标键已存在的 `skipped` 项。

## [0.20.1] - 2026-08-14

v0.20.1 是 v0.20.0 之后的 **PATCH 修复版本**（2 个提交，`5c89752` → `64a6ed6`）：升级管理前端传递依赖 nanoid 3.3.17 → 3.3.18 修复 CVE-2026-67213（随机 ID 生成死循环 DoS，nanoid 仅存在于构建期工具链、不进入运行时 bundle）；将 v0.19–v0.20 执行记录归档并确立 v0.21 路线图为唯一规划入口。**无 API 破坏性变更、无数据库迁移、无 proto/配置变更、无运行时行为变化**，服务端无需重新部署。详见 [release-v0.20.1.md](docs/releases/release-v0.20.1.md)。

### Fixed

- **web(nanoid)**：postcss 传递依赖 nanoid 3.3.17 → 3.3.18，修复 CVE-2026-67213 无限循环 DoS（代码扫描告警）；lockfile 根包 `engines` 同步为 `node >=24 <25`（对齐 `web/.nvmrc`）。

### Changed

- **docs**：新增 `docs/design/v0.19-v0.20-execution-record.md`（归档）与 `docs/design/v0.21-roadmap.md`（唯一规划入口），`docs/README.md` / `docs/TODO.md` 精简指向。

## [0.20.0] - 2026-08-14

v0.20.0 是 v0.19.1 之后的 **MINOR 功能版本**（10 个提交，`47e619e` → `79108d5`）：接通 relay-gateway HTTP 入口请求 / 延迟指标（`NewHTTPMetricsMiddleware` 挂载最外层、路径低基数归一）；为 RANGE 分区后的 `billing_ledgers` 新增非分区全局 dedupe claim 表（迁移 `078`，同事务原子裁决并发资金写，修复分区边界表达式 / 分区名比较 / 财务分区误自动 DROP）；修复 admin 表格 URL 筛选快速连续变更的 stale-closure 竞态；修复 nightly compose E2E / Playwright smoke（卷路径、locators、pb.go 生成、SERVICE_TOKEN）。**无 API 破坏性变更，包含数据库迁移 `078`**。受影响服务：relay-gateway、billing-service、log-service、admin-api（含前端）。详见 [release-v0.20.0.md](docs/releases/release-v0.20.0.md)。

### Added

- **Relay HTTP 入口指标**：`platform/middleware.NewHTTPMetricsMiddleware` 挂载 relay-gateway 路由链最外层，记录最终 status、method、低基数 path 与延迟直方图；`/healthz` / `/metrics` 不计入。
- **分区安全账本幂等**：新增 `billing_ledger_dedupe_claims` 非分区全局 claim 表（迁移 `078`，含存量回填）；`CreateLedgerInTx` 同事务先 claim 后 insert，冲突统一映射 409。

### Fixed

- **分区维护**：`billing_ledgers`（`TO_DAYS`）与 `logs`（Unix epoch）按表选择边界表达式；修复 `2006-01` / `200601` 分区名格式比较；`pYYYYMM` 上界改为次月 1 日；财务账本分区不再被自动 DROP，归档需独立审批。
- **Admin 表格筛选竞态**：`useAdminTableState` 用 pending-issued ref 累积 URL 更新，外部 URL 变化才 resync；同步逻辑移入 effect 避免 react-hooks refs lint 违规；三组回归单测覆盖。
- **Nightly E2E**：prometheus/grafana 卷改仓库相对路径；smoke 对齐中文文案与新组件；upload-artifact v7；compose-e2e 生成 pb.go stubs；gRPC 调用附带 SERVICE_TOKEN。

### Changed

- billing-service `partition.tables` 配置字段 deprecated：旧值仍可解析，运行时忽略，只维护 `billing_ledgers`。

## [0.19.1] - 2026-08-13

v0.19.1 是 v0.19.0 之后的 **PATCH 工程收尾版本**（1 个提交，`23d6c8e`），落地 v0.19 路线图 P2 全部三项：工具链版本固定（`scripts/tool-versions.env` 唯一版本源，消除 `@latest` 漂移）、`make clean` 扩展 + 新增 `make verify` 聚合门禁 + compose/migrate 前置检查、热点文件拆分评估（维持触发式）。**不含任何生产代码变更、无 API/数据库/proto/配置变更、无运行时行为变化**，无需重新部署。详见 [release-v0.19.1.md](docs/releases/release-v0.19.1.md)。

### Added

- **工具链版本固定（P2.1）**：`scripts/tool-versions.env` 统一固定 buf v1.72.0 / wire v0.7.0 / gosec v2.28.0 / govulncheck v1.6.0 / gitleaks v8.30.1 / syft v1.51.0 / go-licenses v1.6.0，`Makefile include` 与 `security.yml source` 共用；`make init` 与全部 `security-*` 目标不再使用 `@latest`；新增 `make tools-upgrade-check`（只读对比 pinned vs latest）；前端 `web/.nvmrc`（24）+ `package.json engines >=24 <25`；策略文档 `docs/design/v0.19-toolchain-pinning.md`。
- **`make verify` 聚合门禁（P2.2）**：一键执行 unit / race / architecture / migration-check / frontend（lint/test/build），与 `ci.yml` PR 门禁对齐。
- **前置检查（P2.2）**：`compose-prereq`（docker + compose 文件，挂 `test-e2e` / `test-e2e-suite`）、`migrate-prereq`（`MIGRATIONS_DSN` / `SQL_DSN`，挂 `migrate` / `migrate-status`）。

### Changed

- **`make clean` 扩展（P2.2）**：新增根目录 ad-hoc 二进制 6 个、`test/e2e/e2e-test`、`coverage.out`、`web/test-results`、`web/playwright-report`、安全扫描报告文件；全部显式列出、幂等，不使用宽泛通配。


## [0.19.0] - 2026-08-12

v0.19.0 是 v0.18.4 之后的 **MINOR 稳定化版本**（4 个提交，`c0ddf36` → `27485b7`），主线为「兼容性守护 + 迁移治理 + 可发布性」：协议转换链路显式契约矩阵、迁移静态一致性门禁、CI integration/e2e 分层、基础设施包直接单测；唯一生产代码改动是 `platform/tracing` 的 OTLP 裸 `host:port/path` 路径归一化修复。**无 API 破坏性变更、无数据库迁移、无 proto 变更、无配置变更**，无强制重新部署需求。详见 [release-v0.19.0.md](docs/releases/release-v0.19.0.md)。

### Added

- **协议兼容性契约矩阵（P1.1）**：`internal/apicompat` + `internal/server` 以「注册表 + 覆盖断言」登记 Responses↔Anthropic / Chat↔Responses / WebSocket sticky 全部转换坐标，新增路径漏注册即测试失败；共享 fixture 覆盖全部三种 server-side web-search tool 变体；规则文档 `docs/design/v0.19-compat-matrix.md`。
- **迁移治理静态门禁（P1.2）**：`cmd/migrate-check` + `make migration-check`——新增重复数字前缀硬失败（历史重复 allowlist）、ownership.yaml 覆盖校验（补 `061` → billing）、`auto_mirror_from_prefix: "072"` 起 postgres/sqlite 逐字镜像强制；SQLite fresh + incremental 生命周期测试。
- **CI 测试分层（P1.3）**：`ci.yml` 新增 integration job（所有 `internal/integration` 每次 PR 运行）+ backend 挂钩 migration-check；新增 `nightly.yml`（compose e2e + Playwright admin smoke + 失败工件采集，`E2E_KEEP_ON_FAILURE` 保留容器供诊断）。
- **基础设施单测（P1.4）**：`platform/grpc/xgrpc`、`platform/grpc/resilience`、`platform/security/auth`（含 race 门禁纳入）、`platform/security/crypto`、`platform/tracing`、`pkg/timeout` 直接单测。

### Fixed

- **fix(platform/tracing)**：`normalizeOTLPEndpoint` 对裸 `host:port/path` endpoint 返回无前导斜杠的 path，导致 `otlptracehttp.WithURLPath` 拼出 malformed URL（platform-L3 回归家族）；现与带 scheme 分支行为一致。


## [0.18.4] - 2026-08-12

v0.18.3 之后的 **PATCH 修复版本**（1 个提交，`29e875d`），修复 admin 运营后台「高消耗渠道」排行把订阅账号流量误渲染为「已删除渠道」的问题，并为全部 Top-N 用量排行补上不依赖 SQL 返回顺序的确定性 quota 降序排序；前端概览页新增「高消耗订阅账号」排行卡片。**无 API 破坏性变更、无数据库迁移、无 proto 变更、无配置变更**。受影响服务为 admin-api（含管理前端 web/dist）。详见 [release-v0.18.4.md](docs/releases/release-v0.18.4.md)。

### Fixed

- **fix(admin): correct usage ranking dimensions and ordering（29e875d）**：订阅账号流量携带等于账号 id 的合成 `channel_id`，`AggregateUsageTopN("channel")` 单维度聚合时这些行混入渠道排行并被渲染为「已删除渠道」，还挤占 Top-N 名额；修复为按 `["channel", "subscription_account"]` 双维度请求（limit=0），在 service 层先剔除订阅账号行再截取 Top-N。同时修复 billing 仅在 bucket 数超过 limit 时才做 SQL Top-N、bucket 较少时行序不确定的问题——所有维度统一在 service 层按 quota 降序重排。影响 admin-api。

### Added

- **前端「高消耗订阅账号」排行卡片**：`OverviewPage` 新增 `top_subscription_accounts` 排行（cyan 配色，标签带平台标注），概览排行区从 4 列扩展为 5 列。影响 web/dist。


## [0.18.3] - 2026-08-12

v0.18.2 之后的 **PATCH 测试收口版本**（2 个提交，`a21970a` + 矩阵补齐），**不含任何生产代码变更，无运行时行为变化**：将发布后落在 develop 上的 Anthropic fallback tool 测试对齐（`a21970a`）收口进 tag，并补齐 fallback 路径的 web_search 兼容性最小矩阵（请求 history `web_search_call` 丢弃、非流式 blocks 丢弃、流式 blocks 丢弃 + 文本/终止事件不受干扰），与 apicompat 侧 OAuth 层用例一一对应。审查确认 `a21970a` 为纯测试对齐——fallback 与 OAuth 路径共用 `convertResponsesToAnthropicTools()`（be53c14 在该函数中跳过 web_search），「tools=2」为正确预期，且新断言方向是收紧而非放松。**无 API 变更、无数据库迁移、无 proto 变更、无配置变更**；生产运行 v0.18.2 的用户无需为本版重新部署。详见 [release-v0.18.3.md](docs/releases/release-v0.18.3.md)。

### Added

- **test(server): fallback web_search minimal compatibility matrix**：在 `internal/server/responses_anthropic_fallback_test.go` 新增三个用例，覆盖 fallback 边界的请求 history `web_search_call` 丢弃（`TestResponsesRequestToAnthropicBodySkipsWebSearchCallHistory`）、非流式 `server_tool_use`/`web_search_tool_result` blocks 静默丢弃（`TestAnthropicResponseToResponsesDropsServerToolBlocks`）、流式 blocks 丢弃且文本 delta 与 `response.completed`/`[DONE]` 终止事件不受干扰（`TestTransformAnthropicStreamDropsServerToolBlocks`）。无生产代码变更。

### Changed

- **test(server): align Anthropic fallback tool expectations（a21970a 收口）**：`TestResponsesRequestToAnthropicBodyNormalizesCodexTools` 预期 tools 数 3 → 2，新增显式断言 client tools（`exec_command`/`multi_agent_v1`）保留、`web_search` 跳过。与既有实现语义对齐，非掩盖实现偏差。

## [0.18.2] - 2026-08-12

v0.18.1 之后的 PATCH 修复版本（7 个提交，`759181f` → `8b40b63`），核心是修复 Kimi K3 等 Anthropic 兼容上游联网搜索导致 codex 端「Search results for query: …」文本无限粘连、多轮对话崩溃的 bug：完全静默丢弃 `server_tool_use` / `web_search_tool_result` content blocks（codex 不支持对应 output item 类型），并在 OAuth relay 路径（`ClaudeOAuthAdaptor`）的 `convertResponsesToAnthropicTools()` 中跳过 web_search tools——此前仅 fallback 路径剥离 tool 标识符，OAuth 路径仍触发上游服务端联网搜索。同时完成 v0.18 P4 可观测性闭环：`xgrpc.UnaryClientMetricsInterceptor` 接入全部 gRPC dial 点并在真实流量下回填 BASELINE；新增 relay 下游 gRPC 熔断器 `resilience` 配置段（env-gated 默认关闭，生产已开启）。**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为 relay-gateway、channel-service、identity-service、billing-service、monitor-worker。详见 [release-v0.18.2.md](docs/releases/release-v0.18.2.md)。

### Fixed

- **fix(apicompat): 修复 Kimi K3 web_search 文本粘连死循环（759181f / ddacd81 / be53c14 三递进提交）**：Kimi K3 等上游联网搜索返回 `server_tool_use` + `web_search_tool_result` blocks。ddacd81 将其从「转换为 codex 不支持的 `web_search_call` output items」改为完全静默丢弃（新增 `SkippingBlock` flag 保证跳过期间 delta/stop 不干扰已打开的 message/reasoning/function_call item 状态），流式 / 非流式 / 请求三方向完整处理；be53c14 根治 OAuth relay 路径——`ClaudeOAuthAdaptor` 此前把 web_search tools 以 `web_search_20250305` 原样转发触发上游服务端联网搜索，现与 fallback 路径一致地完全跳过。影响 relay-gateway（OAuth + fallback 路径）。
- **chore(security): gosec G101 误报标注（8b40b63）**：`ReasonTokenSubnetViolation = "TOKEN_SUBNET_VIOLATION"`（v0.18.1 引入）被 G101 硬编码凭证正则命中，实为错误码标签；按仓库惯例加 `#nosec` 标注，pre-push gosec 门禁通过。

### Added

- **feat(relay): 新增 resilience 配置段，env-gated 默认关闭（8f11028）**：relay-gateway 下游 gRPC 熔断器配置入口 `RELAY_RESILIENCE_ENABLED`（默认 false）/ `RELAY_RESILIENCE_TIMEOUT`（默认 3s）；启用后下游调用经 `ResilientClient` 包装并记录 `circuit_breaker_state`。默认关闭不改变行为。影响 relay-gateway。
- **feat(observability): v0.18 P4 可观测性闭环（445771a + 30ffb73）**：`xgrpc.UnaryClientMetricsInterceptor` 接入全部 gRPC dial 点（relay-gateway / channel / identity / billing / monitor / internal/data），纯计时无 I/O；生产真实流量回填 BASELINE（identity 2.7/6.9/9.6ms、channel 1.5/9.3/18.6ms、billing 1.0/20.9ms、log 0.6/4.8/21.9ms P50/P95/P99；commit async 31/60/92ms、reserve sync 8/40/48ms），无热路径回归。运维开启 relay resilience 后 `circuit_breaker_state` 4 下游 closed、24h trips=0。附 identity server/test 与 pkg/errors 历史 gofmt 漂移修复。影响 relay-gateway、channel-service、identity-service、billing-service、monitor-worker。


## [0.18.1] - 2026-08-11

v0.18.0 之后的 PATCH 修复版本（3 个提交，`e6c1673` → `0ebe48a`），修复两个影响生产可用性的缺陷并完成 v0.18 P2 工程卫生：（1）Token 创建默认配额 bug——前端仅发送 `{name}` 时 `unlimited_quota` 被解析为 `false`（bool 零值），Token 以永久耗尽状态写入、首次使用即被拒绝；（2）identity → relay 错误码链路映射错误——`ErrTokenExhausted` 映射为 gRPC `NotFound` → HTTP 401，误导客户端将有效但耗尽的 key 当作错误 key（修正后：耗尽 → 429、禁用 → 403、子网 → 403）；同时修复 MySQL `RowsAffected==0` 导致幂等更新误判 NotFound 的 DSN 边界（影响 10+ 调用点）、补齐 billing commit/reserve Prometheus Observe 与 admin-api gRPC 客户端延迟拦截器。**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为 identity-service、relay-gateway、admin-api、billing-service。详见 [release-v0.18.1.md](docs/releases/release-v0.18.1.md)。

### Fixed

- **fix(identity): token create defaults to unlimited + correct error-code mapping**：请求结构 `unlimited_quota` 改用 `*bool`（省略 → nil → 默认无限），新增 biz 守卫 `ErrTokenQuotaInvalid` 拒绝退化零配额 Token；全链路错误码修正：`exhausted → ResourceExhausted(429)`、`disabled → PermissionDenied(403)`、`subnet → PermissionDenied(403)`、`not found/expired → Unauthenticated(401)`；relay-gateway 5 个错误处理器补 `codes.Unauthenticated → 401`。影响 identity-service、relay-gateway。
- **fix(test): exclude network-bound integration tests from unit gate**：5 个 `*/internal/integration` 包绑定真实 TCP listener（需网络权限），在 CI sandbox 中 `bind: operation not permitted`；从 `test-unit` 排除、新增 `make test-integration`。同步修正 `TestRelayIntegration/DisabledToken` 断言（401 → 403）。

### Added

- **feat(observability): v0.18 P2 engineering hygiene (C2/C4/C5)**：C2 MySQL `withClientFoundRows` DSN helper（gorm + `*sql.DB` 双路径统一 matched-rows，修复 10+ 调用点幂等更新 false-NotFound）；C5 billing commit/reserve Prometheus Observe（sync/async 标签分离避免双重计数）+ `xgrpc.UnaryClientMetricsInterceptor` 接入 admin-api 三 dial；C4 分区触发阈值文档 + 运维手册（无代码变更）。影响 admin-api、billing-service 及全部 xdb 使用者。

## [0.18.0] - 2026-08-10

v0.17.1 之后的 MINOR 功能版本（4 个提交，`fd09278` → `636fdd4`），完成 v0.18 路线图 P0 并修复 P1 首周期发现：admin 资金写路径的请求级幂等（方案 B，DB 唯一键）。购买（`PurchaseSubscription`）与充值（`TopUpQuota`）的钱包扣款/充值 ledger 携带基于 `(user_id, request_id)` 的显式去重键，复用 `billing_ledgers.ledger_dedupe_key` 既有全局唯一索引作为闸门——并发同键重复请求整笔事务回滚，钱包绝不被扣两次（关闭 M6 已知边界 #1）。同时顺带修复存量 bug：购买扣款 ledger 回退到不含 user_id 的 legacy 键导致同一 group 的第二次购买（即使不同用户）唯一键冲突失败；并修复 P1 对账首周期（C6）发现的卡单检测误报（余额订单被当卡单）。**无数据库迁移、无新增配置项**；API additive（两份 proto 新增 `request_id` 字段）。受影响服务为 admin-api、billing-service（含前端购买流程）。详见 [release-v0.18.0.md](docs/releases/release-v0.18.0.md)。

### Added

- **feat(billing): v0.18 P0 request-level idempotency for admin money paths**：购买/充值 ledger 显式携带 `(user_id, request_id)` 去重键（`{action}:{user_id截断48}:{request_id≤100}`），复用 `ledger_dedupe_key` 全局唯一索引；并发同键第二次 INSERT 唯一约束冲突 → 整笔事务回滚，钱包不被扣两次。客户端发送 `Idempotency-Key` 头（与 relay 同协议），空键映射 `auto:{hex}`（legacy 兼容）；重复请求 → gRPC `AlreadyExists` → HTTP 409。前端购买流程携带 session 级 `Idempotency-Key`。新增 `ErrDuplicateRequest`、`make test-race` 覆盖 app/billing + app/admin。修复 legacy 去重键冲突 bug（同一 group 多用户可各自购买）。影响 admin-api、billing-service。

## [0.17.1] - 2026-08-10

v0.17.0 之后的 PATCH 版本（3 个提交），修复上游 SSE 流卡死导致连接泄漏的生产问题：新增滑动 idle 超时，活跃流不受影响，仅在上游连续 timeout 无字节时主动断开；收尾 P3 性能基线文档与 jsonx 最终决策（纯文档）；修复 TODO.md 相对路径错误导致的 CI markdown 链接检查失败。无 API 破坏性变更、无数据库迁移、无 proto 变更。受影响服务为 relay-gateway。详见 [release-v0.17.1.md](docs/releases/release-v0.17.1.md)。

### Fixed

- **fix(relay): bound stalled upstream SSE streams**：OpenAI / Anthropic / Azure / Gemini provider 的 streamClient 此前为裸 `&http.Client{}`（无超时），上游 SSE 响应 stall（建立连接后停止吐字节不断开）时 `io.Copy` 无限阻塞、仅靠 context deadline 兜底。新增 `stream_timeout.go`：ResponseHeaderTimeout 约束响应头等待，`streamIdleReadCloser` 实现滑动 idle 超时（有字节到达就重置，连续 timeout 无字节才断开），正常长流式响应不受影响；stall 断开时输出 warn 日志。影响 relay-gateway 流式路径。
- **fix(docs): correct P31 execution report link path in TODO.md**：`docs/TODO.md` 两处相对路径多写一层 `../`（`../../scripts/...` → `../scripts/...`），导致 `check-markdown-links.py` CI 检查失败。

### Changed

- **chore(p3): complete P3.1 amd64 baseline + P3.2 jsonx final decision**：P3.1 Linux/amd64 三版本基线复测（v0.16.0 vs develop，3×8min k6 全负载，chat P95 116.68ms vs 116.34ms 无回退）、P3.2 jsonx 决策（amd64 上 sonic 双向胜出，保留 pkg/jsonx 不回退），归档基准数据与 CPU profile。纯文档与基准数据，无代码逻辑变更。


## [0.17.0] - 2026-08-08

v0.16.0 之后的 MINOR 功能版本（14 个提交），完成 v0.17 路线图 P0（工程收尾）与 P1（运营闭环）两项交付并补齐发布门禁：修复两个前端依赖安全漏洞（nanoid CVE-2026-67213、js-yaml GHSA-5p4m-2wfm-xmqj，code-scanning #270/#269）、修复 Docker CI 的 9 服务 × 2 架构（18 job）矩阵、升级 CI 运行时与 CodeQL、加固 gross-profit 指标口径、统一 Grafana dashboard，并为 P3 性能基线与 jsonx 决策补充可复现证据。无 API 破坏性变更、无数据库迁移、无 proto 变更。详见 [release-v0.17.0.md](docs/releases/release-v0.17.0.md)。

### Added

- **chore(p1): complete v0.17 roadmap P1 — charge monitoring, reconciliation automation, forced-failure verification**：新增 cache-creation charge 监控告警规则 + 运行手册、`scripts/reconcile/` 一键对账（reconcile.sh / checks.go / 供应商账单模板）、发布后强制失败验证脚本（verify-forced-failure.sh + forced_failure_checks.go）与运行手册。影响 admin-api、relay-gateway、billing-service。
- **chore(p3): P3.1 baseline harness fixes + P3.2 jsonx benchmark evidence**：k6-baseline.js 新增 SMOKE 模式、Makefile benchmark 正确落盘、P3.1 运行手册、上帝 mock upstream；pkg/jsonx 与 apicompat 代表性负载 benchmark、P3.2 决策文档（arm64 证据：Unmarshal 2.2–3.8x 快、Marshal 大负载收敛 ~5%，保留 jsonx 不回退 Marshal）。

### Changed

- **chore(p0): complete v0.17 roadmap P0 — CI race guard, probe logging, docs index**：新增 `make test-race` 并接入 CI；anthropic_model_probe 补结构化日志；docs 索引对齐。
- **fix(billing): isolate gross-profit metric for tests + document scope and threshold calibration**：gross-profit 指标 registry 可注入、测试不再读共享 DefaultGatherer；文档明确口径（仅覆盖成功 write-ledger 提交）；reconcile README 补阈值校准章节。
- **fix(monitoring): unify dashboard files to raw grafana format**：billing / relay-gateway / service-dependencies dashboard 统一为 raw grafana 格式。
- **ci: upgrade actions to Node 24 runtimes / ci(security): upgrade codeql-action v3 -> v4**：12 个 Actions 升 Node 24 majors、CodeQL 5 处引用升 v4，消除弃用告警。
- **chore(benchmark): repair reproducible performance baseline**：k6 吞吐口径、mock upstream、BASELINE.md 重写。
- **docs(release): 发版流程在合并 main 前先推送 develop**：AGENTS.md 发版流程调整。

### Fixed

- **fix(deps): bump nanoid 3.3.16 -> 3.3.17 (CVE-2026-67213)**：修复无限循环 DoS（code-scanning #270）。影响 admin-api 前端。
- **fix(deps): bump js-yaml to 4.3.1 (GHSA-5p4m-2wfm-xmqj)**：修复 `!!omap` 二次方 CPU DoS（code-scanning #269）。
- **fix(ci): expand Docker matrix to full 9 services x 2 platforms (18 jobs)**：修复 include+platform 组合折叠，显式计算 9×2 笛卡尔积。
- **fix(ci): write matrix-ci output with key prefix for GITHUB_OUTPUT**：修复 `$GITHUB_OUTPUT` 缺 `matrix-ci=` 前缀导致的解析拒绝。
- **fix(ci): reference matrix-ci output key correctly**：修复 job output 引用 `outputs.matrix-ci` 而非 `outputs.matrix`。

## [0.16.0] - 2026-08-06

v0.15.3 之后的 MINOR 功能版本（7 个提交），标志着 v0.11.0 路线图收尾阶段（P0–P3）全部完成：
routing-ops 双源指标降级（Prometheus → relay-gateway 直采）作为唯一面向用户的新功能，
P1 契约加固（同优先级精确回退 + 并发 active 唯一约束）补齐确定性回归测试，cache-creation
计费从 observe 切换为 charge 的生产闭环，以及 6 个服务 conf 包测试等工程卫生工作。
无 API 破坏性变更、无数据库迁移、无 proto 变更。详见 [release-v0.16.0.md](docs/releases/release-v0.16.0.md)。

### Added

- **feat(admin): P2.3 routing-ops dual-source metrics — relay-gateway scrape fallback**：当 Prometheus 不可用时，admin-api 自动降级为直接 scrape relay-gateway 的 `/metrics` 端点（`expfmt` 解析 exposition format，聚合 cumulative counter），保持 routing-ops 视图 `partial=false`。`RoutingRates` 新增 `Source` 字段，JSON 新增 `source`/`cumulative`；docker-compose 内置 `RELAY_METRICS_ENDPOINT` 默认值；`prometheus/common` 从 indirect 提升为 direct。10 条回归测试覆盖双源优先级、降级、全故障路径。影响 admin-api。

### Changed

- **chore(p3): clean up billing_model TODO + update TODO.md status**：`internal/biz/billing_model.go` 的 `channel_mapped ≡ upstream` 限制从 TODO 转为永久 NOTE（结构性变更超出当前计费模型）；`docs/TODO.md` 回写 P0–P3 全部完成状态。

### Fixed

- **docs(v0.16): consolidate v0.16 roadmap + fix mock race + dedup comment**：新增 `docs/design/v0.16-roadmap.md` 作为 P0–P3 收尾文档，替换各文档中的 `.workbuddy` 临时链接；修复 `mockConcurrentCreateRepo` 竞态（补 `GetActiveSubscriptionByUser` 加锁）；删除 `routing_rates.go` 重复注释行。

## [0.15.3] - 2026-08-06

v0.15.2 之后的 PATCH 版本（3 个提交），内容为内部重构与代码规范，无对外行为变更：将全仓库
JSON 序列化统一收敛到 `pkg/jsonx` 单一封装层（底层 sonic `ConfigStd`，保持 `encoding/json`
语义——HTML 转义、map key 排序、字符串拷贝），第一步迁移 52 个非测试文件的
`encoding/json`→`jsonx`（含升级 sonic 至 v1.15.2，唯一保留 `bodylimit.go` 因依赖 sonic 未暴露
的类型断言），第二步替换 53 个热点文件的直接 `sonic.*`/`sonic.ConfigStd.*` 调用、补齐
`jsonx.Get` 与 `AGENTS.md` JSON 策略章节，第三步一次纯机械 `gofmt -w` 收尾 50 个不规范文件。
无 proto 变更、无数据库迁移、无新增配置项。详见 [release-v0.15.3.md](docs/releases/release-v0.15.3.md)。

### Changed

- **refactor(json): replace encoding/json with sonic via pkg/jsonx wrapper**：扩展 `pkg/jsonx` 为 `encoding/json` 的 drop-in 替代（`sonic.ConfigStd`），迁移 `app/*`、`internal/*`、`domain/*`、`platform/*` 共 52 个非测试文件；升级 `github.com/bytedance/sonic` 至 v1.15.2；`platform/middleware/bodylimit.go` 保留 `encoding/json`（依赖 sonic 未暴露的类型断言）。影响全部后端服务。
- **refactor(jsonx): route all sonic calls through pkg/jsonx wrapper**：将 `internal/server`、`internal/apicompat`、`internal/adaptor`、`internal/biz`、`internal/identity`、`domain/upstream/provider/*`、`app/{channel,config,log,monitor,notify}`、`platform/{events,middleware/idempotency}` 共 53 个文件的直接 `sonic.*`/`sonic.ConfigStd.*` 替换为 `jsonx.*`；新增 `jsonx.Get`（封装 `sonic.Get`）与 `pkg/jsonx/json_test.go`（与 `encoding/json` 一致性 + 基准）；`AGENTS.md` 新增 JSON 序列化策略章节。影响全部后端服务。
- **style(gofmt): format 50 noncompliant Go files across the repo**：纯机械 `gofmt -w` 扫描——import 分组排序、结构体字面量字段对齐、闭包体重新缩进、单行函数展开、多余空行清理。无语义改动。`gofmt -l` 全仓库 clean。

## [0.15.2] - 2026-08-05

v0.15.1 的 PATCH 修复版本（3 个提交），全部位于 relay-gateway：修复 v0.15.1
渠道统计去噪因 `applyPlanInputs` 赋值顺序反转而完全失效的回归（`SourceKind`
恒空、`UpstreamModelID` 被写成字面量 "subscription"）；修复订阅来源 Anthropic
协议上游（如 kimi）流式响应的三个缺陷——`data:` 无空格行被静默丢弃、上游中途
断连缺 `[DONE]` 哨兵、`message_start` 前关闭无终止事件——导致 codex 报
"stream disconnected before completion"；并将 `response.completed` 之后的
`response.failed` 守卫补齐到 adaptor 路径，消除矛盾双终止。无 API 破坏性变更、
无数据库迁移。详见 [release-v0.15.2.md](docs/releases/release-v0.15.2.md)。

### Fixed

- **fix(relay): applyPlanInputs reversed source-kind/upstream-model assignment**：按正确顺序接收 `upstreamCostKeyInputsFromPlan` 的 `(sourceKind, upstreamModelID)` 返回值；订阅流量恢复跳过 channel 维度统计，规范化计费 cost key 恢复正常。影响 `relay-gateway`。
- **fix(relay): ensure terminal SSE event on upstream stream interruption**：`sseData` 同时接受 `data:` / `data: ` 两种形式；`scanner.Err()` 分支在终止事件后追加 `[DONE]`；上游在 `message_start` 前关闭时合成 `response.failed`。修复同时应用于 adaptor 路径与 fallback 路径，含回归测试。影响 `relay-gateway`。
- **fix(relay): guard adaptor-path scanner error against completed+failed**：`pumpAnthropicToResponses` 镜像 `CompletedSent` 守卫，已完成的流只追加 `[DONE]`，不再叠加 `response.failed`。影响 `relay-gateway`。

## [0.15.1] - 2026-08-05

v0.15.0 的 PATCH 修复版本（2 个提交）：收尾订阅变更链路的 M6 缺陷——换组用量窗口
仅在真正跨组时重置（同组改套餐保留已跑用量，避免丢数据与免费刷新配额），并为
`ChangeSubscription` 增加行锁串行化（`SELECT ... FOR UPDATE`）防止并发变更互相覆盖
写回；同时让 relay-gateway 跳过订阅来源流量的 channel 维度用量统计（合成 ChannelID
导致 channel-service 刷 "channel not found" 告警噪声）。无 API 破坏性变更、无数据库
迁移。详见 [release-v0.15.1.md](docs/releases/release-v0.15.1.md)。

### Fixed

- **fix(subscription): M6 - reset usage only on group change + row-locked ChangeSubscription**：用量窗口重置改为条件触发（仅 `ToGroupID != fromGroupID` 才清零）；`SubscriptionUsecase` 新增可选 `TxRunner`，`ChangeSubscription` 在 `RunInTx` 内用 `GetByIDInTx`（`SELECT ... FOR UPDATE`）+ `UpdateSubscriptionFieldsInTx` 串行化并发变更；admin-api 接线 `NewTxRunner(repo)`；`subscription_name` 仅在请求实际改变时窄写。影响 `admin-api`、`billing-service`。
- **fix(relay-gateway): 跳过订阅流量的渠道用量统计**：新增 `recordChannelUsageFromDetail` helper，对 `SourceKind == "subscription"` 直接跳过并记 `skipped_channel_stats` 指标，channel 来源保持原逻辑。影响 `relay-gateway`。

## [0.15.0] - 2026-08-04

MINOR 功能版本（6 个提交）：闭合订阅账号 weight 反馈回路（channel selector 的
per-process inflight 计数首次接入 relay 实际占用，`loadFactor` 取
`max(local, crossReplica)`），打通审计平台 actor / request-id 提取（relay-gateway
与 admin 退款 / identity 登录的敏感操作审计记录从此可归因，mutable `*actorHolder`
解决 Go 不可变 request 导致 actor 为空的问题），修复前端 4 个 npm 依赖漏洞
（hono / undici / fast-uri / brace-expansion）。无数据库迁移、无 API 破坏性变更。
详见 [release-v0.15.0.md](docs/releases/release-v0.15.0.md)。

### Added

- **feat(relay,channel): close the subscription-account weight loop (slot feedback)**：channel proto 新增 `RecordSubscriptionAccountSlot`；relay 通过可选 `SubscriptionAccountSlotReporter` 接口在 slot 授予/释放时 fire-and-forget 上报，选择器 `loadFactor` 取 `max(local, crossReplica)`。影响 `relay-gateway`、`channel-service`。
- **feat(audit): resolve actor + request-id extraction, wire admin & login audit**：`extractRequestID` 改读平台中间件；新增 `WithActor`/`ActorFrom` 标准上下文键；admin guard 两条鉴权路径盖戳 actor，退款 handler 显式审计；identity Login 成功/失败分支记录 `LogUserLogin` 并传真实 client IP。
- **feat(audit): mutable actor holder, injected auditors, relay actor, session prefix**：审计中间件注入可变 `*actorHolder` 解决 Go 不可变 request 问题；relay `getAuthSnapshot` 盖戳真实用户；identity auditor 注入 usecase；SessionID 取 8 字符前缀避免泄露会话 token。

### Fixed

- **fix(relay): make slot feedback truly fire-and-forget + idempotent pair**：`reportSubscriptionAccountSlot` 改后台 goroutine + 200ms 超时 + `context.Background()`；`releaseSlotWithReport` 用 `sync.Once` 守卫释放+上报幂等对。影响 `relay-gateway`。
- **fix(web): patch npm vulns (hono, undici, fast-uri, brace-expansion) + remove deprecated tsconfig baseUrl**：hono CVE-2026-69207 ReDoS、undici 6 条注入/desync、fast-uri CVE-2026-18446 host 混淆、brace-expansion CVE-2026-69152 DoS；移除弃用的 TS `baseUrl`。影响 `admin-api` 前端产物。

### Changed

- **test(relay): de-flake slot-feedback assertions for slow CI runners**：slot 上报断言改为集合校验（acquire + release 任意顺序）+ 5s poll 超时。

## [0.14.0] - 2026-08-04

MINOR 版本（4 个提交）：为订阅续费链路补齐 `renewal_strategy` 可观测字段（迁移
`077`，additive）、修复 admin 延长订阅的并发写 clobber 与过期激活缺陷、为 Redis
故障态并发语义补充多副本断言并文档化 fail-open 权衡，完成 code-review L 系列核验。
1 个 additive 数据库迁移。详见 [release-v0.14.0.md](docs/releases/release-v0.14.0.md)。

## [0.13.3] - 2026-08-03

v0.13.2 的 PATCH 修复版本（7 个提交）：订阅购买完成幂等性（claim-before-fulfil，
M10 资金相关）、relay 上游限流（429/423/529）不再误触发断路器、admin 补偿失败错误
透出与卡单检测、CI 矩阵/缓存/多架构推送加固。无 API 破坏性变更、无数据库迁移。
详见 [release-v0.13.3.md](docs/releases/release-v0.13.3.md)。


## [0.13.2] - 2026-08-02

v0.13.1 的 PATCH 修复：billing-service 异步结算分账恢复路径的空指针 panic 导致
crash loop（线上观测到约 1274 次重启），表现为 relay-gateway 的 `ReserveQuota`
gRPC 间歇失败、客户端收到空消息体的 402；以及迁移文件默认旧版共享 DB 历史导致
全新 MySQL / PostgreSQL / SQLite 无法干净建库。详见
[release-v0.13.2.md](docs/releases/release-v0.13.2.md)。

### Fixed

- **fix: resolve intermittent 402 caused by billing-service crash loop**：`ledger_repo.FindByDedupeKey` 在 tx 为 nil 时回退到 `r.data.DB` 不再 panic；`async_billing.processSettlement` 增加 `defer/recover` 防止单任务 panic 击穿 worker；`gatewayErrorMessage` 对每个状态码返回非空客户端安全文案（如 402 → insufficient quota）。影响 `billing-service` 与 `relay-gateway`。
- **fix(migrations): make clean-room DB provisioning work for all drivers**：`061`/`031`/`067` 改为按列/表/schema 存在性守卫（prepared statement，可重入），修复全新 MySQL 单库与 per-service schema 建库失败；`schema_split.sql` 从自动应用迁移中排除（仅作参考 DDL）；`ownership.yaml` 为 log 补 `016`、为 billing 补 `031`、移除 `037`；Postgres 基线 `000` 修复表约束内非法 `COLLATE`；SQLite runner 落实「duplicate column name 幂等 no-op」契约并拆分多列 ALTER（`009`）。

## [0.13.1] - 2026-08-01

v0.13.0 的 PATCH 修复：identity-service 调用 billing-service 的 gRPC 客户端补齐
`SERVICE_TOKEN` 携带，与 billing fail-closed 鉴权对齐；`SERVICE_TOKEN` 为空时服务
启动失败而非带病运行。详见 [release-v0.13.1.md](docs/releases/release-v0.13.1.md)。

### Fixed

- **fix(identity): attach SERVICE_TOKEN to billing gRPC client**

## [0.13.0] - 2026-08-01

生产加固版本（18 个提交）：身份/Token 安全（key 明文改 HMAC-SHA256 哈希、会话撤销、
子网限制、SERVICE_TOKEN gRPC 鉴权）、计费原子性与幂等（dual-track CAS、request_id
唯一索引、actual_cost、refund_reason、兑换码条件更新）、relay/流式稳定性（上游体
上限、SSE 无超时、真实错误比例熔断、类型化 fallback）、错误信息收敛。新增
`identity.v1.ConsumeTokenQuota` RPC 与 additive proto 字段；迁移 `072`–`076`
（additive），修复 `070` MySQL 幂等性。详见 [release-v0.13.0.md](docs/releases/release-v0.13.0.md)。

## [0.12.0] - 2026-07-30

v0.11.0 生产加固与功能补全：落地 v0.11.0 代码评审全部 CRITICAL/HIGH/MEDIUM/LOW
修复（WS 连接池跨命名空间串号、故障转移提前终止、告警永不触发、fallback 成本键错配
等），采纳 sub2api 四项更优实现，新增 Prometheus + Grafana 可观测性监控栈。迁移
`070`–`071`（additive）。详见 [release-v0.12.0.md](docs/releases/release-v0.12.0.md)。

## [0.11.0] - 2026-07-28

核心能力版本：`cache_creation` 全链路计费（五桶 token 语义、observe/charge 开关、
影子成本）、模型规范 ID 治理（canonical ID + 大小写不敏感唯一约束 + 未定价审计）、
统一路由可观测性（订阅账号 weight、selection metrics、routing-ops 运营视图与告警）、
模型版本化导入导出。迁移 `068`–`069`（additive）。详见 [release-v0.11.0.md](docs/releases/release-v0.11.0.md)。

## [0.10.2] - 2026-07-27

问题修复版本：修复 v0.10.0/0.10.1 暴露的模型路由问题（API-key 渠道与订阅账号优先级
硬编码、上游模型 ID 精确映射、GLM Responses 自定义工具 `input_schema` 422）。
详见 [release-v0.10.2.md](docs/releases/release-v0.10.2.md)。

## [0.10.1] - 2026-07-26

PATCH：修复国内订阅账户路由无法命中、上游模型 ID 大小写不敏感匹配丢失，以及 gosec /
Dependabot 安全告警。详见 [release-v0.10.1.md](docs/releases/release-v0.10.1.md)。

## [0.10.0] - 2026-07-25

重大功能版本：独立模型管理系统（账户模型映射、通配符匹配、模型别名、使用统计）与
国内订阅账户支持（智谱 GLM / MiniMax / Kimi：动态模型发现、配额查询、路由恢复探测）。
详见 [release-v0.10.0.md](docs/releases/release-v0.10.0.md)。

## [0.9.3] - 2026-07-20

基础设施升级：Kratos v2 → v3 全量升级（9 服务 + platform 共享库），proto 工具链从
protoc 迁移到 buf（生成与 lint，CI 与 Docker 构建同步接入）。
详见 [release-v0.9.3.md](docs/releases/release-v0.9.3.md)。

## [0.9.2] - 2026-07-19

PATCH：修复启用异步 Billing 与 Schema 隔离后出现的 token 扣费异常。
详见 [release-v0.9.2.md](docs/releases/release-v0.9.2.md)。

## [0.9.1] - 2026-07-19

PATCH：完成 Phase 2.4 Schema 隔离生产启用，修复配置问题并同步本地部署配置。
详见 [release-v0.9.1.md](docs/releases/release-v0.9.1.md)。

## [0.9.0] - 2026-07-18

落地架构重构 Phase 2.1/2.2/2.3/2.4/2.5 与 Phase 3.3：异步计费、渠道加权选路、日志
批量写入、per-service schema 隔离、配置热更新、WebSocket 优雅排空。
详见 [release-v0.9.0.md](docs/releases/release-v0.9.0.md)。

## [0.8.2] - 2026-07-17

v0.8.2 是 v0.8.1 之后的 PATCH 版本，聚焦 `internal/server/http.go` 架构重构拆分与 Phase 0 可观测性基线填充。无 API 破坏性变更、无数据库迁移、无部署配置变更。详见 [release-v0.8.2.md](docs/releases/release-v0.8.2.md)。

### Changed

- **`internal/server/http.go` God Object 拆分**：将 2470 行的主体文件按职责拆分为 13 个聚焦文件（Forwarder / BillingCoord / 按端点的 Handler / 响应与辅助工具），主体降至 472 行。属纯内部重构，无新增/删除端点、无路由变更、无响应格式调整。这是架构重构路线图 Phase 1 的最后一个 P0 项。
- `docs/design/BASELINE.md` 填充全部 16 处 TBD 基线数据（端点延迟、gRPC 调用延迟、缓存命中率、熔断器状态），原始压测结果归档至 `scripts/benchmark/results/phase0-baseline-2026-07-17.json`。
- `docs/TODO.md` 标记 http.go 拆分与 Phase 0 基线填充任务完成。

## [0.7.0] - 2026-07-12

v0.7.0 是 v0.6.1 之后的 Kratos 大仓结构迁移版本。范围覆盖 `v0.6.1..v0.7.0` 共 9 次提交、560 个文件、+8.6k/-3.2k 行。本版为纯结构性重构，不涉及 API 破坏性变更，不新增数据库迁移。

### Added
- 新增 `app/` 大仓结构：8 个子服务（admin/billing/channel/config/identity/log/monitor/notify）各含独立 `cmd/`、`internal/`、`configs/config.yaml`、`Dockerfile` 和 `Makefile`，共用根 `go.mod`。
- 新增 `platform/` 基础设施层：15 个基础设施包（audit/cache/config/database/events/grpc/http/logging/metrics/middleware/registry/security/tls/tracing/websocket），从原 `internal/pkg/` 提取。
- 新增 `domain/` 共享域库：`domain/subscription`（订阅域 biz+data，admin/billing/relay 共享）和 `domain/upstream`（上游 provider+credential），含 `domain/subscription/README.md` 边界与所有权说明。
- 新增 `scripts/check-architecture.sh` 架构边界守卫：7 条层级依赖规则（service→data、biz→service、biz→data、biz→DTO、data→service、data→DTO）+ wireinject 编译检查，集成到 CI。
- 新增 `AGENTS.md` 仓库级编码规约：DTO/DO/PO 三层模型与层间依赖箭头规则、加资源清单和测试策略。
- 新增 per-service `Makefile`（`make wire` / `make wire-check` / `make build`）和 root Makefile `wire` / `wire-check` target。
- 新增 admin `biz` 层（`SystemOption` DO、`SystemOptionsRepo` interface、`SystemOptionsUsecase`），将原 `service.SystemOptionsStore` 重构为符合 DTO→DO→PO 分层。

### Changed
- **Kratos 大仓结构迁移**：relay-gateway 保留为根 `cmd/relay-gateway/` + `internal/`；其余 8 个服务迁移到 `app/<service>/cmd/<service>/` + `app/<service>/internal/{biz,data,server,service}`。
- **配置布局重构**：原 `configs/<service>.yaml` 删除，各服务改为 `app/<service>/configs/config.yaml`；relay-gateway 使用根 `configs/config.yaml`。所有 Dockerfile 设 `ENV CONF_PATH=/configs/config.yaml`。
- **Dockerfile 拆分**：原根 Dockerfile 保留用于 relay-gateway，新增 8 个 `app/*/Dockerfile` 每服务独立构建；`deployments/docker/Dockerfile` 同步更新。
- `api/relay` 重命名为 `api/relay-gateway`；`internal/config` 重命名为 `internal/conf`。
- 导入路径更新：234 处 Go import 路径跨 132 文件更新以匹配新结构。
- `make wire` 全量重生成 9 个 `wire_gen.go`，完全可复现。
- `check-architecture.sh` 更新为支持 flat 结构 + root `internal/`。
- CI 工作流更新：Docker matrix 使用 `include` + path，新增架构检查 step 和生成文件新鲜度验证 step。

### Fixed
- 修复前端 web API 502/404 错误：`SubscriptionPlansPage.tsx` 移除冗余 `/api` 前缀（baseURL 已含 `/api`）；`http.go` 路由顺序修正（`operation-report` 在 prefix handler 前注册）；新增缺失的 `/admin/subscription-plans` SPA 页面路由。
- 修复 `go vet` lock-copy 告警：proto message 含 `sync.Mutex` 不可按值拷贝，改用指针。
- 修复 `go vet` "using resp before checking for errors" 告警：先捕获 err 再解引用 resp。
- 修复 relay-gateway `wire.go`：`newApp()` 返回 error，启动期 discovery/registrar/gRPC client/cache/subscription repo/mTLS 错误正确传播，mTLS 改为 fail-closed。
- 修复 admin `http_test.go`：适配 `service.SystemOptionsStore` → `biz.SystemOptionsRepo + SystemOptionsUsecase` 重构。
- Docker Compose 新增 `SSL_CERT_FILE` 环境变量和 `ca-certificates` 卷挂载，修复 relay-gateway 容器内上游 HTTPS 调用。

## [0.6.1] - 2026-07-09

### Fixed
- 修复 admin logo 静态资源 404（`8c6da60`）。
- 刷新 v0.6.0 品牌和 README（`76628b8`）。

## [0.6.0] - 2026-07-09

### Added
- 新增 `/v1/subscription/usage` API：返回用户订阅额度、已用量、剩余额度和下一次刷新时间。
- 新增管理后台订阅套餐管理页（`SubscriptionPlansPage.tsx`），支持套餐创建、上下架、价格与有效期配置。
- 支付订单新增 `plan_snapshot`，订单发放与后续套餐上下架/删除解耦。
- 新增订阅订单续费幂等、退款撤销/缩短订阅、订单到订阅确定性关联。
- 新增订阅账号治理自动化：fixed 策略 daily/weekly 额度重置、账号恢复、额度告警、治理指标与 runbook。
- 新增 Relay 订阅路径压测脚本、报告生成与压测 runbook。
- 新增单用户单 active 订阅数据库唯一约束（`059`）。

### Changed
- 订阅额度刷新锚点修正为自然日/自然周窗口，并返回 next-refresh times。
- 订阅运营报表新增 usage/fallback ratio。
- 默认订阅用户 RPM 限制保持关闭。

### Fixed
- 支付提交阶段吸收实际订阅用量，避免预估用量和最终账本不一致。
- 修复退款路径在用户已购买新订阅后错误回退到"当前 active 订阅"的风险。
- 修复并发创建 active subscription 时可能绕过业务层检查的问题。

### Security
- gosec SAST：0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks secret scan：0 leaks。
- 临时 replace `github.com/go-kratos/kratos/v2` 到包含 CVE-2026-6993 修复的提交。

## [0.5.0] - 2026-07-05

### Added
- 新增订阅套餐数据层与购买发放流，支持通过 `subscription_plans` 管理套餐并在支付订单成功后分配订阅权益。
- 订阅账号新增本地额度、5h 额度、RPM、会话窗口、额度重置配置和批量额度管理能力。
- 订阅账号额度事件增加幂等记录与聚合分析，管理后台可查看额度事件和账号额度状态。
- relay-gateway 新增 Redis-backed 订阅账号并发控制，并在多副本部署下共享账号并发槽位。
- 管理后台新增用户 RPM 限制展示、订阅账号额度/RPM/窗口控制项和成本分析维度补充。

### Changed
- Docker 构建新增自包含 `go-deps` stage，CI buildx matrix 不再依赖本地 `micro-one-api/go-deps:latest` 镜像。
- `scripts/deploy.sh` 增加依赖镜像 hash tag、可选并行构建和更稳的服务构建流程。
- `scripts/test-e2e-flow.sh` 保留 compose 实时输出，并在 compose 健康检查竞态时重试一次。

### Fixed
- 修复 GitHub Actions Docker matrix 因尝试拉取不存在的 `micro-one-api/go-deps:latest` 而全部失败的问题。
- 修复 gosec G115/G101/G705 告警，新增安全窄化转换工具并标注确认安全的误报点。
- 修复 gitleaks 历史占位符命中，当前文档示例改用环境变量并为确认误报 fingerprint 建立 `.gitleaksignore`。
- 修复订阅账号额度状态展示不完整和用户 RPM 限制展示缺失的问题。

### Security
- gosec SAST：0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks secret scan：0 leaks。
- 临时 replace `github.com/go-kratos/kratos/v2` 到包含 CVE-2026-6993 修复的提交,规避 Kratos `http.DefaultServeMux` confused deputy 告警。

## [0.4.0] - 2026-07-04

### Added
- relay-gateway Responses 路径新增 §5 多层调度:优先复用 `previous_response_id` route,其次复用 `session_hash` sticky channel,最后回退到原 `RelayUsecase.Plan`。
- Responses HTTP/WS sticky session 支持 `session_hash` / `sessionHash` body 字段和 `X-Session-Hash` / `OpenAI-Session-Hash` header,并在 Redis sticky store 中使用独立 `openai_ws_session:` namespace。
- relay-gateway 订阅账号路径新增 §6 `AccountPool` + `RuntimeBlocker` + FailoverLoop:订阅账号选号会跳过运行时熔断账号,subscription adaptor 在上游网络错误、`429`、`5xx` 时短 TTL 熔断当前账号并切换下一个账号重试。
- §7 新增 Codex 5h/7d 配额快照解析与 `account_quota_snapshots` 落点,relay-gateway 可从 Codex 上游响应记录 quota snapshot 并在阈值耗尽时自动暂停订阅账号。
- §7 新增 channel-service 订阅账号 OAuth 授权码绑定端点:支持 Claude/Codex `auth-url` 与 `exchange`,使用进程内 5 分钟 session_store,exchange 后创建 `subscription_accounts` 记录。
- §8 新增订阅系统 Prometheus 指标:覆盖业务订阅配额检查/用量回写、subscription adaptor 请求、failover、runtime block、上游错误透传和 Codex quota snapshot/auto-pause。

### Fixed
- `previous_response_id` 解析拒绝 `msg_` message id,避免把 message id 误当 Responses route id。
- 订阅账号上游 `401` / `403` / `429` / `cyber_policy` 错误改为按 §7 ErrorPassthrough 透传状态码、body 与 `Retry-After`,不再统一包装成网关错误。
- compose E2E mock channel 状态修正为 `ChannelStatusEnabled = 1`,避免模型列表可见但 relay 选号过滤后 chat completion 返回 500。
- `phase1_indexes.sql` 不再重复创建 `billing_ledgers.idx_created_at`,避免干净 MySQL volume 首次初始化时因重复索引中断。
- 订阅套餐支付宝支付成功后由 `billing-service` 自动发放 `user_subscriptions`,并阻止订阅订单在未配置发放器或缺少 `group_id` 时被误标为 `issued`。

## [0.3.1] - 2026-06-29

### Added
- 新增 SQLite3 Lite 部署模式：`deployments/docker-compose/docker-compose.lite.yml`、`.env.lite.example`、`migrations/sqlite/000_create_full_schema.sql`，单机部署可不再启动 MySQL 容器。
- 新增 Postgres 部署模式：`deployments/docker-compose/docker-compose.postgres.yml`、`.env.postgres.example`、`migrations/postgres/000_create_full_schema.sql`。
- 新增统一数据库打开器 `internal/pkg/xdb.Open` / `OpenSQL`，支持 `mysql`、`sqlite3`、`postgres` 三种方言，并支持从 DSN 推断 driver。
- 新增 SQLite3/Postgres 迁移目录说明与 Issue #4 落地文档。

### Changed
- 各服务数据库配置改为通过 `DATABASE_DRIVER` 选择方言，默认保持 MySQL 兼容。
- `cmd/migrate` 支持按 driver 选择表存在性探测与 Postgres `$N` 占位符转换。
- 主服务 Docker 镜像切换为 CGO-enabled Alpine 构建，以支持 `go-sqlite3`。
- MySQL 分区维护在 SQLite3/Postgres 下自动 no-op。

### Fixed
- 修复 `admin-api` system options 在 SQLite3/Postgres 下的连接、占位符与 upsert 兼容性。
- 修复 billing/log 聚合查询中的 MySQL 专用日期函数，使其兼容 SQLite3/Postgres。
- 修复 Postgres baseline 中 `time.Time` 字段类型与 GORM 模型不一致的问题。

## [0.3.0] - 2026-06-29

### Added
- **混合中转网关**:relay-gateway 新增 `Adaptor` 抽象层,把 Codex/Claude OAuth 等订阅账号与 30+ 厂商 API Key 通道统一接入,内含 `apicompat` 四格式转换矩阵(Anthropic ⇄ Responses ⇄ ChatCompletions,含流式 SSE 状态机)与 `identity` 指纹/伪装(metadata.user_id 重写、anthropic-beta 计算、fingerprint 注入)。
- **订阅账号端到端**:`subscription_accounts` 表 + 5 个 admin RPC(列表/创建/更新/删除/启停),管理后台新增「订阅账号」页,使用/费用/日志/渠道健康均按 `subscription_account_id` 维度归因;`scripts/import-subscription-creds.py` 一键导入凭据。
- **架构重构 P0**:http.go 从 2,391 行拆分为 `Orchestrator` + `Forwarder` + `Handler` 矩阵 + `http_raw_helpers` + `http_adaptor`;identity/channel/billing/log 四个 gRPC 客户端接入 sony/gobreaker 熔断 + 4 种降级策略(cache/async/noop/identity)。
- **架构重构 P1**:multi-level cache(L1 内存 + L2 Redis)+ singleflight 防击穿;计费改为异步预扣/批量结算;`SelectChannel` 加权轮询 + 失败率衰减;`logs`/`billing_ledgers`/`billing_reservations` 批量写。
- **架构重构 P2**:`logs` 表按月分区(partition cron 持续维护);统一幂等中间件;relay-gateway 优雅排空(graceful drain);gRPC mTLS 服务间认证;审计日志覆盖 admin 写操作。
- **可观测性补齐**:新增 30+ Prometheus 指标,涵盖 Relay/Selector/Cache/Breaker/Billing/Partition 多维度。
- 新增数据库迁移:`034_create_subscription_accounts.sql`、`035_add_subscription_account_quota_fields.sql`、`036-038_add_subscription_account_id_to_*.sql`、`phase1_indexes.sql`、`phase3_partitioning.sql`。
- 依赖:由 `encoding/json` 切换到 `bytedance/sonic` 提升 apicompat 序列化吞吐。

### Changed
- `internal/relay/server/http.go`:从 2,391 行精简到 1,862 行,只保留路由注册 + 中间件装配。
- 鉴权流程:本地 Auth Cache 命中时不再发起 gRPC;Token 状态变更通过 Redis Pub/Sub 广播失效。
- 计费流程:`ReserveQuota` 改为异步队列提交,失败回退到同步路径(降级策略之一)。
- 渠道选择:同优先级内由纯随机改为加权轮询 + 失败率衰减。
- `scripts/deploy.sh` 部署脚本加固,补齐 migrate 镜像调用与回滚路径。

### Fixed
- 订阅账号健康检查 404(`subscription_account_id` 未透传到 channel-svc gRPC)。
- 编辑订阅账号保存崩溃(`null` → `""` 规范化)。
- 成本归因:cost-analysis 页面之前未按订阅账号分桶,现在全链路带上 `subscription_account_id`。
- gRPC 客户端超时在长上下文场景下误杀,改为可配置 `grpc.dial_timeout` / `call_timeout`。
- relay `Content-Length` 校验对流式响应误判 411,改为仅在非流式路径校验。

### Security
- gosec SAST:本次新增代码 0 issues。
- govulncheck SCA:全代码库 0 vulnerabilities。
- gitleaks 密钥扫描:本次新增代码 0 leaks。
- OAuth 凭据存储:At-Rest 加密 + KMS-style 密钥派生;`scripts/import-subscription-creds.py` 在 stdin 接受凭据而非 argv。

## [0.2.9] - 2026-06-26

### Added
- 新增 Codex Responses WebSocket 协议入站支持：relay-gateway 在 `POST /v1/responses` 上探测 `Upgrade: websocket` 请求，自动切换为 WebSocket 双向转发，可直接作为 Codex CLI 的 WebSocket 后端；非 Upgrade 请求仍走原 HTTP/SSE 路径，向后兼容。
- 客户端 ↔ 上游双向 pump 逐帧镜像转发 Codex 事件，按 turn 解析 usage 并复用现有 reserve / commit / release 计费与 usage 日志链路。
- 新增上游连接池：每渠道空闲连接缓存，经 Ping 健康检查后复用，支持每渠道最大连接数（默认 8）与空闲淘汰（默认 5 分钟）。
- 新增跨进程会话粘滞：`response_id → channel_id` 绑定双写本地热缓存与 Redis，多副本部署下保持多轮会话同一渠道；未配置 Redis 时降级为纯内存。
- 新增多渠道故障转移：上游 dial 失败或首字节前的可重试错误自动按优先级换渠道（默认最多切换 2 次），字节已下发即停止 failover。
- 新增 `openai_ws` 配置块（超时、连接池、failover、sticky、Redis），所有字段可选。
- 依赖新增 `github.com/coder/websocket v1.8.14`。

### Security
- gosec SAST：本次新增代码（`openai_ws_*`）0 issues。
- govulncheck SCA：0 vulnerabilities。
- gitleaks 密钥扫描：本次新增代码 0 leaks（全仓 2 条命中为 README/推广文档中的 `YOUR_TOKEN` 占位符，非真实密钥）。

## [0.2.8] - 2026-06-25

### Added
- 新增 Anthropic Messages API 入站端点 `POST /v1/messages`，使 relay-gateway 能直接对接 Claude Code CLI 及原生 Anthropic SDK 客户端。
- 支持 Anthropic Messages 格式与内部 OpenAI 兼容选路/计费链路的双向转换：string/array content blocks、system prompt、tool_use / tool_result 工具调用。
- 流式响应支持 OpenAI SSE → Anthropic SSE 事件序列转换（message_start / content_block_start / content_block_delta / content_block_stop / message_delta / message_stop）。
- 支持 thinking-mode 模型（DeepSeek-R1、GLM-5.x），将 `reasoning_content` 转换为 Anthropic `thinking` content block。
- 鉴权支持 `x-api-key`（Anthropic 原生）和 `Authorization: Bearer` 两种方式。

### Fixed
- 流式 SSE 中途写入错误不再触发二次 `WriteHeader`。
- `Plan()` 失败返回 Anthropic 错误信封格式，而非 OpenAI 格式。
- 新增 `max_tokens` 上限保护（64000），防止资源耗尽。

### Security
- gosec SAST / govulncheck SCA / gitleaks 密钥扫描全部通过（0 issues / 0 vulns / 0 leaks）。

## [0.2.7] - 2026-06-24

### Added
- 通知记录新增最后一次投递失败原因字段，notify-worker 记录发送错误，管理后台通知面板在失败通知中展示失败原因。

## [0.2.7] - 2026-06-24

### Added
- Prometheus 指标补齐对账任务和渠道健康探测观测：新增对账运行次数/耗时/差异类型计数，以及 monitor-worker 渠道健康 sweep/probe 成功率、耗时和失败原因指标。

## [0.2.6] - 2026-06-20

### Added
- 管理后台新增通知面板，支持查看通知历史、按发送状态筛选、刷新列表和 pending 数量徽标。
- 管理后台新增渠道健康与成本分析页面，补齐健康趋势、成本、收入、毛利等可视化图表组件。

### Fixed
- 管理后台通知接口改为经 `admin-api` 代理到 `notify-worker`，避免前端直接依赖 worker 地址。
- 通知面板兼容 `notify-worker` 直接返回 `{items,total}` 的列表响应格式。
- `admin-api` 补齐 `/admin/channel-health`、`/admin/cost-analysis` 等 SPA 路由，修复刷新或直达页面时的路由回退问题。

## [0.2.5] - 2026-06-19

### Added
- `notify-worker` 新增企业 IM 通知通道：企业微信、钉钉、飞书/Lark 和 Slack。
- 渠道健康告警与对账告警的通知类型支持 `wecom`、`dingtalk`、`feishu`、`slack`。
- 新增 `NOTIFY_WECOM_WEBHOOK_URL`、`NOTIFY_DINGTALK_WEBHOOK_URL`、
  `NOTIFY_FEISHU_WEBHOOK_URL`、`NOTIFY_SLACK_WEBHOOK_URL` 配置项。

### Changed
- 企业微信和钉钉支持配置完整 webhook URL，也支持仅配置 key / access_token 自动拼接。
- 部署文档、README 与示例环境变量补齐企业 IM 告警配置说明。

## [0.2.4] - 2026-06-19

### Added
- 补充渠道健康告警配置文档与 docker-compose 示例环境变量说明。

### Changed
- README 最新发布指针更新到 v0.2.4。

## [0.2.3] - 2026-06-18

### Added
- 渠道健康状态与自动熔断：relay 上游调用会回写成功/失败和响应时间，
  channel-service 连续失败达到阈值后跳过该渠道，冷却期后允许半开恢复；
  管理后台渠道列表展示健康状态、失败次数和熔断冷却时间，并支持手动触发
  `/models` 健康探测。
- monitor-worker 支持定时探测启用渠道的 `/models` 健康状态。
- 新增 `CHANNEL_HEALTH_FAILURE_THRESHOLD`、`CHANNEL_HEALTH_COOLDOWN`、
  `CHANNEL_HEALTH_CHECK_ENABLED`、`CHANNEL_HEALTH_CHECK_INTERVAL`、
  `CHANNEL_HEALTH_CHECK_TIMEOUT` 配置项。

## [0.2.2] - 2026-06-15

### Added
- `notify-worker` 支持 pending 通知实际投递：webhook/event 走 HTTP POST，email 走 SMTP，
  并支持失败重试与最终 failed 状态。
- CI Docker 构建矩阵覆盖全部服务镜像。

### Changed
- 对账告警通知类型可通过 `RECON_ALERT_NOTIFY_TYPE` 配置；
  `RECON_ALERT_RECIPIENTS` 文档改为 webhook URL / email 目标语义。

## [0.2.1] - 2026-06-12

### Added
- **管理后台 Top 用量图表**: `OverviewPage` 增加 Top N 渠道 / 模型用量图表
  (`web/src/pages/admin/OverviewPage.tsx`),导航接入"用量排行"入口
  (`AppNavigation`),并补齐对应单测 (`AppNavigation.test.tsx`)。

### Changed
- 前端构建优化: `web/vite.config.ts` 增加 chunk 拆分策略,
  拆分 vendor / 路由懒加载 / ECharts 等公共依赖,降低首屏包体积。

### Fixed
- 端到端测试与 docker-compose 环境文档对齐: 补齐
  `deployments/docker-compose/.env.example` 的 e2e token / 通知相关变量;
  `test/e2e/main.go` 修正 token 流程以匹配实际 relay 行为;
  同步更新 `.env.example` 与 `docs/deployment.md` / `README.md` 文档。

## [0.2.0] - 2026-06-10

### Added
- **成本与利润分析 (Phase 2)**: `billing_ledgers` 新增 `upstream_cost` 字段(`migrations/029`),
  relay 提交时按渠道侧定价计算上游成本并写入账本;新增收入/成本/毛利相关统计维度。
- **多维 SQL 用量聚合 (Phase 1)**: `billing` 服务新增多维聚合 RPC(按用户/渠道/模型/Token/分组/小时|日),
  取代原先 admin 的 1000 条内存抽样统计;`migrations/028` 为 `billing_ledgers` 补充 `(created_at)`、
  `(channel_id, created_at)`、`(model_name, created_at)` 索引。
- **渠道对账 (Phase 3 起步)**: `RunReconciliation` 增加渠道维度校验
  (本地累计渠道用量/成本 vs 渠道 `used_quota`),以及 ledger/log 双写一致性比对。
- **对账差异告警**: `billing-service` 检测到对账差异时通过 gRPC 投递到 `notify-worker`,
  按 `RECON_ALERT_RECIPIENTS` 创建通知;出错仅记日志,不阻塞对账任务。
- **成本健康 dashboard**: 管理后台新增成本/毛利/渠道余额健康面板
  (基于 `reconciliation_runs` + 账本聚合)。
- **缓存 token 用量展示**: 用量统计与账本写入支持 `cache_read_tokens` 字段
  (`migrations/031`),后台与 `/v1/usage` 路径可见缓存命中率相关指标。
- **管理后台 i18n(zh-CN)**: 关键文案本地化。

### Changed
- 管理员日志/用量统计从内存抽样改为调用 billing 真实聚合,数字可信。
- 对账任务支持通过 `WithNotifier` / `WithRecipients` 选项装配通知;
  不配置通知端点时退化为仅日志模式(向后兼容)。

### Fixed
- 修复 relay 流式响应 logger 偶发空指针 panic。
- dashboard token 趋势 Y 轴在数值跨度大时改为紧凑单位显示。

## [0.1.1] - 2026-05-09

### Added
- 渠道余额刷新适配 OpenAI/DeepSeek/OpenRouter/SiliconFlow 等 provider。
- Docker builder stage 增加 `--platform=$BUILDPLATFORM` 多架构支持。
- `admin-api` 支持外部管理前端构建产物托管。

## [0.1.0] - 2026-05-06

首个公开版本,核心微服务边界确立:
- `relay-gateway`、`admin-api`、`identity-service`、`channel-service`、
  `billing-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`。
- OpenAI 兼容 API 网关、用户/Token/额度/账务基本链路、Docker Compose 部署。
