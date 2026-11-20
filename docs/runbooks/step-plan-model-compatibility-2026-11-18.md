# 订阅渠道模型接口验收

2026-11-18（北京时间）。新增模型仅限订阅账户自用。正式使用范围为需单独授权的专用路由组。

渠道 #9 已启用并处于健康状态，采用 Anthropic 兼容协议。现有 `step-3.7-flash`、`step-5-preview` 的目录和渠道映射均已启用。渠道所属 `default` 组有 10 个不同用户的有效授权。

## 2026-11-18 验证结果

探测从生产主机调用上游供应商的订阅接口，使用短文本和合成语音。下表也记录了生产网关入口检查和代码核对结果。

| 模型 | 上游结果 | 当前网关 |
|---|---|---|
| `step-3.7-flash` | 已由维护者验收 | 已启用 |
| `step-5-preview` | Messages、Chat Completions 均为 200 并返回正文 | 已启用 |
| `step-3.5-flash`、`step-3.5-flash-2603`、`step-router-v1` | Messages、Chat Completions 均为 200 并返回正文 | 兼容现有 Anthropic 渠道，待专用组的网关端到端验收。路由模型的上游消耗随实际命中的模型变化 |
| `stepaudio-2.5-chat` | Chat Completions 的文本与音频输入均为 200，合成语音转写命中预期短句。Messages 返回 404 | 现有 Anthropic 渠道无法承载。需验证使用 OpenAI 兼容渠道时，音频输入和 usage 能否完整通过 |
| `stepaudio-2.5-tts` | Bearer 请求返回 200 和 WAV。使用现有渠道的 Anthropic 请求头返回 401。流式 WebSocket 握手为 101 | HTTP 入口存在。仍需 Bearer 渠道和按字符计费。流式入口为 501 |
| `stepaudio-2.5-asr` | `/audio/asr/sse` 返回 200 和 SSE，转写命中预期短句 | 生产对应路由为 501。已有 `/audio/transcriptions` 的上游路径与结算口径不同 |
| `stepaudio-2.5-realtime` | WebSocket 握手为 101 | 生产实时入口为 501 |

## 上架状态

已从生产管理台创建专属路由组 `step-plan-private`（ID 6）和 Anthropic 兼容渠道 #12。渠道仅属于该专属组。生产回读确认三款新增模型均为非公开，三条渠道映射均启用。使用该渠道配置从生产主机直测上游 Messages，三款模型均返回 200 和正文。

Relay 旧代理入口的渠道授权检查已随 [v0.45.4](../releases/release-v0.45.4.md) 部署。定向回归验证了模型或渠道未授权时，处理器会在读取渠道凭据和预留额度前拒绝请求。

内部计费已沿用现有 Flash 单价。生产计费配置 revision 14 回读确认，三款新增推理模型每百万 token 的输入、输出和缓存读取价格分别为 0.20、1.15 和 0.04 美元。

专用组已启用并保持专属。管理台 v0.45.5 修复授权请求遗漏的版本字段后，Identity 审计确认 `admin` 是组内唯一有效授权用户，默认组保持为 `default`。

语音对话已按同一 Flash 单价登记为非公开，独立 OpenAI 兼容渠道 #13 仅绑定专属组。

TTS、ASR 和 Realtime 的内部美元单价按 6.8 元人民币兑 1 美元计算。计费基准分别为每百万计费字符 580 元、每音频小时 0.45 元，以及每百万 token 的输入 13 元、输出 90 元、缓存读取 3 元。计算保留原始单价与换算关系，按结算最小单位统一舍入。

## 2026-11-19 语音上线

[v0.46.0](../releases/release-v0.46.0.md) 已部署 Billing、Admin 和 Relay Gateway。三个服务的生产镜像均已核对发布源码摘要，网关健康检查返回 200。

生产回读确认渠道 #13 已启用 TTS、ASR、Realtime 和 Chat 的模型映射，且只绑定专属组 `step-plan-private`。四款模型均为非公开，`admin` 是该组唯一有效授权用户。合成与识别的人民币单价和 6.8 汇率已写入 `AudioInputPrice`，实时对话沿用已登记的 token 单价。

生产初验中，语音合成与流式识别在无凭据时均返回 401。Realtime 的 WebSocket 握手最初经公网入口返回 400。公网反代已为 `/v1/realtime` 增加精确匹配并透传升级头。热加载后无凭据握手返回 401，健康检查返回 200。

[v0.46.1](../releases/release-v0.46.1.md) 已部署 Admin API。生产管理台可触发安全验证，验证后成功创建固定到专属组的临时验收 Key。

| 生产验收 | 接口结果 | 账本 |
|---|---|---|
| TTS | HTTP 200，返回 WAV | 4 计费字符，扣除 3 个额度单位 |
| ASR | HTTP 200，收到 SSE 转写终态 | 15 秒音频，扣除 3 个额度单位 |

