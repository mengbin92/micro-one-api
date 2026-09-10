# Micro-One-API v0.27.0 发布：Lite 部署收口与可审计账务工具

> 2026-09-10 · 上一版：[v0.26.6](./release-v0.26.6.md)（2026-09-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.27.0)

v0.27.0 是 v0.26.6 之后的 **MINOR Lite 部署与账务审计版本**，包含 13 个提交
（2 feat/fix 功能 + 3 build/deps/security + 5 docs + 1 merge + 2 发布前修复）。它修复
SQLite Lite 从空环境到首个聊天请求的结算死锁和方言 schema 缺口，补齐可重复部署
Quickstart 与只读历史账务审计入口，并固化 K3 canonical charge 的验收 SQL 口径与
安全依赖基线。

**无公共 API / proto 变更**。SQLite / PostgreSQL 包含方言迁移 `011`（补齐渠道字段）
和 `090`（修复账务时间类型）；MySQL schema 不变。Lite 示例要求启动前生成
`CHANNEL_ENCRYPTION_KEY`，新增 bootstrap 密码写入卷内 `0600` 文件而非服务日志。

## 1. Lite 部署从空环境可直接完成首个请求

**根因**：Lite 将 SQLite 连接池限制为 1 后，chat 结算事务中的账户、订阅组和定价读取
仍逃出事务；动态定价又在连接被占用后加载，导致同一连接自等待，最终表现为 chat 502。
同时，SQLite/PostgreSQL 基线遗漏部分渠道字段，账务时间列沿用整数声明，与 GORM
`time.Time` 映射不兼容，旧库无法安全升级。

**修复**：

- 结算事务改用事务绑定的账户、订阅组和动态定价快照读取；SQLite 单连接下钱包与
  订阅双轨结算可完成、重试幂等，余额与账务守恒。
- SQLite/PostgreSQL 新增 `011_add_channel_oneapi_fields.sql`，补齐 `channels.model_mapping`
  与 `channels.system_prompt`；基线同步声明这些字段。
- 新增 `090_fix_billing_timestamp_types.sql`，把账务时间列修正为方言对应的时间类型；
  旧数据中的 `0` 保持 NULL，字符串时间原样保留，Unix 秒自动转换。
- Lite Compose 使用独立 project、随机本地端口、非 root 数据卷和私有 bootstrap 密码文件；
  后续启动只应用待执行增量迁移。

**影响服务**：`billing-service`、`channel-service`、`identity-service`、Lite Compose 中的
全部服务；MySQL 运行时 schema 不变。

## 2. 新增历史 usage 只读审计入口

**根因**：历史 consume ledger 只靠 token 算术无法证明当时的解析口径、订阅归属、定价
快照和实扣金额，人工导出难以重复复核，且任何写式修复都会放大账务风险。

**修复**：

- 新增 `scripts/reconcile/history-audit`，在只读、可重复读事务中 SELECT billing ledger、
  pricing snapshot 和 reservation 三表，输出确定性 JSON 或 CSV。
- 报告将请求分为 `verified`、`candidate`、`unknown`：只有完整一致的 v1 usage 证据、来源
  和 SHA-256 校验通过的冻结价格快照才复算 canonical 差额；证据不足不反推价格、不自动
  分摊、不输出冲正指令。
- 订阅 / 钱包双轨 ledger 按请求合并原始实扣，保留每条 ledger 证据；差额只出现在请求层。
- 工具不提供 billing RPC、apply、退款或 reversal 路径；实库模式仅要求三表 SELECT 权限。

**影响服务**：新增离线运维工具，不改变线上服务接口与账本数据。

## 3. Canonical charge 验收与回滚口径固化

**根因**：冻结 72h 门禁 SQL 用 MySQL DECIMAL `ROUND` 重建逐桶成本，而生产 Go 代码使用
float64 左结合乘法加 `math.Round`；十进制 `.5` 边界低 1 ULP 时会产生 8 条“少扣 1 quota”
的假差异。legacy 重建又按 ledger `usage_semantics` 推断是否扣除 cache-read，但生产
`PromptExclusive` 实际由订阅平台或渠道类型决定，K3 回滚演练暴露 17 条新样本口径不一致。

**修复**：

- 门禁 SQL 将冻结价格操作数转换为 DOUBLE，按 `FLOOR(x)+(x-FLOOR(x)>=0.5)` 位级复刻
  Go `roundScaled`；5279 个边界 / 随机用例与生产零偏差。
- legacy 重建按生产规则推导每行 `PromptExclusive`：LEFT JOIN 订阅账号与渠道，按平台或
  渠道类型判定是否从 flat prompt 中扣除 cache-read。
- 冻结 K3 窗口复验 `allowlisted_wrong_cost=0`、`nonallowlisted_wrong_cost=0`。
- 2026-09-10 完成配置-only 回滚演练：observe 阶段 19 条 K3 全部按 legacy 实扣（17 条高于
  canonical）；切回 charge 后 2 条全部按 canonical 实扣（1 条低于 legacy）。allowlist
  SHA-256 保持不变，dedupe / claim / reservation / billing-log 多重集均一致。
- 维护者书面决策：GLM-5.3 扩面与全量 charge 不进入 v0.27，继续维持 K3 精确来源 charge。

**影响服务**：只读验收 SQL 与文档，不改变生产 billing 配置、历史 ledger 或余额。

## 4. Lite 文档、演示与协议文档

**根因**：个人部署虽有 SQLite Compose 骨架，但缺少从 clone、密钥、初始密码、渠道、钱包、
Token 到首个聊天请求的完整验证路径；LLM 计费链路和三协议转换也缺少面向维护者的单一
说明入口。

**修复**：

