# B1：长上下文与忙闲时段计价契约

日期：2026-10-29（Asia/Shanghai）。关联[阶段计划](next-stage-plan-2026-10-26.md)与[A3 契约](a3-service-tier-contract-2026-10-27.md)。维护者指定首个目标为 **channel/11、`deepseek-flash`**，随后追加忙闲时段计价；明确先完成工程和隔离验收，保持关闭，阈值及真实售价后续确认。

## 1. 计价顺序与范围

沿用 A3 的单一精确来源/上游模型、canonical 五桶、预扣快照、原子配置入口和幂等结算，不新增表达式运行时。一次预扣依次冻结：配置版本与完整价格 → 预扣时刻及当地时段 → 该时段基础/长上下文的逐桶上界。结算从已验证 usage 选择上下文档位，再按上游实际 service tier 选择价格。

输入上下文总量 `I = uncached_input + cache_read + cache_creation_5m + cache_creation_1h`。四桶互斥，缓存只计一次；输出不参与阈值。**I > T 时命中 long，I = T 仍为 base**。long 将整单五桶（含输出）换成长上下文单价，不按超额部分分摊。无法验证完整 usage 时沿用 A3 释放预扣，不用估算输入跨越阈值收费。

请求档位与实际档位继续分开。DeepSeek 未返回 service_tier 时保留 missing，售价按该上下文和时段内的逐桶最高价；不得把请求值或 provider 类型伪装成 actual=default。若该来源不区分这三档，管理员可配置三组相同单价。缺实际档位、缺对应采购价格或缺证据时，采购成本仍为 unknown/unpriced，不等于零实扣。

支持矩阵：

| 入口 | 本期行为 |
| --- | --- |
| legacy OpenAI / DeepSeek Chat HTTP JSON/SSE | 有显式输出上限、自包含纯文本时支持；DeepSeek 的 max_tokens 可作上限，max_completion_tokens 优先，并映射为发送给 DeepSeek 的 max_tokens |
| legacy OpenAI / DeepSeek Responses HTTP JSON/SSE | 自包含纯文本、显式 max_output_tokens 时支持 |
| DeepSeek cache hit/miss | 必须满足 hit+miss=input，且与已出现的 cached_tokens/cache_read 别名一致；冲突、缺半边、非法数值均不结算估计值 |
| 图片、音频、工具、文件、外部会话状态、后台任务、多份生成 | 配置命中后拒绝不满足 bounded 契约的请求 |
| executor、Responses WS、订阅来源、其他 provider | 保持 A3 拒绝边界，不因本项扩大契约试点 |
| SSE 缺 usage、Chat 缺 [DONE]、Responses 缺 completed、取消/断流 | 释放预扣并记录结果；不根据单个内容块推断完成 |

计价入口还拒绝重复顶层键及受控字段的大小写别名（含 Unicode 折叠），避免 JSON map 与 typed parser 解读不同的 tools、输入、模型或 token 上限。Responses 在模型重写前检查原始字节；Chat 启用计价后的实际发送上限直接使用已冻结值，只发送目标 provider 对应的一个限额字段。未启用计价时沿用原兼容行为。

## 2. 原子配置及版本

唯一配置仍是 Admin `ServiceTierPricing`，沿用 revision/CAS、售价和采购权限组合、审计原因、保存后读回。独立 Config 表不控制计价。未保存配置与 enabled=false 均不改变现有收费。

| version | 要求 |
| --- | --- |
| 1 | 既有 A3，不能包含 long_context/time_pricing |
| 2 | 必须包含 long_context，不包含 time_pricing |
| 3 | 必须包含 time_pricing，可选 long_context |

根 `prices` / `upstream_prices` 表示基础价格；存在 time_pricing 时它们是 off_peak 价格。每组都是 default/priority/flex 三档，单位 USD/token；UI 关闭态示例使用 null 提醒维护者填写，不能直接启用。长上下文与时段覆盖均要求完整五桶非负有限数值，显式零价有效；长售价每桶不能小于同一时段的对应基础售价。

`long_context` 字段：

| 字段 | 语义 |
| --- | --- |
| threshold_tokens | 1..2147483647 的单阈值 |
| policy | 固定 whole_request_input_gt_v1 |
| prices | off_peak 或不分时的 long 完整五桶售价 |
| upstream_prices | 可选 long 完整五桶采购估价，缺失不能回退到基础采购价 |

