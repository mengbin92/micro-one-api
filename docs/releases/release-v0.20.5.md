# Micro-One-API v0.20.5 发布：Responses 工具历史兜底与发布 E2E 门禁

> 2026-08-18 · 上一版：[v0.20.4](./release-v0.20.4.md)（2026-08-16）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.20.5)

v0.20.5 是 v0.20.4 之后的 **PATCH 生产稳定性版本**（4 个提交，`254970a` → `e219741`），主线是「Responses fallback 协议正确性 + 发布质量门禁闭环」：Responses → Chat Completions 兜底改用共享协议转换器，修复 Codex / DeepSeek 工具历史中的并行调用、乱序输出、孤儿输出与中断调用导致上游 400 的问题；管理后台修复 proto3 `omitempty` 造成禁用模型状态显示 `undefined` 的问题；release 工作流在镜像和 GitHub Release 发布前强制执行与 nightly 共用的 compose E2E + Playwright admin smoke。同时补齐 2026Q3 P3 观察基线的数据质量结论。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无应用配置变更**。受影响范围：relay-gateway、admin-api（含管理前端）、release / nightly workflow 与执行文档。生产如需获得修复，需重新部署 relay-gateway，并按前端发布流程同步 `web/dist`；其他服务无需重新部署。

## 1. Responses fallback 修复工具历史协议（`e219741`）

- **根因**：relay-gateway 的 `/responses` → `/chat/completions` fallback 此前使用独立的手工转换逻辑，只按原始顺序逐项映射。Codex 发送的 Responses 历史可能包含并行 tool calls、乱序 tool outputs、孤儿输出、中断调用，以及 call 与 output 之间的 developer notice；DeepSeek 对 assistant `tool_calls` 后必须紧跟对应 `tool` message 的约束更严格，原始形状会被拒绝并导致 fallback 400。
- **修复**：
  - fallback 改用 `internal/apicompat.ResponsesToChatCompletionsRequest` 共享转换器；
  - 合并同一轮的并行 tool calls，并把 reasoning summary 附到对应 assistant tool-call message；
  - 按调用 ID 匹配并重排 tool outputs，确保每个 assistant `tool_calls` 后紧跟对应 `tool` 回复；
  - 过滤未收到输出的中断调用与孤儿 tool 输出，不再把 call/output 之间的 notice 错误插入协议序列；
  - 无 input 的请求保留显式空 user message，instructions / developer 消息转换语义与共享转换矩阵一致。
- **影响服务**：relay-gateway 的 Responses fallback；原生 `/responses` 上游与原生 Chat Completions 请求不受影响。
- **行为变化**：仅改变 fallback 转换后的 Chat Completions 请求体。原先被上游拒绝的复杂工具历史现在会以合法 Chat Completions 消息序列转发。

## 2. 管理后台禁用模型状态显示（`e219741`）

- **根因**：模型管理 API 的 proto3 响应使用 `omitempty` 序列化，`status=0`（禁用）会从 JSON 中省略。前端把缺失状态直接交给状态徽章和操作按钮渲染，导致禁用模型显示为 `undefined`，详情对话框也无法稳定展示“禁用”。
- **修复**：`listModels` / `getModel` 在前端 API 层把缺失的 `status` 归一化为 `0`；表格、状态徽章、启停操作和详情对话框均可正确显示禁用状态。
- **影响服务**：admin-api 前端 `web/dist`；后端模型管理 API 无变更。

## 3. release 发布前置 E2E 门禁（`b1f067d`）

