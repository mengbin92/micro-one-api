# v0.36.0 后续执行：告警修复、生产基线与双语入口

日期：2026-10-18（Asia/Shanghai）。对应 [当前计划](../design/next-stage-plan-2026-10-18.md)。本轮只修改告警规则、CI 与文档；生产只读取证后，仅补入并热加载 Prometheus request-timing 规则组，未重启业务服务、发送模型请求、导入供应商报告或修改财务/灰度配置。

## 1. 告警反馈环与部署

根因：`domain/requesttrace/exporter.go` 实际发出 `full/closed/failed/invalid/turn_capacity`，Wire 原样递增 `relay_request_timing_export_total{result}`；规则只匹配 `dropped|error`。队列饱和和导出失败会漏报。

新增 `deploy/prometheus/alerts/request-timing.test.yml`，用五个实例分别产生五类失败标签，并同时加入 queued/exported 成功流量。断言等待期前不触发、持续失败触发、恢复后清除，以及无流量不误报。修复前 `6m` 预期五条告警、实际 `got:[]`；修复标签后通过。

```bash
docker run --rm --entrypoint /bin/promtool \
  -v "$PWD/deploy/prometheus/alerts:/rules:ro" -w /rules \
  prom/prometheus:v3.6.0 test rules \
  operations.test.yml credential.test.yml routing.test.yml \
  vendor-evidence.test.yml request-timing.test.yml
```

五组均 `SUCCESS`。CI 的 Deployment and docs drift job 同步纳入新增测试及既有 vendor-evidence 测试。

生产只读核对发现 request-timing 组尚未加载。实际挂载文件还含已有 `p3-vendor-evidence` 组，因此没有用仓库文件全量覆盖：保留原文件所有规则，仅追加修正后的四条 request-timing 规则。候选文件本地 `promtool check rules`（53 条）与聚焦测试通过，远端校验旧 SHA-256 后原 inode 写入，容器内校验并 POST `/-/reload` 返回 200。09:43 复核四条规则均 `health=ok/state=inactive`。

备份：`/opt/micro-one-api/monitoring/prometheus/alerts.yml.bak.request-timing-20261018-094036`。恢复时先确认没有后续变更，再将备份内容写回原挂载文件、执行容器内 promtool 校验并热加载；不要删除其他运维规则。没有制造线上导出失败，真实故障触发/收件人送达不在本轮证明范围。

证据：[配置部署](evidence/post-v0.36.0-2026-10-18/alert-deploy.json) · [评估后规则健康](evidence/post-v0.36.0-2026-10-18/executor-cohort.json)。九个业务容器与 Prometheus 实例 ID 均保持不变。

## 2. 生产运行和账务基线

复用现有只读采集器，不读取凭据明文到输出：

```bash
python3 scripts/executor-observation-readonly.py \
  --output /tmp/executor-observation.json
```

[本轮快照](evidence/post-v0.36.0-2026-10-18/executor.json)固定终点 **09:36:06 CST**：

- Relay 健康 200，Prometheus target up；九业务容器 running，restart=0、OOM=false。
- 当前 Relay 始于 **2026-10-17 16:34:43.941505 CST**，已运行约 17.02h；4085 个 15s 样本，无失败/超容差空洞，首尾覆盖。历史 24h 有一次部署边界失败采样，不能拼成正式七天。
- 近 24h 消费 629 笔、distinct dedupe 629；缺预留/消费日志、归因/金额差异、过期 reserved、released 后 consume 均为 0。
- Billing 对账 #623–#640 均 completed/0 差异；列表取于 SQL 事务时点，没有固定 PromQL 终点上界，不混作封闭指标窗口。

## 3. 计时容量：基线已取，持续验收未完成

[09:39 容量快照](evidence/post-v0.36.0-2026-10-18/timing-capacity.json)记录：

| 项目 | 结果 | 边界 |
| --- | --- | --- |
| Producer / OTLP | 开启 / 已配置 | 不改既有开关 |
| 当前 Relay exporter | queued=169、exported=169 | 未出现失败标签；跨重启 24h increase 为外推值，不能和库内条数逐笔对齐 |
| OTel 队列丢弃 / 非法 carrier | 24h increase 均为 0 | 只代表已抓取指标 |
| SQL 摘要 | 669 条，JSON 总计 1,203,487 B，最大 1,870 B | 约 26.9h 的摘要开始时间跨度，不足以证明 48h 稳态 |
| 尝试关联 | 638 条 | 含未预占尝试，不等于账单数 |
| Jaeger | 2,670,332,228 B（约 2.49 GiB）；内存 243.7 MiB / 512 MiB | 全服务 tracing，不能只按模型调用数量估算 |
| 所在文件系统可用空间 | 24,462,512,128 B（约 22.78 GiB） | 单时点，不代表增长趋势 |
| WS 阶段 | 无 `ws.*` 样本 | WS 长连接与独立 turn 运行验收保留 |

