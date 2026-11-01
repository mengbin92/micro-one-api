# Micro-One-API v0.38.0 发布：B 阶段——忙闲时段计价、原生 Gemini 入站与托管备份控制面

> 2026-11-01 · 上一版：[v0.37.1](./release-v0.37.1.md)（2026-10-29）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.38.0)

v0.38.0 是 v0.37.1 之后的 **MINOR 功能版本**：集中发布下一阶段排期的 B 阶段三个工作包——B1 冻结上下文档与忙闲时段计价、B2 原生 Gemini `v1beta` 入站、B3 托管 MySQL/Redis 备份控制面，并附带三项安全扫描修复。版本判 MINOR 而非 PATCH 的依据：B2 新增公开入站契约、B3 新增受 IAM 保护的管理面 RPC 与独立 host worker、B1 新增计价 owner DTO 与公开 UI，且新增 Billing 146、Admin 147 两个三库迁移。

**三个工作包的代码均已先于本 tag 部署生产**（2026-10-30，关闭态或验收边界内），各自部署与验收证据见下文引用；本次发版补齐 release note / CHANGELOG / README / tag 工件，由 CI 构建多架构镜像并创建 GitHub Release。计价（B1）与备份控制面（B3）在生产保持默认关闭；Gemini 入站（B2）在生产按验收边界运行，真实来源/SDK/付费验收保留。

## 功能

### 1. B1：冻结上下文档与忙闲时段计价

**根因**：v0.37.0 的档位计价只有单来源/模型的五桶售价，缺少长上下文与峰谷（忙闲时段）定价，也无法在准入时保留日历与上下文决策供结算复核。

**新增**：准入时冻结时段带，按已核验的输入选择整请求五桶价格，并在账本与快照中保留不可变证据；补齐 DeepSeek 有界 JSON/SSE 处理、缓存别名校验、转发限额完整性、owner DTO、三库迁移 Billing 146 与计价管理 UI。缺失证据保持显式可审计，保留既有账本 hash 与默认值。计价默认关闭，真实阈值/价格/节假日日历由维护者确认后启用。

**影响服务**：`billing-service`、`log-service`、`relay-gateway`、`identity-service`、`admin-api` 与 Web。生产已于 2026-10-30 完成关闭态部署（Billing 146 与既有 Log 145 落库），健康、运行账号权限、gRPC 与公网鉴权通过，其余 16 个容器未变；真实收费启用待维护者确认阈值与价格。见 [B1 验收](../runbooks/b1-context-time-pricing-acceptance-2026-10-29.md)。

### 2. B2：原生 Gemini `v1beta` 入站

**根因**：Gemini SDK 与 REST 客户端此前没有原生生成入口，既有上游桥接也无法证明原生缓存与思考 token 的用量，不能安全结算。

**新增**：经既有鉴权、路由、reservation、Billing 与 Log owner 边界的 `v1beta` generateContent / streamGenerateContent 文本与函数调用入站，支持 JSON 与 SSE；显式拒绝媒体、隐藏状态、服务端工具与 durable 试点路径；规范化原生凭据后再进共享中间件；完整 usage 核验通过前扣留 SSE 终止帧；保留 canonical 零用量并在响应或结算失败后防重放。

**影响服务**：仅 `relay-gateway`。生产已于 2026-10-30 部署，14 项公网检查与 20 个其他容器不变性通过；不扩大既有上游契约试点、不改历史账本。实际来源/SDK 版本/真实付费用量待外部条件。见 [B2 验收](../runbooks/b2-native-gemini-acceptance-2026-10-30.md)。

### 3. B3：托管 MySQL/Redis 备份控制面

**根因**：平台缺少 admin 统一管理的备份策略、任务回执、审计与可验证恢复，人工备份无法证明 RPO/RTO。

**新增**：admin 侧控制面——策略、任务回执、append-only 审计与 worker 状态统一存放于 `oneapi_admin`（迁移 147，含 PostgreSQL/SQLite 镜像），经 admin HTTP/RPC 暴露，受全局 IAM `admin.backups` 执行点与 A2 step-up 会话证明保护。私有 host worker 执行备份/恢复：host + MySQL 双锁调度、stop-the-world 一致性、age 公钥加密、私有 S3 条件不覆盖写入、可存活于控制库丢失的外部恢复索引、保留策略与可恢复中断日志；`admin-api` 只持控制库连接，从不接触制品凭据。

工程验收（隔离 MySQL 8.4 / Redis 8.4 / S3 兼容存储）：完整门禁、三库真实 owner 与 step-up HTTP/gRPC、桌面/移动 Chrome、amd64 静态 worker 构建，以及空环境恢复——从恢复的配置清单解密原 API Token、MFA 与渠道密钥，钱包余额与正金额未结算预扣幂等核对（21 MiB 加密夹具，恢复导入 2.371s、下载至 owner 核验 3.027s，对标工程 RPO 24h / RTO 60min）。

**影响服务**：`admin-api`、`identity-service`、Web 与独立 host worker（未随本版安装）。生产已于 2026-10-30 关闭态部署（迁移 147 经 mysql 容器本地 socket 落库并复核），控制面未启用、无 host worker；回滚锚点为镜像 tag `rollback-20261030-225430` 与 compose/web dist 备份。真实私有 S3、host worker 安装与运行账号授权、生产 RPO/RTO/容量验收及受控启用待维护者提供条件；不执行生产覆盖恢复。见 [B3 验收](../runbooks/b3-managed-backup-acceptance-2026-10-30.md)与[生产部署证据](../runbooks/evidence/b3-production-deployment-2026-10-30.json)。

