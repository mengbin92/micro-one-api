# 计费与上游契约 P0 基线

日期：2026-10-13。实现基线：`ab59a042eb1bbfa65f2d54f4e78874d48175f098`。范围由 Neo 确认为「先交付 P0，验收后继续 P1」。

配套方案：[计费准确性](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游契约演进](../design/upstream-contract-evolution-plan-2026-10-13.md)。

**状态：P0 工程与本地回归已交付；生产观测、独立费用核验及 amd64 执行结果尚未验收。** 本记录不批准 P1 收费变更，不补签旧观察窗口，也不改变任何 charge allowlist。

## 本次交付

- 在 HTTP provider 和 adaptor 的实际请求接缝采集转换前的原生 usage；原生 Chat、Messages、Responses、Gemini、旧 typed provider 和 Responses WebSocket 均有采集入口。
- 固定 `native-static-2026-10-13.1` 契约与 `native-shadow-2026-10-13.1` parser。它们是静态影子版本，与 relay → billing 的 v1 wire contract 分开。
- 影子状态为 `verified / missing / invalid / ambiguous / unsupported / unobserved`。状态仅用于观察，不发给现有 billing/channel consumer。
- 按字段存在性合并累计 SSE usage，显式 0 覆盖旧值；终结与 usage 完整性分开。重复 Responses 终结不会增加影子 token。未知计费字段即使被后续转换删除，仍标 unsupported。
- 保存来源、精确 upstream model ID、root/attempt、原生 usage 摘要、字段路径/类型、parser/contract/build revision、必要供应商 ID、endpoint/API version 和终结状态；比较日志另带当前结算 envelope。
- `micro_one_api_relay_native_usage_shadow_total{protocol,status,source_kind,execution}` 使用有限标签；模型、请求 ID 与字段指纹只在日志中。

原生记录的 cost_basis 始终为 unknown、verification_scope 为 none；不存在供应商金额时不制造金额 0。原生 schema verified 只说明固定 parser 的字段与不变量通过，不证明费用、发票或未知合同维度完整。

## 开关与版本

`RELAY_USAGE_SHADOW=true` 开启 relay 影子观测，默认 false。主 Compose、lite、Postgres 配置均已传入该开关。关闭时直接调用原 HTTP client，不包装响应 body；没有第二次上游推理，也没有新财务写入。

生产尚未启用。部署时沿用已有本地跨平台构建流程；实际开启须在固定范围、日志留存与观察窗口明确后进行。撤回只需关闭该开关，不改变在途请求的原结算路径。

Docker 构建接受 `BUILD_REVISION` 并嵌入二进制：部署脚本复用已有 source digest，CI 使用构建 commit，Release 使用实际 checkout 的 tag commit。源码直接构建优先读取 Go VCS revision。取不到版本时明确记录 unknown，不能据此批准新 parser。该 metadata 不从公网 header 获取。

## 现行产品政策冻结描述

这里只记录源码与仓库默认值，**没有读取生产环境变量或配置**。

| 项目 | 现行基线 |
|---|---|
| 用户金额算法 | 五桶各自 `math.Round(tokens × price × ratio × 10000)` 后整数相加；算法不变 |
| canonical 模式 | `BILLING_CANONICAL_USAGE_MODE` 默认 observe；支持 legacy/observe/charge，另有 charge allowlist |
| cache creation 模式 | `BILLING_CACHE_CREATION_MODE` 默认 observe；独立于 canonical 模式 |
| wire producer | `RELAY_CANONICAL_USAGE_PRODUCER` 默认关闭；本次不修改其值或 v1 可接受状态 |
| reserve 快照 | `BILLING_REQUEST_SNAPSHOT_V2` 默认 false；启用后已有售价、倍率、模式与订阅输入被冻结 |
| 异步结算 | `BILLING_ASYNC_ENABLED` 默认 false；持久任务仅覆盖已送达 billing 的事件 |
| missing/estimated | 保留各入口现行估算兜底；本次不新增 evidence_pending 或补证后追扣政策 |
| ambiguous | v1 consumer 现有保守候选路径不变；非流式 orchestrator 的覆盖问题只在本阶段留证 |
| 取消/断流/总超时 | 沿用现有 release、不 commit；供应商是否有费用继续 unknown |
| final Commit RPC 失败 | 原送达缺口仍存在；P0 不提供 outbox、inbox、lease 或 exactly-once 保证 |

v2 尚未冻结异常政策、采购费率或 parser/算法 revision。本次影子字段不倒填已有 reservation、ledger 或历史快照。

## 路径覆盖与限制

| 路径 | P0 观测入口 | 当前边界 |
|---|---|---|
| orchestrator 非流式/流式 | 原 provider/adaptor HTTP response body | 可比较当前最终 envelope，保留原收费输入 |
| legacy typed Chat | provider 解析前 body | 可观察 Gemini/Anthropic typed projection 前字段 |
| Messages/API-key/订阅 adaptor | `ConvertResponse` / `ConvertStreamResponse` 前 body | 不改变客户端转换输出或采购来源 |
| Responses create/stored route | HTTP 原生 body 与 SSE | 仍保留原 endpoint fallback 行为；P1 才建立独立执行事件 |
| Responses WS | 上游 frame、逐 response ID 影子快照 | 原日志 request ID 是连接/attempt 关联；逐用户 reservation 对应关系仍需 P1 证据协议 |
| raw/one-api/embedding/原生多模态 | provider 投影前 body | 未安装计量契约的协议或维度保持 unsupported，不编造五桶 |
| 模型发现、健康探测 | 现有 `/models` 路径列入基线 | 不把列表检查当成推理 canary；独立 purpose、预算和推理探测属于 P1 |

