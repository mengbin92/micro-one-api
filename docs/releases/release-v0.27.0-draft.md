# v0.27.0（草案）

> 历史草案：保留功能分支早期准备记录，不能作为正式发布说明。v0.27.0 已发布，
> 以 [正式发布说明](release-v0.27.0.md) 为准；本草案中的模型健康功能随
> [v0.28.0](release-v0.28.0.md) 正式发布，升级与验证口径以 v0.28.0 发布说明为准。

## 版本摘要

本版本把模型健康信号、canonical usage 证据和历史账务审计工具整理为可观测、可回滚
的生产准备项。模型健康监测是被动记录，不改变渠道路由或计费决策。

## 变更

1. **模型健康被动监测**：按来源类型/来源 ID、请求模型和上游模型记录终态健康，避免
   不同模型或不同渠道之间相互覆盖；成功和可归因的上游失败都会更新健康状态。
2. **Lite Compose 可验收**：SQLite 命名卷由一次性权限初始化服务交给非 root 应用 UID，
   沿用当前分支的 `sqlite-init`，并提供无 MySQL 的本地 Quickstart 和
   健康检查路径。
3. **历史账本只读审计**：输出来源、usage contract、五个 canonical 成本桶、价格快照、
   候选差额和证据分类；不写账、不调用 billing RPC。
4. **canonical 48 小时验收**：固定 CST/UTC 窗口、SQL 与 Prometheus 门禁、外部供应商
   证据要求和 charge 延后决策已形成 runbook。

## 兼容性与升级

- 生产升级前执行现有编号迁移（包含 080–091），确认备份和回滚窗口。
- Lite 仅用于本地/演示；生产继续使用 MySQL，并保留现有密钥和服务令牌。
- 不改变公开 API 的既有请求格式；新增字段均为观测/证据用途。

## 验证记录（待填）

- [ ] `go test -race` 关键 biz/data/service/server 包
- [ ] Web `npm test -- --run`、`npm run lint`、`npm run build`
- [ ] `make api-check`、`go run ./cmd/migrate-check`
- [ ] Lite Compose 九服务启动，Relay/admin `/healthz` 通过
- [ ] canonical 固定窗口 SQL 与 Prometheus 门禁结论：待 `2026-09-06 09:49:52.108 CST`
- [ ] 外部供应商 usage/账单证据索引：待补

## 完整变更日志（草案）

- `feat: add passive model health monitoring`
- `refactor(web): reorganize admin workspace navigation`
- `feat(reconcile): add v0.27 observe audit materials`
- `docs(v0.27): record observe preparation status`
- `docs(release): include model health migration`
- `chore: integrate shared debugging skill`

正式发布前必须依据观测结论补齐日期、上一版本链接、兼容性说明、升级步骤、验证结果，
再按仓库发布流程更新 `CHANGELOG.md` 与 `README.md` 并打标签。
