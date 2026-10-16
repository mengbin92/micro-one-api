# Micro-One-API v0.35.1 发布：鉴权与定价保护、额度窗口及证据管理修复

> 2026-10-16 · 上一版：[v0.35.0](./release-v0.35.0.md)（2026-10-15）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.35.1)

v0.35.1 是 v0.35.0 之后的 **PATCH 修复版本**：收口模型启用与价格保存、证据管理入口及 owner RPC 可达性，并修复 OAuth 无效身份、未读配置覆盖价格、Codex 额度误暂停、Gemini 路径版本和日报旧证据问题。包含六个非合并功能、测试及记录提交，发布文档提交另计。

## 功能与修复

### 1. OAuth 响应验证

**根因**：五种 OAuth provider 未统一拒绝非成功 HTTP 响应、缺失凭据和身份。GitHub 缺失 ID 会变成字符串 `"0"`，错误邮箱响应还可能提供已验证邮箱。

**修复**：在 token/profile 边界验证 HTTP 状态、必需凭据与身份；GitHub 邮箱错误返回未知邮箱。正常身份和微信 token OpenID 回退保持兼容。

**影响服务**：`identity-service`。

### 2. 模型启用与价格写入保护

**根因**：模型管理与价格保存的授权、写前置条件和页面读写时序不一致；价格配置未读成功时，空草稿可覆盖三项完整价格 map。

**修复**：恢复符合权限的模型启用及价格保存，保留精确 CAS 与中文原因；成功读回配置前禁止建立、编辑、删除和保存草稿。

**影响服务**：宿主机前端；继续使用现有 Admin/Config owner 写入链路。三项价格配置的事务原子保存列入下一阶段，不宣称本版已消除部分成功风险。

### 3. Codex partial quota 与 Gemini 契约

**根因**：额度 fallback 把已识别周窗口重复填入短周期窗口，周额度 96% 因而触发短周期 95% 暂停阈值。Gemini 的版本位于模型 URL 路径，契约校验却只读取 header/query，代理前缀也影响证据记录。

**修复**：仅用未分类额度窗口补缺；契约与证据共用模型路径版本提取，兼容代理前缀，错误版本仍被拒绝。

**影响服务**：`relay-gateway`。历史暂停账号不会自动恢复，仍需依据原始窗口和真实额度核对。

### 4. 证据管理与 owner 授权覆盖

**根因**：上游契约缺少管理页面，供应商报告读取权限仍是 draft；已实现的五项 owner RPC 未列入 ready 清单，使合法管理员读取在到达 owner 前返回 403。

**修复**：补齐上游契约/候选修复页面、读取权限与导航、精确 CAS、撤权清理及回读失败禁写；Identity 136 仅增量发布两项读取权限给既有 root。补齐 Billing/Channel 的固定 RPC 登记，继续拒绝未知方法、非法调用方及系统专用路径。

**影响服务**：`identity-service`、`billing-service`、`channel-service`、`admin-api` 和前端。见 [管理页面更新记录](../runbooks/evidence-management-pages-deploy-2026-10-16.md)。

### 5. 最新日报证据与测试有效性

**根因**：报告按最新导入优先返回，但日报循环不断覆盖 `LatestEvidenceID`，最终指向旧证据；普通测试成功不能证明关键断言会捕获故意回归。

**修复**：日报保留首条最新证据；新增周度测试断言健康检查，在临时 checkout 中验证关键断言能捕获指定回归。

**影响服务**：`billing-service`、开发与 CI 门禁。

## 兼容性说明

- 公开 API 保持兼容；本版不修改用户售价、收费 allowlist、历史账本或已冻结结算政策。
- 相对 v0.35.0 新增 Identity 136，包含 MySQL/Postgres/SQLite 镜像。按 ownership 在 Identity 库应用；已按上次管理页面更新执行的环境只核对记录，不重设授权。Billing 135 等前置迁移继续保留。
- 136 保留显式 deny、已禁用/归档权限和用户绑定；读入口可见不代表取得导入、归因、契约发布或晋级权限。
- 保留现有具名服务凭据、worker 专属环境、持久 outbox、预算与所有灰度开关。`channel-service` 仍按既有单实例边界运行。

## 升级步骤

1. 备份所属数据库、Compose、运行镜像、前端及一致性 outbox；记录环境和开关摘要。
2. 获取 `v0.35.1`，按 ownership 核对/执行 Identity 136；迁移工具与镜像均在本地跨构建，生产不编译。
3. 从纯 v0.35.0 升级时依次更新 Identity → Billing → Channel → Admin → Relay；已经完成 10 月 16 日管理页面更新的环境，只需更新本次有运行时代码变化的 Identity、Billing、Relay。

   ```bash
   ./scripts/deploy-update.sh identity-service billing-service relay-gateway
   ```

4. `cd web && npm ci && npm run build`，独立发布 `/opt/web/dist`；保留旧哈希资源，校验资源后最后原子替换 index。重启 Admin 不会更新前端。
5. 检查镜像/source digest、环境摘要、健康与重启次数、outbox 送达、只读 API 和前端资源。回滚保留新表、账本及证据，不删卷或自动解封账号。

## 验证

- 审查修复回归均先红后绿；标准单元范围 172 包、五个服务内集成包、前端 85 文件/536 测试、lint/TypeScript、分层与 RBAC 863 项检查通过，相关 race 通过。
- 发布前完整 `make verify` 通过：标准单元、完整 race 范围、分层、迁移治理、RBAC、IAM 验收结果检查、生成 API 类型、前端 lint/全量/critical coverage、生产构建及六项体积预算均通过。
- 本次发布的完整门禁、制品与生产验证另行记录，不以历史部署或合成 fixture 代替真实 OAuth/供应商账期与收费灰度验收。
- 下一阶段优先定价原子保存，随后是历史误暂停核对、协议矩阵、实库并发与完整供应商账期，见 [当前计划](../design/next-stage-plan-2026-10-16.md)。

## 完整变更日志

- fix: close auth pricing and upstream review gaps
- docs: record evidence management production rollout
- fix(authz): admit completed evidence owner RPCs
- feat(admin): complete upstream evidence management pages
- fix(admin): restore model activation and pricing saves
- test: add weekly assertion health checks
