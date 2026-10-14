# DeepSeek 余额扩大核验与渠道 1 诊断

日期：2026-10-14。接续[首次官方试点](deepseek-official-pilot-2026-10-14.md)。用户授权加大请求，并确认本轮 DeepSeek 账号在测试期间独占。DeepSeek 本轮费用上限 0.50 CNY，最多两次新推理，关闭 thinking，每次 max_tokens=32；仅发送生成的随机 ASCII 测试数据。

## DeepSeek：余额与官方费用导出相符

| 本轮新请求 | 未缓存输入 | 缓存命中 | 输出 | 原币估价（CNY） |
|---|---:|---:|---:|---:|
| balance_load_1 | 69,853 | 0 | 1 | 0.069857 |
| balance_load_2 | 69,846 | 0 | 1 | 0.069850 |
| 本轮合计 | 139,699 | 0 | 2 | 0.139707 |

两次均 HTTP 200、finish_reason=stop；不重试推理。依据[官方空闲时段价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)每 token 未缓存输入 0.000001、命中 0.00000002、输出 0.000004 CNY，以 decimal 计算。本轮请求均在北京时间 07:50 左右；请求前按峰值单价和保守输入字节上界预留的两次费用最多 0.420512 CNY，低于授权上限。

余额最初未变化，随后 `/user/balance` 显示总扣减 **0.14 CNY**，连续三次、间隔 30 秒读数一致；变动来自充值余额，赠送余额变动为 0。与本轮估价差额 **0.000293 CNY**，小于 0.01 CNY 显示粒度。不能从余额反推逐请求费用，但本轮没有发现超出显示精度的金额差异。官方控制台用量页注明“数据可能有 5 分钟延迟”；本次早期 0.00 的读数不能作为最终扣费结果。

