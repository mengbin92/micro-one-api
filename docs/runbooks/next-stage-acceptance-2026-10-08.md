# 2026-10-08 顺序实施与验收记录

基线 `develop@bf083b28` / 最新发布登记 v0.34.9。按 [当前计划](../design/next-stage-plan-2026-10-08.md) 顺序实施；本轮按授权进入 v0.34.10 文档、Billing 部署与发版流程；最终上线记录见文末，以下隔离结果保留执行时点与范围。

## N1：用户购买闭环完成

扩展既有 `TestIAMSubscriptionPaymentThroughRealOwners`，使用实际 identity/channel/billing owner、admin HTTP、relay API Key、临时 SQLite 与内存 RSA2 密钥。支付 provider 生成离线表单，不联系外部上游。

- 普通用户真实 IAM 会话建单，owner 决定主体和价格；客户端 user_id/金额不能改写。相同幂等键返回原订单，套餐下架后同键重放仍可用、新键拒绝；跨用户、无会话、错误服务 caller 拒绝。
- 实际 RSA2 HTTP 回调校验签名、app_id（包括缺失）、金额（错误、零、无效、缺失）；无效回调订单保持 pending，订阅和路由权益均为零。
- 订阅写入之后注入发放失败，订单/订阅/权益整体回滚；同一有效回调重试后收敛为 paid/issued。重复回调不延长有效期；冻结合同、source_order、订阅和权益唯一。
- 本人进度 HTTP 和真实 API Key 用量读取合同与额度一致。钱包续购按 owner 价格扣款，同键重放只扣款和续期一次，消费账本与 commerce receipt 各一条。

发现并修复回调金额核对失效：原 HTTP callback 调用了需要本人 IAM 会话的查单用例；匿名回调查单失败被忽略，已签名的错误金额通知仍可发放。新增 `PaymentUsecase.AcceptVerifiedNotify` 从本地仓储核对订单金额/支付渠道后调用既有事务，不查询 provider、不要求用户会话。缺失金额失败关闭。既有公开查单授权保持原契约。

```bash
make proto
go test ./internal/integration ./app/billing/... -count=1
```

最终相关包均 PASS（集成包约 12.94s）。业务链路使用 SQLite；未执行 MySQL/PostgreSQL 的整链支付验收，既有多库 migration smoke 不能替代它。本轮没有改动事务或幂等仓储实现。

### 外部支付宝沙箱往返

使用 Chrome 已登录的 Administrator，对现有套餐续期一次。已核对配置指向支付宝沙箱网关；用户手动完成支付。只读数据库、验签回执和浏览器订单/本人订阅三处一致：CNY 100.00，paid/issued，订阅 #6，冻结合同摘要匹配，活动订阅和匹配订单各一条，路由权益一条。

有效期从 **2026-12-26 10:35:33 CST** 延长到 **2027-01-25 10:35:33 CST**，增加 30 天；本人日/周/月用量可见。同合同续期按现有语义保留订阅原 source_order，新增订单通过 subscription_id 关联。见 [脱敏证据](evidence/n1-subscription-sandbox-2026-10-08.json)。

沙箱往返使用原有部署版本。该往返时本轮错误金额修复尚未部署；负例、重复回调和发放故障仅在隔离测试执行。没有归档买家凭据、密钥、完整订单号、provider 交易号或签名 URL。

## N2：本地关键门禁与 smoke 完成

`make verify` 直接接入既有 `npm run test:critical`，保持原阈值。全仓门禁最终 exit 0，包括后端测试/race、架构、迁移、829 项 RBAC、十三项 IAM 结果契约、前端类型/lint/unit/critical/build 与 bundle budget。回调源码变化只刷新固定入口矩阵的一条源文件摘要，入口及 caller 权限没有变更。

关键专项 13 文件、140 测试通过；branches 85.47%、lines 93.13%、statements 91.64%、functions 91.37%。拒绝验证：

```bash
cd web
npm run test:critical -- --maxWorkers=2 --coverage.thresholds.branches=101
```

140 项测试仍通过，但覆盖率阈值使进程 exit 1；随后正常阈值由 `make verify` 再验通过。未修改配置阈值。

通用桌面/移动 smoke 共 **69 passed、1 skipped，exit 0**；授权刷新用例分别执行桌面与手机，均通过。最终命令：

```bash
cd web
CI=1 PWTEST_CHILD_PROCESS_TIMEOUT=10000 npm run test:e2e -- --workers=1 --trace retain-on-failure
```

边界：本机 macOS 的原两 worker 命令虽完成相同业务断言，仍以 worker 退出超时失败，不能登记为 PASS；单 worker 完整套件约 2.3min。保留原 Linux CI 配置，并记录本机 single-worker 执行方式。旧 routing-billing fixture 的 undefined Query 警告仍出现，未借本轮扩大前端修改。

## N3：部署脚本本机验收完成

