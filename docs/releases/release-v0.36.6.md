# Micro-One-API v0.36.6 发布：账单证据监控修复、历史补证与开发依赖安全更新

> 2026-10-26 · 上一版：[v0.36.5](./release-v0.36.5.md)（2026-10-24）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.6)

v0.36.6 是 v0.36.5 之后的 **PATCH 修复版本**：修复上游供应商账单漏导、扫描停滞与复核后重复逾期提醒，保留历史记账关联补证，并更新前端开发测试依赖。公开 API、核验语义和客户结算政策保持兼容，无新增数据库迁移。

**生产上游供应商证据监控保持停用。** 维护者已确认功能完成验证、线上暂不启用；10 月 26 日停用验收确认 Billing 扫描关闭、两条对应规则卸载，其余告警保持。发布代码和制品不代表重新开启该功能，也不把历史 pending 改成费用核验成功。

## 修复与改进

### 1. 检测整期漏导、清单失效与扫描停滞

**根因**：原监控只查看已导入报告，无法发现整个账号账期未导入；指标缺失或检查循环停止也可能没有有效提醒，Alertmanager 接收端还缺少通知服务要求的认证。

**修复**：增加独立登记、严格校验且有有效期的账号账期清单，按完整 scope 精确匹配；对漏导逾期、清单非法或失效、指标消失和扫描超过期限分别检测。扫描有独立超时，恢复后清理通知失败状态。Alertmanager 使用既有 monitor caller 的凭据文件完成认证，秘密不进入仓库。

**影响服务**：`billing-service`、Prometheus、Alertmanager，以及三套 Compose 配置。实际启用前必须准备完整清单和私有凭据文件，不能从已导入报告反推全部采购范围。见[监控实施记录](../runbooks/vendor-report-monitoring-2026-10-25.md)。

### 2. 复核例外绑定不可变报告与 run

**根因**：维护者接受已知历史缺口后，缺少独立于财务核验状态的复核处置，导致同一报告/run 继续重复提醒。

**修复**：复用清单配置添加有期限的 `review_exceptions`，精确绑定 report/run、复核人、原因、依据及期限，并用完整 payload SHA 校验身份。保留原始 pending、overdue 和未知金额，另计 reviewed_overdue；新 run、到期、金额差异、漏导及其他 target 不受该例外影响，直接通知与 Prometheus 使用相同边界。

**影响服务**：`billing-service` 的监控、有限状态指标和供应商告警规则。历史复核不构成费用核验通过；当前生产已停用扫描及对应规则，不能随普通部署自动恢复。见[复核与停用验收](../runbooks/vendor-evidence-overdue-2026-10-26.md)。

### 3. 保留已核实的历史 legacy 记账关联

**根因**：原公开归档保留事后算术，未保留原请求与 reservation 关联；原临时私有材料无法定位，不能据摘要声称完成执行覆盖。

**修复**：通过一致性只读快照恢复 201 条消费、timing、reservation 和 ledger 关联，私有保存原件并逐条重放；使用既有 IAM owner 为原报告追加 sequence=2，独立核对成功审计和历史数据不变性。公开检查器保留关联、用量及金额篡改拒绝验证。

**影响服务**：Billing 的既有报告证据与审计材料，无运行时代码变更。生产为 2 reports / 3 runs，主结论仍为 `pending / missing_local / amount unknown / verification_scope none`；历史 owner execution、水位、完整采购覆盖和执行时原币成本仍未被证明。见[历史补证验收](../runbooks/vendor-bill-supplement-2026-10-25.md)。

### 4. 更新前端开发测试依赖

**根因**：开发测试工具链包含 mock 代码注入、Windows 路径遍历及间接依赖安全问题。

**修复**：Vitest 与 coverage-v8 同步升级到 5.0.3，将现有 Undici override 更新到 7.30.1，刷新兼容 Zod 锁定到 4.6.7；保留生产运行依赖。

