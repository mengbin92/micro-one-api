# DeepSeek 渠道 11：两天零用量基线与小额验证

日期：2026-10-21（Asia/Shanghai）。维护者选择 DeepSeek、候选渠道 11，核验周期先按两天；确认当前线上只有本人使用，最近两天没有使用 DeepSeek，主要使用 kimi-k3、少量 glm-5.3。随后明确允许小额验证测试。本轮选定上限 **0.10 CNY、最多两次**，不重开独立存储观察或扩大契约/收费灰度。

## 来源与核验周期

生产只读确认渠道 **11**：provider type 6，地址 `https://api.deepseek.com/`，启用、healthy，当前 authorization revision **4**。模型映射 `deepseek-flash` 启用，`deepseek-v4-pro` 未启用。

在生产内存中复用仓库 AES-GCM 实现解密当前渠道凭据，仅输出掩码 `sk-7e117**a6fc`，与已登录官方用量页所选 **micro-one-api** Key 的掩码一致。明文凭据未离开生产，也未写入归档；临时验证程序在本机交叉编译后传输，执行后删除。该核对证明当前配置关联，不补造历史凭据生效记录或供应商最终水位。

两天指 **48 小时核验切片**，不将按量计费描述为供应商已经结账。先核对最近两个已结束自然日：**2026-10-19 00:00 至 10-21 00:00 CST（不含终点）**。后续小额测试发生在 10 月 21 日，另归入 **10 月 21–22 日** 的两天核验切片，最早 10 月 23 日结束后才能取得完整已结束窗口；届时仍需核对报告完整性、迟到更正和覆盖证据。本轮没有创建自动跟进任务。

## 已结束两天：没有推理用量

官方用量页 `https://platform.deepseek.com/usage` 筛选 `micro-one-api`、10/19–10/20，页面注明 GMT+8、数据可能延迟五分钟：0 请求、0 token，金额显示 0.00 CNY。导出的费用与 token 两份 CSV **只有表头、没有数据行**。

本地相同窗口的渠道 11 消费账本为 0，渠道日志为 0，存在明确 upstream send 时间的执行事件为 0。按**接收时间**统计另有 600 个 health_probe intent、599 个 header checkpoint 和 599 个 terminal，均为 `/models` 或尚无 endpoint 的 intent；它们没有可靠 send 时间，不能把接收时间等同于执行时间，也不能把条数当作付费模型调用。

结论：**NO_INFERENCE_ACTIVITY_OBSERVED**，与维护者描述一致。页面金额显示与空 CSV 分开记录；不制造一条金额为零的供应商行，也不把没有正金额样本视为真实费用闭环通过。原始 ZIP 私有副本保留在 `/tmp/micro-one-deepseek-two-day-20261021`，仓库中的两份 CSV 原样保留表头，没有账号或密钥数据行。

## 已授权小额验证：两次成功

依据 [DeepSeek 官方价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)与 [Chat Completions 参数](https://api-docs.deepseek.com/api/create-chat-completion/)，使用当前已启用的 `deepseek-flash`，关闭 thinking，`max_tokens=16`，发送固定无私人数据的 `Reply only OK.`，无工具、图片或自动重试。

每次预留 4,096 输入 token，按高峰最贵的未命中输入 2 CNY/百万 token、输出 8 CNY/百万 token，预先为两次保留 **0.016640 CNY**，低于 0.10 CNY 上限。每次网络调用前以独占文件记录 claim；未知结果不重试，第一条响应异常或超出预留就停止第二条。

| 调用（CST） | HTTP / 输出 | 未命中输入 | 缓存命中 | 输出 token | 原币费率估价 |
| --- | --- | ---: | ---: | ---: | ---: |
| 10:47:18–10:47:19 | 200 / OK，stop | 8 | 0 | 1 | 0.00002400 CNY |
| 10:47:19–10:47:20 | 200 / OK，stop | 8 | 0 | 1 | 0.00002400 CNY |
| 合计 | 2 次成功，无重试 | 16 | 0 | 2 | **0.00004800 CNY** |

两次都没有 reasoning 输出，实际输出均低于上限。原生 response ID、usage、响应哈希和有界调用程序见[证据索引](evidence/deepseek-channel11-two-day-2026-10-21/index.json)。金额为 **tariff_estimate**；不将费率估价称为供应商实扣或最终账单。

调用路径是**在生产主机使用渠道 owner 的当前凭据直连官方 API**，本轮归档标记 `internal_test`。没有经过 Relay 用户入口，也没有向生产 owner 合成 execution intent、探针、用户消费账本或供应商核验 run；因此不作为 Relay 路由/用户结算闭环证据。渠道普通“测试”只访问 `/models`，不能代替本次真实推理验证。

## 官方报告回补：当前快照精确相符

随后在官方页面选定同一 micro-one-api Key、今天，页面回补 **2 请求、18 token、<0.01 CNY**。重新导出的费用 CSV 有一行 **0.0000480000000000 CNY**，token CSV 有三行：2 次请求、16 个缓存未命中输入 token、2 个输出 token；原币单价延展、两次原生 usage 估价与官方报告金额使用 Decimal 比较，差额精确为 **0**。

费用行的 wallet_type 为 `Granted`，这里只确认供应商报告费用，不声称现金充值扣款或税务发票。费用/token 行的账号 ID 散列一致；归档将其替换为 SHA256，并移除 API Key 字段。原始 ZIP 保持私有，原始与脱敏文件哈希见 [当前快照证据](evidence/deepseek-channel11-two-day-2026-10-21/provider-live-snapshot.json)。

本次状态为 **CONTROLLED_SOURCE_COST_SNAPSHOT_MATCHED**。报告行属于 10:00–11:00 的时间桶，导出时该桶、当日和两天窗口均不能视为已结账；行的 end_time 也不充当供应商最终水位。CSV 没有逐请求 ID，本次只证明所选 Key 的受控调用合计相符。

## 下一步与边界

- 已完成来源选择、当前凭据掩码核对、已结束两天的零推理基线、小额原生调用和官方当前费用快照合计核对。后续 N3 仍等待 **已结束两天窗口与 owner 覆盖审查**：在 10 月 21–22 日窗口结束后取得官方报告，核对真实账号/Key、原币金额、token 与内部测试覆盖，再按实际材料执行既有导入/重放，不把直连测试补写成用户请求。
- DeepSeek 当前不适用仓库仅面向 OpenAI/Anthropic USD/UTC scope 的费用 API。继续使用官方 CSV；费用 CSV 没有逐请求 ID 或显式最终结账水位，CNY 也不能直接与 owner 的 USD 历史估价相减。
- N4 的独立 reviewer/IAM、已审签名候选、candidate/previous 预算和 30 分钟/100 样本仍待齐备；本次两次小额验证没有批准契约晋级或开启 canary。
- 钱包、价格、权限、灰度、生产实例和原固定 48h 观察证据保持；两次调用只产生上述已授权供应商用量，不更改这些配置。

离线复核：

```bash
python3 docs/runbooks/evidence/deepseek-channel11-two-day-2026-10-21/check.py
```

该命令只核对已保存文件哈希、空 CSV、当前掩码、health_probe 分类、两次原生 usage、十进制估价和官方快照，不发送模型请求或写生产数据。
