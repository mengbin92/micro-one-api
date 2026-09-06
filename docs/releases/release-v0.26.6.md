# Micro-One-API v0.26.6 发布：Canonical Charge 精确门禁、Responses 错误语义与部署安全修复

> 2026-09-06 · 上一版：[v0.26.5](./release-v0.26.5.md)（2026-09-02）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.26.6)

v0.26.6 是 v0.26.5 之后的 **PATCH 计费与生产可靠性修复版本**，包含 11 个提交
（4 fix + 4 build/deps + 2 docs + 1 merge）：为 canonical charge 增加按订阅来源与上游模型
精确匹配、默认 fail-close 的白名单；修复 Responses 上游 4xx 被笼统改写为 502；避免
`admin-api` 在 MySQL 尚未就绪时启动后永久关闭部分管理能力；并升级 Go/Web 安全依赖。

**无公共 API / proto 变更、无新增数据库迁移**。新增可选配置
`BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST`；缺失、为空或非法时安全回退 Observe。
受影响的运行时服务主要为 `billing-service`、`relay-gateway`，Compose 部署配置同时影响
`admin-api` 的启动顺序。

## 1. Canonical Charge 按来源精确放行并默认关闭

**根因**：原有 canonical usage 只有全局 `legacy | observe | charge` 模式。有限来源灰度时，
若白名单未配置或解析失败，charge 可能扩展到全部流量；reservation 提交路径还可能缺少
用于匹配的来源字段。对账脚本同时存在两个边界误报：billing 毫秒时间与 log 整秒时间使用
不同边界，以及订阅/钱包双轨 ledger 被当成重复记录。

**修复**：

- 新增 `BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST`，以
  `<subscription_account_id>:<upstream_model_id>` 精确匹配订阅来源；未命中、channel 来源、
  空值或非法配置全部保持 Observe，全量 charge 必须显式配置 `*`。
- reservation commit 可从 reservation 快照回填来源，避免调用侧缺字段导致白名单误判。
- 新增固定 72 小时 charge 验收 SQL；billing/log 多重集只比较固定窗口内部共同整秒区间，
  并按 reservation reference 折叠审计字段一致的双轨 ledger。
- 补充 allowlist 解析、fail-close、来源匹配、reservation 回填、成本选择和 Compose 配置测试。

**影响服务**：`billing-service`；新增 Compose 环境变量和只读对账脚本。数据库 schema、历史
ledger 与余额不做原地修改。

## 2. Responses 上游 4xx 保留可操作语义

**根因**：Responses 上游请求返回确定性 4xx 时，默认错误映射把未知状态统一改写为 502，
既掩盖请求过大、媒体类型或参数不兼容等客户端可修复问题，也使服务端缺少准确、脱敏的
原始上游状态证据。

**修复**：

- 明确映射 413、415、422，并仅在转换契约成立时允许 415/422 进入 Responses→Chat fallback；
  413 不重试。
- 上游 401/403 继续隔离在网关错误后，避免泄露或混淆上游凭据与客户端 Token 鉴权。
- 普通与流式路径增加有界、脱敏的错误记录和回归矩阵，覆盖 fallback 成功/失败、reservation
  释放以及最终 HTTP 状态。

**影响服务**：`relay-gateway`。正常成功响应、模型选择、Token 统计和计费算法不变。

## 3. Admin 启动等待数据库真正就绪

**根因**：`admin-api` 的系统选项和订阅 repository 只在启动时初始化。Compose 仅等待 MySQL
容器启动而非健康，栈重建时若 admin 抢先连接失败，这些能力会在该进程生命周期内永久关闭，
例如 `/api/v1/admin/subscriptions` 持续返回 501。

**修复**：Compose 的 MySQL、PostgreSQL、Lite 与 E2E 形态统一要求数据库 readiness/migrate
门禁完成后再启动 admin，同时保留下游 gRPC 服务的既有启动顺序；部署检查脚本覆盖各形态。

**影响服务**：`admin-api` 的 Compose 启动编排；没有 admin 二进制或公共接口变更。

## 4. 安全与兼容依赖升级

**根因**：前端锁文件中的 `browserslist`、`postcss-selector-parser` 等传递依赖命中拒绝服务
安全告警；Go 与 Web 基础依赖也有兼容更新待合入。

