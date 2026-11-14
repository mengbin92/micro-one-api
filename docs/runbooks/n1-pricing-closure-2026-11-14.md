# N1：计价扩展条件闭环

> 2026-11-14，北京时间。状态：**4/5 已完成；跨时段实测已排队**。关联 [N1 核账](n1-pricing-readonly-2026-11-08.md)、[N1 分时验收](n1-off-peak-paid-2026-11-08.md)。机器证据见 [closure JSON](evidence/n1-pricing-closure-2026-11-14.json)。

## 1. 已完成

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| ✅ 兜底范围治理 | channel 1 从 catch-all 收口为 `restrict_models=1`；当前所有启用渠道均要求显式 abilities。未注册模型实测返回 503 `no available channel`，未创建 reservation 或账本。 | 审计 `channel.channel.update success`，reason 为 N1 fallback governance；abilities 与渠道回读在 JSON 证据中 |
| ✅ 计价路由绑定 | 为预配置的 channel 11 / `deepseek-flash` 建立显式 ability；/v1/chat/completions 已真实命中 channel 11，账本快照版本 4。 | `channel.channel.update success`；账本 `channel_id=11`、`pricing_config_hash=0a2ec72c...` |
| ✅ 其他档位合同核对 | 上游接受 `priority` / `flex` 请求参数，但响应均返回 `service_tier=null`。平台分别记录 `missing`，按 `actual_or_max_v1` 使用 `max`。 | 两次直连探针与两条平台账本；当前上游没有可验证的真实档位切换 |
| ✅ 长上下文合同核对 | 上游模型声明 1M context、384K max output，公开费率只有一个输入/输出表，无长上下文加价行。经平台发送 190,610 input tokens 成功，账本仍使用基础/闲时快照，`long_context_*` 全为空或 0。 | 上游模型合同、文档哈希、真实请求 usage、平台账本和价格快照 |
| ✅ 上游最终实扣 | 受控请求后上游余额从 43.66 CNY 降至 43.47 CNY，实际钱包减少 0.19 CNY；平台三条受控账本合计 1,908 quota，即 0.1908 CNY。差额 0.0008 CNY 来自上游余额两位小数展示与平台逐请求最低扣量。 | 结算 watcher 检出时间、余额响应和 channel 11 全部受控账本已写入 JSON 证据 |
| ⏳ 跨时段完成 | 已安排在 2026-11-17 08:59:55 发起的长请求，要求请求完成时间跨入忙时，以验证预扣时刻快照。代码层边界冻结与重启恢复测试已通过。 | 后台执行进程已启动；完成后回填请求时间、账本和上游 usage |

## 2. 当前结论

- **兜底治理**：完成。后续不得通过无显式 ability 的模型绕过价格覆盖。
- **其他档位**：本轮上游合同不支持真实 `default` / `priority` / `flex` 切换，登记为 N/A，不伪造切换成功。平台继续使用 `actual_or_max_v1`，实际档位缺失时按最大档保费。
- **长上下文**：上游采用 1M 统一费率，没有独立 long-context 加价档；平台不配置 `long_context`，190K 输入按基础档核验通过，登记为 N/A/合同核对通过。
- **最终实扣**：完成。受控样本的平台账本与上游钱包实际减少已核对。
- **跨时段**：仍待 2026-11-17 09:00 前后的定时请求结果，未完成。

## 3. 边界

- 本轮只操作了生产渠道配置和短生命周期测试路由；临时 API Key 将在收口时删除。
- 渠道 1 保持原有显式模型与 abilities，不删除历史价格键；全局模型映射中未被 ability 覆盖的入口现已被拒绝，不再兜底。
- 本轮以受控请求前后的钱包实际变动作为最终实扣证据；余额两位小数展示不等同于逐请求分配或税务发票。
