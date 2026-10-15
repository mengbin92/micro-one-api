# P2.1：固定账期外部核验与不可变报告

日期：2026-10-13。接续基线：`6071a995`。按本次指令以 P0/P1 完成为接续点，本次完成 **P2 的外部核验工程闭环**；P2 尚未整体完成。验收索引：[工程证据](evidence/billing-upstream-p2-external-2026-10-13.json)。本次没有部署生产、调用费用 API、导入真实供应商账单或修改收费名单。

关联：[计费准确性方案 §7–10](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游契约方案 §7–9](../design/upstream-contract-evolution-plan-2026-10-13.md)、[P1 交付](billing-upstream-p1-completion-2026-10-13.md)。契约方案的外部/CSV 核验接缝使用同一份 attempt 原生证据；typed contract 发布、有限参数规则、事故 revision 恢复仍是 P2 后续工程。

## 实现与核验边界

| 项目 | 行为与证据 |
|---|---|
| 固定账期 | manifest 固定 RFC3339 `[start,end)`、供应商时区、真实 provider/account/project、精确 upstream model、原币和单位；UTC 等价时刻不会形成不同导入身份 |
| 范围映射 | source→真实采购账号的有效时间、revision、证据引用随每次报告冻结；重叠映射拒绝；账号共享时必须覆盖整个可比账号范围，不能把模型名前缀作为财务主键 |
| 原始金额 | CSV 金额保留原始 decimal，计算使用 `big.Rat`；不逐行提前舍入，不自动换汇，不把缺失当零；现有本地费率估价明确标 `USD/currency`，其他原币不直接比较 |
| 采购与用户分账 | 从 billing execution intent/evidence 每个独立 intent 取一次；checkpoint/terminal/final 不叠加 token。用户按 reservation 计数；user/canary/health_probe/internal_test 分开，完整来源轨迹随报告导出 |
| 原生证明 | 校验每条 event digest 和冻结 intent，重新解析原生 usage、核对五桶，按冻结采购费率复算估价；不使用当前价格回填历史证据 |
| 执行时间 | HTTP JSON/SSE 与受控 WS 逐轮保存实际发送边界 `started_at_unix_nano`；不以 reserve 时间、响应完成时间或 inbox 收单时间替代；同一 intent 出现不同发送时间拒绝核验 |
| 匹配粒度 | 支持 request 或 account_period；逐请求必须声明有独立依据的 `request_id_kind=header/response` 与 `request_id_contract_evidence_id`，不把响应正文 ID 自动当成 HTTP request ID |
| 双方键集合 | missing_local、missing_vendor、ambiguous、unknown、variance、matched 分开；vendor=0/local>0 仍报 variance，零分母相对差额为 null；完整、无流量且供应商明确为零的账期可匹配 |
| 完整性 | 供应商报告/分页、本地与供应商水位、独立覆盖证据缺一则 pending；不完整终结、缺时间、未映射来源也维持 pending。未知时间/未映射 attempt 保留可追溯明细 |
| 不可变导入 | migration 132 由 billing 所有；每个 scope 只有一个根报告，同 scope/version 改内容拒绝；后续版本引用当前更正链叶节点。旧报告保留，追加原币差额；同内容重放去重，新本地证据追加 run |
| 差异归因 | 有符号差额固定为供应商减本地估价；confirmed 归因减入剩余差额，hypothesis 不减；重复证据和越过报告粒度的归因拒绝。解释后仍保留原始 variance，不自动批准费用、退款或调整 |
| 代表性来源 | tokens 比较真实范围内的 usage/估价；固定月费需套餐及超额条款凭证，逐请求计量 not_applicable；仅余额和完全不可见来源保持 unknown/not_comparable，不用余额或百分比反推 token 费用 |

