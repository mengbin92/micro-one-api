# Micro-One-API v0.26.2 发布：Relay 模型路由与双语界面修复

> 2026-08-31 · 上一版：[v0.26.1](./release-v0.26.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.2)

v0.26.2 是 v0.26.1 之后的 **PATCH Relay 路由与 Web 界面修复版本**：统一模型标识的大小写和扩展上下文后缀处理，恢复显式渠道模型映射的优先级，并同步中英文界面文案和法律页面翻译。

本版本不新增公共 API、proto 或数据库迁移。Relay 的模型匹配兼容已有配置，同时修复大小写不一致、客户端 `[1M]` 标记泄漏以及渠道映射被注册表标识覆盖的问题；Web 变更需要同步部署 `web/dist`。

## 1. 统一 Relay 模型标识处理

**根因**：部分 fallback、sticky、proxy、gRPC 和权限路径按大小写精确匹配模型；客户端使用的 `[1M]` 扩展上下文标记还可能进入路由、计费或上游请求，导致已有模型配置无法命中或上游返回 502。

**修复**：在 Relay 边界剥离末尾的 `[1M]` 标记，使精确映射、白名单和能力检查大小写不敏感；保留选中渠道配置的上游模型拼写，并将相同规则应用到重试、恢复路由、WebSocket sticky route、OneAPI proxy、HTTP 和 gRPC 路径。

**影响服务**：`relay-gateway`；涉及模型权限、渠道选择、重试 / failover、计费模型名和上游请求模型名。

## 2. 恢复显式渠道模型映射优先级

**根因**：渠道的 `upstream_model_id` 在解析时先于渠道 `model_mapping` 生效，导致注册表返回的标识覆盖渠道明确配置的上游模型名。

**修复**：按文档约定先应用渠道显式模型映射，再使用 `upstream_model_id` 和渠道模型列表作为 fallback；补充 GLM 生产配置与 `[1M]` 后缀组合的回归测试。

**影响服务**：`relay-gateway`；已有显式 `model_mapping` 的渠道将按配置向上游发送模型名。

## 3. 同步中英文界面文案

**根因**：管理端和用户端仍有硬编码中英文文案、无障碍标签及动态消息，语言切换只能更新部分页面；依赖空格拼接的动态片段在翻译归一化后还可能出现粘连。

**修复**：将导航、分页、Token、用量、渠道、日志、法律页面等文案统一接入共享翻译目录，增加完整句子的占位符插值，并补充英文法律文案和回归测试。

**影响服务**：Web 前端和托管静态资源；使用主机 `web/dist` 挂载的部署需要同步新的构建产物。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：无迁移、无数据格式变更；不需要执行数据库升级。
- **模型配置**：已有大小写不同的模型映射和白名单现在可以统一命中；显式 `model_mapping` 按既有文档优先于 `upstream_model_id`。
- **客户端标记**：末尾 `[1M]` 仅作为客户端扩展上下文提示，不会再发送给上游或作为计费模型名。
- **部署**：Relay 运行时变更需要更新 `relay-gateway`；前端变更需要同步 `web/dist`。按项目部署约束在本地或 CI 交叉构建 `linux/amd64` 镜像，不在生产服务器构建。
- **回滚**：可回滚 `relay-gateway` 到 v0.26.1，并恢复上一版 `web/dist`；无 schema 回滚步骤。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.2
```

1. 使用本地或 CI 的 `linux/amd64` 交叉构建并更新 `relay-gateway`，按显式服务列表执行 `docker compose up -d --no-deps relay-gateway`。
2. 在 `web` 目录执行前端构建，并将新的 `web/dist` 同步到部署主机的静态资源目录；主机挂载 `/opt/web/dist:/web:ro` 时无需重启容器。
3. 如果部署方式不是主机静态目录挂载，而是由 `admin-api` 提供前端资源，则按对应方式更新或重建 `admin-api`。
4. 验证 GLM / DeepSeek 的大小写映射、渠道显式 `model_mapping`、`[1M]` 请求、重试 / sticky 路由，以及中英文切换和法律页面。
5. 不执行数据库迁移；其他未涉及的服务无需因本版本单独重启。

## 验证

- `make test-unit`
- `make test-race`
- `./scripts/check-architecture.sh`
- `make migration-check`
- `cd web && npm run lint && npm test -- --run && npm run build`
- `cd web && npm run build:clean`

## 完整变更日志

- fix(relay): honor explicit channel model mapping
- fix(relay): normalize model routing identifiers
- fix(web): synchronize bilingual interface copy
