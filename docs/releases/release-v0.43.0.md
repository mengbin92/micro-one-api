# Micro-One API v0.43.0 发布：legacy abilities 退役与 registry 单一路由来源

> 2026-11-14 · 上一版：[v0.42.0](./release-v0.42.0.md)（2026-11-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.43.0)

v0.43.0 是 v0.42.0 之后的 **MINOR 功能版本（含破坏性变更）**：模型注册表映射（`model_channel_mapping` / `model_subscription_mapping`）成为唯一的路由候选来源，旧的 `abilities` / `subscription_account_abilities` 投影与 `restrict_models` catch-all 开关一并退役。迁移 151 会先把存量 legacy 路由回填进 registry mapping，再删除旧表与列。

版本判 MINOR 而非 PATCH 的依据：删除两张业务表与一列、移除 catch-all 路由行为，使「未注册模型」的请求结果从「可落到兜底渠道」变为 `ErrChannelNotFound`，属于公开路由契约变更。

## 变更内容

### 1. 路由契约收口

- `ChannelRepo` 的 `ListAbilitiesByGroupAndModel` / `ListSubscriptionAccountAbilities` / `ListUnrestrictedChannelsByGroup` 全部移除，改为 `ListChannelCandidates` / `ListSubscriptionCandidates`，只读取 registry mapping（DB 与内存两条路径一致）。
- biz 层 `Ability` / `SubscriptionAccountAbility` 重命名为 `RoutingCandidate` / `SubscriptionRoutingCandidate`；`SelectChannel`、`SelectChannelExcluding`、`CanRoute`、`HasRoutingCandidates` 中的 unrestricted fallback 分支全部删除。
- 未注册模型统一返回 `ErrChannelNotFound`，不会再被放宽到任何兜底渠道；`models` 目录、`/v1/models` 与 group-audit 库存同步改为只读 registry。
- `pkg/wildcard` 的注释与 `docs/model-management-design.md` 顶部状态说明同步；设计文档中 §11 的 P1 实现方案保留为历史设计记录。

### 2. 迁移 151（MySQL / PostgreSQL / SQLite）

- 回填：`abilities` / `subscription_account_abilities` 中非通配符、非空的模型先补进 `models`，再按 `(channel, model)` / `(account, model, group)` 生成 registry mapping，`enabled` 一律为 true（可用性由来源 status 决定），`config` 使用 `{"_managed_by":"channel_models"}` / `{"_managed_by":"subscription_models"}` 标记，使后续自动同步可以正常接管这些行。
- 过滤：三方言 INSERT 均 JOIN 父表（`channels` / `subscription_accounts`），跳过孤儿 legacy 行，避免 `fk_mcm_channel` / `fk_msm_account` 外键报错。
- **父资源清单校验（2026-11-15 code review 修复）**：mapping 回填只复活父资源仍声明的路由——渠道 ability 的模型须仍在该渠道 `models` CSV 中；订阅 ability 的模型须仍在账号 `models` CSV 中、且 ability 的 group 须仍在账号 `group` CSV 中。切换窗口内从渠道／账号删除的模型或分组因此不会被旧投影插回。手工维护的 registry mapping 不受影响（它们只存在于 mapping 表，从不在旧投影中），运行时也不强制 mapping ⊆ 父资源 `Models`。
- 幂等：MySQL 的 `ADD COLUMN config` 与 `DROP COLUMN restrict_models` 使用 `information_schema` + `PREPARE` 守卫（沿用 061/067 约定），破坏性的 `DROP TABLE` 放在文件末尾。**重放安全加固（同轮修复）**：MySQL 上所有读取两张旧表的回填段（catalog 两段 + mapping 两段）同样加源表存在性守卫，DROP 已提交但版本登记失败的运行可以安全重放（守卫段变为 no-op）；PostgreSQL / SQLite 的 DDL 本身随 runner 事务回滚，天然可重放。
- 收尾：删除两张 legacy 表与 `channels.restrict_models` 列，并把 `model_subscription_mapping.routing_group_id` 按 group key 补齐。
- `scripts/test-e2e-flow.sh` 的 bootstrap 校验从 `abilities` 计数改为 `model_channel_mapping` 计数。

### 3. 自动映射的授权与一致性加固

- 订阅账号的自动映射写入（create / update / delete）新增 `channel.model_mapping.*` 的逐条 IAM 检查与 `resource_write_audits` 审计，与渠道侧对齐；缺少 mapping 授权时整个账号事务回滚。
- **并发创建同一新模型（2026-11-15 code review 修复）**：`ensureModelRegistryRowTx` 由「捕获唯一键冲突后在同一事务重查」改为「`OnConflict DoNothing` 插入后读取获胜行」，冲突路径在 MySQL REPEATABLE READ 下使用加锁读避免首读快照 miss。两个并发创建包含同一全新模型的订阅账号时，双方均复用同一 registry 行、只有一条模型记录，不再出现 PostgreSQL `25P02` 整笔账号回滚。
- `syncSubscriptionAccountModelMappingsTx` 记录所有既有 `(account, group, model)` 键（不再只看自动行），跳过创建但只对自动行更新 / 删除，避免与迁移 151 回填行或运维手工行撞唯一键。
- 自动映射的 `enabled` 固定为 true，`changeStatusDB` / `changeSubscriptionAccountStatusDB` 只改来源 status；create / update 分支都会投影 `routing_group_id`。
- 内存（lite）模式的同步助手补齐 `Enabled` / `Priority` / `UpstreamModelID` 更新；渠道侧把逐行 `slices.Contains` 改成 map 查找。
- `toChannelInfo` / `toChannelSummary` 把 deprecated 的 `restrict_models` 响应固定为 `true`，避免退役前客户端把零值误读为「允许兜底」。

