# Micro-One-API v0.26.3 发布：Relay 路由、双语界面与发布门禁修复

> 2026-09-01 · 上一版：[v0.26.2](./release-v0.26.2.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.3)

v0.26.3 是替代未完成发布的 v0.26.2 的 **PATCH 修复版本**：完整包含 Relay 模型标识归一化、显式渠道模型映射优先级和中英文界面同步，并修复生成 API 类型未提交、本地化无障碍标签导致 Release E2E 门禁失败的问题。

v0.26.2 tag 已创建，但对应 Release workflow 未完成，未产出完整 GitHub Release 和多架构镜像；请直接部署 v0.26.3。本版本不新增公共 API、proto 或数据库迁移；Relay 变更需要更新 `relay-gateway`，Web 变更需要同步部署 `web/dist`。

## 1. 统一 Relay 模型标识并恢复显式映射优先级

**根因**：部分 fallback、sticky、proxy、gRPC 和权限路径按大小写精确匹配模型，客户端 `[1M]` 扩展上下文标记还可能进入路由、计费或上游请求；同时 `upstream_model_id` 先于渠道 `model_mapping` 生效，覆盖了渠道明确配置的上游模型名。

**修复**：在 Relay 边界剥离末尾 `[1M]` 标记，统一模型精确映射、白名单和能力检查的大小写处理；按文档约定优先应用渠道显式 `model_mapping`，再使用 `upstream_model_id` 和渠道模型列表作为 fallback，并将归一化规则覆盖到重试、恢复路由、WebSocket sticky route、OneAPI proxy、HTTP 和 gRPC 路径。

**影响服务**：`relay-gateway`；涉及模型权限、渠道选择、重试 / failover、计费模型名和上游请求模型名。

## 2. 同步中英文界面文案

**根因**：管理端和用户端仍有硬编码中英文文案、无障碍标签及动态消息，语言切换只能更新部分页面；依赖空格拼接的动态片段在翻译归一化后还可能出现粘连。

**修复**：将导航、分页、Token、用量、渠道、日志、法律页面等文案统一接入共享翻译目录，增加完整句子的占位符插值，并补充英文法律文案和回归测试。

**影响服务**：Web 前端和托管静态资源；使用主机 `web/dist` 挂载的部署需要同步新的构建产物。

## 3. 修复 Release 前端门禁

**根因**：OpenAPI schema 新增的 canonical usage 字段未同步到已提交的 `web/src/types/api.ts`，导致 CI 的生成类型一致性检查失败；界面默认中文化后，Playwright 登录种子仍只按英文无障碍名称定位“Admin Overview”，使 Release E2E 冒烟测试在会话初始化阶段级联失败。

**修复**：重新生成并提交 Web API 类型，补齐 36 个 canonical usage 字段；将管理端 E2E 的关键无障碍定位器改为中英文兼容表达式，继续以角色和可访问名称进行稳定定位。

**影响服务**：Release CI、Web 类型契约和管理端 Playwright 冒烟测试；无生产运行时 API 行为变化。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：无迁移、无数据格式变更；不需要执行数据库升级。
- **模型配置**：已有大小写不同的模型映射和白名单现在可以统一命中；显式 `model_mapping` 按既有文档优先于 `upstream_model_id`。
- **客户端标记**：末尾 `[1M]` 仅作为客户端扩展上下文提示，不会再发送给上游或作为计费模型名。
- **发布替代**：v0.26.2 未产出完整 Release 制品，不建议移动或复用该 tag；v0.26.3 是包含其完整变更的替代版本。
- **部署**：Relay 运行时变更需要更新 `relay-gateway`；前端变更需要同步 `web/dist`。按项目部署约束在本地或 CI 交叉构建 `linux/amd64` 镜像，不在生产服务器构建。
- **回滚**：可回滚 `relay-gateway` 到 v0.26.1，并恢复上一版 `web/dist`；无 schema 回滚步骤。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.3
```

1. 从 v0.26.1 或未完成发布的 v0.26.2 直接升级到 v0.26.3，不移动或复用 v0.26.2 tag。
2. 使用本地或 CI 的 `linux/amd64` 交叉构建并更新 `relay-gateway`，按显式服务列表执行 `docker compose up -d --no-deps relay-gateway`。
3. 在 `web` 目录执行前端构建，并将新的 `web/dist` 同步到部署主机的静态资源目录；主机挂载 `/opt/web/dist:/web:ro` 时无需重启容器。
4. 如果部署方式不是主机静态目录挂载，而是由 `admin-api` 提供前端资源，则按对应方式更新或重建 `admin-api`。
5. 验证 GLM / DeepSeek 的大小写映射、渠道显式 `model_mapping`、`[1M]` 请求、重试 / sticky 路由，以及中英文切换、法律页面和管理端登录冒烟流程。
6. 不执行数据库迁移；其他未涉及的服务无需因本版本单独重启。

## 验证

- `make test-unit`
- `make test-race`
- `./scripts/check-architecture.sh`
- `make migration-check`
- `cd web && npm run lint && npm test -- --run && npm run build`
- `cd web && npm run build:clean`
- `cd web && npx playwright test e2e/admin-smoke.spec.ts`
- GitHub Actions `CI / Frontend`（生成 API 类型、lint、Vitest、build）

## 完整变更日志

- fix(web): align generated types and localized E2E
- fix(relay): honor explicit channel model mapping
- fix(relay): normalize model routing identifiers
- fix(web): synchronize bilingual interface copy
