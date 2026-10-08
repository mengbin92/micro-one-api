# Micro-One-API v0.34.11 发布：管理控件轮询稳定性与订阅账号治理并发保护

> 2026-10-08 · 上一版：[v0.34.10](./release-v0.34.10.md)（2026-10-08）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.11)

v0.34.11 是 v0.34.10 的 **PATCH 界面与账号治理可靠性版本**。修复管理页面授权续验时控件消失和草稿丢失风险，阻止旧恢复／管理更新覆盖新封禁、旧额度重置擦除新窗口用量，并支持按账号分片扫描与可选轻量 worker。

**无 API/proto 变更、无新增数据库迁移**。运行时影响 `channel-service` 和独立挂载的前端；新增可选分片配置与 `account-ops-shards` Compose profile，默认保持单分片，后台任务仍默认关闭。生产已部署前端修复及 Channel 两分片，本版补齐正式版本制品；executor 灰度和正式七天准入状态沿用 v0.34.10。

## 修复与交付内容

### 1. 管理页面授权续验保留控件与草稿

**根因**：共享权限按钮及导出组件使用续验期间暂时不可用的执行权限快照判断是否渲染，造成按钮卸载、表格布局闪动和局部状态重建；此前 IAM 页面修复未覆盖这些共享组件。

**修复**：使用仍有效的展示权限快照保持控件及其内部状态，授权续验期间禁用执行，完成后恢复。保留对象权限、调用方 disabled 和 any/all 语义；真实撤权、失败、过期及对象权限撤销仍移除受保护控件。

**验证**：共享组件回归覆盖管理动作与导出、草稿、对象权限及撤权边界；真实渠道组件连续三轮授权 timer 保留编辑／导出 DOM 和草稿。桌面／手机 Playwright 8 项通过，覆盖渠道、用户、日志及既有 IAM 续验。生产静态文件、公网入口与已登录浏览器草稿抽样验证见 [管理控件修复记录](../runbooks/console-management-controls-refresh-2026-10-08.md)。

**影响服务**：独立发布的前端资源；无需因本项重建 `admin-api` 镜像。

### 2. 账号恢复和额度重置保护最新状态

**根因**：恢复探测后的账号状态可能已出现新封禁，旧恢复任务或旧管理整行更新仍会清除新标记；瞬时错误可能覆盖手动授权阻断或管理员禁用。额度重置扫描后若策略／时区变化，旧任务可能清除新窗口用量。

**修复**：在现有 metadata 内持久化事件 revision；恢复写入在事务锁内比较捕获状态，复核账号、TTL、本地额度和当前上游快照。管理更新携带恢复基线，状态变动时拒绝旧写入；保留手动授权阻断和管理员禁用。额度重置在锁内重新核对 fixed 策略、时区与边界，过时任务不清零、不占用去重键。统一账号／快照锁顺序，并修复 MySQL 同秒快照写入零 changed rows 被误判 NotFound 的问题。

**验证**：Channel race 回归，以及 MySQL、PostgreSQL、SQLite 存储回归覆盖新封禁保护、旧管理更新、策略／时区变更、重复执行、事务回滚和快照写入。

**影响服务**：`channel-service`；事件 revision 存于既有 metadata，无数据库迁移。

### 3. 分片扫描、游标续扫与轻量 worker

**根因**：多个副本各自扫描全量账号会重复工作；扫描超时后从头开始，使后段账号长期得不到处理。

**交付**：SQL 按账号 ID 取模过滤分片，使用主键游标分页；超时保存进度，下轮从后续账号继续。增加可选 `channel-account-ops-worker`，复用 Channel 镜像，仅运行重置／恢复及 health/metrics，不启动业务 HTTP/gRPC、模型同步、outbox 或额外告警。三种 Compose 配置透传任务与分片参数。

**验证**：分片全覆盖、超时续扫、内存快照隔离、worker 路由隔离及三种 Compose 配置验证通过。生产已运行 0/2、1/2 两分片，标签校正前各观察两轮扫描且错误为零；校正后重新核对两容器健康、镜像及源码摘要，Prometheus 两个 target 均 up。计数重启边界与回滚信息见 [生产分片证据](../runbooks/evidence/subscription-account-ops-shards-2026-10-08.json)。

**影响服务**：`channel-service`，以及按需启用的 `channel-account-ops-worker`。

### 4. 补齐关键测试与 CI 来源事实

