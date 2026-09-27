# Micro-One-API v0.33.0 发布：Alertmanager 邮件告警与支付回调验收

> 2026-09-27 · 上一版：[v0.32.2](./release-v0.32.2.md)（2026-09-26）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.0)

v0.33.0 是 v0.32.2 之后的 **MINOR 通知能力与验收版本**：Alertmanager 告警组
可以使用已有邮件发送端，支付重复回调保持首次入库的平台交易号，并补齐支付
故障、Redis 隔离故障和 Linux/amd64 性能对照的验收记录。

**无新增公共 HTTP API、无数据库迁移**。notify-worker 的内部配置 proto 增加
可选收件人字段；其他通知类型、未配置告警类型时的默认 `webhook` 行为不变。

## 功能与修复

### 1. Alertmanager 告警组支持邮件通知（O2）

**根因**：邮件 sender 已存在，但告警类型校验拒绝 `email`，告警处理器又以空
收件人创建通知，因而无法将 Alertmanager 告警组送往邮件队列。

**修复**：增加 `ALERTMANAGER_EMAIL_RECIPIENT`，仅在告警类型为 `email` 时
填入通知收件人；启动时校验单个合法收件地址及 SMTP 主机、发件地址，避免
不可发送的告警入队。三套 Compose 模板透传收件人配置；本地假 SMTP 测试覆盖
firing/resolved 从 webhook 到持久通知再到投递成功的路径。

**影响服务**：`notify-worker` 与其 Compose 配置。生产 QQ SMTP 受控测试中，
直接投递的 firing/resolved 和经 Alertmanager API 注入的 firing 均记录为
`sent`，见[脱敏证据](../runbooks/evidence/o2-qqmail-production-2026-09-26.json)。
这证明 SMTP 接受和通知链路，尚未验证真实 Prometheus 规则触发或收件箱呈现。

### 2. 支付重复回调保留持久化交易号（F17）

**根因**：已支付订单的幂等重放在状态短路前，将本次回调携带的平台交易号写入
返回的内存对象。数据库行未修改、不会重复发放资产，但调用方可能收到与首次
支付记录不一致的平台交易号。

**修复**：将交易号赋值移至已支付短路之后，并用 repo 回归测试固定返回值。
补充业务层、HTTP 回调层及 SQLite 事务层的故障注入，验证丢回调查单收敛、
暂时查单失败重试、重复签名回调幂等和发放失败回滚恢复。

**影响服务**：`billing-service`。正常支付宝沙箱支付往返和隔离故障注入分别
记录在[沙箱证据](../runbooks/evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)
与[F17 验收记录](../runbooks/f17-fault-acceptance-2026-09-26.md)。支付宝平台的
实际重发节奏和手工补单失败后的自动修复仍不在本次验收范围。

### 3. 性能门禁与 Redis 故障测量（Q3/O5）

**根因**：旧 Linux/amd64 性能基线跨越 436 个提交，无法作为本版单次回归的
判断依据；基线 MySQL socket 健康探测也曾使 CI 提前启动迁移。Redis 隔离
故障的第一版延迟采样复用了旧 Prometheus 抓取并重叠时间窗口。

**修复**：Q3 CI 在同一 runner 上各跑三轮全量 k6，并将后续比较基线重定为
v0.32.2；TCP 健康探测和失败日志使作业可复跑。O5 隔离测试补充 legacy/V2
故障期间的真实 chat 流量、结算断言与抓取边界修正。

**影响服务**：CI、测试及运维证据；无新增生产服务行为。Q3 重定后的确认作业
七项门禁通过，历史跨版本 P95 增幅作为累计功能成本记录，见
[Q3 记录](../runbooks/q3-rebaseline-2026-09-26.md)。O5 原始故障测试中的 chat
可用性已验证；RPC 样本量和 P95 因采样缺陷需按修正后的测试重跑，不能把旧值
当作恢复延迟结论，见[O5 记录](../runbooks/o5-redis-fault-isolation-2026-09-26.md)。

## 兼容性说明

- 无新增公共 HTTP API 或数据库迁移；邮件收件人仅在
  `ALERTMANAGER_NOTIFY_TYPE=email` 时必填。该模式还需配置
  `NOTIFY_SMTP_HOST`、`NOTIFY_SMTP_FROM` 及实际 SMTP 凭证。
- 其他 Alertmanager sender 的目的地址仍由各自 sender 配置提供；未选择
  `email` 的既有部署不需要新增收件人变量。
- 支付订单的持久化状态与发放逻辑不变；修复只使重复回调的返回对象与已存记录
  一致。

## 升级步骤

1. 按现有流程备份 notify-worker 与 billing-service 的部署配置和镜像。生产
   notify-worker 已先行运行邮件配置并完成受控投递；升级时核对当前镜像、
   Compose 变量和健康状态，避免把已生效的独立部署配置覆盖掉。
2. 若选择邮件告警，设置 `ALERTMANAGER_NOTIFY_TYPE=email`、
   `ALERTMANAGER_EMAIL_RECIPIENT`、SMTP 主机、发件地址和凭证；确认 Compose
   将收件人变量传入 notify-worker，再更新或重建该容器。
3. 更新 billing-service 以获得支付重放返回值修复。两项服务如需新镜像，按
   [部署流程](../../AGENTS.md#build--deploy-a-single-service-cross-platform)在
   本机交叉构建 linux/amd64 后传往生产主机，不在服务器构建。
4. 核对服务健康、邮件通知 `sent` 状态和支付回调日志；真实规则触发后的
   firing/resolved、收件箱呈现及 O5 修正后 RPC 延迟复测仍按
   [下一阶段待办](../design/next-stage-todo-2026-09-26.md)跟踪。

## 验证

- `make verify` 通过：Go 单测与关键包 race、分层、迁移检查、生成 API 类型、
  前端 lint、196 项单测及构建字节预算均通过；`make test-integration` 通过。
- F17：`go test ./app/billing/... -count=1` 与关键 biz/data race 测试通过；
  SQLite 真实事务测试覆盖发放失败回滚。
- O2：告警类型/SMTP 配置测试和假 SMTP 端到端测试覆盖 firing/resolved；
  生产受控邮件和 Alertmanager API 注入记录为 `sent`。
- Q3：v0.32.2 对照作业 `36224859013` 七项门禁通过。O5 原始隔离 E2E 的
  chat/结算断言通过；修正采样后的完整隔离 E2E 尚待重跑。

## 完整变更日志

- `aaba3cc4` docs: record next-stage production baselines
- `4645c58c` docs: record F17 sandbox payment and return boundary
- `9adea747` test: run Q3 amd64 comparison in CI
- `ef9ea07e` fix(ci): force TCP mysql readiness in Q3 benchmark overlay
- `e6298957` test(benchmark): re-pin Q3 baseline to v0.32.2
- `67138aae` docs(runbook): record Q3 rebaseline confirmation run
- `31ff9f96` docs: close Q3 amd64 three-run acceptance on current commit
- `8a1abde9` fix(billing): return persisted order on paid replay
- `9790d2c2` test(billing): F17 fault-injection acceptance for payment callbacks
- `ce08e27d` docs: close F17 fault subitems with isolated acceptance evidence
- `6208f805` test(e2e): measure Redis outage degradation latency in routing matrix
- `1270a4d1` docs: record O5 isolated Redis fault propagation evidence
- `a214e8cf` feat(notify): route Alertmanager alert groups to email
- `e41c746c` fix: correct review findings and acceptance status
- docs(release): v0.33.0
