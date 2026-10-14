# P3 运营遗留工程补齐

2026-10-14。接续 `cfa2fc3b`。用户明确选择“先完成工程，保留生产门禁”，本轮不调用真实供应商 API、不进行付费推理，不配置生产 signing key、IAM 授权或新收费名单，也不将合成样例作为现场证据。

关联：[两份方案中的计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游契约方案](../design/upstream-contract-evolution-plan-2026-10-13.md)、[已部署 P3 控制链](billing-upstream-p3-deploy-2026-10-14.md)。

## 已接通的工程闭环

| 原遗留 | 本轮实现 |
|---|---|
| 定时费用抓取 | `evidence-worker -kind cost` 读取独立审查的固定 scope/query/CSV，按计划周期抓取；原始 page 与 decimal 原样归档，差异状态为 attention，不自动导入或确认 invoice |
| 持久预算与重启 | 独立 operator SQLite/WAL/FULL store，事务内先 claim 和占用每日调用预算；计划 ID+内容+payload SHA 不可变，同一个时段不重复执行。过期 running → uncertain，不重发已可能被执行的请求，也不返还已消费预算 |
| 外部 agent 实际接口 | 独立 OpenAI Responses client，显式模型和 256–8192 output-token 上限，不设默认模型。仅发送固定官方 schema/code 与无文本的原生 usage；tools=[]、store=false、无 shell、无文件/发布工具，固定官方 URL，拒绝 redirect。生成的 source/model/protocol/endpoint/inbound 不可改变，事故/binding/source/basis 由 operator facts 重建 |
| 修复调用与费用 | 一份不可变 repair plan 仅一次生成；每日调用预算包括 failed/uncertain。保留 provider header request ID、response ID、usage 和 internal_test purpose；费用为 null/unknown，不写用户账本，不按缺失 token 推断免费 |
| 生成后审查 | candidate JSON 与独立回放 packet 分开持久保存。有限规则用已有 parser/runtime 回放；Go patch 保持 requires_code_review_and_ci_build，不执行、不自签。继续使用 reviewer → 独立 CI → 签名 → owner canary/gray/promote/rollback 链 |
| 隔离部署 | 每个进程只运行 cost/repair/weekly 一种角色。repair 启动拒绝业务 DSN、service/operator token、reviewer 配置和其他角色凭据；输入只读、os.Root 防 symlink/traversal，state 0700、artifact 0600。可选 Compose profiles 默认关闭，rootless/read-only/cap_drop/no-new-privileges/资源限制，无宿主 Docker socket |
| 固定周复盘 | `-kind weekly` 每七天读取最多 31 日的 owner 投影，固定开始/结束时间，保存完整审查材料；pending/unknown/attention 保持可见。没有外发邮件/消息或自动退款，也不累计不同币种/重叠账期 |
| 管理入口 | `/admin/vendor-reports` 独立 `billing.vendor_report.read` 准入，UTC 导入窗、原币 decimal、scope/basis/状态、证据/更正前序、周复盘与导出。权限拒绝不发送请求，角色改变移除旧数据；修复 HTTP 包装丢失 weekly_review 的遗漏 |
| 展示与指标 | Grafana `supplier-evidence` 展示覆盖、unknown/overdue/notification failure 与固定 kind/state task counters。没有观测显示 disabled/unobserved，不当作确认零费用；不在 label 放账号、模型、任务 ID |
| 文档 | 主方案接续阶段统一为 P3，部署前“未部署”文字明确历史时间边界，现场验收与工程完成分别记录 |

接口按 [official OpenAI documentation：Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=responses) 核对。当前采用 JSON object 输出后做独立严格 schema/语义检查；completed、usage、预算、候选范围或独立回放失败都不能产生发布批准。没有把模型自己写出的测试用作独立证据。

## 受控 operator 配置

镜像使用 `deployments/docker/Dockerfile.upstream-evidence-tools` 本地/CI 构建 linux/amd64，新增 `evidence-worker`。生产主机不编译。三种 optional profiles 位于 `deployments/docker-compose/docker-compose.p3-operators.yml`，不并入既有默认部署。

每个角色准备 `p3/<role>/inputs`（只读）与 `p3/<role>/state`（10001-owned、0700）。唯一计划文件名为 `plans`；payload 和 reference 文件名为简单标识符，无绝对路径或子目录跳转。计划字段：

