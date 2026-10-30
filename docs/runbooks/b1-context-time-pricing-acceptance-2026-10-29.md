# B1：长上下文与忙闲时段计价实施验收

日期：2026-10-29（Asia/Shanghai）。关联[阶段计划](../design/next-stage-plan-2026-10-26.md) / [冻结契约](../design/b1-context-time-pricing-contract-2026-10-29.md)。

**当前状态：2026-10-30 已部署，计价保持关闭、真实收费验收保留。** 目标来源为维护者指定的 channel/11、`deepseek-flash`；阈值和价格后续确认。2026-10-29 工程验收通过后，维护者于 10-30 明确要求更新线上、补文档后提交；本次上线包括 Billing/Log/Relay/Identity/Admin 与独立 Web dist，无计价配置写入、付费请求、发版或 tag。工程记录与后续部署分别留证。

## 已交付

长上下文单阈值、含缓存输入量、严格 >T 整单五桶价；忙闲时段支持显式时区、多个周内窗口、节假日排除、日历有效期；两者可独立或组合。预扣时冻结时段与完整 tariff，实际 usage 决定上下文档位，异步恢复、跨时段结束、改价及幂等重放均保留原规则。采购价缺失不跨档回退。

DeepSeek legacy Chat/Responses JSON/SSE 复用现有五桶和请求边界，cache hit/miss 校验后才投影，实际 service tier 缺失保持未知。账单解释贯通 Billing → Admin/Identity → Web；客户可见快照清除嵌套采购价。三库 146 迁移和历史写入省略新列保持兼容。

## 验证记录

| 检查 | 结果及边界 |
| --- | --- |
| 配置与资金单测 | T−1/T/T+1、缓存四输入桶及输出排除、整单价、三档最高预扣、改价冻结与幂等；忙时起止/午间、节假日/周末、时区日期、日历到期拒绝；独立分时和组合分时、相同金额仍区分档位 hash、缺采购价不回退，全部通过 |
| 三库持久化 | SQLite / MySQL 8.4 / PostgreSQL 16，9 个叶子场景无跳过；完整迁移首跑/重复 no-op、规则与时段回读、快照同事务复用通过。异步最终任务落盘后重建 Billing，时钟推进 48 小时且越过日历有效期，仍结算冻结 peak+long 的 8700 定点单位，快照 hash 不变，重放只写一笔账 |
| 三库真实 owner 链路 | P1 修复后最终重跑 30.989s，无 skip：Relay HTTP → 生成的 Billing/Log gRPC → 真实 SQL；B1 使用 DeepSeek channel11/flash、JSON/SSE、缓存输入（6 uncached+4 cached=10，阈值9）、busy+long 四倍夹具价、零用量最低额、未知/缺失 service tier、partial usage 释放、执行中改价和重复 commit。原 A3 矩阵同时回归通过 |
| DTO 与采购脱敏 | Billing/Admin/Identity 的上下文和冻结时间字段完整透传；账单视图清除基础、long、peak、peak-long 采购 map，原冻结对象与 digest 不变；专项通过 |
| DeepSeek 协议 | Chat max_tokens/显式 max_completion 优先及实际发送限额一致；cache hit/miss/details 冲突、非法/缺失证据、Chat 缺 [DONE]、Responses 缺 completed、断流等均保守释放；专项通过 |
| 独立审查修复 | 发现 typed JSON 大小写兼容与 map 精确键导致的限额/TOOLS 绕过。回归先红（400 实为200且上游被调用），修复后 22 个 Chat +18 个 Responses 歧义字段用例均 400/0 上游/释放，4 个真实发送上限控制通过；第二位审阅者确认关闭计价兼容与重试边界保留 |
| Web 专项与浏览器 | 66 项专项；生产 bundle、桌面/Pixel 5 Chrome 共8例，0失败/跳过/flaky，含 v2长上下文、v3独立分时、组合及关闭示例。API 全为隔离 fixture；实际目视检查配置/自助账单截图，09:00 预扣与13:00完成的展示明确区分 |
| 生成和权限矩阵 | `make all` 通过；API/config/Wire/模块依赖与 Web API 类型重新生成，go.mod/go.sum 无变更；矩阵新增两个 helper 的源码审查，未新增 HTTP/RPC/权限或扩大运行授权 |
| 最终 `make verify` | **全部通过**：format、Go unit/race、architecture、migration-check、RBAC、IAM browser result gate、生成类型无漂移、Web lint、95文件/715单测、16文件/196关键覆盖率测试（statements92.26%、branches85.99%）、TypeScript/build/体积预算 |

所有外部数据库只使用本机一次性 schema；最终 owner 链路脚本创建并清理自己的两个本地容器，替身上游不读取生产凭据、不花费真实供应商额度。外部数据库 DSN 不进入仓库；无 DSN 时的 skip 不计作三库通过。

机器可读汇总见 [B1 工程证据](evidence/b1-context-time-pricing-2026-10-29.json)。本机原始门禁输出 `/tmp/b1-make-verify.log`，最终 owner 输出 `/tmp/b1-owner-final.log`。浏览器结果在 `web/a3-test-results/playwright.json`（gitignore），截图位于其 `artifacts/`。