- 新增 `docs/quickstart-lite.md`：空环境启动、私有初始密码、渠道 / 钱包 / Token / Chat
  操作、停机备份恢复、升级边界和可重复 smoke。
- `scripts/test-lite-smoke.py` 在独立临时目录、空卷、随机端口和本地 mock 上游下完整验证
  bootstrap、登录、建渠道、充值、Token、models、chat、重复迁移和重启持久化。
- 更新脱敏截图与 `lite-quickstart.mp4` 演示；Issue #6 已回填并关闭，Issue #9 已回填并保留
  反馈入口。
- 新增 LLM 计费实现讲解和 Chat / Responses / Messages 协议转换指南。

**影响服务**：文档与验证脚本；不新增个人版架构或单二进制形态。

## 5. 安全与依赖升级

**根因**：gRPC CVE-2026-84445 影响已发布依赖基线；Web 依赖包含 `hono` 与 `js-yaml`
兼容更新，Lite 示例还包含确定性加密密钥，容易被直接复制到真实部署。

**修复**：

- `google.golang.org/grpc` 升级到 1.83.2，并同步 `golang.org/x/net` 0.58.0。
- `hono` 升级到 4.13.7，`js-yaml` 升级到 4.3.2，`@vitest/mocker` 升级到 5.0.0。
- Lite `.env` 示例的 `CHANNEL_ENCRYPTION_KEY` 改为空必填，并在 Quickstart 中生成唯一
  32 字节 ASCII key；仅历史占位指纹加入 Gitleaks allowlist。

**影响服务**：发布制品中的 Go/Web 依赖基线与 Lite 部署安全默认值。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：MySQL 无新增迁移，仍是 `089`。SQLite / PostgreSQL 新增 `011` 与 `090`：
  `011` 补齐旧基线缺失的渠道列；`090` 修正账务时间类型。SQLite `090` 在事务内重建相关
  表，保留金额、ID、自增序列、索引和可解析时间值；升级前必须停机备份并预留表空间。
- **配置**：Lite `CHANNEL_ENCRYPTION_KEY` 不能为空，必须由部署者生成并保存；初始管理员
  密码默认写入 SQLite 卷内 `initial-admin-password.txt`（0600），不再打印服务日志。
- **运行时**：Lite chat 结算、重复迁移和重启持久化修复；MySQL 结算语义不变。历史审计与
  72h 门禁均为只读，不修改 ledger。
- **部署**：Lite / PostgreSQL 环境升级时按目标 Compose 停机备份，串行构建镜像，运行
  `migrate` 后再启动服务。MySQL 生产不需要 schema migration。
- **回滚**：canonical charge 保持仅切 `BILLING_CANONICAL_USAGE_MODE=observe` 并重建
  billing-service，不依赖 schema down migration。迁移回滚采用一致的停机备份恢复。

## 升级步骤

```bash
git fetch --tags
git checkout v0.27.0
```

1. 阅读目标方言发布说明并完成备份。MySQL 从 v0.26.6 起无新增迁移；SQLite / PostgreSQL
   会依次应用 `011` 和 `090`。
2. Lite 部署按 [`docs/quickstart-lite.md`](../quickstart-lite.md) 生成并持久保存
   `CHANNEL_ENCRYPTION_KEY`；不要复用历史示例值，也不要在升级时重新生成已有库的密钥。
3. Lite / PostgreSQL 升级采用停机窗口：停止服务并备份数据卷，串行构建镜像，运行
   `migrate`，确认退出码 0 后启动全部服务。
4. 全量 Compose 建议先更新 `migrate`、`billing-service`、`channel-service`、
   `identity-service`，再按依赖更新其余服务；避免在资源受限服务器上构建镜像。
5. 历史审计使用仅具备三表 SELECT 权限的账号通过 `HISTORY_AUDIT_DSN` 执行，报告留在
   受控环境；不要把实库报告提交到仓库。
6. 保持 canonical usage 现有 mode 与 K3 allowlist 不变；本版本发布 tag 与镜像不要求重启
   当前生产 billing / relay 容器。

## 验证

- `develop@d328781` 的 `make verify` 通过：Go unit/race、architecture、migration-check、
  Web lint/test/build。
- SQLite、MySQL、PostgreSQL migration lifecycle 检查通过；`090` 在已填充旧库上验证数据
  保留。
- `python3 scripts/test-lite-smoke.py` 在空 project、空卷和本地 mock 上游下完整通过
  bootstrap、渠道、钱包、Token、models、chat、重复迁移和重启持久化。
- 历史 usage 审计 fixture / race / go vet 通过；隔离 MySQL 8.0 下 SELECT-only 账号验证
  输出稳定且 ledger 不变。
- 冻结 K3 72h 窗口在 float64 与 PromptExclusive 修订后复验 `0/0`；配置-only 回滚演练
  双向行为证据通过。
- `npm audit` 0 vulnerabilities；Trivy 0.70.0 无 high/critical；govulncheck 无受影响代码。

## 完整变更日志

- docs: explain LLM billing with project implementation
- docs: sync project progress after v0.26.6 release
- docs(v0.27): close out K3 canonical charge 72h window with scoped PASS
- feat(reconcile): add read-only historical usage audit
- fix(lite): restore SQLite settlement and complete deployment quickstart
- docs(roadmap): close personal deployment documentation milestone
- build(deps): bump hono (#19)
- build(deps): bump the npm_and_yarn group across 1 directory with 2 updates (#20)
- fix(security): patch gRPC and require generated Lite encryption keys
- docs(design): add protocol conversion guide for Chat/Responses/Messages
- fix(reconcile): replicate production float64 rounding in charge gate SQL
- fix(reconcile): rebuild legacy cost with PromptExclusive semantics
