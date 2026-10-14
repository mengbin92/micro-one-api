# DeepSeek 官方 deepseek-flash：P2 受控实测

日期：2026-10-14（Asia/Shanghai）。用户提供测试凭据并指定精确模型 `deepseek-flash`。仅请求官方 `api.deepseek.com`，没有创建生产渠道、修改价格/收费名单、导入虚构账单或部署。

后续扩大核验：六次合计与官方费用导出精确匹配，受控余额扣减也在显示精度内相符；见[扩大核验报告](deepseek-balance-and-channel1-2026-10-14.md)。以下保留首次四次调用当时的证据和边界。

## 现场结果

GET `/models` 与 `/user/balance` 均 HTTP 200，模型列表确认 `deepseek-flash`，余额可用。随后顺序执行四次真实付费推理，关闭 thinking。前三次最多 16 输出 token；第三次工具参数因 length 截断，保留其失败能力结果，追加一次最多 64 输出 token 的独立工具验证；工具仅返回本地 `p2_probe` 调用声明，不执行任何外部工具。

| 场景 | HTTP | 输入/输出 token | 终结及能力 | 规范五桶（候选回放） |
|---|---|---|---|---|
| Chat JSON 文本 | 200 | 8 / 1 | 完整，文本能力成功 | 8 / 0 / 0 / 0 / 1 |
| Chat SSE 文本，include_usage=true | 200 | 8 / 1 | 非终结 usage=null，末块含 usage，随后 [DONE] | 8 / 0 / 0 / 0 / 1 |
| Chat JSON 工具调用，max_tokens=16 | 200 | 282 / 16 | length 截断；仅确认工具名称，参数能力未通过；usage 完整 | 282 / 0 / 0 / 0 / 16 |
| Chat JSON 工具调用，max_tokens=64 | 200 | 282 / 34 | tool_calls 终结，参数 JSON 为 {"ok":true}，完整通过 | 154 / 128 / 0 / 0 / 34 |

合计 **580 输入 + 52 输出 = 632 token**，其中缓存命中 128、未命中 452。工具调用输入 282 超出现有 P1 canary 的 128 输入上界，追加调用的 max_tokens=64 也超出现有 16 输出预算，这次是独立手工受控实测，不能据此声称该样例满足 monitor 预算门禁；进入 monitor 前需要核对固定工具输入与预授权预算，不自动扩预算。

证据：[脱敏实测索引](evidence/deepseek-official-pilot-2026-10-14/live-evidence.json)、[原生回放 fixture](evidence/deepseek-official-pilot-2026-10-14/redacted-native-fixtures.json)、[候选回放](evidence/deepseek-official-pilot-2026-10-14/candidate-replay.txt)、[相关回归](evidence/deepseek-official-pilot-2026-10-14/regression.txt)、[竞态](evidence/deepseek-official-pilot-2026-10-14/race.txt)。生成内容已移除，供应商 ID 使用 hash/fixture ID，报告没有 API key 或账户绝对余额。实际余额快照保留在本地私有 `/tmp/micro-one-api-deepseek-pilot-20261014`（目录 0700，文件 0600），不进入仓库。

## 官方契约与 parser 缺口

