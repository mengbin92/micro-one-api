# 下一阶段计划：用户购买闭环、交付一致性与 executor 长流验收

> **当前执行入口**。制定：2026-10-08（Asia/Shanghai）。仓库核对基线：`develop@bf083b28`，初始工作区干净；README / CHANGELOG 最新发布登记为 [v0.34.9](../releases/release-v0.34.9.md)。本计划接替 [2026-10-07 计划](next-stage-plan-2026-10-07.md)，保留既有验收及条件项。
>
> 初始规划完成后，按维护者确认顺序实施。N1–N4 已完成本轮验收：用户购买隔离链路与外部沙箱、本地门禁/通用 smoke、部署脚本 stub、600 对长流与 24 项故障矩阵。N5 于 10 月 8 日只读复核仍为 INSUFFICIENT。详见 [本轮实施记录](../runbooks/next-stage-acceptance-2026-10-08.md)。本轮代码未提交、推送或部署，不预定版本；下方基线核对保留初始时点。

## 1. 已交付基线与核对结果

| 主线 | 已交付 | 当前边界 |
| --- | --- | --- |
| IAM / RBAC | A–D、S1–S3、固定入口契约、真实 owner 浏览器与本人/公开读取回归 | v0.34.6 已恢复对账；v0.34.7–v0.34.9 已纳入模型健康、普通用户可用组、购买/支付执行点及权限页面修复，不重新立项 |
| Web / D7 | 安全 Markdown、系统字体、关键分支覆盖率、重复提交与授权轮询修复 | 已纳入 v0.34.7 及后续版本；旧“未提交/未发版”是历史时点，当前入口已同步 |
| 用户购买 | [真实 HTTP/RPC/IAM/SQLite 回归](../../internal/integration/iam_subscription_payment_test.go) 已覆盖订单创建、owner 定价、同键重放与拒绝边界 | provider 为 mock，测试止于建单；[现网证据](../runbooks/iam-self-service-hotfix-2026-10-07.md)仅证明无副作用探针进入 owner 校验，未证明登录用户完成支付/权益发放 |
| executor | HTTP/SSE 首切片、有限灰度、只读取证工具与隔离长流方案 | [第二批结论](../runbooks/executor-second-batch-acceptance-2026-10-07.md)仍为 **INSUFFICIENT**；长流成对模式尚未实现，正式七天起点未成立 |

代码与门禁核对：

- [Makefile](../../Makefile) 的 `make verify` 执行前端 lint/test/build，但没有 CI 已执行的 `npm run test:critical`；已有配置和命令可直接复用。
- Web PR 已通过 [路径选择](../../.github/workflows/pull-request-e2e.yml)触发共享通用浏览器 smoke。普通 CI 的 main/develop push 不运行该 smoke；v0.34.8 在 Release 才暴露旧按钮断言。补发布前执行证据，不新建同义套件。
- 新支付集成用例没有 opt-in，CI 的 `make test-integration` 会执行；`iam-browser-gate-check` 的十三项是结果契约测试，不能替代业务集成或真实浏览器执行。
- [部署脚本](../../scripts/deploy-update.sh)硬编码目标、从 `latest` 保存回滚且吞掉失败，未更新固定镜像引用，启动未显式禁止构建/拉取；[最近上线流程](../runbooks/iam-self-service-hotfix-2026-10-07.md)已按实际 image ID 和固定引用操作。脚本需对齐，不据此判定现网故障。
- 本轮 `go run ./cmd/rbac-contract-check` **PASS（829 项）**。直接架构检查在 Wire 测试中失败，错误指向本地旧协议生成物缺少 IAM 类型和 revision 字段；当前 proto 定义存在，`*.pb.go` 被 Git 忽略。`test-unit: proto` 已有生成前置条件，实施时先生成再验，不作为业务缺陷立项。本轮未刷新生成物、运行全仓验收或构建部署产物。

## 2. 顺序与工作包

推荐顺序：**N1 用户购买闭环 → N2 门禁一致性 → N3 部署脚本对齐 → N4 executor 隔离长流**。N2/N3 可并行；任何实际发布前先完成相关门禁与镜像核验。N5 必须满足起点条件。文档入口收口本轮完成。