**修复**：

- `google.golang.org/grpc` 由 1.82.1 升级到 1.83.1；
- `golang.org/x/crypto` 由 0.55.0 升级到 0.56.0，并同步整理模块依赖；
- 升级受影响的前端传递依赖、`fast-uri` 3.1.7 与 `qs` 6.16.0。

**影响服务**：发布制品中的 Go 服务和 `web/dist` 依赖基线；无用户可见功能变化。

## 5. Canonical Observe 与有限 Charge 记录

48 小时 canonical observe 固定窗口于 2026-09-06 09:49:52.108 CST 满窗，1718 条 consume
的契约、来源、pricing snapshot、幂等、成本算术、billing/log 多重集和固定 Prometheus
门禁均通过。随后仅对一个 K3 订阅账号与 upstream model 组合开启有限 charge。

修订后的 billing 镜像已于 2026-09-06 10:40:07 CST 部署，首条合格样本将固定 72 小时窗口
冻结为 2026-09-06 10:41:50.397 至 2026-09-09 10:41:50.397 CST。生产当前已运行与
v0.26.6 等价的 Relay/billing 修复；**发布 tag 和制品不触发生产容器重建或重启**，避免打断
该窗口。任何门禁失败均切回 Observe，不依赖 schema rollback。

## 兼容性说明

- **API / proto**：无新增或破坏性公共 API、HTTP 路由或 proto 变更。
- **数据库**：无新增迁移；`089` 仍是最新迁移。
- **配置**：新增 `BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST`。安全默认值为空，此时即使
  `BILLING_CANONICAL_USAGE_MODE=charge` 也不会放行任何来源；全量必须显式使用 `*`。
- **运行时**：Responses 的确定性上游 4xx 会返回更准确的客户端状态；billing 只改变
  canonical/legacy 成本选择门禁，不修改 usage bucket 或价格快照。
- **部署**：未运行修复的环境应更新 `billing-service`、`relay-gateway` 和 Compose 配置；
  `admin-api` 仅需在采用新 Compose readiness 编排时重建。
- **回滚**：billing 可将 mode 切回 `observe` 后重建服务；Relay 可回滚镜像。均无 schema
  down migration。

## 升级步骤

```bash
git fetch --tags
git checkout v0.26.6
```

1. 从 v0.26.5 升级无需执行新迁移；从更早版本升级时先按顺序应用至 `089`。
2. 保持 `BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST` 为空即为安全 Observe；有限 charge 必须
   由变更审批提供精确 `<subscription_account_id>:<upstream_model_id>`。
3. 在本地或 CI 交叉构建 `linux/amd64` 镜像，不在资源受限的生产服务器构建。
4. 未运行本修复的环境按 `billing-service` → `relay-gateway` 顺序更新，并同步 Compose；
   若只需修复 admin 启动竞态，使用新 Compose 强制重建 `admin-api`。
5. 当前生产已经运行修复等价镜像，只生成 v0.26.6 tag、GitHub Release 和多架构镜像，
   不为切换 tag 重启正在执行 72 小时 charge 验收的容器。

## 验证

- `develop@b6975fa` 的 CI 与 Security Pipeline 均通过；
- `make verify`：Go unit/race、architecture、migration-check、Web lint/test/build；
- Responses 普通/流式 4xx 与 fallback 状态矩阵通过；
- billing allowlist fail-close、精确来源匹配、reservation 回填与成本选择测试通过；
- Compose 配置及 E2E override 的数据库 readiness 检查通过；
- 48 小时 Observe 固定 SQL/PromQL 门禁 PASS；有限 K3 charge 继续按固定 72 小时窗口观察。

## 完整变更日志

- build(deps): bump google.golang.org/grpc (#16)
- fix(deps): update vulnerable frontend dependencies
- docs: record executor and canonical usage observations
- fix(relay): preserve Responses upstream 4xx semantics
- docs(reconcile): reset canonical observe window and record zero-usage legacy baseline
- build(deps): bump fast-uri (#17)
- build(deps): bump qs (#18)
- build(deps): bump golang.org/x/crypto
- fix(deploy): gate admin startup on database readiness
- fix(billing): gate canonical usage charge by source
