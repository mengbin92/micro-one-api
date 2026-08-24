# 线上 test 渠道全模型不可用事故分析与修复

> 日期：2026-08-24
> 状态：代码已修复，待发布

## 现象

线上环境的 test 渠道（id=1，上游供应商）一度出现所有模型请求失败。

## 根因

1. 09:19–09:26（北京时间）上游 Kimi-K3 节点异常，返回 `500 {"message":"service do not have healthy model Kimi-K3 now"}`。这是上游模型池故障，不是我方密钥或网络故障。
2. relay 每次用户请求重试 3 次，每次失败都记录一次渠道级健康失败；一次请求即可达到阈值，触发 test 渠道 5 分钟熔断。
3. 健康/熔断粒度是渠道级，导致同渠道的 GLM-5.2、qwen3.7-max、MiniMax-M3、Kimi-K3、DeepSeek-V4-Flash-0731、DeepSeek-V4-Pro-0813 全部被路由排除。
4. 客户端持续重试故障模型，熔断窗口被反复刷新，无法及时自愈。

同一时间 DeepSeek-V4-Pro-0813 走同一渠道成功，证明故障集中在 Kimi-K3 上游节点。

## 方案评估

直接将全部渠道健康改造成模型级持久化熔断并不是本次事故的最优首修。该方案需要同时修改健康 RPC、模型映射持久化、多实例状态同步、选择器和半开恢复探测，改动面大且需要单独定义一致的恢复策略。

本次采用三项边界清晰的修复：

1. 同一请求内，同一来源的多次 retry 合并为一次终态健康结算；切换到另一来源时，前一来源才结算一次。这样不会因 retry 放大渠道熔断，也不会重复减少选择器 `inflight`。
2. 将 `service do not have healthy model` / `no healthy model` 识别为模型范围故障。该错误仍触发当前请求 fallback，并标记 `model_unavailable`，但不推进渠道级熔断。
3. monitor-worker 的 `/models` 探测必须解析为包含数组形态 `data` 或 `models` 字段的 JSON；HTML 200、缺字段或错误字段类型记录为 `invalid_response`，不再误判为健康。

## 修复内容

- `internal/biz/retry.go`：按请求来源合并健康结果，处理成功、失败、取消、来源切换和重试耗尽等出口；增加模型无健康节点错误分类。
- `app/monitor/internal/biz/channel_health_checker.go`：增加 `/models` 响应结构校验和 `invalid_response` 监控原因。
- 增加回归测试：模型无健康节点 fallback、同渠道重试只结算一次、HTML 200 探测失败。

完整模型级熔断保留为后续设计项：需要以 `(channel_id, model_id)` 为键，明确阈值、TTL、半开探测和多实例一致性后再实施。

## 验证

```bash
go test ./internal/biz ./app/monitor/internal/biz
go test -run '^$' ./internal/... ./app/monitor/...
```

两组命令均通过。线上发布后需确认：故障模型触发 `model_unavailable` 时渠道失败计数不增长，同渠道其他模型仍可路由；HTML 200 探测触发 `channel_health_probe_total{status="error",reason="invalid_response"}`。
