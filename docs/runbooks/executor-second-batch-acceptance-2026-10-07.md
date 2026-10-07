# executor 第二批 E1/E2 交付与验收

日期：2026-10-07（Asia/Shanghai）。对应 [当前计划第二批](../design/next-stage-plan-2026-10-07.md#4-第二批executor-有限灰度的状态与结论)。仓库基线 `main@43924b1d`，最新正式发布 v0.34.6。运行事实和时间表只维护在 [executor 观察手册 §4](../design/v0.23-executor-observation.md#4-每日留证)，本记录说明交付方法、结论边界及后续方案。

## 本次交付

- **E1 完成**：只读核对运行镜像/源码摘要、flag/allowlist 数量、当前进程、七天内可见进程变化、原始抓取时间戳及实际 SSE 样本。精确正式起点仍未成立，原因已写入观察手册。
- **E2 条件评估完成，准入未通过**：判为 **INSUFFICIENT**，缺当前进程同入口 legacy、连续七天及可比较 model/source/时段 cohort。不是新一轮 FAIL，也不改变历史 FAIL；保留有限灰度和 legacy，不扩面。
- 新增 [只读取证脚本](../../scripts/executor-observation-readonly.py) 和 [生产证据](evidence/executor-stage-two-production-2026-10-07.json)。区分 24h、当前进程与七天历史窗口，保存原始 counter 与 `increase` 两种口径，查询固定终点；直接检查原始 `up` 样本的首尾、失败和时间戳空洞，而非用 `query_range` 插值后的点证明抓取完整。
- 顺带收口第一批的自然小时对账观察；当前计划及文档索引已同步。S3 的固定契约和十三项快速门禁在 [v0.34.6 main CI](https://github.com/mengbin92/micro-one-api/actions/runs/37561206557) 实际执行成功；该 job 的浏览器结果契约检查不等于执行了专项真实浏览器矩阵。

## 命令与实际执行边界

```bash
PYTHONDONTWRITEBYTECODE=1 python3 scripts/executor-observation-readonly.py \
  --output docs/runbooks/evidence/executor-stage-two-production-2026-10-07.json
PYTHONDONTWRITEBYTECODE=1 python3 scripts/executor-observation-readonly.py --help
python3 scripts/check-markdown-links.py
git diff --check
```

脚本默认仅从本地 `.env` 读取 `DEPLOY_REMOTE_SERVER`，也可显式传 `--server`。它在 SSH 远程进程内读取 Docker 环境，凭据不返回本机；数据库查询限定为只读一致性快照、15s 单条 SELECT 预算，输出聚合字段和列名。容器在取证期间发生替换、指标查询报 warning 或采样缺失时，命令失败且不覆盖证据。SQL 覆盖 consume / reservation / consume log / 对账记录，**未重新验证 source usage event、支付或通知投递**；这些沿用第一批各自证据边界。

本轮实际执行：生产只读采集、CLI 帮助、Python 语法检查、仓库 Markdown 链接检查及 diff 检查。执行结果为成功，生产数据详见证据。没有业务 Go/前端/协议/迁移修改，未运行独立三库 IAM、真实浏览器、routing E2E、性能实验或新七天观察，不把这些未执行项目记为通过。此次工具与文档提交见本文件 Git 历史，不另打应用发布 tag。

## 自然 cohort 缺口与隔离长流方案

当自然流量不足时，当前计划要求先给出缺口和隔离方案。本轮完成该交付，下面是**待执行方案**，不能当作已经取得的性能证据。

1. 复用 `scripts/test-routing-e2e.py` 的私有 Compose、真实服务和独立凭据/卷，以及 `test/e2e/routing/stream_reliability_test.go` 的账务和故障断言。旧 `--reliability-only` 只覆盖既有正确性矩阵；尚没有下面长流成对性能模式，不能声称该选项已经执行本方案。
2. 在私有 mock upstream 增加确定性长流 fixture：固定请求体、最终模型、来源、usage 和协议正常终态，覆盖 Chat / Messages / Responses SSE；用固定 256 / 1024 输出 chunk、短/长上下文及一致发送间隔分别测量。fixture 的名称仅表示测试档位，不把 chunk 数冒充 token 数。输出完整性通过每个协议的结束事件及固定内容摘要验证。
3. 每个档位预热后至少 50 对新旧请求，两路分别使用私有 allowlisted / non-allowlisted Token。逐对交替启动顺序，保持低且相同并发；主比较同 endpoint/stream/payload/最终 model/source/时段，单独记录客户端 TTFT、端到端 P50/P95、服务端 elapsed、输入/输出 usage。若实际来源、输出量或缓存命中不一致，排除该对并记录原因，不能将其延迟归因到 executor。
4. 正常样本要求两路完整流、成功率、单一 consume/dedupe、Reserve→Commit；复用故障矩阵验证出流前 500/连接失败可回退，出流后截断、客户端取消、慢下游/预算耗尽仅释放、不重试上游、不落 consume，恢复后的下一请求可完成。failover switched/exhausted 和 quota 异常必须有真实断言，不以空指标判通过。
5. 若任一档位新路径 P95 回归 >20%，或出现协议/账务差异，判 FAIL 并保存 payload 摘要、两路耗时、fixture 节奏及真实来源筛选证据；修复按 diagnosing-bugs 流程。全部档位通过只证明隔离负载，仍须按观察手册建立生产精确起点并完成连续七天，不能据此删除 legacy。

本方案不使用生产 Token、渠道或付费上游，不扩 allowlist、不修改生产配置。额外生产付费请求或扩面仍超出第二批规划范围。

## 线上交付方式

此次交付仅包含观察工具、文档和脱敏证据，不改变应用运行逻辑；线上同步到独立运维目录，保留文件校验清单和部署证据。业务容器、镜像、环境、前端入口及 IAM 事实不因文档同步而改变，避免重启打断候选窗口。同步完成后核对文件 SHA256、九服务实例 ID、Relay 健康和两枚 Token 灰度数量；实际位置和结果见 [线上交付证据](evidence/executor-stage-two-online-2026-10-07.json)。
