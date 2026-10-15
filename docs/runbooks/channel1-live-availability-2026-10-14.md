# 渠道 1 线上可用性复核

日期：2026-10-14，Asia/Shanghai。结论：**渠道 1 当前并非整体不可用；最新 owner 健康状态已恢复 healthy，用户确认对外启用的 5 个模型通过在线调试均能正常返回。** 初始 unavailable、阶段性模型失败、模型目录禁用与最终状态分别保留，不能混为当前全渠道故障。

## 实测范围与结果

08:03–08:07 从生产 Linux 主机读取渠道 1 的实际供应商配置与 owner 持有的凭据，直接请求 `gpunexus.chengfangtech.com`。探测前后 revision 均为 8；随机/固定极短测试提示，不发送用户业务内容。并发最多 2，首次逐个测试全部 8 个配置模型，再测试 qwen 流式；另对 glm 的失败做一次独立确认，共 10 个推理 attempt。每次请求 max_tokens=16，无自动重试；供应商实际费用未取得，保持 unknown，不能把失败请求记为费用零。

| 模型 | 目录对外状态 | 本次直接测试 | 后续确认 |
|---|---|---|---|
| Kimi-K3 | 启用 | HTTP 200，length，16 token 限制下无可见正文 | 用户在线调试正常 |
| DeepSeek-V4-Flash-0731 | 启用 | HTTP 200，stop，有正文 | 用户在线调试正常；实际平台选路命中渠道 1 |
| DeepSeek-V4-Pro-0813 | 启用 | HTTP 200，length，16 token 限制下无可见正文 | 用户在线调试正常 |
| qwen3.8-flash | 启用 | HTTP 200，stop，有正文 | 用户在线调试正常 |
| glm-5.3 | 启用 | 两次 HTTP 500，`service do not have healthy model glm-5.3 now` | 用户随后在线调试正常；先前 500 不作为持续不可用结论 |
| qwen3.7-max | 禁用 | JSON/SSE 均 HTTP 200、stop、有正文，SSE 收到 [DONE] | 平台选路 NotFound，与目录禁用一致 |
| MiniMax-M3 | 禁用 | HTTP 200，length | 不计入对外启用的 5 个模型 |
| glm-5.3-flash | 禁用 | HTTP 200，length | 不计入对外启用的 5 个模型 |

200/length 只确认请求已被供应商接受，不能把无正文的截断响应当作完整回答验收。qwen JSON 的 completion=114、stream=178，qwen3.8 的 completion=23 均超出请求 max_tokens=16；这些 usage/预算差异仍是独立契约问题，不代表端点断连。未改价、解锁收费或修改模型目录状态。

GET `/api/v1/models` 两次 HTTP 200，但 Content-Type=text/html，489 字节，响应不是合法 JSON。仅依据该端点状态码的手动连接测试不能证明模型列表/推理健康。证据：[实际请求与 usage](evidence/channel1-live-availability-2026-10-14/upstream-probes.json)、[glm 与 models 独立确认](evidence/channel1-live-availability-2026-10-14/confirmed-model-and-inventory.json)。

## 平台与状态时间线

初始 owner 标记 unavailable、连续失败 3，冷却时间已过。08:06 通过现有 relay 服务身份调用线上 channel-service：GetChannel 成功，仍为 unavailable；SelectChannel 对 DeepSeek Flash 返回渠道 1 和半开探测租约。因此旧健康标记并不意味着所有选路被永久阻断。此次仅测试选路，没有触发 relay 推理/结算；未转发的临时租约用 neutral 观测取消，服务确认 accepted，不提交虚构成功或持久健康恢复。

同次 qwen3.7 选路 NotFound；生产模型目录该模型 status=0，映射本身启用。没有发现渠道 1 的持久 usage-semantic block 行，不能将 NotFound 归因于不存在的 usage 隔离。证据：[平台选路](evidence/channel1-live-availability-2026-10-14/platform-routing.json)、[能力与隔离行](evidence/channel1-live-availability-2026-10-14/routing-facts.txt)。

随后生产 owner 记录变为 healthy、连续失败 0、revision 仍为 8；最终再次从线上服务读取并验证 DeepSeek Flash 选路。此状态变化不是本次直接供应商诊断或 neutral 租约取消写入的；没有直接改库，也没有部署代码。证据：[目录映射及 healthy 记录](evidence/channel1-live-availability-2026-10-14/model-mappings.txt)、[最终服务状态](evidence/channel1-live-availability-2026-10-14/final-owner-state.json)。用户同时明确确认“通过在线调试，对外可用的 5 个模型都可以正常返回”；该项为用户实测证据，与本次直接探测分开记录。

当前结论适用于此次现场窗口，不能由短样本宣称长期 SLA 或整体 P2 验收完成。前一轮的[版本冲突修复](deepseek-balance-and-channel1-2026-10-14.md)仍为独立代码变更；本次没有用健康状态恢复替代该修复的发布。
