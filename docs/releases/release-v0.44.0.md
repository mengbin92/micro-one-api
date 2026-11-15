# Micro-One API v0.44.0 发布：编排器全量切换开关与账务结算闭环补偿

> 2026-11-15 · 上一版：[v0.43.1](./release-v0.43.1.md)（2026-11-15）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.44.0)

v0.44.0 是 v0.43.1 之后的 **MINOR 功能版本**：为 relay-gateway 的 orchestrator 执行路径补齐全量切换开关，并补上 commit 结算 RPC 失败后的账务闭环补偿。默认配置下所有行为与 v0.43.1 完全一致，无新增迁移、无 API 与计费契约变更、无前端改动。

版本判 MINOR 而非 PATCH 的依据：新增 `relay_orchestrator.full_rollout` proto 字段、`RELAY_ORCHESTRATOR_FULL_ROLLOUT` 环境变量、`GetRelayOrchestratorFullRollout` / `SetRelayOrchestratorFullRollout` 配置面，属于向后兼容的新功能入口；对照 [v0.43.1 发布公告](./release-v0.43.1.md) 中「判 PATCH 的依据是不新增功能、不改变既有行为」，本次不满足。账务补偿修复与非流式异常计数对称本身是 PATCH 性质，同批发布按最高级别判 MINOR。

## 1. 全量切换开关（根因 → 修复 → 影响服务）

- **根因**：orchestrator 灰度只能靠 `RELAY_ORCHESTRATOR_TOKEN_HMAC_SHA256` allowlist 逐 token 放行，缺少一次性把 staging 端点流量全量切到 orchestrator 的机制；回滚只能清空 allowlist。
- **修复**：`internal/conf/relay_conf.proto` 的 `RelayOrchestrator` 新增 `bool full_rollout = 3`，经 `make config` 生成 `FullRollout` 字段；`configs/config.yaml` 与 compose 透传 `RELAY_ORCHESTRATOR_FULL_ROLLOUT`（默认 `false`）；wire 接线把开关传入 `HTTPServer`。`routeAllToRelayOrchestrator()` 要求 `enabled && full_rollout` 同时为 true（fail-closed），开启后 Chat Completions / Responses POST / Messages POST 三端点全部进入 orchestrator 并忽略 allowlist；**gate 只选择执行路径，bearer token 鉴权仍在 executor plan 内完成**。回滚把该变量改回 `false`（或关闭 `enabled`）即可恢复 allowlist 灰度 / legacy 路径。
- **影响服务**：仅 `relay-gateway`。刻意保持 WS 升级请求、`/v1/responses/*` 子资源、raw 端点与无 model 的续传回落走原路径。

## 2. 账务闭环补偿

- **根因**：上游已响应后 commit 结算 RPC 失败时，请求已服务但结算与用量日志丢失；服务端 commit 幂等，但传输错误可能掩盖「已持久化」的事实。
- **修复**：commit 增加 3 次有界重试（50ms / 100ms 退避，billing commit 服务端幂等不会重复扣费）；重试耗尽后通过 `ListRequestAttempts` 按 reservation 查询持久状态，billing 已提交则合成成功响应、金额取账存 `actual_cost`，继续 usage log、渠道计数与订阅记账；未确认则保留 `commit_error` 指标与带诊断状态的告警日志，交由过期清理与人工跟进。
- **影响服务**：`relay-gateway`（`internal/server/http_billing.go`）。

## 3. 非流式终态异常信号对称

- **根因**：`final_attempt_semantics_missing` 异常计数只在流式 finalizer 中触发，非流式 verified 且携带 cache 桶却无语义声明的 envelope 在进入 billing ambiguous 保守路径前没有隔离信号。
- **修复**：抽出公共函数 `recordFinalUsageSemanticsAnomaly`，流式 finalizer 与非流式 attempt 结算前对称调用。
- **影响服务**：仅 `relay-gateway` 内部计数与隔离观测。

## 4. Code review 防御性加固

- billing `ListRequestAttempts` 在调用方缺 `billing.account.cost.read` 时会把 `ActualCost` 抹 0 并置 `CostFieldsVisible=false`；恢复改为拒绝以 0 金额合成成功响应，退回保守 `commit_error` + `committed_cost_hidden` 状态，避免订阅账户与会话窗口记账静默漏记。
- billing 按 `attempt_number ASC` 排序，状态查询改为有界翻页（PageSize=100、最多 3 页）。
- 一次结算只记一个 quota outcome：`commit_recovered` 与 `commit_success` 互斥，避免指标分母 +2。
- 告警 `attempt_state` 全分支改为非空诊断 token；usage 语义隔离改用独立 detached context，避免 3 次重试耗尽 commit context 的 10s deadline；重试循环开头检查 `ctx.Err()`。
- 同步 `docs/design/rbac/entry-matrix.csv` 4 行 SOURCE 摘要与评审说明（CI 门禁要求），`docs/deployment.md` 补鉴权与回滚边界，只读观察脚本补采新开关。

## 兼容性与升级

- **默认行为零变化**：`full_rollout=false`（且 `enabled=false`）时三端点仍按原 allowlist / legacy 规则路由。
- 无 schema 迁移、无 API / 计费契约变更、无前端产物更新；`relay_conf.pb.go` 是 `make config` 生成的 gitignored 产物，随源码一起生成即可。
- 升级步骤：用同一 commit 重新构建并部署 `relay-gateway`；如需全量切换，先确认 `RELAY_ORCHESTRATOR_ENABLED=true`，再设置 `RELAY_ORCHESTRATOR_FULL_ROLLOUT=true` 并重建容器。生产 Compose 位于服务器 `$DEPLOY_REMOTE_DIR/docker-compose`，`.env` 需加入新变量。
- 观察窗口（`docs/design/v0.23-executor-observation.md`）期间保持 `full_rollout=false`。
- 回滚：把 `RELAY_ORCHESTRATOR_FULL_ROLLOUT` 改回 `false` 并重建 `relay-gateway`，无需改动 billing 或其他服务，无数据回写。

## 验证

- `go build ./...` 通过；`go test ./internal/server/... ./internal/conf/... ./cmd/relay-gateway/... -count=1` 6 个包全绿（新增 7 个单测与 5 个 review 补强用例，另补 2 个端点子用例）。
- `make config` 重新生成后 `relay_conf.pb.go` 含 `FullRollout`；`wire ./cmd/relay-gateway` 输出与手工同步的 `wire_gen.go` 零差异。
- `make rbac-contract-check` 962 行通过、`rbac-matrix-refresh.py --check` 通过；markdown links 373 文件通过；gofmt 与 `git diff --check` 干净；gosec 改动文件无 G115/G124/G505（仅生成文件既有 G103/G104）。
- **未跑 `make verify`**：本机 web 全量 vitest 存在 4 例 1s 超时的既有基线，本轮无前端改动，未重跑；以 CI Frontend job 为准。此条如实登记，不表示 `make verify` 通过。
- **未做生产验证**：本版只完成本地提交与发版制品，线上重部署与观察窗口另行执行。

## 完整变更日志

- `feat(relay): add orchestrator full rollout switch and commit settlement recovery`
- `docs(release): v0.44.0`

详见 [CHANGELOG.md](../../CHANGELOG.md) `## [0.44.0]` 与 `git log v0.43.1..v0.44.0`。