**根因**：管理控件回归未纳入关键测试集，函数覆盖率低于既有门槛；部署清单中的路径键使 SHA256 被误判为凭证。提取 worker 共用 HTTP 注册后，RBAC 已审查来源位置与 AST 摘要未同步，来源漂移门禁失败。

**修复**：将管理控件回归纳入关键测试集，补权限拒绝页的显式刷新恢复；部署清单使用 path/sha256 记录，保留全部 77 个文件摘要。刷新八项已审查来源事实，保持 829 项 RBAC 清单及权限／负例契约不变，不降低覆盖率、不放宽安全扫描规则。

**验证**：330 项前端普通测试、148 项关键测试通过，关键函数覆盖率 92.52%；lint、类型、清单完整性与 Gitleaks 检查通过。`go test ./cmd/rbac-contract-check -count=1`、`make rbac-contract-check` 和 `make test` 已通过。

**影响服务**：测试、CI 与文档，无额外运行时部署。

## 兼容性说明

- HTTP/RPC、proto 和数据库结构保持兼容；无新增迁移，旧 metadata 不含 revision 的账号仍通过完整状态比较保护。
- 账号管理更新遇到并发封禁／恢复会拒绝旧状态，需重新加载账号后再提交。
- 新增 `SUBSCRIPTION_ACCOUNT_OPS_SHARD_COUNT`（默认 1）与 `SUBSCRIPTION_ACCOUNT_OPS_SHARD_INDEX`（单分片默认 0）；多分片必须显式指定编号，所有扫描进程 COUNT 相同且编号完整、唯一。
- 可选 worker 通过 `account-ops-shards` profile 启用，编号由 `SUBSCRIPTION_ACCOUNT_OPS_WORKER_SHARD_INDEX` 指定；业务任务默认关闭，不自动发现分片或接管故障 worker。
- 前端继续由宿主机 `/opt/web/dist` 独立挂载；仅更新容器镜像不会更新前端资源。

## 升级步骤

1. 获取 v0.34.11，备份现有 Channel 镜像、Compose、`.env` 和 Prometheus 配置，保留 IAM 与订阅／executor 灰度开关。
2. 已运行重置／恢复任务的环境，先停止旧版本扫描进程或关闭其两个任务开关，等待在途扫描结束；全部扫描进程更新后再启用，避免旧实现绕过恢复 CAS。
3. 在本机交叉构建 linux/amd64 并交付 `channel-service`，禁止在服务器构建。单分片可继续 COUNT=1、INDEX=0；需要两分片时按 [账号治理手册](../runbooks/subscription-account-ops-runbook.md)设置主服务 0/2、worker 1/2，并启用 profile。使用固定镜像，同时重建两个容器；Prometheus 加入 worker target 并验证。
4. 独立构建／发布前端：`cd web && npm run build`，备份宿主机静态目录后上传 dist，核对公网入口和文件摘要。生产已部署本版等价修复，复用记录验证，不必为发布文档重复部署。
5. 核对 health、镜像／源码摘要、分片编号、扫描计数和错误指标；前端验证自然续验期间控件保留且禁用、完成后恢复，草稿仍在。
6. 回滚先停止所有新版扫描 worker，再恢复保存的镜像／配置与前端备份；还原单分片时先停止 worker，再将主服务设为 COUNT=1、INDEX=0。完整顺序及限制见账号治理手册。

## 验证

- 本版变更提交已完成 Channel race、三库存储回归、worker 路由及 Compose 检查；对应提交正文与治理手册记录验证范围。
- 前端普通／关键测试、lint、类型及桌面／手机 8 项授权刷新检查通过；生产静态文件校验与浏览器抽样记录见管理控件手册。
- RBAC 来源漂移修复已通过专项检查和 `make test`，未改变权限控制契约。
- 生产前端与 Channel 两分片已部署，镜像、扫描计数重启边界、摘要和回滚备份均有脱敏证据。本版发布准备仅修改发布文档。
- 独立 Release 流水线执行 Linux E2E 门禁、九服务 amd64/arm64 镜像构建推送及 GitHub Release 发布；正式制品完成状态以该流水线实际结果为准。

## 完整变更日志

- `fix(web): retain management controls during authorization polling`
- `docs(ops): record management controls production deployment`
- `test(ci): cover authorization controls and clarify artifact checksums`
- `feat(channel): shard account governance and fence stale recovery`
- `fix(ci): refresh reviewed channel HTTP entry facts`
- `docs(release): v0.34.11`