| ID / 优先级 | 最小范围 | 退出条件 | 粗估工作量 |
| --- | --- | --- | --- |
| N1 / P1 | 复用真实 owner 支付测试与既有回调/F17 测试，串联创建订单到权益及本人用量可见性 | 隔离链路有真实存储、授权、签名回调和唯一发放断言；允许/拒绝、重复与失败恢复均留证；生产未覆盖项单列 | 1–2 工作日 |
| N2 / P1 | `make verify` 接入既有关键覆盖率命令；明确直接推送/发布前通用 smoke 的执行步骤 | 低于既有阈值会失败；桌面/手机授权刷新用例均实际执行，保留结果和失败 trace | 半个工作日 |
| N3 / P1（下次部署前） | 现有部署脚本对齐环境配置、所选服务的实际运行镜像备份与固定 Compose 引用 | 本机 stub 验证目标、回滚来源、固定引用、禁止远端构建/拉取及失败退出；部署后检查指定服务 image ID/health，而非仅查看固定 billing/admin | 半天–1 工作日 |
| N4 / P1 | 在私有 routing Compose/mock 中实现并运行既定长流成对实验 | 可重跑报告明确 PASS / FAIL / INSUFFICIENT，完整流/账务一致，所有档位新路径 P95 回归不超过 20% | 1–2 工作日，实验耗时另计 |
| N5 / 条件启动 | 依观察手册建立生产同入口新旧对照及连续七天窗口 | 精确起点、采样连续、可比 cohort 和回滚门槛均满足；隔离 PASS 不替代生产准入 | 起点成立后至少 7×24h |

估算用于拆批，不是交付承诺；发现可重复失败后，按 diagnosing-bugs 的聚焦反馈环另开最小修复，保留失败与恢复证据。

### N1：用户购买闭环

复用 [IAM 订单测试](../../internal/integration/iam_subscription_payment_test.go)、[支付 fixture](../../app/billing/testutil/iam.go)、[notify HTTP 边界故障测试](../../app/billing/internal/service/billing_alipay_notify_test.go)和 [F17 故障测试](../../app/billing/internal/biz/payment_f17_fault_test.go)。现有 notify 边界测试使用 fake 验签器/仓储/issuer；新增整链验收需接入真实 RSA2 验签器、临时本地密钥及实际订单仓储，临时密钥做法可参考 [RSA2 测试](../../app/billing/internal/biz/payment_alipay_test.go)。现有测试已验证的建单/定价场景继续复用，新增断言集中在跨边界缺口：

1. 普通用户真实会话读取可用组，为本人购买；钱包与支付分别可达，客户端金额和 user_id 不能改写 owner 决定的价格/主体。
2. 支付订单经有效签名回调到 `paid` 与发放完成；对应订单、幂等 claim、订阅权益/购买快照数量正确，本人进度与 API Key 用量可见。
3. 同键建单、重复签名回调均不能二次发放；错误签名、金额/app_id 不匹配、跨用户、无会话及错误 caller 拒绝。发放失败可补偿并在重试后收敛。
4. 新增链路先在隔离环境完成；外部沙箱往返复用已有手册。生产先关联自然产生的已结束订单/账务记录；受控账号、套餐及支付操作另行明确范围后执行，不能以空请求探针或 SPA 200 计购买成功。

归档命令、退出码、executed/skipped 和存储断言。SQLite 证据标明方言；涉及事务/幂等实现修改时，执行既有 MySQL/PostgreSQL 对应测试并提供独立 scratch DSN，migration smoke 不等于三库业务验收。

### N2：门禁一致性

直接复用 [关键覆盖率配置](../../web/vitest.critical.config.ts)，不降低阈值。把 `npm run test:critical` 接入 `make verify`，输出实际专项执行及覆盖率结果；用临时抬高阈值或对应关键分支断点验证门禁能拒绝，恢复配置后通过。

通用 smoke 继续使用 [Playwright 配置](../../web/playwright.config.ts)和 [授权刷新用例](../../web/e2e/authorization-refresh.spec.ts)。Web PR 已有共享 E2E；直接推送/发版前按同一命令留证，先不新增每次 push 的昂贵全量 E2E：

```bash
cd web
npm run test:critical
CI=1 npm run test:e2e -- --workers=2 --trace retain-on-failure
```

10 月 8 日本机 macOS 双 worker 出现已有退出超时，不能登记 PASS；单 worker 完整 69 passed / 1 skipped。发布前本机使用 `--workers=1`，Linux CI 仍保留既有配置，具体命令与边界见实施记录。

结果需区分既有 mobile-only skip 与必须执行的授权刷新桌面/手机用例。IAM 专项、通用 smoke、D7 生产构建测试各自保存结果，不能相互代替。

### N3：部署脚本对齐

