# 2026-09-15 P1-2 管理台汇总性能与降级验收记录

代码基线：`develop@835aa092`，加本次 P1-2 工作区改动。未新增公共 API 或数据库迁移；`/api/admin/summary` 响应键保持稳定，仅新增 `partial` / `sections` / `alerts_complete` 字段，失败分项由伪造零值改为 `null`。所有测量均在本机测试固件中进行；没有生产部署或远程 CI 执行。

## 根因

1. `handleAdminSummary` 串行执行约 20 次独立 RPC，延迟逐个累加。
2. 聚合挂在入口请求 context 上，尾部调用继承临近过期的 deadline，健康后端也报 DeadlineExceeded；此前加的 60 秒 `WithTimeout` 不能延长先到期的父 context。
3. 分项失败被替换为空集合或零值，前端把不可用显示成真实零值；定价配置读取全部站点配置并在出错时静默使用默认值。

## 修复

- 聚合编排下沉到 service 层 `LoadSummary`；HTTP handler 只保留输入输出适配。
- 独立查询受限并发执行：每请求最多 4 路，总预算 8 秒，单分项预算 2 秒；客户端断开向下取消，排队任务在取消后不再调用依赖，无分离后台 goroutine。
- ID enrichment 保持在排名结果之后的第二阶段，顺序依赖不变。
- 每个分项返回 `available` / `reason`（`timeout` / `canceled` / `unavailable`）状态；失败字段编码为 `null`，`partial=true`，告警来源不全时 `alerts_complete=false`。
- 定价配置收窄为概览实际使用的 5 个公开键；读取失败向上传播，不再静默套用默认值。
- 前端区分“暂无数据”和“数据暂不可用”：部分失败显示 amber 横幅与失败分项名称，可手动重试；失败统计卡片显示“暂不可用”而不是 0；告警来源不完整时不再显示“运行正常”。

## 同负载前后对照

负载：测试固件固定每次 RPC 延迟 20ms，每组 12 次汇总请求，只替换 RPC 边界。

| 场景 | 修复前 | 修复后 |
| --- | --- | --- |
| 单客户端 P50 / P95 | 376.8ms / 380.5ms | 105.6ms / 109.3ms |
| 4 并发客户端 P50 / P95 | 378.2ms / 379.0ms | 105.5ms / 105.8ms |
| 单请求 RPC 峰值并发 | 1 | 4（等于上限） |
| 4 客户端 RPC 峰值并发 | 4 | 16（每请求 4 路上限） |
| 部分失败行为 | 失败字段显示为零 / 空集，断言失败 | 失败字段为 `null`，健康区块保留，断言通过 |

## 验证结果

| 验证 | 结果 |
| --- | --- |
| `make verify` | 通过：format、Go 单元测试、race 门禁、分层、迁移治理、前端 lint / test / build |
| `go test -race ./app/admin/internal/server ./app/admin/internal/service` | 通过；修复了测试固件在并发聚合下未加锁的 `aggregateReqs` 记录 |
| 同负载对照 `TestAdminSummaryLoadProfile` / `TestAdminSummaryBoundedParallelism` | 通过：峰值并发 >1 且 ≤4，P95 如上表 |
| 降级 `TestAdminSummaryPartialFailure` | 通过：注入 AggregateUsage 不可用后收入字段为 `null`，健康数据保留 |
| 超时 / 取消 service 测试 | 通过：慢分项不取消健康兄弟任务；请求预算包含排队；取消后不再调用依赖；无后台工作逃逸请求 |
| 定价配置 `TestSummaryPricingOptionsScopeAndFailures` | 通过：只读 5 个键及既有别名；读取失败传播错误 |
| 前端 `OverviewPage.test.tsx` 4 例 | 通过：部分不可用横幅与重试恢复、真空数据显示为空 / 零、请求失败不伪造健康与收入 |
| `npx tsc -b --noEmit` 与 eslint | 通过 |

## 边界

- P95 对照来自确定性测试固件（每次 RPC 固定 20ms），证明并发与预算行为，不代表生产绝对延迟；生产门槛需在真实基线下另行确定。
- `sections` 的 reason 是有限稳定词汇，不回传上游原始错误；明细仍在服务端日志。
- 汇总响应键保持兼容，但失败语义从伪造零值变为 `null`；依赖旧零值行为的前端区块已同步改造。
- 本次未部署生产、未发布版本；生产观察与真实负载对照留待后续窗口。
