# A3：服务档位计价实施验收

实施开始：2026-10-27；收尾验收：2026-10-28（Asia/Shanghai）。关联[排期](../design/next-stage-plan-2026-10-26.md) / [冻结契约](../design/a3-service-tier-contract-2026-10-27.md)。

**状态：完成（实现、三库链路、浏览器专项与 Go 门禁全部通过；前端单测门禁在本机存在 4 例既有超时，已在基线 HEAD 复现，非本项引入）。** 默认计价关闭；未部署生产、未绑定真实收费来源、未进行付费探测、未发布版本/tag。真实来源/项目档位权限、供应商价格和收费启用分别保留外部验收，不以替身上游结果替代。本次已提交并推送 `develop`（未提交任何生产凭据或真实渠道 ID）。

2026-10-28 A4 部署补记：Billing/Admin/Identity/Web 已随 A4 更新，Billing 144 号兼容字段迁移已落库；Relay/Log 未更新、Log 145 号未迁移，档位计价仍关闭，尚未完成 A3 端到端生产上线。本段不改变原验收时“未部署”的历史记录，见 [A4 线上证据](evidence/a4-production-deployment-2026-10-28.json)。

2026-10-30 B1 部署补记：Billing/Log/Relay/Identity/Admin/Web 已完成本机 Linux/amd64 构建后的生产更新，补齐 Log 145，并新增 Billing 146。A3 相关服务兼容代码和字段现已到位，健康、运行账号权限、gRPC 能力、公网前端和未认证拒绝分支通过；档位及 B1 计价配置仍缺省关闭，真实售价/采购价/付费探测与收费启用继续保留。详见 [B1 线上证据](evidence/b1-production-deployment-2026-10-30.json)。本次没有开启 A3 或扩大既有契约灰度。

## 1. 已交付实现

| 子项 | 实现 |
| --- | --- |
| A3.1 | 已有 OpenAI channel adapter、单个精确来源/模型；省略/auto/default/priority/flex；无隐式升档；`actual_or_max_v1` 缺失策略与输入/输出成本上界 |
| A3.2 | Chat Completions / Responses 的 HTTP JSON/SSE；请求与实际上游档位分开；逐次重试重新预扣；Billing 能力+预扣回显双门；Log 三层及生成契约 |
| A3.3 | 三档五桶售价与可选采购估价；reserve-time RequestSnapshot 冻结；PricingSnapshot v2；ledger 六个解释字段；CAS/同事务/幂等重放；历史账单不重算 |
| A3.4 | Admin 独立配置原子保存、revision 冲突/读回锁；售价与采购双权限；管理端和用户自助账单中英文解释；三库迁移 144/145、所有权与候选升级顺序 |

首期只接纳有明确输出上限的自包含纯文本。Chat 使用 `max_completion_tokens`，Responses 使用 `max_output_tokens`。图片、音频、工具、文件、外部会话状态、后台任务、多份生成不进入档位计价；executor、WS 及其他来源的支持/拒绝矩阵见冻结契约。

成功消费沿用既有 v1 最低收费 **1 定点单位（USD 0.0001）**，包括完整零价或已验证全零 usage；配置中的零单价不等于取消此最低额。采购估价没有该用户最低额：已知零成本为 `priced/0`，缺实际上游档位或采购价/usage 证据为 `unpriced/0`，两者不能混用。

## 2. 验收证据

| 验证 | 结果与边界 |
| --- | --- |
| 三库真实 owner 链路 | SQLite、MySQL 8.4、PostgreSQL 16；真实 Relay HTTP → 生成的 Billing/Log gRPC → 真实 SQL；Priority/Flex/默认回落、实际缺失/未知、JSON/SSE、执行中改售价/采购价、重复结算通过。所有数据库为本机一次性 schema，完整迁移树首跑+重复 no-op |
| 金额与快照专项 | 五桶独立最高值、输入/输出上界、改价冻结、跨来源/档位重放冲突、未知/冲突按 max、旧 durable JSON 摘要、最低额与零采购价语义通过 |
| PostgreSQL 快照重用 | 已存在 hash → 同事务重复 claim → 同事务再插新 hash → 提交后两个快照，三库通过。使用 `ON CONFLICT DO NOTHING`，不再吞掉使 PostgreSQL 事务失效的唯一键错误 |
| Relay 与 Log 专项 | 完整受影响包通过；SSE 分段、缺 usage/终态、取消/断流释放预扣并保留实际档位与零 quota 日志；未覆盖协议转发前拒绝 |
| 采纳准入错误语义 | Billing 的 `SERVICE_TIER_UNSUPPORTED`/`SERVICE_TIER_BOUND_REQUIRED` 在 Relay 侧固定为 **400**（响应点名 `service_tier`），不再误报 402「insufficient quota」；即使重试策略把 400 视为可重试也**只预扣一次**，且不计入来源健康。Chat/Responses/Raw/executor 四个出口一致。旧 Billing 桩因此补齐 `GetRoutingCapabilities.service_tier_pricing_version=1` 与预扣回显 |
| Admin/Config 权限 | 全包及新专项通过；缺任一全局售价/采购权限、scoped 权限、旧 revision、错误价格、非事务 fallback 均拒绝；受限对象显示不可读；Config 独立表拒绝同名无效配置 |
| Web / 实际 Chrome | 专项 47 项测试、build/budget 通过；桌面与 Pixel 5 两项浏览器验收通过（`expected=2, unexpected=0, flaky=0`），验证配置原子保存/读回、管理账单与自助账单、未知实际档位。API 为本地 fixture，不启用真实计价 |
| `make all` | API/config/Wire/模块依赖和 Web API 类型已重新生成 |
| 最终 `make verify` | Go 侧全部通过：format、unit、race、architecture、migration-check、rbac-contract-check（907 行源码矩阵已随 A3 同步摘要与评审说明）、IAM browser gate、生成类型无漂移。**前端单测门禁未通过**：`npm test --run` 4 例超时（`router.test.tsx` 2、`AppNavigation.test.tsx` 1、`PlaygroundPage.test.tsx` 1），与本项无关——同一台机器在未含 A3 改动的基线 `HEAD d4c95572` 上复现同样的 4 例（92 文件 630 例，`4 failed | 626 passed`），单独运行这些文件均通过，属本机 jsdom 初始化过慢导致的 1s 默认等待超时。前端 `lint`、`build`（含预算）通过；`test:critical` 仅 `PlaygroundPage` 2 例同类超时 |

