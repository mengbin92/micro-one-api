# v0.42–v0.44 评审问题修复记录

修复日期：2026 年 11 月 15 日；提交/发布门禁核验日期：2026 年 11 月 16 日（北京时间）。本记录覆盖本地修复与后续提交/部署验证；尚未完成的发布步骤以清单明确标记。

## 1. 修复结果

| 复核项 | 根因与修复 | 状态 |
|---|---|---|
| R1 MySQL 并发返回模型 ID 0 | 生产连接默认开启 `clientFoundRows`，冲突 no-op 的 RowsAffected 不能证明插入。改用非零模型 ID 判断，否则锁定读取获胜行；正式并发测试复用 `xdb.Open`。 | ✅ PostgreSQL/MySQL 并发及 race 回归通过 |
| R2 SQLite 名称被当作通配符 | CSV 拆分后按字面比较 token；三方言只归一 token 边界空白，保留内部空格与分组大小写，不把 `_`、`%` 当通配符。 | ✅ 三方言真实 Runner 回归通过 |
| R3 审计漏 owner | 补齐 config、log、monitor、notify 的只读 RPC、服务身份策略、业务与存储层、注册和接线，admin 聚合全部七个实际审计源。 | ✅ 正常、越权及存储失败回归通过 |
| R4 共享库重复计数 | 各源返回实际审计库的哈希身份，聚合按身份计一次记录和 total；不同数据库保持独立来源。 | ✅ 同库一条事件只计一次 |
| R5 memory 空存储 panic | 所有 owner 审计 repo 对 nil receiver/DB 返回不可用错误。 | ✅ 不再 panic |
| 5000 条窗口 | 每源仅取 `offset + limit` 的前缀；超出全局前 5000 条返回 400，提示缩小时间或筛选，拒绝不完整源批次。 | ✅ API 错误与排序/总数回归通过 |

原评审中已通过的 relay 结算恢复权限、MySQL DROP 后迁移重放、整页用户名 2 秒预算继续保留并回归。已有未提交工作区改动未被覆盖。

## 2. 读取与信任边界

- 对外入口仍为 `GET /api/v1/admin/operation-audits`，由 console 的 global `iam.audit.read` 门控。
- 六个远端 owner 的 `ListWriteAudits` 只允许 admin 专用 system identity；转发用户凭据会被拒绝。adapter 在 console 授权后移除 operator 元数据。
- RPC DTO 转换位于 [internal/writeauditclient](../../app/admin/internal/writeauditclient/source.go)，不让 data 层直接依赖 Proto DTO。
- owner 返回按 `occurred_at DESC, event_id DESC` 排序的前缀、完整 filtered total 和非空 `store_id`；时间单位为毫秒，内部 RPC 的 page 为 0-based。
- [StoreID](../../platform/database/writeaudit/store_id.go) 使用 MySQL 实例/database、PostgreSQL 实例/database/实际审计表 schema、SQLite 实际文件或进程内 pool 身份，并统一哈希。原始定位信息不经公共 HTTP 返回；身份仅用于本次聚合，不持久化。
- 任一来源失败、缺少身份或返回不完整批次时明确失败，不把不可用来源当成空审计。

## 3. 验证证据

| 检查 | 结果 |
|---|---|
| 全仓非 E2E、非外部 integration 单测 | ✅ 130 个有测试包通过，0 FAIL |
| 修改涉及服务与鉴权/StoreID 的 race | ✅ 50 个有测试包通过，0 FAIL |
| `go vet ./...` | ✅ |
| 审计前端页面、导航、路由 | ✅ 3 文件、40 测试通过 |
| 七个 data 源与 StoreID 的真实 MySQL/PostgreSQL 整合 | ✅ 8 包、57 个用例/子用例通过，0 skip |
| config/log/monitor/notify 真实 gRPC 链 | ✅ service identity → operator → service → biz → SQLite；admin 放行，relay 与转发 operator 拒绝；race 通过 |
| 迁移 151 三方言真实 Runner | ✅ 删除窗口、`_`/`%`、首尾/内部空白、大小写负例、MySQL 故障重放通过 |
| RBAC 契约与 SOURCE 摘要 | ✅ 974 项匹配 |
| Proto/Wire | ✅ 经 `make api` 和 Wire 重新生成；未手改生成物 |
| 格式与 diff | ✅ `gofmt`、`git diff --check` |
| Compose 展开 | ✅ MySQL、Postgres、lite、test/e2e overlay 的 admin 审计地址 |
| Kubernetes 静态检查 | ✅ `kubectl kustomize`、89 项 Secret/ConfigMap 引用、目标 Pod/TCP egress、routing defaults |
| Kubernetes schema 校验 | ✅ 官方固定版本工具临时安装到 `/tmp`，42 valid、0 invalid/error/skip，完整部署检查通过 |
| 独立构建 | ✅ `go build ./...`、完整 `make verify` 与 7 个 amd64 候选镜像通过 |
| 生产验证 | ✅ 7 服务与 profile worker、健康、6 owner RPC 与既有结算读取通过；[脱敏证据](evidence/release-v0.44.1-production.json) |

详细临时日志位于评审机器 `/tmp/micro-one-api-review-fixes/`；跨库审计日志位于 `/tmp/writeaudit-integration-5714215r/`，迁移边界红绿日志位于 `/tmp/retirement-boundaries-evidence/`。这些目录可能被系统清理，正式回归测试已贴近源码保留。

## 4. OCR 结果与处置

OCR 本轮结果为 **partial**：选择 56 个文件，完成 36 个，20 个任务因上游供应商网络或响应解码失败未完成，**不声明机器评审通过**。

9 条建议已逐项核对：

- 采纳六份 Proto 的排序、total、StoreID、时间单位和分页契约说明，重新生成。
- 采纳 PostgreSQL 首尾空白与 MySQL/SQLite 内部空格缺陷，先在真实 Runner 中变红，再修复。
- 不采纳分组统一转小写的建议：真实路由契约区分大小写。MySQL 改为精确比较，三方言测试覆盖 `VIP` 与 `vip`。
- 采纳 Postgres Compose 漏地址意见，并核对 lite 与 Kubernetes 的地址及精确网络许可。

OCR 原始结果和意见处置分别保存在 `/tmp/micro-one-api-review-fixes/ocr.json`、`ocr-disposition.json`。外部评审没有标为通过；2026-11-16 按项目明确允许的 `ocr delegate` 委托模式，补齐 35 项未完成/变更源码，加上已完成且未变的 27 项，共 62 项内容 hash 一致，P0/P1 为 0；[本轮门禁证据](evidence/release-v0.44.1-gates.json)。

## 5. 部署前提与下一步

本轮已在 2026-11-16 更新生产，未重跑已登记迁移。发布升级时，admin-api 与 channel/billing/config/log/monitor/notify 六个 owner 应使用包含本修复的同一版本；旧 owner 没有 RPC 或 StoreID 时会明确失败。

各 owner 必须信任 admin 专用服务凭据；共享服务 token 不授予审计 system capability。Compose 已补齐地址，Kubernetes 已补齐 ConfigMap/env 与限定目标端口的 egress；身份 Secret 由实际环境提供，不在仓库生成或记录真实凭据。

本轮按 PATCH v0.44.1 提交和发布，理由与步骤见[发布文档](../releases/release-v0.44.1.md)。三个独立修复提交、生产验证、main/tag 推送与 GitHub Release 均已完成；最终状态和流水线链接见发布文档。历史 ledger/log 对账 WARN 部署前后差异未变，未擅自改账。
