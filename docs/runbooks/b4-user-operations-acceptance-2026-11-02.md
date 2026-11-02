# B4 用户运营可见性验收

2026-11-02（北京时间）。状态：工程验收及机器评审整改通过，待部署；未发版。

## 1. 冻结范围和交付

维护者确认的范围见 [B4 契约](../design/b4-user-operations-contract-2026-11-02.md)。用户控制台新增“模型与公告”；管理员新增“公告管理”。公告支持全体、用户当前默认路由组和指定用户，草稿、定时发布、到期和主动下架，版本化已读。正文为纯文本，日期按北京时间展示和编辑。

用户模型响应只含模型名、聚合状态和检查时间；公告响应不含受众列表、管理状态或审计。查询参数不能选择其他用户或分组。公告写入使用 revision 条件更新，内容、定向索引和审计在同一事务保存；已读按公告、用户和版本唯一。

## 2. 验证记录

| 检查 | 状态 | 证据 |
| --- | --- | --- |
| MySQL 8.4、PostgreSQL 18、SQLite 迁移 fresh/repeat | ✅ 通过 | 既有 scratch DB 工具执行三库真实迁移，重复执行无新增项 |
| 三库存储边界及 race | ✅ 通过 | `TestAnnouncementsOwnerDialects`、`TestAnnouncementsFilterBeforePaginationDialects`；12 并发已读仅一条、8 并发同版本保存仅一个成功、审计失败回滚内容和定向 |
| MySQL 最小运行权限及 race | ✅ 通过 | `TestAnnouncementMySQLRuntimeReceiptGrants`：旧权限真实复现 1142 拒绝；直接应用 148 权限模板后首次/重复已读、版本更新及本人列表通过。MySQL GORM 的去重语句需要已读表 UPDATE，PostgreSQL/SQLite 原生 DO NOTHING 不需要 |
| 三库真实 HTTP/gRPC owner 及 race | ✅ 通过 | `TestB4UserOperationsHTTPAndRPC`；真实 Identity/Admin/Channel，legacy 和 IAM；普通用户不可管理，角色声明不能越权，定向及分组变化、已读、会话撤销、双协议 CAS |
| 模型故障回归 | ✅ 通过 | `TestAvailableModelsDoesNotHideRegistryQueryFailure`、`TestAvailableModelUnionPropagatesGroupStorageFailure` 均先红后绿 |
| 桌面和移动 Chrome | ✅ 8 项通过 | `web/e2e/b4-operations.spec.ts`：搜索/空匹配、空模型/公告、纯文本正文、已读与失败、过期空页/翻页、刷新失败、受众切换清空 ID、北京时间、时间校验、定时/下架、慢保存锁定表单、冲突保留草稿、取消及无横向溢出；截图人工检查 |
| 前端专项 | ✅ 通过，已并入最终全量 | 公告时间及合法发布、英文标签、导航及路由既有测试 |
| TypeScript / lint / 构建与预算 | ✅ 通过 | `npm run lint`、`npm run build` |
| Go 编译与 vet | ✅ 通过 | `go build ./...`、`go vet ./...` |
| 完整 `make verify` | ✅ 整改后最终轮通过 | format、unit、race、分层、迁移、RBAC、IAM browser checker、生成类型、前端全量 733 项、关键路径 196 项及构建 |
| ocr | ✅ 已完成并复核 | 全量 diff 33 个文件、16 条意见：11 条整改、5 条经复核不采纳；Channel 单文件补扫另记 4 条存量建议。本项无未解决 P0/P1 |

真实三库验证只使用本机临时容器和独立 scratch schema，不读取生产 DSN；测试不调用真实付费来源。浏览器使用隔离 API 夹具，owner 链路由独立 Go 三库用例验证，二者分别记账。

## 3. 修复的共享根因

1. 管理模型注册表查询和多组列表合并曾吞掉存储错误，可能把故障显示为空模型；现在错误向上传递，组不存在或禁用仍按既有语义跳过。
2. 用户状态入口带有已验证会话，直接转发到内部投影会触发 managed-user 权限，阻断普通用户。现在先证明本人身份、读取当前路由事实并验证访问，再用固定的 Admin 服务能力读取模型投影；不接受浏览器分组或身份提示。公告当前分组采用同一身份边界。
3. 最小权限测试确认 MySQL 的 GORM `DoNothing` 实际生成 `ON DUPLICATE KEY UPDATE`，原 SELECT/INSERT 授权不足；已补 UPDATE，并让回归直接应用实际模板，防止文档与运行权限再次漂移。