机器可读最终汇总保存在 [`evidence/a3-engineering-acceptance-2026-10-28.json`](evidence/a3-engineering-acceptance-2026-10-28.json)，含各门禁的命令与结果、前端基线对照。浏览器原始结果为本机 `web/a3-test-results/playwright.json`（已 gitignore），含桌面/移动端截图；临时验收输出为 `/tmp/a3-*.log`，不包含生产凭据。三库验收使用本机一次性 schema（MySQL 8.4 / PostgreSQL 16 容器），DSN 仅指向 `127.0.0.1`。

复现（Go 1.27；外部数据库 DSN 仅允许本机一次性数据库）：

```sh
make all
go test ./domain/billing ./app/billing/internal/biz ./app/billing/internal/data \
  -run 'Test(ServiceTier|MaxTier|LegacySettlementJSON|PricingSnapshotReuse)' -count=1
go test ./internal/server ./domain/upstream/provider ./app/log/internal/biz \
  ./app/log/internal/data ./app/log/internal/service -count=1
go test ./internal/integration -run '^TestServiceTierOwnerChain$' -count=1 -v
go test ./app/identity/internal/server -run '^TestSelfLedgerServiceTierExplanation$' -count=1
(cd web && npm run test:e2e:a3)
make verify
```

`TestServiceTierOwnerChain` 和快照重用测试始终执行 SQLite；设置 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN` 后执行另外两库，各子测试自动创建、迁移、删除隔离 schema。没有 DSN 时的 skip 不能当成三库通过。

`make verify` 的生成类型 gate 比较 Git index；本次使用临时 `GIT_INDEX_FILE`，只将有意更新的 `web/src/types/api.ts` 纳入临时比较基准，保留实际暂存区。复现时也可在自己的候选 index 中纳入该文件，再验证重生成无漂移。

## 3. 配置、升级和回滚

1. 备份 Billing/Log 数据和 Admin `system_options`；核对运行账号对新增字段的现有表级权限。迁移 144 只修改 Billing 的 ledger/snapshot，145 只修改 Log，无新凭据或新表授权。
2. 按实际数据库执行对应 `144_add_billing_service_tier.sql` / `145_add_log_service_tier.sql`，分别遵守 Billing/Log ownership。SQLite/PostgreSQL 使用镜像目录。
3. **先升级全部 Billing/Log，再升级 Relay，最后 Admin、Identity 和 Web**。Identity 更新只增加自助账单字段映射。Relay 要求 Billing `service_tier_pricing_version=1` 和 reserve `service_tier_contract_version=1`；混入旧 Billing 时拒绝，不能忽略新字段继续收费。
4. 配置保持 `{"version":1,"enabled":false}`。启用精确范围前打开 `BILLING_REQUEST_SNAPSHOT_V2=true`，完成真实模型、输出上限、项目档位权限及三档售价/采购价审阅。通过 Admin 的带 revision 原子入口设置唯一 `ServiceTierPricing` 对象；独立 Config 表不生效且拒绝此写入。
5. 单独授权一次真实付费探测、核对 response 实际档位与供应商账单，然后决定收费启用与扩面。现有 1% 契约试点、停用的证据监控和独立生产验收均不因本次候选而变更。

若后续部署，Linux/amd64 镜像仍本机/CI 交叉构建；受影响服务为 Billing、Log、Relay、Admin，以及自助账单映射的 Identity，Web 按宿主机 dist 独立发布。没有新增 Wire 依赖，也不重建无关服务。

回滚先关闭档位配置并停止新增档位请求，排空已接纳的 reservation/异步结算。已冻结请求仍使用原始配置结清；不能回滚到无法解释该快照的旧 Billing 接管未完成任务。保留新增列、快照和历史金额；关闭配置不会重新定价已有账本。

## 4. 外部验收保留项

- [ ] 指定真实 OpenAI channel/模型；核对模型生命周期、项目允许档位和真实响应语义。候选示例不等于生产绑定。
- [ ] 审阅真实三档售价/采购价、最低额、未知实际档位按最高价策略以及 Flex 超时边界。
- [ ] 授权付费 HTTP JSON/SSE 探测并取得供应商实扣证据，分别核对售价和采购估价。
- [ ] 生产备份、迁移、全 Billing/Log 升级、Relay/Admin/Identity/Web 部署与线上验收。
- [ ] 真实收费启用与扩面；本次保持关闭，不发布版本/tag。
