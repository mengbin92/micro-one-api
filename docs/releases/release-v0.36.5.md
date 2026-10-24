# Micro-One-API v0.36.5 发布：上游契约写权限与渠道 11 有界试点

> 2026-10-24 · 上一版：[v0.36.4](./release-v0.36.4.md)（2026-10-24）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.5)

v0.36.5 是 v0.36.4 之后的 **PATCH 修复版本**：新增 Identity 142，按维护者明确授权向现有 builtin root 持久发布上游契约 create/publish/quarantine；同步 IAM 存储测试的已发布操作期望。沿用 v0.36.4 的严格类型、独立 proof 与到期探测能力，将生产部署和渠道 11 DeepSeek Flash 的有界试点独立留证，并更正 v0.36.4 发布说明中的初稿状态和版本归属。

本轮已完成生产五服务更新、Identity 142、三个契约 gate 与渠道 11 的 **1% 灰度试点**。JSON/SSE 两次真实调用均通过，新增付费目标已撤销，用户钱包与渠道 11 用户账本核对无变化。现场证据见[生产试点记录](../runbooks/upstream-contract-pilot-2026-10-24.md)；v0.36.4 已发布的 tag 保持不变。

## 修复与改进

### 1. 经授权向现有 builtin root 发布三项契约写权限

**根因**：v0.36.4 安装了契约运行时与管理 API，但契约 create/publish/quarantine 仍为 draft/unbound，历史 builtin root 使用冻结授权集合。仅启用权限目录或尝试普通角色授权 API，不能为该冻结角色追加新操作，正常会话因此仍无法创建与发布候选。

**修复**：新增三方言 Identity 142，通过正式 migration runner 发布且仅发布 `channel.upstream_contract.create`、`channel.upstream_contract.publish`、`channel.upstream_contract.quarantine`，为现有启用的 builtin root 补齐缺失 allow，并增加 root/policy/catalog revision 及固定审计 receipt。既有关系优先，显式 deny 与窄范围 allow 保留；disabled/archived 权限阻止未经治理的重新开启，重复执行根据 receipt 保持无变更。此授权为用户明确要求的持久 root 能力，不随四次试点到期自动撤销。

**影响服务**：`identity-service` 的 IAM schema 与授权快照，以及通过正常会话调用 Channel owner 的契约管理流程。不增加用户或角色分配，不自动创建契约绑定，不变更钱包、价格或财务账本。

### 2. IAM 存储期望包含正式发布的契约操作

**根因**：迁移 142 已加入完整迁移链，`TestIAMStorageDialects` 的 before-D0 已发布操作清单仍只列读取页面和供应商导入，导致 `make verify` 把新增三项经审查操作误报为越界授权。

**修复**：测试期望精确增加三项契约写操作，并保留对其余操作状态及绑定元数据的逐项断言。修复验收基线，不扩大生产权限集合或放宽未知操作检查。

**影响服务**：Identity 存储层测试与完整验证 gate。

### 3. 生产部署与渠道 11 契约试点分别验收

**根因**：Release 构建成功只能证明对应代码和制品，不能证明生产已执行迁移、正常 IAM 可达、指定来源已绑定或真实原生 proof 通过；v0.36.4 的发布初稿仍含“待填”，还曾把后续 142 错记入旧版本范围。

**修复**：更正 v0.36.4 为已发布事实，142 与实际生产启用归入本版。生产使用本机跨构建 linux/amd64 的制品，按消费者/Channel 与 Relay/Monitor 顺序升级，保留配置、镜像回退点与用户账本。通过正式控制台 `https://console.mengbin.top` 的正常会话执行契约 owner API，固定来源 11 / `deepseek-flash` / `openai_chat`、不可变候选 revision 与新 proof pair；真实结果及非财务副作用检查独立记录。

**影响服务**：本轮实际升级的 `identity-service`、`billing-service`、`channel-service`、`monitor-worker`、`relay-gateway` 及运维记录；源码摘要、实际镜像与健康以最终部署证据为准。

### 4. 试点保留四次与 0.10 CNY 的明确边界

**根因**：对指定来源试点不能解释为任意付费探测、全量契约晋级或收费扩面；按日预算也不能跨 UTC 日续期为新的授权。

**修复**：沿用[试点计划](../runbooks/evidence/upstream-contract-types-2026-10-24/pilot-plan.json)，最多四次推理、0.10 CNY 总授权；短期 `expires_at` 早于同一 UTC 日切换，Monitor/Relay 均在准入前检查，完成或失败后撤去付费目标。库存原有 300 次预算保留，同来源额外增加四次、512 输入和 64 输出 token；两种文本 JSON/SSE 共享上限。按归档的官方高峰价格与每次 128 输入/16 输出上界，四次估计不超过 0.001536 CNY；实际供应商费用或未知状态另行保存。fresh proof、绑定 CAS 和 1–5% 灰度按正式 API 验证；30 分钟 / 100 完整 verified attempt / 零失败的晋级门槛保持。

**影响服务**：`monitor-worker`、`relay-gateway`、Channel 契约发布和试点预算配置。独立 proof 不替换结算 parser，不改用户收费名单、价格、钱包和历史账本。

