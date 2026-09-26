# 第三批 Q1–Q3 实施与验收记录

> 日期：2026-09-23
> 基线：`develop@feb4241f`；本轮提交见本文件 Git 历史
> 范围：Q1 历史请求快照与充值页状态；Q2 raw 验收、资料与 PR 门禁；Q3 可复现性能门禁

## Q1：Playground 与控制台

Playground 为每次发送保存独立检查器快照。历史助手消息携带稳定的本地关联 ID，服务端返回不同 `X-Request-ID` 时仍可回看该轮请求 JSON、HTTP 状态、Trace ID、Token、耗时和原始事件。停止请求会立即记录完成时间；清空对话同时清空快照。API Key 仍只存在页面内存中。

充值页移除固定 blue/slate 状态色，改用现有主题 token；快捷金额是带 `aria-pressed` 的真实按钮，键盘激活、焦点环和选中语义在浅色/深色下保持一致。图表已使用 `--chart-*` token，排名标签页已有方向键回归，因此本轮没有重复改写。

本地证据：

- `npm test -- --run src/pages/PlaygroundPage.test.tsx`：5 / 5 通过；两轮请求使用不同服务端 Request ID，第一轮快照仍可复制并与发送时 JSON 一致。
- `npx playwright test e2e/layout-regression.spec.ts --grep 'recharge amount choices'`：Chrome 桌面/移动端、浅色/深色共 4 / 4 通过；覆盖键盘激活、`aria-pressed`、输入联动、页面无横向溢出及选中态文字对比度 ≥ 4.5。
- 完整 `npm test -- --run`：52 个测试文件、196 项通过；`npm run lint` 与带字节门禁的 `npm run build` 通过。
- 完整 `npx playwright test --workers=1`：65 项通过、1 项按桌面项目条件跳过，退出码 0。

Q1 的供应商内部是否在客户端取消后停止推理，仍受 [渠道 9 取消证据](evidence/next-stage-channel9-cancel-2026-09-23.json) 所述外部边界限制。

线上部署（2026-09-24 06:25 CST）：生产前端由 `/opt/web/dist` 挂载提供，本轮只替换静态产物，未重建或重启 admin-api。替换前备份为 `/opt/web/dist.bak.20260924-062528`；部署后远端和公网首页 `index.html` 的 SHA-256 均为 `f3b3c32dc9b7367b4f531048acd1e1d71286e9f9c1efbbf94478aceff61d91a6`。公网新入口 JS `index-CyJssG6o.js`、CSS `index-ZHeBYcrX.css` 及 `/recharge` 均返回 200，admin-api 容器持续运行且近期日志无新增错误。

## Q2：可重现验收和资料治理

共享 routing E2E 新增 raw 合约场景，继续复用现有 mock upstream、真实 Relay/Channel/Billing/Identity/Log 服务和 MySQL/SQLite 矩阵：

1. `/v1/embeddings` 缺省 model 时选择 `text-embedding-ada-002`，在延迟上游返回前能读到 `reserved`；成功后只有 committed reservation 和 consume ledger。
2. 公共模型 `q2-public-embedding` 映射到 `text-embedding-3-small`，响应与 reservation 均记录实际上游模型和 channel 来源。
3. 受控 500 在真实预留后释放，最终无 reserved、无 committed、无 consume ledger、余额不变。

本轮在隔离 Compose 项目中分别运行 SQLite 与 MySQL 完整矩阵，两种方言的 legacy、v2（含上述三个 raw 场景）、Stage F preflight、Redis 停止/恢复、缺失账务能力及创建回滚阶段全部通过；每轮结束均删除测试容器、网络和卷。`go test ./test/e2e/routing -run '^TestRoutingAcceptance$' -count=1` 的非 E2E 编译门禁也通过。

`.github/workflows/pull-request-e2e.yml` 按 PR 路径选择共享 E2E：Web 改动运行 Playwright，后端/迁移/Compose/路由验收改动运行 routing 与 Compose 矩阵；nightly 和 release 仍默认运行全部套件。快速 `ci.yml` 不被全量 E2E 取代。

历史 schema 语义已有三方言证据：SQLite 覆盖 fresh、分段增量和时间字段历史形态；本轮把 D1 新增的 109 镜像纳入已知迁移数，并在 fresh/增量两条路径显式断言 `credential_refresh_pending` 落地。MySQL/PostgreSQL smoke 覆盖 fresh、repeat、status、失败回滚及历史 `schema_migrations.applied_at NOT NULL` 无默认值的显式修复流程。手工分区 DDL 已限定 MySQL、schema owner、迁移 078、claim 回填、维护窗口和验证步骤。`data-flow-next-stage-acceptance.md` 首尾均区分 F17 沙箱正常往返与仍待验收的故障子项。新增 `docs/incidents/TEMPLATE.md`，强制记录发现时间、影响窗口、范围、样本来源及无法恢复的 `UNKNOWN`。

