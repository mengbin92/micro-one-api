# Micro-One-API v0.34.10 发布：支付回调校验、交付门禁与隔离长流验收

> 2026-10-08 · 上一版：[v0.34.9](./release-v0.34.9.md)（2026-10-07）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.10)

v0.34.10 是 v0.34.9 的 **PATCH 支付校验与交付验收版本**。修复有效签名支付宝回调漏核对本地订单金额的问题，完成购买到权益及本人用量闭环，并统一关键覆盖率、固定镜像部署与 executor 隔离长流验收。

**无 API/proto、数据库迁移或新增业务配置**。受影响的运行时服务仅 `billing-service`；前端与 executor 生产逻辑未变。executor 正式七天准入仍为 **INSUFFICIENT**，保持两枚 Token 有限灰度和 legacy。

## 修复与交付内容

### 1. 已验签支付通知必须匹配本地订单

**根因**：HTTP 回调没有用户会话，原金额核对却调用需要本人 IAM 会话的查单用例；查单授权失败被忽略，有效签名但金额错误的通知仍能进入发放。缺失 app_id/金额也可能跳过原核对。

**修复**：验签之后，Billing 用例直接从本地仓储读取订单，校验正金额、支付渠道和订单金额，再进入既有 paid/issued 事务；已配置 app_id 必须匹配。该回调路径不查询 provider、不要求用户会话；用户查单授权、事务和重复发放幂等语义保留。

**验证**：扩展实际 HTTP→RPC→IAM→SQLite/RSA2 集成回归，覆盖本人建单/owner 定价、下架重放、拒绝边界、发放后故障回滚/重试、重复回调、合同/权益唯一、本人与 API Key 用量，以及钱包幂等续期。外部支付宝沙箱一次续期由维护者手动支付，订单 paid/issued、有效期增加 30 天，单独留存脱敏证据；该往返发生在更新本版镜像之前，不等同本版线上故障注入。

**影响服务**：`billing-service`。

### 2. 本地门禁与固定镜像部署对齐

**根因**：本地 verify 缺少 CI 已执行的关键分支覆盖率；旧部署脚本使用硬编码目标和 latest 回滚、未切换有效固定镜像，部分失败仍可能继续。BSD 临时文件模板影响本机部署文档验证。

**修复**：verify 直接复用现有 critical 命令并保持阈值；部署读取环境目标，从实际运行 image ID 备份，携带源码摘要，固定所选服务 Compose 引用，显式禁止远端构建/拉取，逐项检查镜像 ID/健康。整批初始配置与单项恢复分开，多服务失败保留此前已完成的引用。通过解析后的 JSON 读取所选镜像，兼容 Compose 返回依赖镜像的行为。检查脚本使用随机目录中的 YAML，实际验证资源。

**验证**：全仓 verify exit 0，前端 322 测试、专项 140 测试；临时抬高覆盖率阈值正确 exit 1。通用桌面/移动 smoke 69 passed、1 既有 skip；本机单 worker 完整通过，双 worker 退出超时仍列为失败边界，Linux CI 配置不改。部署 stub 6 项/6 失败子场景通过，41 个 Kubernetes 资源 valid、83 项引用与本地文档链接通过。

**影响服务**：交付脚本和本地门禁；不需要额外重启其他服务或发布前端。

### 3. executor 长流与故障验收

**根因**：自然生产窗口缺少可比新旧 cohort，现有故障模式不能替代成对长流性能证据。

**交付**：复用真实服务私有 Compose/local upstream，对 Chat/Messages/Responses、256/1024 chunks、短/32KiB 上下文进行 12 档实验，固定 payload/model/source/usage/content、1ms 上游 cadence、串行并发，交替启动顺序。每档预热 3 对后计量 50 对；固定余额结算避免有限订阅额度途中产生正常拆账。

**验证**：600 对/1,200 个计量请求全部通过，最大 P95 回归 1.63%（门槛 20%），每次完整结束与单次 consume/dedupe 均核对。新旧路径 × 两种上游来源 × 六类故障，共 24 场景通过；流出后仅释放、不消费、不重试，后续请求恢复。低样本实际报告 INSUFFICIENT 并 exit 2。暂停读取后取消/500ms 总预算使用真实服务；另用既有 server 测试覆盖 writer failure、HTTP/2 与 slot/upstream 关闭，不宣称 TCP 缓冲耗尽负载验收。

**影响服务**：隔离测试与文档；不改变线上 executor 或扩大 allowlist。生产只读复核仍缺同入口 legacy 对照，不能把隔离 PASS 作为正式七天准入。

## 兼容性说明

- HTTP/RPC、proto、数据库结构、业务配置均兼容，无迁移步骤。
- 支付通知现在拒绝缺失/错误金额和已配置 app_id 不匹配；合法回调与重复回调保持原发放/幂等行为。
- 部署脚本要求有效部署坐标和标准 Compose service 块；备份/加载/引用/健康失败阻断。前端宿主机资源继续独立发布。
- 隔离支付业务链路与长流使用 SQLite；不把多库 migration smoke 当作三库支付整链验收。

## 升级步骤

1. 获取 v0.34.10，保留当前 .env、IAM 模式、专用服务凭证与订阅/灰度开关。
2. 在 Apple Silicon 本机交叉构建 linux/amd64，仅部署 Billing，服务器禁止构建：

   ```bash
   ./scripts/deploy-update.sh billing-service
   ```

3. 保存脚本输出的旧 image ID 回滚标签、Compose 备份和新 image ID；核对有效 Compose 固定引用、healthz、restart、源码摘要与对账结果。
4. 前端和其余服务本版无需更新；不新增支付或重复已结束的沙箱订单。回调负例/故障留在隔离环境，现网只读核对原订单及权益。
5. 回滚使用实际保存的旧镜像和 Compose；不回退 IAM、不变更授权或数据库。七天观察继续按 [正式手册](../design/v0.23-executor-observation.md)确认起点。

## 验证

- 最终 `make verify` exit 0：unit/race、架构、迁移、829 项 RBAC、十三项 IAM 结果契约、API 类型、前端 lint/unit/critical/build/bundle budget。
- `go test ./internal/integration ./app/billing/... -count=1`，真实支付隔离链路与相关 Billing 包通过。
- `python3 scripts/test-deploy-update.py` 与 `bash scripts/check-deployment-docs.sh` 通过。
- `--long-stream-pairs 50`：12 档、600 对 PASS；`--reliability-only`：24 场景 PASS；`--long-stream-pairs 1 --skip-build`：INSUFFICIENT/exit 2。
- 本轮完整命令、样本与边界见 [顺序实施记录](../runbooks/next-stage-acceptance-2026-10-08.md)；Billing 已部署为 `deploy-20261008-111825-404ccab9`，部署前后只读复核结果记录在该文件 N6 章节。
- Release 的 Linux E2E/九服务多架构镜像/GitHub Release 由 tag 流水线执行；以实际任务结果核对发布完成。

## 完整变更日志

- `fix(billing): validate verified notifications against local orders`
- `chore(quality): align coverage and immutable deployment gates`
- `test(relay): add paired long-stream and fault acceptance`
- `fix(deploy): select service image from resolved Compose configuration`
- `docs(ops): record ordered delivery and sandbox verification`
- `docs(release): v0.34.10`