摘要最早/最晚时刻来自库内 `request_started_at_ms`；Jaeger 的实际最老可查询 span 及 TTL 回收尚未核对。`LOG_RETENTION_DAYS` 没有环境覆盖不等于关闭保留，仍需按 log-service 配置解释。表大小来自 information_schema，是包含其他日志的估值，不能视为计时功能独占空间。

后续每次固定时点采样时保留：

- JSON 中的 PromQL、result 与 query_end；空序列按缺样本处理，不补造为健康零值。
- 各容器启动时刻、镜像、RSS/CPU 与限额，`du -sb` 的 Badger 物理大小、文件系统可用空间；配置 TTL 当前为 48h。
- 只读事务中摘要条数、JSON 字节、最早/最晚开始时刻和每小时增量，以及关联表和日志表的 data/index 大小。
- Jaeger 最老可查询跨度和物理文件回收的区别；至少跨完整 48h TTL 后再评估趋势。后台 RPC、健康检查等也会导出 spans。

容量原始记录内保存了具体 PromQL。SQL 只读查询可复用（凭据沿用现有采集器在远端容器内读取，输出不包含环境变量）：

```sql
SET SESSION MAX_EXECUTION_TIME=15000;
START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT COUNT(*), MIN(request_started_at_ms), MAX(request_started_at_ms),
       SUM(OCTET_LENGTH(request_timing_json))
FROM oneapi_log.logs WHERE observation_id IS NOT NULL;
SELECT FLOOR(request_started_at_ms/3600000), COUNT(*),
       SUM(OCTET_LENGTH(request_timing_json))
FROM oneapi_log.logs WHERE observation_id IS NOT NULL GROUP BY 1;
SELECT COUNT(*) FROM oneapi_log.request_trace_attempt_refs;
SELECT TABLE_NAME, TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH
FROM information_schema.TABLES
WHERE TABLE_SCHEMA='oneapi_log'
  AND TABLE_NAME IN ('logs','request_trace_attempt_refs');
COMMIT;
```

### 隔离高并发对照

复用 `TestRequestTimingControlledOverhead`，通过 Go overlay 将每模式请求数 160 改为 1280、worker 并发从 8 改为 64，保留 8 次预热、50ms 假上游与实际 OTLP HTTP 接收。生产源码和原测试文件没有修改。负载差异保存在 [实验补丁](evidence/post-v0.36.0-2026-10-18/perf-peak.patch)，环境、源文件 SHA-256 与全部结果见 [实验汇总](evidence/post-v0.36.0-2026-10-18/perf-peak-summary.json)。

| 运行 | P95 增量 | 既有 5% 门槛 | 摘要导出 / 丢弃 |
| --- | ---: | --- | ---: |
| 1 | 5.3266% | FAIL | 1288 / 0 |
| 2 | 5.0845% | FAIL | 1288 / 0 |
| 3 | 0.8117% | PASS | 1288 / 0 |
| 4 | 3.4493% | PASS | 1288 / 0 |

四次串行运行全部保留，不以较好的后两次覆盖前两次失败。**本实验不通过峰值准入**。本机为 arm64，固定“关闭后开启”的短测仍受调度、GC 和次序影响；应补长时、交替顺序及部署架构测量，再决定是否需要性能修改。所有运行包含预热，所以导出数为 1280+8；它不是 1288 笔生产账单。

复现时在仓库根目录为原测试建立临时副本及 overlay：

```bash
timing_scratch=$(mktemp -d)
cp internal/server/request_timing_perf_test.go "$timing_scratch/peak_test.go"
patch "$timing_scratch/peak_test.go" < docs/runbooks/evidence/post-v0.36.0-2026-10-18/perf-peak.patch
python3 - "$PWD" "$timing_scratch" <<'PY'
import json, sys
from pathlib import Path
root, scratch = map(Path, sys.argv[1:])
(scratch / 'overlay.json').write_text(json.dumps({'Replace': {
    str(root / 'internal/server/request_timing_perf_test.go'): str(scratch / 'peak_test.go')
}}))
PY
TIMING_PERF_ACCEPTANCE=1 TIMING_PERF_OUTPUT="$timing_scratch/result.json" \
  go test -overlay "$timing_scratch/overlay.json" ./internal/server \
  -run '^TestRequestTimingControlledOverhead$' -count=1 -v
```

`result.json` 即使 P95 断言失败也会写出。WS/计时既有聚焦回归 `go test ./internal/server -run '^(TestWSTiming|TestTiming)' -count=1` 通过；它不替代生产 WS 长跑。没有在生产压测或为对照重启 Relay。48h 持续采样按下方自动观察安排执行；在取得跨时间证据前保持待验收。

## 4. executor：INSUFFICIENT

