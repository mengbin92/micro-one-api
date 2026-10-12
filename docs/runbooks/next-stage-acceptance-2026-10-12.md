# 2026-10-12 顺序实施与验收记录

初始基线 `develop@02078e9b`，正式发布 v0.34.16。维护者确认按 [当前计划](../design/next-stage-plan-2026-10-12.md) 顺序执行；本轮发现并修复退款对账的生产阻断，旧验收结果不被覆盖。

## 1. 生产只读核对与退款对账修复

10:58 CST 核对九服务均运行、HTTP health 200、restart/OOM 均为零、近 24h 无 fatal/panic 日志。billing/admin/identity 的 `RECHARGE_AMOUNT_MULTIPLIER=10`，未调整倍率。096 已在 billing 属主应用，124 在 channel、125 在 config，零版本号账号/配置均为零。[部署前证据](evidence/next-stage-production-before-2026-10-12.json)保留镜像身份、源码摘要、配置指纹和聚合结果。

生产镜像标签是历史构建基线，不能直接当作最终提交号：channel/log/config/monitor/notify 的源码摘要匹配 `96b9dabf`；其余服务保留 10 月 11 日工作区构建摘要，不冒充与最终 v0.34.16 全树摘要相等。前端 77 个文件与当前 HEAD 新构建逐文件一致，[文件核对](evidence/next-stage-query-candidate-2026-10-12.json)无差异；本轮无需重新上传前端。

最近 24h 没有新支付订单；不发起生产购买/退款，不将配置核对记作新钱包购买或真实支付验收。

### 实际发现：退款联查排序规则不兼容

最近 24h 记录为 completed/0 差异 3 轮、partial/0 差异 21 轮。partial 的错误均为退款联查：`utf8mb4_0900_ai_ci` 与 `utf8mb4_unicode_ci` 比较被 MySQL 拒绝。不能以 discrepancy_count=0 判断 partial 成功。

用既有退款测试、仓库 scratch DB helper 与本机专用 MySQL/PostgreSQL 容器复现。修改前：SQLite/PostgreSQL PASS，MySQL 精确报 Error 1267；修改后与 race 复测三库全部 PASS，无跳过。覆盖同订单同用户配对、跨用户不能掩盖缺项、重复退款账本、非退款订单及无订单孤立账本。独立 schema 随测试清理，未使用部署 DSN。

修复仅在 MySQL 下统一查询表达式排序规则，并让分组表达式与输出一致；保留一个 SQL 语句的快照及原错误处理。无 DDL、无账务数据修改、无 API/proto 变更。代码提交 `5c08d26e`。

### 实际发现：只读取证 SQL 超时

原联查对每条账本走日志 level 索引，重复扫描约 19,066 行，超过 15 秒上限。EXPLAIN 确认后，指定日志侧不走该索引，使用一次 hash join；同一关联核对在 0.346 秒完成，899 条均无缺失、归因或金额差异。保留查询上限，未加生产索引。

修复后原 `scripts/executor-observation-readonly.py` 实际执行成功，固定查询终点留在 [完整报告](evidence/next-stage-executor-before-2026-10-12.json)：900 个 consume/dedupe，关联预留、日志、归因及金额差异均为零，过期 reserved 和 released-with-consume 均为零。预留 created_at 窗口与账本 created_at 窗口不同，不能把两者聚合计数直接当作一一对应。

**11:16 CST 部署验收完成**：[部署后证据](evidence/next-stage-production-after-2026-10-12.json)确认仅 Billing 镜像变化。新镜像 `docker-compose-billing-service:deploy-20261012-110921-5c08d26e`，image ID `sha256:9dee76888f7582d54ed2569d088582a7529baf3304dcf429417d693c3914b5de`，源码摘要 `3c0844a197d050e75508978a06f1a9aedb5b822c45c65806eb4e3ab1d850c791`；回滚标签 `rollback-20261012-110921` 保留旧实际运行镜像。服务器仅加载镜像和重建该容器，未构建/拉取。

