# P1 生产部署与开关验收

2026-10-13，按 Neo「更新线上环境，新开关打开」指令部署。运行代码为 `ea8078d4`，构建源码摘要 `f64ca45ebde6aac796639bed0d757f60086c493ed52c1fee596118e315605450`。四个服务均在本机跨构建为 linux/amd64，生产主机只加载镜像；前端独立更新宿主机挂载目录。未创建版本标签。

关联：[工程交付](billing-upstream-p1-completion-2026-10-13.md)、[部署证据](evidence/billing-upstream-p1-deploy-2026-10-13.json)、[计费方案](../design/billing-accuracy-and-upstream-evidence-plan-2026-10-13.md)、[上游方案](../design/upstream-contract-evolution-plan-2026-10-13.md)。本记录只包含本次修复及部署证据，不包含无关自动观察文件。

## 制品与生产配置

镜像统一标签：`p1-20261013-ea8078d4`。

| 服务 | 镜像 ID（SHA256） |
|---|---|
| billing-service | `4ed4a0a55ba03c809430e3107787f4d1e1f8338040746b92a00641b71d400cfa` |
| channel-service | `d5e97be38e0278ad369ee76ebb1bd44ae06541e814bfe624a3a11e8de744fed5` |
| monitor-worker | `8d62423317c2a17f36465c640112d016c1a679992cd85ef669c1b7bcbccc3a8c` |
| relay-gateway | `063047a7b660d094baafe86c48f64e6b121d2957f1b06dac67f910773a66f970` |

`web` TypeScript/Vite 构建通过，发布到 `/opt/web/dist`；admin-api 继续使用原镜像。宿主机与本地 index.html SHA256 一致（`f03808768a53a8734c0042ba368d1e2c47b6f72bf936adcde21542e1a76eaf1c`）。

| 开关 | 生产值 |
|---|---|
| `BILLING_EXECUTION_DELIVERY_V1` | `true` |
| `BILLING_SETTLEMENT_RECEIPTS_V1` | `true` |
| `RELAY_EXECUTION_DELIVERY_V1` | `true` |
| `UPSTREAM_PROBES_ENABLED` | relay、monitor、channel 均为 `true` |
| `RELAY_USAGE_SHADOW` | `true` |
| `RELAY_NATIVE_USAGE_V2_ALLOWLIST` | 空；本次没有验证通过的供应商模型，不放行新收费政策 |

新增 monitor→relay 与 relay→monitor 专用调用身份映射，复用现有独立身份令牌；未扩大 shared token 权限。保留原有调用者。原 canonical/cache charge 配置和 `5:k3` 名单没有作为本次 native v2 名单使用。

relay 独立持久目录为 `docker-compose/data/relay-outbox`，挂载到 `/var/lib/relay-outbox`；目录 0700、数据库 0600，归属 65534:65534。WAL 文件保留，重新创建容器后仍可读取 ACK 记录。

## 迁移与现场修复

已应用 billing 126/131、channel 127/130、monitor 128，均记录 `schema_migrations`。生产数据库实行逐表权限，运行身份没有 DDL 权限；通过现有 MySQL 本地 socket 管理通道执行迁移，仅给对应运行身份增加七张所属新表的 SELECT/INSERT/UPDATE/DELETE，未解锁 root TCP 或授予全库权限。

生产旧 `billing_reservations.reservation_id` 为 `utf8mb4_0900_ai_ci`，新表继承数据库 `utf8mb4_unicode_ci`，到期查询出现 MySQL 1267。`131` 动态读取权威 reservation 字符集/排序规则，仅对齐新 intent 的引用列。修复前后执行相同到期查询，错误消失；旧 reservation/settlement 主键和金额没有修改。SQLite/Postgres 镜像迁移为 no-op。

本地 MySQL 8.4 临时容器完成 fresh/repeat/status/negative/metadata 迁移 smoke；新增回归分别复现 0900/general 与 unicode 的冲突，运行正式 migration runner 修复，验证查询及重复执行。SQLite 生命周期和 ownership 检查通过。回归输出见 [MySQL](evidence/billing-upstream-p1-deploy-2026-10-13-mysql.txt) 和 [amd64](evidence/billing-upstream-p1-deploy-2026-10-13-amd64.txt)。131 为部署兼容性 SQL 修复，四个运行二进制仍对应上述构建摘要。

启动时生产 Compose 缺少实际 outbox 挂载，relay 在只读文件系统上启动失败；补齐挂载并准备目录后恢复，最终四个服务重启计数均为 0。期间六条 `probe_unavailable` attempt 均未认领许可，且当时采购 intent/evidence 为 0，证明没有供应商调用。保留 attempt 记录并对这六条预留预算做一次有审计的补偿；已认领许可的实际探测失败仍消耗预算。

## 现场验证与收费范围

消费者能力协商返回 snapshot v2、settlement receipt v1、execution delivery v1、usage contract v2，以及 `native-v2-release-unproven-2026-10-13`。通过 monitor 独立身份直接调用未预留 attempt，返回 `probe budget permit denied`，确认无预算调用被拒绝。

渠道 1 配置库存、`qwen3.8-flash` JSON/SSE 三个任务；同来源共享每日 calls=6、input=768、output=96、unknown calls=6，单次请求最多 16 输出 token；未知供应商金额保持 unknown。两轮共六次实际探测后预算耗尽：库存响应不能解析，四次推理均 `contract_drift/unsupported/unknown_usage_field`。原生响应同时包含 Chat/Responses 计数别名和未支持的 text token 明细；响应报告输出 25–35，说明上游返回的实际计量也不能仅由请求 max_tokens 证明上界。

该轮产生 21 条内部 intent/checkpoint/terminal 事件，全部 ACK、pending=0；billing 中 purpose 仅为 canary/health_probe，不含用户 reservation。L2 incident 保存四次推理漂移。没有把未知 usage 转成 verified 或用户扣费依据。

Neo 随后新增渠道 10（DeepSeek 官方接口）；先配置独立库存探测，每日 calls=3、input=384、output=48、unknown calls=3。库存与用户授权 Models 分开；新增候选不自动公开、改映射或发布价格。两次库存调用均 HTTP 403（upstream_rejected），未获得库存；渠道授权 Models 仍为空。已向 Neo 请求接口权限及精确模型信息。最终 `RELAY_NATIVE_USAGE_V2_ALLOWLIST` 留空，`UPSTREAM_PROBE_INTERVAL=5m`；原收费路径继续工作。总计 27 条内部事件均已 ACK。

四个更新服务及 admin-api 的 `/healthz` 均返回 200。本机 linux/amd64 离线容器中 execution/native evidence/outbox 聚焦回归通过。未为验收制造真实用户消费；新收费灰度费用差异、P0 完整生产窗口、供应商独立账单仍待独立验收。

## 备份、证据与回滚

- 配置和三个所属数据库完整备份：`docker-compose/backups/p1-20261013-194547/`，目录 0700、文件 0600。
- 四个原镜像保留 `rollback-p1-20261013-201258` 标签；前端备份 `/opt/web/dist.bak.p1-20261013-201307`。
- 私有原始证据位于 `/Users/neo/.codex/evidence/billing-upstream-p1-deploy-20261013/`，目录 0700、文件 0600；Git 中仅放脱敏汇总和回归结果。临时验证身份文件在验收后删除。
- 回滚先停止新收费 admit 与探测，保留新消费者及 outbox 排空；不要删持久卷/新表或回退到无法收取在途证据的旧消费者。配置备份含秘密，只在受控主机恢复。

单次库存/usage 验证不等于供应商费用已核验；布尔开关开启也不等于所有来源进入新收费政策。