灰度 flag=true、allowlist=2。原始 snapshot 有 Responses SSE orchestrator success=156 与 legacy success=10；传输快照只有 POST，没有 GET 系列。

进一步以固定终点读取当前进程原始 counter，逐相邻采样算真实增量：[同窗核对](evidence/post-v0.36.0-2026-10-18/executor-cohort.json)显示 orchestrator 增量出现在 **10/17 16–21 时**，legacy 增量出现在 **10/18 09 时**，没有任何同小时重叠。相邻差值不包含序列首个已非零样本，所以累计增量分别为 155/9，不能替代 snapshot 的 156/10。

**正式七天起点/终点仍为空，结论 INSUFFICIENT。** 当前两侧都有样本，不能继续说缺新路径样本；缺的是同时段可比对照。未拼接旧窗口、扩大名单或制造请求。运行事实同步至 [观察手册](../design/v0.23-executor-observation.md)。

## 5. 供应商与文档

生产 `billing_vendor_reports` 与 `billing_vendor_reconciliation_runs` 均为 0。重跑已有离线检查：

```bash
python3 docs/runbooks/evidence/deepseek-closed-day-2026-10-16/check.py
```

结果仍为六次调用、140333 token、CNY 0.14036956、差额 0；范围仍是手工试点日窗。没有将它导入生产或绑定虚构来源，独立供应商生产闭环继续待证。

中文 README 从 712 行精简到 332 行，保留最新升级摘要和历史索引；新增英文 README、完整英文 Lite Quickstart 与互链。根 README 两种语言均由本地链接检查覆盖。当前计划、TODO 与 docs 索引同步到 v0.36.0 后续阶段，10 月 16 日计划归档。

两种 Quickstart 的七个 Bash 命令块经归一化模型占位符后逐块一致，全部通过 `bash -n`；305 份 Markdown 的本地链接检查及 `git diff --check` 通过。没有为文档修改重新构建镜像或执行整套 Lite 构建。


## 6. 48 小时自动只读观察

维护者选择每小时自动只读采样、平稳时静默、48 小时后汇总。已创建当前聊天 heartbeat `v0-36-0-48`，名称「v0.36.0 存储稳态 48 小时观察」，状态 ACTIVE。

- 固定起点：**2026-10-18 10:10:20 CST**；固定截止：**2026-10-20 10:10:20 CST**。
- 每小时采集上述运行/导出/存储聚合，新增带时间戳的脱敏证据，保留实例、采集缺口与 TTL 边界；不输出密钥明文，不在生产发请求、压测、改配置或重启。
- 平稳时不发常规状态；只在新增异常、采集失败需处理或到期时通知，相同问题不重复提醒。
- 截止后按实际证据给出 PASS/FAIL/INSUFFICIENT，通知一次并暂停本任务；漏跑不补造、不顺延，也不因经过 48 小时自动通过。
- 独立的 `executor` 七天观察自动任务保持 PAUSED；本任务不启动七天窗口或改变收费/灰度边界。

执行依赖本机 Codex 应用运行和既有 SSH 可用。若应用离线导致缺采样，恢复后先记录覆盖缺口，到截止时间仍按实际材料总结。

## 7. 后续 P1/P2 隔离补验（同日）

维护者要求完成两项 P1 与 P2 后，在本地和已有独立原生 x86_64 构建机完成[隔离验收](p1-p2-isolated-acceptance-2026-10-18.md)：

- Linux/amd64、4 CPU / 1 GiB，64 并发四组成对交替、每模式 30 秒；P95 增量 1.09%–1.32%，均通过 5% 门槛，149,428 条摘要与 1,234,804 个 span 完整导出/接收。原 arm64 短测的失败保留，不追认为通过；CPU/分配开销及内存 sink 的范围见新记录。
- 普通模式 WS 真实 socket/gRPC 的 1200 轮、69.81 秒 race 长跑通过，预留/提交/摘要逐轮关联，无重复结算、丢弃或关闭重复导出。发现第 33 轮误用持久化历史上限，已在工作区修复并保留真实持久化模式原保护。
- Lite 全新源码构建、迁移、前端、登录、渠道、钱包、Token、首请求和重启通过；第二项目空卷恢复 SQLite/Redis/.env 后，原登录、钱包四项、Redis marker、原 Token 与渠道解密通过。补修默认指南/测试未生成独立服务身份造成的首请求 503，复用既有生成/校验工具，无授权放宽。

本批源码、Lite 示例和双语指南未提交、推送或部署，临时容器/卷已清理。生产观察实例、配置、TTL 和固定截止不变，生产 WS、供应商账期与 executor 门禁仍待各自证据。

发布更新（同日）：上述隔离批次实现已提交为 `6b656cd8`，纳入 [v0.36.1](../releases/release-v0.36.1.md)；前述未提交状态保留验收时点，生产实例与固定观察窗口保持。
