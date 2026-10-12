# Micro-One-API v0.34.17 发布：退款对账恢复与 IAM 入口修复

> 2026-10-12 · 上一版：[v0.34.16](./release-v0.34.16.md)（2026-10-11）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.17)

v0.34.17 是 v0.34.16 的 **PATCH 修复版本**：恢复升级后 MySQL 混合排序规则环境中的退款对账，修复生产只读取证查询超时，并纳入 IAM 根入口、尾斜杠导航与桌面/手机布局修复。当前阶段文档同步到实际发布、生产核对与 executor 验收状态，历史记录继续保留。

**无 API/proto 破坏性变更，无新增迁移或账务数据修复**。运行时影响 `billing-service` 和宿主机前端；其他变更为只读取证工具、测试与文档。保持既有充值倍率和密钥。当前生产前端已与本版构建全部 77 文件一致，可保留原资源。

## 修复内容

### 1. MySQL 退款对账恢复

**根因**：升级后的 payment_orders 与 billing_ledgers 保留不同 utf8mb4 排序规则，退款联查报 Error 1267，21 轮自然小时对账为 partial；差异数为零不能证明全部检查成功。

**修复**：在 MySQL 下统一退款查询的订单/用户标识表达式排序规则，让分组与输出一致；继续用一个 SQL 语句匹配同订单同用户，保留重复与孤立账本发现、其他方言和错误处理。

**影响服务**：`billing-service`。只更新二进制，不改生产表排序规则或账务事实；历史 partial 不改写。

### 2. executor 只读取证超时修复

**根因**：日志 level 索引选择性低，逐账本重复扫描消费日志，超过既有 15 秒 SQL 上限。

**修复**：在该联查提示日志侧不走索引，MySQL 使用一次 hash join。同样关联断言在生产只读复核中 0.346 秒完成；固定窗口、完整关联及金额/归因检查和原时限保持。同时从实际 image ID 的元数据读取源码摘要，单列容器标签，避免旧 Compose 标签掩盖当前运行代码。

**影响服务**：无运行时服务更新；`scripts/executor-observation-readonly.py`。不新增生产索引，不产生业务请求。

### 3. IAM 根入口与布局修复

**根因**：`/admin/iam` 未注册，子页尾斜杠会回落到 roles；权限矩阵与长角色名在小屏布局中挤出控件。

**修复**：注册 IAM 入口，按权限跳转首个可访问页面，规范化子页路径并保留控制台准入检查；复用现有输入、表格、卡片、加载与弹窗样式，矩阵保持单一可见滚动区，长名称换行。

**影响服务**：独立前端。桌面/手机布局与真实路由守卫回归已执行；API 授权契约保持。

## 兼容性说明

- 无 HTTP/RPC、proto、数据库迁移或持久化形状变化；既有 MySQL、SQLite、PostgreSQL 均保留。
- 不调整 `RECHARGE_AMOUNT_MULTIPLIER`、JWT/Token/AES 密钥或 Redis 配置。
- 前端仍由 `/opt/web/dist` 独立挂载；镜像更新不能替代前端文件摘要核对。
- executor 正式准入仍需可比 legacy 对照和连续七天窗口；隔离验收不能替代生产起点，不扩大两枚 Token 灰度或移除 legacy。

## 升级步骤

1. 备份当前 Billing 实际运行镜像与 Compose，核对数据库及既有环境配置。
2. 本机交叉构建 linux/amd64 并运输到生产，仅更新 `billing-service`；禁止在生产构建/拉取，使用固定镜像引用和 `--no-deps --no-build --pull never`。
3. 核对镜像 ID、源码摘要、health、重启次数与启动对账；随后核对自然小时对账为 completed，保留旧 partial 历史。
4. 前端构建后逐文件核对生产目录；已与本版一致时不重复发布，否则独立备份并发布 dist。
5. 回滚只恢复 Billing 的保存镜像/Compose；回退会重新暴露混合排序规则对账缺陷。前端回滚另用宿主机备份。

## 验证

- 修改前独立 MySQL 精确复现 Error 1267；修改后三库退款配对、跨用户、重复及孤立账本回归与 race 全部 PASS，无跳过。
- `make verify` 与 `make test-integration` PASS，包含架构、迁移、RBAC、IAM 结果契约与前端门禁。
- 前端 441 项 unit、186 项 critical、完整 lint/build 与六项产物预算 PASS；桌面/手机通用 smoke 79 passed、1 既有 skipped，生产 77 文件摘要一致。
- 本机 cross-build amd64 后仅更新 Billing，启动对账 #493 completed/0 差异；九服务健康、环境及前端保持。旧 Compose 源码标签单列，实际镜像摘要核对通过。
- 当前代码 600 对长流与 24 项故障矩阵 PASS，最大 P95 回归 +0.59%；最终 N5 起点复核 INSUFFICIENT，正式七天未启动。完整结果及边界已归档于 [本轮实施记录](../runbooks/next-stage-acceptance-2026-10-12.md)。本轮无真实支付、退款或邮件发送，无生产付费对照请求。

## 完整变更日志

- `fix(web): restore IAM entry and align admin page styles`
- `fix(billing): normalize refund reconciliation collations`
- `fix(ops): read immutable image metadata in executor evidence`
- `docs: reconcile next-stage acceptance and production state`
- `docs(release): v0.34.17`
- `merge: integrate next-stage acceptance and v0.34.17`
- `merge: release v0.34.17 from develop`
