# Micro-One API v0.48.0 发布公告

> 2026-11-20 · 上一版：[v0.47.0](./release-v0.47.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.48.0)

v0.48.0 是 **MINOR 功能版本**。新增 Realtime 自动响应、独立输入转写、多帧 TTS WebSocket、Kimi 设备码绑定和运营探针留证。用户可在令牌页查看已保存的 Key 限制。本版无新增数据库迁移。

## 1. 扩展实时语音和流式合成

**根因**。Realtime 原先由客户端手动发起每轮响应，无法同时使用服务端 VAD 和独立转写。HTTP TTS 可以按请求内的全部字符预扣，WebSocket 多帧输入需要先预扣上限，再按实际使用结算。

**实现**。Relay 检测到一轮语音结束后复核权限并预扣，再向上游发送音频和生成请求。独立输入转写按音频时长结算，联动转写使用上游确认的会话条目。TTS 通过 `/v1/realtime/audio` 接收多帧文本并流式返回音频，每连接最多 1000 字符。Billing 在有效终态后按实际计费字符结算一次，账本保留计量。下游断线后，Relay 在有限时间内继续等待上游终态。 [语音协议与生产核对](../runbooks/step-plan-model-compatibility-2026-11-18.md#2026-11-20-增量部署)

**影响服务**。Billing Service、Relay Gateway、公网 WebSocket 反代。

## 2. 管理台支持 Kimi 设备码绑定

**根因**。现有静态 Key 入口无法完成设备授权和账号凭据录入，跨副本轮询还需要共享会话状态。

**实现**。Channel 使用已获上游批准的应用客户端注册接入 RFC 8628 设备码流程。会话绑定操作者和路由分组，跨副本串行轮询，账号凭据由 Channel owner 保存。Web 展示授权待完成及重试状态，Relay 的 Kimi 转发保留调用方身份。 [绑定手册](../runbooks/subscription-oauth-binding-runbook.md#34-kimi-设备码授权)

**影响服务**。Channel Service、Relay Gateway、Web。

## 3. Realtime 与运营探针接入 usage 留证

**根因**。独立留证原先覆盖用户请求，运营探针与实时语音轮次缺少同一接缝上的发送证据。

**实现**。每个 Realtime 轮次记录实际发送、完整终态或截断。运营探针在发送前校验预算许可和实际来源，按 `health_probe`、`canary` 及显式配置的 `internal_test` 分类。内部测试使用独立结果，留证通过现有 outbox 和 Log 接收端投递。 [观察手册](../runbooks/upstream-usage-observation.md)

**影响服务**。Relay Gateway。启用新的内部测试目标时同步升级 Monitor Worker。

## 4. 令牌列表展示已保存的限制

**根因**。签发成功后可以查看短期 Key 的限制，关闭弹窗后列表没有展示 Identity 已返回的字段。

**修复**。响应式令牌列表展示到期时间、剩余与已用 Token 配额、模型白名单，并区分过期、额度耗尽和禁用。列表继续显示掩码，完整 Key 在签发成功时展示。 [令牌列表与签发手册](../runbooks/routing-groups-runbook.md#56-签发短期固定组-key)

**影响服务**。Web。

## 兼容性说明

- Billing 新增实际语音用量结算字段和能力声明。Relay 请求 TTS WebSocket 计费前检查能力，混版时应先升级 Billing。
- 普通 API Key 和已有路由分组沿用已保存的数据。令牌列表复用现有 Identity 字段。
- Kimi 设备码功能要求 Channel 和 Relay 配置相同的已注册 `KIMI_OAUTH_CLIENT_ID`。现有静态 Key 路径仍可使用。
- 启用 `UPSTREAM_PROBE_TARGETS_JSON` 的 `internal_test` 目标时，Relay 与 Monitor 使用同一份目标配置。独立留证沿用现有 Log 表结构。

## 升级步骤

1. 保存镜像、Compose、Web 目录及公网反代配置回滚点。本机构建目标架构的镜像。 [部署手册](../deployment.md)
2. 依次升级 Billing、Channel、Relay。确认服务健康，核对公网 `/v1/realtime/audio` 的 WebSocket 升级头透传。
3. 更新 Web 静态产物并核对 `/tokens` 和页面资源。启用新探针用途前升级 Monitor。取得上游的应用注册后再配置 Kimi 设备授权。
4. 回滚时恢复对应镜像、Compose 或 Web 回滚副本，按需恢复公网反代配置。保留已有令牌与账本。

## 验证

- [x] `make verify` 和 gosec 通过。PCM16 负采样回归用例在 race 模式下连续通过 5 次，令牌页桌面与移动端 Playwright 通过。
- [x] 生产 Billing、Channel、Relay 健康检查通过。公网 Realtime 与 TTS 的无凭据握手均返回 401。 [生产部署](../runbooks/step-plan-model-compatibility-2026-11-18.md#2026-11-20-增量部署)
- [x] 生产控制台和 `/tokens` 的首页与 Web 制品一致，10 个资源摘要通过核对。 [Web 部署](../runbooks/routing-groups-runbook.md#56-签发短期固定组-key)
- [ ] 真实语音账本、截断释放、生产 Log ACK 和 Kimi 授权及凭据轮换待验。

本次发版按维护者授权跳过 OCR。

## 完整变更日志

- `docs: document current architecture and service boundaries`
- `feat(relay): record usage for supervised upstream probes`
- `feat(audio): support metered realtime speech streams`
- `feat(channel): bind registered Kimi device clients`
- `fix(web): show saved API key restrictions`
- `docs: record production rollout and offsite backup shutdown`
- `fix(audio): widen signed PCM samples safely`
- `docs(release): v0.48.0`
