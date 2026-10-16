# Micro-One-API v0.35.2 发布：定价原子保存、版本冲突与撤权草稿保护

> 2026-10-16 · 上一版：[v0.35.1](./release-v0.35.1.md)（2026-10-16）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.35.2)

v0.35.2 是 v0.35.1 之后的 **PATCH 修复版本**：将三项完整模型价格改为单次 owner 事务保存，补齐逐项版本冲突保护、冻结草稿及撤权清理，并归档协议矩阵、三库实测与 DeepSeek 官方已结束日窗核验。相对上一版包含六个非合并提交，发布文档提交另计。无新增数据库迁移、proto 或依赖；运行时影响 `admin-api` 和宿主机独立前端。

## 功能与修复

### 1. 三项价格原子保存与并发保护

**根因**：页面依次保存 `ModelPrice`、`ModelRatio`、`CompletionRatio`，中途失败会留下部分更新；legacy 路径可能忽略提供的版本，首次创建的 upsert 也可能覆盖竞争写者。

**修复**：复用 Admin owner 的批量事务，一次验证完整三项价格 map、逐项授权、操作原因和字符串版本号；任一失败全部回滚。已有行条件更新，首次创建插入并拒绝唯一竞争，数据库并发冲突统一返回版本冲突；缺少批量存储能力时直接拒绝写入。

**影响服务**：`admin-api` 和前端。`PUT /api/option/` 新增 `values` 批量形状，包含三个 JSON 字符串 map 及 `expected_revisions`；既有单项请求形状保留，提供的版本前置条件得到执行。

### 2. 草稿版本、失败回读与撤权清理

**根因**：后台刷新可能用新版本号提交旧草稿，掩盖并发修改；写入或回读失败后继续保存可能覆盖未知状态；读取权限撤销后仍可能显示旧草稿。

**修复**：草稿冻结建立时的三个版本，一次保存且 409 不自动重试；写入或回读失败后禁写，显式刷新后才能继续。草稿绑定授权版本，撤权或读取不可用时清理受保护内容。

**影响服务**：独立前端，需与支持原子批量的 Admin 配套升级。

### 3. 协议终态与实库回归

**根因**：单元成功不足以证明跨传输冻结版本、缺失用量、截断与隔离结果正确，也不能代表真实 SQL 方言的首次创建竞争和事务回滚。

**修复**：新增 HTTP/SSE/WebSocket 十二项冻结契约与终态证据矩阵，补齐 SQLite、MySQL 8.4、Postgres 16 定价并发、首次创建、旧版本、存储失败和模型授权失败回滚验证；完成 owner 旧证明与回滚实库补验。

**影响服务**：测试与验证记录；本节没有新增 Relay 运行时代码。

### 4. DeepSeek 已结束日窗与交付证据

**根因**：早期供应商试点取证时日窗尚未结束，缺少后续官方汇总；正式版本与已部署修复也需要可追溯记录。

**修复**：固定账号与所选 Key 名称，导出 10 月 14 日官方费用和用量，与历史六次手工调用比较：140,333 token、0.14036956 CNY，精确差额为零。归档脱敏派生文件、原始摘要、离线校验器及部署/回滚记录；15 日空报表保留为空。

**影响服务**：文档与证据。费用为供应商汇总报告，未提供逐请求费用或不可更正结账水位；生产来源绑定、API/CSV 双轨与收费 profile 仍待独立验收。详见 [核验记录](../runbooks/deepseek-closed-day-2026-10-16.md)及[计划执行记录](../runbooks/plan-execution-2026-10-16.md)。

## 兼容性说明

- 相对 v0.35.1 无新增迁移、proto 或依赖。保留既有授权、操作原因与精确字符串版本契约；旧客户端遇到版本冲突需刷新后重新编辑。
- 新前端依赖新版 Admin 批量接口；先部署 Admin，再发布前端，回滚时顺序相反。宿主机 `/opt/web/dist` 独立挂载，更新镜像不会更新前端。
- 已于 10 月 16 日部署等价 Admin `9390ef4a` 与前端 `945bb824` 修复的环境，可核对业务源码和静态资源摘要后保留现有部署；正式镜像的版本标签不同不代表业务差异。
- 本版不修改用户售价、历史账本、收费 allowlist、授权绑定或灰度开关；P2 全量批准仍为 false，正式七天观察因缺少可比 orchestrator 样本尚未开始。

## 升级步骤

1. 备份所属数据库、Compose、运行镜像和前端，记录环境、开关与资源摘要；保留既有财务数据和审计。
2. 获取 `v0.35.2`。从 v0.35.1 升级时更新 Admin；遵循本地跨构建 linux/amd64 并传输到生产的流程，生产不编译：

   ```bash
   ./scripts/deploy-update.sh admin-api
   ```

3. `cd web && npm ci && npm run build`，独立发布 `/opt/web/dist`。保留旧哈希资源，校验资源后最后原子替换 index。已部署等价修复时，核对来源与资源摘要后可跳过对应更新。
4. 核对镜像来源、健康、环境摘要、静态资源和价格读取；负向验证过期批量 409、缺失 map 400、未认证写入 401，确认三项值及版本未改变。
5. 回滚时先恢复与旧接口匹配的前端，再恢复 Admin 镜像；不使用旧数据库备份覆盖升级后的交易、证据或审计。

## 验证

- 定价事务改动后完整 `make verify` 通过：172 个 Go 单元包、完整 race 范围、分层、迁移治理、RBAC 863 项、生成类型与前端门禁。
- 最终撤权修复后前端 lint、541 测试、190 critical、TypeScript/生产构建和六项体积预算通过；两轮真实 IAM 浏览器矩阵各十项通过，skipped=0。
- SQLite/MySQL/Postgres 并发及回滚 race 回归、临时 MySQL/Postgres 全量与重复迁移、相关证据路径和十二项传输矩阵通过。测试细节及范围见计划执行记录。
- 生产已部署等价 Admin/前端修复，真实安全负例 409/400/401 后价格与版本逐项相等；80 个前端文件及 HTTP 资源摘要已核对。
- `python3 docs/runbooks/evidence/deepseek-closed-day-2026-10-16/check.py` 可离线复现六次调用与官方报告的精确合计比较。
- 本版 tag 的 Release 流水线重新执行 E2E 门禁、九服务双架构镜像构建和 GitHub Release 发布；其最终结果以对应流水线与注册表清单为准。

## 完整变更日志

- fix(pricing): save model price maps atomically
- test: verify frozen contracts across relay transports
- fix(web): discard price drafts after authorization changes
- docs: record plan delivery and remaining evidence gates
- docs: verify DeepSeek completed-day trial against official exports
- docs: normalize supplier evidence CSV line endings