- **根因**：nightly E2E 已达到连续 5 次双 suite 成功，但 release 工作流此前只构建并推送镜像、创建 GitHub Release，没有在发布目标 tag 上执行同类 E2E；发布前质量门禁与 nightly 证据来源存在漂移风险。
- **修复**：
  - 新增 reusable `.github/workflows/e2e.yml`，统一承载 compose E2E suite 与 Playwright admin smoke；
  - nightly 调用该 workflow，保持原有诊断与失败工件上传能力；
  - release 通过 `release-context` 解析目标 tag，先在该 tag 上执行 E2E；E2E 失败时 Docker build/push 与 GitHub Release 均不会执行；
  - E2E 失败时继续上传 compose 日志、容器状态与 Playwright report，便于定位。
- **影响服务**：release / nightly CI；不改服务运行时。
- **发布闭环**：本版本是接入该门禁后的第一次真实 release。tag E2E、镜像发布与 GitHub Release 全部成功后，v0.21 roadmap 中“release 挂钩 admin e2e”完成闭环。

## 4. nightly 稳定性与 P3 观察基线更新（`254970a` / `a296295`）

- **nightly 稳定性**：2026-08-17 scheduled main nightly run `31989324792` 双 suite 成功，达到最终修复后连续 5 / 5 准入标准；同日 reusable workflow 远端调用验证 run `32087512854` 成功。
- **P3 基线数据质量**：核实 Prometheus 存储健康（retention 15d、磁盘充足），快照约 41h 历史来自 P3-0 指标上线时间而非保留窗口缺陷；Grafana Viewer Service Account 只读凭据已建立并保存在服务器侧环境变量，下季度可直接采集 dashboard 快照。
- **影响范围**：执行 / 观测文档；不修改服务运行时。

## 兼容性说明

- **API / 公共 proto**：无变更。
- **数据库**：无新增迁移。
- **配置**：无应用配置变更。
- **行为变化**：Responses fallback 转换后的 Chat Completions 消息序列更符合严格上游约束；复杂工具历史不再原样透传。管理后台禁用模型显示从 `undefined` 修正为“禁用”。
- **CI**：release 发布被 tag 基线 E2E 阻断；nightly 与 release 共用同一 E2E 工作流。
- **部署**：需要获得 Responses 修复时重新部署 relay-gateway；需要获得模型状态显示修复时同步发布管理前端 `web/dist`（admin-api 镜像本身无需重建，除非要发布其他服务变更）。release 工作流会照常发布全服务镜像，但生产可继续按受影响服务滚动更新。

## 升级步骤

```bash
git fetch --tags
git checkout v0.20.5
# 无数据库迁移、无应用配置变更。
# 1) 按仓库标准流程重新构建并部署 relay-gateway。
# 2) 构建并同步 web/dist 到 /opt/web/dist，获得模型禁用状态显示修复。
# 3) 如仅部署服务镜像，admin-api 镜像不包含前端 volume，仍需单独同步 web/dist。
```

## 验证

- develop CI run `32106145061`（`e219741`）：Backend、Frontend、Integration、MySQL / Postgres migration smoke、架构 / 生成文件检查与 18 个 Docker build matrix 全部通过。
- develop Security Pipeline run `32106145066`（`e219741`）：CodeQL、gosec、govulncheck、gitleaks、Trivy、SBOM 与 license scan 全部通过。
- nightly / reusable E2E 证据：main scheduled run `31989324792` 连续第 5 次成功；reusable workflow 远端验证 run `32087512854` 双 suite 成功；2026-08-18 main scheduled run `32093155262` 成功。
- 定向回归：`go test ./internal/server` 通过，新增空 input、developer message、复杂 DeepSeek 工具历史重排测试；`ModelsPage.test.tsx` 10/10 通过，新增后端省略 `status=0` 的列表与详情用例。
- 发布验证：tag `v0.20.5` 上的 Release E2E gate 成功后，Docker 多架构镜像与 GitHub Release 由 `release.yml` 自动发布。

## 完整变更日志

- docs(observability): close P3 baseline data-quality gaps (2026Q3)
- docs(ci): record nightly e2e stability 4/5
- ci(release): gate releases with shared e2e workflow (#15)
- fix: harden responses fallback and model status display