启动对账 completed/0 差异 1 轮，九服务 health 200、restart/OOM 为零，全部环境指纹与前端保持；903 条消费关联及金额检查无差异。历史 partial 继续保留，不将启动轮当作自然小时轮验收。**12:18 CST 自然小时复核通过**：12:15:35 CST 的 #494 completed/0 差异，与启动轮 #493 一致；[只读证据](evidence/refund-reconciliation-hourly-2026-10-12.json)已归档，未手动触发对账。

## 2. 当前前端与全仓门禁

- `make verify` exit 0：Go unit/race、架构、迁移、RBAC、IAM 结果契约、生成类型及前端 lint/unit/critical/build 全部通过。
- 前端 75 文件/441 项单测 PASS；16 文件/186 项 critical PASS，statements 92.20%、branches 85.71%、functions 92.69%、lines 93.31%，原阈值保持。
- `CI=1 PWTEST_CHILD_PROCESS_TIMEOUT=10000 npm run test:e2e -- --workers=1 --trace retain-on-failure` exit 0，79 passed/1 既有 skipped，2.6min；包含桌面/手机 IAM 布局及授权刷新。既有 routing-billing mock undefined Query 警告保留，不将其列为本轮产品回归。
- 完整 lint/build 通过，六项产物预算 PASS；生产前端全部 77 文件摘要一致，入口 SHA256 `005ffc771328035a1d7e63e8f183ba9c0a0c502a3977499469d50accfc7fdf42`。

计划、TODO、文档索引已同步到 10 月 12 日入口；[本地门禁汇总](evidence/next-stage-local-checks-2026-10-12.json)保留实际执行/跳过边界。发布交付按 v0.34.17 完整流程执行，制品状态以远端 Release 实际结果为准。

## 3. 当前代码长流与故障复验

当前代码长流 **PASS，exit 0**：12 档 × 50 对，共 600 对/1,200 个计量请求，另有 72 个预热请求；测试 965.57s。最大 P95 回归 **+0.59%**，所有档位低于 20%；完整内容/终止事件、usage、同 payload/model/source 及单次结算断言通过。固定 1ms cadence、串行并发、每档 3 对预热和交替新旧顺序保持。

```bash
python3 scripts/test-routing-e2e.py --driver sqlite3 --long-stream-pairs 50
```

[完整原始样本](evidence/executor-long-stream-2026-10-12.json)包含 TTFT、端到端、服务端 elapsed、usage、payload/内容摘要与源码 SHA256。使用私有 SQLite/Redis/Prometheus 和实际九服务，upstream 为本地 mock/native channel；chunks 不代表 tokens，不外推自然 provider 或生产准入。私有项目容器、网络和卷已自动清理。

| 协议 | chunks | 上下文 | P50 legacy / executor (ms) | P95 legacy / executor (ms) | P95 回归 |
| --- | --- | --- | --- | --- | --- |
| chat | 256 | short | 308.35 / 310.13 | 321.29 / 319.34 | -0.61% |
| chat | 256 | long | 313.79 / 314.46 | 325.78 / 321.75 | -1.24% |
| chat | 1024 | short | 1196.65 / 1187.98 | 1223.55 / 1213.00 | -0.86% |
| chat | 1024 | long | 1199.93 / 1200.89 | 1221.64 / 1220.92 | -0.06% |
| messages | 256 | short | 313.21 / 313.80 | 322.88 / 324.72 | +0.57% |
| messages | 256 | long | 316.83 / 315.43 | 325.86 / 323.97 | -0.58% |
| messages | 1024 | short | 1202.49 / 1196.85 | 1223.73 / 1218.78 | -0.40% |
| messages | 1024 | long | 1199.56 / 1192.81 | 1235.68 / 1228.58 | -0.57% |
| responses | 256 | short | 315.37 / 311.73 | 330.27 / 324.78 | -1.66% |
| responses | 256 | long | 312.06 / 312.99 | 321.86 / 323.76 | +0.59% |
| responses | 1024 | short | 1204.57 / 1204.36 | 1228.81 / 1221.69 | -0.58% |
| responses | 1024 | long | 1198.37 / 1201.86 | 1228.15 / 1229.07 | +0.07% |