`time_pricing` 字段：

| 字段 | 语义 |
| --- | --- |
| timezone | 显式时区，如 Asia/Shanghai；拒绝依赖宿主机 Local。Go 二进制内置 tzdata |
| peak_windows | 非空数组；每项 weekdays 为 ISO 周一=1..周日=7，start_minute 含、end_minute 不含，0..1440；不得重叠，跨日拆两项 |
| holidays | 必填日期数组（可空），YYYY-MM-DD；这些日期整日 off_peak，不自动把周末调休当作工作日 |
| calendar_valid_through | 必填当地日期，含该日；到期后的新预扣拒绝，已有冻结请求仍可结清 |
| peak_prices / peak_upstream_prices | 忙时基础售价 / 可选采购估价 |
| peak_long_context_prices / peak_long_context_upstream_prices | 忙时 long 售价 / 可选采购估价；有 long_context 时售价必填，没有时禁止配置 |

节假日表由维护者审核并原子保存，系统不联网抓取或猜测官方假日。时段在 **Billing 预扣时** 判定，跨时段结束、异步恢复和执行中改价都使用已冻结价格；每次新尝试重新预扣并选择时段。该时间是本地采购估价依据，不声称证明供应商实际接纳时刻或账单实扣。

2026-10-29 查阅的[DeepSeek 官方价目](https://api-docs.deepseek.com/quick_start/pricing/)列有忙闲时段、周一至周五及中国法定假日排除，未列长上下文阈值加价；因此本项提供可配置的客户售价规则，不宣称 DeepSeek 已有长上下文附加费，也不自动将官网价格或示例窗口投入生产。模型 ID 依[官方 Chat 契约](https://api-docs.deepseek.com/api/create-chat-completion/)使用 `deepseek-flash`。

## 3. 冻结、审计与兼容

- RequestSnapshot 深拷贝完整配置、配置 hash、输入/输出上限；分时额外冻结 PricingTimeBand / PricingTimeUnix，验证时重新核对该时刻的时段。业务使用 Billing 注入时钟，不接收客户端报价或收费时间。
- 预扣覆盖已冻结时段内所有 service tier 与 base/long 的逐桶最大值，输入上界覆盖任一缓存桶，包含逐桶舍入余量。结算超界拒绝，不偷偷提高预扣外金额。
- Ledger 保存 long_context_policy/threshold/tier/input_tokens，以及 pricing_time_band/timezone/unix。PricingSnapshot 保存规则/阈值/命中档位及时间段/时区；每请求输入量与时刻留在 ledger，避免破坏快照去重。
- A3 价格快照继续为 v2；长上下文使用 v3，分时（可组合长上下文）使用 v4。新决策加入 hash；旧 v1/v2 构造和未配置新规则时的配置 digest 保持原样。历史金额不重算。
- 采购价沿相同冻结时段/上下文解析；缺忙时采购价不继承 off_peak，缺 long 采购价不继承 base。账单可见视图清除全部嵌套采购 map，先拷贝对象，不能污染结算快照。
- Billing 146 三库迁移只加 ledger/snapshot 列，不回填推断历史规则；未使用新规则时插入省略新列，兼容迁移前旧 schema。Admin/Identity/Web 透传账单解释，无新增权限或独立配置 owner。
- 版本 2/3 配置会被旧 Billing 拒绝；滚动升级必须先完成全部 Billing，再启用。回滚先关闭新增请求，排空冻结 reservation/异步任务，不能用旧 Billing 处理新格式快照。

## 4. 候选升级与待确认项

先执行 Billing 146 对应数据库迁移，再升级全部 Billing；随后 Relay、Admin、Identity、Web。现有 A3 Log 145 的部署前置条件仍保留，本项不向 Log 伪造 Billing 才能决定的最终档位。Web dist 单独发布。没有新增 Wire 依赖。

工程验收时保持 enabled=false，不修改渠道启用状态/契约灰度/历史账本、不进行付费调用。2026-10-30 按维护者后续要求完成生产关闭态部署，Billing 146 与原有 Log 145 已落库，五服务和 Web 验证通过，见 [生产验收记录](../runbooks/b1-context-time-pricing-acceptance-2026-10-29.md#部署与回滚)。维护者后续确认：阈值与整单售价、两个时段的售价和采购估价、经审核的节假日表和有效期、付费验收预算、收费启用与回滚窗口。
