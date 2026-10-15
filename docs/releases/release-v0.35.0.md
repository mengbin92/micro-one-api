# Micro-One-API v0.35.0 发布：持久化计费证据、供应商对账与受控上游修复

> 2026-10-15 · 上一版：[v0.34.18](./release-v0.34.18.md)（2026-10-13）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.35.0)

v0.35.0 是 v0.34.18 之后的 **MINOR 功能版本**：汇总 21 个提交（17 个非合并提交），交付 P0–P3 原生用量证据、持久送达、供应商费用核验、有限上游契约和隔离修复运营链，并纳入 10 月 15 日复审修复及前端／安全门禁修正。发布文档提交不计入上述数量。

公开接口保持兼容，新增内部 RPC、管理端点及前端页面；新增 9 组数据库迁移，均有 MySQL/Postgres/SQLite 文件。主要影响 `identity-service`、`billing-service`、`channel-service`、`admin-api`、`relay-gateway`、`monitor-worker`、宿主机前端及可选 operator 工具。收费范围、用户价格和历史账本不因升级自动改变。

## 功能与修复

### 1. 原生用量证据与持久化结算

**根因**：协议转换可能丢失供应商原生字段，零值与未知混淆；进程崩溃或异步重放可能丢证据、替换结算输入，或提前释放预留。

**修复**：在转换前保留 JSON/SSE/WS 原生证据和信任状态；reserve 时持久化 execution intent，以 SQLite/WAL outbox 送达 checkpoint/final，Billing 固定 digest、财务输入、租约和截止时间。合法零值保持为零，缺失或歧义不推算成已核验金额。收费采用独立精确 source/model 白名单与冻结政策。

**影响服务**：`billing-service`、`relay-gateway`、`admin-api` 及前端。见 [P1 工程记录](../runbooks/billing-upstream-p1-completion-2026-10-13.md)。

### 2. 库存、受控探测与供应商费用对账

**根因**：库存发现、能力验证和推理恢复混用，探测缺少持久预算；平台用量不能替代独立供应商账单，未知成本和更正容易被误报为零。

**修复**：拆分库存与 canary，使用 source 预算、单次 permit 和持久事故限制；补齐 DeepSeek provider、模型库存与目录映射，以及空响应体和过时渠道操作校验。供应商 CSV/API 按固定账号、账期、原币 decimal 和证据摘要核验，报告／更正只追加，采购按 attempt 去重，unknown 保持可见。

**影响服务**：`billing-service`、`channel-service`、`admin-api`、`relay-gateway`、`monitor-worker`。见 [P2 工程记录](../runbooks/billing-upstream-p2-completion-2026-10-14.md)。

### 3. 有限上游契约与隔离修复链

**根因**：适配规则和自动修复缺少独立的回放、授权、版本与生产发布边界，失败候选可能影响正在服务的版本。

**修复**：增加有限规则校验、CAS 发布、事故 fence、独立 fixture 回放和签名 reviewer；候选遵循 canary、gray、promote、rollback 门禁。新增隔离 cost/repair/weekly workers，持久 claim 与每日调用预算，失败／不确定任务不自动重发；生成器不能自签或直接发布，Go patch 仍需代码审查和 CI。

**影响服务**：`channel-service`、`billing-service`、`relay-gateway`、`monitor-worker` 及可选工具。见 [P3 控制链](../runbooks/billing-upstream-p3-2026-10-14.md)、[运营工程](../runbooks/billing-upstream-p3-operations-completion-2026-10-14.md)。

### 4. 供应商运营入口与权限

**根因**：费用证据缺少 owner 视图、固定周期复盘和明确的未知状态展示，角色变化可能保留旧数据。

**修复**：新增 `/admin/vendor-reports`、周复盘与导出，保留原币精度和 unknown，按 `billing.vendor_report.read` 准入并在权限变化时清理旧数据；新增有限指标、告警和 Grafana `supplier-evidence` 看板。迁移 134 增加权限目录，不自动绑定角色。修正页面 Fast Refresh 和 render purity 问题，恢复全量 lint。