两笔请求均通过渠道 #13 和专属组结算，每笔只有一条扣费记录。

Realtime 首次使用临时 Key 的公网握手返回 101，旧版 Relay 随后关闭会话。账本确认当次没有预留或扣费。模拟上游复现了 SSE 响应体包装丢失 WebSocket 写入能力的问题。

[v0.46.2](../releases/release-v0.46.2.md) 已部署 Relay Gateway，生产镜像源码摘要和健康检查通过。使用原临时 Key 通过渠道 #13 完成一轮 WebSocket 语音对话，终态用量为 59 个输入 token、10 个输出 token 和 0 个缓存读取 token。生产账本只有一条已结算扣费，共 2 个额度单位。临时 Key 已停用，管理台回读为禁用，同一 Key 重试握手返回 403。

## 2026-11-20 待验收模型复核

三款新增文本模型和语音 Chat 的专属映射、渠道健康与价目均核对通过，暂未找到生产消费账本或可用固定组 Key。维护者授权最多 4 次、合计不超过 1 CNY。短请求夹具已准备，正式调用待能签发匹配权限的 Key 并确认预算上限。本轮新调用 0 次，实扣 0 CNY。

## 语音扩展本地验证

| 功能 | 接入方式 | 结算边界 |
| --- | --- | --- |
| 自动响应 | `/v1/realtime?model=stepaudio-2.5-realtime` 的 `session.update` 配置 `turn_detection.type=server_vad`。Relay 识别 PCM16 单声道 24 kHz 的语音和静音，每次处理一个在途回复 | 检测到语音结束后先复核授权并预扣，再向上游发送音频、commit 和 `response.create`。收到有完整用量的 `response.done` 时结算 |
| 独立输入转写 | `session.update` 配置 `input_audio_transcription.model=stepaudio-2.5-asr`，按 PCM16 输入逐段 append 后 commit。VAD 模式在对应话轮自动转写 | API Key 及当前渠道需同时允许 ASR。转写单独按音频时长预扣和结算，最多 30 秒。单独转写的 commit 会返回转写事件 |
| 流式合成 | `/v1/realtime/audio?model=stepaudio-2.5-tts`。按 `tts.create`、多帧 `tts.text.delta`、`tts.text.done` 的顺序发送，可使用 `tts.text.flush` 加速分段输出 | 每连接最多 1000 字符，首次发送文本前预扣额度上限。收到对应会话的音频终态后按实际计费字符结算一次，未完成时释放预扣 |

本地回归覆盖音频分帧、自动生成前的预扣、独立 ASR 时长、多帧 TTS 在输入结束前输出音频，以及冻结价率结算和拒绝超出预扣上限的计量。生产升级时由 Billing 先提供实际字符结算能力，再升级 Relay 并在公网反代接入新的 WebSocket 路径。真实上游音频、账本与断线验收待补。

VAD 联动转写使用上游确认的输入条目 ID。有待处理音频时，先清空缓冲区再切换 VAD 或转写配置。下游断线后，已发出的 Realtime 轮次最多等候 10 秒，WebSocket ASR 和符合收尾条件的 TTS 最多等候 5 秒，且仍受原上游截止时间限制。只对可验证的终态结算。语音入口使用现有计费路径，若同一来源和模型被加入 native execution 计费 pilot，Relay 会在发送前拒绝。

## 2026-11-20 增量部署

北京时间 19:49 起按 Billing、Channel、Relay、Web 的顺序切换。本机 `make verify` 通过。维护者明确授权仅本次跳过未完成的 OCR 门禁。三个后端镜像的源码摘要为 `1a82a277f2444ca2edcb8a723a42595c231d84f43cd3440a600e75c21bb0e6df`，各服务 `/healthz` 均返回 200。

| 验证项 | 结果 |
| --- | --- |
| Realtime 无凭据 WebSocket 握手 | Relay 直连和公网均返回 401 |
| TTS 无凭据 WebSocket 握手 | Relay 直连返回 401。公网入口原返回 400，19:56 在独立 Nginx 增加精确匹配和升级头透传后返回 401 |
| Web 静态资源 | Admin 直连及公网控制台首页 SHA-256 与候选包一致，9 个公开资源逐个校验通过 |

生产回滚点为 `rollback-20261120-194928`，包括三服务旧镜像、Compose 备份和 Web 挂载目录备份。独立 Nginx 配置备份为 `/var/backups/micro-one-api-nginx-tts.2otisa/api-vhost.conf`，语法检查和热加载通过。本轮未发起付费语音调用，真实音频、账本和断线行为待验。

同日经维护者确认，应用机删除 27 个旧 Compose 备份、10 个旧 Web 备份及 130 个历史部署镜像标签，实测释放 2.60 GiB。复核保留的回滚组、生产容器镜像和公网健康检查均通过。域名机保留唯一的 Nginx 回滚副本，异地数据库备份沿用既定保留策略。
