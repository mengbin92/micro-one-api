# P3：费用 API、运营复盘与有界候选修复

后续部署：按用户指令已完成 [P3 生产更新](billing-upstream-p3-deploy-2026-10-14.md)，四个服务、工具镜像和告警已更新；以下保留部署前的工程验收边界。

2026-10-14。以 P0/P1/P2 已完成为接续基线，在 `90a818ce` 上继续两份方案的 P3。本文记录本地工程与可审查制品；真实供应商权限、同窗 API/CSV 和生产灰度使用新的证据窗口验证。工程实现不会替代来源收费、模型公开或发票确认门禁。

关联：[计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[契约方案](../design/upstream-contract-evolution-plan-2026-10-13.md)、[P2 工程](billing-upstream-p2-completion-2026-10-14.md)。

## 交付与边界

| 接缝 | 实现 |
|---|---|
| 费用 API | billing biz 声明 query/evidence/reader/validator，data 持有共享 HTTP client 和供应商凭证。OpenAI organization costs 与 Anthropic organization cost_report 只读；固定官方域名，无 redirect、无任意 URL、无失败正文/凭证日志 |
| 固定范围 | 已关闭的 UTC 整日 `[start,end)`，最多 31 日；只接受 account_period、USD/currency；OpenAI 使用官方 project filter；Anthropic 无 workspace filter，读取完整 workspace 分组后精确选择，保留组织原页且无归属项拒绝，不发送未定义参数。不能从组织费用推导模型/key/request 粒度。账号与 credential binding 单独有证据 |
| 精度与分页 | 原始 page SHA 与 bytes 留证，最多 64 页、每页 1 MiB、总原始页 2 MiB，缺页/重复游标/重复桶/范围变化/重复 JSON key 拒绝。OpenAI decimal/scientific JSON 数字与 Anthropic cents 字符串以 `big.Rat` 处理，不经过 float64；超过现有 18 位金额契约拒绝 |
| CSV 门禁 | 抓取前固定完整同窗 CSV 身份和金额；API 精确比较，不以宽泛容差放行。owner 导入重新回放原始 pages，并要求该独立 CSV 已在 billing 表内；agent 自报 total 不够。差异 archive 可输出供调查，但不能走匹配导入 |
| 迟到/更正 | 复用 P2 immutable report/run 与唯一 supersedes 链；API archive 原始页以 base64 bytes 随报告保存（缩进/序列化不会改变原页 SHA），重复不生效，旧 CSV/报告不改写，API 只声明 provider_reported。新报告不自动升级 invoice，不改变用户价或 ledger |
| 管理 API | 既有 POST `/api/admin/vendor-reports` 增加 mutually-exclusive `cost_evidence_json` 与 `csv`；import/read/attribute 权限继续独立。GET 增加 `weekly_review_json`，每项保留真实 scope、币种、unknown、内部成本、parser/contract revision 与后续动作，不累加重叠账期 |
| 监控 | 既有 ReconciliationJob 可选读取最多 100 个、全量历史的最新 leaf/run（超过 100 个则标覆盖不足，不截断）。attention、超过 72 小时 pending、空覆盖、读取失败与通知失败可见；指标只有有限 state，通知复用已有 notifier。无报告不当成零费用。Prometheus 增加故障与超期规则及故障注入用例 |
| agent 接缝 | `upstream-repair prepare` 接收脱敏事故定位、固定官方 basis digest、精确 scope/base/source/binding/incident revision，以及 agent 的有限规则或最小 patch。JSON 不接受命令字段，不执行样本文本、补丁或候选测试，不获得生产客户端/凭证 |
| 独立回放 | fixtures 与 proposal 分开输入；独立 authority、basis、scope 和 fixture digest 固定，要求 verified、负例、流终结覆盖。复用原生 parser/stream tracker 与 finite runtime，同一 fixture 离线比较；报告只保存 ID/路径/判定，没有请求值/对话正文。PASS 后仍为 review_required |
| 审查门禁 | reviewer 环境对独立回放和 CI 证据审查后，以 Ed25519 签署完整候选内容，授权最多 24 小时。channel 发布在 CAS 事务内验证受信公钥、内容签名、当前 base/binding/incident/source revision；未审新候选不能 publish，审查后换范围或换凭据会失败 |
| 新代码 | patch 限 16 个 adapter/apicompat/usage/evidence Go 文件和 256 KiB；不支持脚本、URL/鉴权配置、计费/钱包文件、mode/rename/binary 操作。新代码始终 requires_code_review_and_ci_build，不允许有限配置签名绕过新制品。路径检查只是候选封装约束，不能证明任意 Go patch 安全 |
| 自动协调 | `upstream-rollout` 在独立 operator 环境核验签名后，默认只校验；显式 apply 才请求 owner。固定期限、1–5% gray、最多 256 个控制请求，读到 owner 样本/窗口后才请求 promote；失败先 quarantine，再在 previous fresh proof 有效时 rollback。owner 持久期限在选路时生效，协调器退出/重启不会放过超期灰度；failed repair 撤销自动恢复授权 |
| 发布接续 | 获批有限契约通过既有 create → 独立有预算 canary → 1–5% gray → 固定 30 分钟/100 完整样本 → fresh proof → promote；P2 事故/TTL、三次/小时切换、回滚 proof 和冻结 attempt 保持有效。原协议失效时保持隔离及已授权备用路径 |

费用接口依据：[OpenAI Costs](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/usage/methods/costs)、[Anthropic Cost Report](https://platform.claude.com/docs/en/api/beta/organization/cost_report/retrieve)、[Anthropic 费用口径](https://platform.claude.com/docs/en/manage-claude/usage-cost-api)。Anthropic 报告以 cents 为单位，Priority Tier 不在此费用接口内。API 分页读完不证明 invoice finality；完整性水位、排除项和 CSV 依据需运营另行留证。当前不为 DeepSeek 猜测不存在于已验证契约的费用 API。

## 费用抓取与导入

1. 先用 P2 CSV 导入同账号、项目和固定账期的独立报告；`CSVReportID` 使用 `VendorReportID` 的完整 SHA。query JSON 包含 `scope`、`credential_binding_evidence`、`csv_report_id`、`csv_total`。`scope.unit="currency"` 表示原币主单位 USD，原 API cent 输入会另行换算。
2. 在独立 operator 工具环境设置 `VENDOR_COST_API_KEY`，不要传 CLI 参数或放入 query/report。原始 API archive 文件权限 0600，输出文件必须是新的绝对路径。

```sh
go run ./app/billing/cmd/vendor-cost-fetch \
  -query /secure/fixed-cost-query.json \
  -csv-manifest /secure/reference-manifest.json \
  -vendor-bill /secure/reference.csv \
  -out /secure/new-cost-archive.json
```

同样参数加 `-replay /secure/new-cost-archive.json` 可无凭证重放到另一个新输出文件。返回码 0 表示 API/CSV 同范围金额一致；1 表示差异且 archive 已保存；2 表示抓取/范围/分页/权限失败，不产出可确认报告。

3. API manifest 使用新 version、`evidence_id=archive.ID()`、`cost_basis="provider_reported"`。同 scope 已有报告时必须 `supersedes` 当前 leaf；完整性/水位保持人工核对，不能由 pages_complete 自动填成结清。CLI 可用 `vendor-reconcile -cost-evidence` 替代 `-vendor-bill`。`-import` 仍须专用 owner DSN，禁止带 offline attempts 导入。
4. 管理 API 可发送 `manifest_json`、`cost_evidence_json`、`expected_report_id`、`reason`；bytes 字段按 protobuf JSON/base64。owner 重新校验原页、存储中的 CSV reference 和权限，不信任调用方 Rows/CSVMatched 标志。2 MiB 管理输入预算不变，大 archive 走受控 CLI。

## 候选生成、审查与发布

```sh
go run ./cmd/upstream-repair -action prepare \
  -proposal domain/upstream/repair/testdata/finite-proposal.json \
  -fixtures domain/upstream/repair/testdata/independent-fixtures.json \
  -out /tmp/new-repair-review.json
```

仓库样例全部是合成 fixture，不是生产事故/供应商验收。外部修复 agent 使用同一 proposal schema；只获得脱敏输入、固定资料和候选工作区。工具没有 agent provider 的隐式付费调用，也不启动 shell/生产连接。客户端输入错误或没有失败的有效契约 probe 不进入该修复流程。

reviewer 使用自己维护、不得提供给候选 agent 的 fixtures 和 CI 结果，核对 basis 与请求含义；在 reviewer 环境运行：

```sh
go run ./cmd/upstream-repair -action review \
  -proposal /review/proposal.json -fixtures /review/independent-fixtures.json \
  -review-key /review/private-reviewer-key -key-id reviewer-1 \
  -reviewer operator -ci-evidence fixed-ci-artifact-id \
  -reason reviewed-equivalent-rule -expires-at REVIEW_EXPIRY_UNIX \
  -out /review/new-create-request.json
```

private key 是权限最多 0600 的 regular file，包含 base64 原始 64-byte Ed25519 private key。channel `UPSTREAM_REPAIR_REVIEW_KEYS` 配置 key ID → base64 32-byte public key 的 JSON map；默认无受信 reviewer，新 repair 发布失败关闭。`review` 只输出 protobuf JSON create request；仍需具备原有 IAM create/publish 权限的操作者提交，并经过 P2 canary/预算/灰度/晋级门禁。签名不包含新增收费/模型授权。

patch 候选先 code review、独立既有回归与 CI，再按仓库本地/CI 跨平台流程构建新 adapter/parser binary。该工具不应用 patch，也不会以 agent 自己新增的测试自证通过。修复失败不触碰已冻结请求或历史财务结果。

获批之后可在独立 operator runner 配置 `UPSTREAM_ROLLOUT_ADMIN_URL`（HTTPS 或 loopback HTTP）、`UPSTREAM_ROLLOUT_OPERATOR_TOKEN` 和 reviewer public keys，运行：

```sh
go run ./cmd/upstream-rollout \
  -reviewed-request /review/new-create-request.json \
  -until ROLLOUT_EXPIRY_UNIX -gray-percent 1 -max-control-requests 128
```

默认只验证。加 `-apply` 才允许执行具体已签候选的自动 create/publish/promote/quarantine/rollback；coordinator 没有供应商凭证，不调用真实推理。monitor 必须事先配置候选和 previous revision 的 ProbeTarget 及 P1 一次性预算，费用/权限仍由各 owner 核对。候选 200 但 usage 错误不会得到 proof，样本不足不按超时晋级，原 previous 已失效时回滚返回门禁错误并保持隔离。HTTP 控制失败需要核对 owner binding 后按同一候选恢复，不盲目覆盖 CAS。

## 配置、制品与验证

升级时先更新 billing/channel/admin/relay 的消费者与生成 DTO，再注册/发布含 repair_review 的契约；旧制品不用于 P3 签名候选。

三套 Compose 添加 `BILLING_VENDOR_REPORT_MONITOR_V1=false` 和 channel reviewer public-key 配置；没有自动开启生产开关。监控按当前小时级对账 job 执行；weekly_review 是固定读取窗口的复盘材料，没有给用户创建新的每周消息自动化。

```sh
docker buildx build --platform linux/amd64 --load --progress=plain \
  -f deployments/docker/Dockerfile.upstream-evidence-tools \
  -t micro-one-api/upstream-evidence-tools:p3-20261014 .
```

镜像包含 `vendor-reconcile`、`vendor-cost-fetch`、`upstream-repair`、`upstream-rollout`，非 root 运行；用 `--entrypoint` 选择工具。首次真实推理仍使用既有 relay/monitor 的一次性许可、调用/并发/token/成本预算，不在生产机编译。

本轮 `make all`、116 个单元测试 package、26 个竞态检查 package、internal integration、架构/RBAC 862 行/迁移治理、gosec、Prometheus 告警注入、三套 Compose 默认关闭状态、格式和 Markdown 链接检查通过。新增 owner 期限/故障与费用格式回归另行复测通过，linux/amd64 工具镜像及容器内离线回放已验证。完整部署文档脚本因本机缺少 kustomize 未运行，Kubernetes 文件未变更。

验证记录见 [P3 本地验收](evidence/billing-upstream-p3-2026-10-14.json)。新增用例覆盖 decimal/cents、合法零、无金额、分页与重复 key、credential redirect、固定 CSV identity、原始页篡改、重复导入/更正、未审候选、过期/内容/范围签名、旧事故、独立 fixture 失败、未知 patch 不执行、重复流终结及资金/通知告警。全仓 `go test ./...` 中真实 e2e suite 需要本机 9001/3000 等运行栈；本轮使用仓库 `make test` 和 `make test-integration` 门禁，真实外部 e2e 未用空栈冒充通过。

尚需现场资料：OpenAI/Anthropic 真实 admin 权限与 credential binding、独立完整同窗 CSV/API、官方水位/更正/排除项，以及已审候选的生产预算、至少 30 分钟/100 完整样本和发布/回滚收敛。未部署、未付费推理、未扩大收费、未修改用户账本。
