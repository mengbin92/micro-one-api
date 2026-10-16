# 10 月 16 日计划执行：补丁交付、定价原子保存及条件验收

日期：2026-10-16（Asia/Shanghai）。执行 [当前计划](../design/next-stage-plan-2026-10-16.md)，脱敏结果见 [验证索引](evidence/plan-execution-2026-10-16/index.json)。

## 退出状态

| 项目 | 本轮结果 |
| --- | --- |
| 1. 交付审查修复 | 完成：v0.35.1 正式 Release、17 项任务及九服务双架构清单通过；Identity/Billing/Relay 与独立前端已更新 |
| 2. 定价原子保存 | 完成：已提交、合入 develop 并部署 Admin/前端；真实负向 API 验证未改变价格或版本 |
| 3. 历史误暂停与协议矩阵 | 完成现存数据核对及既定工程：当前 Codex 账号为零，未恢复账号；12 项传输矩阵及三库旧证明/回滚回归通过。历史已删除账号及缺原始 payload 的时点不可追溯 |
| 4. 实库与独立供应商证据 | 实库完成，供应商验收未完成：临时三库回归通过；浏览器工具禁止 DeepSeek 当前 URL，已停止网站操作，不将 UI 观察或未关账旧导出记作费用核验通过 |
| 5. 有限灰度准入 | 条件复核完成，INSUFFICIENT：新实例只有 legacy SSE 样本，没有可比 orchestrator cohort，正式七天不计时、不拼接历史窗口 |

## v0.35.1 正式交付

发布文档 `f4c6d88d` → develop `cfbe869b` → main/tag `49dfe7ce7fa0f5017d678fd52073f1e1fa5057d0`。三件套按仓库流程先提交，再推 develop、合 main、推 tag。

