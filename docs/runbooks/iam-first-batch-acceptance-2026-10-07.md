# IAM 上线后第一批 S1–S3 交付与验收

> 2026-10-07（Asia/Shanghai）· 规划提交 `67ba46d1` · 实施分支 `codex/iam-first-batch`。
> 依据：[当前计划](../design/next-stage-plan-2026-10-07.md)。S1 只读基线、S2 关键链路覆盖与 S3 门禁接线已交付；发现的生产对账断点已本地修复，**尚未部署或发布**。本次未推送，新增 GitHub job 尚未在远端运行，执行证据为本地同一入口与工作流静态/路径检查。

## S1：生产只读基线

[脱敏生产证据](evidence/iam-first-batch-production-2026-10-07.json) 采于 09:06:10 CST。仅执行 Docker inspect、HTTP GET、现存日志/指标读取和 MySQL 只读一致性事务；没有停服务、修改权限/配置、触发对账 POST、发起模型/支付请求或发送通知。

| 项 | 实际结果 |
| --- | --- |
| 九服务 | `/healthz` 均 200，restart 0，无 OOM；逐服务镜像 ID 与 source digest 匹配各自最新部署记录，允许 v0.34.1/v0.34.2/v0.34.4 与已验证 IAM 镜像混合 |
| 前端 | `/opt/web/dist/index.html` 与 admin 实际 HTTP 返回摘要均为 `883962cd…2820df`，匹配 v0.34.4 |
| IAM / SQL | `iam/complete`，policy 7、catalog 2；九个独立 outbound 凭证、固定 receiver map 完整，SQL/DATABASE DSN 一致，owner 通道非 root，旧 `root@%` 仍锁定，救援关闭 |
| 业务窗口 | UTC `[2026-10-06 00:50, 2026-10-07 00:50)`，即 CST `[10-06 08:50, 10-07 08:50)`；70 committed、4 released |
| 已结算关联 | 70 笔均有 consume ledger、reservation 关联 log、相应来源用量事件及非空订阅归属；`actual_cost = -SUM(consume.amount)`，金额不一致 0 |
| 后台积压 | 过期 reserved 0；结算任务 5920 条均 completed；支付订单 3 条均 paid/issued，没有 pending 样本 |
| 支付补偿 | billing 最近失败状态 0；24h success 增量约 1440.25，partial/error 0。Prometheus `increase` 是外推值，不当作精确调用次数 |
| 监控与通知 | 十个 target 均 up（九服务及 Prometheus 自身），47 条规则 health=ok/inactive；129 sent、2 条历史 failed，失败记录创建于 9 月 28–30 日。没有重放通知或重新核对收件箱；monitor/notify 24h 没有可用日志行不等于任务全部执行成功 |
| **定时对账断点** | 最新持久化结果仍为 `2026-10-05 01:05:20 UTC` 的 completed/0 差异；billing 可用近 24h 日志中 17 次 `Unauthenticated: user operator required`，没有 reconciliation completed。这项为生产故障，不能用旧的 0 差异结果证明当前对账正常 |

本窗口未覆盖未付款订单、真实 OAuth/多副本、供应商取消、平台重发与通知新失败/恢复。这些仍按原条件保留。

## S1 补缺：定时对账的 IAM 会话要求

红色反馈：

```bash
go test ./app/billing/internal/biz \
  -run '^TestReconciliationJobIAMBackgroundPersistsWithoutUserSession$' -count=1
```

修复前：后台 job 未保存任何运行记录，`store.saved` 长度为 0，断言失败。修复后：保存 completed 记录，外部无 operator 请求仍失败且不运行/落库。完整 reconciliation/alert 回归与 Billing 全包 race 通过。

排查区分了后台缺少用户会话、专属 caller grant 遗漏、事务内授权上下文丢失三种可能。复现定位到 `RunReconciliation` 中的 `PrepareOptional(...issues.read)`：主运行授权已区分 trusted in-process 与外部入口，但可选明细字段授权无条件向 identity 请求用户 operator，在建立结果/持久化 defer 之前中止了后台任务。

修复只在 `authorization.External(ctx)` 时查询该独立字段权限；后台继续通过既有本进程信任路径执行，外部运行权、reason、明细字段、用户身份和 owner 范围检查保留。没有新增系统 caller 能力、权限豁免、迁移或配置。

**生产恢复待部署**：将这份修复纳入按仓库流程发布的 billing 镜像后，复核启动对账及下一次定时对账的新运行时间、状态/差异、错误日志与待结算/过期预留。若报告真实差异，另行核对，不用手改账本或清空历史来消警。本次第一批验收没有执行该生产动作。

## S2：关键链路覆盖

