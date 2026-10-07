# Micro-One-API v0.34.6 发布：定时对账恢复与 IAM 验收门禁

> 2026-10-07 · 上一版：[v0.34.4](./release-v0.34.4.md)（2026-10-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.6)

v0.34.6 是 v0.34.4 之后的 **PATCH 可靠性与验收修复版本**：恢复 IAM 切换后被用户会话要求阻断的后台定时对账，补齐本人订阅与目录只读的真实所有者链路，并把完整 IAM 浏览器矩阵与固定入口契约接入持续门禁。

**运行时只需更新 `billing-service`**。无新增 API/proto、数据库迁移或业务配置，前端运行代码及挂载的 `web/dist` 不变。

## 修复内容

### 1. 恢复后台对账执行与运行记录

**根因**：`RunReconciliation` 已区分本进程后台任务与外部操作者，但可选明细权限无条件向 identity 请求用户会话，在创建结果和注册持久化收尾之前中止任务。线上可用近 24h 日志有 17 次 `user operator required`，最新运行记录仍停留在 10 月 5 日；旧记录的零差异不能证明对账仍在工作。

**修复**：只对外部操作者查询独立的明细字段权限，启动及定时后台任务继续使用既有本进程信任。有效外部用户有运行权而无明细权时仍隐藏明细，只有明细权不能执行对账，无会话外部请求仍拒绝；持久化审计事实保持完整。

**影响服务**：`billing-service`。

### 2. IAM 持续验收要求完整矩阵实际通过

**根因**：Go PASS 与 Playwright 汇总的 `expected>0` 不能证明完整角色矩阵通过；实际 `test.only` 精简到两项和“预期失败”均曾被接受。PR 分类器还可能把 `git diff` 失败当作无相关变更；IAM 与通用 smoke 共用目录，导致前者证据被后者删除。

**修复**：禁止聚焦测试，按受审契约核对当前十项场景、文件、项目及逐项实际 passed；拒绝缺失、重复、替换、跳过、预期失败及不完整统计。diff 失败直接阻断，IAM/owner/共享 harness/工具版本及工作流变更触发专项。每次独立保存 Go/PW JSON、截图和失败 trace，Git/Docker 排除验收目录；十三项快速回归与固定 829 行入口契约接入 verify/后端 CI，完整浏览器接入 PR/Release/Nightly。

**影响范围**：开发验证与 GitHub Actions；不增加运行时权限 grant。

### 3. 关键链路与证据覆盖补齐

**根因**：本人订阅进度、API Key 用量和权限详情曾依赖局部 stub 或组件检查；撤销主要在 HTTP 层验证，无法单独证明 Billing owner 的拒绝行为。用量夹具反复签发同一 JTI 会跨秒改变到期时间，造成无效会话误报；SQL 账号非空的证据字段还暗示了未执行的最小权限探针。

**修复**：增加真实 Admin/Relay → identity/Billing → SQLite 的本人/Key 用量链路，核对 settled/frozen/available 及异用户、失效/撤销、禁用 Key 和无关 caller 反例；直接 owner RPC 复验会话与主体。增加目录只读用户和桌面/手机详情，403 使用合法 CAS 载荷并确认目标未改写。每个用户复用固定签名会话，SQL 证据限定为非 root 账号配置及 DSN 接线，实际 grants 仍引用原 D1 独立探针。

**影响范围**：测试夹具、验收记录及文档。

### 4. Linux CI 工作区一致性

**根因**：Python 门禁动态导入在 Linux 默认设置下生成 `scripts/__pycache__`；本机缓存前缀/全局忽略设置隐藏了副作用。后端测试通过后，工作区一致性门禁失败，v0.34.5 发布候选因此中止，未创建 GitHub Release 或发布镜像。

**修复**：导入前禁止 bytecode 写入；隔离回归显式采用无外部缓存前缀、允许默认 bytecode 的 CI 行为。修复前生成 1 个缓存文件，修复后 0，快速门禁增至十三项。保留已推送的 v0.34.5 tag，不改写历史，正式发布使用 v0.34.6。