**影响服务**：`identity-service`、`billing-service`、`admin-api`、前端及监控配置。

### 5. 复审中的计费、证据与灰度边界修复

**根因**：不完整响应、空 canary 和流式提前终止可能被误认作成功；outbox 未为完成事件保留足够容量；重放、灰度恢复和对账结果排序存在竞争与时序缺口。

**修复**：验证实际输出及流终结，保留原生异常／歧义；持久 intent 限一次 HTTP 发送，拒绝同 intent 的协议 fallback 和 redirect。outbox 预留 header/terminal/final 容量，跨 ACK 和重启恢复，迟到 ACK 从收到时起保留七日；已结算结果在证据截止后仍可幂等读取。对账 run 用锁内单调序号排序（迁移 135），未知原值不制造更正差额；修复授权受签名期限约束，恢复／回滚重新开始灰度窗口，失败候选只影响自身。运营脚本对 partial、隐藏差异和无效结果拒绝 PASS。

**影响服务**：`billing-service`、`channel-service`、`relay-gateway`、`monitor-worker` 及工具。逐项回归见 [10 月 15 日复审](../runbooks/billing-upstream-review-2026-10-15.md)。

### 6. Go 1.27 安全扫描与历史误报修复

**根因**：固定版 gosec 的依赖无法读取 Go 1.27 export data；全历史 Gitleaks 扫描将四处 P2 SHA256 指纹／文件摘要误识别为凭据。

**修复**：在隔离工具模块中构建兼容的固定版扫描器，复用到 CI、Make 与 pre-push；仅豁免四个已核对的 commit/file/rule/line 指纹，未来改动继续扫描。

**影响服务**：开发与 CI 门禁，应用依赖和运行行为不变。

## 兼容性说明

- 内部消费者需先升级，再升级调用方；保留具名服务身份及接收方映射。`channel-service` 继续保持单实例，尚不能据此扩大副本数。
- 迁移归属：Billing `126/131/132/135`；Channel `127/130/133`；Monitor `128`；Identity `134`。编号 129 不属于本次新增迁移。按 `migrations/ownership.yaml` 执行，运行身份不授予 DDL 或全库权限。
- relay outbox 必须挂载持久目录，保留 SQLite/WAL 和权限；schema 启动自动升级，新请求可能更早背压，禁止删除卷来消除容量告警。
- 默认关闭的 delivery/receipt、契约、费用监控和 operator profiles 需按各自手册受控启用；保留现网已批准值。空 `RELAY_NATIVE_USAGE_V2_ALLOWLIST` 不放行新收费。迁移及安装权限目录不代表生产授权或自动启用。
- 既有 P0–P3 部署记录覆盖 10 月 13–14 日工程制品；**10 月 15 日复审修复、迁移 135 和后续 lint／扫描修正尚无生产部署记录**，不能以先前部署代替本版更新。
- 完整账期 CSV/API、生产 reviewer/IAM、30 分钟／100 完整样本、五分钟实例收敛、真实通知及两周现场基线仍需独立验收。单机 outbox 不保证整机损毁后的证据恢复。

## 升级步骤

1. 备份所属数据库、Compose、实际镜像、前端和 outbox；记录现有收费白名单、预算与开关，停止新的受控试验准入，保留在途证据消费。
2. 获取 `v0.35.0`，按所属库、实际方言与受控迁移身份应用待执行迁移，并检查状态。**必须先应用 Billing 135，再启动本版 Billing**。生产主机不编译，迁移 CLI 和服务镜像均在本地交叉构建 linux/amd64 后运输；执行方式见 [部署文档](../deployment.md)。
3. 按消费者在先顺序更新 Identity → Billing → Channel → Admin → Relay → Monitor；沿用标准脚本的本地跨构建和运输流程：

   ```bash
   git fetch --tags
   git checkout v0.35.0
   ./scripts/deploy-update.sh identity-service billing-service channel-service admin-api relay-gateway monitor-worker
   ```