### 4. 测试稳定性

- `TestTimingHTTPBoundaryIgnoresHeartbeatAndExcludesPostOutputSettlement` 改用绝对里程碑（`OutputFinishedAtMS` 与 handler 内最后内容帧时刻）比较，不再跨基准比较 `HandlerDurationMS` / `OutputDurationMS`；后两者起点不同（handler 入口 vs recorder 创建），全量并行时中间件耗时会让断言翻转。
- `TestStress_SessionHashSticky_CrossReplicaBindAndReuse` 在并发阶段前先同步 bind 一次 session，避免 40 个 goroutine 同时竞争空的 sticky store 而全部 rebind。

**复核加固（2026-11-15）**：三方言 151 只归一 CSV token 边界空白，保留合法内部空格和分组大小写；SQLite 拆分 token 后按字面比较，保留名称中 `_`、`%` 的原义；订阅模型并发创建以非零主键判断插入结果，生产 MySQL 默认 `clientFoundRows=true` 下的冲突 no-op 不再返回模型 ID 0。对应真实迁移与跨库并发回归已补齐。

## 兼容性

**破坏性变更**：

- `abilities`、`subscription_account_abilities` 表被删除，`channels.restrict_models` 列被删除；依赖这三者的外部脚本 / 报表需改为读 registry mapping。
- 未注册模型不再有兜底渠道，返回 `ErrChannelNotFound`（HTTP 侧表现为无可用渠道）。
- `restrict_models` 请求字段保留但标记 deprecated 且被忽略；响应字段保留 wire 兼容并固定为 `true`。

**部署顺序约束（本次采用无停机分步迁移）**：

1. 备份 `oneapi_channel`。
2. 先单独执行 151 的 `ADD COLUMN config` 守卫段（或等价 ALTER）。新代码的订阅映射 INSERT 需要该列；此时 legacy 表仍在，新旧镜像都能工作。
3. 部署 `channel-service`、`relay-gateway`、`admin-api` 同一 commit 的镜像，并把复用该镜像的 `channel-account-ops-worker` 切到同一 tag（否则旧 worker 仍会写已删除的 `subscription_account_abilities`）。
4. 再执行完整 151（守卫跳过加列，回填后删表删列），并登记 `151_retire_legacy_abilities` 版本。
5. 验证服务健康与 schema 状态。

直接先删表再部署会让旧实例报错，直接先部署而不加列会让新实例的订阅映射 INSERT 失败。

切换窗口约束：步骤 3 到步骤 4 之间冻结渠道／订阅账号的写入。父资源清单校验保证「从父资源 CSV 删除」的模型／分组不会被旧投影插回；但通过 model-management API 单独删除手工 mapping（父资源 CSV 仍列出该模型）在窗口内仍可能被旧投影复活，此类操作须等到删表完成后再进行。

回滚：服务可用本次部署的共享 `rollback-20261114-225215` 镜像 tag 回退；数据侧因为 DROP 不可逆，必须从备份恢复 `abilities` / `subscription_account_abilities` 与 `restrict_models` 列，并删除 `151_retire_legacy_abilities` 版本记录后再回退服务。

## 验证

- 本地门禁：全仓非 E2E Go 测试 131 包全绿（带 MySQL / PostgreSQL DSN），`app/channel/internal/biz` 与 `data` 的 `-race` 通过，`make rbac-contract-check` 962 行，Markdown 本地链接 371 文件，`go build ./...` 与 `git diff --check` 通过。
- 迁移真机复验：本机 MySQL / PostgreSQL 容器用最小 schema 验证 151 首跑与重跑，孤儿 mapping 0 条、`routing_group_id` 投影 1 条；SQLite 由 `TestRetireLegacyAbilitiesMigrationSQLite` 覆盖（含孤儿行用例）。
- 2026-11-14 线上更新：备份 `oneapi_channel`（769 KiB，gzip 校验通过）后按上述顺序完成；4 个实例统一到 `deploy-20261114-225215-9e5d28bb`，容器 healthy、近 3 分钟无 panic / `no such table` / `unknown column` 日志；relay-gateway `:8080/healthz`、admin-api `:3000/healthz` 与容器内 `channel-service:8002/healthz` 均 200；schema 最新版本为 `151_retire_legacy_abilities`，legacy 表 0、`restrict_models` 列 0、mapping 12 / 4 条、孤儿 0。
- CPU 满载（10 核 `yes`）下两个稳定性用例 ×5 通过，随后全仓 131 包复跑 0 FAIL。
- 已知限制：本机 web 全量 vitest 存在既有 jsdom 超时基线，本轮无前端改动未重跑；以 CI Frontend job 为准。

## 完整变更日志

见 [CHANGELOG.md](../../CHANGELOG.md) `## [0.43.0]` 与 `git log v0.42.0..v0.43.0`。
