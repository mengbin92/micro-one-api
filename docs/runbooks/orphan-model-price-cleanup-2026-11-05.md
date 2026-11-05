# 孤儿模型价格键清理：glm-5.2

> 2026-11-05 · Asia/Shanghai · [毛利与未定价监控修复](billing-monitoring-recovery-2026-11-03.md) · [v0.39.3 发布](../releases/release-v0.39.3.md) · [B1 日历对齐](b1-holiday-calendar-2026-11-05.md)

## 为什么清

`glm-5.2` 已经从模型目录和路由中移除，但 `ModelPrice` / `UpstreamModelPrice` 两份价格 JSON 仍保留它的键。售价 `input_price=1e-7` 远低于采购估价 `1.26e-6`，任何全量价格体检都会把它报成倒挂；它同时是隐患——将来若重新挂上路由，会静默继承这个低于成本的定价。

## 调用来源核查（清理前）

| 维度 | 事实 |
| --- | --- |
| 近 90 天调用 | 0 笔，最后一次 2026-08-17 09:13:23 |
| 历史总量 | 23,294 笔，2026-07-23—2026-08-17，quota 合计 2,658,816,836 |
| 计费来源 | 全部 `cost_source=subscription`、`upstream_cost=0`（订阅计费不记按量采购成本） |
| 账号与渠道 | 订阅账号 4（18,033 笔）、channel 1（10,283 笔）、channel 6（116 笔） |
| 用户 / token | 用户 1（`admin`：`test` / `home` / `token-1`）22,889 笔；用户 3（`1`）16 笔 |
| 路由状态 | `model_routings`、`abilities`、`channels.models`、`channels.model_mapping`、`oneapi_billing.models` 均无 `glm-5.2` |

结论：历史上只有维护者本人与一个测试账号的自用流量，模型已不可调用，价格键属于残留。

## 改动与结果

| 项 | 结果 |
| --- | --- |
| `UpstreamModelPrice` | 维护者已于 2026-11-05 20:55 手动清除，revision 5；审计 `billing.upstream_cost.delete`（resource 3） |
| `ModelPrice` | 本次写入，revision **9 → 10**，模型数 **15 → 14**，值长度 1264 → 1211 |
| 写入入口 | `PUT /api/option`，单键 + `expected_revision` + `reason`（owner 路径，非直接改库） |
| 审计 | `billing.pricing.update`、`system.option.update`、`system.option.pricing.update` 三条，结果 `success`，actor 用户 1 |
| 服务 | 未重启 admin-api / billing-service，日志无 error，`/api/pricing` 返回 200 |

## 校验

- 写入前后逐键对比：差异集合仅 `glm-5.2`，其余 14 个模型的五桶价格字段完全一致。
- `oneapi_admin.system_options` 与 `oneapi_billing.system_options` 的 `ModelPrice` 同步为同一值（长度 1211）。
- 历史账本未改写：23,294 笔 `GLM-5.2` 记录保持原样，不做重算或补价。

## 顺带发现的待确认项（本次未改动）

对比 `ModelPrice`、`UpstreamModelPrice` 与 `oneapi_billing.models`、`oneapi_channel.abilities`：

| 现象 | 说明 |
| --- | --- |
| `qwen3.7-max` 有售价、无目录条目、无 ability | 与 glm-5.2 形态相同，疑似孤儿键；未确认是否有其他路由入口，故未删 |
| `step-explore` 有采购价、无售价 | 有采购估价但无对外售价，若被路由会按未定价处理 |
| `k3-256k`、`kimi-for-coding`、`kimi-for-coding-highspeed` 有目录、无按量售价 | 可能是订阅专属模型，需按订阅计价语义确认，不应当作缺失 |
| `step-5-preview` 有售价与 ability（channel 9，status=2 停用） | 属于合法配置，非孤儿 |

上述四项需要维护者确认后再动，本次只处理已确认不可路由的 `glm-5.2`。