4. 在 `web` 执行 `npm ci && npm run build`，独立发布宿主机 `/opt/web/dist`；保留旧哈希静态资源，最后原子替换 index。重建 admin-api 镜像不会更新前端。
5. 需要工具时，单独使用 `deployments/docker/Dockerfile.upstream-evidence-tools` 本地构建并运输；正式 Release 的九服务镜像矩阵不包含该工具。可选 `docker-compose.p3-operators.yml` 保持 disabled，真实凭据、reviewer 与 IAM 在独立批准后配置。
6. 检查迁移状态、镜像 revision、健康与重启次数、outbox ACK/pending、报告权限、前端摘要和 unknown 展示。付费推理／收费灰度仍使用独立限定范围和预算验收。
7. 回滚先停止新准入，保留能够消费在途证据的新消费者；保留新表、账本、outbox 与证据。禁止通过删卷、回退账本或扩大收费名单处理异常。

## 验证

- 复审归档：119 个单元测试包、25 个 race 包、5 个服务内集成包，以及架构、863 行 IAM 入口矩阵、迁移治理、格式、Markdown 链接和 7 个运营脚本回归通过。详见 [验证索引](../runbooks/evidence/billing-upstream-review-2026-10-15/index.json)。
- 迁移 135 的 SQLite 新装／升级／backfill／重复应用已有实测；复审时 MySQL/Postgres 因无 scratch DSN 跳过。后续 `30020772` 的 CI 已通过 MySQL/Postgres 全迁移新装／重复应用冒烟；这不替代 135 在两个方言上的历史数据 backfill 专项验证。
- 前端 lint 修复提交 `30020772` 的 [CI](https://github.com/mengbin92/micro-one-api/actions/runs/38666925497) 与 [Security Pipeline](https://github.com/mengbin92/micro-one-api/actions/runs/38666925498) 已通过；该提交记录全量前端 456 项测试通过。
- 本次发版在本地重验 Markdown 链接、提交正文、迁移治理、863 行 RBAC 契约、135 SQLite backfill／重复应用和 gosec，全部通过；Gitleaks 全历史扫描 1,114 个提交，无发现项。业务代码测试复用上述已通过的 CI 与归档证据。
- 正式发布由 tag 触发 `release.yml`，先通过共享 E2E 门禁，再构建九服务 linux/amd64 与 linux/arm64 镜像，最后创建 GitHub Release。标签推送本身不等于制品已发布；以流水线完成结果为准。
- 本次发版不执行生产部署、真实供应商调用或新的收费授权。

## 完整变更日志

以下列出 v0.34.18 后的全部非合并提交，按提交先后排序：

- `feat(relay): add P0 native usage shadow evidence`
- `docs: record P0 deployment and live shadow verification`
- `feat(billing): guard durable settlement intake and expiry`
- `feat(billing): complete P1 evidence delivery and controlled upstream probes`
- `fix(billing): align execution reservation collation and record P1 deployment`
- `feat: complete P2 billing evidence and upstream contracts`
- `docs: refresh normalized evidence checksums`
- `docs: record P2 production deployment and acceptance`
- `fix: make security scanning compatible with Go 1.27`
- `docs: verify deployed inventory and security gate`
- `feat: deliver P3 cost evidence and bounded upstream repair`
- `docs: record P3 production deployment and verification`
- `feat: complete gated P3 evidence operations`
- `docs: record gated P3 operations production deployment`
- `fix: close billing evidence and upstream contract review gaps`
- `fix(web): resolve vendor reports lint failures`
- `fix(security): ignore historical P2 SHA256 false positives`

完整提交比较：[v0.34.18...v0.35.0](https://github.com/mengbin92/micro-one-api/compare/v0.34.18...v0.35.0)。