## 兼容性说明

- 从 v0.36.4 升级新增 **Identity 142**；v0.36.4 tag 不含该迁移。更早版本还需完成 Identity 140 与 Billing 141；已经执行过的迁移不重复修改数据。
- 142 持久发布的是现有 builtin root 的三项精确操作；不向其他角色自动授权，不更改用户分配或会话。显式 deny、窄授权、禁用/归档治理和 receipt 重放保护继续生效。
- 运行时契约 build、proof pair、可选 `expires_at` 与 v0.36.4 一致；本次权限/测试修复不新增公开 proto 或结算算法版本。
- Gate 开启、候选创建、fresh proof、灰度发布和全量晋级是不同状态，须分别留证。四次试点不能证明 100 个完整样本，也不能宣称满足全量晋级条件。
- 财务报告、用户钱包、价格、消费 ledger 和收费 allowlist 保持独立门禁；supplier tariff estimate 不冒充实际扣款。

## 升级步骤

1. 核对 v0.36.4 已发布制品和当前生产版本，备份 Identity 数据库、Compose/环境及原镜像；秘密备份仅在生产保留，目录 0700、文件 0600。确认维护者已授权现有 builtin root 的三项持久契约写能力。
2. 使用本机/CI 正式构建的 migrate 制品，经专用管理通道和 ownership runner 执行 **Identity 142**。核对 schema_migrations、三项 enabled/bound 状态、既有关系保留及 `migration:142:upstream-contract-write` 审计；重放应无新增写入。不要直接修改 IAM 表。
3. 按本轮制品差异更新消费者与五服务，在本机交叉构建并上传，生产只加载镜像；验证实际 image ID、源码摘要、HTTP healthz、重启/OOM 与专用凭据信任。保留统一回退标签与配置备份。
4. 通过正式 HTTPS 控制台的正常 IAM 会话核对权限，调用 owner 创建精确候选；分别确认 Channel/Relay/Monitor gate、scope、内容 revision、binding/incident revision 和当前 source fingerprint。
5. 配置同一 UTC 日内的短 expiry 与共享四次推理预算，运行 JSON/SSE 独立 canary。只有当前 revision 的 fresh proof 成功才可按 CAS 发布小比例灰度；失败保留证据并停止试点，不伪造成功或降低晋级门槛。
6. 完成后移除付费目标，核对过期拒绝、供应商成本/未知状态及用户 ledger/钱包/价格无变化。需要回退时使用兼容镜像与配置，保留 migration receipt、不可变契约、事故隔离、财务数据及 outbox；不要通过旧迁移重放撤销持久授权。

## 验证

- 完整 `make verify` 已通过：单元、竞态、架构、迁移、RBAC/IAM、生成、前端 lint、599 项测试、191 项关键覆盖、构建和资产预算；关键覆盖与全量有重叠。八个直接相关后端包另行 `-race -count=1`、真实 HTTP/gRPC `internal/integration` 回归通过。[验证摘要](../runbooks/evidence/upstream-contract-types-2026-10-24/v0.36.5-verification.json)。
- 142 在真实 SQLite、MySQL 8.4.11、PostgreSQL 16.15 上通过 33 个数据库场景，0 skip；覆盖既有 deny/窄授权、禁用/非法 root/resource 和 receipt 防复活，临时容器已清理。[三方言结果](../runbooks/evidence/upstream-contract-types-2026-10-24/migration142-dialects.json)。
- 生产正式 runner 应用 142 一次，三项 enabled/bound、成功审计 receipt=1，IAM 私有备份已保存。五服务 HTTP healthz 均 200、重启 0、OOM false；三个 gate=true，回退标签为 `rollback-20261024-140822`。[部署与迁移](../runbooks/upstream-contract-pilot-2026-10-24.md)。
- 正式控制台创建不可变 revision `49170c393d038e263c2544fb170b614f5b3455c6925389cae6c0f24e2e2b185a`；JSON/SSE 两次均 `contract_verified`，14:31:10 CST 通过 fresh proof/CAS 发布 1% gray，binding=1、incident=0。没有全量晋级，30 分钟/100 完整样本门槛保持。
- 本轮最多四次、实际两次调用；两个付费目标已移除并核对健康，渠道 11 库存目标保留。两次按归档价格估计为 0.000030 CNY，实际供应商实扣尚未核验；试点前后钱包聚合校验相同、渠道 11 用户账本均 201 行、canary 无用户 final。[试点结果](../runbooks/evidence/upstream-contract-types-2026-10-24/pilot-result.json)。
- 标签推送后由 Release workflow 执行完整 E2E、九服务 amd64/arm64 构建、manifest 验证并发布 GitHub Release；实际结果以本版 workflow 为准，不复用 v0.36.4 的成功结果。

## 完整变更日志

- `fix(identity): publish upstream contract write permissions for builtin root`
- `test(identity): align IAM storage expectations with contract publication`
- `docs(upstream): record channel 11 contract pilot and deployment`
- `docs(release): v0.36.5`