从已登录的[官方用量页](https://platform.deepseek.com/usage)筛选 `micro-one-api` key 后导出原始 ZIP，费用与 token 明细进一步确认：

| 六次官方请求（首次四次 + 本轮两次） | 本地原生响应合计 | 官方导出 | 差异 |
|---|---:|---:|---:|
| 请求次数 | 6 | 6 | 0 |
| 缓存未命中输入 | 140,151 | 140,151 | 0 |
| 缓存命中输入 | 128 | 128 | 0 |
| 输出 | 54 | 54 | 0 |
| 全部 token | 140,333 | 140,333 | 0 |
| CNY 金额 | 0.14036956（tariff_estimate） | 0.1403695600000000（官方费用导出） | 0 |

费用 CSV 的行粒度为模型/钱包/币种/日，**没有请求 ID 或 key 列**；key 筛选来源保留在导出来源记录，amount CSV 含该 key 的请求数与 token 数。金额一致证明本次已捕获快照的合计费用相符，不证明每笔费用分配，也不自动升级为完整日账单。

报告覆盖日期是 `2026-10-14T00:00:00+08:00` 至 `2026-10-15T00:00:00+08:00`；取得报告时尚未到结束时间。状态为“真实导出快照已核对、受控余额区间在显示精度内相符”，**完整供应商日账期仍未关闭**。未把未来日结束时间或控制台导出当作完整水位，未用 CSV 修改价格、用户收费、退款或生产账本。

证据：[余额区间](evidence/deepseek-balance-and-channel1-2026-10-14/balance-window.json)、[导出来源与精确核对](evidence/deepseek-balance-and-channel1-2026-10-14/official-export-provenance.json)、[脱敏费用 CSV](evidence/deepseek-balance-and-channel1-2026-10-14/cost-2026-09-15_2026-10-14.csv)、[脱敏 token CSV](evidence/deepseek-balance-and-channel1-2026-10-14/amount-2026-09-15_2026-10-14.csv)。未脱敏 ZIP 与绝对余额快照仅保存在本地私有 `/tmp/micro-one-api-deepseek-balance-window-20261014`，目录 0700，文件 0600；仓库只保留脱敏派生 CSV 和原始文件 SHA256。

后续 08:09 线上复核：owner 已恢复 healthy，用户确认对外启用的 5 个模型在线调试均正常；见[渠道 1 最新可用性](channel1-live-availability-2026-10-14.md)。以下保留上一轮的初始诊断事实。

## 渠道 1：旧版本冲突与历史不可用状态分别核对

生产渠道 1 指向 `gpunexus.chengfangtech.com`，不是 DeepSeek 官方渠道；当前 owner revision 为 7，状态启用，历史连续三次 503 后健康状态为 unavailable。旧错误是 `no algo service available now, please wait a moment`。

在当前用户管理员页面提交了一次测试；页面旧缓存可能继续携带 revision 6，而 owner 已是 7。现有后端只在请求供应商后检查 CAS；409 后前端只显示错误，不刷新 owner 列表，容易反复提交旧版本。已用隔离的前后端回归复现：

- `go test ./app/channel/internal/data -run '^TestChannelActionStaleRevisionDoesNotCallProvider$' -count=1` 在修复前失败：`a stale diagnostic must fail before provider IO`，实际调用次数为 1。
- `npm test -- --run src/pages/admin/ChannelsPage.conflict.test.tsx` 在修复前失败：`expected 1 to be greater than 1`，409 后列表没有刷新。

三个假设：旧列表 revision 导致重复冲突；请求期间并发变更导致合法提交拒绝；历史供应商故障/隔离导致独立不可用状态。回归确认前两者需要请求前与提交时两次保护；第三项由真实 owner 健康记录和供应商诊断区分。

修复：渠道测试和余额刷新遇到 409 后刷新 owner 列表，保留错误，不自动重试动作、不覆盖已打开编辑草稿的 revision。IAM 操作在访问上游前核对 expected revision；远端请求后继续在事务内核对新权限、源配置和 CAS。既有权限撤销测试补充有效起始 revision，继续验证“请求期间权限撤销不能保存结果”。

单独从生产主机读取 owner 持有的渠道凭据，在内存中解密并诊断供应商，凭据未打印、落盘或移入前端：

| 诊断 | 结果 | 核验范围 |
|---|---|---|
| GET `/models` | HTTP 200 | 只确认端点响应，未证明模型列表结构或推理健康 |
| POST `/chat/completions`，模型 `qwen3.7-max`，max_tokens=16 | HTTP 200、finish_reason=stop | 一次真实小规模推理，未自动重试 |
| usage | 输入 13、completion 113、其中 reasoning 108、total 126 | completion 超出请求上限；含额外 input/output aliases，不能假设已经满足 P1 预算或收费契约 |

当前 IAM 手动测试仅保存诊断时间与延迟，**不把 `/models` 成功当作恢复真实流量的证明**。直接诊断也没有修改健康状态。已刷新管理员页，但当时授权动作呈禁用状态，未完成第二次 fresh-revision 的 UI 提交；未绕过客户端授权或直接改库解锁。

本次修复保留 P1 的恢复门禁。真实推理 200 说明该次调用可用；它没有解决输出预算与 usage 契约差异，也不代表所有模型恢复。若发布修复，只需发布渠道服务的小修复和前端；本工作区还有未发布的 P2 改动，不能直接把整个工作区的镜像当作本次线上小修复发布。本次未部署。

证据：[渠道诊断](evidence/deepseek-balance-and-channel1-2026-10-14/channel1-diagnostic.json)。

## 验证

```sh
go test ./app/channel/internal/biz ./app/channel/internal/data ./app/channel/internal/service ./app/admin/internal/service -count=1
go test -race ./app/channel/internal/data -run 'TestChannelAction|TestIAMB2ActionsRecheckRevocation' -count=1
go vet ./app/channel/internal/biz ./app/channel/internal/data
cd web
npm test -- --run src/pages/admin/ChannelsPage.conflict.test.tsx src/pages/admin/ChannelsPage.preconditions.test.tsx src/pages/admin/ChannelsPage.refresh.test.tsx src/lib/admin-write.test.ts
npx tsc -b
```

覆盖请求前 stale 6/7 拒绝且无 IO、请求中 7/8 变更拒绝提交、fresh 7 一次提交为 8 且不绕过健康恢复、请求中权限撤销、测试/余额 409 刷新但不自动重试、编辑草稿 CAS 保留。P2 的其他代表来源、关闭账期、收费 profile 安装与生产窗口仍分别待验。
