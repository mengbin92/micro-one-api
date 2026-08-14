# Micro-One-API v0.20.2 发布：上游成本管理与账本对账守护补强

> 2026-08-14 · 上一版：[v0.20.1](./release-v0.20.1.md)（2026-08-14）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.2)

v0.20.2 是 v0.20.1 之后的 **PATCH 补强版本**（5 个提交，`3cde415` → `538f80e`），主线是「运营侧上游成本可配置 + 资金对账常驻守护」：管理后台新增「上游成本」页面，支持按渠道 / 订阅账号 / 全局裸模型配置每 1M tokens 的上游采购价，并补充缓存读取价格与 legacy 键迁移能力；对账脚本新增 ledger ↔ dedupe claim 双向覆盖检查，防止迁移窗口孤儿账本再次静默漏检。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无配置变更**。受影响范围：admin-api（含管理前端 web/dist）与运维侧 `scripts/reconcile` 工具。

## 1. 新增管理后台「上游成本」页面（`9391d5f` / `9f854dc`）

- **根因**：上游采购价此前只能通过配置键手工维护，运营无法区分渠道、订阅账号与全局裸模型，也无法查看 legacy 键迁移进度。
- **修复**：新增 `/admin/upstream-costs` 页面，复用现有 `GET/POST/DELETE /api/admin/upstream-costs` 与 `/migrate` 接口；支持新增、编辑、删除、legacy 键 dry-run 预览与确认迁移，删除带确认弹窗；注册侧边栏导航与总览快捷入口，风格对齐现有 PricingPage。
- **影响服务**：admin-api（管理前端 web/dist）。

## 2. 上游成本支持缓存价格与可靠可选字段语义（`365df12` / `538f80e`）

- **根因**：上游成本条目此前只支持 input/output 价格，无法配置缓存读取采购价；JSON `null` 与「字段未发送」无法区分，导致可选价格不能显式清空；负数价格只依赖前端约束，服务端缺少兜底校验；legacy 迁移完成后无法得知实际执行数量。
- **修复**：
  - 上游成本条目新增 `cache_read_price`，并预留 `cache_creation_5m_price` / `cache_creation_1h_price`；
  - 引入 `*_set` 请求标记：显式发送可清空可选价格，字段缺失则保留原值；
  - 服务端拒绝负的 input/output/cache 价格；
  - 迁移结果返回 `executed`，目标键在执行间隙已存在时会计入 `skipped`，不再误报全部成功；
  - 前端新增缓存读取价格列与编辑输入框，保存 payload 显式标记 `cache_read_price_set`。
- **影响服务**：admin-api（含管理前端 web/dist）。

## 3. 账本 dedupe claim 覆盖完整性检查（`3cde415`）

- **根因**：v0.20.0 将资金幂等闸门迁移到 `billing_ledger_dedupe_claims` 后，如果迁移与代码滚动部署之间存在窗口期写入，会出现「ledger 有 dedupe key 但没有 claim」的孤儿账本。旧检查只校验 ledger 键本身，不能发现这类覆盖缺口。
- **修复**：`scripts/reconcile/checks.go` 新增 `checkClaimCoverage`，双向校验「ledger 无 claim」与「claim 无 ledger」；任一方向存在孤儿行都会使对账失败。该检查已按 v0.20 首个结算周期生产数据完成正 / 负路径验证，并作为常驻 DB 侧守护。
- **影响服务**：运维侧对账工具；不改运行服务。

## 兼容性说明

- **API / 公共 proto**：无破坏性变更、无 proto 变更。上游成本内部管理接口新增的可选缓存价格与 `*_set` 字段均为 additive；未发送新字段的旧请求行为保持不变。
- **数据库**：无迁移，无表结构变更。
- **配置**：无变更。
- **行为变化**：
  - 上游成本保存现在会在服务端拒绝负价格；
  - 显式发送 `*_set=true` 可清空对应可选价格；
  - 对账脚本发现 ledger ↔ claim 覆盖缺口时返回失败（这是预期的资金安全守护增强）。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.2
# 无数据库迁移、无配置变更。
# 按仓库部署流程重新构建并部署 admin-api 镜像。
# 管理前端需重新构建并同步 web/dist 到 /opt/web/dist。
# 运维侧如使用 scripts/reconcile，同步本次脚本后重新执行对账验证。
```

## 验证

- `go test ./app/admin/...`：通过（含新增上游成本可选价格、负数校验、显式清空、迁移 skip/executed 计数回归用例）。
- `go test ./scripts/reconcile/...`：通过（该包当前无测试文件，验证可构建）。
- `cd web && npm test`：30 个测试文件、109 个用例全部通过（含「上游成本」页面 7 个用例）。

## 完整变更日志

- feat(reconcile): v0.21 P0 settlement reconciliation record and claim coverage check
- feat(web): add upstream cost management page
- fix(web): upstream cost page review fixes
- feat(admin): upstream cost supports cache read price
- fix(admin): upstream cost optional prices and migration results
