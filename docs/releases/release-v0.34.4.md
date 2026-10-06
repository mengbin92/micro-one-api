# Micro-One-API v0.34.4 发布：代码扫描与前端依赖安全修复

> 2026-10-06 · 上一版：[v0.34.3](./release-v0.34.3.md)（2026-10-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.4)

v0.34.4 是 v0.34.3 之后的 **PATCH 安全修复版本**：关闭 GitHub Code Scanning 中三条配置查询告警和四条前端依赖漏洞告警，保留已有查询绑定、IAM/CAS、组件样式和权限详情行为。

**更新 `config-service` 和前端 `web/dist`**。无新增 API/proto、数据库迁移或运行时配置。

## 修复内容

### 1. 配置查询的静态分析告警

**根因**：配置 get/set/delete 使用 GORM 的键值 map 作为查询参数。实际生成的查询已经绑定参数，但动态 map 形状被 CodeQL 识别为用户输入进入 SQL 构造的位置。

**修复**：三处条件改为固定 SQL 文本、显式占位符和固定 `clause.Column{Name: "key"}`；列名继续由数据库方言安全引用，用户命名空间和 key 始终作为绑定值。新增含引号、OR 条件和 DROP 文本的读取/更新/删除测试，证明只影响目标记录，其他记录不变；原有 tombstone、版本 CAS 与 IAM 行为通过回归。main 的 CodeQL 复扫自动将 #321、#323、#324 标记为 fixed。

**影响服务**：`config-service`。不改变授权范围或配置业务语义。

### 2. 四条前端依赖漏洞

**根因**：前端只导入 shadcn CLI 包中的 CSS，却安装了其 Express/MCP、文件匹配和 CSS 解析工具链，引入 `braces`（CVE-2026-93687）、`proxy-addr`（CVE-2026-90711）和 `postcss-selector-parser`（CVE-2026-104844）；已有样式构建依赖还使用受影响的 `source-map-js` 1.2.1（CVE-2026-93749）。braces 没有可升级的上游修复版。

**修复**：将 shadcn 4.7.0 的相同样式变体保留为本地 `src/styles/shadcn.css`，附原 MIT 许可，移除未使用的 CLI 及其依赖链；通过 override 要求 `source-map-js` 1.2.2 及后续兼容修复版。无需替换现有组件或改变页面权限逻辑。npm audit 的 9 个根/连带告警归零，Trivy 对 Go 和前端全部依赖（含开发依赖）均为零漏洞；main 复扫自动关闭 #325–#328。

**影响范围**：前端依赖清单、构建工具链与样式来源。

## 兼容性说明

- 无 API/proto、数据库迁移、配置或 IAM 权限变更。配置查询继续使用绑定参数，保留方言列名引用和事务行为。
- 本地样式保留原 keyframes、data-* 变体和 scrollbar utility，现有 shadcn/base-ui 组件继续工作。仓库构建依赖不再包含组件生成 CLI。
- 其他服务沿用此前已验证的修复；本版只替换 config 镜像和挂载前端。

## 升级步骤

1. 备份当前 config 镜像、Compose 与 `/opt/web/dist`。
2. 本机按 `app/config/Dockerfile` 交叉构建 `linux/amd64` config 镜像，上传、校验并加载；服务器不构建。
3. 使用 `docker compose up -d --no-deps --no-build config-service` 更新，核对健康与环境一致性。
4. 单独构建并发布前端静态资源，保留旧 hash 资源并最后原子替换入口；复验配置读取、权限详情和既有用量/价格页面。

## 验证

- SQL 恶意输入绑定、config data/biz 全包、race、既有 IAM/CAS 和非生成代码 gosec 检查通过；RBAC 829 行契约通过。
- npm audit 为零；Trivy 0.75.0 对 go.mod 和前端锁文件（含开发依赖）均报告零漏洞。
- 前端 215 项单元测试、生产 build/体积预算和桌面/手机权限详情 Chrome 回归通过。
- GitHub main Code Scanning 当前开放告警为零。七条告警均由新扫描自动标记 fixed，dismissed_at 全部为 null；[安全扫描](https://github.com/mengbin92/micro-one-api/actions/runs/37451924450) 包含 CodeQL 与 Trivy 结果。
- 2026-10-06 18:51（Asia/Shanghai）完成 config 和前端更新：config 健康 200、restart 0、运行时环境逐项未变；173 个前端文件与入口摘要核验一致。真实会话的 owner-backed 配置列表和公开状态均为 200。回滚镜像、资源备份、扫描状态及脱敏证据见 [安全修复记录](../runbooks/evidence/patch-v0.34.4-security-2026-10-06.json)。

## 完整变更日志

- `fix(security): remove vulnerable CLI dependencies and clarify bound queries`
- `docs(release): v0.34.4`

