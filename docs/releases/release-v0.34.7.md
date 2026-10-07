# Micro-One-API v0.34.7 发布：模型健康权限修复与控制台稳定刷新

> 2026-10-07 · 上一版：[v0.34.6](./release-v0.34.6.md)（2026-10-07）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.7)

v0.34.7 是 v0.34.6 之后的 **PATCH 权限与界面可靠性修复版本**：恢复 IAM 模型健康读取，修正 CSV 组名匹配的权限范围，阻止持续 403 引发请求循环，并完整发布此前 develop 上的后台授权刷新、Playground Markdown 与前端体积优化。

**运行时更新 `channel-service` 和宿主机挂载的 `web/dist`**。无 API/proto 变更、数据库迁移或新增运行时配置。

## 修复与改进

### 1. IAM 模型健康查询恢复，组名精确匹配

**根因**：线上服务构造器仍采用兼容 CSV 成员读取，`routingGroupRelations` 未启用，而模型健康查询在编译实际权限范围之前无条件拒绝这一路径，连 All 范围也返回 `authorization denied`。初始修复中的 `LIKE` 回退还会把合法组名 `%`、`_` 解释为通配符，部分方言忽略大小写，扩大可见范围。

**修复**：按渠道或订阅账号的真实成员关系生成查询；兼容模式使用完整 CSV token 的字节精确匹配，关系读取模式继续使用成员表。渠道/账号列表与健康读取共享安全匹配表达式。所有范围继续先应用 allow/deny，再计数、过滤和分页；All/资源 ID 范围不依赖成员表。

**影响服务**：`channel-service`。不切换分组读取开关，不修改 IAM grant、用户或数据库数据。

### 2. 持续 403 不再不断刷新页面与接口

**根因**：owner 返回 403 后，前端清理受保护查询并刷新授权摘要；摘要仍允许该操作时，同一查询被重新创建，形成无界的“403 → 刷新摘要 → 再次请求”循环。仅关闭 React Query 的 retry 不能阻止此循环。

**修复**：按查询键和真实授权 generation 保存脱敏拒绝标记；同一授权版本下的自动轮询、重挂载和缓存重建不再发出已拒绝的请求。用户可手动重试，权限/会话/凭证边界实际变化后自动恢复。手动重试从实时授权缓存验证权限，防止授权变化先于 React 重绘时发起请求；标记不保留 JWT 或响应正文。

**影响服务**：管理前端 `web/dist`。

### 3. 后台授权轮询保留页面和有效数据

**根因**：每轮授权刷新都取消 owner 请求、清缓存，路由把后台 fetching 当成首次加载而卸载页面；其他标签页修改主题或语言也触发权限重载，导致闪屏和丢失草稿。

**修复**：保留仍有效的展示快照和读缓存，执行/写入检查继续要求当前已验证摘要。摘要版本、权限、角色、会话、到期或凭证真正变化时清理旧数据和页面实例；storage 事件仅在 token 变更或 clear 时触发授权刷新。

**影响服务**：管理前端 `web/dist`。该批修复此前已部署前端，本版正式纳入 tag。

### 4. Playground 展示与验收门禁

**根因**：模型答复缺少受限 Markdown 展示，重复提交可能并发执行；打包字体增加首屏体积，关键分支和生产 bundle 缺少独立持续验收。

**修复**：按结构白名单渲染 Markdown，忽略 HTML，仅允许明确的 HTTP(S) 链接，代码手动复制，超大文本回退纯文本；修复同批重复提交。改用系统字体，移除 98 个字体分片；新增关键模块覆盖率和桌面/手机生产 bundle 浏览器门禁。

**影响服务**：管理前端 `web/dist` 与 GitHub Actions。另包含 executor 第二阶段条件复核文档，不改变 relay 运行路径。

## 兼容性说明

- 无公共 API/proto 变更、无数据库迁移、无新增配置项。
- IAM 仍由各资源 owner 授权，拒绝范围继续优先；前端摘要不授予接口权限。
- `CHANNEL_ROUTING_GROUP_DUAL_WRITE` 与成员关系读取模式保持现网配置；修复不以开启关系读取来绕过兼容边界。
- 无需重启 `admin-api`、`relay-gateway` 或其他服务；前端必须单独更新，更新 channel 镜像不会更新宿主机挂载目录。

## 升级步骤

1. 备份当前 channel 镜像、Compose 文件和 `/opt/web/dist`，记录镜像 ID、环境摘要及其他服务容器 ID。
2. 本机按 `app/channel/Dockerfile` 构建 `linux/amd64`，附源码 revision/digest 与 `0.34.7` 版本标签；保存、上传并校验镜像归档摘要，服务器不构建。
3. 加载镜像，将现网 Compose 的 channel 镜像引用明确更新为 `docker-compose-channel-service:v0.34.7`，执行 `docker compose up -d --no-deps --no-build --pull never channel-service`。
4. 本机执行 `cd web && npm run build`；单独上传产物到 `/opt/web/dist`，先更新资源文件、最后更新 `index.html`，保留回滚备份。挂载目录更新无需重启 admin。
5. 验证服务健康、restart、环境及其他服务实例，核对前端摘要，访问模型健康页确认不再出现持续 403 请求循环。

## 验证

- 全仓 `make verify` 通过：格式、单元/race、分层、迁移与 IAM 入口契约、API 类型、前端 lint/test/build 及六项预算。
- 关键路径 140 个测试通过，覆盖率满足既有门禁；总分支 85.47%，授权模块分支 87.17%、行 98.90%。
- 新增后端 All/组/资源 ID/deny/失效权限、两类来源、关系与 CSV 路径、分页总数及特殊组名回归；SQLite 实际迁移 schema 通过，MySQL/PostgreSQL opt-in 实库未配置，不声称本次已复验。
- 包含真实 `AdminRoute` 和授权生命周期的模型健康页面回归通过；同一摘要下持续 403 只请求一次接口并核查一次授权，重挂载/轮询、手动重试和真实权限变化恢复均已验证。
- 十项真实 owner IAM 浏览器矩阵全部实际通过，无跳过。
- 2026-10-07 18:32 CST 完成生产 channel 镜像及前端更新；channel/admin 健康 200、channel restart 0，环境、IAM iam/complete（policy 7、catalog 2）与其他八服务实例保持一致。回滚镜像、Compose/环境及旧前端在服务器保留，见[生产证据](../runbooks/evidence/patch-v0.34.7-production-2026-10-07.json)。
- 公网前端 HTTP 200，`index.html` SHA256 `2061a6b5c2707d13822f7eebf9226da8e4a5c0888461b7065a2d4920e04b53f7` 与本机构建一致。
- 现有管理员浏览器会话的模型健康页正常显示 11 条观测；`kimi` 筛选显示 2 条，超过一次 30 秒自动轮询后输入和数据仍保持，未显示加载失败。此为页面只读复验，未签发新 JWT、修改权限或调用模型。

## 完整变更日志

- `chore(ops): record executor stage-two condition review`
- `fix(web): stop console flash-refresh and deliver D7 playground batch`
- `fix: restore model health authorization and bound denied reads`
- `docs(release): v0.34.7`
