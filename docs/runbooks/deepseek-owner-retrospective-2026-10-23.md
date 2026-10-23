# DeepSeek 渠道 11：owner 补证与最终性收口

日期：2026-10-23（Asia/Shanghai）。基于 `develop@4a165c4b`，按“定位执行覆盖 → 实现补证 → 核验最终性”推进。承接[闭窗记录](deepseek-channel11-closed-window-2026-10-23.md)和 [N3 计划](../design/next-stage-plan-2026-10-20.md#n3首个生产来源费用闭环)。

**已按维护者授权完成本机跨构建、Billing/前端发布及既有 IAM owner 的正式继任导入。** 生产共 2 份报告 / 2 条 run，继任 sequence=1、actor=`iam-user:1`、成功写审计 1 条，原报告/run 哈希不变。N3 仍保留历史执行覆盖边界：事后重算不补造执行事件或重新证明已脱敏掉的私有记录关联。部署及真实导入证据见第 5 节。

## 1. 执行覆盖：当前配置未协商 durable execution

在真实注册的 `POST /v1/responses` SSE 入口，以观测配置 `RELAY_EXECUTION_DELIVERY_V1=true`、空 `RELAY_NATIVE_USAGE_V2_ALLOWLIST`、未设置发送时间开关复现：HTTP 成功、结算和消费日志各一条、计时 `execution_path=legacy`，但 `reserve_delivery=0 / reserve_usage=0 / outbox_events=0`。初次以“应有 owner 事件”为断言的反馈环失败，随后用隔离配置矩阵定位准入条件。

| 隔离配置 | 协商 delivery / usage | outbox 事件 | send 时间 |
| --- | --- | --- | --- |
| 生产快照开关组合 | 0 / 0 | 0 | 无记录 |
| 空名单，仅启用发送时间 | 0 / 0 | 0 | 无记录 |
| 其他来源名单 + 发送时间 | 0 / 0 | 0 | 无记录 |
| 精确 fixture 名单，关闭发送时间 | 1 / 2 | intent/checkpoint/terminal/final 各 1 | 0 |
| 精确 fixture 名单 + 发送时间 | 1 / 2 | intent/checkpoint/terminal/final 各 1 | 正值 |

正控验证有效 payload digest、receipt 和 ACK，以及原生 Responses usage / 18 token 最终结算。另覆盖未准入 legacy 的原生 404 → Chat Completions SSE 回退，仍成功结算且不产生 owner 事件。

这证明**当前快照组合下 writer 未进入**，以及时间采集是独立条件；不将 delivery 开关等同于路径覆盖。测试的 owner 是假接收端，不证明线上 Billing 落库；已有真实 `/models` 探针说明 owner 能接收该类事件，但不能替代模型调用。生产两天全部 201 条均有 legacy 计时，历史 allowlist/时间开关未独立证明，不能把当前配置反推为历史唯一根因。

精确名单正控仅用于隔离原生 Responses。durable intent 禁止以同一意图更换协议重发，因此不能据正控自动为需要协议回退的真实来源扩大准入。本轮保持生产路径、开关与灰度。

见[回归测试](../../internal/server/execution_coverage_test.go)和[实际输出](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/execution-coverage-green.log)。

## 2. 事后原币补证：由 owner 重放算术，保留来源边界

`VendorManifest.retrospective_evidence` 承载有界输入：固定报告和完整范围、精确来源/模型/映射 revision、原始材料 SHA256 引用、完整半开调用时间区间、连续的历史费率窗口和明确提供的三类 token。缺失/null 不当零；两个未知 cache-creation 桶不加入模型。

owner 以 `big.Rat` 重算，费率最多 12 位小数、按百万 token 计算，结果最多 18 位小数且不逐条舍入。拒绝跨费率边界、区间越界、窗口缺口/重叠、来源错配、重复记录、负数、溢出、伪造结果和超过 2 MiB 的补证。落库前再次重算校验。

派生结果独立保存在 `retrospective_result`，固定：

- `basis=retrospective_tariff_recomputation`
- `provenance=operator_submitted_archive`
- `owner_arithmetic_replayed=true`
- `execution_join_verified=false`

公开归档已去掉 root/reservation ID；哈希是对外部材料的引用，不能认证其真实性或让 owner 重新验证已省略的关联。结果不进入 `VendorLocalAttempt`、冻结 USD、普通 comparisons、coverage 或用户账本，也不授予 matched、request 或 invoice 结论。

203 条真实归档的 owner 重放结果：

| 来源 | 条数 | CNY 重算 |
| --- | ---: | ---: |
| user | 201 | 2.04696096 |
| 外部 internal_test | 2 | 0.000048 |
| 合计 | 203 | **2.04700896** |
| 对原供应商金额差额 | — | **0** |

SQLite 实际存储边界先留存原报告/run，再以补证追加不可变 run：1 report / 2 runs；完全相同的重试不增加 run，旧 payload 不变。MySQL/PostgreSQL 测试入口保留，本机未提供隔离 DSN，子项跳过，不记为三库通过。没有修改 SQL、表或索引。

## 3. 最终性：维护者确认及继任版本

本轮重新从官方页面下载同一 `micro-one-api` Key、10/21–10/22、GMT+8 的导出。203 请求、30,832,706 token、2.0470089600000000 CNY 与初次材料相符。新旧 ZIP/CSV 原始字节哈希不同；经相同脱敏和无序行规范化，全部 3 行费用、11 行用量/价格内容相同，未将字节差异误报为费用更正。保留[新脱敏导出与核对](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/finality-recheck.json)。原下载路径现在是后一次导出，原 ZIP 哈希保留为历史采样身份，不声称当前文件仍是旧 ZIP。

官方页面仍仅给出可能延迟 5 分钟的提示，CSV 没有最终水位。维护者随后明确确认 **“不会再更正了”**；本账期按该确认登记为 `maintainer_confirmation / no_further_corrections`，见[确认记录](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/finality-attestation.json)。这项来源标签保留在业务模型和页面中，不标为供应商签名或其公开发布的水位。

完整性/水位/确认身份均属于报告身份。候选使用新 version、`complete=true`、`vendor_watermark=scope.end` 和 `supersedes`，保留原报告；local watermark 与 coverage 仍未知。

| 候选字段 | 值 |
| --- | --- |
| version | `deepseek-key11-20261021-22-export-20261023-v1-final-v2` |
| report ID | `aecaa5c09fe36405cd59d59eb1e8e7c4d62e6c72f04e9c67052db5cf045ed9b1` |
| supersedes | `2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db` |
| finality basis | maintainer_confirmation |
| correction delta | 0.000000000000000000 CNY |
| 正式核验状态 | pending / missing_local / amount unknown |

实际存储测试继续导入候选：2 reports / 3 runs，最新查询返回继任版本，旧报告和原 run 不变。若生产只导入最终候选，原报告的补充 run 可以不单独创建；不能把隔离测试的 2/3 数量宣称为生产现状。

## 4. 复核与后续执行

```bash
python3.11 docs/runbooks/evidence/deepseek-channel11-closed-window-2026-10-23/check.py
python3 docs/runbooks/evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/check.py
go test -race ./internal/server -run '^TestResponsesExecutionCoverageAdmissionAndSendTime$' -count=1
go test -race ./app/billing/internal/biz ./app/billing/internal/data -run 'VendorRetrospective|VendorFinality' -count=1
```

使用 Python 3.11 运行原检查器，因为历史外部调用保留 9 位纳秒，当前 macOS 的 Python 3.9 `fromisoformat` 不接受该输入。新准备/检查脚本兼容当前 Python 3.9，不修改原始调用时间。

`prepare.py --check` 核对已保存候选；可加 `--request-out /绝对路径/新文件.json` 生成 0600、仅创建的新 owner HTTP 请求。该命令不发送请求、不读取凭据、不连接生产。业务 hash 的权威实现仍为 Go，实际存储测试同时核对旧报告和候选 ID，防止准备脚本漂移。

本轮发布涉及 Billing 和宿主机前端 `web/dist`，无迁移或权限变更。已通过既有 IAM owner 导入继任报告，核对 report ID、前序链、不可变 run、actor 和审计。完全相同的最终 payload 会去重，但相同请求重新加载到新增探针时可能形成新的合法 run，不能据请求相同盲目重试。未直接写报告表、伪造 local attempts 或使用 `-local-attempts` 搭配 `-import`。

发布前需本机跨构建 Linux/amd64，生产不编译；前端须独立构建并替换宿主机挂载目录。验证失败使用 Billing rollback image 和前端备份恢复服务；已导入的审计事实保留，后续证据以追加 run 或新继任版本办理。

N3 后续真正剩余的是可核验的本地历史执行覆盖，不能用这份算术重放抹掉该边界。N4/executor 扩面继续遵守各自关卡。

## 5. 已授权生产发布与真实导入

维护者明确授权“本机跨构建并发布 Billing 和前端，再导入继任报告，完成后提交并推送”。沿用标准部署脚本，仅在本机构建 Linux/amd64；生产只加载镜像并启动服务。Billing 健康响应 `{"status":"ok"}`、restart=0，Admin 镜像/容器及价格、权限、灰度配置未改。

| 制品 / 回滚 | 实际值 |
| --- | --- |
| Billing image | `sha256:24f8d57df70f3b946eb238dd6de9e4209a00916d5cd95f8ef5816d6b5c043413` |
| Billing source digest | `5c324e059887b9788fb0924a9da3a7010d46c20696235343faaa4a03a8957f53` |
| Billing rollback | `docker-compose-billing-service:rollback-20261023-221148` |
| 最终前端 archive SHA256 | `8480e01945fb1acc3a0f09d77d0c672a4a26ae0922ca77eec14ad5a8e06a65fb` |
| 最终前端 index SHA256 | `8ed5e753828240310ecebd7bb1b08e170b3fb8e1e96f77d521aa2711d9e2d912` |
| 前端原始备份 | `/opt/web/dist.bak.n3-20261023T141509734343Z` |
| 回执修正前的备份 | `/opt/web/dist.bak.n3-20261023T143303530226Z` |

前端先校验压缩包及全部 80 个文件，再备份、复制资源，最后原子替换 `index.html`；保留宿主机挂载目录 inode 和旧 hash 资源，已有客户端仍可读取旧 chunk。公网 HTML/页面资源哈希另行校验。后续仅前端回执处理发生变化，8 个已登记 Go 输入哈希未变，Billing 保持首次已验证的镜像；两个制品分别登记实际源码 digest，没有为前端修正重复重启 Billing。

原页面只有查询/导出；本轮增加受既有 `billing.vendor_report.import` 控制的文件导入入口：2 MiB 上限、严格四字段请求、UTF-8 预览、实时权限门、撤权清稿和同步提交锁。不读取或导出用户 token，不使用系统 ADMIN_TOKEN 绕过 owner。使用当前真实登录会话，在预览核对后只提交一次已准备的继任请求。

**22:20:26 CST 已落库**，独立一致性只读事务核验：

| 字段 | 生产结果 |
| --- | --- |
| reports / runs | **2 / 2** |
| 继任 report ID | `aecaa5c09fe36405cd59d59eb1e8e7c4d62e6c72f04e9c67052db5cf045ed9b1` |
| 继任 run ID / payload SHA256 | `346bb2e1b9a4ba4582b3a8af79496f4ce57ff617efa7cd0680f41b56e92f4b5d` |
| sequence / actor | **1 / iam-user:1** |
| 成功写审计 | billing.vendor_report.import，actor 1，**1 条**，原因 SHA 和 decision versions 相符 |
| 原 report/run SHA256 | 保持 `6b74a1c12409e0321f31688c18228d7e265a07a1ccc8987f5c5217a3445be45a` |
| owner 归档算术 | **203 条，2.04700896 CNY，差额 0** |
| 正式状态 | **pending / missing_local / amount unknown / verification_scope none** |
| unknown_time_attempts / user_reservations | **2,615 / 0**；前者是来源全历史 unresolved 探针，不是本账期请求数 |

浏览器提交后未观察到明确成功回执，草稿曾重新可提交；没有重复 POST。先以 owner 表和审计确认真实保存，再重载页面，只读回显继任版本、维护者最终性、原币算术及主核验边界。没有证据将该现象具体归因为 HTTP timeout。

以“保存后无响应/503、超时、权限后台刷新”的准确调用模式先建立红灯，再修正前端：无响应、abort、timeout、5xx 或缺失/不匹配回执归为结果不确定，消费已提交草稿，提示核对预期 ID，并在权限恢复 idle 后执行一次只读回查；已知 4xx 校验拒绝保留草稿与错误。不自动重试 POST、不延长原 HTTP/RPC timeout。34 项前端专项、类型检查、lint、构建及六项资源预算通过，并已单独发布该修正，未再次导入。

证据：[部署结果](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/deployment-result.json)、[落库与审计](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/owner-persistence-verification.json)、[导入摘要](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/owner-import-result.json)、[前端制品/验证](evidence/deepseek-channel11-closed-window-2026-10-23/retrospective-owner/frontend-artifact.json)。实现及证据一并提交到 develop，不发布新 tag。
