# Micro-One API v0.47.0 发布公告

> 2026-11-20 · 上一版：[v0.46.3](./release-v0.46.3.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.47.0)

v0.47.0 是 **MINOR 功能版本**。本版新增短期固定组 API Key 签发入口，并修复 MySQL 订阅窗口对账、上游 usage 留证、Relay 5xx 告警与长邮件投递。短期 Key 新增用户可用的签发方式，兼容已有签发接口和数据结构。

## 1. 签发短期固定组 API Key

**根因**。固定组签发原先默认使用不限额度，无法在同一次操作中设置有效期、有限 Token 配额和模型白名单。

**实现**。用户在「API 密钥 → 创建 Token」选择有资格的固定组，勾选「短期 Key」，设置到期时间、Token 配额和该组允许的模型。`POST /api/v1/routing-tokens/restricted` 沿用本人会话、组资格和二次验证。Identity 通过独立内部 RPC 在一次写入中保存全部限制，明文密钥在签发时显示。Token 配额在用量结算后扣减，耗尽后拒绝后续请求。 [签发与回退手册](../runbooks/routing-groups-runbook.md#56-签发短期固定组-key)

**影响服务**。Identity Service、Admin API、Web。

## 2. MySQL 冻结订阅扣费对账保留主键查找

**根因**。升级库的预扣与冻结费用 ID 可能使用不同的排序规则，导致对账报 MySQL 1267。统一强制一种规则会使部分新装库的冻结费用主键无法用于 JOIN。

**修复**。Billing 读取实际列元数据。排序规则相同时直接比较，混合规则下按冻结费用列的规则处理预扣 ID 一侧。成功读取的规则由进程复用。隔离 MySQL 8.4.11 的新装与混合布局中，冻结费用表均使用 `PRIMARY` 和 `eq_ref`。原有冻结费用、旧账本及扣费口径沿用。

**影响服务**。Billing Service。

## 3. 观察留证、告警和邮件修复

**根因**。指定上游的完整 Chat 用量可能包含重复计量字段，引发独立留证解析误报。旧 Relay 告警的 PromQL 分子和分母都保留 HTTP 状态标签，单条 5xx 序列会计算出 100% 错误率。合并告警的 JSON 邮件正文还可能超过 SMTP 单行长度限制。

**修复**。Relay 对适用的完整 JSON/SSE 观察记录校验并合并等价字段和缓存分桶，保留原始留证及原有财务结算规则。Prometheus 和 Grafana 按方法、路由和实例，以 5xx 数除以全部请求数。Notify 改用按 76 字符折行的 MIME Base64 正文。

**影响服务**。Relay Gateway、Notify Worker、Prometheus 规则与 Grafana 面板。

## 兼容性说明

- 此版无新增数据库迁移。普通 Key 和有序组 Key 沿用已有签发方式。短期签发要求固定组能力、专属 Admin 调用凭据与 Identity 用户会话。混版遇到旧端时受限签发返回错误。
- SQLite 和 PostgreSQL 沿用原有对账 SQL。若运维手工调整 MySQL 相关列的排序规则，重启 Billing 后刷新进程内元数据。
- 短期 Key 的 Token 配额适用于结算后的累计消耗。受控付费测试按请求大小、输出上限、源价格和重试次数额外核定金额预算。

## 升级步骤

1. 保留当前镜像标签、Compose 配置和 Web 静态产物回滚点。自建部署按现有流程在本机构建目标架构镜像。 [部署手册](../deployment.md)
2. 依次升级 Identity、Admin 和 Web，再用真实会话核对固定组签发、到期、额度及白名单。升级 Billing 后核对健康状态与自然启动对账。
3. 对仍运行 v0.46.3 及更早制品的 Relay、Notify 应用对应升级，并检查 Prometheus 规则与 Grafana 面板版本。
4. 如需回滚，恢复对应服务镜像和 Web 静态产物。保留已经签发的 Token 数据与历史账本。

## 验证

- [x] SQLite 上的 Admin → Identity → Relay 端到端验证通过，覆盖持久化、白名单拒绝和用尽额度后拒绝。Identity 专项验证本人及二次验证，桌面与移动浏览器均覆盖签发。 [签发验收](../runbooks/routing-groups-runbook.md#56-签发短期固定组-key)
- [x] 生产短期签发已部署。受控测试只有一次上游发送和一条账本，在闲时预扣、忙时完成，结算沿用冻结的闲时价。 [N1 实测](../runbooks/n1-pricing-closure-2026-11-14.md)
- [x] MySQL 8.4.11 新装与混合规则布局的完整聚合计划均走冻结费用主键。SQLite、MySQL、PostgreSQL 回归验证冻结与历史账本金额。 [对账验证](./release-v0.46.3.md#验证)
- [x] 本地 `make verify`、gosec 和凭据检查通过。OCR 复核了 Billing 查询变更，未发现 P0/P1。
- [x] 11 月 20 日 15:48 按提交 `2899bdf4` 交叉构建并仅更新生产 Billing，运行镜像摘要 `e3c3ed135477`，健康检查 200，重启 0。首轮自然对账 #1455 为 `completed`，9 个账户和两类既有差异与旧基线相符，新增 1267、元数据和窗口求和错误均为 0。生产三表 `EXPLAIN` 中冻结明细使用 `PRIMARY / eq_ref`，预计每次查找 1 行。已保留原镜像和 Compose 回滚点。

## 完整变更日志

- `fix(relay): reconcile channel 1 usage observation aliases`
- `fix(observability): calculate the relay 5xx error rate`
- `fix(notify): fold long alert email bodies`
- `fix(billing): align frozen charge reconciliation collation`
- `feat(routing): issue restricted fixed-group keys`
- `docs: reconcile production acceptance and release status`
- `fix(billing): preserve frozen charge index across MySQL collations`
- `docs(release): v0.47.0`
