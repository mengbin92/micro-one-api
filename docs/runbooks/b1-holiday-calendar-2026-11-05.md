# B1 忙闲时段日历与法定节假日对齐

> 2026-11-05 · Asia/Shanghai · [B1 冻结契约](../design/b1-context-time-pricing-contract-2026-10-29.md) · [B1 验收](b1-context-time-pricing-acceptance-2026-10-29.md) · [下一阶段排期](../design/next-stage-plan-2026-10-26.md)

## 改动与结果

生产 `ServiceTierPricing`（`version:3`，`channel/11`，`deepseek-flash`）保持启用，只调整日历字段：

| 字段 | 改动前 | 改动后 |
| --- | --- | --- |
| `time_pricing.calendar_valid_through` | `2026-12-31` | `2027-12-31` |
| `time_pricing.holidays` | `[]` | 62 个日期（2026 年 33 个 + 2027 年 29 个） |
| `time_pricing.peak_windows` / `peak_prices` / `peak_upstream_prices` | 工作日 09:00–12:00、14:00–18:00，忙时双倍 | 不变 |

节假日日期命中 `holidays` 即返回 `off_peak`，直接使用根价格（基础价）；周末本就无忙时窗口，同样按基础价。因此本次改动等价于「法定节假日全天不涨价，其余工作日按原忙时窗口翻倍」。

写入走管理端 owner 路径，未直接改库：

- 入口：`PUT /api/option`，body 为 `values.ServiceTierPricing` + `expected_revisions.ServiceTierPricing` + `reason`。
- 修订号 `1 → 2`，`oneapi_admin.system_options` 与 `oneapi_billing.system_options` 同步更新（`updated_at` 一致，长度 2805）。
- 审计落 `oneapi_admin.resource_write_audits`：`billing.pricing.update`、`billing.upstream_cost.update`、`system.option.pricing.update` 三条，结果 `success`，actor 为管理员用户 1。
- 部署/重启：无。billing 与 admin 均未重启，日志无 error、无 `invalid ServiceTierPricing`。

## 日期来源

- 2027 年：国务院办公厅《关于2027年部分节假日安排的通知》，国办发明电〔2026〕10 号，2026-11-05 发布（[中国政府网](https://www.gov.cn/zhengce/content/202611/content_7085101.htm)）。元旦 1/1–1/3；春节 2/5–2/13；清明 4/3–4/5；劳动节 5/1–5/5；端午 6/9；中秋 9/15；国庆 10/1–10/7。
- 2026 年：国务院办公厅《关于2026年部分节假日安排的通知》，2025-11-04 发布。元旦 1/1–1/3；春节 2/15–2/23；清明 4/4–4/6；劳动节 5/1–5/5；端午 6/19–6/21；中秋 9/25–9/27；国庆 10/1–10/7。

2026 年日期均已过去，补入只为配置自洽；已结算账本不重算、不回写，历史金额保持不变。

## 本地校验

用仓库解析器 `billingdomain.ParseServiceTierConfig` 与 `BandAt` 直接校验新配置（临时用例执行后已删除）：

- 解析与 `Validate` 通过：`version:3`、`timezone:Asia/Shanghai`、节假日无重复且不晚于日历有效期。
- 2027-01-01（元旦）、2027-02-08（春节）、2027-06-09（端午）、2027-10-01（国庆）10:00 均为 `off_peak`。
- 2027-01-04、2027-02-15、2027-06-16、2027-10-08、2027-12-30 的 10:00 均为 `peak`；12:30 为 `off_peak`。
- 2028-01-03 返回日历过期错误，确认有效期边界。

## 遗留边界

1. **调休上班日未加价**：2027-01-31、02-14、05-08、09-26、10-09（2026 年为 01-04、02-14、02-28、05-09、09-20、10-10）按国务院通知上班，但落在周六/周日。当前配置只能表达「工作日窗口 + 节假日排除」，没有「额外工作日」字段，这些日期仍按基础价计费。若要求与国内工作日完全一致，需要给 `TimePricingConfig` 增加 workday 覆盖字段并走契约变更，不能靠改 `peak_windows` 表达（会把所有周末变成忙时）。
2. **日历到期**：有效期止于 2027-12-31。`BandAt` 对超期日期返回错误，计费冻结会以 `ErrRequestSnapshotUnavailable` 拒绝该来源/模型的请求，不会静默按旧日历收费。2028 年安排发布后须在到期前更新，可与 2028 年节假日通知同时进行。
3. **未产生收费样本**：`deepseek-flash` 近 7 天 0 请求，本次改动仍只有配置事实，没有真实忙时/节假日计费流水；真实收费验收继续保留。
