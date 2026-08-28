# Micro-One-API v0.24.0 发布：全新双语控制台与中国法律协议

> 2026-09-04 · 上一版：[v0.23.2](./release-v0.23.2.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.24.0)

v0.24.0 是 v0.23.2 之后的 **MINOR Web 体验与运营基础版本**：完成用户端与管理端的
可访问性设计重构，新增中英文切换、响应式登录 / Playground 流程、语义化图表，
并为中国大陆部署补齐用户协议、隐私政策、注册显式同意与可配置的运营主体信息。

本版本 **无数据库迁移、无公共 API / proto 破坏性变更、无新增 relay 运行时行为**。
受影响的运行时仅为 `admin-api` 和 `web/dist`；`relay-gateway` 在 executor 7 天观察完成前
不应重建或重启。

## 1. 重构 Web 设计基础与核心页面

**根因**：旧控制台的设计令牌、响应式布局、焦点态、加载 / 空状态和数据图表语义不一致，
登录、Playground、用户仪表盘和管理端页面在桌面与移动视口下缺少统一的可访问体验。

**修复**：重建字体、颜色、间距、阴影和动效令牌，统一导航、表格、卡片、加载与错误状态；
迁移用户控制台和管理后台主要页面，重设登录 / 注册与 Playground 流程，补齐响应式布局、
键盘操作和语义化图表；同步刷新 Micro-One API 图标和字标。

**影响服务**：`web/dist`。无后端契约或数据格式变化。

## 2. 新增全局中英文界面

**根因**：原语言按钮没有共享 locale 状态，大量用户端、管理端文案和数字 / 日期格式
仍硬编码为中文，切换语言后界面不会完整重新渲染。

**修复**：新增持久化 `I18nProvider`、语言切换组件、英文长尾文案目录和产品术语覆盖；
用户端与管理端页面按 locale 显示文案、数字和日期，并将用户选择保存到本地偏好。
中文字体资产和 OFL 授权跟随 Web 构建分发。

**影响服务**：`web/dist`。默认语言仍为 `zh-CN`，现有用户无需迁移偏好。

## 3. 增加中国用户协议、隐私政策与运营主体配置

**根因**：公开控制台没有用户协议和隐私政策，注册流程也没有显式同意；不同自托管
部署无法公示各自的运营者、注册地址和隐私联系方式。

**修复**：增加公开 `/terms` 和 `/privacy` 页面，在注册流程中要求显式勾选；
`admin-api` 系统选项增加 `LegalOperatorName`、`LegalOperatorAddress` 和
`LegalContactEmail`，公开 `/api/status` 仅返回这三项公示数据。法律页面在信息不完整时
显示明确警告，不会使用虚构的运行时默认值。

**影响服务**：`admin-api`、`web/dist`。新选项为向后兼容空值，不需要数据库迁移。

## 4. 修复英文注册协议流程

**根因**：法律页面在初始双语目录生成后加入，英文注册页的协议勾选文案、链接、
校验错误和新增后台配置仍保留中文。

**修复**：补齐上述文案的人工英文术语映射，保留中文法律正文的中国大陆适用边界；
新增英文注册同意、校验错误、法律链接和配置完整态测试。测试资料使用明显占位值，
不写入生产默认值。

**影响服务**：`web/dist`；`admin-api` 仅测试数据对齐。

## 5. 增强 Claude `Edit` 工具链路回归

**根因**：既有 Anthropic native SSE 测试只覆盖单分片 `Read` 工具，不能证明文件编辑所需的
`file_path`、`old_string` 和 `new_string` 跨多个 `input_json_delta` 时仍按原顺序传递。

**修复**：将 native Anthropic 流回归升级为分片 `Edit` 调用，断言工具名称、每个 JSON 分片和
`stop_reason=tool_use` 全部保留。该变更为测试基线，不修改 relay 运行时代码。

**影响服务**：无运行时影响；用于区分 relay 传输损坏与上游模型 / Claude 客户端的
`Error editing file` 问题。

## 兼容性说明

- **API / proto**：无破坏性变更。`/api/status` 只新增三个可选的公开字段。
- **数据库**：无新增迁移。法律主体值复用现有系统选项存储。
- **配置**：无必填环境变量。对外开放注册前，应在系统设置填写真实的
  `LegalOperatorName`、`LegalOperatorAddress` 和 `LegalContactEmail`。
- **法律文本**：随仓库提供的中文文本是工程模板，部署者仍需根据自身主体、业务与
  适用法律完成审核。
- **Relay**：v0.24.0 不增加 relay 运行时变更；develop 上重复的 Responses / Anthropic
  修复与观察记录已属于 v0.23.2 发布范围，不要因 v0.24.0 重新部署 relay。

## 升级步骤

```bash
git fetch --tags
git checkout v0.24.0
```

1. 备份当前 `/opt/web/dist` 和 `admin-api` 镜像。
2. 在系统设置填写并复核真实运营者名称、注册地址与专用联系邮箱；
   不得将发布测试中的 `示例科技有限公司` / `privacy@example.com` 用于公开生产环境。
3. 按既有跨平台流程构建并部署 `admin-api`，再构建和发布 `web/dist`。
4. 从未登录窗口验证中英文登录 / 注册、协议勾选、`/terms`、`/privacy` 与
   `/api/status` 的三项公示值。
5. executor 观察期间不构建、加载、重建或重启 `relay-gateway`，不修改
   `RELAY_ORCHESTRATOR_ENABLED` 或 allowlist。

## 验证

- `npm run lint`：通过。
- `npm run test`：41 个测试文件、146 个测试通过。
- `npm run build` 与 `npm run build:clean`：通过。
- `go test ./internal/server ./internal/adaptor ./internal/apicompat ./app/admin/internal/server ./app/admin/internal/service`：通过。
- `check-architecture.sh`、`check-deployment-docs.sh` 和 Markdown 链接检查：通过。
- 视觉验收：1440×900 桌面登录 / 英文界面，390×844 移动注册 / 法律页面无主要布局回归。
- executor 生产观察仍以
  [`v0.23-executor-observation.md`](../design/v0.23-executor-observation.md) 为唯一事实源；
  v0.24.0 发布前须完成 PASS / ROLLBACK 判定。

## 完整变更日志

- feat(web): establish accessible design foundation
- feat(web): migrate console surfaces and status details
- feat(web): refine dashboards and semantic charts
- feat(web): redesign authentication and playground workflows
- docs: document completed web redesign
- feat(web): refresh Micro-One API logo
- feat(web): enable bilingual interface
- feat(web): add China legal agreements
- fix(web): complete bilingual legal consent
- test(relay): cover fragmented edit tool input
- docs: prepare v0.24 release candidate

`develop` 上还包含 `fix(relay): bridge responses through anthropic api-key channels` 以及两条对应的
canary 记录；它们是 v0.23.2 release branch 修复的等价同步，已列入
[v0.23.2 完整变更日志](./release-v0.23.2.md#完整变更日志)，不重复计为 v0.24.0 功能。