| 场景 | 复用 / 新增证据 | 实际覆盖 |
| --- | --- | --- |
| 本人首页/账本/按日用量 | `TestIAMSelfUsageThroughDedicatedIdentityCaller`（真实 HTTP → identity/billing gRPC → SQLite） | root/member 精确 fixture 数值，跨用户查询过滤，无 operator/普通无效会话拒绝，密码 epoch 撤销 |
| 本人订阅与 API Key 用量 | 新增 `TestIAMSelfSubscriptionProgressThroughRealOwners`，复用两个 transport 回归与 authoritative-window 数据测试 | 普通会话 Admin → Billing 和 API Key Relay → identity/Billing 两条真实链路；已用 1.25、冻结 0.5、可用 8.25；客户端伪造 user_id、过期/无效会话、无关 caller、服务身份单独伪装本人、禁用 Key 被拒绝；无订阅返回结构化 false |
| 财务/联系字段 | `TestIAMUserListUsesOwnerScopeAndDistinguishesOutages`、`TestIAMB1ManagedUsersDialects`、`UsersPage.test.tsx` | 获准零值、范围外省略、联系字段空值与受限区别、依赖故障独立标记；owner 用户范围/总数与独立字段权限 |
| 公开模型/价格 | `TestReadonlyPricingWithGuardedModelOwner`、`TestPublicModelCatalogFiltersBeforePagination`、`TestPublicModelCatalogProjectsOnlyPublicMetadata`、`PricingPage.test.tsx` | 公开启用目录过滤先于分页/总数，模态投影、内部字段不泄露，目录失败不回退未过滤价格 |
| IAM 交互与 CAS | 既有七组真实浏览器、新增桌面/手机权限详情；`IAMPage.test.tsx`、`IAMChangeDialog.test.tsx` | 详情在当前视口内打开，关闭/切换正确；独立只读权限、目录引用、授权树/矩阵、DSD 与委派；409 后必须重新加载/预检，不自动重试 |

本轮发现并修正本人用量测试夹具不稳定：同一 JTI 每次重新签发 JWT 会跨秒改变 `exp`，与 IAM 已绑定的 session expiry 不一致。单次失败、连续三次复现中的失败及修复后五次 race 通过已核对；改为每个用户只生成一次会话，同时增加无效/撤销负向检查。业务的会话到期绑定保持有效。

新增订阅 fixture 使用真实 domain repo、billing 锁定读取、固定 caller map 和身份 resolver；没有 mock 授权响应。跨服务测试构建沿用各 app/testutil 边界，没有绕过 internal 导入限制。

## S3：持续门禁

- `make verify` 和后端 CI 显式调用 `make rbac-contract-check`，当前 829 行契约通过。
- 共享 `e2e.yml` 新增独立 IAM browser job，准备 Go 1.27、Node 24、锁定生成器/协议、web 依赖及 Chrome；Release/Nightly 默认执行，PR 通过路径分类选择。
- IAM/owner/api/domain/platform/web、共享 harness、Makefile、工具版本、校验脚本及工作流自身的变更会触发；实际 PR shell 对 16 个路径 fixture 执行验证，纯 docs 不触发额外 IAM 浏览器。
- `make test-iam-browser` 强制打开 opt-in，保存 Go JSON、Playwright JSON、截图及失败 trace。结果检查器拒绝 missing/skip/fail、零浏览器场景、浏览器 skipped/unexpected/flaky；不把 Go PASS 下的浏览器跳过计作验收。
- 已用实际 checker 注入矩阵 source digest 漂移，退出 1；实际未开启 opt-in 的 Go skip 报告也被拒绝，退出 1。原矩阵和代码随即恢复。

复验入口：

```bash
make verify
go test -race ./internal/integration ./app/billing/... -count=1
make test-iam-browser
cd web && CI=1 npx playwright test
```

验收边界：全仓 verify（215 个前端单元测试、lint/build/六项预算、架构/迁移/契约检查）、完整 Billing/集成 race、专项九组真实浏览器均通过；受影响工作流 actionlint 通过。三库 opt-in 测试本轮仅执行 SQLite，MySQL/PostgreSQL 明确跳过，不新增三库生产或升级证明。通用浏览器以 `CI=1` 完成复跑，67 passed、1 skipped（桌面项目按既有规则跳过 mobile-only 用例）；首次本地运行未正常退出，已取消，不计为通过。[本地验证清单](evidence/iam-first-batch-local-2026-10-07.json)记录执行、跳过和四类 Playwright 负向门禁。

## E1：有限灰度状态核对

只读确认 Relay flag=true、allowlist=2，当前实例于 `2026-10-05 10:36:33 UTC` 启动，不能拼接 10 月 1 日旧实例窗口。最近 24h 有约 60 条 Chat SSE、8 条 Messages SSE orchestrator success；无同入口/stream 的 legacy 对照，重置 0、已抓取的 up 最小值 1、失败抓取样本 0。没有新增生产模型探测。

E1 退出结论为**观察条件未满足**，不认定七天 PASS；连续性、对照及样本构成不足仍保留。运行事实同步至 [executor 观察手册](../design/v0.23-executor-observation.md)。本次不启动 E2 扩面、O5 缓存优化或 D7 新功能。