现有脚本读取 .env/环境中的部署坐标，先验证全部服务和远端有效 Compose。继续在本机 cross-build linux/amd64；从实际运行容器的 image ID 建回滚标签，加载镜像后校验 ID，固定所选服务 Compose 引用，显式 `--no-deps --no-build --pull never`，逐项核对新 image ID 与健康状态。

整批保留原 Compose 备份；单项有效配置不匹配时恢复该项修改前配置，保留此前已成功服务的镜像引用。失败会阻断，不输出完成。前端宿主目录和迁移继续独立步骤。

```bash
bash -n scripts/deploy-update.sh
python3 scripts/test-deploy-update.py
```

exit 0，6 项测试（另含 6 个失败子场景）：缺失配置/非默认目标、参数全列表校验、实际回滚 ID、固定镜像和原字段、禁止远端构建/拉取、上传/保存/备份/加载/有效引用/健康失败退出，多服务初始备份及第二项失败保留第一项引用。stub 执行实际远端 heredoc，但所有 Docker/SSH/SCP 均被本机替身接管。此节为部署前验收；实际上线见文末。

额外运行 `bash scripts/check-deployment-docs.sh`：41 个 Kubernetes 资源全部 valid、83 项引用、254 个 Markdown 文件本地链接，以及各 Compose 变体/路由开关检查通过。修正此检查脚本的 BSD `mktemp` 模板：使用随机临时目录中的 `.yaml` 文件，兼容 macOS 并确保 kubeconform 实际验收资源，而非因无后缀而跳过。schema 下载在获授权联网后执行；初次沙箱网络阻断不计 PASS。

## N4：executor 隔离长流

实现 `--long-stream-pairs`，复用私有 Compose、真实九服务、SQLite/Redis/Prometheus 与本地 mock。固定同一 gateway、用户、模型、来源 channel、请求体、输出内容与 usage，使用 allowlist 内外两个 Key 选择新旧路径，并核对真实 executor 计数增长。固定 1ms 上游 cadence、串行并发，每档预热 3 对后交替新旧顺序。

Chat/Messages/Responses × 256/1024 输出 chunks × 短/32KiB 上下文，共 12 档；chunks 不代表 tokens。保存原始 TTFT、总耗时、账本服务端 elapsed、usage、内容与 payload SHA256，以及各档 P50/P95。每次 Reserve→Commit、单一 consume/dedupe、完整内容与终止事件均核验；少于 50 对为 INSUFFICIENT，P95 回归超过 20% 为 FAIL。

初次小样本暴露夹具计费路径变化：小订阅额度耗尽，一笔 120 的消费正常拆为订阅 39 + 余额 81，两个明确后缀的 dedupe；并非重复扣款。正式性能 cohort 撤销夹具订阅，固定余额结算，保留严格单一 consume 断言。

正式性能实验 **PASS，exit 0**。每档 50 对，共 600 对/1,200 个计量请求；另有 72 个预热请求，未混入统计。长流测试耗时 957.88s。最大端到端 P95 回归 **1.63%**，全部低于 20% 门槛；完整流、usage、模型/来源/内容/payload 一致和单次结算断言全部通过，排除请求对为零。[完整脱敏报告](evidence/executor-long-stream-2026-10-08.json)包含全部原始计量样本、TTFT/账本 elapsed 的 P50/P95、测试源码 SHA256 和边界。

```bash
python3 scripts/test-routing-e2e.py --driver sqlite3 --long-stream-pairs 50
```

单位 ms；单元格为 legacy / orchestrator。各行均 50 对。

| 协议 | chunks | 上下文 | 总耗时 P50 | 总耗时 P95 | P95 回归 |
| --- | --- | --- | --- | --- | --- |
| chat | 256 | short | 308.57 / 306.20 | 319.52 / 314.93 | -1.44% |
| chat | 256 | long | 312.57 / 309.64 | 321.59 / 321.32 | -0.08% |
| chat | 1024 | short | 1193.76 / 1190.14 | 1213.66 / 1213.36 | -0.03% |
| chat | 1024 | long | 1188.09 / 1191.55 | 1211.40 / 1216.58 | +0.43% |
| messages | 256 | short | 310.62 / 310.34 | 323.11 / 317.95 | -1.60% |
| messages | 256 | long | 310.87 / 308.79 | 325.16 / 324.61 | -0.17% |
| messages | 1024 | short | 1189.41 / 1194.89 | 1216.51 / 1218.71 | +0.18% |
| messages | 1024 | long | 1195.93 / 1199.11 | 1223.56 / 1221.38 | -0.18% |
| responses | 256 | short | 310.40 / 309.23 | 318.68 / 319.52 | +0.26% |
| responses | 256 | long | 310.23 / 309.00 | 320.91 / 326.13 | +1.63% |
| responses | 1024 | short | 1188.19 / 1193.96 | 1208.18 / 1212.47 | +0.36% |
| responses | 1024 | long | 1199.99 / 1191.42 | 1217.73 / 1215.77 | -0.16% |