```json
{
  "id": "reviewed-fixed-plan",
  "kind": "weekly",
  "enabled": false,
  "payload_file": "weekly-payload",
  "payload_digest": "SHA256_OF_EXACT_PAYLOAD_BYTES",
  "interval_seconds": 604800,
  "expires_at": 1792022400,
  "max_runs": 4
}
```

`expires_at` 必须未来且不超过 30 日；过期时停止任务，不按时间到了推断通过。`kind=repair` 的 `max_runs=1`；cost 最短一小时；weekly 最短七日。示例在 `deployments/docker-compose/p3-example`，默认 disabled。

- **cost payload**：`query`、完整 CSV 的 `manifest`、`csv_file`、`csv_digest`、`credential_ref`。凭据从独立 `P3_COST_<credential_ref>` 环境读取，不进入计划/归档。CSV 发生更正需人工审查并使用新的计划 identity，不能原地修改固定 scope/总额。
- **repair payload**：`context` 与 `fixtures_file`/`fixtures_digest`。context 包含固定 `template` Proposal、`official_schema`（必须匹配 basis SHA）、允许目录的最小 `code` map、仅数字/boolean/null 的 `native_usage`、显式 `model`、`max_output_tokens`。模板和全输入有字节上限。仅设置 `P3_REPAIR_AGENT_KEY`，不得传入 reviewer private key 或生产凭据。
- **weekly payload**：`{"window_days":7}`。仅设置有 `billing.vendor_report.read` 权限的 `P3_REVIEW_OPERATOR_TOKEN` 与固定 HTTPS/loopback `P3_REVIEW_ADMIN_URL`。worker 只 GET 既有 owner，不做导入/发布调用。

operator profile 的独立 credentials.env 不进入 Git 或镜像；实际网络 allowlist 按角色限到官方费用/生成域名或 review owner。代码只使用固定供应商域名/已校验的 operator owner，并关闭 redirect；部署网络策略也应限制出口。内网指标端口 9465 仅暴露有限 kind/state，不发布到宿主公网。

```sh
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  -v /review/inputs:/inputs:ro -v /review/state:/state:rw \
  --entrypoint evidence-worker micro-one-api/upstream-evidence-tools:p3-ops-20261014 \
  -kind weekly -daily-calls 1 -once
```

没有批准生产配置前只使用 disabled plans、offline fake transport 和 `--network none` 验证。worker 输出 `<task-key>.json` 的带 receipt 内容；成功生成可审查 proposal 后同时输出 `<task-key>.proposal.json`，可交给既有 `upstream-repair -action review`，仍须重新核对独立 fixtures/CI/签名权限。

## 尚保留的现场关卡

工程接缝已经提供，以下项按用户本轮选择不执行：实际供应商 admin/模型凭据与账号绑定；真实完整同窗 CSV/API；生产 reviewer 公钥、IAM 与 candidate/previous canary 预算；至少 30 分钟/100 完整样本/fresh proof、5 分钟切换收敛及真实通知链路验收；两周实际流量基线。它们需要真实资料和批准后的观测窗口，不能通过增加 fixture 或改一个 PASS 字段完成。

正式启用前按本记录逐项配置并保存证据。开发和离线验证不改变已部署实例的 gate、用户价格、钱包或历史账本。

## 本轮验证

[验收索引](evidence/billing-upstream-p3-operations-2026-10-14/index.json)：完整 Go 单元、竞态、服务内集成、863 行 IAM 入口矩阵、架构、格式、全量 gosec 均通过；前端 80 文件/456 测试、TypeScript/Vite/资产预算通过。桌面与手机浏览器以合成 owner 数据验证原币精度、unknown 显示、查询和导出入口。linux/amd64 工具镜像在 network=none/read-only/cap_drop 环境运行默认关闭的 weekly/repair plans，通过持久 task DB 初始化与单次调度。

工程验收时代码位于 `codex/p3-operations-completion`，按当时用户选择尚不部署、不启用生产 profiles，不发起真实供应商或生成调用。用户随后要求更新生产并合并 develop，已按[后续生产更新记录](billing-upstream-p3-operations-deploy-2026-10-14.md)发布工程能力；profiles 和现场门禁继续关闭。工程验收索引保留验收时的历史状态。
