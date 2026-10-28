# A3：服务档位计价 MVP 冻结契约

2026-10-27（Asia/Shanghai）。关联[当前排期](next-stage-plan-2026-10-26.md)与[实施验收](../runbooks/a3-service-tier-acceptance-2026-10-27.md)。

## 1. 来源、范围与默认策略

复用已有 OpenAI channel adapter，首期只允许一个精确的 `channel:<source_id>:<upstream_model>`。候选模型为 `gpt-5.1`；配置不内置任何真实渠道 ID、供应商价格或付费凭据，实际渠道可用性、模型生命周期和项目档位权限须在收费启用前核对。隔离测试使用替身模型名与合成价格，不是供应商价格证据。

依据 2026-10-27 查阅的 [OpenAI Responses API](https://developers.openai.com/api/reference/resources/responses/methods/create) 与 [Chat Completions API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)：省略 `service_tier` 相当于 `auto`，由项目配置解析；响应中的实际档位可能不同于请求。MVP 仅接受省略、`auto`、`default`、`priority`、`flex`，其他请求值明确拒绝。`fast` 等新枚举不隐式映射；扩展需另行更新契约。不自动升档、不强制 Priority、不在 Flex 失败后偷偷改请求档位。

默认 `ServiceTierPricing` 为 `{"version":1,"enabled":false}`。关闭或未命中来源时，省略/auto/default 继续旧价格策略；显式 priority/flex 拒绝。启用并命中来源时，所有请求（包括省略档位）都执行本契约，不能回退旧计价。

## 2. 原子配置与权限

Admin-owned `system_options.ServiceTierPricing` 是唯一生效来源。一个 JSON 对象包含 `version`、`enabled`、`source_kind`、`source_id`、`model`、`prices` 和可选 `upstream_prices`。只允许 `source_kind=channel`、正整数 ID 和一个精确模型名。`prices` 必须同时提供 default/priority/flex；可选的采购估价 `upstream_prices` 一旦提供也必须完整提供三档。

每档使用现有五桶的 **USD/token** 单价：`input_price`、`output_price`、`cache_read_price` 必填，`cache_creation_5m_price`、`cache_creation_1h_price` 可选。数值必须有限且非负；显式零价格有效，不将遗漏或 null 当成免费。成功消费保留既有最低收费 1 定点单位（USD 0.0001），包括全零单价或已验证全零 usage；采购估价不使用该用户最低额。第一版不加入表达式、时间段、长上下文、图片/音频或工具收费。

整个对象通过带 `expected_revisions` 的单事务批量写入口保存；并发版本冲突要求重新读取，不能覆盖别人的价格。因为对象同时可含售价与采购估价，读取需要全局售价与采购读取权限，写入需要两类全局写权限及既有 system option 写权限。现有按模型行过滤不足以安全披露整个对象，因此不能代替全局范围。Config-service 的独立 `configs` 表不是生效来源，不能用同名配置冒充成功设置价格。

## 3. 入口矩阵与成本上界

| 入口 | 命中启用来源 | 未命中或关闭 |
| --- | --- | --- |
| legacy OpenAI Chat Completions HTTP JSON/SSE | 支持有界纯文本请求 | 默认策略兼容；priority/flex 拒绝 |
| legacy OpenAI Responses HTTP JSON/SSE | 支持有界、自包含纯文本请求 | 默认策略兼容；priority/flex 拒绝 |
| executor / 原生执行证据 v2 | 拒绝档位计价，不混用两套结算证据 | 保持原策略；priority/flex 拒绝 |
| Responses WS | 拒绝档位计价，包括省略档位 | 保持原策略；priority/flex 拒绝 |
| 订阅来源、其他 provider / 协议 | 不在首期来源范围；priority/flex 拒绝 | 保持原策略 |
| 图片、音频、文件、工具、外部会话/previous_response_id、后台任务、多份生成 | 不支持有界档位计价，转发前拒绝 | 继续原有入口能力与默认策略 |

请求需明确限制输出：Chat 使用 `max_completion_tokens`，Responses 使用 `max_output_tokens`。Relay 只为能从提交正文界定的纯文本形状给出保守输入 token 上界；隐藏上下文和远程媒体不以字节数猜测价格。Billing independently 检查协议、契约版本及正数输入/输出上界。

预扣使用三档价格**逐桶最大值**；输入上界乘所有输入桶最高单价，输出上界乘最高输出价，再应用冻结的组倍率并保留分桶取整余量。这覆盖实际档位回落、混合缓存桶和未知档位策略。实际用量超过冻结 token/金额边界时拒绝资金结算并留下可见错误，不静默向钱包追加无界收费。

Relay 在每次重试选定来源后重新向 Billing 预扣。跨来源/模型重试不能继承上一来源的档位价格；同一 request ID 的重放须与已持久化的来源、模型、请求档位及上界一致。未覆盖入口以 unsupported 协议传递给 owner，避免省略字段时绕开范围检查。

## 4. 冻结、结算与解释

预扣时把配置、配置摘要、请求档位、协议/上界、策略版本及金额上界写入既有 `RequestSnapshot`。`BILLING_REQUEST_SNAPSHOT_V2` 未开启时，启用来源的所有请求拒绝。已接纳请求继续使用冻结配置，即使执行中关闭计价、改组倍率、修改采购价或切换配置来源，也不重算。

请求与实际档位独立存储。HTTP 从上游响应顶层提取；Responses SSE 从响应事件的 response 对象提取；Chat SSE 汇总上游 chunk。缺失保留空值，不用请求值填补；32 字节以内的未来未知枚举保留原始值，超长值清空并标为未知，多次矛盾报告标记冲突。冲突或超长证据不能因后续 chunk 出现已知值而恢复为可定价实际档位。`reported` 且为已配置实际档位时按该档位计价；缺失、未知、冲突按 `actual_or_max_v1` 冻结逐桶最高价结算，`billed_service_tier=max`，同时保留实际未知这一事实。

账本记录 `requested_service_tier`、`actual_service_tier`、`service_tier_status`、`billed_service_tier`、`service_tier_policy`、`service_tier_config_hash`；五桶价格快照 v2 另包含选中档位、策略和配置摘要。同一实际价格但不同策略配置不混为同一证据。旧 v1 快照、旧账单和原金额保持可读，空字段表示历史无证据，不回填推断。

采购价格只形成 tariff estimate，与供应商账单实扣分别建模。采购估价需冻结采购价、已知实际上游档位及可验证 canonical usage，所有实际用到的正数桶都须有对应价格；缺任一条件时保持未定价，不能把零金额解释为免费或把售价反推为实扣。显式零采购价与未定价分别为 `priced/0`、`unpriced/0`。

结算复用现有 reservation CAS、同事务快照/账本写入及 dedupe key；重复消费返回第一次已持久化金额，不能因重放携带不同实际档位而改账。

## 5. 流异常与兼容窗口

档位计价只使用已验证的完整 usage：请求/响应的 input 与 output 必须明确出现，显式零与缺字段分别处理；不能把估算值补进上游实际零值。JSON 缺/无效 usage、SSE 缺完整 usage 或终态、取消和断流均释放预扣，不产生 consume 账本，并保留零 quota 日志、上游档位及 `released/<reason>` 结果（释放失败记 `release_pending/<reason>`）。档位确定本身不能证明 token 用量，也不能把完整预扣金额冒充供应商用量。已验证 usage 的结算采用冻结档位策略；实际档位仍可未知。

发布顺序：三库迁移 144（Billing）、145（Log）→ 全部 Billing/Log → Relay → Admin/Identity/Web。Relay 依赖 Billing 的 `service_tier_pricing_version=1` 能力与 `service_tier_contract_version=1` 预扣回显，旧 Billing 不得默默忽略字段。默认关闭时先完成兼容验收，再由维护者单独决定精确来源、真实付费探测、收费启用及扩面。MVP 不扩大既有 1% 契约试点、不恢复供应商监控或其他暂停任务。

常规回滚先关闭配置并排空新请求，等待既有档位 reservation/异步任务按冻结快照结清；保留新 Billing 的快照解释能力。不能让旧 Billing 接管未完成的档位 reservation。保留新增字段和历史证据，不以回滚为由重算旧金额。