没有新增临时日志、真实来源探测或后台调度器。

## 3.1 评审意见处置

全量会话 `2bad18a7-fe3d-47b4-8b09-a0caba0e1a40`，补扫会话 `3b6573bd-d5bf-4997-babd-4c4de0b87a25`。按本仓库口径分级，不直接沿用工具的 high/medium 标签。修复后复跑针对性回归及最终门禁。

| 优先级 / 类型 | 定位 | 影响与处理 |
| --- | --- | --- |
| ✅ P1 / 定向错误 | `web/src/pages/admin/AnnouncementsPage.tsx:38` | 分组 ID 沿用为用户 ID 有误投递风险；切换受众清空目标，浏览器断言先红后绿 |
| ✅ P2 / 运行权限 | `migrations/manual/148_announcement_runtime_grants.example.sql:6` | MySQL 运行账号无法去重写已读；补 UPDATE，真实账号验证原拒绝和实际模板恢复 |
| ✅ P3 / 契约归属 | `api/admin/v1/error_reason.proto:24` | 公告错误独立为资源错误枚举，已重新生成并通过 owner 回归 |
| ✅ P3 / 本地化、交互与重复计算 | `web/src/locales/en-US.ts:918`、`web/src/pages/OperationsPage.tsx:24`、`web/src/pages/admin/AnnouncementsPage.tsx:32` | 补三条翻译和无权限提示；复用过滤结果及两页的分页常量；校验下架时间，区分过期空页。合计 7 条意见已整改 |
| ✅ P3 / 配置格式 | `web/playwright.b4.config.ts:4` | 与既有配置统一数字分隔格式 |
| 不采纳 / 未来接口扩展 | `api/admin/v1/admin.proto:13`（评审原定位） | 两入口当前仅共用分页字段，身份与权限各自验证；不为尚不存在的管理筛选预建另一份请求。未来增加筛选时重新评估 |
| 不采纳 / 三条 JSX 样式意见 | 两个新增页面的状态和标签分支 | 现有有限状态分支类型检查、交互及异常验收通过，没有功能错误，不为统一写法增加重构 |
| 不采纳 / 时区抽象 | `web/src/lib/announcements.ts` | 当前明确固定北京时间，不引入可配置时区；往返和发布时间边界测试保证显示/编辑一致 |

合计 11 条整改（含 9 条 P3）与 5 条不采纳。另补自审回归：立即发布冻结真实时间、存储错误在 data 层映射为 typed unavailable、慢保存期间禁用表单和新建，避免后续编辑丢失。

Channel 全文件补扫的 4 条意见涉及既有刷新时长换算、旧式余额/优先级/到期字段更新语义和重复错误分支，均不在 B4 新增路径；保留为存量核验线索，未在本项扩改渠道配置写接口，也不把这些建议记为已修复。

## 4. 部署候选与回滚

受影响：Admin、Channel、Web。Identity 无新增迁移或权限；要求现有路由 v2、用户默认组、专用 Admin 服务身份和既有 `admin.content` 权限链可用。

1. 用迁移账号按 Admin ownership 应用 148，再按 [运行账号权限模板](../../migrations/manual/148_announcement_runtime_grants.example.sql)授予四张公告表的最小读写权限。服务运行账号不承担 DDL。
2. 在本机准备 Linux/amd64 的 Channel/Admin 镜像和独立 Web 制品；部署前保存回滚锚点。先更新 Channel、Admin，再更新宿主机 Web 目录。
3. 用真实普通用户/管理员验证模型投影、管理拒绝、定向与定时状态、已读保存和运行账号权限；核对健康、日志和制品摘要。
4. 旧 Web 不使用新增接口；旧 Admin 可与保留的公告表共存。回滚按 Web → Admin/Channel，保留 148 表和记录，不执行删表或数据回写。新 Web 配旧 Admin 会出现功能不可用，因此不能只回滚后端。

生产部署、真实用户运营公告、版本发布/tag 均未执行。下一步在维护者安排的发布窗口执行上述部署验收。
