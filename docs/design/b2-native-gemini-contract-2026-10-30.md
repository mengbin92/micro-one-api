# B2 原生 Gemini 入站冻结契约

> 2026-10-30（Asia/Shanghai）。对应[下一阶段计划 B2](next-stage-plan-2026-10-26.md)。维护者确认没有指定客户端、模型或测试渠道；首版以官方 Gemini REST / Google Gen AI SDK 的 `v1beta` 文本与函数调用报文为基线，工程验收使用隔离来源，不推定任何生产模型已支持或已完成付费验证。

## 1. 入口与调用链

- `POST /v1beta/models/{model}:generateContent`：原生 JSON。
- `POST /v1beta/models/{model}:streamGenerateContent?alt=sse`：原生 SSE；省略 `alt` 时仍只提供 SSE，不提供 JSON 数组流。
- 客户端使用平台 Token，接受 `x-goog-api-key`、`Authorization: Bearer ...`、`?key=`，兼容 `x-api-key`。同时提供不同凭据、重复凭据 header/query 时拒绝。原生凭据在共享中间件前转换为 Bearer，query 中的密钥移除；上游使用渠道自己的 `x-goog-api-key`，不透传客户端密钥。
- URL 模型名进入既有 Identity 授权、模型白名单、Channel 路由、模型映射与重试流程。模型映射只改变上游 URL，不在 Gemini body 中插入 OpenAI `model` 字段。模型名限 1–128 个 ASCII 字母/数字/点/下划线/短横线，首字符为字母或数字；路径、转义路径和扩展上下文后缀不支持。
- 原生调用固定使用现有 staged executor，与旧入口的灰度开关无关；预扣/结算仍由 Billing owner 执行，日志仍由 Log owner 保存。每次重试保留既有独立 reservation 和 attempt 身份；响应已完成、协议解析失败、客户端已收到流或结算失败后不会重放上游。
- 选中的来源必须为 Gemini API-key 渠道（type 3），不支持订阅账号或转换到其他厂商。渠道 Base URL 配置为 API 根地址，可带代理前缀，例如 `https://gateway.example/google`；适配器追加 `/v1beta/models/...`。
- 不新增 proto、Wire provider 或数据库迁移。部署候选只需要更新 Relay；Billing/Log 使用已有 canonical v1、快照与幂等能力。

客户端示例（`RELAY_TOKEN` 是本平台 Token，模型须在平台已有路由与普通价格配置中）：

```sh
curl "$RELAY_BASE_URL/v1beta/models/$MODEL:generateContent" \
  -H "x-goog-api-key: $RELAY_TOKEN" -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":128}}'

curl -N "$RELAY_BASE_URL/v1beta/models/$MODEL:streamGenerateContent?alt=sse" \
  -H "x-goog-api-key: $RELAY_TOKEN" -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":128}}'
```

使用 Google Gen AI SDK 时选择 Gemini API backend，base URL 指向 Relay 根地址、API version 选择 `v1beta`、API key 填本平台 Token；具体语言和 SDK 版本留待外部验收，不启用 SDK 的服务端工具/媒体或隐藏状态功能。

## 2. 支持和拒绝矩阵

| 能力 | B2 行为 |
| --- | --- |
| `contents` / `systemInstruction` 文本 | 支持单轮/多轮；校验 role、非空 parts、字段类型 |
| 函数声明、调用和结果 | 支持 `functionDeclarations`、`functionCall`、`functionResponse`、`toolConfig.functionCallingConfig`；保留 id、参数、结果及 thought signature；工具实际执行仍由客户端负责 |
| 输出参数 | 支持 temperature、topP、topK、stopSequences、maxOutputTokens；只允许 candidateCount=1 |
| 结构化文本 | 支持 text/plain、application/json 及 responseSchema / responseJsonSchema；仍以文本 token 结算 |
| thinking | 透传 thinkingBudget / thinkingLevel / includeThoughts；保留 thought/signature；计量规则见下节 |
| safetySettings | 透传类别/阈值；原生安全阻断响应保留；存在有效 usage 时按实际 usage 结算 |
| protobuf JSON 字段拼写 | 请求支持 camelCase 和 snake_case；同一字段的两个别名同时出现拒绝；用户参数/schema 的键不改名 |
| 图片/音频/视频/PDF/文件 | 入站 inlineData、fileData、responseModalities、speechConfig 等返回 400；上游意外返回非文本内容或非 TEXT 用量时 JSON 返回 502，SSE 失败并释放预扣 |
| 搜索、代码执行、URL context 等服务端工具 | 请求 400；非零 toolUsePromptTokenCount 或未知计量字段拒绝结算 |
| cachedContent、continuationToken、隐藏会话 | 请求 400；允许供应商自动缓存的实际 cachedContentTokenCount |
| serviceTier / service_tier | 请求 400；A3/B1 服务档位、长上下文、忙闲计价启用的来源继续执行 Billing 既有入口准入，不静默套用这些扩展价格 |
| `Idempotency-Key` 响应缓存 | 在共享中间件前返回 400，防止不同原生凭据共用缓存或不完整 SSE 被缓存；Billing reservation/commit 幂等仍有效 |
| durable execution / native usage v2 试点 | 命中既有执行交付 allowlist 时，在预扣前返回 501；B2 不改变冻结的 v2 parser 或扩大历史试点 |
| `/v1`、Vertex/Bedrock、Live/WS、批任务、countTokens、模型/文件管理 | 不属于本次原生生成接口；`/v1beta/models/` 下其他操作返回 501，非 POST 返回 405 |

