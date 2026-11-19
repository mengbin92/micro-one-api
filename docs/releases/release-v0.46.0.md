# Micro-One API v0.46.0 发布公告

> 2026-11-19 · 上一版 [v0.45.5](./release-v0.45.5.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.46.0)

v0.46.0 是 **MINOR 功能版本**，接入语音合成、流式语音识别和实时语音对话，补齐网关授权与计量结算。

## 1. 合成和识别使用可核验的请求计量

**根因**。旧版合成原样转发按估算 token 预扣和结算，无法表达供应商合成按字符、识别按时长的单价。语音识别的 SSE 协议也没有专用入口。

**修复**。合成沿用 `/v1/audio/speech`，支持二进制和 SSE 结果。识别使用 `/v1/audio/asr/sse`，支持 PCM16 和 PCM WAV。Billing 按预扣时的测量单位、价格、汇率和组倍率冻结金额。SSE 收到终态后提交结算，提前断开时释放预留。

**影响服务**。Relay Gateway、Billing、Admin。

## 2. 实时语音逐轮校验资格和结算

**根因**。Relay 没有实时语音 WebSocket 入口，常规对话路径的旧 DTO 也无法解析语音输入数组。

**修复**。实时语音通过 `/v1/realtime?model=stepaudio-2.5-realtime`。每次客户端发出 `response.create` 前重新校验 API Key、路由资格和来源并预扣。完成时按上游终态的输入、输出和缓存读取 token 结算。上、下游帧写入沿用既有 WebSocket 时限。语音对话模型的 `/v1/chat/completions` 保留原生音频输入数组。

**影响服务**。Relay Gateway、Billing。

## 兼容性说明

- 实时语音使用客户端明确触发的单轮模式。服务端 VAD 自动响应与独立输入转写配置需要进一步的用量关联支持。
- 识别入口按可从样本与格式核验的长度计费。本版接受 PCM16 和 PCM WAV，压缩音频需在调用前转为这两种格式。
- 合成每个 Unicode 字符按一计费字符，英文字母和标点按半个计费字符。请求和账本以此平台计量规则为准。
- 正金额的语音请求沿用最低 1 个额度单位的结算规则。
- 原有文本及音频 API 的请求和结算方式沿用既有契约，无数据库迁移。

## 升级步骤

1. 在本机构建 linux/amd64 镜像，保留三个生产服务的回滚标签。
2. 依次部署 Billing、Admin、Relay Gateway，确认 Billing 报告语音计量能力。
3. 使用现有管理台的系统选项发布 `AudioInputPrice`，为实时对话配置输入、输出和缓存读取单价。
4. 只将模型映射到指定专属组，验收结果、选路、拒绝路径和结算。
5. 回滚前先停用新模型映射，再依次恢复 Relay、Admin 和 Billing。

## 验证

- [x] 合成字符计量、识别 PCM 时长、SSE 终态、音频对话入参及 Realtime 自动响应限制的定向回归。
- [x] Relay、Billing 和相关域的 race 测试及综合 `make verify`。
- [x] 计费冻结和重放回归验证 60 秒识别按预扣金额结算，供应商返回的 token 数不会替换时长计价。
- [x] 空白音频价目拒绝写入，显式空对象保留既有停用语义。修复后的 `make verify` 通过。
- [x] Billing、Admin 和 Relay 的 linux/amd64 本机候选镜像均已构建，源摘要为 `76279a3f2eb781d4ced3c4833d781642aca55bf8000ba8bc2769edb9c6bd489d`。
- [x] 高强度评审三轮均取得完成记录。核实计费授权、预扣、重放与 SSE 终态结算，并实测价目解析拒绝非有限数字。
- [ ] 验收生产发布、专属调用及实际结算。

## 完整变更日志

- `feat(audio): meter synthesis and transcription and relay realtime turns`
- `docs(release): v0.46.0`