24 项故障矩阵 **PASS，exit 0**：[逐场景断言结果](evidence/executor-stream-faults-2026-10-12.json)覆盖 legacy/executor × channel/subscription 首选 × 500/拒绝连接/截断/取消/暂停读取后取消/总预算耗尽。出流前可切换并只结算一次；出流后仅释放、不重试、不消费，故障后请求恢复。测试包含实际 owner、存储及用量关联，500ms 总预算生效。慢下游为暂停 100ms 后取消，不外推持续 TCP 缓冲耗尽负载。

```bash
python3 scripts/test-routing-e2e.py --driver sqlite3 --reliability-only --skip-build
```

复用同轮构建镜像，期间未改测试或运行时代码；两个私有项目的容器、网络及卷均已自动清理。10 月 8 日报告保持独立。

## 4. N5 起点条件

生产运行事实维护在 [观察手册](../design/v0.23-executor-observation.md)。部署前只读报告：flag=true、allowlist=2、健康 200，当前进程约 20.93h；只有 Responses orchestrator SSE 增量，没有同 endpoint/stream legacy 对照。不能建立正式七天起点。

当前进程抓取 5,024 点、最近 24h 5,760 点，无失败/空洞；历史七天含部署边界和一个失败点，不能拼接为准入窗口。报告中当前进程还包括 6 次 stream_error 和 12 次 canceled 的外推增量，不据聚合成功数声称无错误或已准入。

**最终条件复核完成，结论 INSUFFICIENT**。固定终点 **2026-10-12 11:37:53 CST** 的 [最终报告](evidence/executor-n5-condition-2026-10-12.json)仍无同入口 legacy 对照，正式七天未启动。生产事实及连续性详见观察手册最新条目；隔离 600 对/24 场景 PASS 不替代生产准入。

补修了取证元数据：旧 Compose 容器标签覆盖镜像标签，新 Billing 原本会被报告为旧源码摘要。当前报告按 image ID 读取不可变镜像元数据，单列 `container_source_digest`，未改 Compose、配置或容器。以下自检实际通过，若用旧采集器重新采集会在源码摘要断言处失败：

```python
import json
from pathlib import Path
report = json.loads(Path('docs/runbooks/evidence/executor-n5-condition-2026-10-12.json').read_text())
deployed = json.loads(Path('docs/runbooks/evidence/next-stage-production-after-2026-10-12.json').read_text())
billing = next(row for row in report['runtime'] if row['service'] == 'billing-service')
expected = next(row for row in deployed['runtime'] if row['service'] == 'billing-service')
assert billing['image_id'] == expected['image_id']
assert billing['source_digest'] == expected['source_digest']
assert billing['container_source_digest'] != billing['source_digest']
```

本轮未扩 allowlist、关闭灰度、发付费对照请求或删除 legacy。下一步只有生产对照/起点条件齐备才进入 N5 连续七天，条件项继续保留。

## 5. 正式发布及最终生产复核

v0.34.17 已按 develop → main → tag 完整交付，tag 指向 `e8839266`，**13:15:55 CST 正式发布**。[Release](https://github.com/mengbin92/micro-one-api/actions/runs/38201074940) 17 项任务全部成功，含真实 IAM owner 浏览器、前端 smoke、Compose、MySQL/SQLite 路由门禁、九服务多架构推送及 GitHub Release。develop/main CI 和安全流水线均成功；[完整确认与独立注册表清单核对](evidence/release-v0.34.17-confirmation-2026-10-12.json)保留九服务 linux/amd64、linux/arm64 验证结果。生产保留已经验证的 Billing 热补丁镜像，没有为改变标签重复部署。

[发布后复核](evidence/executor-n5-post-release-2026-10-12.json)及 [传输区分](evidence/executor-transport-disambiguation-2026-10-12.json)已归档。legacy POST Responses SSE 出现约 63 次增量，但同一时间区间 orchestrator success 没有增长，仍缺同时段可比对照和确认起点。N5 继续 INSUFFICIENT，不回填七天窗口；生产运行事实以观察手册最新条目为准。Billing #493/#494/#495 均 completed/0 差异，其中两轮为自然小时运行；本轮没有手动触发对账、付费请求或扩大灰度。