未知请求字段、重复 JSON 键、null 字段和歧义别名均失败关闭；不会悄悄丢掉特性再发送。JSON 请求、响应及单个 SSE 事件上限 4 MiB；嵌套/令牌数沿用 `jsonx.ValidateUnique` 的 32 层 / 200000 token 预算。

## 3. 预扣与实际用量

预扣前给未指定输出限额的请求写入 `maxOutputTokens=8192`，显式限额只接受 1–65536。文本输入预估为提交 UTF-8 字节数 × 2 + 4096，并将输出限额纳入预扣；没有隐藏上下文或远程媒体输入。具体金额沿用已有普通模型价格与快照；这些数字为工程上界，不是生产模型价格。

只读取响应根部 `usageMetadata`，不递归搜索生成文本或函数参数。映射为互斥五桶：

```
uncached_input = promptTokenCount - cachedContentTokenCount
cache_read     = cachedContentTokenCount
cache_5m/1h    = 0
output         = candidatesTokenCount + thoughtsTokenCount
billable_total = promptTokenCount + candidatesTokenCount + thoughtsTokenCount
```

`promptTokenCount` / `totalTokenCount` 必须存在，其他已知计数省略按零处理；全部计数须为非负 int32，缓存不能超过 prompt，total 必须与上式一致。真实零用量保留零，不替换成预估。输入缓存采用已有 `openai_subset` 数学语义，同时将 `SourceProtocol=gemini` 与原生字段形状写入 canonical v1；该入口始终发送 canonical v1，旧 OpenAI producer 开关关闭也不降级。账本和日志的 completion 包含候选文本与思考 token，原生响应仍分别保留供应商字段；不另设思考单价。

原生元数据缺失、未知/矛盾/负数/小数/溢出计量、多候选或非文本计量均不使用估价结算。供应商采购证据与用户收费保持原有独立边界，不将此处的计价桶宣称为供应商实扣证据。

## 4. SSE 与失败结算

SSE 按完整事件解析，支持 CRLF、多行 `data:` 和网络字节分片。合法事件仍是原生 GenerateContentResponse，函数参数与签名不转换成 OpenAI/Anthropic。累计 usage 使用最后一个完整有效快照，显式零覆盖先前值，不累加每个 chunk。Gemini 不使用 `[DONE]`。

首个完整事件在提交响应头前校验。终态事件及后续 usage-only 事件有界缓冲，只有收到完整 EOF、已知文本终态/安全阻断和有效 usage 才下发终态。终态后新增候选、非法 JSON、上游 error、事件超限、缺少事件边界或缺终态均失败；不补造 STOP 或 usage。

- JSON / 流首帧失败：502 原生 error envelope，释放预扣。
- 已开始流之后断流、取消、超时、下游写失败：关闭流，保留已发部分，不追加 JSON、不重放；使用脱离已取消请求的结算 context 释放预扣。
- 完整 JSON / SSE：canonical usage 提交一次，账本与日志保留模型/来源/attempt/reservation；重复 CommitQuota 不能重复扣款。
- 上游已完成但 Billing commit 失败：不重放上游；保留既有结算错误观测与预扣恢复机制，不向账本补造成功。

## 5. 配置、兼容与回滚

不修改生产价格、渠道、凭据、契约灰度、供应商监控、付费探测目标或其他阶段的开关。普通渠道配置中列出客户端可访问模型并完成映射后，部署新版 Relay 即提供上述原生入口。旧 Relay 回滚会失去该入口，原有 OpenAI/Anthropic 入口和历史账本继续可读；无需逆迁移。

2026-10-30 10:32 CST 已部署生产 Relay，正式 API 根地址为 `https://api.mengbin.top`；原生入口鉴权/拒绝分支、CORS 和健康验收通过。没有新增或改动真实来源、模型、价格或付费探测目标。真实供应商、实际模型限额、SDK 版本及付费请求仍待指定环境验收；工程测试和生产拒绝分支不替代这些条件。验收证据见 [B2 实施验收](../runbooks/b2-native-gemini-acceptance-2026-10-30.md)。

协议依据：[Google GenerateContent / StreamGenerateContent 与 UsageMetadata](https://ai.google.dev/api/generate-content)、[Google 函数调用文档](https://ai.google.dev/gemini-api/docs/function-calling)，2026-10-30 核对；B2 的有限支持集合以本文为准。
