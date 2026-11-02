# Micro-One-API v0.39.0 发布：B4——用户模型状态与定向公告

> 2026-11-02 · 上一版：[v0.38.1](./release-v0.38.1.md)（2026-11-01）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.39.0)

v0.39.0 是 v0.38.1 之后的 **MINOR 功能版本**：发布下一阶段排期 B4 工作包——用户控制台"模型与公告"（本人可用模型的聚合路由状态、定向/定时/到期公告与版本化已读）和管理员"公告管理"，并附带三项安全扫描与依赖修复。版本判 MINOR 的依据：新增 4 个公开 HTTP 端点与 Admin 148 迁移（四张公告表，三方言）、运行账号新授权与 `admin.content` 权限链、两个前端新页面，不能按 PATCH 判。

**B4 代码已先于本 tag 部署生产**（2026-11-02，镜像 `deploy-20261102-111927-377e9028` + 宿主机 Web dist），迁移 148 与运行账号授权均已落库，部署与验证证据见 [B4 验收](../runbooks/b4-user-operations-acceptance-2026-11-02.md)；本次发版补齐 release note / CHANGELOG / README / tag 工件，由 CI 构建多架构镜像并创建 GitHub Release。真实管理员的公告管理旅程与真实用户的已读旅程仍需浏览器人工补验，不因本 tag 通过。

## 功能

### 1. 用户控制台：模型与公告

**根因**：用户无法感知自己当前默认路由组下哪些模型可用及聚合状态，平台也没有面向用户的公告触达渠道，运营信息只能靠外部渠道。

**新增**：

- `/api/user/model-availability`：返回本人当前默认路由组下的模型名、聚合可用状态与检查时间；状态为最长 15 秒缓存的路由快照。用户响应只含模型名、状态与检查时间，不含来源、分组细节或他人信息。
- `/api/user/announcements` 与 `/api/user/announcements/read`：已发布且未到期公告列表（含定向与分组受众的本人可见过滤）、按公告 + 用户 + 版本唯一约束的已读保存。
- 身份边界：用户入口先证明本人身份、读取当前路由事实并验证访问，再用固定的 Admin 服务能力读取模型投影；不接受浏览器传入的分组或身份提示，查询参数不能选择其他用户或分组。
- Web"模型与公告"页：公告正文为纯文本、日期按北京时间展示；已读状态、过期空页、刷新失败与分页边界齐全；英文无中文残留。

**影响服务**：`admin-api`（用户端点与投影）、`channel-service`（路由事实）、Web。见 [B4 契约](../design/b4-user-operations-contract-2026-11-02.md)。

### 2. 管理员：公告管理

**根因**：运营公告缺少平台内管理面，定向投放、定时发布与到期下架无法自助完成。

**新增**：`/api/admin/announcements` 支持草稿、定时发布（按真实发布时间冻结显示，拒绝下架时间早于发布时间）、到期自动失效与主动下架；受众支持全体、用户当前默认路由组和指定用户三类；公告写入使用 revision 条件更新，内容、定向索引与审计在同一事务保存，审计表 append-only。管理接口受全局 IAM 与既有权限链保护，普通用户访问管理端点被拒绝。

**影响服务**：`admin-api`、Web 管理页；新增 Admin 148 迁移与运行账号四表最小授权（MySQL 含已读表 UPDATE，匹配 GORM `ON DUPLICATE KEY UPDATE` 实际语句）。

### 3. 共享正确性修复

- 管理模型注册表查询与多组列表合并曾吞掉存储错误、把故障显示为空模型；现在错误向上传递，组不存在或禁用仍按既有语义跳过（回归先红后绿）。
- 公共价格请求的取消信号与档位保存后的缓存失效沿用 v0.38.1 修复，不在本版重复。

### 4. 安全扫描与依赖修复

- 关闭备份扫描 findings 与 `minimax`/`minimatch` 咨询（PR #29）：仅测试与依赖面，不改生产行为。
- `follow-redirects` 升级（Dependabot #28）：web 开发依赖安全修复。

## 兼容性说明

- **API**：新增 4 个公开端点（3 个用户 + 1 个管理），既有端点不变；用户端响应不含受众列表、管理状态或审计字段。
- **数据库**：新增 Admin 148 迁移（四张公告表，MySQL/PostgreSQL/SQLite 三方言）；生产已落库，重复执行应为 no-op。服务运行账号不承担 DDL。
- **配置与依赖**：无强制新配置；无新增第三方运行时依赖。运行账号需按 [权限模板](../../migrations/manual/148_announcement_runtime_grants.example.sql) 授权（生产 `iamd_admin_*` 已授）。
- **资金与历史记录**：不涉及计费路径，不重算历史账单。
- **回滚**：Web → Admin/Channel 顺序回滚，保留 148 表与记录，不删表不回写数据；新 Web 配旧 Admin 功能不可用，不能只回滚后端。

## 升级步骤

1. 生产侧 B4 已于 2026-11-02 部署（Channel/Admin/Web + 迁移 148 + 运行授权）；本 tag 发布后核对运行镜像与本版 commit 一致即可。新环境顺序：先应用 148 并按模板授权，再更新 Channel/Admin，最后更新 Web dist。
2. 验证：匿名访问三个公告/模型端点应 401；运行账号对四张公告表 SELECT 通过；管理员创建定向公告 → 目标用户可见 → 已读保存 → 下架后用户侧不可见的完整旅程。
3. 将旅程验证结果回填 B4 验收 runbook。

## 验证

| 状态 | 证据与边界 |
| --- | --- |
| ✅ | 三库迁移 fresh/repeat、存储边界及并发 race（12 并发已读仅一条、8 并发同版本保存仅一个成功、审计失败回滚） |
| ✅ | MySQL 最小运行权限专项：旧权限真实复现 1142，模板授权后首读/重复已读/版本更新通过；回归直接应用实际模板防漂移 |
| ✅ | 三库真实 HTTP/gRPC owner：legacy 与 IAM、普通用户不可管理、角色声明不能越权、双协议 CAS |
| ✅ | Web 733 项测试、196 项关键路径、`npm run build` 体积预算、桌面/移动 Chrome 8 项验收（含受众切换清空 ID、慢保存锁定、冲突保留草稿） |
| ✅ | 完整 `make verify`；OCR 全量 33 文件 16 条意见：11 条整改、5 条复核不采纳，无未解决 P0/P1 |
| ✅ | 生产部署：镜像/来源摘要一致、匿名 401、运行账号 SELECT 通过、无 1142（runbook §5） |
| ⏳ | 保留项：真实管理员公告旅程与真实用户已读旅程的浏览器人工补验；不随本 tag 通过 |

## 完整变更日志（v0.38.1 → v0.39.0）

```
6dec307b docs(runbook): record B4 production deployment evidence
34a5f5a4 Merge branch 'codex/b4-user-operations' into develop
377e9028 feat: add personal model status and targeted announcements
a202bd7d Merge pull request #29 from mengbin92/codex/fix-security-pipeline-alerts
2f2cc80c fix(security): resolve backup scan findings and minimatch advisory
a19f12f2 chore(deps): bump follow-redirects (#28)
7889bc9e docs(runbook): close out deploy source label correction
```