复现命令（Go 1.27，外部 DSN 必须指向本机一次性数据库；测试自行建表/schema并清理）：

```sh
make all
go test ./domain/billing ./app/billing/internal/biz -count=1
go test ./app/billing/internal/data -run 'TestLongContext|TestPricingSnapshotReuseKeepsTransactionUsable' -count=1 -v
go test ./internal/integration -run 'Test(ServiceTierOwnerChain|LongContextTimePricingOwnerChain)' -count=1 -v
go test ./internal/server -run '^TestTier(Chat|Responses)|TestDeepSeekTier|TestServiceTier|TestResponsesExecutionCoverageAdmissionAndSendTime' -count=1
(cd web && npm run test:e2e:a3)
make verify
```

三库开关仍是 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN`，不读取部署 `.env`。首次 `make all` 曾遇模块下载 TLS 超时，重试成功；本机旧 node_modules 缺 qrcode.react，按现有 lock 安装后 build/tsc 正常，未改依赖声明/lock。完整门禁最初揭示新增源码矩阵登记、146迁移数量断言与DeepSeek计价能力查询测试预期遗漏，已补齐并最终通过；没有豁免失败门禁。

## 部署与回滚

**2026-10-30 09:08 CST 完成首轮切换，09:12 CST 最终独立复核通过。** 五个 Linux/amd64 镜像在本机按服务官方 Dockerfile 交叉构建后上传；部署标签 `b1-20261030-085841-691292280c1e`，源码摘要 `691292280c1e0f8904ecf451d4356e568d2c52c7f1d045334bf4c98521929c21`。生产备份 Billing/Log 一致性 SQL、Compose/私有配置、旧镜像及 Web 后，执行 Billing 146 和现场缺失的 Log 145（各首跑1项、重复0项），再依次更新 Billing、Log、Identity、Relay、Admin。Web 在 `/opt/web/dist` 先铺资源、最后原子替换 index，保留旧哈希资源供已打开页面使用。

五服务运行镜像 ID、镜像及 Compose 容器源码标签均与本机构建一致；`restart_count=0`、healthz 全通过、启动 error/panic/fatal 计数为0。真实运行账号对新增列的读取、零行 INSERT/UPDATE 及回滚通过；使用 Relay 现有服务身份的 Billing gRPC 能力探测通过。其余 **16 个容器 ID/镜像未变**，运行环境与计价配置 hash 未变，`ServiceTierPricing` 仍缺省关闭；MFA、微信、证据监控等原有设置未调整。

公网 `/login`、`/pricing`、`/usage` 均返回新构建 index（SHA256 `2805228752bfd70c3b4186eb090464ac02ea1796fece77160b48b7ab32ac3951`），9个入口静态资源可取，health/status 为200，未认证 options/payment channels 为401。真实浏览器匿名登录页渲染正常、控制台错误为0；本次未取得管理员会话，未冒充已完成生产登录后的配置/账单页面验收，该部分仍引用前述隔离浏览器结果。

部署验证保留 MySQL 远程 root 锁定与容器只读/noexec 设置：迁移改在现有 MySQL 容器本地 socket 执行仓库 Runner，权限检查在主机使用现有服务账号执行。初次远程连接在迁移历史 guard 处停止，未执行 SQL；一次末尾汇总脚本变量错误发生在部署完成并保存状态之后，随后按状态文件、镜像、健康和公网重新独立复核。未通过解锁账号或放宽容器保护完成部署。

脱敏机器证据：[B1 生产部署](evidence/b1-production-deployment-2026-10-30.json)。私有备份与回滚标签均为 `rollback-b1-20261030-085841-691292280c1e`，备份位于 `/opt/micro-one-api/backups/rollback-b1-20261030-085841-691292280c1e`；其中含 SQL、Compose/私有 env、Web 归档和部署状态，秘密不进入仓库。全部受影响代码和本记录在本次提交中交付，未 push 或打 tag。

配置仍须通过 Admin revision 原子入口保存，保持关闭直到维护者确认真实价格与日历并完成付费验收。A3 145 前置迁移及 Log/Relay 兼容代码现已随本次到位，但不代表 A3/B1 已启用真实计价。

回滚先关新计价请求并排空已冻结任务；保留列与历史快照，不能让旧 Billing 处理 version2/3 配置及新冻结请求。关闭不会重新计算已有账本。

## 外部保留项

- [ ] 确认长上下文阈值、整单售价、忙闲售价及采购估价；示例 null 不能启用。
- [ ] 确认目标模型/输出上限、真实服务档位语义、节假日表和有效期。
- [ ] 授权付费 JSON/SSE 调用并核对独立供应商账单；预扣时刻不等于供应商计费时刻的证明。
- [x] 2026-10-30 生产备份、实际 MySQL 迁移、Billing/Log/Relay/Identity/Admin/Web 升级与关闭态线上验收。
- [ ] 单独决定收费启用；本项默认关闭。
