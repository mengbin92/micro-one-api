# Micro-One API v0.42.0 发布：只读业务操作审计台与操作人用户名

> 2026-11-12 · 上一版：[v0.41.0](./release-v0.41.0.md)（2026-11-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.42.0)

v0.42.0 是 v0.41.0 之后的 **MINOR 功能版本**：管理后台新增只读「操作审计」页与 `GET /api/v1/admin/operation-audits` 端点，直接读取 shared `resource_write_audits` 表，支持按操作人、操作码前缀、资源 ID、结果与时间范围筛选；操作人默认显示用户名，解析失败时回退数字 ID。新增 Admin 150 三方言迁移（两个读取索引），不改动计费契约与既有 API。

版本判 MINOR 而非 PATCH 的依据：新增面向运维的公开读取能力（新端点、新页面）与配套迁移，向后兼容，不修改既有行为。

## 新增内容

### 1. 只读业务操作审计读取面

**内容**：`resource_write_audits` 一直在 IAM 写事务内与业务变更原子落库，但此前没有读取入口，运维只能直连 owner 数据库查询。本版新增固定执行点 `admin.business_audits`（operation `iam.audit.read`，要求 global scope），并提供 biz / data / server / service 四层只读实现：

- `GET /api/v1/admin/operation-audits`，GET-only，响应带 `Cache-Control: no-store`。
- 筛选：`actor_user_id`（精确）、`operation`（转义后的前缀匹配）、`resource_id`（精确）、`result`（`success` / `failure`）、`start_time_ms` / `end_time_ms`（毫秒，闭区间）、`page` / `page_size`（默认 1 / 20，上限 100）。
- 排序：`occurred_at DESC, event_id DESC`，与索引方向一致。
- 返回字段：事件 ID、操作人 ID 与用户名、操作码、资源 ID、决策版本、原因、发生时间、结果。**不返回请求正文或凭据**。

**索引**：Admin 150 新增 `resource_write_audits_time (occurred_at, event_id)` 与 `resource_write_audits_operation_time (operation, occurred_at, event_id)`，覆盖最新优先分页与操作码前缀过滤。三方言内容一致，纯 `CREATE INDEX`，可 `DROP INDEX` 回滚。

### 2. 操作人用户名

**内容**：审计表只存 `actor_user_id`，用户名在 identity schema，跨库不能 JOIN。读取时按**唯一 actor 去重**调用 identity `GetUser` 补齐 `actor_username`：

- 每次调用带 2s 超时，不阻塞审计列表。
- 调用方缺少 `identity.user.read` / `identity.users.read`、账号已删除、或 identity 短暂不可用时，该字段为空，前端回退显示 `#<id>`，`title` 保留数字 ID 便于追溯。
- 用户名不写入审计表，避免历史数据与改名产生不一致。

### 3. Web 操作审计页

- 新增懒加载 `OperationAuditsPage`，入口位于「监控与分析」分组，路由 `/admin/operation-audits`。
- 页面准入与接口调用都要求 `iam.audit.read`；筛选支持操作人 ID、操作码、资源 ID、结果、开始/结束时间，结束时间按**整分钟**包含。
- 详情弹窗展示原因与格式化后的决策版本；空态、加载骨架、错误重试与权限受限提示齐备。

## 安全设计

- 端点固定在 console IAM guard 白名单内，仅 `GET`；未注册的兄弟路径仍返回 404。
- 服务层在查询前再次校验 `admin.business_audits` + `iam.audit.read` + global scope，HTTP 层与资源层双重门控。
- `operation` 前缀匹配使用参数绑定并对 `!`、`%`、`_` 做转义（`ESCAPE '!'`），阻断通配符注入。
- 分页、结果枚举、长度与时间区间在触库前校验；非法输入返回 400 而不是空结果。
- 审计存储不可用时返回 **503**（可重试），与内部错误 500 区分。
- 用户读权限不足只降级显示数字 ID，不泄露用户资料，也不让审计读取失败。

## 兼容性

- 新增 Admin 150 迁移（MySQL / Postgres / SQLite 三方言），纯追加索引，不改表结构与数据。
- 新增一个只读端点与一个前端页面，既有 API、计费契约、价格与写入路径不变。
- **部署顺序约束**：`admin.business_audits` 是编译在 `domain/authorization` 中的执行点声明，admin-api、identity-service 及其他 authorizing 服务必须部署**同一份代码**。只更新 admin-api 时，identity 会返回 `ErrIAMInvalidRelation`，上层统一渲染为 `authorization denied`。
- **权限前提**：`iam.audit.read` 需授予运维角色（scope `all`）。线上默认仅 root 角色持有，`platform_admin` 未授予，需要显式补授权。

## 升级步骤

1. 构建前端：`make web-dist`（admin-api 从 `ADMIN_WEB_ROOT` 读取，Docker Compose 下是宿主机 `/opt/web/dist` 只读挂载，重建 admin-api 不会更新它）。
2. 应用 Admin 150：`MIGRATIONS_DRIVER=mysql MIGRATIONS_DSN=... go run ./cmd/migrate -dir ./migrations`，或按 `migrations/150_add_resource_write_audit_read_indexes.sql` 在 `oneapi_admin` 执行并登记 `schema_migrations`（`applied_at` 为 Unix 秒）。
3. 同时部署 `admin-api` 与 `identity-service`（同一 commit）。
4. 同步 `/opt/web/dist`（先备份）。
5. 如需给非 root 运维账号使用，在 IAM 管理台授予 `iam.audit.read`（scope `all`）。

回滚：索引可 `DROP INDEX resource_write_audits_time, resource_write_audits_operation_time` 并删除对应 `schema_migrations` 记录；服务回滚到上一镜像即可，无数据回写。

## 验证

- CI run `43222139066`（develop `3480fafe`）：**success，25/25 job 全绿**，含 Backend（OpenAPI / tests / race / architecture / commit body / 迁移治理 / RBAC 契约 / IAM browser / 生成文件一致性）、Frontend（lint / test / critical coverage / build）、Integration、**Migration smoke (MySQL + Postgres)**、Docker Build ×18。
- 本地：gofmt、Go 全量单测与 race、architecture、migration-check、RBAC 矩阵（962 行）与契约、IAM browser（13）、markdown links（369）、`go mod tidy -diff`、wire-check、api-check 全绿。
- 2026-11-12 线上热更新验证（同源工作区构建）：admin-api 部署镜像的 `source.digest` 与提交后本地 `rbac-source-digest.py` 一致；identity-service 于同日早前部署同一份执行点代码，其后只有 admin / 前端 / 测试改动，不影响其二进制。容器 running、restarts=0、无 panic/fatal/ERROR；新端点返回 401（进入 guard）而兄弟路径返回 404（证明路由注册）；`FORCE INDEX` 显示 `Backward index scan; Using index`；`oneapi_admin` 库 72 条真实审计记录未被改动。
- 已知限制：完整登录态数据验收需要 root 或已获 `iam.audit.read` 的账号。本机 `make verify` 的 format / RBAC / Go 全量单测 / race / architecture / migration / RBAC 契约 / IAM browser / 生成文件门禁全部通过，但 web 全量 vitest 有 1 例既有 jsdom 超时（`PlaygroundPage.test.tsx:64`，756 例中 1 例；单文件复跑 15/15 通过），`test:critical` 与 `build`（含 bundle 预算）单独通过；以 CI 的 Frontend job 为准。

## 完整变更日志

- `9136f67d` feat(admin): add read-only business operation audit console API
- `3480fafe` feat(web): add operation audit page to the admin console
