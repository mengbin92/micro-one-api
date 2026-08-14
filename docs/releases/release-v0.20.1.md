# Micro-One-API v0.20.1 发布：nanoid CVE 修复与 v0.21 路线图落地

> 2026-08-14 · 上一版：[v0.20.0](./release-v0.20.0.md)（2026-08-14）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.1)

v0.20.1 是 v0.20.0 之后的 **PATCH 修复版本**（2 个提交，`5c89752` → `64a6ed6`），主线是「供应链安全修复 + 文档治理」：升级管理前端传递依赖 nanoid 3.3.17 → 3.3.18 修复 CVE-2026-67213（随机 ID 生成死循环 DoS），并将 v0.19–v0.20 执行记录归档、确立 v0.21 路线图为唯一规划入口。

**无 API 破坏性变更、无 proto 公共契约变更、无数据库迁移、无配置变更**；唯一运行时相关变更是前端 `package-lock.json` 依赖版本。受影响服务：admin-api（仅管理前端 web/dist 构建产物，且 nanoid 为构建期工具链依赖）。

## 1. 修复 nanoid CVE-2026-67213（`64a6ed6`）

- **根因**：管理前端构建工具链（postcss 传递依赖）锁定 nanoid 3.3.17，该版本存在随机 ID 生成的无限循环 DoS 缺陷（CVE-2026-67213），被代码扫描告警。
- **修复**：`web/package-lock.json` 升级 nanoid 3.3.17 → 3.3.18。nanoid 仅被 postcss 在构建期用于生成 CSS 源映射等内容哈希，不进入浏览器运行时 bundle；同提交顺带把 lockfile 根包的 `engines` 字段同步为 `node >=24 <25`（与 v0.19.1 引入的 `web/.nvmrc` 对齐）。
- **影响服务**：admin-api（仅 web/dist 前端构建链）；重新构建前端产物后告警消除，运行时行为无变化。

## 2. 文档治理：v0.19–v0.20 归档与 v0.21 路线图（`5c89752`）

- **根因**：v0.19 / v0.20 两个版本的规划与执行条目散落在 `docs/TODO.md` 与 `docs/README.md` 中，已完成项与未完成项混杂，缺少单一规划入口。
- **修复**：新增 `docs/design/v0.19-v0.20-execution-record.md` 归档两版全部执行记录；新增 `docs/design/v0.21-roadmap.md` 作为唯一规划入口；`docs/README.md` / `docs/TODO.md` 精简并指向新文档。纯文档变更，无运行时影响。
- **影响服务**：无。

## 兼容性说明

- **API / 公共 proto**：无破坏性变更、无对外契约变更。
- **数据库**：无迁移。
- **配置**：无变更。
- **行为变化**：无运行时行为变化；nanoid 为前端构建期依赖，不影响浏览器 bundle。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.1
# 无需数据库迁移、无需配置变更。
# 如需消除代码扫描告警，重新构建管理前端产物即可：
#   cd web && npm ci && npm run build
# 服务端无需重新部署（无生产代码变更）。
```

## 验证

- `web` 构建：`npm ci && npm run build` 通过，产物行为与 v0.20.0 一致。
- 代码扫描：nanoid CVE-2026-67213 告警消除。
- 无生产代码变更，服务端无需回归。

## 完整变更日志

- fix(web): bump nanoid to 3.3.18 for CVE-2026-67213
- docs: archive v0.19-v0.20 record and establish v0.21 roadmap as sole entry