## 安全修复（随本版一并发布）

### 4. SMTP 头注入与响应包装器 Unwrap（P1）

**根因**：mail sender 将邮箱/主题直接插入 RFC822 头未剥离 CR/LF（真实 SMTP 头注入）；多处 response-writer 包装器缺少 `http.ResponseController.Unwrap()`，Flush/Hijack/Push 被包装链隐藏。

**修复**：identity 邮件发送加固；admin 资产 gzip/immutable 包装器与幂等 writer 暴露 Unwrap；`challenge.go` 的 SHA-256 澄清为 Redis key 寻址而非口令哈希（code-scanning 误报）。

**影响服务**：`identity-service`、`admin-api`。gitleaks 两条微信测试夹具发现转为 baseline 登记，RBAC 源码摘要与校验门禁同步刷新。

## 兼容性说明

- **API**：B2 新增 Gemini `v1beta` 入站公开契约；B3 新增受 IAM 与 step-up 保护的管理面 RPC；B1 无新公开 RPC，新增计价 owner 字段与 UI。既有 OpenAI 兼容契约不变。
- **数据库**：新增 Billing 146 与 Admin 147 两个三库迁移，生产均已落库；重复执行应为 no-op。未新增 Identity/Log 迁移（Log 145 为 v0.37.0 既有）。
- **配置与依赖**：无强制新配置。计价与 `BACKUP_CONTROL_ENABLED` 默认关闭；Gemini 入站随既有鉴权/路由配置生效。无新增第三方运行时依赖。
- **资金与历史记录**：不重算历史账单，不补造执行时成本；B1 保留冻结快照与既有账本 hash，B2 不扩大上游契约试点、不改历史账本。
- **范围**：正常 compose 安装者按镜像 tag 更新即可；B3 的 host worker 为独立静态二进制，不由 compose 镜像分发。

## 升级步骤

1. 生产侧三个工作包已于 2026-10-30 按各自证据完成部署（B1 六服务 + Web，B2 仅 Relay，B3 Identity/Admin/Web + 迁移 147），本 tag 发布后按既有流程核对运行镜像与本版 commit 一致即可；新环境按 compose 镜像 tag 拉取。
2. 迁移：Billing 146、Admin 147 走既有迁移 CLI；已落库环境重复执行为 no-op，用运行账号验证新列/新表可读可写。
3. 保留计价与备份控制面的现场开关状态；真实阈值/价格、私有 S3、host worker 安装与生产 RPO/RTO 确认前不启用对应功能。
4. 验证：`docker ps` 健康、镜像摘要、匿名拒绝与已认证只读链路；Gemini 入站按 B2 验收的拒绝/鉴权分支抽测，不调用付费推理。
5. 将实际核对结果回填各验收 runbook 后视为升级完成。

### 回滚限制

- B1：保留 Billing 146 新增列与冻结时段带证据；不因回滚删除字段或改写历史快照。
- B2：Relay 可回滚至上一镜像，Gemini 入站消失，既有 OpenAI 兼容契约不受影响；已结算的 Gemini 请求不回放、不退款，由 Billing owner 核对。
- B3：保留 Admin 147 表与 append-only 审计；控制面默认关闭，回滚不触发也不依赖备份任务；不执行生产覆盖恢复。

## 验证

| 状态 | 证据与边界 |
| --- | --- |
| ✅ | 三个工作包均通过 `make verify` 全量门禁（build/vet/test/race/lint/格式/架构/迁移/部署文档检查）与各自红绿回归 |
| ✅ | B1：90 项三库 owner 场景、8 项桌面/移动浏览器检查、生产五查（健康/摘要/权限/gRPC/公网）；迁移重复执行 no-op |
| ✅ | B2：完整门禁 + 15 项真实 SQLite/MySQL/PostgreSQL owner 场景；生产 14 项公网检查与 20 容器不变性 |
| ✅ | B3：隔离空环境恢复核对原始 Token/MFA/渠道密钥解密、钱包与正金额预扣幂等；生产关闭态部署与回滚锚点 |
| ✅ | 安全修复：code-scanning 8 项告警关闭（SMTP 头注入、Unwrap、误报澄清），gitleaks 两条 baseline 登记 |
| ⏳ | 保留项：B1 真实阈值/价格/节假日与付费验收；B2 真实来源/SDK/付费用量；B3 真实私有 S3、host worker、生产 RPO/RTO 与受控启用；均不因本 tag 通过 |

## 完整变更日志（v0.37.1 → v0.38.0）

```
15c39c23 test(relay): mirror billing tier contract in fixture mocks
d627f7e9 docs(backup): record post-deploy gosec hardening in B3 deployment evidence
14f58fec feat(backup): managed MySQL and Redis backup control plane (B3)
7d3f4cbf feat(relay): add and deploy native Gemini ingress
62edafdc feat(billing): add frozen context and time-band pricing
80a16c1a chore(rbac): add local digest refresh script and verify gate
7b837310 chore(rbac): refresh source digests after security fix
9148b178 chore(security): baseline two gitleaks findings from wechat test fixtures
a142924f fix(security): close code-scanning alerts on mail sender and response writers
```