**影响服务**：本地与 CI 前端测试工具链。纯开发依赖更新无需重新部署生产前端静态资源。

## 兼容性说明

- 无新增 API/proto、数据库迁移、用户权限或收费政策变化。
- 既有清单可不配置 `review_exceptions`；严格解析、完整 scope 匹配和清单有效期继续生效。
- 新 Alertmanager 配置需要私有 monitor caller 凭据文件；未准备文件的环境不能直接启用该接收配置。
- 生产 `BILLING_VENDOR_REPORT_MONITOR_V1=false`，对应 `VendorEvidenceMonitorUnavailable`、`VendorEvidenceNeedsReview` 两条规则保持卸载；其他支付、结算及可用性告警保持原状。
- 旧账期补证和复核例外不恢复历史执行事件、不把未知成本当零、不变更钱包或客户账本。
- 契约 1% 灰度、executor 七天准入、Jaeger/WS 生产验收保持各自边界，不因发布自动通过。

## 升级步骤

1. 核对当前镜像与源码摘要，备份受影响的 Compose、环境、告警规则和运行镜像；私有材料保存在受限目录，不进入 Git。无新增迁移需要执行。
2. 对尚未部署本轮 Billing 代码的环境，在本机或 CI 构建 Linux/amd64 镜像后上传，仅更新 `billing-service`；生产主机不构建镜像。运行相同源码摘要的实例无需重复重建。
3. 按环境实际启用决定维护监控配置。当前生产继续关闭扫描并保留两条规则卸载状态，不用仓库完整规则覆盖现有加载清单。未来启用须先核实并更新采购清单、例外期限和私有认证文件。
4. 核对 Billing 健康、镜像来源、抓取状态及其他告警。历史报告/run 与客户账本保留，不通过删除报告、补造 execution 或修改金额消警。
5. 前端开发环境使用 Node.js 24 和 `npm ci` 更新锁定依赖；本轮没有生产前端运行逻辑变化，无需为开发依赖重发 `web/dist`。
6. 回滚时只恢复受影响的镜像与配置；恢复历史配置可能重新开启监控，应先核对清单和例外有效期，继续遵守当前停用决定。

## 验证

- 整合后的业务源码在 Go 1.27.2 下通过 `go build ./...` 和完整 `make verify`：单元、race、架构、迁移、IAM/RBAC、生成类型、前端 lint、599 项测试、191 项关键覆盖及构建和六项资源预算。关键覆盖与全量测试有重叠。
- 五套 Prometheus 规则测试通过，包含缺报、清单失效、扫描停滞、复核例外、新 run、到期及恢复场景。
- 使用官方 npm registry 对当前锁定依赖执行 audit，漏洞总数为 0；本机默认镜像不支持 audit，因此该次验证显式指定官方 registry，未修改仓库或全局 registry 配置。
- 真实 HTTP/gRPC `internal/integration` 回归、326 份 Markdown 本地链接、提交正文和 `git diff --check` 均通过；本次静态复审未发现阻塞 PATCH 发布的缺陷。
- 既有生产验收已证明 Billing 本轮二进制上线、历史数据不变及后续停用；10 月 26 日 09:39:42 的停用证据记录健康正常、重启 0、OOM false、其余 51 条规则健康、其他 20 个容器保持。该记录不代替未来时点的实时核对。
- 本版 tag 对应的 CI、Release E2E、多架构镜像、manifest 与 GitHub Release 由发布流水线执行；最终结果以本版 workflow 为准，不复用 v0.36.5 的结果。

## 完整变更日志

- `fix(billing): detect missing vendor reports and stalled monitoring`
- `docs(billing): preserve verified legacy bill evidence`
- `fix(billing): bound vendor evidence review exceptions`
- `fix(deps): patch frontend development dependency advisories`（同一修复经 PR #27 合入 main）
- `docs(release): v0.36.6`
