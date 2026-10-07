# 第一批 S1–S3 代码审查与修复

> 2026-10-07（Asia/Shanghai）· 审查范围：`67ba46d1..1a325310`，包含代码、工作流、测试与验收记录。
> 本次发现的五项问题/差异已修复并本地复验；没有推送、部署、修改生产事实或重采生产。此前 Billing 对账恢复仍待上线。
>
> 后续上线（2026-10-07 09:49–09:51 CST）：维护者另行授权后已更新 Billing，启动轮 #365 completed/0 差异，见 [v0.34.5 证据](../runbooks/evidence/patch-v0.34.5-production-2026-10-07.json)。本审查“未部署”描述保留当时边界。

## 发现与处理

| ID / 优先级 | 触发、根因与影响 | 修复及证据 |
| --- | --- | --- |
| R1 / P1 | Go test 通过且 Playwright `expected>0` 即接受，汇总 `expected` 还包含“预期失败”。实际在 CI 模式临时加入 `test.only` 后只执行两项仍 PASS；缩减报告或把一项改为 expected failure 也被接受，角色矩阵可被静默漏验 | 禁止 `.only`；增加受审场景契约，核对完整标题集合、spec、project、每项 `expectedStatus=passed` 及实际 results 全部 passed，并检查整数汇总与完整数量。当前十项受审场景全部执行通过；实际 `.only` 重试被 forbidOnly 拒绝 |
| R2 / P1 | `while ... < <(git diff ...)` 不传播 process substitution 的失败。实际将 diff 模拟为退出 128，分类器仍退出 0、any=false，缺失 base/head 时可能跳过全部专项 | 在循环前独立检查 diff 命令的退出状态；失败不生成成功的 suite-selection 输出。对实际 workflow shell 的失败用例及 16 类依赖/文档路径验证通过；新增契约与测试脚本也纳入触发范围 |
| R3 / P2 | IAM 和通用 smoke 共用 `web/test-results`。实际先运行 IAM 再运行一项通用 smoke，IAM Go 报告被删除；报告、截图/trace 不能按文档稳定保留 | 每次 IAM 验收使用独立 `web/iam-test-results/run.XXXXXX`；Go/PW JSON 与 screenshot/trace 同属该次目录，workflow 只上传 IAM 目录，Git 与 Docker 忽略该目录。通用 smoke 后七个 IAM 文件及 SHA-256 全部保持不变，结果 checker 仍通过 |
| R4 / P2 | 首轮测试只确认无会话外部对账拒绝；订阅撤销主要从 HTTP 被拒绝，未直接复验 Billing owner；目录只读详情依赖组件 mock。证据不足以单独支撑这三项 owner/角色边界声明 | 增加有效外部对账操作者的“有运行权/无明细权”“运行+明细权”“只有明细权”隔离回归；增加有效会话的 Billing 直接本人/异用户/密码撤销 RPC；增加 catalog_reader 真角色/真浏览器，详情可见、无引用请求、有效 CAS 写入 403 且元数据不变。全通过 |
| R5 / P2 | 生产 evidence 的 `least_privilege_owner_channel` 由 SQL 账号非空派生，名称暗示已证明实际最小权限；S1 实际核对的是 DSN/非 root 配置，未重新执行数据库 grant 探针 | 使用首轮保存的原始脱敏账号快照明确判断非空且非 root，字段改为 `non_root_sql_account_configured`，并标注未重验实际 grants。保留 D1 的原数据库权限探针，不改历史窗口/业务数据或制造新生产证据 |

受审浏览器集合在 `scripts/iam-browser-contract.json`。新增、删除、重命名场景或切换浏览器项目时须同时审查契约，不能只修改数字使精简矩阵通过。

## 验证

```bash
make iam-browser-gate-check
make test-iam-browser
go test -race ./app/billing/internal/biz ./internal/integration \
  -run 'TestReconciliationExternalRunKeepsIndependentIssuePermission|TestReconciliationJobIAMBackgroundPersistsWithoutUserSession|TestIAMSelfSubscriptionProgressThroughRealOwners' -count=1
make verify
go test -race ./internal/integration ./app/billing/... -count=1
```

- `iam-browser-gate-check` 的十二项回归通过：完整矩阵、精简/缺失/重复/替换场景、预期失败与实际失败、错误 spec/project、Go skip/fail/缺失、包失败、异常计数，以及路径分类失败/依赖触发。此快速目标接入本地 verify 和后端 CI。
- 十项真实 HTTP → gRPC → biz/data → SQLite 浏览器验收通过，实际执行结果均 passed、skipped=0。新增 catalog_reader 只有目录列表与 console 权限；元数据写入使用实际 policy/target CAS，不以无效请求的 400 冒充权限拒绝。
- 临时 `.only` 的修复前实际 CI-mode 命令返回 0、执行两项；修复后返回非零且说明 forbidOnly。临时改动已恢复，未提交聚焦测试。
- 通用 smoke 的一项代表性用例复跑通过，之后 IAM 全部文件及摘要保持，Go/PW checker 仍通过。首轮 67 passed/1 个既定 mobile-only skip 的完整通用 smoke 记录保留，没有把该单项复跑记作本轮完整 smoke。
- 本轮 `make verify`、完整 Billing/集成 race、工作流 actionlint、格式与文档链接检查通过；前端仍为 215 个单元测试。MySQL/PostgreSQL opt-in 和远端新增 job 未重跑，不将 SQLite/本地证据扩大为三库或 GitHub 运行证明。

复验机器结果见 [审查证据](../runbooks/evidence/iam-first-batch-review-2026-10-07.json)。首轮 [本地记录](../runbooks/evidence/iam-first-batch-local-2026-10-07.json) 的九项统计保留为 `1a325310` 历史结果，当前实现以本审查的十项矩阵为准。

## 发布边界

本次主要改变验收门禁、测试夹具与证据表述，没有新增生产业务逻辑、API、权限 grant、迁移或运行时配置。原对账业务修复仍在同一分支，生产恢复步骤继续按 [第一批记录](../runbooks/iam-first-batch-acceptance-2026-10-07.md#s1-补缺定时对账的-iam-会话要求) 执行；当前不声称线上对账已经恢复。