`verification_scope` 仅描述供应商原始 invoice 金额的真实报告粒度，`amount_status` 才表示本地估价比较结果；两者独立。账号账期的 invoice 不能升级本地每个 attempt 为实扣，matched 也不会改写原 `tariff_estimate`。报告元数据和证据引用需要运营独立核对，代码只检查其结构与一致性，不会凭一个非空证据 ID 取得或鉴真发票。

## 命令与归属

无数据库的可复现样例全部为虚构数据：

```bash
go run ./app/billing/cmd/vendor-reconcile \
  -manifest scripts/reconcile/testdata/p2/tokens.manifest.json \
  -vendor-bill scripts/reconcile/testdata/p2/tokens.csv \
  -local-attempts scripts/reconcile/testdata/p2/local-attempts.json \
  -out /absolute/new/vendor-report.json
```

将 `tokens` 换成 `monthly / unknown / balance_only / zero-variance / pending` 可重放其他场景。token 样例包含一条用户请求和一条 canary，用户 reservation 只计一次，采购范围包含两次执行。

命令在本地或受控审计环境运行；生产主机使用本地/CI 预构建制品，不在资源受限主机编译或执行 `go run`。实库入口只读 `VENDOR_RECONCILE_DSN` 和可选 `VENDOR_RECONCILE_SCHEMA`，不自动加载 `.env`、服务写凭据或 Redis。默认仅需 billing 两张 execution 表的 SELECT 权限；快照读取使用只读、可重复读事务。命令例：

```bash
# 通过安全环境注入专用 SELECT-only VENDOR_RECONCILE_DSN，不在命令行传密码。
go run ./app/billing/cmd/vendor-reconcile -driver mysql \
  -manifest /secure/trial/manifest.json \
  -vendor-bill /secure/trial/vendor.csv \
  -out /secure/trial/new-report.json
```

显式 `-import` 才向 `billing_vendor_reports`、`billing_vendor_reconciliation_runs` 追加记录，需要独立的报告表 SELECT/INSERT 权限（MySQL 幂等 no-op 还需报告表 id 列 UPDATE 权限）；不需要钱包、用户 ledger 或定价表写权限。离线 `-local-attempts` 样例禁止 `-import`。此版是拥有者数据库运维入口，actor/reason 为操作声明；没有新增公网 API 或 IAM 报告管理页面，也不把这些声明当成已签名的 IAM 审批。

归因文件通过 `-attributions /secure/trial/attributions.json` 提交，格式是 comparison key 到归因数组的 JSON：

```json
{"account_period":[{"category":"contract_discount","amount":"-0.1","status":"confirmed","evidence_id":"invoice:discount-line","verification_scope":"account_period"}]}
```

类别支持失败/取消、重试/切换、缓存、reasoning、工具/多模态、内部探测、合同折扣、币种/税费/赠金/舍入、迟到/不完整；必须使用代码定义的固定枚举（见 `AttributeVendorDifference`）。未知差额不能归因，同一证据不能重复用于不同差额。

输出文件必须为绝对新路径，0600、同步落盘，拒绝覆盖；失败时清理未完成文件。退出码 0 表示工具检查得到 matched 或有凭证的 monthly not_applicable；1 表示 pending/unknown/missing/variance；2 表示输入、权限、导入冲突或运行失败。退出码不批准 charge，也不表示采购估价已变成逐笔发票。

现有 `scripts/reconcile/checks.go` / `reconcile.sh` 支持 `-vendor-manifest/-vendor-out` 或 `--vendor-manifest/--vendor-out`，外部核验委托上述 billing 命令；原 provider-family CSV 和默认 5% 比较已撤除。脚本内部 rolling window 仍只用于原有用户账务检查，外部窗口来自 manifest。展示用 ledger token/配置估价先按 reservation 合并双轨行；配置估价差额不称为已确认供应商毛利。

## 输入规则、历史范围与预算

