# Micro-One-API v0.8.0 发布公告

> 2026-07-16 · 上一版：[v0.7.2](./release-v0.7.2.md)（2026-07-15）

v0.8.0 是 v0.7.2 之后的 **MINOR** 版本。本版新增面向客户端的 **API 指南页**与 **CC Switch 一键导入**，并将管理后台前端从 `go:embed` 改为 `ADMIN_WEB_ROOT` 运行时提供，使前端构建产物不再进入 Git。

本版**没有新增业务表迁移**，**没有 API 破坏性变更**；但 admin-api 的前端提供方式发生运行时行为变更，重新部署 admin 镜像时需按下文配置 `ADMIN_WEB_ROOT`。

## 亮点

- **新增 API 指南页**：`/api-guide` 页面聚合 OpenAI / Claude / Gemini 的调用示例与端点发现，从 `/status` 读取 `server_address` 与 `system_name`，无需用户手填占位符。
- **CC Switch 一键导入**：Tokens 页面新增 `ccswitch://v1/import` 深链接生成，支持 Claude Code、Codex、Gemini CLI 客户端快速导入令牌与基地址；对话框状态在重开时正确重置。
- **管理前端不再内嵌进二进制**：移除 `//go:embed all:static/web`，admin 前端统一由 `ADMIN_WEB_ROOT` 提供；构建产物加入 `.gitignore`，CI 不再需要 Node 构建步骤，"Verify generated files" 步骤不再因前端资产漂移而失败。
- **支付宝证书挂载可配置**：新增 `ALIPAY_CERT_DIR` 环境变量，宿主机密钥目录可只读挂载到容器 `/cert/alipay`，默认相对 compose 文件为 `../../cert/alipay`。
- **路径遍历收敛**：`scripts/check-k8s-references.go` 中的文件读取改为基于 `os.Root` 的受限读取，不再可能逃逸出固定目录根。

## 变更内容

### Added

- 新增 `/api-guide` 页面，提供 OpenAI / Claude / Gemini 调用文档与端点发现，读取 `/status` 的 `server_address` 和 `system_name`。
- 新增 `CCSwitchDialog` 组件，生成 `ccswitch://v1/import` 深链接并接入 TokensPage。
- `/status` 端点新增 `server_address` 字段，并支持可配置的 `SystemName` 选项。
- Admin 选项页新增 `ServerAddress` 文本字段，供管理员发布客户端使用的外部 API 基地址。

### Changed

- admin-api 移除 `go:embed` 前端机制，改为仅从 `ADMIN_WEB_ROOT`（或构造参数）解析静态文件；未配置或目录不可用时返回 500 `frontend not available`，不再回退到内嵌资源。
- admin Dockerfile 将 web-builder 构建产物复制到 `/web` 并设置 `ENV ADMIN_WEB_ROOT=/web`；其余服务 Dockerfile 移除不再使用的 web-builder 构建阶段。
- `app/admin/internal/server/static/web/` 下 54 个已跟踪构建产物从 Git 移除，并加入 `.gitignore`。
- Makefile 的 `build` 目标不再依赖 `web-build`；`web-build` 不再向 embed 路径复制 dist。
- CI backend 任务移除 Node 环境与"Build embedded admin frontend"步骤。

### Fixed

- CCSwitchDialog 在 Base UI 受控 Dialog 下重新打开时状态未重置的问题；初始状态改由 `useState` 初始化器在重挂载时设置，父组件通过 key bump 强制重挂载。
- API 指南页步骤 2 文案改为指向 Connection 区域实际渲染的 baseUrl，而非占位符。
- `scripts/check-k8s-references.go` 的 gosec G304/G703 路径遍历告警：用 `os.OpenRoot` + `fs.ReadFile` / `fs.Glob` 替换 `os.ReadFile`，所有读取限制在固定目录根下。
- 支付宝证书挂载可配置：Compose lite/postgres/标准版均新增 `ALIPAY_CERT_DIR` 挂载点。

## 数据库迁移

本版没有新增编号迁移文件，也没有 schema 变更。迁移执行方式与 v0.7.2 一致：全新 MySQL 环境由一次性 `migrate` 服务按顺序执行自动迁移，成功后才启动应用。

## 破坏性变更

API 和数据库 schema 均无破坏性变更。运维侧需要注意：

- **admin 前端提供方式变更**：admin-api 不再内嵌前端资源。部署 admin 镜像时必须确保容器内存在前端构建产物并设置 `ADMIN_WEB_ROOT`（admin Dockerfile 已默认设置为 `/web`）。未设置该变量且目录不可用时，管理后台将返回 500 而非回退到内嵌资源。
- 若此前自定义了 admin Dockerfile 或运行时未设置 `ADMIN_WEB_ROOT`，升级时需同步更新。
- 部署文档中关于"二进制内嵌资源"和"自动回退到内嵌资源"的描述已随本版更新为 `ADMIN_WEB_ROOT` 模式。

## 升级步骤

```bash
git fetch --tags
git checkout v0.8.0

# 检查并替换 deployments/docker-compose/.env 中的生产密钥
cd deployments/docker-compose
docker compose --env-file .env config --quiet

# 旧数据卷升级前先备份；全新环境直接启动
docker compose --env-file .env up -d --build
```

admin 镜像由 Dockerfile 自动将前端构建产物放到 `/web` 并设置 `ADMIN_WEB_ROOT=/web`；自定义部署需手动构建前端（`make web-dist`）并设置 `ADMIN_WEB_ROOT` 指向产物目录。

Kubernetes 部署应先运行迁移，再执行 `kubectl apply -k deployments/k8s`，并等待九个 Deployment rollout 完成。完整步骤和 Secret 清单见 [docs/deployment.md](../deployment.md)。

## 验证

发布前已执行：

```bash
go build ./...                                   # 通过
go vet ./...                                     # 通过
go test $(go list ./... | grep -v '/test/e2e/suite$' | grep -v '/web/node_modules/')  # 全部通过
```