[官方 Chat API](https://api-docs.deepseek.com/api/create-chat-completion/)定义：prompt_cache_hit_tokens 与 prompt_cache_miss_tokens 合计为 prompt_tokens，prompt_tokens_details.cached_tokens 与缓存 hit 相同；include_usage=true 的非终结 chunk 使用 usage=null，终结块给出完整 usage。

实测最小回放将现有严格 parser 判为 `unsupported/unknown_usage_field`；SSE 的 usage=null 则锁存 `invalid/usage_not_object`。三个假设按证据区分：

1. 两个 flat cache 字段未在现行原生契约内：删除已定义等价 alias 的投影后核心五桶能验证，成立。
2. flat hit/miss 与标准 cached 字段冲突：前三次 hit=0、miss=prompt、标准 cached=0；第四次 hit=128、miss=154、prompt=282、标准 cached=128，均无冲突；另以不一致 fixture 确认候选拒绝。
3. SSE 累计值叠加或终结重放：原计数不是本次根因；非终结 null 才是流式完整性缺口，候选对重复完整快照保留覆盖语义。

新增 **offline candidate** `ParseDeepSeekCandidate` / `NewDeepSeekCandidateTracker`：严格核对整数、presence、非负/overflow、hit+miss=prompt、cached alias 一致、total=prompt+completion；只投影经官方定义的两个 cache aliases，返回原始 usage/fingerprint，未知其他维度仍 unsupported。只有非终结、明确 chat.completion.chunk 的 usage=null 才视为未附带 usage，终结 null 仍 invalid。帧/usage 大小限制继续 fail closed，空流和超限观测保留候选 revision。没有第六个 reasoning 桶。

候选 revision 为 `candidate-deepseek-chat-2026-10-14.1` / `candidate-deepseek-official-cache-alias-2026-10-14.1`。**未注册到 Parse、execution.Freeze、billing final verifier 或 contract compiler 的收费 profile**；既有生产 parser/开关/算法保持原样。独立 regression 先 red 后 green；实际四份响应只离线回放，不重复推理；fixture 回放验证 usage 五桶，不用已脱敏的工具内容重新证明参数能力。完整工具参数能力由原始实测中的 JSON 解码和 {"ok":true} 精确校验确认。

## 金额与核验范围

调用发生于北京时间 2026-10-14 07:26 与 07:40，处于[官方人民币定价](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)的空闲时段。保存本次价目：每百万未缓存输入 1 元、缓存命中 0.02 元、输出 4 元。

按原币 decimal 计算：JSON 文本 `0.000012 CNY`，SSE 文本 `0.000012 CNY`，截断工具 `0.000346 CNY`，完整工具（含缓存命中）`0.00029256 CNY`，合计 **0.00066256 CNY tariff_estimate**。价格不是响应中逐笔供应商实扣，不写成 invoice/provider charge。

[余额接口](https://api-docs.deepseek.com/api/get-user-balance/)返回账户当前 CNY 总余额、赠金与充值余额；本次实际精度为两位小数，前三次调用结束后多次读取仍显示总变化 `0.00 CNY`。这些余额快照均早于第四次调用，未覆盖追加请求；前三次估价 0.000370 元及四次合计估价均低于 0.01 元显示粒度，不能据此证明免费、漏扣或匹配成功。账户是否专用、有无其他 key 流量、充值/赠金变化及入账延迟未独立确认；verification_scope 保持 none，外部成本状态为 `unknown_below_balance_display_precision`。

本次没有制造 vendor CSV 的金额 0，也没有把这段探测区间当作完整供应商账期。官方公开文档确认的 key 能力为推理、模型和余额；费用/逐请求扣费明细还需要控制台原始导出或实际可用的独立报告权限，不能推测一个费用 API。

## P2 关卡状态

通过：真实官方 source/model 可用、JSON/SSE 完整响应、工具完整参数与截断负例、非零缓存命中原生 usage、特定契约缺口定位、候选同响应回放及金额估价口径。

仍待：其他目标协议覆盖、已审查并安装的收费 profile、来源/真实账号/报告范围映射、完整真实账期/实扣费用凭据、平台预算 canary、生产 30 分钟/100 完整样本与多实例发布/恢复收敛。本次不是 P2 全量出阶段批准。

## 可重复验证

```sh
go test ./domain/upstream/evidence -run '^TestDeepSeekCandidate' -count=1 -v
go test ./domain/upstream/evidence ./domain/execution ./app/billing/internal/biz ./internal/server -count=1
go test -race ./domain/upstream/evidence -count=1
go vet ./domain/upstream/evidence
```

以上均通过。候选大小限制测试已先复现超限响应被错误验证，再修复并通过；负例还覆盖 alias 冲突、未知维度、非整数/负数/overflow、重复键、终结 null 和累计快照重放。回放及回归只读取本地 fixture，不调用付费 API。