[Release 流水线](https://github.com/mengbin92/micro-one-api/actions/runs/38841890516) 17 项任务全部成功；[正式 Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.35.1) 于北京时间 12:17:41 发布。九个服务的注册表 manifest 均独立核对包含 linux/amd64 与 linux/arm64。

生产镜像全部在本地跨构建 linux/amd64，生产没有编译。第一轮依次更新 Identity、Billing、Relay，标签 `deploy-20261016-104335-49dfe7ce`，源码摘要 `45e0632245eabd36d06cad7d6afe20fbe52b2f64dcae947421a8114e42b033d1`；其余业务服务保持原镜像。Identity 136、Billing 135 的既有迁移记录均已核对，未重复发布权限。

七个相关进程的完整环境摘要逐项保持，健康正常、restart=0；第一轮 outbox acknowledged=1206、pending=0。真实管理员会话下价格、供应商报告、精确上游契约读取均 HTTP 200，价格内容摘要保持一致。OAuth 错误响应、partial quota、Gemini 和日报修复通过对应源码回归验证；未向真实供应商注入故障、发送付费推理或制造账单。

## 定价原子保存与补验

功能提交 `87aa3657`，协议矩阵 `e63582fa`，合入 develop `9390ef4a`；撤权草稿补验修复 `945bb824`。这些提交晚于 v0.35.1 tag，不能把它们描述为正式 v0.35.1 镜像已经包含。

复用既有 Admin owner 的 `SystemOptionsBatchRepo.SetMany`，不新增跨服务 RPC：

- `PUT /api/option/` 新增 `values` 形状，仅接收完整 `ModelPrice`、`ModelRatio`、`CompletionRatio` 三个 JSON map；价格与倍率为非负有限数字，拒绝空身份模型名、null、数组、混合单键请求及缺失版本。
- `expected_revisions` 是三个字符串版本号，沿用 owner 的逐项授权、事务和审计。legacy 提供的版本也参与 CAS；没有批量事务能力时返回不可用，不逐项降级。
- 已有行在锁内条件更新，首次创建使用插入及唯一冲突拒绝，避免 unconditional upsert 覆盖另一写者。MySQL 首次创建竞争的 deadlock、Postgres 唯一冲突等映射为版本冲突。
- 前端冻结草稿读取时的三个版本，一次请求保存，409 不自动重试。写入/回读失败后禁写，显式刷新后继续；授权版本变化或读取权限撤回时不再显示旧草稿。

关键失败反馈环：

| 场景 | 修复前失败信号 | 修复后 |
| --- | --- | --- |
| 三次独立保存中一项失败 | `expected 2 to be 0`，另外两项已写入 | 单次请求，失败不留下部分价格 |
| legacy 旧版本批量 | 预期 conflict，实际 nil | 409，早先键值与版本一起回滚 |
| 首次创建竞争 | MySQL 返回 1213；恢复旧 upsert 的 Postgres 单键用例出现两个成功写者 | 只有一个成功，另一个为 conflict |
| 后台读取替换草稿版本 | 预期原版本 1，实际发送新版本 2 | 原草稿始终发送版本 1 |
| 撤权后加载完成 | 页面仍显示 `private-model` 草稿 | 清除受保护草稿显示，保存禁用 |

第二轮仅更新 Admin 镜像 `deploy-20261016-144012-9390ef4a`，实际 ID `sha256:3f6f6af68ee11db6dd15ef079b2ad41805504a5c539952e59a9d666f968bb71b`，源码摘要 `71a500c31f5003cfe36d64a40c879d960f1a7d0d601a392726d8a26f1b9cb6d5`。其余六个相关进程保持原镜像；七份环境摘要仍一致，健康正常、restart=0，outbox acknowledged=1353、pending=0。

真实普通管理员会话提交安全负例：过期批量 HTTP 409、缺少两项 map HTTP 400、未认证写入 HTTP 401；操作前后三个值及其版本逐项相等，没有修改用户售价。存储失败与部分模型授权的事务回滚来自临时实库故障注入，不在生产制造存储故障。

前端独立发布，保留旧哈希资源，最后原子替换 index。最终 80 个文件的宿主机内容、Admin HTTP 静态资源及 `/admin/pricing` 返回的 index 逐项核对；index SHA256 为 `482a472f1fae273f4283c757c53d0482e0e58dcad0efe9880a200ac941cefd71`。未将资源摘要校验描述为本轮真实生产浏览器点击验收。

## 本地与实库验收

- 完整 `make verify` 在定价事务改动后通过：标准 Go 单元 172 包、完整 race 范围、分层、迁移治理、RBAC 863 项、生成类型、前端 lint/540 测试/190 critical、构建和六项预算。
- 最终撤权修复后，前端完整 lint、541 测试、190 critical、TypeScript/生产构建及六项预算通过。两轮真实 IAM 浏览器十项均通过，skipped=0。
- 临时 MySQL 8.4/Postgres 16 全量迁移、repeat、元数据与故意失败负例通过；相关 native reservation、供应商报告及证据路径实库回归通过。
- SQLite/MySQL（含 clientFoundRows）/Postgres 定价 stale、并发、首次创建、存储失败/模型授权拒绝回滚的 race 回归通过；HTTP/SSE/WebSocket 12 项契约版本/缺 usage/断流/隔离回归和 owner 三库旧证明、恢复、回滚验收通过。
- develop `9390ef4a` 的 Backend、Frontend、安全检查已成功；其 Docker arm64 矩阵仍运行，不能将本地通过或 v0.35.1 Release 成功替代该后续提交的所有 CI 任务。

## 运行边界与剩余事项

当前现存订阅账号仅 kimi/minimax/zhipu 各一，三条最新快照均未暂停，Codex 账号为零。本轮没有可确认的现存 Codex 误暂停对象，也未自动恢复任何账号；快照是覆盖写，原始历史响应未持久化，不能证明已删除账号或更早时点无误暂停。

14:42:05 CST 只读观察固定截止 14:40:04：Relay 进程自 **10:48:16.088515 CST** 启动，927 个 15 秒采样连续、无失败或缺口。当前原始计数只有 legacy Responses SSE success=332、stream_error=2，没有 orchestrator 样本，不能建立可比新旧 cohort 或正式七天起点。保持 flag=true、allowlist=2 及 legacy，不扩大灰度。近 24h 596 笔 consume 与 596 个 dedupe 对应，账本关联异常、过期 reserved、released 后 consume 均为零；四个自然对账轮 completed/0 差异。运行事实同步到 [观察手册](../design/v0.23-executor-observation.md)。

独立供应商完整账期仍未通过。用户完成 DeepSeek 登录后已定位官方用量页，但浏览器工具随后禁止该 URL，网站操作立即停止；没有解析或导入新导出，也没有以先前明确未关账的材料确认费用、归因或扩面。

## 备份与回滚

两轮私有备份保存在生产 `/opt/micro-one-api/docker-compose/backups/review-plan-20261016-104249` 和 `review-plan-20261016-112020`，目录 0700、敏感文件 0600，包含所属数据库、Compose、旧前端和一致性 outbox；不上传数据库或凭据。后者另外保留撤权草稿修复前的前端。

第一轮共享回滚标签 `rollback-20261016-104335`，Admin 单独回滚标签 `rollback-20261016-144012`。回滚 Admin 时先恢复与旧接口匹配的前端，再恢复镜像；保留新表、账本、outbox、迁移及审计，不用旧数据库备份覆盖部署后的财务记录。源码摘要以镜像标签为准，Compose 的旧 container source label 不是本轮编译来源。