仅保存 usage 子树；其中字符串值统一脱敏，不保存对话或 Authorization/OAuth token。usage digest 绑定解码后的原生 usage，fingerprint 只含精确字段路径与类型，包含关系由固定 parser 检查，不能通过来源名称覆盖。

每个 JSON/SSE frame 最大观察 1 MiB，usage 最大 32 KiB，单流保留最多 32 个计量/终结事件；WS 影子重复抑制保留最近 32 个已完成 response ID，P1 才改为持久执行证据；超限记录 unobserved/丢弃事件数量，响应仍照常转发。深度、节点数和字段长度也有上限。日志遵守已有受控运维访问与留存；这不是 P1 的持久执行证据库或可恢复 outbox，进程在采集前崩溃仍会缺证据。

## 已复现的差异与重放范围

| 样本 | 影子结果 | 当前结算基线 |
|---|---|---|
| OpenAI input 1000/cache 600/output 100 | `[400,600,0,0,100]` | 合法 canonical 五桶 |
| Anthropic input 400/cache 600/output 100 | 同一五桶 | input 不再减 cache |
| 合法 input/output 均为 0 | verified 的零五桶 | 现行非流式路径替换为 estimated；P1 修正 |
| cached 大于 input | ambiguous，无 canonical | 现行非流式路径替换为 estimated；P1 修正 |
| null、字符串、负数、小数、溢出、重复键 | invalid，保留原因 | 不修改现行 parser 的输出 |
| Gemini 新增 thoughtsTokenCount | unsupported，保留字段指纹 | typed 转换删除该字段后，现行 parser 仍可给出 verified；不能证明完整原生用量 |
| Messages 累计 output 2→5→0 | 最终 output 0 | 影子快照按 presence 覆盖，终结不额外计一次 |
| 同一 Responses completed 重复 | 同一五桶 | 影子不相加；现有账务去重逻辑不变 |

上述差异只证明离线构造样本与当前代码行为，不能据此宣布已发生线上错扣。Gemini thinking 的输出归属尚未进入收费 parser，新字段先作为未覆盖契约维度。

沿用 history-audit 的独立金额重放：双轨 consume 先按请求合并，fixture 的 110 quota 收费对应 94 quota 可重放金额、差异 -16；ambiguous fixture 只输出 94/114 两个候选，缺价/缺证据保留 candidate/unknown，不反推当今价格。该 fixture 不是供应商账单。完整 v2 冻结输入重放与请求途中改价测试已通过；存量 ratio/legacy 或缺原始证据行不承诺精确重算。

## 验证与证据

[固定 native fixtures](../../domain/upstream/evidence/testdata/native-usage.json)包含 24 组计量/状态样本，另有 malformed、重复键、SSE 分块、终结、累计/零值、断流、HTTP 字节保真、拒绝、并发 Close、Gemini 投影及 WS 重复终结测试。

- [聚焦原始测试输出](evidence/native-usage-p0-tests-2026-10-13.jsonl)：native、P0 差异、快照及 history-audit 用例。
- [history-audit 离线差异报告](evidence/native-usage-p0-history-audit-2026-10-13.json)：来自固定 ledger fixture，包含 verified/candidate/unknown。
- [相关包完整回归汇总](evidence/native-usage-p0-regression-2026-10-13.json)：9 个相关包通过。
- [race 输出](evidence/native-usage-p0-race-2026-10-13.txt)：evidence/provider/server/jsonx 通过。
- [基线索引](evidence/native-usage-p0-baseline-2026-10-13.json)：源码摘要、fixture 摘要、命令、架构与未完成关卡。

部署脚本的 shell 语法、修改的 workflow YAML、新增 Compose 环境变量及分层检查通过。本机旧 Ruby YAML parser 对原始和修改后的 Compose 主文件均在既有第 184 行失败，不能据此宣称已完成 Compose 全量解析或镜像运行验证。

本地环境为 Go 1.27.2、darwin/arm64。尝试执行 amd64 测试得到 `bad CPU type in executable`，机器没有可用的 amd64 执行支持；不能把成功交叉编译当成 amd64 回放通过。现有 Ubuntu CI 会执行相同 golden fixture 和 billing 算法测试，须归档其实际结果后才满足跨架构关卡。

## P0 验收后再推进 P1

工程交付不替代生产观察。下一阶段前需固定试点来源/协议、现行价格与开关、日志保留和完整窗口，取得真实 shadow 差异、同一一致视图的资金基线，并保存供应商可见范围；缺失证据继续 unknown。P0 本地回归和旧 48h/七天观察文件不能互相替代。

P1 再实现 consumer/schema/快照能力协商、nil/zero/ambiguous 传播修正、原生 parser 受控路径、独立 attempt/purpose 与采购费率、relay outbox/billing inbox、digest 冲突、lease/sweeper 竞争与补证期限，以及库存/canary/预算。P2/P3 的契约发布、外部报告和自动修复亦未在本次启用。