流式故障矩阵 **PASS，exit 0**，新旧路径 × channel/subscription 上游优先 × 500/连接拒绝/截断/取消/暂停读取后取消/总预算耗尽，共 **24 个场景**。出流前失败后跨来源完成，一次 commit/consume 并释放失败预扣；出流后仅一条 released reservation、无 commit/consume、一次上游调用，取消/预算故障后新请求恢复并单次结算。固定用户钱包计费；上游 subscription 来源仍实际覆盖。500ms 总预算在真实 relay 配置生效，暂停读取后取消等待 100ms。[脱敏场景结果](evidence/executor-stream-faults-2026-10-08.json)保存每项结算和调用数量。

```bash
python3 scripts/test-routing-e2e.py --driver sqlite3 --reliability-only --keep
```

同轮 legacy 基线、Redis 停止降级及恢复全部通过；保留的私有 Compose 在取证后按精确项目名清理。迭代中修正两个过严的测试假设：转换后的错误流允许 error finish frame 后 `[DONE]`，它不代表成功结算；SSE 首行允许 `event:`，取消在首个 data 帧之后执行。没有因此修改 executor 生产逻辑或放松 commit/consume/retry 断言。

补充执行既有五项真实 HTTP/server 故障测试，覆盖 HTTP/2 取消/总预算、adaptor slot/upstream 关闭、截断不提交/重放，以及 downstream writer failure；exit 0（1.416s）。这些单元测试的 billing client 是 fake。真实 Compose 的慢下游场景为暂停读取后取消，未模拟 TCP 发送缓冲耗尽负载；性能使用固定 cadence/native channel，不外推自然上游、跨来源性能或生产准入。

小样本退出门禁另行实测：`--long-stream-pairs 1 --skip-build` 完成 12 档，各仅 1 对，报告 **INSUFFICIENT，命令 exit 2**；零样本参数在创建项目之前 exit 2。只有完整 PASS 返回 0，协议/账务/P95 失败返回 1。[小样本证据](evidence/executor-long-stream-insufficient-2026-10-08.json)单独归档，不混入正式 600 对。**N4 隔离范围完成；N5 仍独立按条件保留。**

## N5：条件复核，尚未启动正式七天

通过既有 [只读取证脚本](../../scripts/executor-observation-readonly.py)重新采集，生产运行事实见 [观察手册](../design/v0.23-executor-observation.md)；完整 [脱敏证据](evidence/executor-n5-condition-2026-10-08.json)保留固定查询终点 **2026-10-08 10:28:37 CST**。

flag=true、allowlist=2、健康 200；当前进程约 63.87h。同入口/stream 的 legacy 对照仍为空，条件 4 不满足，不能确认可比自然 cohort 或正式起点。当前进程 15,328 抓取点、最近 24h 5,760 点，无失败/空洞；历史七天含 4 个失败点及部署边界，不能拼接成准入窗口。

24h consume=289、distinct dedupe=289；账本→reservation→consume log 关联/状态/归因/金额均无差异，committed=289、released=512、expired reserved=0、released-with-consume=0；归档的 14 轮自然小时对账均 completed/0 差异。聚合正常不代表新旧性能可比。

结论保持 **INSUFFICIENT**；继续两枚 Token 有限灰度与 legacy。只读复核没有发起上游请求、扩大 allowlist、改配置或重启服务。只有观察手册四项起点条件齐备，才开始连续 7×24h；隔离长流结果不能替代生产准入。

## N6：v0.34.10 Billing 线上部署完成

部署前只读快照（`before`）与部署后快照（`after`）及首次尝试记录归档于 [生产部署证据](evidence/patch-v0.34.10-production-2026-10-08.json)。

**首次尝试（deploy-20261008-111427-ea4c22aa）在容器切换前停止**：服务器 Compose 5.5 的 `docker compose config --images billing-service` 会同时返回所选服务与依赖服务镜像，导致脚本单一镜像比较失败并自动恢复 Compose 配置；服务未切换，无线上影响。修复为从解析后的 JSON 配置读取所选服务镜像字段（`fix(deploy)`，`404ccab9`），6 项部署 stub 与失败子场景回归通过后重试。

**重试部署成功**：

```bash
./scripts/deploy-update.sh billing-service
```

- 新镜像 `docker-compose-billing-service:deploy-20261008-111825-404ccab9`（对应 `404ccab9`），旧镜像回滚标签 `rollback-20261008-111825`；服务器仅加载镜像并重建该容器，未远端构建/拉取。
- 部署后只读复核（11:55 CST）：billing `healthz` 200 `{"status":"ok"}`，restart_count=0，源码摘要标签存在；启动以来日志无 fatal/panic；其余 8 个服务容器未受影响。
- 数据库只读一致快照：IAM `cutover_state=complete`；沙箱订单 `paid/issued`、金额 10000、合同摘要与订阅 6 一致；订阅 `active`、路由权益 1 条；最近 5 轮对账均 `completed/0` 差异。
- 前端与其余服务本版未变更，无需更新；回调负例与故障注入留在隔离环境，现网仅只读核对原订单及权益。