2026-09-26 已完成一笔支付宝沙箱 ¥0.01 正常支付往返：生产订单由 `pending/pending` 收敛到 `paid/issued`，有支付宝交易号和支付时间，对应充值账本及幂等 claim 各 1 条、到账 $0.1000；控制台订单与充值记录一致。用户确认支付后的浏览器返回到控制台 `/orders`，该 `alipay.trade.page.pay.return` 路径属于同步 `return_url`，不能代替服务端异步 `notify_url` 回执。当前没有归档异步通知 HTTP 记录，订单终态也可能由每分钟后台查单收敛；脱敏口径见 [F17 沙箱往返证据](evidence/f17-alipay-sandbox-roundtrip-2026-09-26.json)。F17 的丢回调、查单暂时失败、重复签名回调和发放失败恢复仍待隔离故障验收；正常支付成功不替代这些场景。

## Q3：性能基线与门禁

### 前端字节预算

`npm run build` 现在自动运行 `web/scripts/check-build-budget.mjs`。基线记录于 `web/performance-budget.json`，20% 是工程准入线。统计口径为 `dist/index.html` 引用的入口及 modulepreload JS、首屏 CSS、最大 WOFF2 字体分片和全部字体分片产物；不会把 98 个 Unicode 分片之和冒充单次首屏下载量。

本轮实测：初始 JS 724,051 bytes / gzip 236,900 bytes；CSS 214,134 / gzip 61,082 bytes；最大字体分片 76,800 bytes；98 个字体分片产物合计 4,507,216 bytes。全部低于基线 +20%。

### 服务基线脚本

`scripts/benchmark/summarize-regression.py` 要求基线与候选各至少 3 个 k6 summary，保留原始样本，取中位数检查吞吐、HTTP 错误率、aggregate/chat/models/healthz P95 和 dropped iterations。20% 与连续 5 / 5 E2E 都是工程准入规则，不解释为统计显著性。

脚本以归档的 Linux/amd64 `ff518b1` 三次 summary 对自身做自检，七项均通过。该自检只证明解析和门禁逻辑；当前 `feb4241f` 工作树尚未在同一 Linux/amd64 机器完成 3 次全量 k6，因此没有写入新的跨版本性能结论。

### Dashboard 索引

`scripts/benchmark/dashboard-index.py` 在同一个 200,000 行 SQLite fixture 上分别强制旧 `(user_id, created_at, model_name)` 与新 `(user_id, type, created_at)` 索引，查询、参数、预热和 9 次采样完全相同。证据见 [next-stage-q3-dashboard-index-2026-09-23.json](evidence/next-stage-q3-dashboard-index-2026-09-23.json)：新索引计划同时收窄 user/type/time，median 从 32.11 ms 降到 13.86 ms（56.82%）。这是 arm64 SQLite 的可复现计划证据。

2026-09-26 在生产 MySQL 对 `AggregateLedgerByDate` 的同一用户、`consume`、相同 90 天窗口进行只读 `EXPLAIN ANALYZE`：旧索引三次 225/258/229ms，新索引三次 231/225/223ms，交替采样中位数分别为 229/225ms（新索引快 1.75%）。新索引确实把 type 加入 range 条件，但此用户 57,350 条旧索引范围记录中 57,301 条就是 consume，选择性很低；不能把 SQLite fixture 的 56.82% 提升外推到生产。原始口径、扫描行数和脱敏结果见[生产 MySQL 索引证据](evidence/q3-production-mysql-dashboard-index-2026-09-26.json)。这完成了该负载的生产计划核对，不构成对其他用户或高并发场景的性能保证。

高频内部导航的 hover/focus 预取已由 `route-loaders.ts` 和 `AppNavigation.tsx` 接入；账户概览继续使用已发布的 30 秒 `staleTime`，本轮没有缺少新鲜度样本却继续调参。`RELAY_ORCHESTRATOR_ENABLED` 默认仍为 `false`，在新的流式 Linux/amd64 对照完成前保持 legacy 默认路径。

最终统一门禁 `make verify` 通过，包括全量 Go 单测、关键包 race、分层检查、迁移治理、生成一致性、52 个前端测试文件（196 项）和生产构建字节预算。
