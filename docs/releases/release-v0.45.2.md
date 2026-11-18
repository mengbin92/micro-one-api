# Micro-One API v0.45.2 发布：管理订阅分配 CAS 注入修复

> 2026-11-18 · 上一版：[v0.45.1](./release-v0.45.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.45.2)

本版为 **PATCH 修复版本**：只改 Web 前端（`web/src`）与文档，无 Go 服务、无 API 契约、无数据库迁移变更。修复管理员在 `admin/subscriptions` 手动分配订阅时报「版本值无效，请重新加载数据」、请求无法发出的缺陷，并加固撤销、延期、重置配额的 CAS 取值路径。后端授权边界、计费语义与订阅数据零变化。

## 1. 订阅分配不再从合同策略推断 CAS

**根因**：管理写拦截器 `prepareManagedWrite` 递归展开受保护缓存，按 id 反查「资源行」并读取任意可用的版本字段。v2 订阅合同内嵌额度策略 `contract.quota_policy`：其 `version` 是 `contractHash()` 生成的哈希字符串，`id` 又可能与管理员输入的用户 ID 相同。分配订阅时 `user_id` 命中该策略对象，哈希被送入 `numericRevision()`，`Number(hash)` 得到 `NaN`，于是在 axios 请求拦截器阶段抛出「版本值无效，请重新加载数据」。

**关键证据**：生产容器日志（含历史全量）检索 `subscriptions/assign` 与 `expected_revision` 均为零命中——请求从未离开浏览器，服务端因此没有任何记录。后端 `AssignSubscriptionRequest` 也不含 `ExpectedRevision` 字段：分配是创建订阅，本就不需要 CAS。

**修复**：将 `/v1/admin/subscriptions/assign` 识别为无 CAS 的创建操作，跳过 `expected_revision` 注入。

**影响服务**：admin（Web 前端）。后端与数据库未改动。

## 2. 订阅写操作只读取权益版本

**根因**：仅忽略合同里的 `version` 字段并不充分。缓存中前一条订阅若带有 `quota_policy.id == 目标订阅 id`，`find` 会先命中该嵌套策略对象，取不到 `entitlement_revision`，于是**静默漏带** CAS——比直接报错更隐蔽，会让撤销、延期、重置配额绕过预期的版本前置条件。

**修复**：订阅端点在匹配缓存行时要求该行带 `entitlement_revision`（或 `entitlementRevision`），并只从该字段读取 CAS；合同格式版本 `contract.version` 与策略哈希 `quota_policy.version` 都不再参与。显式传入的 `expected_revision` 保持原样交由服务端判定；非数字或超出安全整数的权益版本仍然 fail-closed 拒绝发送。

**影响服务**：admin（Web 前端）。其它端点的 CAS 取值与严格校验完全不变。

## 兼容性说明

- 无 Go 服务变更、无 proto/API 契约变更、无数据库迁移；不需要迁移或服务重启。
- 前端产物为静态资源，由 admin 容器只读挂载，替换目录即生效。
- 后端授权校验未放宽：服务端仍按 `entitlement_revision` 做 CAS 判定，显式旧版本照常返回冲突。

## 升级步骤

1. 从 `.env` 读取部署目标，备份当前前端产物目录（`/opt/web/dist`）。
2. 本机构建前端（`npm run build`），上传替换；只读挂载，无需重启容器。
3. 校验 `index.html` 摘要与资源数量，确认首页与主 bundle 返回 200。
4. 浏览器强刷后确认：`admin/subscriptions` 手动分配订阅不再弹「版本值无效」，Network 中出现 `POST /v1/admin/subscriptions/assign` 并返回成功。
5. 验证通过后走 develop → main → tag。

**回滚**：恢复备份的前端产物目录即可，无数据或 schema 回滚需求。

## 验证

- [x] 完整 `make verify` 通过（`all gates passed`）：格式、RBAC 矩阵摘要、Go 单测与 race、分层架构、迁移检查、RBAC 契约（979 行）、IAM 浏览器门禁、前端 API 类型再生成无 diff、前端 lint/单测/关键用例/构建与体积预算。
- [x] 前端全量单测：100 个测试文件、788 例全部通过；关键用例 16 个文件、209 例通过，覆盖率 语句 92.34% / 分支 86.16% / 函数 92.81% / 行 93.44%。
- [x] `tsc -b` 类型检查随 `npm run build` 执行通过；初始 JS/CSS 体积预算全部 `PASS`。
- [x] 反向验证：将 `admin-write.ts` 临时换回修复前版本，新增用例复现**完全相同**的「版本值无效，请重新加载数据」错误；恢复后全绿。
- [x] 生产前端产物与本次提交一致：本地构建 `index.html` sha256 `a61b70fb…64f5646`，与线上 `/opt/web/dist/index.html` 完全相同；82 个静态资源，首页与主 bundle 均 200，`admin-api` 未重启且近 5 分钟 error/panic/fatal 为 0。
- [ ] 浏览器端真实分配操作验证（需真实管理员登录态）；不属于本次部署健康证明。

## 完整变更日志

- fix(web): stop inferring subscription CAS from contract policies
- docs(release): v0.45.2
