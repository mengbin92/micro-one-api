# Micro-One-API v0.39.3 发布：计费毛利与未定价监控修复

> 2026-11-05 · 上一版：[v0.39.2](./release-v0.39.2.md)（2026-11-02）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.39.3)

v0.39.3 是 v0.39.2 之后的 **PATCH 修复版本**：修复配置毛利告警把全零窗口误报为负毛利、又把净负差额漏报的问题，并恢复后台未定价监控的采集链路（专用服务身份 RPC、失败保留上次计数、求值失败告警）。无新增公开 API、数据库迁移或运行配置项，不改动线上售价、采购估价与历史账本。

版本判 PATCH 而非 MINOR 的依据：只修改既有指标的落账位置、直方图分桶、告警表达式与内部 gRPC 统计入口，不新增对外端点、不新增表结构、不改变计费契约或账单金额。

**本次修复已于 2026-11-03 12:25 部署生产**（`deploy-20261103-122545-629d5158`，channel-service → admin-api → billing-service），本次发版补齐 release note / CHANGELOG 与 tag 工件；不重复重启生产服务。完整复现、根因与边界见 [计费监控修复记录](../runbooks/billing-monitoring-recovery-2026-11-03.md)。

## 修复内容

### 1. 配置毛利告警：全零误报与净负差额漏报

**根因**：旧规则取差额直方图的 P50 判断符号。差额集中在 -1 与 0 两个相邻桶时，P50 落在 0 附近显示 -50；而带负值样本的 `rate()` 在多数窗口为正，导致「净差额为负」这一真实状态被漏报。直方图分桶缺少 `-1` 边界，负值样本被并入 0 桶。

**修复**：

- 直方图增加整数负值边界 `le="-1"`，负差额不再混入零桶。
- 新增非负的 `gross_profit_positive_quota_total` / `gross_profit_negative_quota_total` 计数器，告警用「负请求占比」与两个计数器速率之差计算净差额，不依赖 P50 的符号，进程重启后仍可继续累计。
- 毛利仅在账本落账或事务提交成功后观测，一次成功请求观测一次；重试、拆账与回滚均不产生观测。

**影响服务**：`billing-service`；Prometheus 告警规则与 Grafana 计费面板。

### 2. 后台未定价监控：`PermissionDenied` 与计数缺失

**根因**：后台审计用人工列表 RPC `ListUnpricedRoutedModels` 采集，该入口要求 owner 权限，专用后台服务身份每次求值都被拒绝（线上每分钟一条），`micro_one_api_model_unpriced_routed_*` 指标长期缺失，监控处于盲区。

**修复**：

- 新增仅限专用 admin 服务身份的 gRPC 计数入口 `CountUnpricedRoutedModels`，只返回两种来源的计数；人工 `ListUnpricedRoutedModels` 继续由 owner 校验，权限边界不变。
- 读取价格失败、配置缺失或解析失败时保留上次计数并报告求值失败，成功时间不被失败刷新，避免把缺失指标当成 0。
- 新增告警同时覆盖求值失败、指标缺失与「后台停止刷新」三种情形；目标本身不可采集仍由既有采集告警负责。

**影响服务**：`channel-service`（新增统计入口）、`admin-api`（调用侧与 metric 发布）；Prometheus 新增 `unpriced-metric` 规则文件。

### 3. 明确不做改动

- `default` 无单独策略行时继承默认倍率与 `subscription_first` 是受支持状态，不补策略、不改写历史账本、不清洗既有日志。
- 线上售价与采购估价保持原值。当前价差属于运营定价决策，不由本次修复掩盖。

## 兼容性说明

- **API**：无新增公开端点。`CountUnpricedRoutedModels` 为内部 gRPC，需由服务身份令牌调用；人工接口权限语义不变。
- **数据库**：无新增迁移，无字段变更。
- **配置**：无强制新配置；Prometheus 需加载更新后的告警文件与新增规则文件，Grafana 计费面板需同步更新。
- **指标**：`le="-1"` 桶与两个新计数器从部署时刻开始累计，与既有 `_count` 的 15 分钟窗口长度不同，部署后约 15 分钟内「负请求占比」偏低，以窗口补齐后的数值为准。Prometheus 会把 `le` 规范化为 `-1.0`，告警正则同时覆盖 `-1` 与 `-1.0`。
- **部署顺序**：先 channel-service（提供统计入口），再 admin-api，最后 billing-service；滚动更新期间 15 分钟混合窗口不作为新口径验收。
- **回滚**：三个服务镜像按 `rollback-20261103-122545` 回滚；告警文件 `alerts.yml.bak.20261103-123149`、Grafana 面板 `billing.json.bak.20261103-123459` 已在生产保留。回滚后未定价监控恢复为缺失状态，毛利告警回到旧口径。

## 升级步骤

1. 获取 `v0.39.3`。已有环境无需数据库迁移。
2. 在开发机交叉构建 Linux/amd64 镜像，经 `scripts/deploy-update.sh` 按 channel-service → admin-api → billing-service 顺序上传并部署，保留旧镜像；不得在生产服务器构建。
3. 更新 Prometheus 告警文件并校验、重载；同步 Grafana 计费面板。至少观察两个后台求值周期：不再出现 `PermissionDenied`、`micro_one_api_model_unpriced_routed_evaluation_success` 为 1 且成功时间持续刷新、两种来源计数可读。
4. 用账本按请求合并后的差额核对新计数器；真实配置倒挂应继续告警，监控修复不隐藏负差额。
5. 售价或采购价如需调整，走既有配置发布入口并刷新缓存，不直接改写历史价格快照。2026-11-03 已完成生产更新的环境无需重复部署。

## 验证

- 旧告警规则已用回归材料复现：全零窗口误报、净负差额漏报；新规则 `gross-margin.test.yml` 通过，新监控规则 `unpriced-metric.test.yml` 先失败后通过（CI 执行 `promtool test rules`）。
- Go 回归：`go test ./app/billing/internal/biz -count=1`（落账、事务回滚、幂等观测）、`go test ./app/admin/internal/service ./app/channel/internal/server ./platform/metrics ./platform/security/serviceidentity -count=1`（后台统计、人工权限、配置读取失败）。
- `scripts/check-architecture.sh` 与 `python3 scripts/rbac-matrix-refresh.py --check` 通过。
- 生产观察（部署后 12 分钟）：admin-api 自 12:26 起 `unpriced routed metric evaluation failed` 告警日志计数由每分钟 1 条降为 **0**；`micro_one_api_model_unpriced_routed_evaluation_success{instance="admin-api:8000"} = 1` 且成功时间持续刷新；billing-service `gross_profit_negative_quota_total=5`、直方图 `le="-1"` 桶累计到 16，与重启后已结算请求全部为负差额一致；`UnpricedRoutedMetricUnavailable` 为 inactive；三服务无 error 级日志与 panic。
- 遗留真实信号：`NegativeGrossMargin` reload 后处于 firing/pending，原因是缓存读取售价 $0.006/百万 token 低于采购估价 $0.007/百万 token，属配置价差而非误报，调整需运营决策。
- 已知现场差异：生产 `alerts.yml` 的分组名与两条 `VendorEvidence*` 规则在本次改动前就与仓库不一致，本次只按补丁应用本任务改动，未整文件覆盖，也未恢复这两条规则；是否与仓库对齐另行确认。

## 完整变更日志（v0.39.2 → v0.39.3）

- `fix(billing): measure settled margin with non-negative profit and loss counters`
- `fix(admin,channel): give the background unpriced audit its own capability`
- `docs(billing-monitoring): record the production deployment and verification`
