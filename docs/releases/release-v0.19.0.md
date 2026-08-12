# Micro-One-API v0.19.0 发布：兼容性契约、迁移治理与测试分层

> 2026-08-12 · 上一版：[v0.18.4](./release-v0.18.4.md)（2026-08-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.19.0)

v0.19.0 是 v0.18.4 之后的 **MINOR 稳定化版本**（4 个提交，`c0ddf36` → `27485b7`），不含新产品功能，主线是「兼容性守护 + 迁移治理 + 可发布性」：为刚修复的 Responses ↔ Anthropic / Chat 协议转换链路建立显式契约矩阵，为迁移体系补上静态一致性门禁，把 integration/e2e 测试分层纳入 CI，并为 v0.18 新落地的基础设施包（xgrpc、resilience、auth、crypto、tracing、timeout）补齐直接单测。

**无 API 破坏性变更、无数据库迁移、无 proto 变更、无配置变更**。唯一生产代码改动是 `platform/tracing` 的一处缺陷修复（见 §4）。受影响服务：无强制重新部署；若使用裸 `host:port/path` 形式的 OTLP endpoint，relay/admin 等服务重新部署后可获得正确的 trace 上报路径。

## 1. 协议兼容性契约矩阵（c0ddf36，P1.1）

- **根因**：协议适配测试过去随具体 bug 生长（web_search 问题连续三个递进提交才根治），缺少统一的契约表；新增 provider/tool/adaptor 路径时，未覆盖的组合只能靠部署后暴露。
- **修复**：`internal/apicompat/compatibility_matrix_test.go` + `internal/server/compatibility_matrix_test.go` 以「注册表 + 覆盖断言」实现契约矩阵——每个坐标（方向 × 路径 × 流式）注册一个具体 check，`TestCompatibilityMatrix_Coverage` / `TestServerCompatibilityMatrix_Coverage` 断言期望坐标全部注册且无孤儿 cell，新增路径漏注册即测试失败。共享 fixture（`matrix_fixtures_test.go`）统一定义 web_search / server_tool_use / tool round-trip 样例，覆盖全部三种 server-side web-search tool 变体（`web_search`、`google_search`、`web_search_20250305`，与 `convertResponsesToAnthropicTools` 的 drop list 一一对应）。规则文档见 `docs/design/v0.19-compat-matrix.md`。
- **影响服务**：无（纯测试 + 文档）。

## 2. 迁移治理静态门禁（2d89511，P1.2）

- **根因**：根 MySQL 迁移存在两个 `057_*` 文件、SQLite 迁移存在两个 `009_*` 文件；runner 以完整文件名作为版本因此暂不冲突，但序号重复增加人工判断、跨方言映射与未来自动化风险，且 ownership.yaml 覆盖与实际文件漂移只能靠人工发现。
- **修复**：新增 `cmd/migrate-check`（`make migration-check`，纯文件检查、无需数据库）：新增重复数字前缀硬失败（历史重复进入 `migrations/dialect-manifest.yaml` allowlist）；ownership.yaml 引用必须存在、每个可运行根迁移必须被认领或显式豁免（补入 `061_add_billing_schema_system_options` → billing，该 MySQL-only view 列入 `not_applicable`）；`auto_mirror_from_prefix: "072"` 起根迁移必须逐字镜像到 postgres/sqlite。SQLite fresh + incremental 生命周期测试（`platform/database/migrate/sqlite_lifecycle_test.go`）对 scratch 库应用真实 sqlite 迁移树：全新安装每个迁移恰好执行一次、二次 Apply 为空操作、增量升级只执行尾部文件并达到与全新安装一致的 schema。
- **影响服务**：无（治理工具 + 测试）；backend CI 已挂钩 `make migration-check`。

## 3. CI 测试分层（bde8385，P1.3）

- **根因**：默认单测主动排除所有 `*/internal/integration` 包（绑定真实 loopback 监听），CI 没有单独跑这些集成包；compose e2e 与前端 Playwright smoke 无自动化执行。
- **修复**：`ci.yml` 新增 `integration` job——所有 `internal/integration` 包每次 PR 在普通 runner 上运行；backend job 增跑 `make migration-check`。新增 `.github/workflows/nightly.yml`（每日 02:00 UTC + 手动触发）：compose e2e 套件 + Playwright admin smoke，失败时通过 `E2E_KEEP_ON_FAILURE` 保留容器并采集 `docker compose ps` / 全量服务日志为工件。`scripts/test-e2e-flow.sh` 自动探测 `docker-compose` / `docker compose`（GH runner 只有 v2 插件）。`make test-integration` 增加空包列表 guard：integration 包若被误删/重命名导致匹配为空，目标硬失败而非退化为对仓库根目录跑 `go test` 静默通过。
- **影响服务**：无（CI/脚本）。

## 4. 基础设施单测补强 + OTLP 路径修复（27485b7，P1.4）

- **修复（唯一生产代码变更）**：`normalizeOTLPEndpoint` 对裸 `host:port/path` 形式返回无前导斜杠的 path，`otlptracehttp.WithURLPath` 会拼出 malformed URL（platform-L3 回归家族）；修正为与带 scheme 分支一致返回 `/path`。
- **测试**：`platform/grpc/xgrpc`（trace 传播 + 指标拦截器，含 v0.18 P2 C5 依赖延迟 label 回归）、`platform/grpc/resilience`（isRetryableError 表、relay-H1 `ErrCircuitBreakerOpen` sentinel、platform-H1 非可重试错误不熔断）、`platform/security/auth`（platform-M4 缺 exp 拒绝、吊销 blocklist、RBAC；包加入 race 门禁）、`platform/security/crypto`（AES-GCM 往返 / nonce 随机化 / 错误路径）、`pkg/timeout`、`platform/tracing`（含上述修复的回归用例）。
- **影响服务**：仅当 OTLP endpoint 配置为裸 `host:port/path` 形式时，重新部署后 trace 上报路径才正确；其余部署无行为变化。

## 兼容性说明

- **API / proto / 数据库 / 配置**：全部无破坏性变更、无迁移、无新增配置项。
- **行为变化**：仅 §4 的 OTLP 路径归一化修复影响 trace 上报 URL 构造（修复 malformed URL 场景）；其余全部为测试、治理工具与 CI 变更。
- **升级**：无强制升级需求。如需 OTLP 修复，按标准流程重新构建部署使用该配置的服务即可。

## 升级步骤

```bash
git fetch --tags
git checkout v0.19.0
# 无强制重新部署；若需要 §4 的 OTLP 修复，重新构建部署对应服务。
```

## 验证

- `make test-unit`：77 包通过、0 失败。
- `./scripts/check-architecture.sh`：通过。
- `make migration-check`：通过（历史重复前缀 allowlist 警告为预期输出）。
- `make test-integration`：5 个 integration 包全部通过。
- `go test -race ./platform/security/auth/...`：通过。

## 完整变更日志

- test(apicompat): add protocol compatibility contract matrix (v0.19 P1.1)
- feat(migrations): add static migration governance gate (v0.19 P1.2)
- ci: add integration job and nightly e2e workflow (v0.19 P1.3)
- test(platform): add infrastructure unit tests, fix OTLP path normalization (v0.19 P1.4)