CSV 必须具有固定 13 列：`provider,account,project,upstream_model_id,currency,unit,request_id,amount,uncached_input_tokens,cache_read_tokens,cache_creation_5m_tokens,cache_creation_1h_tokens,output_tokens`。每行 scope 必须与 manifest 完全一致；未知/重复列拒绝，不静默删除附加计费维度。amount 空为未知、`0` 为已知零；五桶须全部出现或全部为空。account_period 文件只有一条聚合行且 request_id 为空；request 文件按供应商实际 ID 唯一。供应商原始科目到五桶的单位/包含关系与税费、折扣、赠金、现金支出口径，须在原报告及 manifest 证据引用中先核实；未知维度不按基础 token 价替代。

manifest 的绝对、相对容限默认都用字符串 `"0"`；非零容限必须引用 `rounding_evidence_id`，预算按绝对容限加供应商金额绝对值乘相对容限计算，不用宽泛 5% 掩盖系统误差。原币必须相同；费用 API、换汇、月费分摊、现金流余额反推不在此版实现。

金额最多 18 位小数，超出则拒绝而不截断。输入限 32 MiB、最多 100,000 行/attempt，单 attempt 最多 1,000 条 event；数据库意图扫描超过上限明确失败，不静默截断。实库查询限一分钟；大范围须先整理可审计的试点证据或归档。旧 intent 无实际发送时间，仍列 unknown_time；只有独立证据证明该 intent 在窗口外时，才通过 `excluded_attempts=[{attempt_id,reason,evidence_id}]` 明确排除。排除对象是持久 intent key，不是跨用户可能重复的客户端 request ID。

## 分步部署与验证

1. 应用 billing-owned migration `132_vendor_reconciliation`（MySQL/Postgres/SQLite 均提供），先更新 billing 证据消费者。
2. 更新 relay，`RELAY_UPSTREAM_EXECUTION_TIME_V1=false` 保留 P1 签名原形状；三种 Compose 模板默认关闭该新增字段。
3. 消费者全部识别新字段后才开启上述 relay 开关；其仅增加执行证据，不改变用户费用、native-v2 收费 allowlist、采购单价或 parser revision。旧消费者会因新字段 digest 不一致而拒收，不能倒置部署顺序。
4. 选定真实、完整、可比的试点账期及专用账号范围，留存供应商原报告、账号映射与覆盖水位；运行只读报告，复核后再显式 import。

本地 `make all`、原生 CLI 编译通过。未交付 linux/amd64 制品：尝试 `CGO_ENABLED=0` 交叉编译时，现有 `platform/database/xdb` 对 `sqlite3.Error/ErrBusy` 的依赖无法编译；后续制品须使用仓库已有的本地 buildx/CGO 流程，不按无 CGO 工具宣称可部署。

本地验收覆盖：零分母/显式零/未知金额、双方缺行、尾边界排除、双轨与终结去重、原生 proof/digest、精确模型/币种、内部成本分离、月费/余额/unknown、归因重组、时间字段关闭时 P1 编码兼容、SSE checkpoint 同一发送时间、WS 两轮分别固定发送时间、不可变版本冲突、更正根/分支拒绝和叶链追加。三种 dialect 使用本地临时容器或 SQLite；容器在结束时删除。CLI 六个虚构场景验证退出码、0600 和拒绝覆盖；竞态、架构及生成检查以验收索引为准。

## P2 后续进展与现场关卡

2026-10-14：以下工程缺口已由 [P2 后续交付](billing-upstream-p2-completion-2026-10-14.md)接通；本节保留 P2.1 当时边界，真实账期仍待独立验收。

typed contract 不可变版本/CAS、有限 rename/default/reject、两个 mapping 的运行时引用、contract/build revision、灰度审计、持久协议事故与 TTL 受控验证仍需接着实现。P3 的费用 API与候选修复尚未开始。

真实代表性账号的完整试点账期、外部凭据真实性、未解释差异和正式日报/权限接入仍需独立验收。本次的合成 fixture 与工具 PASS 不能替代这些关卡，不宣称 P2 整体通过。