**影响范围**：验证脚本；不改变 Billing 业务逻辑。

## 兼容性说明

- 无 API/proto 或数据库迁移，无新增运行时业务配置；不变更 IAM 模式、分配、专属 caller map 或 SQL grants。
- 外部对账运行权、明细字段权限、操作者与 reason 约束保持有效；后台恢复不代表普通服务凭证可以代替用户授权。
- 前端源码的变动为测试和 Playwright 配置，本版不要求重发生产静态资源。
- IAM 兼容旧镜像保留作为回滚材料；不恢复 legacy 模式或已撤权的共享数据库通道。

## 升级步骤

1. 备份当前 Billing 镜像、Compose、运行环境与数据库；保留已验证的 IAM 回滚标签。
2. 本机按 `app/billing/Dockerfile` 交叉构建 `linux/amd64`，附本次源码摘要，保存、上传并校验运输摘要；服务器不构建。
3. 只更新 Billing 的镜像引用及来源标签，使用 `docker compose up -d --no-deps --no-build --pull never billing-service`。
4. 检查健康、restart、IAM 状态、环境及其他八服务实例；确认启动后新增对账运行记录和指标，核对状态/差异与结算/预留。
5. 如报告真实账务差异，按对账处置流程核查，不手改账本或清空历史。保持下一次定时运行观察。

## 验证

> v0.34.6 最终生产镜像已更新；首轮 v0.34.5 记录保留为中止候选的历史证据。

- 全仓 `make verify` 通过，包含 829 行入口契约、十三项快速门禁、215 个前端单元测试、lint/build 及六项预算。
- 完整 Billing/集成 race、有效外部对账明细权限及直接 owner 会话/主体反例通过；十项真实 IAM 浏览器逐项通过，无跳过。
- 真实 `.only`、精简矩阵、预期失败及 diff 失败的负向验证通过；通用 smoke 后七个 IAM 文件及摘要保持不变。
- 首轮完整通用浏览器 67 passed、1 个既定 mobile-only skip 保留；三库 opt-in 本轮只重新执行 SQLite，不扩大为 MySQL/PostgreSQL 复验。
- 2026-10-07 09:49 CST 首次完成 Billing 业务修复更新，健康 200、restart 0；环境与其他八服务实例保持一致，前端摘要保持 v0.34.4，IAM 为 iam/complete（policy 7、catalog 2）。09:51 只读复核新增对账 **#365 completed、差异 0**，成功指标 1、启动失败/原会话错误 0；结算任务均 completed。数据库/部署备份、回滚镜像与[首次上线证据](../runbooks/evidence/patch-v0.34.5-production-2026-10-07.json)已归档。启动轮已恢复，下一次自然小时轮尚未到达；不将启动轮当作小时轮验收。
- 2026-10-07 10:12 CST 更新最终 v0.34.6 Billing 镜像；10:14 只读验收 **#366 completed、差异 0**，启动原错误/panic/fatal 均 0，健康 200、restart 0，环境及其他八服务/IAM 保持一致，前端摘要不变。数据库备份、回滚与[最终生产证据](../runbooks/evidence/patch-v0.34.6-production-2026-10-07.json)已归档；下一次自然小时轮仍待观察。
- 本地复验与历史生产证据分别见 [第一批记录](../runbooks/iam-first-batch-acceptance-2026-10-07.md)和[复审](../design/iam-first-batch-review-2026-10-07.md)。

## 完整变更日志

- `docs: plan IAM stabilization and critical path gates`
- `fix: restore background reconciliation and enforce IAM gates`
- `fix: require complete IAM acceptance and preserve review evidence`
- `docs(release): v0.34.5`
- `fix(ci): keep IAM gate imports from dirtying clean checkouts`
- `docs(release): v0.34.6`