读取 `DEPLOY_REMOTE_SERVER` / `DEPLOY_REMOTE_DIR`，复用现有配置来源；先核对目标 Compose 的有效配置。备份实际运行容器的 image ID，备份失败必须阻断；明确切换目标服务固定 image 引用，并使用 `--no-deps --no-build --pull never`。所有镜像继续本机交叉构建 `linux/amd64`。

扩展 [现有 stub 测试](../../scripts/test-deploy-update.py)，覆盖配置缺失、非默认目标、固定引用、实际回滚来源、上传/加载/备份失败和所选服务核验。最小检查为 `bash -n scripts/deploy-update.sh` 与 `python3 scripts/test-deploy-update.py`，不以测试脚本为由访问生产。前端仍独立同步宿主机挂载目录。

### N4 / N5：executor 验收

按 [第二批待执行方案](../runbooks/executor-second-batch-acceptance-2026-10-07.md#自然-cohort-缺口与隔离长流方案)实施，复用 [routing harness](../../scripts/test-routing-e2e.py)、[mock upstream](../../test/e2e/routingmock/main.go)和 [流式故障矩阵](../../test/e2e/routing/stream_reliability_test.go)。不把已有 `--reliability-only` 当成长流性能模式。

- Chat / Messages / Responses SSE，固定 256 / 1024 chunks、短/长上下文及一致发送间隔；每档预热后至少 50 对，交替启动新旧顺序，低且相同并发。chunk 数不冒充 token 数。
- 比较同 payload / 最终 model / source / 时段，输出 TTFT、端到端 P50/P95、服务端 elapsed、usage、完整结束事件和内容摘要；来源、输出量或缓存条件不一致的请求对排除并记原因。
- 正常流单一 consume/dedupe、Reserve→Commit；出流前错误可回退，出流后截断/取消/慢下游/预算耗尽仅释放、不重试、不落 consume；故障恢复后请求可完成。
- 任一档位 P95 回归 >20% 或协议/账务差异判 FAIL；样本不足判 INSUFFICIENT，不能用空指标判 PASS。

生产运行事实只维护在 [executor 观察手册](v0.23-executor-observation.md)，本计划引用结论。只有该手册四项起点条件齐备才进入 N5；保留中断/重启边界，不拼接窗口，不按 10 月 1 日开启日期推算 PASS。验收通过前保留 legacy 和有限灰度。

## 3. 条件保留项

继承 [历史阶段清单](next-stage-todo-2026-09-26.md)的条件，不因旧 TODO 未勾选而重新实施：

| 项目 | 恢复条件 |
| --- | --- |
| O5 缓存/回源与首次数据等待 | 出现代表性多用户流量或明确延迟/QPS/成本问题；首次约一秒等待先关联同一登录请求的浏览器和 admin→owner 耗时，再决定优化 |
| 真实 OAuth / 多副本 | 接入真实轮换或准备扩副本；多 channel 前验证共享 selector，复用 Redis 协调/强杀恢复验收 |
| F17 平台重发与通知 | 自然失败后补生产 firing/resolved、收件箱和平台实际重发证据；隔离恢复已验，不主动制造生产支付故障 |
| 三库 IAM 持续门禁 | IAM 迁移/事务/并发变更时优先保证既有三库测试实际执行；按路径配置 scratch DB，输出 executed/skipped，不重复造测试框架 |
| D2–D6 / 原生 provider | 真实产品需求、供应商接入、容量/毒事件或可比性能问题出现后，以单个最小工作包恢复；executor 未准入前不删除 legacy |
| Playground 参数重放/分支编辑、对账容差、Lite | 具体用户需求或部署反馈出现后实施，继续保留当前行为 |

## 4. 交付清单

- [x] 当前发布登记、已完成项及下一阶段入口同步，历史记录保留。
- [x] N1：用户购买→回调→权益→本人进度闭环，隔离/沙箱/生产证据分别留界。
- [x] N2：本地关键覆盖率门禁与发布前通用 smoke 执行一致（本机单 worker）。
- [x] N3：所选服务部署脚本与固定镜像/实际回滚流程一致（本机 stub）。
- [x] N4：executor 隔离长流成对模式实施、运行及结论归档（600 对、24 项故障 PASS）。
- [ ] N5：条件满足后完成正式七天准入。

每项实施结果记录在本轮实施记录；当前未提交。业务实现继续遵守 DO/DTO/PO、`pkg/jsonx` 与生成文件规则。发版按 release note → CHANGELOG → README → develop → main → tag 的完整流程执行，版本按实际变更决定。
